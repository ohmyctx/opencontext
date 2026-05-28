# OpenContext Agent Installation Guide

> This document is for AI coding agents helping a user install OpenContext. Do not silently choose integrations for the user. Ask the questions below, then run the commands and edit the config files.

## Goal

Install one command, `oc`, then configure:

1. which collectors should capture activity;
2. which memory subscriptions should be generated;
3. which agent files should read or receive that memory.

OpenContext is local-first. The daemon listens on `http://localhost:6060` and stores data in `~/.opencontext/`.

## Collector Packaging

Use the bundled collectors first. They ship with the same `oc` binary and should be installed through `oc collector ... install`.

Recommended default: install only the collectors for tools the user actually uses.

| Collector | Install command | Use when |
|---|---|---|
| Shell | `oc collector shell install` | user wants terminal command activity |
| Claude Code | `oc collector claude install` | user uses Claude Code locally |
| Codex | `oc collector codex install` | user uses Codex CLI |
| Cursor | `oc collector cursor install` | user uses Cursor hooks |
| OpenCode | `oc collector opencode install` | user uses OpenCode |
| Chrome browser | read `collectors/browser/README.md` | user wants browser page/search/form/action activity |
| macOS activity | read `docs/COLLECTOR_INSTALL.md` | user wants app/window/click/text activity on macOS |
| Windows activity | read `docs/COLLECTOR_INSTALL.md` | user wants app/window/click/text activity on Windows |

The shell and agent hook collectors are bundled in `oc`. The macOS and Windows activity collectors are external collectors stored in this repo; install them only when the user explicitly chooses OS activity capture. Collectors are language-agnostic as long as they report OpenContext events.

## Ask The User First

Ask these questions before changing files:

1. Which activity sources should OpenContext collect?
   Suggested choices: shell, Claude Code, Codex, Cursor, OpenCode, Chrome browser, macOS activity, Windows activity.

2. Where should OpenContext memory be connected?
   Suggested choices: Claude Code, Cursor or other project agents via a project memory file, Hermes, OpenClaw, standalone `~/.opencontext/memory.md`.

3. Should memory be global or project-specific?
   Global means one memory file for all work. Project-specific means one subscription filtered to the current repo/project.

4. What privacy level should be allowed?
   Recommend L2 for useful command and agent context. Use L1 for conservative metadata-only capture. Do not enable L3 unless the user explicitly asks.

## Install `oc`

### npm

Use this when Node.js and npm are available:

```bash
npm install -g @yetanotherai/opencontext
oc --version
```

### GitHub Releases

If npm is not available, download the matching archive from:

https://github.com/yetanotherai/opencontext/releases

Expected asset names:

- `oc-v<version>-darwin-arm64.tar.gz`
- `oc-v<version>-darwin-amd64.tar.gz`
- `oc-v<version>-linux-arm64.tar.gz`
- `oc-v<version>-linux-amd64.tar.gz`
- `oc-v<version>-windows-amd64.zip`
- `oc-v<version>-windows-arm64.zip`

### Build From Source

Requires Go 1.22+:

```bash
git clone https://github.com/yetanotherai/opencontext.git
cd opencontext
make build
./bin/oc --version
```

## Start And Verify The Daemon

For a quick foreground run:

```bash
oc daemon
```

For a persistent background service, prefer:

```bash
oc daemon install
```

OpenContext service management uses:

- macOS: launchd LaunchAgent
- Linux with systemd: systemd user service, or system service when run as root
- Linux without systemd, including common WSL/container setups: pidfile-managed background process

Check service status:

```bash
oc daemon status
```

Then verify the HTTP daemon is reachable:

```bash
oc status
```

Continue only after `oc status` reports `status: ok`.

## Install Selected Collectors

The agent may inspect available collectors first:

```bash
oc collectors list
oc collectors info shell
oc collectors schemas
```

Run only the commands matching the user's choices:

```bash
oc collector shell install
oc collector claude install
oc collector codex install
oc collector cursor install
oc collector opencode install
```

If the user selected Chrome browser, read:

```text
collectors/browser/README.md
```

If the user selected macOS activity or Windows activity, stop here and read:

```text
docs/COLLECTOR_INSTALL.md
```

Then follow the platform-specific instructions in that guide.

After shell collector install, reload the shell:

