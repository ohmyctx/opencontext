"""macOS helper utilities: AXUIElement wrappers and foreground app info.

Uses the Accessibility API (AXUIElement) via pyobjc-framework-ApplicationServices.
Requires Accessibility permission:
  System Settings → Privacy & Security → Accessibility → allow this app.
"""

from __future__ import annotations

import logging
import threading
from typing import Optional

logger = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Lazy imports — keep module importable even if pyobjc is not installed yet
# ---------------------------------------------------------------------------
_ax_ok = False
try:
    from ApplicationServices import (  # type: ignore
        AXUIElementCreateSystemWide,
        AXUIElementCreateApplication,
        AXUIElementCopyAttributeValue,
        kAXErrorSuccess,
        kAXFocusedUIElementAttribute,
        kAXRoleAttribute,
        kAXValueAttribute,
        kAXTitleAttribute,
        kAXURLAttribute,
        kAXFocusedWindowAttribute,
        kAXTextFieldRole,
        kAXTextAreaRole,
        kAXComboBoxRole,
        kAXDescriptionAttribute,
        kAXChildrenAttribute,
    )
    from AppKit import NSWorkspace  # type: ignore

    # These constants are missing in some pyobjc versions — use string literals
    kAXPasswordFieldRole = "AXSecureTextField"
    kAXWebAreaRole = "AXWebArea"
    kAXSearchFieldRole = "AXSearchField"

    _ax_ok = True
except ImportError as e:
    logger.warning("pyobjc not available — AX helpers disabled: %s", e)

# Browser bundle IDs whose address bar we can read via AX
_BROWSER_BUNDLES = {
    "com.google.Chrome",
    "com.google.Chrome.canary",
    "org.mozilla.firefox",
    "com.apple.Safari",
    "com.microsoft.edgemac",
    "com.brave.Browser",
    "com.operasoftware.Opera",
}

# AX roles that are considered text input fields
_TEXT_ROLES = {kAXTextFieldRole, kAXTextAreaRole, kAXComboBoxRole, kAXWebAreaRole, kAXSearchFieldRole} if _ax_ok else set()

_name_cache: dict[int, str] = {}  # pid → friendly app name
_cache_lock = threading.Lock()


def check_accessibility_permission() -> bool:
    """Return True if Accessibility permission is granted (in-process TCC cache)."""
    if not _ax_ok:
        return False
    try:
        from ApplicationServices import AXIsProcessTrustedWithOptions  # type: ignore
        return bool(AXIsProcessTrustedWithOptions(None))
    except Exception:
        # Older API fallback
        try:
            from ApplicationServices import AXAPIEnabled  # type: ignore
            return bool(AXAPIEnabled())
        except Exception:
            return False


def check_accessibility_functional() -> bool:
    """Probe live TCC via CGEventTap creation (less cache-prone than AXIsProcessTrusted)."""
    if not _ax_ok:
        return False
    try:
        from Quartz import (  # type: ignore
            CGEventTapCreate,
            CGEventMaskBit,
            kCGHIDEventTap,
            kCGHeadInsertEventTap,
            kCGEventTapOptionListenOnly,
            kCGEventKeyDown,
        )

        def _callback(proxy, etype, event, refcon):  # noqa: ARG001
            return event

        mask = CGEventMaskBit(kCGEventKeyDown)
        tap = CGEventTapCreate(
            kCGHIDEventTap,
            kCGHeadInsertEventTap,
            kCGEventTapOptionListenOnly,
            mask,
            _callback,
            None,
        )
        if tap is not None:
            from Quartz import CFMachPortInvalidate, CGEventTapEnable  # type: ignore

            CGEventTapEnable(tap, False)
            CFMachPortInvalidate(tap)
            return True
        return False
    except Exception as e:
        logger.debug("accessibility functional probe failed: %s", e)
        return False


def get_session_context() -> str:
    """Return coarse session type: gui, ssh, or unknown."""
    import os

    if os.environ.get("SSH_CONNECTION") or os.environ.get("SSH_CLIENT"):
        return "ssh"
    try:
        import subprocess

        out = subprocess.run(
            ["/usr/bin/launchctl", "managername"],
            capture_output=True,
            text=True,
            timeout=3,
        )
        name = (out.stdout or "").strip()
        if name.startswith("gui/"):
            return "gui"
        if name.startswith("user/"):
            return "background"
    except Exception:
        pass
    return "unknown"


