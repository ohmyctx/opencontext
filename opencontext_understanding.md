# OpenContext 理解文档

## 一、项目本质

**一句话定位**：
> 把用户在浏览器、IDE、Shell、Git、IM 等工具里自然产生的工作信号，转化成 Agent 可被动感知、可查询、可总结的上下文记忆。

**更短版本**：
> 让 Agent 拥有聊天之外的记忆。

**核心解决的问题**：
> 现在所有 AI Agent 最大的问题是：它不知道你在聊天窗口之外做过什么。每次新开会话，它都是失忆的。而人的工作是连续的，但 Agent 会话是碎片的。

---

## 二、为什么这件事有意义

### 2.1 Agent 现在只懂"对话上下文"

当前 Agent 的上下文来源：
- 你在聊天里说了什么
- 你让它读了什么文件
- 你当前给了什么任务

**缺失的部分**：
- 你没告诉它，但你真实做过什么
- 你昨天做了什么
- 你在哪个项目上卡住了
- 哪些事情开始了但没闭环

### 2.2 解决了什么问题

| 场景 | 没有这套系统 | 有这套系统 |
|------|-------------|-----------|
| 新开 Claude Code | 需要重新解释上下文 | 自动知道用户最近在做什么 |
| 继续昨天的任务 | "我昨天做到哪了？" | Agent 直接回答 |
| 生成日报 | 手动回忆 | 自动基于 Git/Shell/Browser 生成 |
| 问 Agent 建议 | 泛泛而谈 | 基于真实工作流给具体建议 |

### 2.3 为什么不是"监控"

- **不靠截图**，而是轻量事件（窗口标题、URL、命令、文件路径）
- **不靠键盘监听**，而是结构化信号
- **默认本地存储**，数据归用户
- **隐私分级**，敏感内容可过滤

---

## 三、系统架构（类比 Prometheus）

```
┌─────────────────────────────────────────────────────────┐
│  Collectors / Exporters                                 │
│  ┌─────────┐ ┌─────────┐ ┌────────┐ ┌───────┐         │
│  │ Browser │ │  IDE    │ │ Shell  │ │  Git  │  ...    │
│  │Collector│ │Collector│ │Hook    │ │Collector│        │
│  └────┬────┘ └────┬────┘ └───┬────┘ └───┬───┘         │
│       │            │          │          │              │
│       └────────────┴──────────┴──────────┘              │
│                      │                                  │
│                      ▼                                  │
│              Activity Daemon                             │
│                      │                                  │
│                      ▼                                  │
│         ┌──────────────────────┐                        │
│         │  Event Store         │                        │
│         │  (SQLite/DuckDB)     │                        │
│         └──────────────────────┘                        │
│                      │                                  │
│                      ▼                                  │
│         ┌──────────────────────┐                        │
│         │  Summarizer / Rules  │                        │
│         │  (sessionize / compress) │                   │
│         └──────────────────────┘                        │
│                      │                                  │
│                      ▼                                  │
│         ┌──────────────────────┐                        │
│         │  Agent Memory Layer   │                        │
│         │  (记忆文档)           │                        │
│         └──────────────────────┘                        │
│                      │                                  │
│                      ▼                                  │
│         ┌──────────────────────┐                        │
│         │  MCP Server / API    │                        │
│         │  (供 Agent 调用)     │                        │
│         └──────────────────────┘                        │
└─────────────────────────────────────────────────────────┘
```

### 数据流

```
用户操作
  → Collector 采集（JSON 格式）
  → 本地 Daemon 接收
  → 存储原始事件到时序数据库
  → 定时压缩汇总（规则引擎 → LLM 总结）
  → 生成记忆文档（Markdown）
  → Agent 可通过 MCP 调用查询
```

### Prometheus 类比

| Prometheus 世界 | OpenContext 世界 |
|----------------|----------------|
| Exporter 采集指标 | Collector 采集工作事件 |
| Prometheus 定期 scrape | 本地 Daemon 收集 |
| Time-series DB | Activity event store |
| PromQL 查询 | ActivityQL / SQL 查询 |
| Alertmanager 告警 | Agent 主动提醒/总结 |
| Grafana 仪表盘 | Timeline / Daily Review UI |

---

## 四、记忆的分层设计

### 4.1 时间维度（纵向）

