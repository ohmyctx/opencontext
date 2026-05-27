package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/yetanotherai/opencontext/pkg/event"
	"github.com/yetanotherai/opencontext/pkg/session"
	_ "modernc.org/sqlite" // pure-Go SQLite driver, no CGO
)

const schema = `
PRAGMA journal_mode = WAL;
PRAGMA synchronous  = NORMAL;
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS events (
    id          TEXT    PRIMARY KEY,
    ts          INTEGER NOT NULL,
    source      TEXT    NOT NULL,
    type        TEXT    NOT NULL,
    sensitivity INTEGER NOT NULL DEFAULT 1,
    labels      TEXT    NOT NULL DEFAULT '{}',
    payload     TEXT    NOT NULL DEFAULT '{}'
) STRICT;

CREATE INDEX IF NOT EXISTS idx_events_ts       ON events(ts DESC);
CREATE INDEX IF NOT EXISTS idx_events_source   ON events(source, ts DESC);
CREATE INDEX IF NOT EXISTS idx_events_project  ON events(json_extract(labels, '$.project'), ts DESC);

CREATE VIRTUAL TABLE IF NOT EXISTS events_fts
    USING fts5(
        content='events',
        content_rowid='rowid',
        labels,
        payload,
        tokenize='unicode61 remove_diacritics 1'
    );

CREATE TRIGGER IF NOT EXISTS events_fts_insert AFTER INSERT ON events BEGIN
    INSERT INTO events_fts(rowid, labels, payload)
    VALUES (new.rowid, new.labels, new.payload);
END;

CREATE TRIGGER IF NOT EXISTS events_fts_delete AFTER DELETE ON events BEGIN
    INSERT INTO events_fts(events_fts, rowid, labels, payload)
    VALUES ('delete', old.rowid, old.labels, old.payload);
END;

CREATE TABLE IF NOT EXISTS sessions (
    id          TEXT    PRIMARY KEY,
    start_ts    INTEGER NOT NULL,
    end_ts      INTEGER NOT NULL,
    project     TEXT,
    topic       TEXT,
    event_ids   TEXT    NOT NULL DEFAULT '[]',
    summary     TEXT
) STRICT;

CREATE INDEX IF NOT EXISTS idx_sessions_ts      ON sessions(start_ts DESC);
CREATE INDEX IF NOT EXISTS idx_sessions_project ON sessions(project, start_ts DESC);
`

// SQLiteEventStore implements EventStore using modernc.org/sqlite (pure Go).
type SQLiteEventStore struct {
	db *sql.DB
}

// SQLiteSessionStore implements SessionStore using the same DB handle.
type SQLiteSessionStore struct {
	db *sql.DB
}

// OpenSQLite opens (or creates) the SQLite database at path and runs schema migrations.
func OpenSQLite(path string) (*SQLiteEventStore, *SQLiteSessionStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}

	// Single writer is the SQLite sweet spot; use a single connection for writes.
	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(0)

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, nil, fmt.Errorf("apply schema: %w", err)
	}

	return &SQLiteEventStore{db: db}, &SQLiteSessionStore{db: db}, nil
}

// ── EventStore ────────────────────────────────────────────────────────────────

func (s *SQLiteEventStore) Save(ctx context.Context, events []*event.ActivityEvent) ([]string, error) {
	ids := make([]string, 0, len(events))

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO events (id, ts, source, type, sensitivity, labels, payload)
         VALUES (?, ?, ?, ?, ?, ?, ?)
         ON CONFLICT(id) DO NOTHING`)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	for _, e := range events {
		if e.ID == "" {
			e.ID = uuid.Must(uuid.NewV7()).String()
		}
		if e.Labels == nil {
			e.Labels = map[string]string{}
		}
		if e.Payload == nil {
			e.Payload = map[string]any{}
		}

		labelsJSON, err := json.Marshal(e.Labels)
		if err != nil {
			return nil, fmt.Errorf("marshal labels for event %s: %w", e.ID, err)
		}
		payloadJSON, err := json.Marshal(e.Payload)
		if err != nil {
			return nil, fmt.Errorf("marshal payload for event %s: %w", e.ID, err)
		}

		if _, err := stmt.ExecContext(ctx,
			e.ID, e.Ts, string(e.Source), string(e.Type),
			int(e.Sensitivity), string(labelsJSON), string(payloadJSON),
		); err != nil {
			return nil, fmt.Errorf("insert event %s: %w", e.ID, err)
		}
		ids = append(ids, e.ID)
	}

	return ids, tx.Commit()
}

func (s *SQLiteEventStore) Query(ctx context.Context, q *event.QueryRequest) ([]*event.ActivityEvent, error) {
	now := time.Now().UnixMilli()

	since := q.Since
	if since <= 0 {
		since = now - 24*60*60*1000 // default: last 24 hours
	}
	until := q.Until
	if until <= 0 {
		until = now
	}
	limit := q.Limit
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	maxSens := int(q.MaxSensitivity)
	if maxSens <= 0 {
		maxSens = 3
	}

	// Full-text search path
	if q.Query != "" {
		return s.queryFTS(ctx, q.Query, since, until, maxSens, limit)
	}

	query := `SELECT id, ts, source, type, sensitivity, labels, payload
              FROM events
              WHERE ts >= ? AND ts <= ? AND sensitivity <= ?`
	args := []any{since, until, maxSens}

	if q.Source != "" {
		query += " AND source = ?"
		args = append(args, string(q.Source))
	}
	if q.Project != "" {
		query += " AND json_extract(labels, '$.project') = ?"
		args = append(args, q.Project)
	}

	query += " ORDER BY ts DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanEvents(rows)
}

func (s *SQLiteEventStore) queryFTS(ctx context.Context, q string, since, until int64, maxSens, limit int) ([]*event.ActivityEvent, error) {
	query := `SELECT e.id, e.ts, e.source, e.type, e.sensitivity, e.labels, e.payload
              FROM events e
              JOIN events_fts f ON e.rowid = f.rowid
              WHERE events_fts MATCH ?
              AND e.ts >= ? AND e.ts <= ? AND e.sensitivity <= ?
              ORDER BY e.ts DESC
              LIMIT ?`

	rows, err := s.db.QueryContext(ctx, query, q, since, until, maxSens, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEvents(rows)
}

func (s *SQLiteEventStore) Count(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events`).Scan(&n)
	return n, err
}

