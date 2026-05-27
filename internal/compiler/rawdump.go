package compiler

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/opencontext/opencontext/internal/injector"
	"github.com/opencontext/opencontext/internal/store"
	"github.com/opencontext/opencontext/internal/subscription"
	"github.com/opencontext/opencontext/pkg/event"
)

// RawDumpRunner writes recent raw events directly to a memory.md file without
// LLM summarization. The agent reading the file (Claude Code, Cursor, etc.) is
// already an LLM and can interpret the structured events directly.
//
// This is the default, zero-config memory backend. No API keys required.
type RawDumpRunner struct {
	store *store.Store
	log   *slog.Logger
}

// NewRawDumpRunner creates a RawDumpRunner.
func NewRawDumpRunner(s *store.Store, log *slog.Logger) *RawDumpRunner {
	return &RawDumpRunner{store: s, log: log}
}

// Run queries recent events for the subscription and writes them as markdown.
func (r *RawDumpRunner) Run(ctx context.Context, sub *subscription.Subscription) error {
	if sub.Memory.Path == "" {
		return fmt.Errorf("subscription %q: memory.path is required for raw_dump backend", sub.Name)
	}

	since := time.Now().Add(-24 * time.Hour).UnixMilli()
	maxEvents := 100

	events, err := r.queryEvents(ctx, sub, since, maxEvents)
	if err != nil {
		return fmt.Errorf("query events: %w", err)
	}

	r.log.Debug("raw dump", "subscription", sub.Name, "events", len(events))

	md := renderRawDump(sub, events)

	path := sub.Memory.Path
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create dirs: %w", err)
	}
	if err := os.WriteFile(path, []byte(md), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	// If claude_md is configured, append @reference to CLAUDE.md if not already present.
	if sub.Memory.ClaudeMD != "" {
		if err := appendClaudeLink(sub.Memory.ClaudeMD, sub.Memory.Path); err != nil {
			r.log.Warn("failed to append claude link", "claude_md", sub.Memory.ClaudeMD, "err", err)
		}
	}

	// Inject memory section into each configured third-party agent file
	// (e.g. ~/.hermes/memories/MEMORY.md, ~/.openclaw/workspace/MEMORY.md).
	for _, t := range sub.Memory.InjectTargets {
		if t.Path == "" {
			continue
		}
		target := injector.InjectTarget{Path: t.Path, Header: t.Header}
		if err := injector.Inject(target, md); err != nil {
			r.log.Warn("inject target failed", "path", t.Path, "err", err)
		} else {
			r.log.Debug("injected memory", "target", t.Path)
		}
	}

	return nil
}

// appendClaudeLink appends @memoryRef to claudeMD if the line doesn't already exist.
// claudeMD is the path to CLAUDE.md (e.g., /path/to/CLAUDE.md)
// memoryPath is the absolute path to the memory file (e.g., /path/to/.opencontext/memory.md)
func appendClaudeLink(claudeMD, memoryPath string) error {
	// Compute @-style path relative to CLAUDE.md directory
	// e.g., /path/to/CLAUDE.md and /path/to/.opencontext/memory.md -> @.opencontext/memory.md
	// Note: rel is like ".opencontext/memory.md" (with leading dot as part of dir name)
	rel := computeRelativeToClaudeMD(claudeMD, memoryPath)
	ref := "@" + rel // "@" + ".opencontext/memory.md" = "@.opencontext/memory.md"

	// Read existing CLAUDE.md
	content, err := os.ReadFile(claudeMD)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read CLAUDE.md: %w", err)
	}

	// Check if reference already exists
	if os.IsNotExist(err) {
		content = []byte{}
	} else {
		// Split into lines and check each
		lines := strings.Split(string(content), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == ref || line == ref+")" {
				// Already present
				return nil
			}
		}
	}

	// Append the reference
	f, err := os.OpenFile(claudeMD, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open CLAUDE.md: %w", err)
	}
	defer f.Close()

	// Add newline if file doesn't end with one
	if len(content) > 0 && content[len(content)-1] != '\n' {
		if _, err := f.WriteString("\n"); err != nil {
			return err
		}
	}

	// Append the @ reference as a comment explaining its purpose
	if _, err := f.WriteString("\n" + ref + "  \n"); err != nil {
		return fmt.Errorf("write @ reference: %w", err)
	}

	return nil
}

