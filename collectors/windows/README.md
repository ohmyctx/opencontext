# OpenContext — Windows UI Activity Collector

监听 Windows 桌面用户行为，将结构化事件推送到本地 OpenContext daemon。

## 采集的事件

| 事件 | source | type | 灵敏度 | 内容 |
|------|--------|------|--------|------|
| 前台窗口切换 | `os` | `window_focus` | L1 | app 名称、窗口标题、上一个 app |
| 鼠标点击 | `os` | `ui_click` | L2 | 控件名称、控件类型、坐标、窗口标题 |
| 文本输入提交 | `os` | `text_input` | L2 | 输入框内容（Enter/Tab 触发），跳过密码框 |
| 进程启动 | `os` | `app_launch` | L1 | 程序名、可执行路径 |
| 剪贴板复制 | `os` | `clipboard_copy` | **L3** | 文本、文件列表或图片元数据 |
| 屏幕截图 | `os` | `screenshot` | **L3** | 本地图片路径，不上传图片内容 |
| 按键（可选） | `os` | `key_press` | **L3** | 单个按键名，需显式开启，永远跳过密码框 |

剪贴板是 L3。只有在用户明确同意 L3 后，才把订阅的 `max_sensitivity` 调到 `3`。

---

## 前置条件

- **Windows 10 / 11**
- **Python 3.10+** — 从 [python.org](https://python.org) 安装，安装时勾选 "Add Python to PATH"
- **OpenContext daemon 运行中** — 监听 `http://localhost:6060`

---

## 快速开始

```bat
:: 1. 安装依赖（只需执行一次）
install.bat

:: 2. 启动采集器（前台模式，Ctrl+C 停止）
python collector.py

:: 3. 后台静默运行（无控制台窗口）
pythonw collector.py
```

验证事件是否到达：

```bat
curl http://localhost:6060/api/v1/events?source=os&since=1m
```

---

## 命令行参数

```
python collector.py [选项]

  --url URL          OpenContext daemon 地址（默认 http://localhost:6060）
  --config PATH      YAML 配置文件路径
  --dry-run          将事件打印为 JSON，不推送（调试用）
  --debug            启用详细日志
  --no-clicks        禁用鼠标点击监控
  --no-keys          禁用键盘/文本输入监控
  --no-processes     禁用进程启动监控
```

---

## 配置文件

推荐配置文件位置：`%APPDATA%\OpenContext\collectors\windows.yaml`

旧路径仍兼容：`%USERPROFILE%\.opencontext\windows-collector.yaml`

```yaml
# OpenContext daemon 地址
daemon_url: http://localhost:6060

# 事件推送间隔（秒）
flush_interval: 5.0

# 前台窗口轮询间隔（秒）
window_poll_interval: 0.2

# 进程轮询间隔（秒）
process_poll_interval: 1.0

# 默认灵敏度（1=仅元数据, 2=结构内容, 3=敏感内容）
sensitivity: 2

# 捕获文本框提交内容（Enter/Tab 触发）—— L2，推荐开启
collect_text_input: true

# 捕获每个按键 —— L3，需要显式开启
collect_raw_keys: false

# 捕获剪贴板 —— L3
collect_clipboard: true

# 周期截图 —— L3，默认关闭。事件只上报本地图片路径，不上传图片内容。
collect_screenshots: false
screenshot_interval_secs: 300
screenshot_dir: "%LOCALAPPDATA%\\OpenContext\\screenshots\\windows"
screenshot_max_width: 1440
screenshot_format: "jpg"
screenshot_retention_days: 3
screenshot_max_total_mb: 1024

# 截图文件只保存在本机，事件里只包含 payload.path。
# agent 可以在需要视觉上下文时再读取该路径。

# 完全忽略的 App（exe 名称）
ignore_apps:
  - LockApp.exe
  - ScreenClippingHost.exe

# 窗口标题包含以下字符串时跳过
ignore_window_titles:
  - "密码"
  - "Password"
  - "登录"
```

---

## 技术方案

### 语言选择：Python

Python 是本 collector 的最优选择：
- **pywin32** 提供完整的 Win32 API 访问（窗口句柄、进程信息）
- **uiautomation** 封装了 Windows UIAutomation COM 接口，可读取任意控件的名称、类型、值
- **pynput** 提供全局鼠标/键盘钩子，无需管理员权限
- **psutil** 跨平台进程监控

正常用户安装时 collector 直接通过 HTTP 推送到 `oc daemon`，不需要 JSONL 文件或 bridge。
每个事件都会带上 `platform=windows`、`collector=opencontext-windows`、
`collector_version` 和 `host` 等 label，agent 可以用这些字段区分 Windows 与 macOS。

### 架构

```
collector.py (主线程，定时 flush)
  ├── WindowMonitor   (daemon thread) ── SetWinEventHook → os.window_focus
  ├── ClickMonitor    (pynput thread) ── 鼠标钩子 + UIAutomation → os.ui_click
  ├── KeyboardMonitor (pynput thread) ── 键盘钩子 + UIAutomation → os.text_input
  └── ProcessMonitor  (daemon thread) ── psutil 轮询 → os.app_launch
                                   ↓
                              Queue（线程安全）
                                   ↓
                    ContextClient.push_batch() → oc daemon
```

### 关键实现细节

- **WindowMonitor**：优先使用 `SetWinEventHook(EVENT_SYSTEM_FOREGROUND)` 事件驱动，失败时退化为轮询
- **ClickMonitor**：pynput 捕获点击坐标 → `uiautomation.ControlFromPoint(x, y)` 获取控件信息
- **KeyboardMonitor**：不记录逐键击，而是在 Enter/Tab 时读取当前焦点控件的 Value（已提交文本）；密码框（`IsPassword == True`）始终跳过
- **COM 线程安全**：每个使用 UIAutomation 的线程调用 `CoInitialize()` 初始化 COM STA
- **防崩溃**：所有监控操作都包裹在 `try/except` 中，任何错误只记录 debug 日志，不影响其他监控或主流程

---

## 禁用/卸载

停止采集器：按 `Ctrl+C`，或结束 `python.exe` / `pythonw.exe` 进程。

该 collector 不修改系统注册表、不安装服务，停止进程即可完全停用。

---

## 隐私说明

- **默认不采集**：密码、剪贴板、原始按键流
- **密码框保护**：通过 UIAutomation `IsPassword` 属性自动跳过
- **文本输入**（L2）：仅在用户按 Enter/Tab 提交时读取文本框当前值，不记录实时输入过程
- **L3 内容**需在配置中显式开启，并建议在 README 中告知使用者

---

## 调试

```bat
:: 打印事件但不推送
python collector.py --dry-run

:: 查看详细日志
python collector.py --debug

:: 只监控窗口切换，关闭其他监控
python collector.py --no-clicks --no-keys --no-processes
```

示例输出（`--dry-run`）：

```json
{"ts": 1748200000000, "source": "os", "type": "window_focus", "sensitivity": 1, "labels": {"app": "chrome.exe"}, "payload": {"title": "Google - Chrome", "pid": 1234, "prev_app": "notepad.exe"}}
{"ts": 1748200005000, "source": "os", "type": "ui_click",     "sensitivity": 2, "labels": {"app": "notepad.exe", "control_type": "Button"}, "payload": {"button": "left", "x": 540, "y": 300, "control_name": "OK", "window_title": "保存"}}
{"ts": 1748200010000, "source": "os", "type": "text_input",   "sensitivity": 2, "labels": {"app": "chrome.exe", "control_type": "Edit"}, "payload": {"text": "opencontext github", "trigger": "enter", "window_title": "New Tab - Chrome"}}
{"ts": 1748200015000, "source": "os", "type": "app_launch",   "sensitivity": 1, "labels": {"app": "notepad.exe"}, "payload": {"pid": 5678, "exe": "C:\\Windows\\notepad.exe"}}
```
