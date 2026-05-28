#!/usr/bin/env bash
# grant-accessibility.sh — guide Accessibility setup (visible app in ~/Applications)
# Usage: bash grant-accessibility.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
APP_DIR="${OPENCONTEXT_MAC_APP:-$HOME/Applications/OpenContext Collector.app}"
APP_EXEC="$APP_DIR/Contents/MacOS/OpenContextCollector"

if [[ ! -x "$APP_EXEC" ]]; then
  echo "ERROR: $APP_DIR not found. Run: bash install.sh" >&2
  exit 1
fi

# Path for the file picker (the .app bundle — visible under Applications).
PICKER_PATH="$APP_DIR"

open_accessibility_settings() {
  open "x-apple.systempreferences:com.apple.settings.PrivacySecurity.extension?Privacy_Accessibility" \
    >/dev/null 2>&1 \
    || open "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility" \
    >/dev/null 2>&1 \
    || true
}

copy_path_to_clipboard() {
  if command -v pbcopy >/dev/null 2>&1; then
    printf '%s' "$PICKER_PATH" | pbcopy
  fi
}

show_dialog() {
  /usr/bin/osascript <<EOF || true
set appPath to "$PICKER_PATH"
display dialog "OpenContext 需要「辅助功能」权限。

【推荐】在辅助功能列表里：
1. 点「打开 Finder」— 会定位到「OpenContext Collector」
2. 再点「打开设置」
3. 点 + → 左侧选「应用程序」→ 选 OpenContext Collector

【若列表里没有】点 + 后按 ⌘⇧G，粘贴路径（已复制到剪贴板）：
" & appPath & "

完成后在终端执行：
bash run.sh --check-permissions" with title "OpenContext 权限" buttons {"打开 Finder", "打开设置", "完成"} default button 1
EOF
}

echo "OpenContext — Accessibility setup"
echo ""

copy_path_to_clipboard

if [[ -z "${SSH_CONNECTION:-}${SSH_CLIENT:-}" ]]; then
  show_dialog
fi

echo "1. Finder → 应用程序 → OpenContext Collector.app"
open -R "$APP_DIR" >/dev/null 2>&1 || true
sleep 1
echo "2. 系统设置 → 隐私与安全性 → 辅助功能"
open_accessibility_settings

echo ""
echo "路径已复制到剪贴板（⌘V）。在辅助功能点 + 后按 ⌘⇧G 可粘贴："
echo "  $PICKER_PATH"
echo ""
echo "或在应用程序列表里直接选择：OpenContext Collector"
echo ""
echo "验证: cd $SCRIPT_DIR && bash run.sh --check-permissions"
echo ""

if [[ -n "${SSH_CONNECTION:-}${SSH_CLIENT:-}" ]]; then
  echo "(SSH：请在 Mac 屏幕上完成上述步骤。)"
  exit 0
fi

if "$APP_EXEC" --check-permissions; then
  echo "✓ Accessibility granted."
  exit 0
fi
exit 4
