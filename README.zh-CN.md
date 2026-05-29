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
  <a href="INSTALL.md">Agent 安装指南</a> ·
  <a href="config.example.yaml">配置参考</a> ·
  <a href="docs/PROTOCOL.md">协议文档</a> ·
  <a href="docs/COLLECTORS.md">Collector 文档</a>
</p>

<br>

<p align="center">
  <b>让每个 AI Agent 都能记住你实际做过什么。</b>
</p>

<p align="center">
  OpenContext 会采集你日常开发工具里的轻量信号，<br/>
  把它们存在本地，并生成 Agent 可直接读取的 Markdown 记忆文件，<br/>
  这样 Agent 不需要每次都问你「刚才做了什么、上下文在哪里」。
</p>

<p align="center">
  <img src="docs/images/concept.png" alt="OpenContext 架构图" width="100%"/>
</p>

```text
你说："继续早上那个 auth refactor。"

没有 OpenContext：Agent 先问你改了什么、哪里失败、从哪个文件开始。
有 OpenContext：   Agent 可以先读 memory.md，看到最近命令、失败构建、
                  提交记录、当前项目和未完成事项。
```

## 为什么需要 OpenContext

AI 编程 Agent 很强大，但大多数情况下每次新会话都记不住上次聊了什么。OpenContext 为它们构建了一个本地活动层：

- Shell 命令、Agent 提示、IDE hooks 以及更多收集器的事件流入同一个本地事件存储
- 隐私等级决定记录什么、丢弃什么
- Subscription 决定哪些项目和来源会成为 Agent 可读的记忆
- `memory.md` 可以被 Claude Code、Cursor、Hermes、OpenClaw 等 Agent 引用

## AI Agent 安装（推荐）

> **最简单的方式** — 把下面这行发给 Claude Code 或任意 AI 编程 Agent，它会自动完成整个安装和配置：

```bash
Follow https://raw.githubusercontent.com/ohmyctx/opencontext/refs/heads/main/INSTALL.md to install and configure opencontext.
```

## 手动安装

### npm（推荐）

```bash
npm install -g @ohmyctx/opencontext
oc --version
```

### GitHub Releases

