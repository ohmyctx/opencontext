# Collector Architecture

This document defines the responsibility boundaries between collectors, hook adapters, schemas, the daemon, and the CLI.

## Concepts

### Event Protocol

`pkg/event.ActivityEvent` is the only wire contract between producers and the OpenContext daemon.

The daemon accepts any event with:

- `ts`
- `source`
- `type`
- `sensitivity`
- `labels`
- `payload`

The daemon must not require source-specific payload fields to ingest or query an event.

### Collector

A collector observes user activity and emits OpenContext events.

Examples:

- shell hook collector
- Claude Code hook collector
- macOS activity collector
- Windows activity collector

Collectors may be built into `oc`, shipped as external processes, packaged as plugins, or published separately. OpenContext does not care which language a collector uses as long as it emits the event protocol.

### Hook Adapter

A hook adapter translates a third-party hook payload into an OpenContext event.

Examples:

- Codex hook payload -> `codex.user_message`
- Cursor hook payload -> `cursor.user_message`
- OpenCode hook payload -> `opencode.user_message`

These are user-facing collector integrations, but implementation-wise they are adapters mounted inside the daemon.

### Event Schema

An event schema documents a `source.type` pair. It explains label and payload fields for:

- memory rendering
- LLM summarization
- CLI introspection
- human documentation

Schema is advisory metadata. It must not be required for ingestion.

### Collector Manifest

A collector manifest documents an installable or available collector integration:

- name
- version
- kind
- supported platforms
- emitted sources
- install commands
- schema references

The CLI can list manifests without knowing implementation details.

## Boundaries

### `pkg/event`

Owns:

- event protocol types
- event schema registry

Must not own:

- collector install logic
- daemon routes
- third-party hook parsing

### `internal/ingester`

Owns:

- generic event ingest endpoints
- queueing and persistence handoff

Must not own:

- collector installation
- CLI presentation
- collector discovery metadata
- third-party hook payload parsing

### `internal/adapters`

Owns:

- third-party hook adapters
- adapter-specific payload parsing
- translation from third-party payloads into `pkg/event.ActivityEvent`

Must not own:

- queueing or persistence
- policy filtering
- collector installation

### `cmd/oc`

Owns:

- user commands
- command-line presentation
- wiring commands to installer and service packages
- event querying presentation

Must not hard-code collector payload schemas for correctness. It may use generic schema metadata and small presentation hints.

### `internal/installers`

Owns:

- local collector and hook installation logic
- patching supported third-party hook config files
- shell startup snippets

Must not own:

- CLI parsing
- daemon runtime state
- event ingestion

### `internal/registry`

Owns:

- built-in collector manifests
- static collector discovery
- mapping from collector manifests to schema refs

Later this can be backed by dynamic manifests from `~/.opencontext/collectors.d/*.json` or daemon APIs.

## Current Implementation Notes

`claude`, `cursor`, `codex`, and `opencode` have hook adapter integrations. They do not have source directories under `collectors/` because they do not run as independent processes. Their install commands patch third-party hook config files, and the daemon exposes matching `/api/v1/hooks/...` endpoints.

`mac` and `windows` are OS activity collectors under `collectors/mac` and `collectors/windows`, installed only when the user opts into OS-level activity capture. Their current implementation is an implementation detail, not part of the collector contract.

## CLI Introspection

The CLI should expose:

```bash
oc collector list
oc collector info <name>
oc collector schemas
oc collector schemas --format json
```

This lets agents discover available integrations and their schemas without reading source code.

The daemon also exposes:

```http
GET /api/v1/collectors
GET /api/v1/schemas
```

Those endpoints return the same manifest and schema metadata to local agents that prefer daemon introspection over CLI execution.

## Event Display Rule

`oc event list` must be generic.

Preferred summary order:

1. `payload.summary`
2. `payload.message`
3. `payload.command`
4. `payload.text`
5. common label fields such as `title`, `url`, `app_name`, `app`, `project`
6. compact label/payload fallback

Collector-specific renderers can be added later as optional presentation plugins, not as core query requirements.

## Future Work

Dynamic collector registration should add:

```http
GET  /api/v1/collectors
POST /api/v1/collectors/register
GET  /api/v1/schemas
POST /api/v1/schemas
```

External collectors would register a manifest and schemas at startup. The daemon would persist them under SQLite or `~/.opencontext/collectors.d/`.

Dynamic registration should remain language-neutral. A collector may be a native binary, script, plugin, container, or process managed by another tool. The daemon only needs its manifest, optional schemas, and events reported through the OpenContext event protocol.
