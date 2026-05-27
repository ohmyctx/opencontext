# Building a Collector for OpenContext

A **Collector** is any process that observes user activity and pushes structured events to `contextd`. This guide covers everything you need to ship a production-quality collector — from a 10-line shell script to a full browser extension.

---

## Table of Contents

1. [Quick Start](#1-quick-start)
2. [The ActivityEvent Format](#2-the-activityevent-format)
3. [API Reference](#3-api-reference)
4. [Sensitivity Levels](#4-sensitivity-levels)
5. [Source & EventType Conventions](#5-source--eventtype-conventions)
6. [Examples in Multiple Languages](#6-examples-in-multiple-languages)
7. [Claude Code HTTP Hook Integration](#7-claude-code-http-hook-integration)
8. [Client-Side Pre-filtering](#8-client-side-pre-filtering)
9. [Registering an Event Schema](#9-registering-an-event-schema)
10. [Built-in Collectors Reference](#10-built-in-collectors-reference)
11. [Testing Your Collector](#11-testing-your-collector)
12. [Checklist](#12-checklist)

---

## 1. Quick Start

You don't need a Go project or SDK. Any HTTP client works. Here is a complete collector in bash:

```bash
#!/usr/bin/env bash
# Minimal collector: push one event to contextd

curl -sf -X POST http://localhost:6060/api/v1/events \
  -H "Content-Type: application/json" \
  -d "{
    \"ts\": $(date +%s%3N),
    \"source\": \"shell\",
    \"type\": \"command\",
    \"sensitivity\": 2,
    \"labels\": {
      \"project\": \"$(basename $(git rev-parse --show-toplevel 2>/dev/null) 2>/dev/null || true)\",
      \"cwd\": \"$PWD\",
      \"exit_code\": \"$?\"
    },
    \"payload\": {
      \"command\": \"$1\",
      \"duration_ms\": $2
    }
  }" &>/dev/null &   # run non-blocking — never slow down the user
```

That's it. `contextd` stores the event, and it will appear in `memory.md` the next time the scheduler runs.

---

## 2. The ActivityEvent Format

Every event is an `ActivityEvent` object. All fields are required unless noted.

```json
{
  "id":          "019e5ea8-9dae-7447-9cde-26108d2ed2ac",
  "ts":          1748182937000,
  "source":      "shell",
  "type":        "command",
  "sensitivity": 2,
  "labels": {
    "project":   "opencontext",
    "cwd":       "/root/code/opencontext",
    "exit_code": "0"
  },
  "payload": {
    "command":     "go build ./...",
    "duration_ms": 3240
  }
}
```

### Field rules

| Field | Type | Required | Notes |
|-------|------|----------|-------|
| `id` | string (UUIDv7) | No | Assigned by `contextd` on ingest if omitted |
| `ts` | int64 | **Yes** | Unix milliseconds **when the activity occurred**, not when pushed |
| `source` | string | **Yes** | Tool family: `shell`, `git`, `browser`, `ide`, `claude`, or a custom string |
| `type` | string | **Yes** | Specific activity within the source |
| `sensitivity` | int (1–3) | **Yes** | Privacy tier — see [Sensitivity Levels](#4-sensitivity-levels) |
| `labels` | `map<string,string>` | **Yes** | Queryable dimensions. **No empty string values** — omit the key instead |
| `payload` | `map<string,any>` | **Yes** | Arbitrary JSON for LLM consumption. **No empty string values** |

### Labels vs. Payload

| | Labels | Payload |
|---|--------|---------|
| Purpose | Filtering / querying | LLM context |
| Storage | Indexed (queryable via `?project=`, `?source=`) | JSON blob, full-text search only |
| Values | Strings only | Any JSON scalar or nested object |
| Rule | No empty values | No empty values |

**Put in Labels:** `project`, `cwd`, `exit_code`, `language`, `branch`, `domain` — anything you filter by.

**Put in Payload:** `command`, `url`, `message`, `duration_ms`, `file`, `diff_summary` — the rich content an LLM reads.

---

## 3. API Reference

`contextd` listens on `http://localhost:6060` by default (configurable in `~/.opencontext/config.yaml`).

### POST /api/v1/events — push a single event

```http
POST /api/v1/events
Content-Type: application/json

{ <ActivityEvent> }
```

**Response 200:**
```json
{ "id": "019e5ea8-9dae-7447-9cde-26108d2ed2ac" }
```

### POST /api/v1/events/batch — push multiple events

Prefer this for any collector that buffers events (browser extension, file watcher, etc.).

```http
POST /api/v1/events/batch
Content-Type: application/json

{
  "events": [ <ActivityEvent>, <ActivityEvent>, ... ]
}
```

**Response 200:**
```json
{
  "accepted": 3,
  "rejected": 0,
  "ids": ["019e...", "019e...", "019e..."]
}
```

### POST /api/v1/hooks/claude — Claude Code HTTP hook endpoint

Dedicated endpoint for Claude Code's [UserPromptSubmit hook](https://docs.anthropic.com/en/docs/claude-code/hooks). Accepts Claude Code's native hook JSON body directly — no translation needed.

```http
POST /api/v1/hooks/claude
Content-Type: application/json

{
  "hook_event_name": "UserPromptSubmit",
  "session_id":      "8478ea2f-d285-4bfc-92eb-0e5eb948e8fb",
  "transcript_path": "/root/.claude/projects/-.../session.jsonl",
  "cwd":             "/root/code/opencontext",
  "prompt":          "why is this test failing?"
}
```

### GET /api/v1/health — liveness check

```http
GET /api/v1/health
```

```json
{
  "status":         "ok",
  "version":        "0.1.0",
  "uptime_seconds": 3600,
  "events_stored":  2847
}
```

Use this to detect whether `contextd` is running before attempting to push.

### GET /api/v1/events — query events (for debugging)

```
GET /api/v1/events?source=shell&project=opencontext&since=1h&limit=20
```

| Query param | Description |
|-------------|-------------|
| `source` | Filter by source (`shell`, `git`, `claude`, …) |
| `project` | Filter by `labels.project` |
| `since` | Duration string (`10m`, `1h`, `24h`) or Unix ms |
| `limit` | Max results (default 200) |

---

## 4. Sensitivity Levels

Every event must declare a sensitivity level. `contextd` drops events that exceed the user's configured `max_sensitivity`.

| Level | Value | What to store | Examples |
|-------|-------|---------------|---------|
| **L1** | `1` | Metadata only — names, paths, counts | Command name (`go`), domain (`github.com`), file extension (`.go`) |
| **L2** | `2` | Structured content — readable, work-related | Full command string, commit message, page title, file path |
| **L3** | `3` | Sensitive — personal or high-privacy content | Clipboard text, full URLs with tokens, message body |

**Default rule:** start at L1 and offer L2 as an explicit opt-in. Never collect L3 unless the user explicitly enables it and understands what is being stored.

---

## 5. Source & EventType Conventions

Use existing source/type pairs when they fit. Only introduce new ones if nothing matches.

### Built-in sources and event types

| Source | EventType | Description |
|--------|-----------|-------------|
| `shell` | `command` | A shell command was executed |
| `shell` | `session_end` | Shell session ended |
| `git` | `commit` | A git commit was made |
| `git` | `branch_switch` | Checked out a different branch |
| `git` | `push` | Pushed to remote |
| `git` | `pr_create` | A pull request was opened |
| `browser` | `page_visit` | User visited a web page |
| `browser` | `tab_focus` | User focused a browser tab |
| `ide` | `file_save` | A file was saved in the editor |
| `ide` | `file_open` | A file was opened in the editor |
| `ide` | `search` | A search was performed in the editor |
| `claude` | `user_message` | User sent a message in Claude Code |
| `claude` | `session_start` | A Claude Code session began |
| `os` | `app_launch` | An application was launched |
| `os` | `window_focus` | Application window gained focus |

### Custom sources

If you're building a collector for a tool not in the list (e.g., `docker`, `jira`, `figma`), use a lowercase snake_case string:

```json
{ "source": "docker", "type": "container_crash", ... }
```

Register a schema for it (see [Section 9](#9-registering-an-event-schema)) so `memory.md` describes your event type to the LLM.

---

## 6. Examples in Multiple Languages

### Bash / curl

```bash
#!/usr/bin/env bash
# Usage: oc-push-event.sh <command> <duration_ms>

CONTEXTD_URL="${OPENCONTEXT_URL:-http://localhost:6060}"
PROJECT=$(basename "$(git rev-parse --show-toplevel 2>/dev/null)" 2>/dev/null || true)
TS=$(date +%s%3N)

# Build labels — skip empty values
LABELS="{\"cwd\": \"$PWD\""
[[ -n "$PROJECT" ]] && LABELS+=", \"project\": \"$PROJECT\""
LABELS+="}"

curl -sf -X POST "$CONTEXTD_URL/api/v1/events" \
  -H "Content-Type: application/json" \
  -d "{
    \"ts\": $TS,
    \"source\": \"shell\",
    \"type\": \"command\",
    \"sensitivity\": 2,
    \"labels\": $LABELS,
    \"payload\": {
      \"command\": $(printf '%s' "$1" | python3 -c 'import json,sys; print(json.dumps(sys.stdin.read()))'),
      \"duration_ms\": ${2:-0}
    }
  }" &>/dev/null
```

### Python

```python
#!/usr/bin/env python3
"""Example: push a browser page visit event from a Python script."""

import time
import requests

CONTEXTD_URL = "http://localhost:6060"

def push_page_visit(url: str, title: str, domain: str, duration_ms: int):
    event = {
        "ts": int(time.time() * 1000),
        "source": "browser",
        "type": "page_visit",
        "sensitivity": 2,
        "labels": {
            "domain": domain,
        },
        "payload": {
            "url": url,
            "title": title,
            "duration_ms": duration_ms,
        },
    }

    try:
        resp = requests.post(
            f"{CONTEXTD_URL}/api/v1/events",
            json=event,
            timeout=2,
        )
        resp.raise_for_status()
        return resp.json()["id"]
    except Exception:
        pass  # contextd unavailable — never crash the caller


def push_batch(events: list[dict]):
    """Push multiple events at once. Prefer this over many single-event calls."""
    try:
        resp = requests.post(
            f"{CONTEXTD_URL}/api/v1/events/batch",
            json={"events": events},
            timeout=5,
        )
        resp.raise_for_status()
        return resp.json()
    except Exception:
        pass


def contextd_running() -> bool:
    """Quick liveness check before buffering events."""
    try:
        requests.get(f"{CONTEXTD_URL}/api/v1/health", timeout=1).raise_for_status()
        return True
    except Exception:
        return False
```

### Node.js / TypeScript

```typescript
// opencontext-client.ts
// Minimal TypeScript client for pushing events to contextd.

const CONTEXTD_URL = process.env.OPENCONTEXT_URL ?? "http://localhost:6060";

interface ActivityEvent {
  ts: number;           // Unix ms
  source: string;
  type: string;
  sensitivity: 1 | 2 | 3;
  labels: Record<string, string>;
  payload: Record<string, unknown>;
}

async function pushEvent(event: ActivityEvent): Promise<string | null> {
  try {
    const res = await fetch(`${CONTEXTD_URL}/api/v1/events`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(event),
      signal: AbortSignal.timeout(2000),
    });
    if (!res.ok) return null;
    const { id } = await res.json();
    return id;
  } catch {
    return null; // contextd unavailable — fail silently
  }
}

async function pushBatch(events: ActivityEvent[]): Promise<void> {
  try {
    await fetch(`${CONTEXTD_URL}/api/v1/events/batch`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ events }),
      signal: AbortSignal.timeout(5000),
    });
  } catch {
    // fail silently
  }
}

// Example: browser extension background script
chrome.tabs.onActivated.addListener(async ({ tabId }) => {
  const tab = await chrome.tabs.get(tabId);
  if (!tab.url || tab.url.startsWith("chrome://")) return;

  const domain = new URL(tab.url).hostname;

  await pushEvent({
    ts: Date.now(),
    source: "browser",
    type: "tab_focus",
    sensitivity: 2,
    labels: { domain },
    payload: {
      url: tab.url,
      title: tab.title ?? "",
    },
  });
});
```

### Go

```go
package main

import (
    "context"
    "time"

    "github.com/opencontext/opencontext/pkg/client"
    "github.com/opencontext/opencontext/pkg/event"
)

func main() {
    c := client.New("http://localhost:6060")

    e := &event.ActivityEvent{
        Ts:          time.Now().UnixMilli(),
        Source:      event.SourceBrowser,
        Type:        event.EventTypePageVisit,
        Sensitivity: event.SensitivityL2,
        Labels: map[string]string{
            "domain": "pkg.go.dev",
        },
        Payload: map[string]any{
            "url":         "https://pkg.go.dev/net/http",
            "title":       "http package - Go Packages",
            "duration_ms": int64(45000),
        },
    }

    ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
    defer cancel()

    _, _ = c.Push(ctx, e) // errors silently dropped — contextd may be offline
}
```

---

## 7. Claude Code HTTP Hook Integration

Claude Code supports [HTTP hooks](https://docs.anthropic.com/en/docs/claude-code/hooks) that POST JSON to any URL when lifecycle events occur. OpenContext exposes a dedicated endpoint (`/api/v1/hooks/claude`) that accepts Claude Code's native hook payload without translation.

### Configuration

Add to `~/.claude/settings.json`:

```json
{
  "hooks": {
    "UserPromptSubmit": [{
      "hooks": [{
        "type": "http",
        "url": "http://localhost:6060/api/v1/hooks/claude",
        "timeout": 2
      }]
    }],
    "SessionStart": [{
      "hooks": [{
        "type": "http",
        "url": "http://localhost:6060/api/v1/hooks/claude",
        "timeout": 2
      }]
    }]
  }
}
```

Claude Code will POST a body like:

```json
{
  "hook_event_name": "UserPromptSubmit",
  "session_id":      "8478ea2f-d285-4bfc-92eb-0e5eb948e8fb",
  "transcript_path": "/root/.claude/projects/-.../session.jsonl",
  "cwd":             "/root/code/opencontext",
  "prompt":          "why is this test failing?"
}
```

`contextd` derives the project name from `cwd` by walking up to find `.git`, then stores the event as `claude.user_message`. Timestamps use `time.Now()` since the hook fires at the moment of submission.

### Adapting this pattern for other AI tools

Any AI coding tool with a webhook/hook system can integrate the same way. Create a new route handler in `internal/ingester/` that:

1. Parses the tool's native payload
2. Converts it to an `ActivityEvent`
3. Enqueues it via `ing.queue`

---

## 8. Client-Side Pre-filtering

**Filter before sending, not after.** If `contextd` receives an event it will drop (due to sensitivity policy or exclude patterns), you've wasted a round-trip HTTP call and a debug log line.

### Shell hook pattern

```zsh
# In your zsh precmd / bash PROMPT_COMMAND:

_oc_should_skip() {
  local cmd=$1

  # Leading space = shell privacy convention (always skip)
  [[ "$cmd" == " "* ]] && return 0

  # Extract the first word
  local first="${cmd%% *}"

  # Skip bare no-arg navigation/meta commands
  case "$first" in
    clear|reset|ls|ll|la|pwd|cd|history|exit)
      [[ "$cmd" == "$first" ]] && return 0 ;;
  esac

  return 1  # keep this command
}

_oc_precmd() {
  local exit_code=$?
  [[ -z "$_oc_cmd" ]] && return 0
  _oc_should_skip "$_oc_cmd" && { _oc_cmd=""; return 0; }

  /path/to/oc collector shell push \
    --command "$_oc_cmd" \
    --exit-code "$exit_code" \
    --cwd "$PWD" \
    --sensitivity 2 &>/dev/null &!   # zsh: &! = background + disown (no job notification)

  _oc_cmd=""
}
```

### General rule

Mirror the server-side filter in your collector for any patterns you know will always be rejected:

```python
SKIP_COMMANDS = {"clear", "reset", "ls", "ll", "la", "pwd", "cd", "history", "exit"}

def should_push(command: str) -> bool:
    if command.startswith(" "):   # shell privacy convention
        return False
    first_word = command.split()[0] if command.split() else ""
    if first_word in SKIP_COMMANDS and command.strip() == first_word:
        return False
    return True
```

---

## 9. Registering an Event Schema

When you introduce a new `source.type` pair, register a schema so `memory.md` contains a human-readable description that helps the LLM interpret the events correctly.

### In Go (add to your collector's `init()`)

```go
import "github.com/opencontext/opencontext/pkg/event"

func init() {
    event.RegisterSchema(&event.EventTypeSchema{
        Source:      "docker",
        Type:        "container_crash",
        Description: "A Docker container exited with a non-zero code. High-signal debugging event.",
        LabelDefs: map[string]event.FieldDef{
            "project":    {Description: "Project or compose stack name", Example: "myapp"},
            "image":      {Description: "Docker image name", Example: "postgres:16"},
        },
        PayloadDefs: map[string]event.FieldDef{
            "container":  {Description: "Container name or ID", Example: "myapp-db-1"},
            "exit_code":  {Description: "Process exit code", Example: "137"},
            "restart_count": {Description: "How many times this container has restarted", Example: "3"},
        },
    })
}
```

The schema appears in the `## Event Type Reference` table in `memory.md`, giving the LLM field-level context without needing to guess.

### Schema registration via HTTP (future)

A `POST /api/v1/schemas` endpoint is planned for collectors that are not written in Go. Until then, open a PR to add your schema to `pkg/event/schema.go`.

---

## 10. Built-in Collectors Reference

| Collector | Command | Mechanism | Event types |
|-----------|---------|-----------|-------------|
| **Shell** | `oc collector shell install` | zsh/bash preexec + precmd hooks | `shell.command` |
| **Claude Code** | Configured in `~/.claude/settings.json` | Claude Code HTTP hook (`UserPromptSubmit`, `SessionStart`) | `claude.user_message`, `claude.session_start` |
| **Git** | `oc collector git install` | git post-commit, post-checkout hooks | `git.commit`, `git.branch_switch` |

---

## 11. Testing Your Collector

```bash
# 1. Start contextd with debug logging so you see every event
contextd --log-level debug

# 2. Push a test event from your collector
curl -sf -X POST http://localhost:6060/api/v1/events \
  -H "Content-Type: application/json" \
  -d '{
    "ts": '"$(date +%s%3N)"',
    "source": "docker",
    "type": "container_crash",
    "sensitivity": 1,
    "labels": {"project": "myapp", "image": "postgres:16"},
    "payload": {"container": "myapp-db-1", "exit_code": "137", "restart_count": 3}
  }'

# 3. Verify it was stored
oc events --source docker --since 5m

# 4. Trigger a memory rebuild
curl -X POST http://localhost:6060/api/v1/compile \
  -H "Content-Type: application/json" \
  -d '{"subscription": "my-project"}'

# 5. Check the output
cat ~/.opencontext/memory.md
```

### Simulate contextd being unavailable

```bash
# Stop contextd, push events, verify your collector doesn't crash or block
pkill contextd
your-collector-push-command   # must exit cleanly within 2s
# Restart contextd — events pushed while offline are lost (expected for fire-and-forget collectors)
contextd &
```

---

## 12. Checklist

Before publishing your collector, verify:

**Correctness**
- [ ] `ts` is the timestamp **when the activity occurred**, not when the HTTP call was made
- [ ] No empty string values in `labels` — omit the key if the value would be empty
- [ ] `source` and `type` follow the naming conventions in [Section 5](#5-source--eventtype-conventions)
- [ ] A schema is registered for any new `source.type` pair

**Privacy**
- [ ] Default sensitivity is L1 or L2 — never L3 without explicit user opt-in
- [ ] Any L3 collection has a visible warning in the README
- [ ] Credentials, tokens, and secrets are never captured in any payload field

**Resilience**
- [ ] Collector never blocks user workflow waiting for `contextd` (2s timeout max)
- [ ] Collector handles `contextd` being down gracefully (drop silently or buffer locally)
- [ ] Client-side pre-filtering mirrors the server's exclude patterns

**UX**
- [ ] `install` command is idempotent — running it twice doesn't duplicate hooks
- [ ] `install` output tells the user exactly what was changed and what to do next
- [ ] README describes: what events are collected, at what sensitivity, and how to disable

---

## Ideas for Future Collectors

| Collector | Trigger | Events | Value |
|-----------|---------|--------|-------|
| **Browser extension** | Tab focus/unload | `browser.page_visit` | Captures research and documentation reading |
| **Test runner** | Detect `pytest`, `go test`, `jest` output | `test.run` | Pass/fail rate is high-signal debugging context |
| **Docker events** | `docker events --format json` stream | `docker.container_crash` | Container instability correlates with active debugging |
| **Cursor/VSCode extension** | `onDidSaveTextDocument`, `onDidChangeActiveEditor` | `ide.file_save`, `ide.file_open` | File activity is more precise than shell commands |
| **Clipboard watcher** | OS clipboard change (L3 opt-in) | `os.clipboard_copy` | Copied error messages and stack traces |
| **GitHub/GitLab webhooks** | PR review, CI failure | `git.pr_review`, `ci.build_failed` | Remote workflow context |

---

*For questions or to contribute a collector, open an issue at [github.com/opencontext/opencontext](https://github.com/opencontext/opencontext).*
