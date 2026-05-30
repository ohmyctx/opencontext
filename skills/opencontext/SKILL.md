---
name: opencontext
description: Use when the user asks an AI agent to inspect recent local activity, understand what they just did, recover context from shell/browser/OS/agent events, query OpenContext (`oc`) events, install or verify OpenContext collectors, or use OpenContext without injecting memory into system prompts or project files. This skill is for on-demand, privacy-aware local context retrieval through the `oc` CLI and should be used before modifying OpenContext subscriptions or reading sensitive L3 data such as screenshots, clipboard contents, raw keystrokes, or full chat text.
metadata:
  short-description: Query local OpenContext activity on demand
---

# OpenContext

OpenContext is a local-first activity context system. Use this skill when a user wants you to recover recent local context without automatically injecting memory into your system prompt or project files.

Prefer on-demand reads through the `oc` CLI. Do not install collectors, enable L3 data, or read screenshot files unless the user explicitly agrees.

## Quick Decision

Use OpenContext when the user asks things like:

- "What was I just doing?"
- "Look at my recent terminal/browser/agent activity."
- "Use local context, but do not modify my prompts."
- "Check recent events from OpenContext."
- "Read the screenshot event path if needed."

Do not use it when the user only needs normal repository inspection, code search, or git history.

## Verify Availability

Run structured checks first:

```bash
oc --version
oc status --format json
```

If `oc` is missing, tell the user OpenContext is not installed. If the daemon is down, suggest:

```bash
oc daemon status --format json
oc daemon install
```

Only start or install the daemon after the user confirms.

## Query Recent Context

Default to L1/L2 events unless the user asks for sensitive context.

```bash
oc event list --since 30m --format json
```

Useful focused queries:

```bash
oc event list --source shell --since 2h --format json
oc event list --source browser --since 1h --format json
oc event list --source os --since 30m --format json
oc event list --source claude --since 24h --format json
oc event list --source codex --since 24h --format json
oc event list --source cursor --since 24h --format json
oc event list --source opencode --since 24h --format json
oc event list --source hermes --since 24h --format json
```

OS events use `labels.platform` to distinguish machines:

```bash
oc event list --source os --since 30m --format json
```

Look for:

- `labels.platform`: `macos` or `windows`
- `labels.collector`
- `labels.host`
- `type`: `window_focus`, `browser_nav`, `ui_click`, `text_input`, `clipboard_copy`, `screenshot`

## Sensitive L3 Data

L3 includes screenshots, clipboard content, raw keystrokes, and full sensitive text. Never query or read L3 by default.

Ask first:

> OpenContext has sensitive L3 events such as screenshots and clipboard contents. Do you want me to query those for this task?

If the user agrees:

```bash
oc event list --since 30m --max-sensitivity 3 --format json
```

For screenshots, events contain only a local path:

```json
{
  "source": "os",
  "type": "screenshot",
  "sensitivity": 3,
  "payload": {
    "path": "/path/to/screenshot.jpg"
  }
}
```

Do not read the image automatically. Ask before opening or analyzing `payload.path`.

## Install Collectors

Only install collectors after asking which sources the user wants.

Discovery:

```bash
oc collector list --format json
oc collector info shell --format json
```

Common installs:

```bash
oc collector shell install
oc collector claude install
oc collector codex install
oc collector cursor install
oc collector opencode install
oc collector hermes install
oc collector browser-chrome install --format json
```

For macOS or Windows OS activity collectors, read the repository installation guide first if available:

```text
docs/COLLECTOR_INSTALL.md
docs/COLLECTOR_CONFIG.md
```

OS screenshot capture is disabled by default and should remain off unless the user explicitly opts in.

## Prefer Skill Mode When

Use this skill mode instead of subscription injection when:

- The user does not want system prompt or project memory files modified.
- The user wants local context only for the current task.
- The user wants to review what data is queried each time.
- The environment is shared, sensitive, or experimental.

Subscription injection can still be useful for continuous memory, but it is more invasive. Ask before configuring it.

## Failure Handling

If no events are returned:

1. Check daemon health: `oc status --format json`.
2. Check collectors: `oc collector list --format json`.
3. Query a wider window: `oc event list --since 24h --format json`.
4. For OS collectors, permissions may be missing.
5. For browser extensions, the user may need to load or enable the extension.

Do not fabricate activity. If OpenContext has no relevant events, say so and continue with normal context.
