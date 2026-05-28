// Package ingester implements the HTTP endpoint that accepts events from
// collectors and writes them to the EventStore via a buffered in-memory queue.
//
// Architecture: collectors push events → HTTP handler enqueues → background
// worker drains queue into SQLite. This decouples the HTTP response time from
// SQLite write latency.
package ingester

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ohmyctx/opencontext/internal/policy"
	"github.com/ohmyctx/opencontext/internal/store"
	"github.com/ohmyctx/opencontext/pkg/event"
)

const (
	defaultQueueSize  = 10_000
	defaultFlushSize  = 500
	defaultFlushEvery = 2 * time.Second
)

// Ingester accepts events via HTTP, filters them, and writes them to the store.
type Ingester struct {
	store  store.EventStore
	filter policy.Filter
	queue  chan *event.ActivityEvent
	log    *slog.Logger

	wg     sync.WaitGroup
	stopCh chan struct{}
}

// New creates an Ingester. Call Start() before using the HTTP handlers.
func New(es store.EventStore, f policy.Filter, log *slog.Logger) *Ingester {
	return &Ingester{
		store:  es,
		filter: f,
		queue:  make(chan *event.ActivityEvent, defaultQueueSize),
		log:    log,
		stopCh: make(chan struct{}),
	}
}

// Start launches the background flush worker. Call Stop() during shutdown.
func (ing *Ingester) Start() {
	ing.wg.Add(1)
	go ing.flushWorker()
}

// Stop drains remaining queued events and shuts down the flush worker.
func (ing *Ingester) Stop() {
	close(ing.stopCh)
	ing.wg.Wait()
}

// Mount registers HTTP routes on the given router.
func (ing *Ingester) Mount(r chi.Router) {
	r.Post("/api/v1/events", ing.handleSingle)
	r.Post("/api/v1/events/batch", ing.handleBatch)
}

// DispatchEvent filters and queues an event produced by an adapter.
func (ing *Ingester) DispatchEvent(w http.ResponseWriter, e *event.ActivityEvent) {
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
		ing.log.Warn("event queue full, dropping adapter event")
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "accepted", "id": e.ID})
}

// handleSingle handles POST /api/v1/events.
func (ing *Ingester) handleSingle(w http.ResponseWriter, r *http.Request) {
	var e event.ActivityEvent
	if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if err := e.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if e.ID == "" {
		e.ID = uuid.Must(uuid.NewV7()).String()
	}

	if !ing.filter.Allow(&e) {
		writeJSON(w, http.StatusOK, event.PushResponse{ID: e.ID})
		return
	}

	select {
	case ing.queue <- &e:
	default:
		ing.log.Warn("event queue full, dropping event", "id", e.ID)
	}

	writeJSON(w, http.StatusOK, event.PushResponse{ID: e.ID})
}

// handleBatch handles POST /api/v1/events/batch.
func (ing *Ingester) handleBatch(w http.ResponseWriter, r *http.Request) {
	var req event.BatchPushRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	resp := event.BatchPushResponse{
		IDs: make([]string, 0, len(req.Events)),
	}

	for _, e := range req.Events {
		if err := e.Validate(); err != nil {
			resp.Rejected++
			resp.Errors = append(resp.Errors, err.Error())
			continue
		}
		if e.ID == "" {
			e.ID = uuid.Must(uuid.NewV7()).String()
		}

		if !ing.filter.Allow(e) {
			resp.Accepted++ // accepted = received, not necessarily stored
			resp.IDs = append(resp.IDs, e.ID)
			continue
		}

		select {
		case ing.queue <- e:
		default:
			ing.log.Warn("event queue full, dropping batch event", "id", e.ID)
		}

		resp.Accepted++
		resp.IDs = append(resp.IDs, e.ID)
	}

	writeJSON(w, http.StatusOK, resp)
}

// flushWorker drains the queue and writes batches to the store.
func (ing *Ingester) flushWorker() {
	defer ing.wg.Done()

	ticker := time.NewTicker(defaultFlushEvery)
	defer ticker.Stop()

	buf := make([]*event.ActivityEvent, 0, defaultFlushSize)

	flush := func() {
		if len(buf) == 0 {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := ing.store.Save(ctx, buf); err != nil {
			ing.log.Error("failed to flush events to store", "count", len(buf), "err", err)
		} else {
			ing.log.Debug("flushed events", "count", len(buf))
		}
		buf = buf[:0]
	}

	for {
		select {
		case e := <-ing.queue:
			buf = append(buf, e)
			if len(buf) >= defaultFlushSize {
				flush()
			}

		case <-ticker.C:
			flush()

		case <-ing.stopCh:
			// Drain remaining items
			for {
				select {
				case e := <-ing.queue:
					buf = append(buf, e)
				default:
					flush()
					return
				}
			}
		}
	}
}

// ── HTTP helpers ──────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
