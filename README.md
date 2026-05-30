<p align="center">
  <img src="./docs/images/banner.svg" alt="OpenContext Banner" width="800"/>
</p>

<p align="center">
  <a href="https://github.com/ohmyctx/opencontext/releases">
    <img src="https://img.shields.io/github/v/release/ohmyctx/opencontext?include_prereleases&color=6366f1" alt="Release"/>
  </a>
  <a href="https://www.npmjs.com/package/@ohmyctx/opencontext">
    <img src="https://img.shields.io/npm/v/@ohmyctx/opencontext?logo=npm&color=0891b2" alt="npm version"/>
  </a>
  <a href="https://www.npmjs.com/package/@ohmyctx/opencontext">
    <img src="https://img.shields.io/npm/dm/@ohmyctx/opencontext?logo=npm&color=64748b" alt="npm downloads"/>
  </a>
  <a href="https://github.com/ohmyctx/opencontext/blob/main/LICENSE">
    <img src="https://img.shields.io/badge/License-MIT-yellow.svg" alt="License"/>
  </a>
</p>

<p align="center">
  <a href="./README.md">English</a> · <a href="./README.zh-CN.md">中文</a>
</p>

<p align="center">
  <a href="INSTALL.md">Agent install guide</a> ·
  <a href="config.example.yaml">Config reference</a> ·
  <a href="docs/PROTOCOL.md">Protocol</a> ·
  <a href="docs/COLLECTORS.md">Collectors</a>
</p>

<br>

<p align="center">
  <b>Never lose context. Local-first activity memory for AI agents.</b>
</p>

<p align="center">
  OpenContext watches lightweight work signals from the tools you already use,<br/>
  stores them locally, and turns them into a Markdown memory file<br/>
  that coding agents can read before they ask you to repeat context.
</p>

<p align="center">
  <img src="docs/images/concept.png" alt="OpenContext Architecture" width="100%"/>
</p>

```text
You: "Continue the auth refactor from yesterday."

Without OpenContext: the agent knows what was said in chat, but has no idea
                     what you did in the terminal, which commits you made,
                     what the CI build looked like, or what you asked Cursor
                     before switching back here.
With OpenContext:    the agent reads memory.md and knows exactly which
                     commits were pushed, which commands failed, and
                     where you left off.
```

## Why It Exists

AI coding agents have chat memory — but they don't know what happened outside the conversation. A new session doesn't know what you committed last night, which commands failed this morning, what you asked another agent, or what you were browsing.

OpenContext fills that gap:

- terminal commands, git activity, other agent sessions, browser history, and more all flow into one local event store
- privacy levels decide what is recorded and what is dropped
- subscriptions decide which sources and labels become agent-readable memory
- `memory.md` can be referenced by Claude Code, Cursor, Hermes, OpenClaw, and other agents

## Install via AI Agent (Recommended)

> **The easiest way** — Send this to Claude Code or any AI coding agent, and it will handle the entire installation and configuration for you:

```bash
Follow https://raw.githubusercontent.com/ohmyctx/opencontext/refs/heads/main/INSTALL.md to install and configure opencontext.
```

## Install Manually

### npm (Recommended)

```bash
npm install -g @ohmyctx/opencontext
oc --version
```

### GitHub Releases

Download the matching archive from [GitHub Releases](https://github.com/ohmyctx/opencontext/releases):

- `oc-v<version>-darwin-arm64.tar.gz`
- `oc-v<version>-darwin-amd64.tar.gz`
- `oc-v<version>-linux-arm64.tar.gz`
- `oc-v<version>-linux-amd64.tar.gz`
- `oc-v<version>-windows-amd64.zip`

```bash
# Linux amd64 — Stable
curl -L -o oc https://github.com/ohmyctx/opencontext/releases/latest/download/oc-v<version>-linux-amd64.tar.gz
tar -xzf oc-*.tar.gz
./oc --version
```

### Build From Source

Requires Go 1.22+:

```bash
git clone https://github.com/ohmyctx/opencontext.git
cd opencontext
make build
./bin/oc --version
```

## Quick Start

Start the daemon:

```bash
oc daemon
```

In another terminal:

```bash
oc status
oc collector shell install
oc collector git install --repo .
source ~/.zshrc    # or ~/.bashrc for bash
```

Create `~/.opencontext/config.yaml` — see [`config.example.yaml`](config.example.yaml) for all options:

```yaml
subscriptions:
  - name: "global"
    filter:
      sources: ["shell", "git", "claude", "codex", "cursor", "opencode"]
      max_sensitivity: 2
    memory:
      backend: "raw_dump"
      path: "/root/.opencontext/memory.md"
    refresh_interval: 300
```

Compile once and check the output:

```bash
oc memory compile
oc subscription list
cat ~/.opencontext/memory.md
```

Keep OpenContext running in the background:

```bash
oc daemon install
oc daemon status
```

Service management uses launchd on macOS, systemd on Linux when available, and a pidfile-managed background process in WSL/container environments without systemd.

## Collectors

| Source | Install command | Notes |
|---|---|---|
| Shell | `oc collector shell install` | zsh/bash command history with privacy filtering |
| Git | `oc collector git install --repo .` | repository-local hooks for commits, branch switches, merges, and pushes |
| Claude Code | `oc collector claude install` | installs Claude Code HTTP hooks |
| Codex | `oc collector codex install` | installs Codex hook adapter |
| Cursor | `oc collector cursor install` | installs Cursor hook adapter |
| OpenCode | `oc collector opencode install` | installs OpenCode hook adapter |
| OpenClaw | `oc collector openclaw install` | installs OpenClaw hook adapter |
| Hermes | `oc collector hermes install` | installs Hermes hook adapter |
| Chrome browser | `oc collector browser-chrome install` | optional extension — user must load from `chrome://extensions` |
| Firefox browser | `oc collector browser-firefox install` | optional extension for Firefox |
| Edge browser | `oc collector browser-edge install` | optional extension for Edge |
| macOS activity | see [docs/COLLECTOR_INSTALL.md](docs/COLLECTOR_INSTALL.md) | optional external collector |
| Windows activity | see [docs/COLLECTOR_INSTALL.md](docs/COLLECTOR_INSTALL.md) | optional external collector |

Run `oc collector list` and `oc collector info <name>` to inspect collector manifest, version, emitted sources, install command, and schema references.

## Privacy

**Your data stays on your machine.** OpenContext captures what you choose, stores it locally, and gives you granular control.

### Sensitivity Levels

| Level | What is recorded | Default |
|---|---|---|
| **L1** | App name, command name, git repo, URL domain only | On |
| **L2** | Full command arguments, commit messages, complete URLs | Opt-in |
| **L3** | Keyboard input, full chat text, screenshots | Off |

L3 is never enabled without explicit consent — it is not needed for useful agent context.

### Filtering & Isolation

- **`sources`**: include only the collectors you use (e.g. drop `"browser"` if you don't want browser activity)
- **`label_selectors`**: scope memory to a specific project or task
- **`retention_days`**: control how long raw events are kept (default: 90 days)
- **Space-prefixed commands** are never recorded by the shell collector

### What Stays Local

Shell events and agent prompts go to your local SQLite DB and compiled memory file. Nothing is sent to a remote server unless you explicitly configure an LLM provider — and only the compiled summary (not raw events) is sent then.

## License

MIT