// computeRelativeToClaudeMD computes the relative path from the CLAUDE.md directory
// to the memory file, for use in @-style references.
// claudeMD: /path/to/CLAUDE.md
// memoryPath: /path/to/.opencontext/memory.md
// Returns: .opencontext/memory.md (caller prepends @)
func computeRelativeToClaudeMD(claudeMD, memoryPath string) string {
	claDir := filepath.Dir(claudeMD)
	rel, err := filepath.Rel(claDir, memoryPath)
	if err != nil {
		// Fallback: just use the memory filename
		return filepath.Base(memoryPath)
	}
	// rel is like ".opencontext/memory.md" - the leading dot is part of the directory name
	// Strip leading "./" if present (e.g., "../../other/.opencontext/memory.md")
	rel = strings.TrimPrefix(rel, "./")
	return rel
}

func (r *RawDumpRunner) queryEvents(ctx context.Context, sub *subscription.Subscription, since int64, limit int) ([]*event.ActivityEvent, error) {
	// System-level sources that don't have per-project semantics
	isSystemSource := func(s event.Source) bool {
		return s == event.SourceOS || s == event.SourceBrowser ||
			s == event.SourceClaude || s == event.SourceCodex ||
			s == event.SourceCursor || s == event.SourceOpenCode
	}

	// Check if all sources are system-level
	allSourcesSystem := len(sub.Filter.Sources) > 0
	for _, src := range sub.Filter.Sources {
		if !isSystemSource(src) {
			allSourcesSystem = false
			break
		}
	}

	if len(sub.Filter.Projects) > 0 {
		var all []*event.ActivityEvent
		perProjectLimit := limit / len(sub.Filter.Projects)
		if perProjectLimit < 20 {
			perProjectLimit = 20
		}

		// Query per-project events for non-system sources
		hasNonSystemSource := !allSourcesSystem

		if hasNonSystemSource {
			for _, proj := range sub.Filter.Projects {
				evts, err := r.store.Events.Query(ctx, &event.QueryRequest{
					Project:        proj,
					Since:          since,
					MaxSensitivity: sub.MaxSensitivity(),
					Limit:          perProjectLimit,
				})
				if err != nil {
					return nil, err
				}
				all = append(all, evts...)
			}
		}

		// Also query system-level events that don't have a specific project
		// (os events, browser events, claude events from unknown projects)
		if len(sub.Filter.Sources) == 0 || isSystemSource(event.SourceOS) {
			evts, err := r.store.Events.Query(ctx, &event.QueryRequest{
				Since:          since,
				MaxSensitivity: sub.MaxSensitivity(),
				Limit:          limit,
			})
			if err != nil {
				return nil, err
			}
			all = append(all, evts...)
		}

		sort.Slice(all, func(i, j int) bool { return all[i].Ts < all[j].Ts })
		if len(all) > limit {
			all = all[len(all)-limit:]
		}
		return all, nil
	}

	return r.store.Events.Query(ctx, &event.QueryRequest{
		Since:          since,
		MaxSensitivity: sub.MaxSensitivity(),
		Limit:          limit,
	})
}

// ── markdown renderer ─────────────────────────────────────────────────────────

