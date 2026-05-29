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
| Shell | `oc collector shell install` | zsh/bash on Linux/macOS, PowerShell on Windows |
| Claude Code | `oc collector claude install` | user uses Claude Code locally |
| Codex | `oc collector codex install` | user uses Codex CLI |
| Cursor | `oc collector cursor install` | user uses Cursor hooks |
| OpenCode | `oc collector opencode install` | user uses OpenCode |
| Chrome browser | `oc collector browser-chrome install` | user uses Chrome and wants browser page/search/form/action activity |
| macOS activity | read `docs/COLLECTOR_INSTALL.md` | user wants app/window/click/text activity on macOS |
| Windows activity | read `docs/COLLECTOR_INSTALL.md` | user wants app/window/click/text activity on Windows |

The shell and agent hook collectors are bundled in `oc`. The Chrome collector is a browser extension that `oc` can prepare locally, but Chrome requires the user to load the unpacked extension from `chrome://extensions`. The macOS and Windows activity collectors are external collectors stored in this repo; install them only when the user explicitly chooses OS activity capture. Collectors are language-agnostic as long as they report OpenContext events.

The macOS and Windows activity collectors push directly to `oc daemon` in a normal install. Do not ask users to set up JSONL files or bridge scripts; those are local development helpers for unusual WSL2/network setups.

The macOS installer builds `~/Applications/OpenContextCollector.app`. Ask the user to add only that app in System Settings → Privacy & Security → Accessibility, then run `bash grant-accessibility.sh` and verify with `bash run.sh --check-permissions` on the Mac (not SSH).

OS collector configuration uses platform config directories:

- macOS: `~/.config/opencontext/collectors/macos.yaml`
- Windows: `%APPDATA%\OpenContext\collectors\windows.yaml`

Screenshot capture is available for macOS and Windows as `os.screenshot`, but it is L3 and disabled by default. When enabled, events contain only a local image path in `payload.path`; image bytes are not uploaded to the daemon.
For the full collector configuration policy, read `docs/COLLECTOR_CONFIG.md`.

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

If the user chooses Chrome browser, also confirm:

5. Is Google Chrome installed and actively used on this machine?
   If yes, install the Chrome collector. If the user uses Edge/Firefox instead, do not silently install Chrome; explain that Chrome is the currently supported browser extension.

Optional non-invasive Chrome checks:

```bash
command -v google-chrome || command -v google-chrome-stable || command -v chromium || true
test -d "/Applications/Google Chrome.app" && echo "chrome-macos-present"
where.exe chrome 2>/dev/null || true
```

These checks only indicate that a Chrome-like binary exists. Still ask the user whether they actually use Chrome before installing the collector.

## Agent-Friendly CLI Rules

`oc` is primarily intended to be used by AI agents. Prefer structured discovery and explicit flags:

```bash
oc schema --format json
oc schema collector browser-chrome install --format json
oc collectors list --format json
oc collectors info browser-chrome --format json
oc status --format json
```

Rules for agents:

- In non-TTY execution, `oc` defaults to JSON output. Still pass `--format json` when the surrounding workflow depends on machine-readable output, because it documents intent.
- Use `--format table` only when the user explicitly wants human-readable tables.
- OS activity events use `source: "os"` and include `labels.platform` (`macos` or `windows`) plus `labels.collector`, `labels.collector_version`, and `labels.host`.
- Clipboard events are L3. Query with `oc events --source os --max-sensitivity 3 --format json`, and only set subscription `max_sensitivity: 3` after explicit user consent.
- Use long flags only, for example `--subscription`, `--source`, `--since`, `--daemon`.
- Before running a side-effect command, inspect it with `oc schema <command...> --format json`.
- For commands with `--dry-run`, run the dry run first and show the user what will change.
- Do not scrape human help text if `oc schema` can provide the command metadata.
- Do not keep retrying an error blindly; JSON errors include a `message` and `suggestion`.

## Install `oc`

### npm

Use this when Node.js and npm are available:

```bash
npm install -g @ohmyctx/opencontext
oc --version
```

### GitHub Releases

If npm is not available, download the matching archive from:

https://github.com/ohmyctx/opencontext/releases

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
git clone https://github.com/ohmyctx/opencontext.git
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
oc collectors info browser-chrome
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

