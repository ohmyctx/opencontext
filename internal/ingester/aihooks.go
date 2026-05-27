package ingester

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/yetanotherai/opencontext/pkg/event"
)

// ── Codex CLI ─────────────────────────────────────────────────────────────────

// codexHookInput is the JSON body Codex CLI sends for hook events.
// Fields mirror Claude Code with additions: turn_id, model, permission_mode.
// See https://developers.openai.com/codex/hooks
type codexHookInput struct {
	HookEventName  string `json:"hook_event_name"`
	SessionID      string `json:"session_id"`
	TurnID         string `json:"turn_id"`
	Cwd            string `json:"cwd"`
	TranscriptPath string `json:"transcript_path"`
	Model          string `json:"model"`
	PermissionMode string `json:"permission_mode"`
	Prompt         string `json:"prompt"`
}

// MountCodexHook registers the Codex CLI hook endpoint.
func (ing *Ingester) MountCodexHook(r chi.Router) {
	r.Post("/api/v1/hooks/codex", ing.handleCodexHook)
}

func (ing *Ingester) handleCodexHook(w http.ResponseWriter, r *http.Request) {
	var input codexHookInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	var e *event.ActivityEvent
	switch input.HookEventName {
	case "UserPromptSubmit":
		e = ing.buildCodexPromptEvent(input)
	case "SessionStart":
		e = ing.buildCodexSessionStartEvent(input)
	default:
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
		return
	}

	ing.dispatchAIEvent(w, e)
}

func (ing *Ingester) buildCodexPromptEvent(input codexHookInput) *event.ActivityEvent {
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
	if input.Model != "" {
		payload["model"] = input.Model
	}
	return &event.ActivityEvent{
		ID:          uuid.Must(uuid.NewV7()).String(),
		Ts:          time.Now().UnixMilli(),
		Source:      event.SourceCodex,
		Type:        event.EventTypeUserMessage,
		Sensitivity: event.SensitivityL2,
		Labels:      labels,
		Payload:     payload,
	}
}

func (ing *Ingester) buildCodexSessionStartEvent(input codexHookInput) *event.ActivityEvent {
	project := projectFromCwd(input.Cwd)
	labels := map[string]string{"session_id": input.SessionID}
	if project != "" {
		labels["project"] = project
	}
	payload := map[string]any{}
	if input.Model != "" {
		payload["model"] = input.Model
	}
	return &event.ActivityEvent{
		ID:          uuid.Must(uuid.NewV7()).String(),
		Ts:          time.Now().UnixMilli(),
		Source:      event.SourceCodex,
		Type:        event.EventTypeSessionStart,
		Sensitivity: event.SensitivityL1,
		Labels:      labels,
		Payload:     payload,
	}
}

// ── Cursor IDE ────────────────────────────────────────────────────────────────

// cursorHookInput is the JSON body Cursor IDE sends for hook events.
// Common fields: conversation_id, generation_id, model, hook_event_name,
// cursor_version, workspace_roots, user_email, transcript_path.
// Hook-specific: prompt (beforeSubmitPrompt), no extras for sessionStart.
// See https://cursor.com/docs/hooks.md
type cursorHookInput struct {
	HookEventName  string   `json:"hook_event_name"`
	ConversationID string   `json:"conversation_id"`
	GenerationID   string   `json:"generation_id"`
	Model          string   `json:"model"`
	CursorVersion  string   `json:"cursor_version"`
	WorkspaceRoots []string `json:"workspace_roots"`
	UserEmail      string   `json:"user_email"`
	TranscriptPath string   `json:"transcript_path"`
	Prompt         string   `json:"prompt"`
}

// MountCursorHook registers the Cursor IDE hook endpoint.
func (ing *Ingester) MountCursorHook(r chi.Router) {
	r.Post("/api/v1/hooks/cursor", ing.handleCursorHook)
}

func (ing *Ingester) handleCursorHook(w http.ResponseWriter, r *http.Request) {
	var input cursorHookInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	var e *event.ActivityEvent
	switch input.HookEventName {
	case "beforeSubmitPrompt":
		e = ing.buildCursorPromptEvent(input)
	case "sessionStart":
		e = ing.buildCursorSessionStartEvent(input)
	default:
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
		return
	}

	ing.dispatchAIEvent(w, e)
}

func (ing *Ingester) buildCursorPromptEvent(input cursorHookInput) *event.ActivityEvent {
	msg := strings.TrimSpace(input.Prompt)
	if len([]rune(msg)) < 5 {
		return nil
	}
	cwd := cursorWorkspaceRoot(input.WorkspaceRoots)
	project := projectFromCwd(cwd)
	labels := map[string]string{"conversation_id": input.ConversationID}
	if project != "" {
		labels["project"] = project
	}
	payload := map[string]any{
		"message":     msg,
		"message_len": len([]rune(msg)),
	}
	if input.Model != "" {
		payload["model"] = input.Model
	}
	return &event.ActivityEvent{
		ID:          uuid.Must(uuid.NewV7()).String(),
		Ts:          time.Now().UnixMilli(),
		Source:      event.SourceCursor,
		Type:        event.EventTypeUserMessage,
		Sensitivity: event.SensitivityL2,
		Labels:      labels,
		Payload:     payload,
	}
}

