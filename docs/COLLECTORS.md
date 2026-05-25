# OpenContext Collector Development Guide

> How to build a Collector that pushes activity events to `contextd`.

---

## 1. What is a Collector?

A Collector is any process that:
1. Observes user activity in a specific tool (shell, browser, IDE, git, etc.)
2. Converts observations into `ActivityEvent` structs
3. Pushes those events to `contextd` via HTTP POST

Collectors are **decoupled** from `contextd`. They can be:
- Shell hook scripts (run as part of your zsh/bash precmd/preexec)
- Long-running background daemons (for OS activity tracking)
- Browser extensions (for tab/page events)
- IDE extensions (for file save/open events)
- Git hooks (for commit/push events)
- Any HTTP client in any language

The only requirement: speak the [PROTOCOL.md](./PROTOCOL.md).

---

## 2. Minimal Collector (curl example)

```bash
#!/usr/bin/env bash
# Push a shell command event to contextd

curl -sf -X POST http://localhost:6060/api/v1/events \
  -H "Content-Type: application/json" \
  -d "{
    \"ts\": $(date +%s%3N),
    \"source\": \"shell\",
    \"type\": \"command\",
    \"sensitivity\": 1,
    \"labels\": {
      \"app\": \"bash\",
      \"project\": \"$(basename $(git rev-parse --show-toplevel 2>/dev/null) 2>/dev/null || echo unknown)\",
      \"cwd\": \"$PWD\",
      \"exit_code\": \"$?\"
    },
    \"payload\": {
      \"command\": \"$1\",
      \"duration_ms\": $2
    }
  }" &>/dev/null &  # non-blocking, don't slow down shell
```

---

## 3. Shell Collector (built-in)

OpenContext ships a built-in Shell Collector as part of `collectors/shell/`. It provides:
- A compiled Go binary `oc-shell-collector` (or invoked via `oc collector shell push`)
- Shell hooks for zsh and bash
- Automatic project detection from git root
- Configurable sensitivity level

### Installation

```bash
# Install shell hooks
oc collector shell install

# This adds to your ~/.zshrc (or ~/.bashrc):
#   source ~/.opencontext/collectors/shell/hooks.zsh
```

### Hooks file (`hooks.zsh`)

```zsh
# Hooks written by: oc collector shell install

_oc_preexec() {
  _oc_cmd_start=$(date +%s%3N)
  _oc_cmd=$1
}

_oc_precmd() {
  local exit_code=$?
  local end=$(date +%s%3N)
  local duration=$(( end - ${_oc_cmd_start:-$end} ))

  [[ -z "$_oc_cmd" ]] && return

  oc collector shell push \
    --command "$_oc_cmd" \
    --exit-code "$exit_code" \
    --duration-ms "$duration" \
    --cwd "$PWD" &>/dev/null &

  _oc_cmd=""
}

autoload -Uz add-zsh-hook
add-zsh-hook preexec _oc_preexec
add-zsh-hook precmd _oc_precmd
```

### What gets collected

| What | Sensitivity | Labels | Payload |
|------|-------------|--------|---------|
| Command name | L1 | app, project, cwd, exit_code | command (L1: first word only) |
| Full command string | L2 | same | command (full string with args) |

The Shell Collector sends L1 events by default. Pass `--sensitivity 2` to enable full command strings.

### Excluded commands

By default, the Shell Collector drops events for:
- Empty commands
- Commands starting with a space (shell privacy convention)
- `cd`, `ls`, `pwd`, `clear`, `history` (low-value navigation)
- Configurable via `~/.opencontext/config.yaml`

```yaml
collectors:
  shell:
    sensitivity: 1             # default: L1 (command name only)
    exclude_patterns:
      - "^\\s"                 # commands starting with space
      - "^(cd|ls|pwd|clear|history|exit)$"
    project_detect: "git_root" # or "cwd_basename"
```

---

## 4. Git Collector (built-in)

The Git Collector uses git hooks to track commit, branch switch, and push events.

### Installation

```bash
# Install git hooks globally (all repos)
oc collector git install --global

# Install in current repo only
oc collector git install
```

This writes to `~/.gitconfig`:
```ini
[core]
    hooksPath = ~/.opencontext/collectors/git/hooks
```

### Hooks

`post-commit`:
```bash
#!/usr/bin/env bash
oc collector git push-event commit \
  --repo "$(basename $(git rev-parse --show-toplevel))" \
  --branch "$(git rev-parse --abbrev-ref HEAD)" \
  --hash "$(git rev-parse --short HEAD)" \
  --message "$(git log -1 --format='%s')" \
  --files "$(git diff --stat HEAD~1 HEAD | tail -1)" &>/dev/null &
```

