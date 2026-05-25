# OpenContext Architecture

> Memory beyond the chat.  
> 把用户在浏览器、IDE、Shell、Git、IM 等工具里自然产生的工作信号，转化为 Agent 可被动感知、可查询、可总结的上下文记忆。

---

## 1. Problem Statement

Every AI Agent suffers from the same amnesia: it only knows what you tell it in the current chat window. Start a new session and it forgets everything — what project you were working on, what commands failed, what you decided yesterday.

OpenContext solves this by collecting **lightweight work signals** from the tools you already use, compressing them into a structured `memory.md`, and letting any Agent read that file passively — no API calls, no special integration required.

```
Without OpenContext                   With OpenContext
─────────────────────────────         ─────────────────────────────
User: "Help me fix this bug"          Agent already knows:
Agent: "What bug? Give me context."   - You're in the opencontext repo
                                      - Last 3 commands failed with exit 1
User: "I was working on..."           - You committed "feat: ingester" 2h ago
(repeats 5 min of context)           - Open loop: test coverage still 0%
```

---

## 2. Core Architecture

```
┌─────────────────────────────────────────────────────────────┐
│  Collectors  (independent processes, push-based)            │
│                                                             │
│  Shell Collector   Git Collector   OS Tracker   Browser Ext │
│       │                 │               │            │      │
└───────┴─────────────────┴───────────────┴────────────┴──────┘
                          │
                          │  POST /api/v1/events  (JSON, push)
                          ▼
┌─────────────────────────────────────────────────────────────┐
│  contextd  (local daemon, :6060)                            │
│                                                             │
│  ┌─────────────┐   ┌──────────────┐   ┌─────────────────┐  │
│  │  HTTP        │   │  Policy      │   │  Event Store    │  │
│  │  Ingester    │──▶│  Filter      │──▶│  (SQLite WAL)   │  │
│  │  + queue     │   │  L1/L2/L3    │   │  + FTS5         │  │
│  └─────────────┘   └──────────────┘   └────────┬────────┘  │
│                                                 │           │
│                                       (on schedule)         │
│                                                 ▼           │
│  ┌──────────────────────────────────────────────────────┐   │
│  │  Memory Compiler  (async background task)            │   │
│  │                                                      │   │
│  │  Subscription  ──▶  Sessionizer  ──▶  Summarizer    │   │
│  │  (filter rules)     (events→sessions) (LLM/noop)    │   │
│  │                                            │         │   │
│  │                                     MemoryBackend    │   │
│  └────────────────────────────────────────────┬─────────┘   │
└───────────────────────────────────────────────┼─────────────┘
                                                │
                          ┌─────────────────────┼──────────────┐
                          │                     │              │
                          ▼                     ▼              ▼
                    memory.md             mem0 API        custom API
                    (FileBackend)         (Phase 2)       (Phase 2)
                          │
                          │  Agent reads passively
                          ▼
               ┌─────────────────────┐
               │  AI Agent           │
               │  (Claude Code,      │
               │   Cursor, etc.)     │
               │                     │
               │  @memory.md in      │
               │  CLAUDE.md          │
               └─────────────────────┘
```

---

## 3. Data Flow

```
1.  User types "go build ./..."  in terminal
2.  zsh preexec hook fires
3.  Shell Collector calls: oc collector shell push --command "go build ./..."
4.  oc sends: POST http://localhost:6060/api/v1/events/batch
5.  contextd HTTP Ingester receives event, enqueues it
6.  Policy Filter checks sensitivity level, drops L3 events if not enabled
7.  Event stored to SQLite: events table (labels + payload as JSON)
8.  [30 min later] Memory Compiler wakes up
9.  Loads subscription config for "opencontext-project"
10. Queries events: source=shell, project=opencontext, since last compile
11. Sessionizer groups events into ActivitySessions (by project + time gap)
12. Summarizer calls LLM with:
    - system prompt: event type schemas (what each field means)
    - user message:  chronological event list as JSON
13. LLM returns Markdown summary
14. FileBackend writes to /root/code/opencontext/memory.md
15. User starts new Claude Code session
16. Claude reads CLAUDE.md → @memory.md → instantly knows the context
```

---

## 4. Event Protocol Design

Inspired by Prometheus labels, events use **base fields + labels + payload** rather than a flat struct. This eliminates empty optional fields: each source type only carries the labels relevant to it.

```
Prometheus:   metric_name{label=value, ...}  numeric_value  [timestamp]
OpenContext:  {source, type, ts, labels: {k:v,...}, payload: {...}}
```

| Component | Role | Analogous to |
|-----------|------|--------------|
| `source + type` | Event family identifier | Prometheus metric name |
| `labels` | Queryable dimensions (indexed) | Prometheus label set |
| `payload` | Raw data for LLM consumption | Prometheus sample value |
| `ts` | **Mandatory** Unix ms timestamp | Prometheus timestamp |
| `sensitivity` | Privacy tier (L1/L2/L3) | *(no equivalent)* |

See [PROTOCOL.md](./PROTOCOL.md) for the full event schema and field conventions.

---

## 5. Memory Compilation

The Memory Compiler runs as an async background task on a configurable schedule:

```
Events (raw, time-ordered)
        │
        ▼  Sessionizer (rules-based, no LLM)
Activity Sessions
  [10:00-10:45] coding session: opencontext, shell commands + git
  [14:00-14:30] coding session: opencontext, file edits
        │
        ▼  Summarizer (LLM or Noop)
Markdown Summary
  ## Today (2026-05-25)
  - 10:00-10:45 Implemented HTTP ingester for opencontext...
  - 14:00-14:30 Worked on SQLite schema design...
        │
        ▼  MemoryBackend
memory.md  (or mem0, or custom)
```

