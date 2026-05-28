package compiler

import (
	"strings"
	"testing"
	"time"

	"github.com/ohmyctx/opencontext/internal/subscription"
	"github.com/ohmyctx/opencontext/pkg/event"
)

func TestFormatEventLineUsesGenericFieldsForUnknownCollector(t *testing.T) {
	e := &event.ActivityEvent{
		Ts:          time.Date(2026, 5, 28, 14, 30, 0, 0, time.Local).UnixMilli(),
		Source:      event.Source("customcrm"),
		Type:        event.EventType("ticket_update"),
		Sensitivity: event.SensitivityL2,
		Labels: map[string]string{
			"project": "billing",
			"status":  "blocked",
			"domain":  "crm.example.com",
		},
		Payload: map[string]any{
			"summary": "Escalated renewal ticket",
			"url":     "https://crm.example.com/tickets/123",
			"ticket":  "123",
		},
	}

	line := formatEventLine(e, time.UnixMilli(e.Ts))

	for _, want := range []string{
		"`customcrm.ticket_update`",
		"`[billing]`",
		`"Escalated renewal ticket"`,
		"`crm.example.com`",
		"crm.example.com/tickets/123",
		"status=blocked",
		"ticket=123",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("formatEventLine() missing %q in:\n%s", want, line)
		}
	}
}

func TestRenderRawDumpDoesNotRequireRegisteredSchema(t *testing.T) {
	e := &event.ActivityEvent{
		Ts:          time.Date(2026, 5, 28, 14, 30, 0, 0, time.Local).UnixMilli(),
		Source:      event.Source("external"),
		Type:        event.EventType("semantic_action"),
		Sensitivity: event.SensitivityL2,
		Labels:      map[string]string{"project": "opencontext"},
		Payload:     map[string]any{"message": "Unknown collector event rendered without schema"},
	}

	md := renderRawDump(&subscription.Subscription{Name: "test"}, []*event.ActivityEvent{e})

	for _, want := range []string{
		"| `external.semantic_action` | — | — |",
		"`external.semantic_action`",
		`"Unknown collector event rendered without schema"`,
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("renderRawDump() missing %q in:\n%s", want, md)
		}
	}
}

func TestFormatEventLineKeepsBrowserTitleAndURLWithoutSourceSpecificFormatter(t *testing.T) {
	e := &event.ActivityEvent{
		Ts:          time.Date(2026, 5, 28, 14, 28, 0, 0, time.Local).UnixMilli(),
		Source:      event.SourceBrowser,
		Type:        event.EventTypeTabFocus,
		Sensitivity: event.SensitivityL1,
		Labels:      map[string]string{"domain": "chatgpt.com"},
		Payload: map[string]any{
			"title": "ChatGPT",
			"url":   "https://chatgpt.com/c/abcdef",
		},
	}

	line := formatEventLine(e, time.UnixMilli(e.Ts))

	for _, want := range []string{
		"`browser.tab_focus`",
		"`chatgpt.com`",
		"ChatGPT",
		"chatgpt.com/c/abcdef",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("formatEventLine() missing %q in:\n%s", want, line)
		}
	}
	if strings.Count(line, "ChatGPT") != 1 {
		t.Fatalf("formatEventLine() duplicated title in:\n%s", line)
	}
}