```bash
source ~/.zshrc
```

If the user uses bash, reload `~/.bashrc` instead.

## Configure Subscriptions

OpenContext config lives at:

```text
~/.opencontext/config.yaml
```

Create the parent directory if needed:

```bash
mkdir -p ~/.opencontext
```

Use `backend: "raw_dump"` unless the user explicitly wants LLM summarization and has provided model credentials.

`refresh_interval` is seconds.

### Global Subscription

Use this when the user wants one memory file across all projects:

```yaml
subscriptions:
  - name: "global"
    filter:
      sources: ["shell", "claude", "codex", "cursor", "opencode"]
      max_sensitivity: 2
    memory:
      backend: "raw_dump"
      path: "~/.opencontext/memory.md"
    refresh_interval: 1800
```

Remove any sources the user did not choose.

### Project Subscription

Use this when the user wants memory scoped to one repo/project:

```yaml
subscriptions:
  - name: "<project-name>"
    filter:
      projects: ["<project-name>"]
      sources: ["shell", "claude", "codex", "cursor", "opencode"]
      max_sensitivity: 2
    memory:
      backend: "raw_dump"
      path: "<absolute-project-path>/.opencontext/memory.md"
    refresh_interval: 1800
```

Replace:

- `<project-name>` with the repo/project name OpenContext records in event labels.
- `<absolute-project-path>` with the actual project directory.
- the source list with the user's selected collectors.

## Connect Memory To Agents

Choose the matching section based on the user's answer.

### Claude Code

For project memory, add `claude_md` to the subscription:

```yaml
memory:
  backend: "raw_dump"
  path: "<absolute-project-path>/.opencontext/memory.md"
  claude_md: "<absolute-project-path>/CLAUDE.md"
```

After the next compile, OpenContext appends an `@.opencontext/memory.md` reference to `CLAUDE.md`.

### Cursor Or Other Project Agents

For agents that read project instruction files, write the generated memory path into the relevant project file.

Common choices:

```text
<absolute-project-path>/AGENTS.md
<absolute-project-path>/.cursor/rules/opencontext.md
<absolute-project-path>/CLAUDE.md
```

Add a short reference, for example:

```markdown
Read recent work context from `.opencontext/memory.md` before answering project-continuation questions.
```

If the agent supports direct file references, use the direct reference format it expects.

### Hermes

Use the command:

```bash
oc inject hermes
```

Or add the inject target manually:

```yaml
memory:
  backend: "raw_dump"
  path: "~/.opencontext/memory.md"
  inject_targets:
    - path: "~/.hermes/memories/MEMORY.md"
      header: "## OpenContext Recent Activity"
```

### OpenClaw

Use the command:

```bash
oc inject openclaw
```

Or add the inject target manually:

```yaml
memory:
  backend: "raw_dump"
  path: "~/.opencontext/memory.md"
  inject_targets:
    - path: "~/.openclaw/workspace/MEMORY.md"
      header: "## OpenContext Recent Activity"
```

### Multiple Targets

One subscription can write one memory file and inject into multiple targets:

```yaml
subscriptions:
  - name: "global"
    filter:
      sources: ["shell", "claude", "codex"]
      max_sensitivity: 2
    memory:
      backend: "raw_dump"
      path: "~/.opencontext/memory.md"
      inject_targets:
        - path: "~/.hermes/memories/MEMORY.md"
          header: "## OpenContext Recent Activity"
        - path: "~/.openclaw/workspace/MEMORY.md"
          header: "## OpenContext Recent Activity"
    refresh_interval: 1800
```

## Compile And Verify

Trigger compilation:

```bash
oc compile
```

Then verify:

```bash
oc events --since 24h
test -f ~/.opencontext/memory.md && sed -n '1,80p' ~/.opencontext/memory.md
```

For project subscriptions, check the project memory path instead.

If an inject target was configured, verify the target file contains an OpenContext section bounded by:

```html
<!-- opencontext:start -->
<!-- opencontext:end -->
```

## Final Checklist

Report these results to the user:

1. `oc --version` output.
2. `oc daemon status` result.
3. `oc status` result.
4. Installed collectors.
5. Config file path changed.
6. Subscription names created.
7. Memory file paths created.
8. Agent files updated or inject targets installed.