**For Windows users:** The shell collector installs PowerShell hooks automatically (no bash/zsh on Windows). PowerShell 5.1+ is required. After install, open a new PowerShell window — the hook loads via the profile and commands will be captured.

If the user selected Chrome browser and has Chrome installed, prepare the unpacked extension:

```bash
oc collector browser-chrome install --format json
```

This copies the extension to a stable local directory and prints `extension_path` plus `next_steps`. The agent must ask the user to complete the Chrome UI steps:

1. Open `chrome://extensions`.
2. Enable Developer mode.
3. Click "Load unpacked".
4. Select the printed `extension_path`.
5. Open the OpenContext extension options and confirm the daemon URL.
6. Click "Send Test Event".

Then verify:

```bash
oc events --source browser --since 10m --format json
```

If `oc collector browser-chrome install` cannot find the extension source, clone the repo and pass `--source`:

```bash
mkdir -p ~/.opencontext/collectors
git clone --depth 1 https://github.com/ohmyctx/opencontext.git ~/.opencontext/collectors/opencontext
oc collector browser-chrome install \
  --source ~/.opencontext/collectors/opencontext/collectors/browser/chrome \
  --format json
```

For detailed browser privacy behavior, read `collectors/browser/README.md`.

If the user selected macOS activity or Windows activity, stop here and read:

```text
docs/COLLECTOR_INSTALL.md
```

Then follow the platform-specific instructions in that guide.

After shell collector install, reload the shell (or open a new window):

```bash
source ~/.zshrc        # zsh
source ~/.bashrc       # bash
# Windows: just open a new PowerShell window (profile auto-loads hooks)
```

## Configure Subscriptions

OpenContext config lives at:

```text
~/.opencontext/config.yaml
```

Create the parent directory if needed:

```bash
mkdir -p ~/.opencontext
```

A full configuration reference with all options and descriptions is available at [`config.example.yaml`](config.example.yaml). Use it as a starting point.

Use `backend: "raw_dump"` unless the user explicitly wants LLM summarization and has provided model credentials.

`refresh_interval` is seconds.

**Hot reload:** Changes to `~/.opencontext/config.yaml` are picked up automatically without restarting the daemon — the new config is reloaded within 500ms and subscription schedulers are restarted.

### Global Subscription

Use this when the user wants one memory file across all work:

```yaml
subscriptions:
  - name: "global"
    filter:
      sources: ["shell", "claude", "codex", "cursor", "opencode"]
      max_sensitivity: 2
    memory:
      backend: "raw_dump"
      path: "/root/.opencontext/memory.md"
      inject_targets:
        - path: "/root/.hermes/memories/MEMORY.md"
          header: "## OpenContext Recent Activity"
    refresh_interval: 300
```

Remove any sources the user did not choose.
If the user selected Chrome browser, include `"browser"` in `sources`.

### Project Subscription (Label-Based)

Use this when the user wants memory scoped to a specific project by label:

```yaml
subscriptions:
  - name: "<project-name>"
    filter:
      label_selectors:
        project: "<project-name>"
      sources: ["shell", "claude", "codex", "cursor", "opencode"]
      max_sensitivity: 2
    memory:
      backend: "raw_dump"
      path: "<absolute-project-path>/.opencontext/memory.md"
    refresh_interval: 300
```

Replace:

- `<project-name>` with the project label value OpenContext records on events.
- `<absolute-project-path>` with the actual project directory.
- the source list with the user's selected collectors.
If the user selected Chrome browser, include `"browser"` in `sources`.

## Connect Memory To Agents

OpenContext injects memory **directly into each agent's config file** using HTML comment markers:

```
<!-- opencontext:start -->
...generated content...
<!-- opencontext:end -->
```

Only the content between the markers is replaced on each compile. Everything else in the file is preserved.
The injected content includes a hint for fetching older history via CLI when needed.

> **Important:** `memory.path` (the canonical memory file) is auto-generated and fully overwritten on every compile. Do not edit it manually. All other agent config files (CLAUDE.md, AGENTS.md, etc.) are modified only within the markers.

---

### How to choose: global vs project-level

