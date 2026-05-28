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
APP_DIR="$HOME/Applications/OpenContextCollector.app"
APP_MACOS="$APP_DIR/Contents/MacOS"
APP_RESOURCES="$APP_DIR/Contents/Resources"
APP_EXEC="$APP_MACOS/opencontext-collector"
SERVICE_EXEC="$HOME/.opencontext/bin/opencontext-mac-collector"

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

echo "Creating OpenContext Collector.app launcher …"
mkdir -p "$APP_MACOS" "$APP_RESOURCES"
if command -v clang >/dev/null 2>&1; then
  LAUNCHER_C="$APP_RESOURCES/opencontext-collector-launcher.c"
  cat > "$LAUNCHER_C" <<C
#include <unistd.h>
#include <stdio.h>
#include <stdlib.h>

int main(int argc, char *argv[]) {
  const char *workdir = "$SCRIPT_DIR";
  const char *python = "$VENV/bin/python";
  const char *collector = "$SCRIPT_DIR/collector.py";

  if (chdir(workdir) != 0) {
    perror("chdir");
    return 1;
  }

  char **args = calloc((size_t)argc + 2, sizeof(char *));
  if (args == NULL) {
    perror("calloc");
    return 1;
  }
  args[0] = (char *)python;
  args[1] = (char *)collector;
  for (int i = 1; i < argc; i++) {
    args[i + 1] = argv[i];
  }
  args[argc + 1] = NULL;

  execv(python, args);
  perror("execv");
  return 1;
}
C
  clang -Os "$LAUNCHER_C" -o "$APP_EXEC"
else
  cat > "$APP_EXEC" <<APP
#!/usr/bin/env bash
cd "$SCRIPT_DIR"
exec "$VENV/bin/python" "$SCRIPT_DIR/collector.py" "\$@"
APP
  chmod +x "$APP_EXEC"
fi
cat > "$APP_DIR/Contents/Info.plist" <<PLIST
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
  <string>opencontext-collector</string>
  <key>LSUIElement</key>
  <true/>
</dict>
</plist>
PLIST
mkdir -p "$(dirname "$SERVICE_EXEC")"
cp "$APP_EXEC" "$SERVICE_EXEC"
chmod +x "$SERVICE_EXEC"

echo ""
echo "Checking macOS Accessibility permission …"
if "$APP_EXEC" --check-permissions >/tmp/opencontext-mac-permission.json 2>/dev/null; then
  ACCESSIBILITY_OK=1
else
  ACCESSIBILITY_OK=0
fi

if [[ "$ACCESSIBILITY_OK" -eq 0 && "$PROMPT_PERMISSIONS" -eq 1 ]]; then
  echo "Opening the Accessibility permission prompt/settings page."
  "$APP_EXEC" --prompt-permissions >/tmp/opencontext-mac-permission.json 2>/dev/null || true
  open "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility" >/dev/null 2>&1 || true
  open -R "$APP_DIR" >/dev/null 2>&1 || true
fi

echo ""
echo "✓ Installation complete."
echo ""
echo "Next steps:"
if [[ "$ACCESSIBILITY_OK" -eq 0 ]]; then
  echo "  1. Grant Accessibility permission if macOS did not already show the prompt:"
  echo "     System Settings → Privacy & Security → Accessibility"
  echo "     Add and enable this app:"
  echo "     $APP_DIR"
  echo ""
  echo "     If you run the collector as a background service, enable this launcher too:"
  echo "     $SERVICE_EXEC"
  echo ""
  echo "     If macOS still lists Python separately, enable this executable as a fallback:"
  echo "     $VENV/bin/python"
  echo ""
  echo "     Verify after granting:"
  echo "     bash $SCRIPT_DIR/run.sh --check-permissions"
else
  echo "  1. Accessibility permission is already granted."
fi
echo ""
echo "  2. Start the collector:"
echo "     bash $SCRIPT_DIR/run.sh"
echo "     or: $APP_EXEC"
echo "     service launcher: $SERVICE_EXEC"
echo ""
echo "  3. (Optional) Edit config:"
echo "     mkdir -p ~/.opencontext"
echo "     cp $SCRIPT_DIR/mac-collector.example.yaml ~/.opencontext/mac-collector.yaml"
