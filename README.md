# OpenContext

> Memory beyond the chat.

OpenContext collects lightweight work signals from the tools you already use — shell, git, browser, IDE — and compresses them into a structured `memory.md` that any AI agent can read passively.

```
You: "Help me continue working on the ingester."

Without OpenContext:  Agent asks for 5 minutes of context.
With OpenContext:     Agent already knows your last 3 failed builds,
                      the commit you made 2 hours ago, and the open loop
                      from yesterday's session.
```

## Quick Start

### 1. Install

```bash
git clone https://github.com/opencontext/opencontext
cd opencontext
go build -o contextd ./cmd/contextd
go build -o oc ./cmd/oc
```

### 2. Start the daemon

```bash
contextd
# Listening on 127.0.0.1:6060
# Data stored in ~/.opencontext/events.db
```

### 3. Install shell hooks

```bash
oc collector shell install
source ~/.zshrc
```

Every command you run is now quietly recorded. High-value signals only — no passwords, no blank commands.

### 4. Configure a subscription

Create `~/.opencontext/config.yaml`:

```yaml
subscriptions:
  - name: "my-project"
    filter:
      projects: ["opencontext"]
      sources: ["shell", "git"]
      max_sensitivity: 2
    memory:
      backend: "file"
      path: "/root/code/opencontext/memory.md"
    schedule: "*/30 * * * *"
    # llm:                      # optional: uncomment for LLM summaries
    #   provider: openai
    #   model: gpt-4o-mini
    #   api_key: sk-...
```

### 5. Compile memory on demand

```bash
oc compile --subscription my-project
cat /root/code/opencontext/memory.md
```

### 6. Connect to your agent

In `CLAUDE.md` (or any agent config):

```markdown
@memory.md
```

That's it. The agent reads your work context every session.

---

## Architecture

```
Collectors ──push──▶ contextd ──store──▶ SQLite
                        │
                    (on schedule)
                        ▼
                   Memory Compiler
                        │
                  ┌─────┴──────┐
              Sessionizer   Summarizer
              (rules-based) (LLM opt-in)
                        │
                        ▼
                    memory.md ◀── Agent reads
```

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the full design.

---

## Event Protocol

Events use a Prometheus-inspired `labels + payload` schema:

```json
{
  "ts": 1748138400000,
  "source": "shell",
  "type": "command",
  "sensitivity": 1,
  "labels": { "app": "zsh", "project": "opencontext", "exit_code": "1" },
  "payload": { "command": "go build ./...", "duration_ms": 423 }
}
```

See [docs/PROTOCOL.md](docs/PROTOCOL.md) for the full specification.

---

## CLI

```bash
oc status                          # daemon health
oc events                          # recent events (last 24h)
oc events --source shell --since 2h --project myapp
oc events --query "go build"       # full-text search
oc compile                         # trigger all subscriptions
oc compile --subscription my-project
oc collector shell install         # install zsh/bash hooks
```

---

## Privacy

| Level | Default | Content |
|-------|---------|---------|
| L1 | ON | App name, command name, git repo, URL domain |
| L2 | Opt-in | Full command args, commit messages, full URLs |
| L3 | OFF | Keyboard input, full chat text, screenshots |

Commands starting with a space are never recorded (standard shell convention).

---

## Building Your Own Collector

Any language. Any tool. If it can make an HTTP POST, it's a collector.

```bash
curl -X POST http://localhost:6060/api/v1/events \
  -H "Content-Type: application/json" \
  -d '{
    "ts": 1748138400000,
    "source": "custom-tool",
    "type": "task_done",
    "sensitivity": 1,
    "labels": {"project": "myapp"},
    "payload": {"task": "deployed to staging"}
  }'
```

See [docs/COLLECTORS.md](docs/COLLECTORS.md) for the full guide.

---

## Roadmap

- [x] Shell collector + zsh/bash hooks
- [x] SQLite storage with FTS5 full-text search
- [x] Memory Compiler with tiered Hot/Warm/Cold memory
- [x] LLM summarization (OpenAI, Anthropic, Ollama)
- [x] `memory.md` file backend for passive agent consumption
- [ ] Git collector (post-commit/post-checkout hooks)
- [ ] OS activity tracker (window focus)
- [ ] Browser extension
- [ ] IDE extension (VS Code / Cursor)
- [ ] Scheduled compilation (cron)
- [ ] `mem0` backend for vector memory
- [ ] Web UI for browsing events and memory

---

## License

MIT
