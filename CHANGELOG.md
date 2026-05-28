# Changelog

All notable changes to this project will be documented in this file.

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

[unreleased]: https://github.com/ohmyctx/opencontext/compare/v0.1.0...HEAD
[v0.1.0]: https://github.com/ohmyctx/opencontext/releases/tag/v0.1.0