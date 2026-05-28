package aihooks_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ohmyctx/opencontext/internal/adapters/aihooks"
	"github.com/ohmyctx/opencontext/internal/ingester"
	"github.com/ohmyctx/opencontext/internal/policy"
	"github.com/ohmyctx/opencontext/internal/store"
	"github.com/ohmyctx/opencontext/pkg/event"
)

func TestHookAdapterToIngesterE2E(t *testing.T) {
	es, _, err := store.OpenSQLite(t.TempDir() + "/events.db")
	if err != nil {
		t.Fatal(err)
	}

	ing := ingester.New(es, policy.New(policy.DefaultConfig()), slog.Default())
	ing.Start()

	r := chi.NewRouter()
	ing.Mount(r)
	aihooks.Mount(r, ing.DispatchEvent)
	srv := httptest.NewServer(r)
	defer srv.Close()

	body := `{
		"hook_event_name": "UserPromptSubmit",
		"session_id": "s1",
		"cwd": "/tmp/project",
		"prompt": "please inspect the collector boundary"
	}`
	resp, err := http.Post(srv.URL+"/api/v1/hooks/claude", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %s", resp.Status)
	}
	var accepted map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&accepted); err != nil {
		t.Fatal(err)
	}
	if accepted["status"] != "accepted" {
		t.Fatalf("expected accepted response, got %#v", accepted)
	}

	ing.Stop()

	events, err := es.Query(context.Background(), &event.QueryRequest{
		Source: event.SourceClaude,
		Since:  time.Now().Add(-time.Minute).UnixMilli(),
		Limit:  10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected one stored event, got %d", len(events))
	}
	got := events[0]
	if got.Type != event.EventTypeUserMessage {
		t.Fatalf("expected user_message, got %s", got.Type)
	}
	if got.Payload["message"] != "please inspect the collector boundary" {
		t.Fatalf("unexpected payload: %#v", got.Payload)
	}
}
