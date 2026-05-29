// Package event defines the core ActivityEvent protocol types used by all
// collectors and the OpenContext daemon. It has zero external dependencies.
package event

import (
	"errors"
	"fmt"
)

// Source identifies the tool family that produced an event.
type Source string

const (
	SourceShell    Source = "shell"
	SourceGit      Source = "git"
	SourceOS       Source = "os"
	SourceBrowser  Source = "browser"
	SourceIDE      Source = "ide"
	SourceIM       Source = "im"
	SourceClaude   Source = "claude"
	SourceCodex    Source = "codex"    // OpenAI Codex CLI
	SourceCursor   Source = "cursor"   // Cursor IDE agent
	SourceOpenCode Source = "opencode" // OpenCode (sst/opencode)
)

// EventType identifies the specific activity within a Source.
type EventType string

const (
	// shell
	EventTypeCommand    EventType = "command"
	EventTypeSessionEnd EventType = "session_end"

	// git
	EventTypeCommit       EventType = "commit"
	EventTypeBranchSwitch EventType = "branch_switch"
	EventTypePush         EventType = "push"
	EventTypePRCreate     EventType = "pr_create"

	// os
	EventTypeWindowFocus   EventType = "window_focus"
	EventTypeAppLaunch     EventType = "app_launch"
	EventTypeUIClick       EventType = "ui_click"
	EventTypeTextInput     EventType = "text_input"
	EventTypeKeyPress      EventType = "key_press"
	EventTypeBrowserNav    EventType = "browser_nav"
	EventTypeClipboardCopy EventType = "clipboard_copy"
	EventTypeSystemIdle    EventType = "system_idle"

	// browser
	EventTypePageVisit   EventType = "page_visit"
	EventTypeTabFocus    EventType = "tab_focus"
	EventTypeLinkClick   EventType = "link_click"
	EventTypeButtonClick EventType = "button_click"
	EventTypeFormSubmit  EventType = "form_submit"

	// ide
	EventTypeFileSave EventType = "file_save"
	EventTypeFileOpen EventType = "file_open"
	EventTypeSearch   EventType = "search"

	// im
	EventTypeMessageSent EventType = "message_sent"
	EventTypeCallStart   EventType = "call_start"

	// claude
	EventTypeUserMessage  EventType = "user_message"
	EventTypeSessionStart EventType = "session_start"
)

// SensitivityLevel encodes the privacy tier of an event.
type SensitivityLevel int

const (
	// SensitivityL1 is metadata only — app name, domain, file path, command name.
	// Safe to always collect.
	SensitivityL1 SensitivityLevel = 1

	// SensitivityL2 is structured content — full URLs, commit messages, file snippets.
	// Opt-in.
	SensitivityL2 SensitivityLevel = 2

	// SensitivityL3 is sensitive — keyboard input, full chat content, screenshots.
	// Explicit opt-in only.
	SensitivityL3 SensitivityLevel = 3
)

// ActivityEvent is the canonical unit of user activity in the OpenContext protocol.
//
// Design: inspired by Prometheus labels. Instead of flat optional fields (most of
// which would be empty for any given source), we use:
//   - Labels: queryable string dimensions — indexed in SQLite, used for filtering
//   - Payload: raw data for LLM consumption — stored as JSON, not indexed
//
// Rules:
//   - Ts is REQUIRED and must be > 0 (Unix milliseconds when activity occurred)
//   - Labels and Payload must not contain empty string values
//   - ID is assigned by the daemon on ingest; collectors may omit it
type ActivityEvent struct {
	ID          string            `json:"id"`          // UUIDv7, assigned by daemon
	Ts          int64             `json:"ts"`          // REQUIRED: Unix ms, when activity occurred
	Source      Source            `json:"source"`      // event source family
	Type        EventType         `json:"type"`        // specific activity type
	Sensitivity SensitivityLevel  `json:"sensitivity"` // 1=L1 | 2=L2 | 3=L3
	Labels      map[string]string `json:"labels"`      // queryable dimensions, no empty values
	Payload     map[string]any    `json:"payload"`     // raw data for LLM, no empty values
}

// Validate checks mandatory fields and invariants.
func (e *ActivityEvent) Validate() error {
	if e.Ts <= 0 {
		return errors.New("ts is required and must be > 0 (Unix milliseconds)")
	}
	if e.Source == "" {
		return errors.New("source is required")
	}
	if e.Type == "" {
		return errors.New("type is required")
	}
	if e.Sensitivity < SensitivityL1 || e.Sensitivity > SensitivityL3 {
		return fmt.Errorf("sensitivity must be 1, 2, or 3; got %d", e.Sensitivity)
	}
	if e.Labels == nil {
		return errors.New("labels must not be nil (use empty map if no labels)")
	}
	if e.Payload == nil {
		return errors.New("payload must not be nil (use empty map if no payload)")
	}
	for k, v := range e.Labels {
		if v == "" {
			return fmt.Errorf("label %q has empty value; omit empty labels", k)
		}
	}
	return nil
}

// BatchPushRequest is the body of POST /api/v1/events/batch.
type BatchPushRequest struct {
	Events []*ActivityEvent `json:"events"`
}

// BatchPushResponse is the response from POST /api/v1/events/batch.
type BatchPushResponse struct {
	Accepted int      `json:"accepted"`
	Rejected int      `json:"rejected"`
	IDs      []string `json:"ids"`
	Errors   []string `json:"errors,omitempty"`
}

// PushResponse is the response from POST /api/v1/events (single event).
type PushResponse struct {
	ID string `json:"id"`
}

// QueryRequest holds parameters for event queries.
type QueryRequest struct {
	Source         Source
	Project        string              // deprecated: use LabelSelectors instead
	LabelSelectors map[string]string   // key=value pairs; event must match all
	Since          int64 // Unix ms
	Until          int64 // Unix ms (0 = now)
	MaxSensitivity SensitivityLevel
	Limit          int
	Query          string // FTS5 full-text search
}

// QueryResponse is the response from GET /api/v1/events.
type QueryResponse struct {
	Events    []*ActivityEvent `json:"events"`
	Total     int              `json:"total"`
	Truncated bool             `json:"truncated"`
}
