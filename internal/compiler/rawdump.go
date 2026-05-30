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

	"github.com/ohmyctx/opencontext/internal/injector"
	"github.com/ohmyctx/opencontext/internal/store"
	"github.com/ohmyctx/opencontext/internal/subscription"
	"github.com/ohmyctx/opencontext/pkg/event"
)

const cursorMDCFilename = "opencontext-memory.mdc"

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

	// Inject memory section directly into CLAUDE.md (Claude Code).
	if sub.Memory.ClaudeMD != "" {
		target := injector.InjectTarget{Path: sub.Memory.ClaudeMD, Header: "## OpenContext — Recent Activity"}
		if err := injector.Inject(target, md); err != nil {
			r.log.Warn("claude_md inject failed", "path", sub.Memory.ClaudeMD, "err", err)
		} else {
			r.log.Debug("injected memory into CLAUDE.md", "path", sub.Memory.ClaudeMD)
		}
	}

	// Inject memory section directly into AGENTS.md (Codex, OpenCode).
	if sub.Memory.AgentsMD != "" {
		target := injector.InjectTarget{Path: sub.Memory.AgentsMD, Header: "## OpenContext — Recent Activity"}
		if err := injector.Inject(target, md); err != nil {
			r.log.Warn("agents_md inject failed", "path", sub.Memory.AgentsMD, "err", err)
		} else {
			r.log.Debug("injected memory into AGENTS.md", "path", sub.Memory.AgentsMD)
		}
	}

	// Write a dedicated Cursor rule file into the configured .cursor/rules/ directory.
	if sub.Memory.CursorRulesDir != "" {
		if err := writeCursorRuleFile(sub.Memory.CursorRulesDir, md); err != nil {
			r.log.Warn("cursor_rules_dir write failed", "dir", sub.Memory.CursorRulesDir, "err", err)
		} else {
			r.log.Debug("wrote cursor rule file", "dir", sub.Memory.CursorRulesDir)
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

// writeCursorRuleFile writes a Cursor .mdc rule file containing the memory
// content into dir/opencontext-memory.mdc. OpenContext fully owns this file
// and overwrites it on every compile.
func writeCursorRuleFile(dir, content string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create cursor rules dir %s: %w", dir, err)
	}
	// Cursor .mdc frontmatter: alwaysApply ensures the rule is always loaded.
	mdc := "---\ndescription: OpenContext recent activity context\nalwaysApply: true\n---\n\n" + content
	path := filepath.Join(dir, cursorMDCFilename)
	if err := os.WriteFile(path, []byte(mdc), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func (r *RawDumpRunner) queryEvents(ctx context.Context, sub *subscription.Subscription, since int64, limit int) ([]*event.ActivityEvent, error) {
	return r.store.Events.Query(ctx, &event.QueryRequest{
		Since:          since,
		MaxSensitivity: sub.MaxSensitivity(),
		Limit:          limit,
		LabelSelectors: sub.Filter.LabelSelectors,
	})
}

// ── markdown renderer ─────────────────────────────────────────────────────────

func renderRawDump(sub *subscription.Subscription, events []*event.ActivityEvent) string {
	var sb strings.Builder
	now := time.Now()

	projectLabel := sub.Name
	if project, ok := sub.Filter.LabelSelectors["project"]; ok {
		projectLabel = project
	}

	sb.WriteString("# OpenContext: Activity Memory\n\n")
	sb.WriteString(fmt.Sprintf("> **Project:** %s  \n", projectLabel))
	sb.WriteString(fmt.Sprintf("> **Updated:** %s  \n", now.Format("2006-01-02 15:04:05")))
	sb.WriteString(fmt.Sprintf("> **Events:** %d (up to 100 most recent, last 24h)  \n", len(events)))
	sb.WriteString("> **Query more:** `oc event list --since 7d --source shell` · `oc event list --project myapp`  \n")
	sb.WriteString(">\n")
	sb.WriteString("> *Auto-generated by [OpenContext](https://github.com/ohmyctx/opencontext). Do not edit manually.*\n\n")

	sb.WriteString("---\n\n")

	// Optional schema reference section — helps the agent interpret registered
	// event types without making raw rendering depend on collector-specific code.
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
		sb.WriteString("> oc event list --since 7d\n")
		sb.WriteString("> oc event list --since 7d --source shell --project myapp\n")
		sb.WriteString("> oc event list --since 7d --source claude\n")
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
	content := firstPayloadString(e.Payload,
		"summary", "message", "text", "command", "title", "url", "href",
		"query", "search", "file", "path", "name",
	)
	if content == "" {
		content = firstLabelString(e.Labels,
			"title", "url", "domain", "app_name", "app", "control_name", "project",
		)
	}
	return fmt.Sprintf("%s|%s|%s|%s", e.Source, e.Type, proj, truncateRunes(content, 160))
}

func formatEventLine(e *event.ActivityEvent, t time.Time) string {
	ts := t.Format("15:04")
	proj := formatProjectRef(e)
	detail := formatGenericEventDetail(e)

	return fmt.Sprintf("- **%s** · `%s.%s`%s · %s\n", ts, e.Source, e.Type, proj, detail)
}

func formatProjectRef(e *event.ActivityEvent) string {
	if cwd := e.Labels["cwd"]; cwd != "" {
		return fmt.Sprintf(" `[%s]`", cwd)
	}
	if project := e.Labels["project"]; project != "" {
		return fmt.Sprintf(" `[%s]`", project)
	}
	return ""
}

func formatGenericEventDetail(e *event.ActivityEvent) string {
	parts := []string{}

	if action := firstLabelString(e.Labels, "action"); action != "" && action != string(e.Type) {
		parts = append(parts, action)
	}

	primary := firstPayloadString(e.Payload,
		"summary", "message", "text", "command", "query", "search",
	)
	if primary != "" {
		parts = append(parts, quoteForMarkdown(truncateRunes(primary, 140)))
	}

	context := formatContextFields(e)
	if context != "" {
		parts = append(parts, context)
	}

	status := formatStatusFields(e)
	if status != "" {
		parts = append(parts, status)
	}

	extras := formatExtraFields(e, usedGenericKeys())
	if extras != "" {
		parts = append(parts, extras)
	}

	if len(parts) == 0 {
		return string(e.Type)
	}
	return strings.Join(parts, " · ")
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
	return anyToString(v)
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

func truncateRunes(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	return string(runes[:max-1]) + "…"
}

func firstPayloadString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := payloadString(payload, key); value != "" {
			return value
		}
	}
	return ""
}

func firstLabelString(labels map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := labels[key]; value != "" {
			return value
		}
	}
	return ""
}

func formatContextFields(e *event.ActivityEvent) string {
	contexts := []string{}
	if domain := e.Labels["domain"]; domain != "" {
		contexts = append(contexts, "`"+domain+"`")
	}
	if title := payloadString(e.Payload, "title"); title != "" {
		contexts = append(contexts, truncateRunes(title, 100))
	} else if title := e.Labels["title"]; title != "" {
		contexts = append(contexts, truncateRunes(title, 100))
	} else if name := firstPayloadString(e.Payload, "name"); name != "" {
		contexts = append(contexts, truncateRunes(name, 100))
	} else if name := e.Labels["name"]; name != "" {
		contexts = append(contexts, truncateRunes(name, 100))
	}
	if url := firstPayloadString(e.Payload, "url", "href"); url != "" {
		contexts = append(contexts, shortenURL(url))
	} else if url := e.Labels["url"]; url != "" {
		contexts = append(contexts, shortenURL(url))
	}
	if app := firstLabelString(e.Labels, "app_name", "app"); app != "" {
		contexts = append(contexts, app)
	}
	if file := firstPayloadString(e.Payload, "file", "path"); file != "" {
		contexts = append(contexts, "`"+truncateRunes(file, 100)+"`")
	}
	return strings.Join(contexts, " — ")
}

func formatStatusFields(e *event.ActivityEvent) string {
	status := []string{}
	if exit := e.Labels["exit_code"]; exit != "" {
		if exit == "0" {
			status = append(status, "exit 0")
		} else {
			status = append(status, "exit "+exit)
		}
	}
	if duration := payloadInt(e.Payload, "duration_ms"); duration > 0 {
		status = append(status, formatDuration(duration))
	}
	if session := firstLabelString(e.Labels, "session_id", "conversation_id"); len(session) >= 8 {
		status = append(status, "session `"+session[:8]+"…`")
	}
	return strings.Join(status, " · ")
}

func formatExtraFields(e *event.ActivityEvent, used map[string]bool) string {
	fields := []string{}
	for _, key := range sortedLabelKeys(e.Labels) {
		if used[key] || key == "project" || key == "cwd" {
			continue
		}
		fields = append(fields, fmt.Sprintf("%s=%s", key, truncateRunes(e.Labels[key], 60)))
	}
	for _, key := range sortedPayloadKeys(e.Payload) {
		if used[key] {
			continue
		}
		value := anyToString(e.Payload[key])
		if value == "" {
			continue
		}
		fields = append(fields, fmt.Sprintf("%s=%s", key, truncateRunes(value, 60)))
	}
	if len(fields) > 4 {
		fields = fields[:4]
		fields = append(fields, "…")
	}
	return strings.Join(fields, " ")
}

func usedGenericKeys() map[string]bool {
	return map[string]bool{
		"action": true, "summary": true, "message": true, "text": true,
		"command": true, "query": true, "search": true, "title": true,
		"name": true, "domain": true, "url": true, "href": true,
		"app_name": true, "app": true, "file": true, "path": true,
		"exit_code": true, "duration_ms": true, "session_id": true,
		"conversation_id": true,
	}
}

func sortedLabelKeys(labels map[string]string) []string {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedPayloadKeys(payload map[string]any) []string {
	keys := make([]string, 0, len(payload))
	for key := range payload {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func quoteForMarkdown(s string) string {
	s = strings.ReplaceAll(s, "\n", " ↵ ")
	return fmt.Sprintf("%q", s)
}

func anyToString(v any) string {
	switch value := v.(type) {
	case string:
		return value
	case fmt.Stringer:
		return value.String()
	case int:
		return fmt.Sprintf("%d", value)
	case int64:
		return fmt.Sprintf("%d", value)
	case float64:
		if value == float64(int64(value)) {
			return fmt.Sprintf("%d", int64(value))
		}
		return fmt.Sprintf("%.2f", value)
	case bool:
		return fmt.Sprintf("%t", value)
	default:
		return ""
	}
}

func shortenURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	raw = strings.TrimPrefix(raw, "https://")
	raw = strings.TrimPrefix(raw, "http://")
	return truncateRunes(raw, 100)
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