def get_permission_diagnostics() -> dict:
    """Return context for Accessibility (TCC) troubleshooting."""
    import os
    import subprocess
    import sys

    session = get_session_context()
    trusted = check_accessibility_permission()
    functional = check_accessibility_functional()
    diag: dict = {
        "executable": sys.executable,
        "session": session,
        "accessibility_trusted": trusted,
        "accessibility_functional": functional,
        "accessibility": trusted,
        "pyobjc_available": _ax_ok,
        "launched_via_ssh": session == "ssh",
    }

    if sys.platform != "darwin":
        return diag

    try:
        from Foundation import NSBundle  # type: ignore

        bundle = NSBundle.mainBundle()
        if bundle is not None:
            diag["bundle_id"] = bundle.bundleIdentifier() or ""
            diag["bundle_path"] = bundle.bundlePath() or ""
    except Exception:
        pass

    try:
        proc = subprocess.run(
            ["codesign", "-dv", sys.executable],
            capture_output=True,
            text=True,
            timeout=5,
        )
        for line in (proc.stderr + proc.stdout).splitlines():
            line = line.strip()
            if line.startswith("Identifier="):
                diag["codesign_identifier"] = line.split("=", 1)[1]
            elif line.startswith("TeamIdentifier="):
                diag["codesign_team"] = line.split("=", 1)[1]
            elif "CDHash" in line:
                diag["codesign_cdhash"] = line.split("=", 1)[-1].strip()
    except Exception:
        pass

    app_path = os.path.expanduser("~/Applications/OpenContext Collector.app")
    diag["permission_target_hint"] = (
        "Add OpenContext Collector in System Settings → Privacy & Security → "
        f"Accessibility (Applications folder): {app_path}"
    )

    if session == "ssh":
        diag["ssh_note"] = (
            "This process runs in a non-GUI SSH session. In-process checks often "
            "return false even when OpenContextCollector.app is enabled. "
            "Verify with bash run.sh --check-permissions on the Mac."
        )

    return diag


def prompt_accessibility_permission() -> bool:
    """Prompt for Accessibility permission and return current trust state.

    macOS only shows the system prompt from a GUI-capable user session. When the
    collector is launched from SSH, this usually returns False without a useful
    prompt; run it from Terminal/iTerm on the Mac when onboarding permissions.
    """
    if not _ax_ok:
        return False
    try:
        from ApplicationServices import AXIsProcessTrustedWithOptions  # type: ignore
        opts = {"AXTrustedCheckOptionPrompt": True}
        return bool(AXIsProcessTrustedWithOptions(opts))
    except Exception:
        return check_accessibility_permission()


def get_frontmost_app() -> Optional[dict]:
    """Return info about the currently frontmost application.

    Returns a dict with keys: app, bundle_id, pid, title, url (for browsers).
    Returns None if the information cannot be retrieved.
    """
    if not _ax_ok:
        return None
    try:
        ws = NSWorkspace.sharedWorkspace()
        front = ws.frontmostApplication()
        if front is None:
            return None

        app_name: str = front.localizedName() or ""
        bundle_id: str = front.bundleIdentifier() or ""
        pid: int = front.processIdentifier()

        title = _get_window_title(pid)
        url = _get_browser_url(pid, bundle_id) if bundle_id in _BROWSER_BUNDLES else ""

        return {
            "app": app_name,
            "bundle_id": bundle_id,
            "pid": pid,
            "title": title,
            "url": url,
        }
    except Exception as e:
        logger.debug("get_frontmost_app error: %s", e)
        return None


