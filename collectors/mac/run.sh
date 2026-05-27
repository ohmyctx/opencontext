#!/usr/bin/env bash
# run.sh — start the macOS OpenContext collector
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
VENV="$SCRIPT_DIR/.venv"

if [[ ! -f "$VENV/bin/python" ]]; then
  echo "Virtual environment not found. Run: bash install.sh"
  exit 1
fi

exec "$VENV/bin/python" "$SCRIPT_DIR/collector.py" "$@"
