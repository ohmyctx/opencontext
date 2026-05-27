// Package store defines the storage interfaces and provides a SQLite implementation.
// All persistence in contextd goes through these interfaces; swap the implementation
// without touching any other package.
package store

import (
	"context"

	"github.com/opencontext/opencontext/pkg/event"
	"github.com/opencontext/opencontext/pkg/session"
)

// EventStore persists and retrieves ActivityEvents.
type EventStore interface {
	// Save persists events; assigns IDs if missing. Returns assigned IDs.
	Save(ctx context.Context, events []*event.ActivityEvent) ([]string, error)

	// Query returns events matching the filter.
	Query(ctx context.Context, q *event.QueryRequest) ([]*event.ActivityEvent, error)

	// Prune deletes events older than beforeMs (Unix milliseconds).
	// Returns the number of rows deleted.
	Prune(ctx context.Context, beforeMs int64) (int64, error)

	// DeleteAll removes all events from the store.
	DeleteAll(ctx context.Context) error

	// DeleteBySource removes all events from the store with the given source.
	DeleteBySource(ctx context.Context, source string) error

	// Count returns the total number of stored events.
	Count(ctx context.Context) (int64, error)

	// Close releases underlying resources.
	Close() error
}

// SessionStore persists and retrieves ActivitySessions.
type SessionStore interface {
	// Save persists one or more sessions; assigns IDs if missing.
	Save(ctx context.Context, sessions []*session.ActivitySession) error

	// Query returns sessions for a project, ordered by start_ts desc.
	Query(ctx context.Context, q SessionQuery) ([]*session.ActivitySession, error)

	// Close releases underlying resources.
	Close() error
}

// SessionQuery filters session queries.
type SessionQuery struct {
	Project string
	Since   int64 // Unix ms
	Until   int64 // Unix ms (0 = now)
	Limit   int
}

// Store bundles EventStore and SessionStore for convenient injection.
type Store struct {
	Events   EventStore
	Sessions SessionStore
}