func renderRawDump(sub *subscription.Subscription, events []*event.ActivityEvent) string {
	var sb strings.Builder
	now := time.Now()

	projectLabel := sub.Name
	if len(sub.Filter.Projects) == 1 {
		projectLabel = sub.Filter.Projects[0]
	} else if len(sub.Filter.Projects) > 1 {
		projectLabel = strings.Join(sub.Filter.Projects, ", ")
	}

	sb.WriteString("# OpenContext: Activity Memory\n\n")
	sb.WriteString(fmt.Sprintf("> **Project:** %s  \n", projectLabel))
	sb.WriteString(fmt.Sprintf("> **Updated:** %s  \n", now.Format("2006-01-02 15:04:05")))
	sb.WriteString(fmt.Sprintf("> **Events:** %d (up to 100 most recent, last 24h)  \n", len(events)))
	sb.WriteString("> **Query more:** `oc events --since 7d --source shell` · `oc events --project myapp`  \n")
	sb.WriteString(">\n")
	sb.WriteString("> *Auto-generated by [OpenContext](https://github.com/opencontext/opencontext). Do not edit manually.*\n\n")

	sb.WriteString("---\n\n")

	// Schema reference section — helps the agent interpret fields without guessing
	sb.WriteString("## Event Type Reference\n\n")
	sb.WriteString("| Type | Meaning | Key fields |\n")
	sb.WriteString("|------|---------|------------|\n")

	seenTypes := collectEventTypes(events)
	for _, key := range seenTypes {
		parts := strings.SplitN(key, ".", 2)
		if len(parts) != 2 {
			continue
		}
		schema := event.LookupSchema(event.Source(parts[0]), event.EventType(parts[1]))
		if schema == nil {
			sb.WriteString(fmt.Sprintf("| `%s` | — | — |\n", key))
			continue
		}
		fields := schemaFieldSummary(schema)
		sb.WriteString(fmt.Sprintf("| `%s` | %s | %s |\n", key, schema.Description, fields))
	}

	sb.WriteString("\n---\n\n")

	// Events grouped by day, newest first
	sb.WriteString("## Recent Activity\n\n")

	if len(events) == 0 {
		sb.WriteString("*No events in the last 24 hours.*\n")
		return sb.String()
	}

	// Reverse: newest first, then deduplicate consecutive identical events
	reversed := make([]*event.ActivityEvent, len(events))
	copy(reversed, events)
	sort.Slice(reversed, func(i, j int) bool { return reversed[i].Ts > reversed[j].Ts })
	reversed = deduplicateConsecutive(reversed)

	var currentDay string
	for _, e := range reversed {
		t := time.UnixMilli(e.Ts)
		day := t.Format("2006-01-02 (Monday)")
		if day != currentDay {
			currentDay = day
			sb.WriteString(fmt.Sprintf("### %s\n\n", day))
		}
		sb.WriteString(formatEventLine(e, t))
	}

	if len(reversed) >= 100 {
		sb.WriteString("\n> **Showing 100 most recent events.** To query further back:\n")
		sb.WriteString("> ```\n")
		sb.WriteString("> oc events --since 7d\n")
		sb.WriteString("> oc events --since 7d --source shell --project myapp\n")
		sb.WriteString("> oc events --since 7d --source claude\n")
		sb.WriteString("> ```\n")
	}

	return sb.String()
}

// deduplicateConsecutive removes consecutive events that have the same
// logical content (same source+type+project+command/message). The list is
// expected to be sorted newest-first; only the first (newest) of a run is kept.
func deduplicateConsecutive(events []*event.ActivityEvent) []*event.ActivityEvent {
	if len(events) == 0 {
		return events
	}
	out := events[:1]
	for i := 1; i < len(events); i++ {
		if eventDedupeKey(events[i]) != eventDedupeKey(events[i-1]) {
			out = append(out, events[i])
		}
	}
	return out
}

// eventDedupeKey returns a string that identifies an event's logical content
// for deduplication purposes.
func eventDedupeKey(e *event.ActivityEvent) string {
	proj := e.Labels["project"]
	switch e.Source {
	case event.SourceShell:
		return fmt.Sprintf("shell|%s|%s", proj, payloadString(e.Payload, "command"))
	case event.SourceClaude, event.SourceCodex, event.SourceCursor, event.SourceOpenCode:
		return fmt.Sprintf("%s|%s|%s", e.Source, proj, payloadString(e.Payload, "message"))
	default:
		return fmt.Sprintf("%s|%s|%s", e.Source, e.Type, proj)
	}
}

