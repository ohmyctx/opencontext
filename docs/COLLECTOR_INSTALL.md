# OpenContext Collector Installation Guide

> This document is for AI coding agents. Read it only after the user chooses which collectors to install from `INSTALL.md`.

## Collector Types

Inspect available collector manifests from `oc`:

```bash
oc collectors list
oc collectors info macos
oc collectors info windows
```

Bundled collectors are installed by the `oc` binary:

```bash
oc collector shell install
oc collector claude install
oc collector codex install
oc collector cursor install
oc collector opencode install
```

These bundled hook collectors do not require separate directories under `collectors/`.
Their install commands patch the target tool's hook configuration, and `oc daemon`
receives those hook payloads at `/api/v1/hooks/<tool>`.

Standalone activity collectors live in this repository:

- `collectors/shell`
- `collectors/browser/chrome`
- `collectors/mac`
- `collectors/windows`

They are intentionally installed only when the user asks for OS-level activity capture because they require extra dependencies and platform permissions. The current repo implementations use Python, but OpenContext's collector contract is language-agnostic.

Production collectors should push directly to the daemon with `POST /api/v1/events` or `/api/v1/events/batch`. Do not use JSONL files or bridge scripts for a normal user install. The bridge scripts in the repo are local development helpers for unusual network layouts such as WSL2 where the OS collector cannot reach the WSL daemon directly.

Browser extension collectors are installed through the browser's extension UI. For Chrome, see `collectors/browser/README.md`.

## Build Your Own Collector

A collector can be written in any language. It can run as a process, plugin, browser extension, IDE extension, scheduled task, or one-shot script. The only required contract is the OpenContext event protocol.

Send one event:

```bash
curl -X POST http://localhost:6060/api/v1/events \
  -H "Content-Type: application/json" \
  -d '{
    "source": "my_tool",
    "type": "activity",
    "sensitivity": 1,
    "labels": {"project": "my-project"},
    "payload": {"summary": "something happened"}
  }'
```

Send a batch:

```bash
curl -X POST http://localhost:6060/api/v1/events/batch \
  -H "Content-Type: application/json" \
  -d '{"events":[{"source":"my_tool","type":"activity","sensitivity":1,"payload":{"summary":"one"}}]}'
```

Recommended fields:

- `source`: stable collector/source name, such as `browser`, `ide`, or `my_tool`
- `type`: event type within that source
- `ts`: Unix milliseconds; if omitted, clients should set it before sending when possible
- `sensitivity`: `1`, `2`, or `3`
- `labels`: small indexed strings for filtering, such as `project`, `app`, `domain`
- `payload`: source-specific JSON object

For cross-platform collectors, include platform identity in labels so agents can distinguish events with the same source:

```json
{
  "source": "os",
  "type": "window_focus",
  "sensitivity": 1,
  "labels": {
    "platform": "macos",
    "collector": "opencontext-macos",
    "collector_version": "0.1.0",
    "host": "alice-macbook"
  }
}
```

Schemas are optional metadata. They document labels and payloads for humans, agents, and display code, but daemon ingestion should not depend on a source-specific schema.

## Ask Before Installing OS Collectors

Ask the user:

1. Do you want OS-level activity capture, such as active app/window, clicks, app launches, and submitted text fields?
2. Are you comfortable granting the required OS permissions?
3. Should sensitive L3 features stay disabled? Recommended answer: yes.

Do not enable clipboard capture or raw key capture unless the user explicitly asks.

## Get Collector Source

If the current machine already has an OpenContext source checkout, use it.

Otherwise clone a copy under `~/.opencontext/collectors/opencontext`:

```bash
mkdir -p ~/.opencontext/collectors
git clone --depth 1 https://github.com/ohmyctx/opencontext.git ~/.opencontext/collectors/opencontext
```

If the directory already exists, update it:

```bash
cd ~/.opencontext/collectors/opencontext
git pull --ff-only
```

## macOS Activity Collector

Use on macOS only.

### Install

```bash
cd ~/.opencontext/collectors/opencontext/collectors/mac
bash install.sh
```

This creates a local `.venv`, installs Python dependencies, and creates:

```text
~/Applications/OpenContextCollector.app
```

The app wrapper is the primary macOS permission target and is also used by the LaunchAgent background service. The installer also attempts to trigger the macOS Accessibility permission prompt and opens the matching System Settings page.

### Permissions

Ask the user to grant Accessibility permission:

```text
System Settings -> Privacy & Security -> Accessibility
```

Add and enable `~/Applications/OpenContextCollector.app`, then run `bash run.sh --check-permissions`.

If the check still reports `"accessibility": false`, macOS is applying TCC to the Python process that runs the collector. Reveal the exact Python executable and add it too:

