#!/usr/bin/env bash
# run.sh — start the macOS OpenContext collector
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
APP_EXEC="${HOME}/Applications/OpenContext Collector.app/Contents/MacOS/OpenContextCollector"
VENV="$SCRIPT_DIR/.venv"

if [[ -x "$APP_EXEC" ]]; then
  case "${1:-}" in
    --check-permissions|--check-permission|--prompt-permissions)
      exec "$APP_EXEC" "$@"
      ;;
  esac
  exec "$APP_EXEC" --run "$@"
fi

# Dev fallback
if [[ -f "$VENV/bin/python" ]]; then
  exec "$VENV/bin/python" "$SCRIPT_DIR/collector.py" "$@"
fi

echo "Run: bash install.sh" >&2
exit 1
