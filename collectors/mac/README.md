# OpenContext macOS Collector

Monitors user activity on macOS and pushes structured events to a local `contextd` daemon.

## Events captured

| Event type | Description | Sensitivity |
|---|---|---|
| `os.window_focus` | App/window in focus, URL for browsers | L1 |
| `os.browser_nav` | URL change inside Chrome/Safari/Firefox/Edge | L2 |
| `os.ui_click` | UI element clicked (name + role via Accessibility API) | L2 |
| `os.text_input` | Text submitted in input fields | L2 |
| `os.app_launch` | New application launched | L1 |
| `os.clipboard_copy` | Clipboard content changes (text/files/image metadata) | L3 |
| `os.key_press` | Individual keystrokes (opt-in, L3) | L3 |

## Requirements

- macOS 12 Monterey or later
- Python 3.9+
- **Accessibility permission** (for UI element inspection and keyboard monitoring)

## Installation

```bash
bash install.sh
```

This creates a `.venv` and installs all Python dependencies (`pyobjc`, `pynput`, etc.).

## Permissions

Go to **System Settings → Privacy & Security → Accessibility** and add your terminal
app (Terminal, iTerm2, etc.) or the collector binary.

Without this permission, UI element names in click events will be empty and
text-input capture will not work. Window focus and app launch still work without it.

## Usage

```bash
# Start collector (pushes to contextd at localhost:6060)
bash run.sh

# Debug mode (verbose logging)
bash run.sh --debug

# Dry-run mode (print JSON events, don't push)
bash run.sh --dry-run

# Custom contextd URL
bash run.sh --url http://192.168.1.10:6060
```

## Configuration

```bash
mkdir -p ~/.opencontext
cp mac-collector.example.yaml ~/.opencontext/mac-collector.yaml
# edit as needed
```

## macOS API overview

| What we monitor | macOS API |
|---|---|
| App focus changes | `NSWorkspace.didActivateApplicationNotification` |
| Window title | `AXUIElement` (Accessibility API) |
| Browser URL | `AXUIElement` address bar or `kAXURLAttribute` |
| Mouse clicks | `CGEventTap` (via `pynput`) |
| Keyboard / text fields | `AXUIElement` focused element + `CGEventTap` |
| App launches | `NSWorkspace.didLaunchApplicationNotification` |
| Clipboard | `NSPasteboard.changeCount` polling |

## Architecture

```
collector.py  ←  event Queue  ←  WindowMonitor   (NSWorkspace + AXUIElement)
                               ←  ClickMonitor    (CGEventTap via pynput)
                               ←  KeyboardMonitor (AXUIElement + pynput)
                               ←  ProcessMonitor  (NSWorkspace notifications)
                               ←  ClipboardMonitor (NSPasteboard polling)
      ↓
  ContextdClient  →  HTTP POST  →  contextd (localhost:6060)
```