```bash
open -R ~/.opencontext/collectors/opencontext/collectors/mac/.venv/bin/python
```

Or use the file picker shortcut `Cmd+Shift+G` and paste the full path printed by `install.sh`.

Without this permission, app launch and clipboard monitoring can still work, but window titles, browser URLs, click element names, and text input capture may be incomplete.

Verify permission status on the Mac:

```bash
bash run.sh --check-permissions
```

Prompt macOS to open the Accessibility permission flow:

```bash
bash run.sh --prompt-permissions
```

Run the prompt command from Terminal, iTerm, or Warp on the Mac. Do not rely on
a headless SSH session to request this permission; macOS may not show the TCC
prompt for SSH-launched processes. If macOS still applies TCC to Python, use
`open -R <python-path>` or `Cmd+Shift+G` in the file picker with the path printed
by `install.sh`.

Clipboard events are `sensitivity: 3`. If they are visible with `oc events --source os --max-sensitivity 3` but absent from generated memory, update the selected subscription's `filter.max_sensitivity` to `3` only after the user explicitly agrees to L3 capture.

### Run In Foreground

```bash
cd ~/.opencontext/collectors/opencontext/collectors/mac
bash run.sh
```

Debug mode:

```bash
bash run.sh --debug
```

Dry run:

```bash
bash run.sh --dry-run
```

### Run In Background With launchd

Create `~/Library/LaunchAgents/ai.opencontext.collector.mac.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>ai.opencontext.collector.mac</string>
  <key>ProgramArguments</key>
  <array>
    <string>/bin/bash</string>
    <string>-lc</string>
    <string>exec __HOME__/Applications/OpenContextCollector.app/Contents/MacOS/opencontext-collector</string>
  </array>
  <key>WorkingDirectory</key>
  <string>__HOME__</string>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>__HOME__/.opencontext/logs/mac-collector.out.log</string>
  <key>StandardErrorPath</key>
  <string>__HOME__/.opencontext/logs/mac-collector.err.log</string>
</dict>
</plist>
```

Replace:

- `__HOME__` with the user's home directory.

Then load it:

```bash
launchctl bootstrap "gui/$(id -u)" ~/Library/LaunchAgents/ai.opencontext.collector.mac.plist
launchctl kickstart -kp "gui/$(id -u)/ai.opencontext.collector.mac"
```

Stop it:

```bash
launchctl bootout "gui/$(id -u)/ai.opencontext.collector.mac"
```

## Windows Activity Collector

Use on Windows 10/11 only.

If the collector source is not already on Windows, clone it from PowerShell:

```powershell
$dst = Join-Path $env:USERPROFILE ".opencontext\collectors\opencontext"
New-Item -ItemType Directory -Force (Split-Path $dst) | Out-Null
git clone --depth 1 https://github.com/ohmyctx/opencontext.git $dst
```

### Install

Open PowerShell or Command Prompt in:

```text
%USERPROFILE%\.opencontext\collectors\opencontext\collectors\windows
```

If the repo was cloned from WSL, use a Windows-native path or clone from Windows so Python can import and run the files normally.

Run:

```bat
install.bat
```

This installs Python dependencies from `requirements.txt`.

### Run In Foreground

```bat
python collector.py
```

Debug mode:

```bat
python collector.py --debug
```

Dry run:

```bat
python collector.py --dry-run
```

### Run Silently

```bat
pythonw collector.py
```

### Run At Login With Task Scheduler

From PowerShell, in the collector directory:

```powershell
$collectorDir = (Get-Location).Path
$pythonw = (Get-Command pythonw.exe).Source
$action = New-ScheduledTaskAction -Execute $pythonw -Argument "collector.py" -WorkingDirectory $collectorDir
$trigger = New-ScheduledTaskTrigger -AtLogOn
$principal = New-ScheduledTaskPrincipal -UserId $env:USERNAME -LogonType Interactive -RunLevel Limited
Register-ScheduledTask -TaskName "OpenContext Windows Collector" -Action $action -Trigger $trigger -Principal $principal -Force
Start-ScheduledTask -TaskName "OpenContext Windows Collector"
```

Stop:

```powershell
Stop-ScheduledTask -TaskName "OpenContext Windows Collector"
```

Uninstall task:

```powershell
Unregister-ScheduledTask -TaskName "OpenContext Windows Collector" -Confirm:$false
```

## Verify Events

After installing any collector:

```bash
oc status
oc events --since 10m
```

For OS collectors:

```bash
oc events --source os --since 10m --max-sensitivity 3
```

If no events appear:

1. Confirm `oc daemon` or `oc daemon install` is running.
2. Confirm the collector process is running.
3. Confirm the collector is using `http://localhost:6060`.
4. On macOS, confirm Accessibility permission.
5. On Windows, confirm Python dependencies installed successfully.