| | Global | Project-level |
|---|---|---|
| **Memory scope** | All events, all projects | Events for one project only |
| **Agent file** | `~/.claude/CLAUDE.md` (global Claude) | `/path/to/project/CLAUDE.md` |
| **Filter** | No `label_selectors` | `label_selectors: {project: "..."}` |
| **When to use** | User wants one view of all work | User wants per-project context in that project's agent |

You can have both: one global subscription (for `~/.claude/CLAUDE.md`) and one or more project subscriptions (for each project's `CLAUDE.md`).

---

### Claude Code

Claude Code reads `CLAUDE.md` in the project directory (and all parent directories up to `~/.claude/CLAUDE.md`). Set `claude_md` to the path of the relevant file.

**Global** (memory from all projects, available in every Claude session):

```yaml
subscriptions:
  - name: "global"
    filter:
      sources: ["shell", "claude", "codex", "cursor", "opencode"]
      max_sensitivity: 2
    memory:
      backend: "raw_dump"
      path: "~/.opencontext/memory.md"
      claude_md: "~/.claude/CLAUDE.md"
    refresh_interval: 300
```

**Project-level** (memory scoped to one project, visible only when Claude runs in that project):

```yaml
subscriptions:
  - name: "my-project"
    filter:
      label_selectors:
        project: "my-project"
      sources: ["shell", "claude", "codex", "cursor", "opencode"]
      max_sensitivity: 2
    memory:
      backend: "raw_dump"
      path: "/path/to/my-project/.opencontext/memory.md"
      claude_md: "/path/to/my-project/CLAUDE.md"
    refresh_interval: 300
```

---

### Codex / OpenCode

Codex and OpenCode read `AGENTS.md` in the current directory and parent directories. Set `agents_md` to the path of the relevant file.

**Project-level** (recommended — scoped to one project):

```yaml
subscriptions:
  - name: "my-project"
    filter:
      label_selectors:
        project: "my-project"
      sources: ["shell", "claude", "codex", "cursor", "opencode"]
      max_sensitivity: 2
    memory:
      backend: "raw_dump"
      path: "/path/to/my-project/.opencontext/memory.md"
      agents_md: "/path/to/my-project/AGENTS.md"
    refresh_interval: 300
```

**Global** (if using a global AGENTS.md, e.g. `~/AGENTS.md`):

```yaml
memory:
  backend: "raw_dump"
  path: "~/.opencontext/memory.md"
  agents_md: "~/AGENTS.md"
```

---

### Cursor

Cursor reads rule files from `.cursor/rules/` in the project directory. Set `cursor_rules_dir` to the rules directory path. OpenContext will write `opencontext-memory.mdc` there on every compile (this file is fully owned by OpenContext).

**Project-level** (recommended):

```yaml
subscriptions:
  - name: "my-project"
    filter:
      label_selectors:
        project: "my-project"
      sources: ["shell", "claude", "codex", "cursor", "opencode"]
      max_sensitivity: 2
    memory:
      backend: "raw_dump"
      path: "/path/to/my-project/.opencontext/memory.md"
      cursor_rules_dir: "/path/to/my-project/.cursor/rules"
    refresh_interval: 300
```

---

### Hermes

```yaml
memory:
  backend: "raw_dump"
  path: "~/.opencontext/memory.md"
  inject_targets:
    - path: "~/.hermes/memories/MEMORY.md"
      header: "## OpenContext Recent Activity"
```

---

### OpenClaw

```yaml
memory:
  backend: "raw_dump"
  path: "~/.opencontext/memory.md"
  inject_targets:
    - path: "~/.openclaw/workspace/MEMORY.md"
      header: "## OpenContext Recent Activity"
```

---

### Combining multiple agents

One subscription can target multiple agents simultaneously:

```yaml
subscriptions:
  - name: "my-project"
    filter:
      label_selectors:
        project: "my-project"
      sources: ["shell", "claude", "codex", "cursor", "opencode"]
      max_sensitivity: 2
    memory:
      backend: "raw_dump"
      path: "/path/to/my-project/.opencontext/memory.md"
      claude_md: "/path/to/my-project/CLAUDE.md"
      agents_md: "/path/to/my-project/AGENTS.md"
      cursor_rules_dir: "/path/to/my-project/.cursor/rules"
      inject_targets:
        - path: "~/.hermes/memories/MEMORY.md"
          header: "## OpenContext Recent Activity"
    refresh_interval: 300
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
