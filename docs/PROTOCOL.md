# OpenContext Event Protocol

> Specification for the push-based event protocol between Collectors and `contextd`.

---

## 1. Overview

Collectors are independent processes that observe user activity and push structured events to the `contextd` daemon via HTTP. The event schema is inspired by Prometheus labels: a small set of **mandatory base fields** plus a `labels` map for queryable dimensions and a `payload` map for LLM-consumable raw data.

This design ensures:
- No empty optional fields cluttering the schema
- Each collector only carries fields meaningful to it
- LLMs can understand event semantics via the EventTypeSchema registry
- Time-series querying via mandatory `ts` field

---

## 2. ActivityEvent Schema

### 2.1 Go Type

```go
// ActivityEvent is the canonical event structure pushed by all collectors.
// All fields except Labels/Payload entries are mandatory.
type ActivityEvent struct {
    ID          string            `json:"id"`          // UUIDv7, assigned by contextd on ingest
    Ts          int64             `json:"ts"`          // REQUIRED: Unix milliseconds, must be > 0
    Source      Source            `json:"source"`      // shell | git | os | browser | ide | im
    Type        EventType         `json:"type"`        // event type within source
    Sensitivity SensitivityLevel  `json:"sensitivity"` // 1=L1 | 2=L2 | 3=L3
    Labels      map[string]string `json:"labels"`      // queryable dimensions (no empty values)
    Payload     map[string]any    `json:"payload"`     // raw data for LLM (no empty values)
}
```

### 2.2 Field Rules

| Field | Required | Notes |
|-------|----------|-------|
| `id` | assigned by contextd | Collectors may omit; server assigns UUIDv7 |
| `ts` | **REQUIRED** | Unix milliseconds from collector's system clock; missing ts = event rejected |
| `source` | required | Must match a registered Source constant |
| `type` | required | Must be a valid EventType for the given source |
| `sensitivity` | required | Must be 1, 2, or 3 |
| `labels` | required | Can be empty map `{}` but must be present |
| `payload` | required | Can be empty map `{}` but must be present |

**No empty string values in labels or payload.** Collectors must omit keys whose values are empty or unknown rather than setting them to `""`.

### 2.3 Source Constants

```go
const (
    SourceShell   Source = "shell"
    SourceGit     Source = "git"
    SourceOS      Source = "os"
    SourceBrowser Source = "browser"
    SourceIDE     Source = "ide"
    SourceIM      Source = "im"
)
```

### 2.4 EventType Constants

```go
const (
    // shell
    EventTypeCommand     EventType = "command"
    EventTypeSession     EventType = "session_end"

    // git
    EventTypeCommit      EventType = "commit"
    EventTypeBranch      EventType = "branch_switch"
    EventTypePush        EventType = "push"
    EventTypePR          EventType = "pr_create"

    // os
    EventTypeWindowFocus EventType = "window_focus"
    EventTypeAppLaunch   EventType = "app_launch"
    EventTypeSystemIdle  EventType = "system_idle"

    // browser
    EventTypePageVisit   EventType = "page_visit"
    EventTypeTabFocus    EventType = "tab_focus"

    // ide
    EventTypeFileSave    EventType = "file_save"
    EventTypeFileOpen    EventType = "file_open"
    EventTypeSearch      EventType = "search"

    // im
    EventTypeMessageSent EventType = "message_sent"
    EventTypeCallStart   EventType = "call_start"
)
```

### 2.5 Sensitivity Levels

| Level | Constant | Description | Examples |
|-------|----------|-------------|---------|
| 1 | `SensitivityL1` | Metadata only — safe to always collect | Command name, app name, file path, git repo, URL domain |
| 2 | `SensitivityL2` | Structured content — opt-in | Full URL, git commit message, file content snippet, search query |
| 3 | `SensitivityL3` | Sensitive — opt-in explicit | Full chat text, keyboard log, clipboard content |

---

## 3. Event Examples

### 3.1 Shell Command (L1)

```json
{
  "ts": 1748138400000,
  "source": "shell",
  "type": "command",
  "sensitivity": 1,
  "labels": {
    "app":     "zsh",
    "project": "opencontext",
    "cwd":     "/root/code/opencontext",
    "exit_code": "1"
  },
  "payload": {
    "command":      "go build ./...",
    "duration_ms":  423,
    "user":         "root"
  }
}
```

### 3.2 Shell Command (L2, includes full command args)

```json
{
  "ts": 1748138460000,
  "source": "shell",
  "type": "command",
  "sensitivity": 2,
  "labels": {
    "app":        "zsh",
    "project":    "opencontext",
    "cwd":        "/root/code/opencontext",
    "exit_code":  "0"
  },
  "payload": {
    "command":     "curl -X POST http://localhost:6060/api/v1/events -d '{\"test\":1}'",
    "duration_ms": 120,
    "user":        "root"
  }
}
```

### 3.3 Git Commit (L2)

