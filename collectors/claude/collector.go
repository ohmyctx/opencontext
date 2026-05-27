// Package claude implements a collector that watches Claude Code session files
// and emits ActivityEvents for each user message.
//
// Claude Code stores sessions as JSONL files under:
//
//	~/.claude/projects/<project-hash>/<session-id>.jsonl
//
// Each line is a JSON object. Lines with "type":"user" carry user messages.
// The project hash is the absolute working directory path with "/" replaced by "-".
//
// Collector behaviour:
//   - Polls ~/.claude/projects/ every PollInterval for new/modified JSONL files
//   - Tracks read offsets per file in a state file to avoid duplicate events
//   - Skips empty user messages (tool-call reply stubs inserted by Claude Code)
//   - Pushes events via the contextd HTTP API (non-blocking, tolerates unavailability)
package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/yetanotherai/opencontext/pkg/client"
	"github.com/yetanotherai/opencontext/pkg/event"
)

const (
	DefaultPollInterval = 3 * time.Second
	// MinMessageLen is the minimum character count to emit as an event.
	// Shorter messages are usually empty tool-call stubs or noise.
	MinMessageLen = 5
)

// Config holds collector configuration.
type Config struct {
	// ClaudeProjectsDir is the root of Claude Code's project sessions.
	// Defaults to ~/.claude/projects.
	ClaudeProjectsDir string

	// StateFile tracks read offsets. Defaults to ~/.opencontext/collectors/claude/state.json.
	StateFile string

	// DaemonURL is the contextd base URL.
	DaemonURL string

	// PollInterval controls how often session files are checked.
	PollInterval time.Duration

	// Sensitivity is the event sensitivity level pushed to contextd.
	// L2 is required to store actual message text; L1 stores length only.
	Sensitivity event.SensitivityLevel
}

// DefaultConfig returns a config populated from standard paths.
func DefaultConfig() Config {
	home, _ := os.UserHomeDir()
	return Config{
		ClaudeProjectsDir: filepath.Join(home, ".claude", "projects"),
		StateFile:         filepath.Join(home, ".opencontext", "collectors", "claude", "state.json"),
		DaemonURL:         "http://localhost:6060",
		PollInterval:      DefaultPollInterval,
		Sensitivity:       event.SensitivityL2,
	}
}

// Collector watches Claude Code session files and pushes user message events.
type Collector struct {
	cfg    Config
	client *client.Client
	log    *slog.Logger

	mu      sync.Mutex
	offsets map[string]int64 // file path → last read byte offset
}

// New creates a Collector. Call Run() to start watching.
func New(cfg Config, log *slog.Logger) *Collector {
	return &Collector{
		cfg:     cfg,
		client:  client.New(cfg.DaemonURL),
		log:     log,
		offsets: map[string]int64{},
	}
}

// Run watches Claude Code session files until ctx is cancelled.
func (c *Collector) Run(ctx context.Context) error {
	if err := c.loadState(); err != nil {
		c.log.Warn("could not load state (starting fresh)", "err", err)
	}

	c.log.Info("claude collector started",
		"projects_dir", c.cfg.ClaudeProjectsDir,
		"poll_interval", c.cfg.PollInterval)

	ticker := time.NewTicker(c.cfg.PollInterval)
	defer ticker.Stop()

	// Do an initial scan immediately.
	c.scan(ctx)

	for {
		select {
		case <-ticker.C:
			c.scan(ctx)
		case <-ctx.Done():
			c.log.Info("claude collector stopping")
			return c.saveState()
		}
	}
}

// isTestSession returns true for Claude Code's internal test directories,
// which generate hundreds of synthetic messages that pollute the event stream.
func isTestSession(path string) bool {
	dir := filepath.Base(filepath.Dir(path))
	// Claude Code test suite uses dirs like:
	//   -tmp-TestP0-..., -tmp-TestE2E-..., -tmp-TestP2-...
	for _, prefix := range []string{"-tmp-Test", "-tmp-test"} {
		if strings.HasPrefix(dir, prefix) {
			return true
		}
	}
	return false
}

// scan discovers all JSONL session files and processes new lines.
func (c *Collector) scan(ctx context.Context) {
	pattern := filepath.Join(c.cfg.ClaudeProjectsDir, "*", "*.jsonl")
	files, err := filepath.Glob(pattern)
	if err != nil || len(files) == 0 {
		return
	}

	var batch []*event.ActivityEvent

	for _, path := range files {
		if isTestSession(path) {
			continue
		}
		events, err := c.processFile(path)
		if err != nil {
			c.log.Debug("process file error", "file", path, "err", err)
			continue
		}
		batch = append(batch, events...)
	}

	if len(batch) == 0 {
		return
	}

	pushCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	resp, err := c.client.PushBatch(pushCtx, batch)
	if err != nil {
		c.log.Debug("push failed (contextd unavailable?)", "err", err)
		return
	}

	c.log.Info("pushed claude messages", "accepted", resp.Accepted)

	// Persist offsets after a successful push so we don't re-push on restart.
	_ = c.saveState()
}