def get_focused_field() -> Optional[dict]:
    """Return the currently focused text field's value and metadata.

    Returns None if focus is not on a text input or value is empty.
    Automatically skips password fields.
    """
    if not _ax_ok:
        return None
    try:
        system = AXUIElementCreateSystemWide()
        err, focused = AXUIElementCopyAttributeValue(
            system, kAXFocusedUIElementAttribute, None
        )
        if err != kAXErrorSuccess or focused is None:
            return None

        # Role check
        err, role = AXUIElementCopyAttributeValue(focused, kAXRoleAttribute, None)
        if err != kAXErrorSuccess:
            return None

        if role == kAXPasswordFieldRole:
            return None  # never capture passwords

        if role not in _TEXT_ROLES:
            return None

        # Extract value
        err, value = AXUIElementCopyAttributeValue(focused, kAXValueAttribute, None)
        text = str(value) if err == kAXErrorSuccess and value else ""

        # Element title / label
        err, title_val = AXUIElementCopyAttributeValue(focused, kAXTitleAttribute, None)
        field_label = str(title_val) if err == kAXErrorSuccess and title_val else ""

        # App context
        fg = get_frontmost_app()

        return {
            "role": str(role),
            "text": text,
            "field_label": field_label,
            "app": fg["app"] if fg else "",
            "bundle_id": fg["bundle_id"] if fg else "",
            "window_title": fg["title"] if fg else "",
        }
    except Exception as e:
        logger.debug("get_focused_field error: %s", e)
        return None


# ---------------------------------------------------------------------------
# Internal helpers
# ---------------------------------------------------------------------------

def _get_window_title(pid: int) -> str:
    """Read the focused window title for the given process via AX."""
    try:
        app_el = AXUIElementCreateApplication(pid)
        err, win = AXUIElementCopyAttributeValue(app_el, kAXFocusedWindowAttribute, None)
        if err != kAXErrorSuccess or win is None:
            return ""
        err, title = AXUIElementCopyAttributeValue(win, kAXTitleAttribute, None)
        return str(title) if err == kAXErrorSuccess and title else ""
    except Exception:
        return ""


def _get_browser_url(pid: int, bundle_id: str) -> str:
    """Read the current URL from a browser's address bar via AX."""
    try:
        # Chrome / Edge / Brave: look for kAXURLAttribute on the web area,
        # or fall back to reading the address bar text field value.
        app_el = AXUIElementCreateApplication(pid)
        err, win = AXUIElementCopyAttributeValue(app_el, kAXFocusedWindowAttribute, None)
        if err != kAXErrorSuccess or win is None:
            return ""

        # Try kAXURLAttribute directly on the window (Safari uses this)
        err, url_val = AXUIElementCopyAttributeValue(win, kAXURLAttribute, None)
        if err == kAXErrorSuccess and url_val:
            url = str(url_val)
            return _strip_scheme(url)

        # Chrome/Edge/Firefox: find the address bar text field by searching
        # children of the toolbar group (heuristic — role=AXTextField, title contains "Address")
        url = _find_address_bar(win)
        return _strip_scheme(url) if url else ""
    except Exception as e:
        logger.debug("_get_browser_url error: %s", e)
        return ""


def _find_address_bar(win_element) -> str:
    """Recursively search for an address bar AXTextField in the window."""
    from ApplicationServices import (  # type: ignore
        AXUIElementCopyAttributeValue,
        kAXChildrenAttribute,
        kAXRoleAttribute,
        kAXTitleAttribute,
        kAXValueAttribute,
        kAXErrorSuccess,
    )
    _ADDRESS_TITLES = {"address and search bar", "address bar", "location", "url"}

    def _search(el, depth: int) -> str:
        if depth > 6:
            return ""
        err, role = AXUIElementCopyAttributeValue(el, kAXRoleAttribute, None)
        if err != kAXErrorSuccess:
            return ""
        role_str = str(role) if role else ""

        if role_str == kAXTextFieldRole:
            err, title = AXUIElementCopyAttributeValue(el, kAXTitleAttribute, None)
            title_str = str(title).lower() if err == kAXErrorSuccess and title else ""
            if any(a in title_str for a in _ADDRESS_TITLES):
                err, val = AXUIElementCopyAttributeValue(el, kAXValueAttribute, None)
                return str(val) if err == kAXErrorSuccess and val else ""

        err, children = AXUIElementCopyAttributeValue(el, kAXChildrenAttribute, None)
        if err != kAXErrorSuccess or not children:
            return ""
        for child in children:
            result = _search(child, depth + 1)
            if result:
                return result
        return ""

    return _search(win_element, 0)


def _strip_scheme(url: str) -> str:
    """Remove https:// or http:// prefix for compact storage."""
    for prefix in ("https://", "http://"):
        if url.startswith(prefix):
            return url[len(prefix):]
    return url
