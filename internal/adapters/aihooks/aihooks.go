package aihooks

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ohmyctx/opencontext/pkg/event"
)

type DispatchFunc func(http.ResponseWriter, *event.ActivityEvent)

type Adapter struct {
	dispatch DispatchFunc
}

func Mount(r chi.Router, dispatch DispatchFunc) {
	a := &Adapter{dispatch: dispatch}
	r.Post("/api/v1/hooks/claude", a.handleClaudeHook)
	r.Post("/api/v1/hooks/codex", a.handleCodexHook)
	r.Post("/api/v1/hooks/cursor", a.handleCursorHook)
	r.Post("/api/v1/hooks/opencode", a.handleOpenCodeHook)
	r.Post("/api/v1/hooks/hermes", a.handleHermesHook)
	r.Post("/api/v1/hooks/openclaw", a.handleOpenClawHook)
}

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

func (a *Adapter) handleCodexHook(w http.ResponseWriter, r *http.Request) {
	var input codexHookInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	var e *event.ActivityEvent
	switch input.HookEventName {
	case "UserPromptSubmit":
		e = buildCodexPromptEvent(input)
	case "SessionStart":
		e = buildCodexSessionStartEvent(input)
	default:
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
		return
	}

	a.dispatch(w, e)
}

func buildCodexPromptEvent(input codexHookInput) *event.ActivityEvent {
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

func buildCodexSessionStartEvent(input codexHookInput) *event.ActivityEvent {
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

func (a *Adapter) handleCursorHook(w http.ResponseWriter, r *http.Request) {
	var input cursorHookInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	var e *event.ActivityEvent
	switch input.HookEventName {
	case "beforeSubmitPrompt":
		e = buildCursorPromptEvent(input)
	case "sessionStart":
		e = buildCursorSessionStartEvent(input)
	default:
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
		return
	}

	a.dispatch(w, e)
}

func buildCursorPromptEvent(input cursorHookInput) *event.ActivityEvent {
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

func buildCursorSessionStartEvent(input cursorHookInput) *event.ActivityEvent {
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

func (a *Adapter) handleOpenCodeHook(w http.ResponseWriter, r *http.Request) {
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
			e = buildOpenCodePromptEvent(input.SessionID, input.Cwd, input.Prompt)
		case "SessionStart":
			e = buildOpenCodeSessionStartEvent(input.SessionID, input.Cwd)
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
		e = buildOpenCodePromptEvent(sessionID, cwd, input.Content)
	}

	a.dispatch(w, e)
}

func buildOpenCodePromptEvent(sessionID, cwd, prompt string) *event.ActivityEvent {
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

func buildOpenCodeSessionStartEvent(sessionID, cwd string) *event.ActivityEvent {
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

// ── Hermes Agent ─────────────────────────────────────────────────────────────

// hermesHookInput handles two wire formats:
//
//  1. Gateway hook (handler.py): event_type + context fields flattened
//     {"event_type":"agent:start","platform":"telegram","user_id":"123","session_id":"s","message":"hi"}
//
//  2. Shell hook (oc-hook.sh via stdin): hook_event_name + standard fields
//     {"hook_event_name":"pre_llm_call","session_id":"s","cwd":"/...",
//      "extra":{"user_message":"hi","platform":"cli","model":"gpt-4o",...}}
type hermesHookInput struct {
	// Gateway hook fields
	EventType string `json:"event_type"`
	Platform  string `json:"platform"`
	UserID    string `json:"user_id"`
	SessionID string `json:"session_id"`
	Message   string `json:"message"`

	// Shell hook top-level fields
	HookEventName string                 `json:"hook_event_name"`
	Extra         map[string]interface{} `json:"extra"`
}

func hermesExtraStr(extra map[string]interface{}, key string) string {
	if extra == nil {
		return ""
	}
	if v, ok := extra[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func (a *Adapter) handleHermesHook(w http.ResponseWriter, r *http.Request) {
	var input hermesHookInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	// Normalise shell hook format to gateway hook format for unified handling.
	// Shell hook payload puts event-specific kwargs in the "extra" dict.
	if input.HookEventName != "" && input.EventType == "" {
		switch input.HookEventName {
		case "pre_llm_call":
			input.EventType = "agent:start"
			if input.Message == "" {
				// user_message is in extra, not at top level
				input.Message = hermesExtraStr(input.Extra, "user_message")
			}
			if input.Platform == "" {
				input.Platform = hermesExtraStr(input.Extra, "platform")
			}
			if input.UserID == "" {
				input.UserID = hermesExtraStr(input.Extra, "sender_id")
			}
		case "on_session_start":
			input.EventType = "session:start"
			if input.Platform == "" {
				input.Platform = hermesExtraStr(input.Extra, "platform")
			}
		default:
			writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
			return
		}
	}

	var e *event.ActivityEvent
	switch input.EventType {
	case "agent:start":
		e = buildHermesMessageEvent(input)
	case "session:start":
		e = buildHermesSessionStartEvent(input)
	default:
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
		return
	}

	a.dispatch(w, e)
}

func buildHermesMessageEvent(input hermesHookInput) *event.ActivityEvent {
	msg := strings.TrimSpace(input.Message)
	if len([]rune(msg)) < 5 {
		return nil
	}
	labels := map[string]string{}
	if input.SessionID != "" {
		labels["session_id"] = input.SessionID
	}
	if input.Platform != "" {
		labels["platform"] = input.Platform
	}
	if input.UserID != "" {
		labels["user_id"] = input.UserID
	}
	return &event.ActivityEvent{
		ID:          uuid.Must(uuid.NewV7()).String(),
		Ts:          time.Now().UnixMilli(),
		Source:      event.SourceHermes,
		Type:        event.EventTypeUserMessage,
		Sensitivity: event.SensitivityL2,
		Labels:      labels,
		Payload: map[string]any{
			"message":     msg,
			"message_len": len([]rune(msg)),
		},
	}
}

func buildHermesSessionStartEvent(input hermesHookInput) *event.ActivityEvent {
	labels := map[string]string{}
	if input.SessionID != "" {
		labels["session_id"] = input.SessionID
	}
	if input.Platform != "" {
		labels["platform"] = input.Platform
	}
	if input.UserID != "" {
		labels["user_id"] = input.UserID
	}
	return &event.ActivityEvent{
		ID:          uuid.Must(uuid.NewV7()).String(),
		Ts:          time.Now().UnixMilli(),
		Source:      event.SourceHermes,
		Type:        event.EventTypeSessionStart,
		Sensitivity: event.SensitivityL1,
		Labels:      labels,
		Payload:     map[string]any{},
	}
}

// ── OpenClaw ──────────────────────────────────────────────────────────────────

// openClawHookInput is the JSON body sent by the OpenClaw internal hook handler.js.
// Three event types are forwarded:
//   - message_received: type="message", action="received" — gateway channel messages
//   - before_agent_run: type="agent",   action=*          — local TUI + gateway agent turns
//   - session_start:    type="session", action="start"    — new session
type openClawHookInput struct {
	Type       string                 `json:"type"`
	Action     string                 `json:"action"`
	SessionKey string                 `json:"session_key"`
	Context    map[string]interface{} `json:"context"`
}

func (a *Adapter) handleOpenClawHook(w http.ResponseWriter, r *http.Request) {
	var input openClawHookInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	var e *event.ActivityEvent
	switch {
	case input.Type == "message" && input.Action == "received":
		e = buildOpenClawMessageEvent(input)
	case input.Type == "agent":
		// before_agent_run fires in both local TUI and gateway; context.content holds the prompt.
		e = buildOpenClawMessageEvent(input)
	case input.Type == "session" && input.Action == "start":
		e = buildOpenClawSessionStartEvent(input)
	default:
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
		return
	}

	a.dispatch(w, e)
}

func openClawStr(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func buildOpenClawMessageEvent(input openClawHookInput) *event.ActivityEvent {
	// "content" is the canonical field; "prompt" is the before_agent_run fallback.
	msg := strings.TrimSpace(openClawStr(input.Context, "content"))
	if msg == "" {
		msg = strings.TrimSpace(openClawStr(input.Context, "prompt"))
	}
	if len([]rune(msg)) < 5 {
		return nil
	}
	sessionKey := input.SessionKey
	if sessionKey == "" {
		sessionKey = openClawStr(input.Context, "sessionKey")
	}
	channelID := openClawStr(input.Context, "channelId")
	senderID := openClawStr(input.Context, "senderId")

	labels := map[string]string{}
	if sessionKey != "" {
		labels["session_key"] = sessionKey
	}
	if channelID != "" {
		labels["channel_id"] = channelID
	}
	if senderID != "" {
		labels["sender_id"] = senderID
	}
	return &event.ActivityEvent{
		ID:          uuid.Must(uuid.NewV7()).String(),
		Ts:          time.Now().UnixMilli(),
		Source:      event.SourceOpenClaw,
		Type:        event.EventTypeUserMessage,
		Sensitivity: event.SensitivityL2,
		Labels:      labels,
		Payload: map[string]any{
			"message":     msg,
			"message_len": len([]rune(msg)),
		},
	}
}

func buildOpenClawSessionStartEvent(input openClawHookInput) *event.ActivityEvent {
	sessionKey := input.SessionKey
	if sessionKey == "" {
		sessionKey = openClawStr(input.Context, "sessionKey")
	}
	channelID := openClawStr(input.Context, "channelId")

	labels := map[string]string{}
	if sessionKey != "" {
		labels["session_key"] = sessionKey
	}
	if channelID != "" {
		labels["channel_id"] = channelID
	}
	return &event.ActivityEvent{
		ID:          uuid.Must(uuid.NewV7()).String(),
		Ts:          time.Now().UnixMilli(),
		Source:      event.SourceOpenClaw,
		Type:        event.EventTypeSessionStart,
		Sensitivity: event.SensitivityL1,
		Labels:      labels,
		Payload:     map[string]any{},
	}
}

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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