越近的记忆越清晰，越久远越模糊：

```
当前记忆（热）    → 今天/本周，细粒度事件
短期记忆（温）    → 本月，项目级别摘要
长期记忆（冷）    → 更早，主题/结论式记录
```

### 4.2 横向维度

- **个人记忆**：用户自己的工作上下文
- **集体记忆**：团队/公司共享的项目上下文
- **Agent 记忆**：Agent 自身产生的认知

### 4.3 敏感分级

| Level | 内容 | 默认 |
|-------|------|------|
| L1 元信息 | App名、窗口标题、URL、文件路径 | ✅ 开启 |
| L2 结构化内容 | Git diff摘要、DOM标题、消息摘要 | ⚠️ 需授权 |
| L3 高敏感 | 键盘输入、完整聊天、截图、剪贴板 | ❌ 关闭 |

---

## 五、技术实现要点

### 5.1 事件结构设计

```go
type ActivityEvent struct {
    ID        string                 // 事件ID
    Time      time.Time              // 时间戳
    Source    string                 // os, browser, ide, shell, git, im
    Type      string                 // focus, page_view, command, file_edit, commit
    App       string                 // 应用名
    Title     string                 // 窗口/页面标题
    URL       string                 // 网址
    Path      string                 // 文件路径
    Repo      string                 // Git仓库
    Project   string                 // 项目名
    Text      string                 // 摘要文本
    Metadata  map[string]any         // 额外元数据
    Sensitive bool                   // 是否敏感
}
```

### 5.2 收集器设计

每个数据源一个 Collector（类比 Prometheus Exporter）：

```
browser_collector     → URL、页面标题、点击元素、表单提交
vscode_collector      → 打开文件、workspace、terminal命令
shell_collector       → pwd、命令、exit code、duration
git_collector         → commit、branch、diff
os_window_collector   → 前台App、窗口标题、切换时间
im_collector          → 日历事件、IM元信息
```

### 5.3 汇总策略

```
实时事件  → 本地存储（便宜）
5分钟聚合 → 规则引擎（不用 LLM）
30分钟摘要 → 小模型或本地模型
每日总结  → 大模型（按需调用）
```

### 5.4 Agent 接入方式

两种主流方式：

1. **MCP Server**：Agent 通过 MCP 工具调用查询记忆
2. **Prompt 注入**：将最新记忆 append 到用户 prompt 中

---

## 六、名字选择

经过讨论，推荐的名字优先级：

| 排名 | 名字 | 感觉 | Slogan |
|------|------|------|--------|
| 1 | **OpenContext** | 最像开源基础设施，和 OpenHuman 一个气质 | Human context for AI agents |
| 2 | **SecondContext** | 最贴"聊天之外的第二上下文" | Memory beyond the chat |
| 3 | **OuterMind** | 最有品牌感，外部心智 | The memory layer outside the chat |
| 4 | **HumanContext** | 直白高级，人类上下文层 | Give agents memory beyond the chat |

---

## 七、核心价值总结

**不是监控，而是：**

> 把人的连续工作流，转化为 Agent 可持续读取的上下文记忆。

**三个最强场景**：

1. **新会话不失忆**：新建 Claude Code 会话，Agent 自动知道昨天做了什么
2. **任务可恢复**：中断的工作可以接着做，不用重新解释背景
3. **自动日报/总结**：基于真实工作行为生成工作总结

**一句话 Slogan**：

```
Memory beyond the chat.
让 Agent 拥有聊天之外的记忆。
```

---

## 八、和现有工具的区别

| 工具类型 | 目标用户 | OpenContext 的差异 |
|----------|----------|-------------------|
| 时间追踪 (ActivityWatch/WakaTime) | 给人看统计 | 给 Agent 看上下文 |
| 截图回放 | 回看画面 | 结构化事件，无画面 |
| 笔记/知识库 | 人自己读 | Agent 可查询理解 |
| 普通 Memory DB | 存储 | 采集+压缩+主动触发 |

---

## 九、风险与挑战

1. **用户信任**：必须强调 local-first、隐私分级、无截图无键盘监听
2. **安装成本**：如果安装太重，用户不愿意用
3. **价值感知**：需要快速展示"新会话接上上下文"的惊艳感
4. **采集质量**：事件太多是噪音，需要智能过滤和聚合