**Sessionizer** is purely rule-based — no LLM required:
- Groups consecutive events within the same project
- Splits sessions when there is a gap > 15 minutes (configurable)
- No LLM cost, runs on every Compiler cycle

**Summarizer** is optional and pluggable:
- `NoopSummarizer`: rule-based text rendering, zero API cost
- `LLMSummarizer`: calls OpenAI/Anthropic/Ollama to write narrative summaries
- Configured per subscription; defaults to Noop

---

## 6. Memory Layers

Memory is tiered by recency — recent events are granular, older events are compressed:

| Tier | Window | Granularity |
|------|--------|-------------|
| Hot  | Today / this week | Individual activity sessions |
| Warm | This month | Project-level summaries |
| Cold | Older | Topic / conclusion records |

The `memory.md` output structure reflects these tiers:

```markdown
# Project Memory: opencontext
> Updated: 2026-05-25 21:30

## Today
- 10:12-12:30 Implemented HTTP ingester (pkg event types, chi router)
- 14:00-15:20 Designed SQLite schema (FTS5, WAL mode)

## This Week
- [2026-05-24] Set up project structure, wrote architecture docs
- [2026-05-23] Initial concept and protocol design

## Open Loops
- [ ] Shell collector test coverage: 0% (started, not completed)
- [ ] memory.md auto-update: not yet validated end-to-end
```

---

## 7. Agent Subscription Model

Each agent or project defines a **subscription** that tells the Memory Compiler:
- Which event sources and projects to include
- Maximum sensitivity level to allow
- Which memory backend to write to
- How often to recompile
- Which LLM to use (optional)

```yaml
# ~/.opencontext/config.yaml
subscriptions:
  - name: "opencontext-project"
    filter:
      projects: ["opencontext"]
      sources: ["shell", "git", "ide"]
      max_sensitivity: 2
    memory:
      backend: "file"
      path: "/root/code/opencontext/memory.md"
    schedule: "*/30 * * * *"
    llm:
      provider: "openai"
      model: "gpt-4o-mini"

  - name: "global-daily"
    filter:
      sources: ["shell", "git", "os"]
      max_sensitivity: 1
    memory:
      backend: "file"
      path: "~/.opencontext/memory.md"
    schedule: "0 21 * * *"
```

---

## 8. Module Dependency Graph

All cross-module dependencies flow through interfaces. No circular dependencies.

```
pkg/event       ← zero dependencies, pure types + schema registry
pkg/session     ← zero dependencies, pure types
      ↑
internal/store       ← depends on pkg/event (via interface)
internal/policy      ← depends on pkg/event (via interface)
internal/sessionizer ← depends on pkg/event, pkg/session
internal/summarizer  ← depends on pkg/session (calls LLM API)
internal/memory      ← depends on pkg/session (writes MemoryContent)
internal/subscription← zero business dependencies, pure config parsing
internal/compiler    ← depends on all of the above via interfaces
internal/ingester    ← depends on store interface only
      ↑
cmd/contextd    ← wires all concrete implementations, dependency injection
cmd/oc          ← CLI, calls contextd HTTP API
collectors/shell← uses pkg/event types, calls contextd HTTP API
```

---

## 9. Technology Choices

| Concern | Choice | Rationale |
|---------|--------|-----------|
| Language | Go 1.22+ | Single binary, strong concurrency, great stdlib |
| Storage | `modernc.org/sqlite` | Pure Go (no CGO), FTS5, WAL mode, zero deploy deps |
| HTTP Router | `github.com/go-chi/chi` | Lightweight, stdlib-compatible |
| CLI | `github.com/spf13/cobra` + `viper` | Industry standard |
| LLM | Custom interface, HTTP | Not locked to any SDK; supports OpenAI/Anthropic/Ollama |
| MCP | Not in Phase 1 | `memory.md` is sufficient for passive agent awareness |

**Why SQLite over DuckDB:**
- SQLite insert throughput: ~30K rows/sec vs DuckDB ~4K rows/sec
- Event ingestion is write-heavy; analytics queries are infrequent
- FTS5 handles full-text search natively
- Single file, trivial backup

---

## 10. Privacy Design

Privacy is a first-class concern in the protocol, not an afterthought.

| Level | Label | Default | Content |
|-------|-------|---------|---------|
| L1 | Metadata | ON | App name, window title, URL domain, file path, git repo, command name |
| L2 | Structured | Opt-in | Full URL, git diff summary, DOM text, message summaries |
| L3 | Sensitive | OFF | Keyboard input, full chat content, screenshots, clipboard |

Collectors label each event with the appropriate `sensitivity` value before pushing. The Policy Filter in contextd can enforce a maximum allowed level per subscription.

---

## 11. Comparison with ActivityWatch

| Aspect | ActivityWatch | OpenContext |
|--------|--------------|-------------|
| Primary consumer | Humans (time tracking UI) | AI Agents (memory.md) |
| Data model | Buckets + events (flat) | Events with labels + payload (Prometheus-style) |
| LLM integration | None | Native (Summarizer interface) |
| Memory output | Dashboard charts | Structured Markdown memory |
| Privacy model | Basic | L1/L2/L3 protocol-level tiers |
| Agent interface | None | memory.md passive read |
| Deployment | Multi-process | Single binary |
