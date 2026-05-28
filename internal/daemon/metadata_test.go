package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ohmyctx/opencontext/internal/registry"
	"github.com/ohmyctx/opencontext/pkg/event"
)

func TestCollectorsHandlerReturnsVersionedManifests(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/collectors", nil)
	rec := httptest.NewRecorder()

	makeCollectorsHandler("test-version")(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	var collectors []registry.CollectorManifest
	if err := json.Unmarshal(rec.Body.Bytes(), &collectors); err != nil {
		t.Fatal(err)
	}
	if len(collectors) == 0 {
		t.Fatal("expected collectors")
	}
	for _, c := range collectors {
		if c.Name == "claude" && c.Version != "test-version" {
			t.Fatalf("expected bundled collector version to be replaced, got %q", c.Version)
		}
	}
}

func TestSchemasHandlerReturnsEventSchemas(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/schemas", nil)
	rec := httptest.NewRecorder()

	makeSchemasHandler()(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	var schemas []event.EventTypeSchema
	if err := json.Unmarshal(rec.Body.Bytes(), &schemas); err != nil {
		t.Fatal(err)
	}
	if len(schemas) == 0 {
		t.Fatal("expected schemas")
	}
}