```json
{
  "ts": 1748139000000,
  "source": "git",
  "type": "commit",
  "sensitivity": 2,
  "labels": {
    "repo":    "opencontext",
    "branch":  "main",
    "author":  "dev"
  },
  "payload": {
    "hash":    "a1b2c3d",
    "message": "feat: implement HTTP ingester with buffered queue",
    "files_changed": 4,
    "insertions": 182,
    "deletions": 12
  }
}
```

### 3.4 Browser Page Visit (L1 domain only)

```json
{
  "ts": 1748139300000,
  "source": "browser",
  "type": "page_visit",
  "sensitivity": 1,
  "labels": {
    "browser": "chrome",
    "domain":  "pkg.go.dev"
  },
  "payload": {
    "title":    "modernc.org/sqlite - Go Packages",
    "duration_ms": 45000
  }
}
```

### 3.5 OS Window Focus (L1)

```json
{
  "ts": 1748139600000,
  "source": "os",
  "type": "window_focus",
  "sensitivity": 1,
  "labels": {
    "app":   "cursor",
    "class": "Code"
  },
  "payload": {
    "title":       "internal/ingester/handler.go - opencontext",
    "duration_ms": 1800000
  }
}
```

---

## 4. HTTP API

### 4.1 Push Single Event

```
POST /api/v1/events
Content-Type: application/json

{ActivityEvent}
```

Response `200 OK`:
```json
{"id": "01957f8e-1234-7abc-8def-000000000001"}
```

Response `400 Bad Request` (validation failure):
```json
{"error": "ts is required and must be > 0"}
```

### 4.2 Push Batch of Events (recommended)

```
POST /api/v1/events/batch
Content-Type: application/json

{"events": [{ActivityEvent}, {ActivityEvent}, ...]}
```

Response `200 OK`:
```json
{"accepted": 5, "rejected": 0, "ids": ["...", "...", "...", "...", "..."]}
```

Batch endpoint is preferred: reduces HTTP overhead for shell collectors sending events on every command.

### 4.3 Health Check

```
GET /api/v1/health
```

Response `200 OK`:
```json
{
  "status": "ok",
  "version": "0.1.0",
  "uptime_seconds": 3600,
  "events_stored": 12487
}
```

### 4.4 Query Events (for `oc events` CLI command)

```
GET /api/v1/events?source=shell&project=opencontext&since=2h&limit=50
```

Query parameters:

| Param | Type | Default | Description |
|-------|------|---------|-------------|
| `source` | string | all | Filter by source |
| `project` | string | all | Filter by `labels.project` |
| `since` | duration or RFC3339 | 24h | Minimum event timestamp |
| `until` | duration or RFC3339 | now | Maximum event timestamp |
| `max_sensitivity` | int | 3 | Exclude events above this level |
| `limit` | int | 100 | Maximum events returned |
| `q` | string | — | FTS5 full-text search query |

Response `200 OK`:
```json
{
  "events": [{ActivityEvent}, ...],
  "total": 42,
  "truncated": false
}
```

### 4.5 Trigger Memory Compile

```
POST /api/v1/compile
Content-Type: application/json

{"subscription": "opencontext-project"}
```

Response `200 OK`:
```json
{"status": "triggered", "subscription": "opencontext-project"}
```

---

## 5. EventTypeSchema Registry

The schema registry provides LLMs with semantic understanding of each event type. The Memory Compiler includes the relevant schemas in its summarization prompts.

```go
type FieldDef struct {
    Description string
    Example     string
}

type EventTypeSchema struct {
    Source      Source
    Type        EventType
    Description string               // one-line description for LLM system prompt
    LabelDefs   map[string]FieldDef  // label field definitions
    PayloadDefs map[string]FieldDef  // payload field definitions
}
```

Example schema for `shell.command`:

```go
{
    Source:      SourceShell,
    Type:        EventTypeCommand,
    Description: "A shell command was executed by the user. exit_code=0 means success.",
    LabelDefs: map[string]FieldDef{
        "app":       {Description: "Shell application name", Example: "zsh"},
        "project":   {Description: "Inferred project name from cwd", Example: "opencontext"},
        "cwd":       {Description: "Working directory when command ran", Example: "/root/code/opencontext"},
        "exit_code": {Description: "Exit code: 0=success, non-zero=failure", Example: "1"},
    },
    PayloadDefs: map[string]FieldDef{
        "command":     {Description: "The full command string executed", Example: "go test ./..."},
        "duration_ms": {Description: "Execution duration in milliseconds", Example: "1243"},
    },
}
```

---

## 6. Collector Implementation Contract

A Collector MUST:
1. Set `ts` to the Unix millisecond timestamp when the activity occurred (not when the event is sent)
2. Set `sensitivity` to the most conservative accurate level
3. Omit any label or payload field that is empty or not applicable
4. Use batch push (`/api/v1/events/batch`) for high-frequency events
5. Tolerate `contextd` being unavailable (buffer locally or drop, never crash)
6. Register a schema for any custom EventTypes it introduces via `event.RegisterSchema()`

A Collector MUST NOT:
1. Push events with `ts = 0` or negative `ts`
2. Include empty string values in `labels` or `payload`
3. Block user workflows waiting for HTTP response
4. Push L3 events unless the user has explicitly opted in
