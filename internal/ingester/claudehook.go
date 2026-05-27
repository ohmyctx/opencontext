package ingester

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/yetanotherai/opencontext/pkg/event"
)

// claudeHookInput is the JSON body Claude Code sends for hook events.
// See https://docs.anthropic.com/en/docs/claude-code/hooks
type claudeHookInput struct {
	HookEventName      string `json:"hook_event_name"`
	SessionID          string `json:"session_id"`
	TranscriptPath     string `json:"transcript_path"`
	Cwd                string `json:"cwd"`
	Prompt             string `json:"prompt"`
	SessionStartReason string `json:"session_start_reason"`
}

// MountClaudeHook registers the Claude Code hook endpoint on the given router.
func (ing *Ingester) MountClaudeHook(r chi.Router) {
	r.Post("/api/v1/hooks/claude", ing.handleClaudeHook)
}

// handleClaudeHook handles POST /api/v1/hooks/claude.
// Claude Code is configured to POST hook events here via HTTP hook in
// ~/.claude/settings.json. Response must arrive within 2 seconds.
func (ing *Ingester) handleClaudeHook(w http.ResponseWriter, r *http.Request) {
	var input claudeHookInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	var e *event.ActivityEvent

	switch input.HookEventName {
	case "UserPromptSubmit":
		e = ing.buildPromptEvent(input)
	case "SessionStart":
		e = ing.buildSessionStartEvent(input)
	default:
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
		return
	}

	if e == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "skipped"})
		return
	}

	if !ing.filter.Allow(e) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "filtered"})
		return
	}

	select {
	case ing.queue <- e:
	default:
		ing.log.Warn("event queue full, dropping claude hook event")
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "accepted", "id": e.ID})
}

func (ing *Ingester) buildPromptEvent(input claudeHookInput) *event.ActivityEvent {
	msg := strings.TrimSpace(input.Prompt)
	if len([]rune(msg)) < 5 {
		return nil
	}

	project := projectFromCwd(input.Cwd)

	labels := map[string]string{
		"session_id": input.SessionID,
	}
	if project != "" {
		labels["project"] = project
	}

	payload := map[string]any{
		"message":     msg,
		"message_len": len([]rune(msg)),
	}
	if input.TranscriptPath != "" {
		payload["session_file"] = input.TranscriptPath
	}

	return &event.ActivityEvent{
		ID:          uuid.Must(uuid.NewV7()).String(),
		Ts:          time.Now().UnixMilli(),
		Source:      event.SourceClaude,
		Type:        event.EventTypeUserMessage,
		Sensitivity: event.SensitivityL2,
		Labels:      labels,
		Payload:     payload,
	}
}

func (ing *Ingester) buildSessionStartEvent(input claudeHookInput) *event.ActivityEvent {
	project := projectFromCwd(input.Cwd)

	labels := map[string]string{
		"session_id": input.SessionID,
	}
	if project != "" {
		labels["project"] = project
	}

	payload := map[string]any{}
	if input.SessionStartReason != "" {
		payload["reason"] = input.SessionStartReason
	}

	return &event.ActivityEvent{
		ID:          uuid.Must(uuid.NewV7()).String(),
		Ts:          time.Now().UnixMilli(),
		Source:      event.SourceClaude,
		Type:        event.EventTypeSessionStart,
		Sensitivity: event.SensitivityL1,
		Labels:      labels,
		Payload:     payload,
	}
}

// projectFromCwd derives a project name by walking up from cwd to find the
// git root, then returning that directory's basename.
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