`post-checkout`:
```bash
#!/usr/bin/env bash
# $1 = previous ref, $2 = new ref, $3 = flag (1=branch checkout, 0=file checkout)
[[ "$3" != "1" ]] && exit 0

oc collector git push-event branch_switch \
  --repo "$(basename $(git rev-parse --show-toplevel))" \
  --from "$(git rev-parse --abbrev-ref "$1")" \
  --to "$(git rev-parse --abbrev-ref HEAD)" &>/dev/null &
```

---

## 5. Building a Custom Collector

### Step 1: Understand the event schema

Read [PROTOCOL.md](./PROTOCOL.md). Know:
- Which `source` string identifies your tool
- Which `type` strings identify the activities you want to track
- Which fields belong in `labels` (queryable) vs `payload` (LLM context)
- Which sensitivity level each field warrants

### Step 2: Write Go types (optional but recommended)

If writing in Go, import `pkg/event`:

```go
import "github.com/opencontext/opencontext/pkg/event"

// Your collector's event builder
func buildEvent(cmd string, exitCode int, durationMs int64) *event.ActivityEvent {
    project := detectProject()

    e := &event.ActivityEvent{
        Ts:          time.Now().UnixMilli(),
        Source:      event.SourceShell,
        Type:        event.EventTypeCommand,
        Sensitivity: event.SensitivityL1,
        Labels: map[string]string{
            "app":       "zsh",
            "exit_code": strconv.Itoa(exitCode),
        },
        Payload: map[string]any{
            "command":     extractCommandName(cmd), // L1: first word only
            "duration_ms": durationMs,
        },
    }

    if project != "" {
        e.Labels["project"] = project
    }

    return e
}
```

### Step 3: Push events

Use the batch endpoint for best performance:

```go
import "github.com/opencontext/opencontext/pkg/client"

c := client.New("http://localhost:6060")

batch := &client.BatchRequest{
    Events: []*event.ActivityEvent{e1, e2, e3},
}

resp, err := c.PushBatch(ctx, batch)
if err != nil {
    // contextd unavailable — log and continue, never block user workflow
    return
}
```

### Step 4: Register a custom schema

If you're adding custom event types, register schemas so the LLM Summarizer understands them:

```go
func init() {
    event.RegisterSchema(&event.EventTypeSchema{
        Source:      "my-tool",
        Type:        "my-event",
        Description: "A task was completed in MyTool.",
        LabelDefs: map[string]event.FieldDef{
            "task_type": {Description: "Type of task completed", Example: "build"},
        },
        PayloadDefs: map[string]event.FieldDef{
            "result": {Description: "Outcome of the task", Example: "success"},
        },
    })
}
```

### Step 5: Handle unavailability

`contextd` may not always be running. Collectors MUST be resilient:

```go
// Option A: fire and forget (best for shell hooks — never block terminal)
go func() {
    ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
    defer cancel()
    _ = client.PushBatch(ctx, batch) // errors silently dropped
}()

// Option B: local buffer (for long-running collector daemons)
// Write events to a local queue file, retry when contextd comes back up
```

---

## 6. Collector Configuration

All collector settings live in `~/.opencontext/config.yaml` under `collectors.<name>`:

```yaml
collectors:
  shell:
    enabled: true
    sensitivity: 1
    exclude_patterns: ["^\\s", "^(cd|ls)$"]
    project_detect: "git_root"

  git:
    enabled: true
    sensitivity: 2       # commit messages are L2

  browser:
    enabled: false       # not yet installed
```

---

## 7. Testing Your Collector

```bash
# Start contextd in debug mode (verbose logging)
contextd --log-level debug

# Push a test event
oc collector shell push --command "echo hello" --exit-code 0 --duration-ms 5

# Verify it was stored
oc events --source shell --limit 5

# Trigger a memory compile to see it in memory.md
oc compile --subscription opencontext-project

# View the result
cat memory.md
```

---

## 8. Collector Checklist

Before shipping a collector:

- [ ] `ts` is set to the Unix millisecond when the activity **occurred**, not when pushed
- [ ] No empty string values in `labels` or `payload`
- [ ] `sensitivity` is set conservatively (default to L1, only use L2/L3 with explicit config)
- [ ] Collector never blocks user workflow waiting for contextd HTTP response
- [ ] Collector handles `contextd` being down gracefully (drop or local buffer)
- [ ] Custom event types have a registered `EventTypeSchema` for LLM summarization
- [ ] Shell hooks are idempotent (running `install` twice doesn't duplicate hooks)
- [ ] README or docs describe what events are collected and at what sensitivity
