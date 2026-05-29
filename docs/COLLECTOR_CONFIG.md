# Collector Configuration

OpenContext collectors should keep their own configuration under platform
configuration directories, not mixed with generated memory files or runtime data.

## Preferred Paths

| Platform | Collector | Preferred config path | Legacy path |
|---|---|---|---|
| macOS | OS activity | `~/.config/opencontext/collectors/macos.yaml` | `~/.opencontext/mac-collector.yaml` |
| Windows | OS activity | `%APPDATA%\OpenContext\collectors\windows.yaml` | `%USERPROFILE%\.opencontext\windows-collector.yaml` |

Collectors read the preferred path first when it exists. If only the legacy path
exists, it is still loaded for compatibility.

## Screenshot Capture

Screenshot capture is sensitive L3 data and is disabled by default. When enabled,
collectors save image files locally and emit only an `os.screenshot` event with
`payload.path`. The daemon does not receive image bytes.

Agents may decide whether to read the local path after considering the user's
privacy settings and current task.

```yaml
collect_screenshots: false
screenshot_interval_secs: 300
screenshot_max_width: 1440
screenshot_format: "jpg"
screenshot_retention_days: 3
screenshot_max_total_mb: 1024
```

macOS default screenshot directory:

```yaml
screenshot_dir: "~/Library/Application Support/OpenContext/screenshots/macos"
```

Windows default screenshot directory:

```yaml
screenshot_dir: "%LOCALAPPDATA%\\OpenContext\\screenshots\\windows"
```

## Permissions

macOS screenshots require Screen Recording permission for
`OpenContextCollector.app`. This is separate from Accessibility permission.

Windows screenshots do not normally require an extra OS permission, but secure
desktop/UAC screens may be unavailable or blank.

## Cleanup

Collectors run cleanup after screenshot capture:

- Delete screenshots older than `screenshot_retention_days`.
- If the directory still exceeds `screenshot_max_total_mb`, delete oldest
  screenshots first.

Set either value to `0` to disable that cleanup limit.
