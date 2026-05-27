"""Monitors mouse click events and identifies the UI element that was clicked.

Uses pynput for low-level mouse hooking (no admin required).
Uses Windows UIAutomation (uiautomation library) to inspect the clicked control.

COM must be initialized per-thread; we call CoInitialize at the start of the
pynput listener thread and in each callback invocation as a safety measure.

Events emitted:
  - os.ui_click    (sensitivity L2)
"""

from __future__ import annotations

import ctypes
import logging
import threading
import time
from queue import Queue
from typing import Optional

import psutil
import win32gui
import win32process

from client import make_event
from config import Config
from monitors.helpers import get_foreground_info

logger = logging.getLogger(__name__)

# Thread-local flag to track per-thread COM initialization
_com_initialized = threading.local()


def _ensure_com() -> None:
    """Initialize COM in the calling thread (idempotent per thread)."""
    if not getattr(_com_initialized, "done", False):
        ctypes.windll.ole32.CoInitialize(None)
        _com_initialized.done = True


def _query_element_at(x: int, y: int) -> Optional[dict]:
    """Use UIAutomation to get info about the UI control at screen coordinates."""
    _ensure_com()
    try:
        import uiautomation as auto

        element = auto.ControlFromPoint(x, y)
        if element is None:
            return None

        control_type = element.ControlTypeName or ""
        name = element.Name or ""
        class_name = element.ClassName or ""

        # Skip truly invisible/unnamed system elements
        if not control_type and not name:
            return None

        result = {
            "control_type": control_type,
            "control_name": name,
        }
        if class_name:
            result["class_name"] = class_name

        # Grab the current value for editable controls (text fields)
        if control_type in ("Edit", "Document", "ComboBox"):
            try:
                vp = element.GetValuePattern()
                if vp and not element.IsPassword:
                    val = vp.Value
                    if val:
                        result["control_value"] = val[:200]  # truncate
            except Exception:
                pass

        # Window title from the top-level ancestor
        try:
            top = element.GetTopLevelControl()
            if top:
                result["window_title"] = top.Name or ""
        except Exception:
            pass

        return result

    except ImportError:
        logger.debug("uiautomation not available — click events will lack element info")
        return None
    except Exception as e:
        logger.debug("UIAutomation query error: %s", e)
        return None




# Deduplicate repeated clicks on the same control within this window (seconds).
# Prevents floods when the user keeps clicking inside a web/Electron content area
# that has no distinguishable UIA control (e.g. WPS协作 canvas).
_DEDUP_WINDOW_SECS = 3.0

# If a click produces no named control (just shows the window title), allow at most
# one event per this many seconds to avoid content-area click floods.
_ANON_CLICK_THROTTLE_SECS = 10.0


class ClickMonitor:
    """Listens for left/right mouse clicks and pushes os.ui_click events."""

    def __init__(self, queue: Queue, config: Config):
        self.queue = queue
        self.config = config
        self._listener = None
        # Deduplication state: (app, control_type, control_name, window_title) → last emit time
        self._last_key: tuple = ()
        self._last_ts: float = 0.0

    def start(self) -> None:
        from pynput import mouse

        self._listener = mouse.Listener(on_click=self._on_click)
        self._listener.daemon = True
        self._listener.start()

    def stop(self) -> None:
        if self._listener:
            self._listener.stop()

    def _on_click(self, x: int, y: int, button, pressed: bool) -> None:
        if not pressed:
            return  # ignore mouse-up

        from pynput.mouse import Button

        button_name = "left" if button == Button.left else "right" if button == Button.right else "middle"

        ts = int(time.time() * 1000)

        fg = get_foreground_info()
        app = fg["app"] if fg else ""
        app_name = fg["app_name"] if fg else ""

        if self.config.should_ignore_app(app):
            return

        # Enrich with UIAutomation element info (done before building labels
        # so we can gate on window_title ignore list early).
        element_info = _query_element_at(x, y)

        ct = element_info.get("control_type", "") if element_info else ""
        cn = element_info.get("control_name", "") if element_info else ""
        wt = (element_info.get("window_title", "") if element_info else "") or (fg["title"] if fg else "")

        if wt and self.config.should_ignore_title(wt):
            return

        # ── Deduplication ────────────────────────────────────────────────────
        # Key: (app, control_type, control_name, window_title)
        click_key = (app, ct, cn, wt)
        now = time.time()

        # "Anonymous" click: UIA returned no specific control name (or the name
        # equals the window title / app name) — these are content-area clicks in
        # web/Electron apps.  Throttle heavily to avoid floods.
        is_anon = not cn or cn == wt or cn == app_name or cn == "Chrome Legacy Window"
        throttle = _ANON_CLICK_THROTTLE_SECS if is_anon else _DEDUP_WINDOW_SECS

        if click_key == self._last_key and (now - self._last_ts) < throttle:
            return  # duplicate or throttled

        self._last_key = click_key
        self._last_ts = now
        # ─────────────────────────────────────────────────────────────────────

        labels: dict = {}
        if app:
            labels["app"] = app
        # Friendly name: ProductName > window title > (omit)
        display_name = app_name or wt or ""
        if display_name and display_name != app:
            labels["app_name"] = display_name
        if ct:
            labels["control_type"] = ct
        # control_name in labels makes it visible in SUMMARY (e.g. "关闭" button)
        if cn:
            labels["control_name"] = cn
        if wt:
            labels["window_title"] = wt

        payload: dict = {
            "button": button_name,
            "x": x,
            "y": y,
        }
        if element_info:
            if ct:
                payload["control_type"] = ct
            if cn:
                payload["control_name"] = cn
            if wt:
                payload["window_title"] = wt
            if "class_name" in element_info:
                payload["class_name"] = element_info["class_name"]
            if self.config.sensitivity >= 2 and "control_value" in element_info:
                payload["control_value"] = element_info["control_value"]

        self.queue.put(make_event(
            source="os",
            event_type="ui_click",
            sensitivity=2,
            labels=labels,
            payload=payload,
            ts=ts,
        ))