func formatEventLine(e *event.ActivityEvent, t time.Time) string {
	ts := t.Format("15:04")
	project := e.Labels["project"]

	// For shell events, prefer the full cwd over the bare project name so the
	// agent can distinguish /root/code/foo from /home/user/foo.
	var proj string
	if e.Source == event.SourceShell {
		cwd := e.Labels["cwd"]
		if cwd != "" {
			proj = fmt.Sprintf(" `[%s]`", cwd)
		} else if project != "" {
			proj = fmt.Sprintf(" `[%s]`", project)
		}
	} else {
		if project != "" {
			proj = fmt.Sprintf(" `[%s]`", project)
		}
	}

	var detail string
	switch e.Source {
	case event.SourceShell:
		cmd := payloadString(e.Payload, "command")
		exit := e.Labels["exit_code"]
		dur := payloadInt(e.Payload, "duration_ms")

		status := "✓"
		if exit != "" && exit != "0" {
			status = fmt.Sprintf("✗ exit %s", exit)
		}
		durStr := ""
		if dur > 0 {
			durStr = fmt.Sprintf(" · %s", formatDuration(dur))
		}
		detail = fmt.Sprintf("`%s` → %s%s", cmd, status, durStr)

	case event.SourceGit:
		switch e.Type {
		case event.EventTypeCommit:
			hash := payloadString(e.Payload, "hash")
			msg := payloadString(e.Payload, "message")
			files := payloadInt(e.Payload, "files_changed")
			ins := payloadInt(e.Payload, "insertions")
			branch := e.Labels["branch"]
			detail = fmt.Sprintf("commit `%s` on `%s`: \"%s\" (%d files, +%d)", hash, branch, msg, files, ins)
		case event.EventTypeBranchSwitch:
			from := payloadString(e.Payload, "from")
			to := payloadString(e.Payload, "to")
			detail = fmt.Sprintf("branch switch `%s` → `%s`", from, to)
		default:
			detail = fmt.Sprintf("%s", e.Type)
		}

	case event.SourceClaude, event.SourceCodex, event.SourceCursor, event.SourceOpenCode:
		msg := payloadString(e.Payload, "message")
		// Session/conversation identifier (differs by tool)
		sessionID := e.Labels["session_id"]
		if sessionID == "" {
			sessionID = e.Labels["conversation_id"]
		}
		sessionShort := ""
		if len(sessionID) >= 8 {
			sessionShort = fmt.Sprintf(" · session `%s…`", sessionID[:8])
		}
		if msg == "" {
			msgLen := payloadInt(e.Payload, "message_len")
			detail = fmt.Sprintf("*(message, %d chars)%s*", msgLen, sessionShort)
		} else {
			if len([]rune(msg)) > 80 {
				runes := []rune(msg)
				msg = string(runes[:77]) + "…"
			}
			// escape pipe chars for markdown table safety
			msg = strings.ReplaceAll(msg, "\n", " ↵ ")
			detail = fmt.Sprintf("\"%s\"%s", msg, sessionShort)
		}

	case event.SourceBrowser:
		domain := e.Labels["domain"]
		title := payloadString(e.Payload, "title")
		dur := payloadInt(e.Payload, "duration_ms")
		durStr := ""
		if dur > 0 {
			durStr = fmt.Sprintf(" · %s", formatDuration(dur))
		}
		if title != "" {
			detail = fmt.Sprintf("`%s` — %s%s", domain, title, durStr)
		} else {
			detail = fmt.Sprintf("`%s`%s", domain, durStr)
		}

	case event.SourceIDE:
		file := payloadString(e.Payload, "file")
		lang := e.Labels["language"]
		if lang != "" {
			detail = fmt.Sprintf("`%s` (%s)", file, lang)
		} else {
			detail = fmt.Sprintf("`%s`", file)
		}

	default:
		detail = fmt.Sprintf("%s", e.Type)
		for k, v := range e.Labels {
			if k != "project" {
				detail += fmt.Sprintf(" %s=%s", k, v)
			}
		}
	}

	return fmt.Sprintf("- **%s** · `%s.%s`%s · %s\n", ts, e.Source, e.Type, proj, detail)
}

// ── helpers ───────────────────────────────────────────────────────────────────

func collectEventTypes(events []*event.ActivityEvent) []string {
	seen := map[string]bool{}
	var order []string
	for _, e := range events {
		key := fmt.Sprintf("%s.%s", e.Source, e.Type)
		if !seen[key] {
			seen[key] = true
			order = append(order, key)
		}
	}
	sort.Strings(order)
	return order
}

func schemaFieldSummary(s *event.EventTypeSchema) string {
	var parts []string
	for k, def := range s.LabelDefs {
		parts = append(parts, fmt.Sprintf("`%s` (%s)", k, def.Description))
	}
	for k, def := range s.PayloadDefs {
		parts = append(parts, fmt.Sprintf("`%s` (%s)", k, def.Description))
	}
	sort.Strings(parts)
	if len(parts) > 4 {
		parts = parts[:4]
		parts = append(parts, "…")
	}
	return strings.Join(parts, ", ")
}

func payloadString(payload map[string]any, key string) string {
	v, ok := payload[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

func payloadInt(payload map[string]any, key string) int64 {
	v, ok := payload[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	}
	return 0
}

func formatDuration(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	if ms < 60000 {
		return fmt.Sprintf("%.1fs", float64(ms)/1000)
	}
	return fmt.Sprintf("%dm%ds", ms/60000, (ms%60000)/1000)
}