func (ing *Ingester) buildCursorSessionStartEvent(input cursorHookInput) *event.ActivityEvent {
	cwd := cursorWorkspaceRoot(input.WorkspaceRoots)
	project := projectFromCwd(cwd)
	labels := map[string]string{"conversation_id": input.ConversationID}
	if project != "" {
		labels["project"] = project
	}
	payload := map[string]any{}
	if input.Model != "" {
		payload["model"] = input.Model
	}
	return &event.ActivityEvent{
		ID:          uuid.Must(uuid.NewV7()).String(),
		Ts:          time.Now().UnixMilli(),
		Source:      event.SourceCursor,
		Type:        event.EventTypeSessionStart,
		Sensitivity: event.SensitivityL1,
		Labels:      labels,
		Payload:     payload,
	}
}

// cursorWorkspaceRoot returns the first workspace root, or empty string.
func cursorWorkspaceRoot(roots []string) string {
	if len(roots) > 0 {
		return roots[0]
	}
	return ""
}

// ── OpenCode ──────────────────────────────────────────────────────────────────

// openCodeHookInput handles two formats:
//  1. Claude-compatible format (via opencode-claude-hooks or manual config):
//     hook_event_name=UserPromptSubmit, session_id, cwd, prompt
//  2. Native OpenCode message.updated event:
//     sessionID, role, content, directory
type openCodeHookInput struct {
	// Claude-compatible fields (sent when using opencode-claude-hooks package
	// or when pointing opencode hooks config at this endpoint)
	HookEventName string `json:"hook_event_name"`
	SessionID     string `json:"session_id"`
	Cwd           string `json:"cwd"`
	Prompt        string `json:"prompt"`

	// Native OpenCode event fields (message.updated / session.created)
	OpenCodeSessionID string `json:"sessionID"`
	Role              string `json:"role"`
	Content           string `json:"content"`
	Directory         string `json:"directory"`
}

// MountOpenCodeHook registers the OpenCode hook endpoint.
func (ing *Ingester) MountOpenCodeHook(r chi.Router) {
	r.Post("/api/v1/hooks/opencode", ing.handleOpenCodeHook)
}

func (ing *Ingester) handleOpenCodeHook(w http.ResponseWriter, r *http.Request) {
	var input openCodeHookInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	var e *event.ActivityEvent

	// Claude-compatible format (hook_event_name present)
	if input.HookEventName != "" {
		switch input.HookEventName {
		case "UserPromptSubmit":
			e = ing.buildOpenCodePromptEvent(input.SessionID, input.Cwd, input.Prompt)
		case "SessionStart":
			e = ing.buildOpenCodeSessionStartEvent(input.SessionID, input.Cwd)
		default:
			writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
			return
		}
	} else {
		// Native OpenCode format: only capture user messages
		if input.Role != "user" {
			writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
			return
		}
		cwd := input.Directory
		sessionID := input.OpenCodeSessionID
		e = ing.buildOpenCodePromptEvent(sessionID, cwd, input.Content)
	}

	ing.dispatchAIEvent(w, e)
}

func (ing *Ingester) buildOpenCodePromptEvent(sessionID, cwd, prompt string) *event.ActivityEvent {
	msg := strings.TrimSpace(prompt)
	if len([]rune(msg)) < 5 {
		return nil
	}
	project := projectFromCwd(cwd)
	labels := map[string]string{"session_id": sessionID}
	if project != "" {
		labels["project"] = project
	}
	return &event.ActivityEvent{
		ID:          uuid.Must(uuid.NewV7()).String(),
		Ts:          time.Now().UnixMilli(),
		Source:      event.SourceOpenCode,
		Type:        event.EventTypeUserMessage,
		Sensitivity: event.SensitivityL2,
		Labels:      labels,
		Payload: map[string]any{
			"message":     msg,
			"message_len": len([]rune(msg)),
		},
	}
}

func (ing *Ingester) buildOpenCodeSessionStartEvent(sessionID, cwd string) *event.ActivityEvent {
	project := projectFromCwd(cwd)
	labels := map[string]string{"session_id": sessionID}
	if project != "" {
		labels["project"] = project
	}
	return &event.ActivityEvent{
		ID:          uuid.Must(uuid.NewV7()).String(),
		Ts:          time.Now().UnixMilli(),
		Source:      event.SourceOpenCode,
		Type:        event.EventTypeSessionStart,
		Sensitivity: event.SensitivityL1,
		Labels:      labels,
		Payload:     map[string]any{},
	}
}

// ── shared helper ─────────────────────────────────────────────────────────────

// dispatchAIEvent filters and queues an AI tool event, writing the HTTP response.
func (ing *Ingester) dispatchAIEvent(w http.ResponseWriter, e *event.ActivityEvent) {
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
		ing.log.Warn("event queue full, dropping ai hook event")
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "accepted", "id": e.ID})
}
