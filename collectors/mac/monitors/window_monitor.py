"""macOS window focus monitor.

Uses NSWorkspace notifications to detect app switches, then reads the
focused window title and (for browsers) the current URL via AXUIElement.

Emits:
  os.window_focus  — when the frontmost app or window title changes
  os.browser_nav   — when the URL changes inside a browser (polled every 3s)
"""

from __future__ import annotations

import logging
import threading
import time
from queue import Queue
from typing import Optional

from client import make_event
from config import Config
from monitors.helpers import get_frontmost_app, check_accessibility_permission, _BROWSER_BUNDLES

logger = logging.getLogger(__name__)

_FOCUS_DEDUP_SECS = 1.5   # suppress duplicate (app, title) within this window
_URL_POLL_INTERVAL = 3.0  # check for in-browser URL changes every N seconds


class WindowMonitor:
    """Listens to NSWorkspace notifications in a background thread."""

    def __init__(self, queue: Queue, config: Config) -> None:
        self.queue = queue
        self.config = config

        self._last_focus_key: tuple = ()
        self._last_focus_ts: float = 0.0
        self._browser_bundle: str = ""
        self._last_url: str = ""
        self._last_url_title: str = ""

        self._stop = threading.Event()
        self._thread = threading.Thread(target=self._run, daemon=True, name="WindowMonitor")
        self._url_thread = threading.Thread(target=self._url_poll_loop, daemon=True, name="BrowserURLPoller")

    def start(self) -> None:
        self._thread.start()
        self._url_thread.start()

    def stop(self) -> None:
        self._stop.set()

    # ------------------------------------------------------------------
    # Main poll loop (simple thread — no NSRunLoop dependency)
    # ------------------------------------------------------------------

    def _run(self) -> None:
        """Poll frontmost app every 0.5s. Reliable even from non-GUI contexts (SSH)."""
        # Also try to register NSWorkspace notifications for faster response.
        self._register_notifications()

        while not self._stop.is_set():
            time.sleep(0.5)
            try:
                self._emit_focus(is_poll=True)
            except Exception as e:
                logger.debug("poll error: %s", e)

    def _register_notifications(self) -> None:
        """Optionally subscribe to NSWorkspace notifications for faster response.
        Falls back gracefully if NSRunLoop is not available in this context."""
        try:
            from AppKit import NSWorkspace, NSRunLoop, NSDate  # type: ignore
            ws = NSWorkspace.sharedWorkspace()
            nc = ws.notificationCenter()
            observer = _make_activation_observer(self._on_app_activated)
            for note_name in [
                "NSWorkspaceDidActivateApplicationNotification",
                "NSWorkspaceActiveSpaceDidChangeNotification",
            ]:
                nc.addObserver_selector_name_object_(
                    observer, "handleNotification:", note_name, None
                )
            logger.debug("NSWorkspace notifications registered")
        except Exception as e:
            logger.debug("NSWorkspace notifications not available: %s", e)

    def _on_app_activated(self) -> None:
        """Called from NSNotification for fast response on app switch."""
        time.sleep(0.05)
        self._emit_focus()

    def _check_title_change(self) -> None:
        """(Unused — poll loop now calls _emit_focus directly)"""
        pass

    def _emit_focus(self, is_poll: bool = False) -> None:
        fg = get_frontmost_app()
        if not fg:
            return

        app = fg["app"]
        title = fg["title"]
        url = fg["url"]
        bundle = fg["bundle_id"]

        if self.config.should_ignore_app(app):
            return
        if self.config.should_ignore_title(title):
            return

        focus_key = (app, title)
        now = time.monotonic()

        # Poll path: only emit on actual change (app or title different)
        if is_poll and focus_key == self._last_focus_key:
            return

        # Notification path: dedup rapid duplicate events
        if not is_poll and focus_key == self._last_focus_key and (now - self._last_focus_ts) < _FOCUS_DEDUP_SECS:
            return

        self._last_focus_key = focus_key
        self._last_focus_ts = now

        # Track browser for URL polling
        if bundle in _BROWSER_BUNDLES:
            self._browser_bundle = bundle
            self._last_url = url
            self._last_url_title = title

        labels: dict = {"app": app}
        payload: dict = {"app": app}
        if title:
            labels["title"] = title
            payload["title"] = title
        if url:
            labels["url"] = url
            payload["url"] = url
        if bundle:
            labels["bundle_id"] = bundle

        self.queue.put(make_event(
            source="os",
            event_type="window_focus",
            sensitivity=1,
            labels=labels,
            payload=payload,
        ))

    # ------------------------------------------------------------------
    # Browser URL change detection (separate poll thread)
    # ------------------------------------------------------------------

    def _url_poll_loop(self) -> None:
        while not self._stop.is_set():
            time.sleep(_URL_POLL_INTERVAL)
            try:
                self._check_browser_nav()
            except Exception as e:
                logger.debug("url poll error: %s", e)

    def _check_browser_nav(self) -> None:
        if not check_accessibility_permission():
            return
        fg = get_frontmost_app()
        if not fg or fg["bundle_id"] not in _BROWSER_BUNDLES:
            return

        url = fg["url"]
        title = fg["title"]

        if not url or url == self._last_url:
            return

        self._last_url = url
        self._last_url_title = title

        labels: dict = {"app": fg["app"], "url": url}
        payload: dict = {"app": fg["app"], "url": url}
        if title:
            labels["title"] = title
            payload["title"] = title

        self.queue.put(make_event(
            source="os",
            event_type="browser_nav",
            sensitivity=2,
            labels=labels,
            payload=payload,
        ))


def _make_activation_observer(callback):
    """Create an NSObject to receive NSWorkspace activation notifications."""
    from Foundation import NSObject  # type: ignore

    # Class must be module-level-like unique name to avoid Obj-C class registry collision.
    # We define it here but use a deterministic unique name.
    cls_name = "_WMActivationObserver_oc"
    import objc  # type: ignore
    try:
        cls = objc.lookUpClass(cls_name)
    except objc.nosuchclass_error:
        # Define the class once
        cls = type(cls_name, (NSObject,), {
            "handleNotification_": lambda self, n: _obs_handle(self, n),
        })

    obs = cls.alloc().init()
    obs._wm_callback = callback
    return obs


def _obs_handle(self, notification) -> None:
    try:
        cb = getattr(self, "_wm_callback", None)
        if cb:
            cb()
    except Exception as e:
        logger.debug("window observer error: %s", e)
