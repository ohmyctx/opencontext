# OpenContext macOS Collector

Monitors user activity on macOS and pushes structured events to the local OpenContext daemon.

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

This creates a `.venv`, installs all Python dependencies (`pyobjc`, `pynput`, etc.),
and creates `~/Applications/OpenContextCollector.app` as a stable permission
target. It also attempts to show the macOS Accessibility prompt and opens the
matching System Settings page.

## Permissions

Go to **System Settings → Privacy & Security → Accessibility** and add:

```text
~/Applications/OpenContextCollector.app
```

Then run `bash run.sh --check-permissions`. If it still reports
`"accessibility": false`, macOS is applying the permission to the Python process.
Run this command to reveal the exact executable in Finder, then drag or add it
from the Accessibility picker:

```bash
open -R "$PWD/.venv/bin/python"
```

In the file picker you can also press `Cmd+Shift+G` and paste the Python path
printed by `install.sh`.

Without this permission, app launch and clipboard monitoring can still work, but
window titles, browser URLs, UI element names, and text-input capture may be incomplete.

Check permission status from the Mac:

```bash
bash run.sh --check-permissions
```

Ask macOS to show the Accessibility prompt:

```bash
bash run.sh --prompt-permissions
```

Run the prompt command from Terminal or iTerm on the Mac. A collector started
from a headless SSH session may not be able to display the macOS permission
prompt. If the collector runs via LaunchAgent, grant Accessibility access to
the terminal app used during setup and, if macOS shows it separately, the
`~/Applications/OpenContextCollector.app`. This is also the executable used by the background LaunchAgent. If the permission check is still false, reveal and enable the Python executable shown by `install.sh`.

Clipboard events are captured through `NSPasteboard` and normally do not require
Accessibility permission, but they are L3 events. They only appear in generated
memory when the selected subscription allows `max_sensitivity: 3`.

## Usage

```bash
# Start collector (pushes to oc daemon at localhost:6060)
bash run.sh

# Start through the permission-friendly app wrapper
"$HOME/Applications/OpenContextCollector.app/Contents/MacOS/opencontext-collector"

# Debug mode (verbose logging)
bash run.sh --debug

# Dry-run mode (print JSON events, don't push)
bash run.sh --dry-run

# Custom OpenContext daemon URL
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
  ContextClient  →  HTTP POST  →  oc daemon (localhost:6060)
```

Every event includes platform labels such as `platform=macos`,
`collector=opencontext-macos`, `collector_version`, and `host`.
