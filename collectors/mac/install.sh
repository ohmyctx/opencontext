#!/usr/bin/env bash
# install.sh — set up the macOS OpenContext collector
# Usage: bash install.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
VENV="$SCRIPT_DIR/.venv"

# ── Python ────────────────────────────────────────────────────────────────────
if command -v python3 &>/dev/null; then
  PY=$(command -v python3)
elif command -v python &>/dev/null; then
  PY=$(command -v python)
else
  echo "ERROR: Python 3.9+ is required. Install via: brew install python"
  exit 1
fi

PY_VER=$("$PY" -c "import sys; print(f'{sys.version_info.major}.{sys.version_info.minor}')")
PY_MAJOR=${PY_VER%%.*}
PY_MINOR=${PY_VER##*.}
if [[ "$PY_MAJOR" -lt 3 ]] || { [[ "$PY_MAJOR" -eq 3 ]] && [[ "$PY_MINOR" -lt 9 ]]; }; then
  echo "ERROR: Python 3.9+ required (found $PY_VER). Install via: brew install python"
  exit 1
fi
echo "Using Python $PY_VER at $PY"

# ── Virtual environment ───────────────────────────────────────────────────────
if [[ ! -d "$VENV" ]]; then
  echo "Creating virtual environment at $VENV …"
  "$PY" -m venv "$VENV"
fi

"$VENV/bin/pip" install --quiet --upgrade pip
echo "Installing dependencies …"
"$VENV/bin/pip" install --quiet -r "$SCRIPT_DIR/requirements.txt"

echo ""
echo "✓ Installation complete."
echo ""
echo "Next steps:"
echo "  1. Grant Accessibility permission:"
echo "     System Settings → Privacy & Security → Accessibility → add Terminal (or your app)"
echo ""
echo "  2. Start the collector:"
echo "     bash $SCRIPT_DIR/run.sh"
echo ""
echo "  3. (Optional) Edit config:"
echo "     mkdir -p ~/.opencontext"
echo "     cp $SCRIPT_DIR/mac-collector.example.yaml ~/.opencontext/mac-collector.yaml"