// processFile reads new lines from a JSONL session file since the last offset.
func (c *Collector) processFile(path string) ([]*event.ActivityEvent, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	offset := c.offsets[path]
	c.mu.Unlock()

	// Nothing new.
	if info.Size() <= offset {
		return nil, nil
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	if offset > 0 {
		if _, err := f.Seek(offset, 0); err != nil {
			return nil, err
		}
	}

	sessionID := sessionIDFromPath(path)
	project := projectFromPath(path)

	var events []*event.ActivityEvent
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1 MB max line

	var newOffset int64 = offset
	for scanner.Scan() {
		line := scanner.Bytes()
		newOffset += int64(len(line)) + 1 // +1 for newline

		e := c.parseLine(line, path, sessionID, project)
		if e != nil {
			events = append(events, e)
		}
	}
	if err := scanner.Err(); err != nil {
		return events, err
	}

	c.mu.Lock()
	c.offsets[path] = newOffset
	c.mu.Unlock()

	return events, nil
}

// claudeLine is the shape of a line in a Claude Code JSONL session file.
// Claude Code includes timestamp, session_id, and cwd on every entry.
type claudeLine struct {
	Type      string         `json:"type"`
	Message   *claudeMessage `json:"message"`
	Timestamp string         `json:"timestamp"` // RFC3339, e.g. "2026-05-25T07:21:37.599Z"
	SessionID string         `json:"sessionId"`
	Cwd       string         `json:"cwd"`
}

type claudeMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// parseLine extracts a user message event from a JSONL line, or returns nil.
func (c *Collector) parseLine(raw []byte, filePath, sessionID, project string) *event.ActivityEvent {
	var line claudeLine
	if err := json.Unmarshal(raw, &line); err != nil {
		return nil
	}

	if line.Type != "user" || line.Message == nil || line.Message.Role != "user" {
		return nil
	}

	text := extractText(line.Message.Content)
	if len([]rune(text)) < MinMessageLen {
		return nil // skip empty / tool-call stubs
	}

	// Use the actual message timestamp from the JSONL file.
	ts := time.Now().UnixMilli()
	if line.Timestamp != "" {
		if t, err := time.Parse(time.RFC3339Nano, line.Timestamp); err == nil {
			ts = t.UnixMilli()
		}
	}

	// Prefer cwd-derived project (more accurate than path hash heuristics).
	if line.Cwd != "" {
		project = projectFromCwd(line.Cwd)
	}
	if line.SessionID != "" {
		sessionID = line.SessionID
	}

	labels := map[string]string{
		"session_id": sessionID,
	}
	if project != "" {
		labels["project"] = project
	}

	payload := map[string]any{
		"message_len":  len([]rune(text)),
		"session_file": filePath,
	}
	if c.cfg.Sensitivity >= event.SensitivityL2 {
		payload["message"] = text
	}

	return &event.ActivityEvent{
		ID:          uuid.Must(uuid.NewV7()).String(),
		Ts:          ts,
		Source:      event.SourceClaude,
		Type:        event.EventTypeUserMessage,
		Sensitivity: c.cfg.Sensitivity,
		Labels:      labels,
		Payload:     payload,
	}
}

// projectFromCwd derives a project name by walking up from cwd to find .git.
func projectFromCwd(cwd string) string {
	if cwd == "" {
		return ""
	}
	dir := cwd
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return filepath.Base(dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return filepath.Base(cwd)
}

// extractText pulls plain text out of a Claude content field.
// Content can be a plain string or an array of content blocks.
func extractText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	// Try string first.
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}

	// Try array of content blocks: [{type: "text", text: "..."}, ...]
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		var parts []string
		for _, b := range blocks {
			if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
				parts = append(parts, strings.TrimSpace(b.Text))
			}
		}
		return strings.Join(parts, "\n")
	}

	return ""
}

// ── state persistence ─────────────────────────────────────────────────────────

func (c *Collector) loadState() error {
	data, err := os.ReadFile(c.cfg.StateFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	return json.Unmarshal(data, &c.offsets)
}

func (c *Collector) saveState() error {
	c.mu.Lock()
	data, err := json.Marshal(c.offsets)
	c.mu.Unlock()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(c.cfg.StateFile), 0o755); err != nil {
		return err
	}
	return os.WriteFile(c.cfg.StateFile, data, 0o644)
}

// ── path helpers ──────────────────────────────────────────────────────────────

// sessionIDFromPath extracts the session UUID from a path like:
//
//	~/.claude/projects/-root-code-opencontext/8478ea2f-d285-4bfc-92eb-0e5eb948e8fb.jsonl
func sessionIDFromPath(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, ".jsonl")
}

// projectFromPath derives a human-readable project name from a path like:
//
//	~/.claude/projects/-root-code-opencontext/session.jsonl
//	→ "opencontext"
func projectFromPath(path string) string {
	dir := filepath.Base(filepath.Dir(path))

	// The dir is the absolute path with "/" replaced by "-", with a leading "-".
	// e.g. "-root-code-opencontext" → "/root/code/opencontext" → "opencontext"
	dir = strings.TrimPrefix(dir, "-")

	// Take the last segment after splitting on "-" — but we need to be careful
	// since project names themselves can contain hyphens. Heuristic: the last
	// non-empty segment that doesn't look like a path component (root, home, etc.).
	parts := strings.Split(dir, "-")
	for i := len(parts) - 1; i >= 0; i-- {
		p := parts[i]
		if p != "" && p != "root" && p != "home" && p != "code" && p != "src" && p != "projects" {
			return p
		}
	}

	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return dir
}
