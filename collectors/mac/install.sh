#!/usr/bin/env bash
# install.sh — set up the macOS OpenContext collector
# Usage: bash install.sh [--no-prompt-permissions]
set -euo pipefail

PROMPT_PERMISSIONS=1
for arg in "$@"; do
  case "$arg" in
    --no-prompt-permissions)
      PROMPT_PERMISSIONS=0
      ;;
    -h|--help)
      echo "Usage: bash install.sh [--no-prompt-permissions]"
      exit 0
      ;;
    *)
      echo "ERROR: unknown argument: $arg" >&2
      echo "Usage: bash install.sh [--no-prompt-permissions]" >&2
      exit 2
      ;;
  esac
done

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
VENV="$SCRIPT_DIR/.venv"
BUILD_DIR="$SCRIPT_DIR/.build-bin"
APP_DIR="$HOME/Applications/OpenContext Collector.app"
APP_MACOS="$APP_DIR/Contents/MacOS"
APP_EXEC="$APP_MACOS/OpenContextCollector"
LEGACY_APP="$HOME/Applications/OpenContextCollector.app"

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

# Remove old app names
for old in "$LEGACY_APP" "$HOME/Applications/OpenContextCollector.app"; do
  [[ -d "$old" ]] && rm -rf "$old"
done
rm -f "$HOME/Applications/opencontext-mac-collector" 2>/dev/null || true

echo "Building OpenContext Collector.app in ~/Applications …"
rm -rf "$BUILD_DIR" "$APP_DIR"
mkdir -p "$BUILD_DIR" "$APP_MACOS"
if ! "$VENV/bin/python" -m PyInstaller \
  --noconfirm \
  --clean \
  --onefile \
  --name OpenContextCollector \
  --distpath "$BUILD_DIR/dist" \
  --workpath "$BUILD_DIR/work" \
  --specpath "$BUILD_DIR" \
  "$SCRIPT_DIR/collector.py" >/tmp/opencontext-mac-pyinstaller.log 2>&1; then
  echo "ERROR: failed to build collector" >&2
  echo "       See /tmp/opencontext-mac-pyinstaller.log" >&2
  exit 1
fi

cp "$BUILD_DIR/dist/OpenContextCollector" "$APP_EXEC"
chmod +x "$APP_EXEC"

cat > "$APP_DIR/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key>
  <string>OpenContext Collector</string>
  <key>CFBundleDisplayName</key>
  <string>OpenContext Collector</string>
  <key>CFBundleIdentifier</key>
  <string>ai.opencontext.collector.mac</string>
  <key>CFBundleVersion</key>
  <string>0.1.0</string>
  <key>CFBundleShortVersionString</key>
  <string>0.1.0</string>
  <key>CFBundleExecutable</key>
  <string>OpenContextCollector</string>
  <key>CFBundlePackageType</key>
  <string>APPL</string>
  <key>NSHighResolutionCapable</key>
  <true/>
  <key>NSAccessibilityUsageDescription</key>
  <string>OpenContext needs Accessibility to capture window titles and UI activity.</string>
</dict>
</plist>
PLIST

xattr -dr com.apple.quarantine "$APP_DIR" >/dev/null 2>&1 || true
if command -v codesign >/dev/null 2>&1; then
  codesign --force --deep --sign - "$APP_DIR" >/tmp/opencontext-mac-codesign.log 2>&1 || true
fi

echo ""
echo "Checking macOS Accessibility permission …"
ACCESSIBILITY_OK=0
if "$APP_EXEC" --check-permissions >/tmp/opencontext-mac-permission.json 2>/dev/null; then
  ACCESSIBILITY_OK=1
fi

if [[ "$ACCESSIBILITY_OK" -eq 0 && "$PROMPT_PERMISSIONS" -eq 1 ]]; then
  echo "Starting guided Accessibility setup …"
  chmod +x "$SCRIPT_DIR/grant-accessibility.sh" 2>/dev/null || true
  bash "$SCRIPT_DIR/grant-accessibility.sh" || true
  if "$APP_EXEC" --check-permissions >/tmp/opencontext-mac-permission.json 2>/dev/null; then
    ACCESSIBILITY_OK=1
  fi
fi

echo ""
echo "✓ Installation complete."
echo ""
echo "Add this app in System Settings → Privacy & Security → Accessibility:"
echo "  $APP_DIR"
echo "  (Finder → 应用程序 / Applications → OpenContext Collector)"
echo ""
if [[ "$ACCESSIBILITY_OK" -eq 0 ]]; then
  echo "Next: bash $SCRIPT_DIR/grant-accessibility.sh"
else
  echo "Accessibility permission looks OK."
fi
echo "Start: bash $SCRIPT_DIR/run.sh"