从 [GitHub Releases](https://github.com/ohmyctx/opencontext/releases) 下载对应平台的压缩包：

- `oc-v<version>-darwin-arm64.tar.gz`
- `oc-v<version>-darwin-amd64.tar.gz`
- `oc-v<version>-linux-arm64.tar.gz`
- `oc-v<version>-linux-amd64.tar.gz`
- `oc-v<version>-windows-amd64.zip`

```bash
# Linux amd64
curl -L -o oc https://github.com/ohmyctx/opencontext/releases/latest/download/oc-v<version>-linux-amd64.tar.gz
tar -xzf oc-*.tar.gz
./oc --version
```

### 源码编译

需要 Go 1.22+：

```bash
git clone https://github.com/ohmyctx/opencontext.git
cd opencontext
make build
./bin/oc --version
```

## 快速开始

启动守护进程：

```bash
oc daemon
```

另开一个终端：

```bash
oc status
oc collector shell install
source ~/.zshrc    # bash 用户用 ~/.bashrc
```

创建 `~/.opencontext/config.yaml` — 完整配置参考见 [`config.example.yaml`](config.example.yaml)：

```yaml
subscriptions:
  - name: "global"
    filter:
      sources: ["shell", "claude", "codex", "cursor", "opencode"]
      max_sensitivity: 2
    memory:
      backend: "raw_dump"
      path: "/root/.opencontext/memory.md"
    refresh_interval: 300
```

编译一次并验证：

```bash
oc compile
cat ~/.opencontext/memory.md
```

常驻后台运行：

```bash
oc daemon install
oc daemon status
```

macOS 使用 launchd，Linux 优先用 systemd，没有 systemd 的环境（WSL/容器）自动降级为 pidfile 后台进程。

## Collectors

| 来源 | 安装命令 | 说明 |
|---|---|---|
| Shell | `oc collector shell install` | zsh/bash 命令历史，含隐私过滤 |
| Claude Code | `oc collector claude install` | 安装 Claude Code HTTP hooks |
| Codex | `oc collector codex install` | 安装 Codex hook adapter |
| Cursor | `oc collector cursor install` | 安装 Cursor hook adapter |
| OpenCode | `oc collector opencode install` | 安装 OpenCode hook adapter |
| Chrome 浏览器 | `oc collector browser-chrome install` | 可选扩展，需从 `chrome://extensions` 手动加载 |
| Firefox 浏览器 | `oc collector browser-firefox install` | 可选扩展，适用于 Firefox |
| Edge 浏览器 | `oc collector browser-edge install` | 可选扩展，适用于 Edge |
| macOS 活动 | 见 [Collector 安装指南](docs/COLLECTOR_INSTALL.md) | 可选外部 collector，需 Accessibility 权限 |
| Windows 活动 | 见 [Collector 安装指南](docs/COLLECTOR_INSTALL.md) | 可选外部 collector，可前台运行或接入任务计划 |

用 `oc collectors list` 和 `oc collectors info <name>` 查看 collector manifest、版本、事件来源、安装命令和 schema 引用。

## 隐私

OpenContext 遵循一个核心原则：**你的数据留在本机**。OpenContext 采集的所有内容都存储在本地，你控制哪些信号会成为 Agent 可读的记忆。

### 为什么隐私很重要

AI 编程 Agent 已深度融入日常工作流。没有隐私层的情况下，它们会默默监听：

- 每一条 Shell 命令及其参数
- 每一次提交的提示词和编辑的文件
- 浏览历史、剪贴板内容、键盘输入

OpenContext 把选择权还给你。你定义 Agent 能看到什么上下文，以及在什么粒度上看到。

### 敏感度等级

三个等级控制记录的内容。全局 `max_sensitivity` 上限阻止任何 Collector 超过配置等级采集。

| 等级 | 记录内容 | 默认 |
|---|---|---|
| **L1** | 仅 App 名、命令名、git repo、URL 域名 | 开启 |
| **L2** | 完整命令参数、commit message、完整 URL | 需选择开启 |
| **L3** | 键盘输入、完整聊天内容、截图 | 关闭 |

L3 事件（剪贴板、原始按键）除非明确授权，否则永不采集。它们对有用的 Agent 上下文并非必需，且带来显著隐私风险。

### 来源过滤

并非每个来源都和每个项目相关。每个订阅可以指定包含哪些来源：

```yaml
# 只采集 shell 和 agent 事件 — 不包含 OS/浏览器活动
sources: ["shell", "claude", "codex", "cursor", "opencode"]
```

只安装你实际使用的 Collector。如果不用 Cursor，从列表中移除 `"cursor"`。

### 标签过滤

使用 `label_selectors` 按任意键值对过滤事件：

```yaml
# 只采集标签为 project=opencontext 的事件
filter:
  label_selectors:
    project: "opencontext"
```

这让你可以把记忆限定在特定仓库或任务，不会混入无关活动。

### Shell Collector 隐私功能

- **以空格开头的命令永不记录。** 在命令前加空格，Shell collector 完全看不见。
- **按敏感度等级选择记录。** 安装时的 `--sensitivity` 标志控制 Shell collector 采集的详细程度。
- **不采集凭证。** Shell hooks 明确避免记录包含密码、Token 或 API 密钥的命令字符串。

### 数据保留

原始事件存在本地 SQLite 数据库（`~/.opencontext/`）。`retention_days` 设置控制事件保留天数，超期后每日清理：

```yaml
retention_days: 90   # 默认 90 天；0 = 不清理
```

### 订阅隔离

每个订阅相互独立。`project=myapp` 的项目订阅有独立的记忆文件，无法读取标记到其他项目的事件。这防止了跨项目上下文渗透。

### 数据流向

| 数据 | 去向 |
|---|---|
| Shell 事件 | 存入 `~/.opencontext/` SQLite DB，编译后写入你的记忆文件 |
| Agent 提示 | 通过 HTTP hook 发送到本地 daemon，走相同流程 |
| 注入记忆 | 直接写入你配置的目标文件 |
| 无 | 未经你明确配置，不会发送到外部服务器 |

OpenContext 没有云端后端。不需要账号、不做遥测、没有外部服务器——除非你配置了 LLM 提供商进行总结——即便如此，也只发送编译后的记忆（不是原始事件）。

### 隐私检查清单

- [ ] 将 `max_sensitivity` 设为 `2`（不是 `3`），除非明确需要 L3
- [ ] 只安装你实际使用的 Collector
- [ ] 使用 `label_selectors` 按项目限定记忆范围
- [ ] 在 Shell 中用空格前缀保护敏感命令
- [ ] 设置让你舒适的 `retention_days` 值
- [ ] 检查 `~/.opencontext/memory.md` 确认 Agent 会看到什么内容

## License

MIT