func (s *SQLiteEventStore) Prune(ctx context.Context, beforeMs int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM events WHERE ts < ?`, beforeMs)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func (s *SQLiteEventStore) DeleteAll(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM events`)
	return err
}

func (s *SQLiteEventStore) DeleteBySource(ctx context.Context, source string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM events WHERE source = ?`, source)
	return err
}

func (s *SQLiteEventStore) Close() error {
	return s.db.Close()
}

// ── SessionStore ──────────────────────────────────────────────────────────────

func (s *SQLiteSessionStore) Save(ctx context.Context, sessions []*session.ActivitySession) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO sessions (id, start_ts, end_ts, project, topic, event_ids, summary)
         VALUES (?, ?, ?, ?, ?, ?, ?)
         ON CONFLICT(id) DO UPDATE SET
           end_ts=excluded.end_ts, topic=excluded.topic,
           event_ids=excluded.event_ids, summary=excluded.summary`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, sess := range sessions {
		if sess.ID == "" {
			sess.ID = uuid.Must(uuid.NewV7()).String()
		}

		idsJSON, err := json.Marshal(sess.EventIDs)
		if err != nil {
			return err
		}

		_, err = stmt.ExecContext(ctx,
			sess.ID, sess.StartTs, sess.EndTs,
			nullableStr(sess.Project), nullableStr(sess.Topic),
			string(idsJSON), nullableStr(sess.Summary),
		)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *SQLiteSessionStore) Query(ctx context.Context, q SessionQuery) ([]*session.ActivitySession, error) {
	now := time.Now().UnixMilli()

	since := q.Since
	if since <= 0 {
		since = now - 30*24*60*60*1000 // default: last 30 days
	}
	until := q.Until
	if until <= 0 {
		until = now
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 100
	}

	query := `SELECT id, start_ts, end_ts, project, topic, event_ids, summary
              FROM sessions
              WHERE start_ts >= ? AND start_ts <= ?`
	args := []any{since, until}

	if q.Project != "" {
		query += " AND project = ?"
		args = append(args, q.Project)
	}

	query += " ORDER BY start_ts DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []*session.ActivitySession
	for rows.Next() {
		var sess session.ActivitySession
		var project, topic, summary sql.NullString
		var eventIDsJSON string

		if err := rows.Scan(&sess.ID, &sess.StartTs, &sess.EndTs,
			&project, &topic, &eventIDsJSON, &summary); err != nil {
			return nil, err
		}
		sess.Project = project.String
		sess.Topic = topic.String
		sess.Summary = summary.String

		if err := json.Unmarshal([]byte(eventIDsJSON), &sess.EventIDs); err != nil {
			return nil, err
		}
		sessions = append(sessions, &sess)
	}
	return sessions, rows.Err()
}

func (s *SQLiteSessionStore) Close() error {
	return nil // DB is shared; closed via EventStore.Close()
}

// ── helpers ───────────────────────────────────────────────────────────────────

func scanEvents(rows *sql.Rows) ([]*event.ActivityEvent, error) {
	var events []*event.ActivityEvent
	for rows.Next() {
		var e event.ActivityEvent
		var labelsJSON, payloadJSON string

		if err := rows.Scan(&e.ID, &e.Ts, &e.Source, &e.Type,
			&e.Sensitivity, &labelsJSON, &payloadJSON); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(labelsJSON), &e.Labels); err != nil {
			return nil, fmt.Errorf("unmarshal labels for %s: %w", e.ID, err)
		}
		if err := json.Unmarshal([]byte(payloadJSON), &e.Payload); err != nil {
			return nil, fmt.Errorf("unmarshal payload for %s: %w", e.ID, err)
		}
		events = append(events, &e)
	}
	return events, rows.Err()
}

func nullableStr(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
