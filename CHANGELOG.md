# Changelog

All notable changes to this project will be documented in this file.

## [0.4.0] - 2026-05-30

### Added
- **Git Collector**: Repository-local git hooks (post-commit, post-checkout, post-merge, post-push) that capture semantic git events — commits, branch switches, merges, and pushes — sent to daemon via `/api/v1/hooks/git`

### Changed
- **Why It Exists**: Clarified that agents have chat memory but lack knowledge of out-of-session activity — terminal commands, other agent sessions, browser history, CI builds, etc.
- **Privacy section shortened**: Collapsed detailed privacy docs into a concise section while retaining core controls

### Fixed
- **Claude Hook Dedup**: Use `/hooks/claude` as dedup key instead of full URL to prevent duplicate entries from `127.0.0.1` vs `localhost`
- **YAML Path Bug**: Bare `~` in config paths unmarshals to Go nil causing silent WriteFile failure; absolute paths now used in all docs/examples

## [0.3.0] - 2026-05-29

### Changed
- **`projects` filter replaced with `label_selectors`**: Generic label-based filtering replaces removed `projects` field
- **Default `refresh_interval`**: Changed from 1800s (30min) to 300s (5min)
- **`opencontext-project` subscription removed**: Local config no longer has project-scoped subscription pointing to repo-tracked CLAUDE.md

## [0.2.0] - 2026-05-29

### Added
- **PowerShell Shell Hooks**: Full Windows PowerShell support for shell command capture
  - PowerShell 5.1/7+ compatible `prompt()` override using `Get-History` API
  - Saves original prompt ScriptBlock before override to avoid recursion
  - Supports PS7+ `Duration`/`WorkingDirectory` with PS5.1 fallback
  - Installs to both PowerShell Core and Windows PowerShell profiles
- **Hot Config Reload**: Config file changes detected automatically via fsnotify
  - 500ms debounce to coalesce rapid file changes
  - Cancels old subscription schedulers, rebuilds compiler, restarts schedulers
  - No daemon restart needed
- **Collector Uninstall**: `oc collector <name> uninstall` for all hook types
  - `shell`, `claude`, `codex`, `cursor`, `opencode` all have uninstall commands
  - Idempotent: safe to run multiple times, only removes OpenContext entries

### Changed
- **`projects` filter replaced with `label_selectors`**: Generic label-based filtering
  - `Filter.Projects []string` removed, `Filter.LabelSelectors map[string]string` added
  - More flexible: filter by any label key=value pair
  - Project label still works via `label_selectors.project`
- **Default `refresh_interval`**: Changed from 1800s (30min) to 300s (5min)
- **Removed unused `platform` column**: `oc event list` table output no longer shows empty platform field

### Changed (Internal)
- PowerShell hook: simplified `shellHookPwsh` with `strings.Builder` removal
- `appendPwshProfile`: now writes to both PowerShell 7+ and PowerShell 5.1 profile paths

## [0.1.0] - 2026-05-29

### Added
- **Browser Extensions**: Chrome, Firefox, and Edge browser activity collectors (MV3)
  - Page visits, tab focus, link clicks, button clicks, search, form submit, text input capture
  - Redesigned options UI with open-design/simple tokens
  - Popup with status bar, event count, and per-source breakdown
  - Logo SVG with PNG icons generated for all sizes
- **macOS Collector**: macOS activity collector packaged as .app bundle
  - Click and keyboard monitoring
  - Accessibility permission diagnostics
  - Improved install experience with grant-accessibility.sh helper
- **CLI Improvements**: `oc` CLI enhanced for agent workflows
  - `oc collector browser-chrome/firefox/edge install` commands
  - Rich help output with examples for agents
  - Default JSON output for non-TTY environments
  - `--sensitivity` flag for shell hook installer

### Changed
- Migrated from `github.com/yetanotherai/opencontext` to `github.com/ohmyctx/opencontext`
- OS events now properly appear in memory.md query results
- RawDump queryEvents now queries each system source individually
- Codex hook now outputs valid JSON to stdout

### Fixed
- Code signing certificate path issue in shell hooks
- Browser extension icon display issues
- queryEvents OS events missing when no projects configured

### Contributors
- @ohmyctx - all contributions

[unreleased]: https://github.com/ohmyctx/opencontext/compare/v0.2.0...HEAD
[v0.2.0]: https://github.com/ohmyctx/opencontext/releases/tag/v0.2.0
[v0.1.0]: https://github.com/ohmyctx/opencontext/releases/tag/v0.1.0
