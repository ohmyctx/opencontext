"""macOS mouse click monitor.

Uses pynput (which wraps CGEventTap) to capture left-click events.
On each click, queries the AX element under the cursor for its role and label.

Emits:
  os.ui_click  — left button press on a named UI element
"""

from __future__ import annotations

import logging
import time
from queue import Queue

from pynput import mouse  # type: ignore

from client import make_event
from config import Config
from monitors.helpers import get_frontmost_app, check_accessibility_permission

logger = logging.getLogger(__name__)

_DEDUP_WINDOW_SECS = 3.0      # suppress identical (app, role, label) within this window
_ANON_THROTTLE_SECS = 10.0    # extra throttle for clicks with no useful element name


def _get_ax_element_at(x: int, y: int) -> dict:
    """Return AX info (role, label) for the element at screen coordinates."""
    try:
        from ApplicationServices import (  # type: ignore
            AXUIElementCreateSystemWide,
            AXUIElementCopyElementAtPosition,
            AXUIElementCopyAttributeValue,
            kAXRoleAttribute,
            kAXTitleAttribute,
            kAXDescriptionAttribute,
            kAXErrorSuccess,
        )
        system = AXUIElementCreateSystemWide()
        err, element = AXUIElementCopyElementAtPosition(system, float(x), float(y), None)
        if err != kAXErrorSuccess or element is None:
            return {}

        err, role = AXUIElementCopyAttributeValue(element, kAXRoleAttribute, None)
        role_str = str(role) if err == kAXErrorSuccess and role else ""

        # Try title, then description
        label_str = ""
        for attr in (kAXTitleAttribute, kAXDescriptionAttribute):
            err, val = AXUIElementCopyAttributeValue(element, attr, None)
            if err == kAXErrorSuccess and val:
                label_str = str(val)
                break

        return {"role": role_str, "label": label_str}
    except Exception as e:
        logger.debug("AX element query error at (%d,%d): %s", x, y, e)
        return {}


class ClickMonitor:
    def __init__(self, queue: Queue, config: Config) -> None:
        self.queue = queue
        self.config = config
        self._last_key: tuple = ()
        self._last_ts: float = 0.0
        self._listener: mouse.Listener | None = None

    def start(self) -> None:
        if not check_accessibility_permission():
            logger.warning(
                "Accessibility permission not granted — click monitoring disabled.\n"
                "  Fix: System Settings → Privacy & Security → Accessibility → add Terminal/Python"
            )
            return
        self._listener = mouse.Listener(on_click=self._on_click)
        self._listener.start()

    def stop(self) -> None:
        if self._listener:
            self._listener.stop()

    def _on_click(self, x: int, y: int, button, pressed: bool) -> None:
        if not pressed or button != mouse.Button.left:
            return

        fg = get_frontmost_app()
        if not fg:
            return
        app = fg["app"]
        if self.config.should_ignore_app(app):
            return
        if self.config.should_ignore_title(fg.get("title", "")):
            return

        ax = _get_ax_element_at(int(x), int(y))
        role = ax.get("role", "")
        label = ax.get("label", "")
        title = fg.get("title", "")

        # Deduplication
        click_key = (app, role, label)
        now = time.monotonic()
        is_anon = not label or label == title or label == app
        throttle = _ANON_THROTTLE_SECS if is_anon else _DEDUP_WINDOW_SECS
        if click_key == self._last_key and (now - self._last_ts) < throttle:
            return
        self._last_key = click_key
        self._last_ts = now

        labels: dict = {"app": app}
        payload: dict = {"app": app, "button": "left", "x": int(x), "y": int(y)}
        if title:
            labels["window_title"] = title
        if role:
            labels["control_type"] = role
            payload["control_type"] = role
        if label:
            labels["control_name"] = label
            payload["control_name"] = label

        self.queue.put(make_event(
            source="os",
            event_type="ui_click",
            sensitivity=2,
            labels=labels,
            payload=payload,
        ))
