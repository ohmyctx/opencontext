package aihooks

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ohmyctx/opencontext/pkg/event"
)

// claudeHookInput is the JSON body Claude Code sends for hook events.
type claudeHookInput struct {
	HookEventName      string `json:"hook_event_name"`
	SessionID          string `json:"session_id"`
	TranscriptPath     string `json:"transcript_path"`
	Cwd                string `json:"cwd"`
	Prompt             string `json:"prompt"`
	SessionStartReason string `json:"session_start_reason"`
}

func (a *Adapter) handleClaudeHook(w http.ResponseWriter, r *http.Request) {
	var input claudeHookInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	var e *event.ActivityEvent
	switch input.HookEventName {
	case "UserPromptSubmit":
		e = buildClaudePromptEvent(input)
	case "SessionStart":
		e = buildClaudeSessionStartEvent(input)
	default:
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
		return
	}

	a.dispatch(w, e)
}

func buildClaudePromptEvent(input claudeHookInput) *event.ActivityEvent {
	msg := strings.TrimSpace(input.Prompt)
	if len([]rune(msg)) < 5 {
		return nil
	}

	project := projectFromCwd(input.Cwd)
	labels := map[string]string{"session_id": input.SessionID}
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

func buildClaudeSessionStartEvent(input claudeHookInput) *event.ActivityEvent {
	project := projectFromCwd(input.Cwd)
	labels := map[string]string{"session_id": input.SessionID}
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
