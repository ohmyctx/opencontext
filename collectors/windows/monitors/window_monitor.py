"""Monitors foreground window focus changes on Windows.

Uses SetWinEventHook to subscribe to EVENT_SYSTEM_FOREGROUND (efficient, no polling).
Falls back to polling if the hook cannot be installed.

Events emitted:
  - os.window_focus   (sensitivity L1)
  - os.browser_nav    (sensitivity L1) — URL change within the same browser window
"""

from __future__ import annotations

import ctypes
import ctypes.wintypes
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
from monitors.helpers import get_friendly_name

logger = logging.getLogger(__name__)

EVENT_SYSTEM_FOREGROUND = 0x0003
WINEVENT_OUTOFCONTEXT = 0x0000

WinEventProcType = ctypes.WINFUNCTYPE(
    None,
    ctypes.wintypes.HANDLE,
    ctypes.wintypes.DWORD,
    ctypes.wintypes.HWND,
    ctypes.wintypes.LONG,
    ctypes.wintypes.LONG,
    ctypes.wintypes.DWORD,
    ctypes.wintypes.DWORD,
)

# Browser exe names that support URL reading via UIA address bar
_BROWSER_EXES = {
    "chrome.exe", "msedge.exe", "firefox.exe",
    "brave.exe", "opera.exe", "vivaldi.exe",
}

# Minimum seconds between emitting the same (app, title) combination.
# Prevents rapid sub-window flicker (e.g., WPS dialog open/close) from flooding.
_FOCUS_DEDUP_SECS = 1.5

# How often the browser URL poller checks for navigation within the same window.
_URL_POLL_INTERVAL = 3.0

# Address bar locator names across Chrome/Edge locales
_ADDRESS_BAR_NAMES = (
    "地址和搜索栏",          # Chrome zh-CN
    "Address and search bar",  # Chrome en
    "搜索或输入网址",          # Chrome alt zh
    "地址栏",                 # Edge zh-CN
    "Address bar",             # Edge en / Firefox en
    "搜索或输入地址",
)

_com_local = threading.local()


def _ensure_com() -> None:
    if not getattr(_com_local, "done", False):
        ctypes.windll.ole32.CoInitialize(None)
        _com_local.done = True


def _read_browser_url(hwnd: int) -> str:
    """Try to read the current URL from a browser window's address bar via UIA.

    Returns the URL string, or "" if not available.
    """
    _ensure_com()
    try:
        import uiautomation as auto

        window = auto.ControlFromHandle(hwnd)
        if not window:
            return ""

        for name in _ADDRESS_BAR_NAMES:
            bar = window.EditControl(Name=name)
            if bar.Exists(maxSearchSeconds=0):
                try:
                    vp = bar.GetValuePattern()
                    if vp:
                        val = vp.Value or ""
                        # Filter out search queries typed in the address bar
                        # (they don't start with http/https/file)
                        if val and (val.startswith(("http://", "https://", "file://"))
                                    or "." in val.split("/")[0]):
                            return val
                except Exception:
                    pass
        return ""
    except Exception as e:
        logger.debug("read_browser_url error: %s", e)
        return ""


def _get_window_info(hwnd: int) -> Optional[dict]:
    """Extract app name, friendly name, exe path, pid, and window title from an HWND."""
    try:
        _, pid = win32process.GetWindowThreadProcessId(hwnd)
        proc = psutil.Process(pid)
        exe_name = proc.name()
        try:
            exe_path = proc.exe()
        except (psutil.AccessDenied, psutil.NoSuchProcess):
            exe_path = ""
        title = win32gui.GetWindowText(hwnd) or ""
        friendly = get_friendly_name(exe_name, exe_path)
        return {
            "app": exe_name,
            "app_name": friendly,
            "exe": exe_path,
            "pid": pid,
            "title": title,
        }
    except Exception as e:
        logger.debug("get_window_info error: %s", e)
        return None


class WindowMonitor(threading.Thread):
    """Subscribes to foreground window changes via SetWinEventHook.

    Also spawns a browser-URL poller that detects in-browser navigation
    (same window, different URL / title).
    """

    def __init__(self, queue: Queue, config: Config):
        super().__init__(daemon=True, name="WindowMonitor")
        self.queue = queue
        self.config = config

        # Focus dedup state
        self._last_hwnd: int = 0
        self._last_app: str = ""
        self._last_app_name: str = ""
        self._last_focus_key: tuple = ()   # (app, title) of last emitted event
        self._last_focus_ts: float = 0.0

        # Browser URL poller state
        self._browser_hwnd: int = 0        # current browser HWND (0 if none)
        self._last_url: str = ""
        self._last_url_title: str = ""

        self._hook = None

    # ── Main thread (Win32 message pump) ─────────────────────────────────────

    def run(self) -> None:
        _ensure_com()
        self._callback = WinEventProcType(self._on_event)

        # Start the browser URL poller on a separate daemon thread
        poller = threading.Thread(
            target=self._url_poll_loop,
            daemon=True,
            name="BrowserURLPoller",
        )
        poller.start()

        try:
            self._hook = ctypes.windll.user32.SetWinEventHook(
                EVENT_SYSTEM_FOREGROUND,
                EVENT_SYSTEM_FOREGROUND,
                0,
                self._callback,
                0, 0,
                WINEVENT_OUTOFCONTEXT,
            )
            if not self._hook:
                logger.warning("SetWinEventHook failed, falling back to polling")
                self._poll_loop()
                return

            logger.debug("WinEventHook installed")
            self._message_pump()
        except Exception as e:
            logger.warning("window hook error (%s), falling back to polling", e)
            self._poll_loop()
        finally:
            if self._hook:
                ctypes.windll.user32.UnhookWinEvent(self._hook)

    def _message_pump(self) -> None:
        msg = ctypes.wintypes.MSG()
        while True:
            result = ctypes.windll.user32.GetMessageW(ctypes.byref(msg), 0, 0, 0)
            if result == 0 or result == -1:
                break
            ctypes.windll.user32.TranslateMessage(ctypes.byref(msg))
            ctypes.windll.user32.DispatchMessageW(ctypes.byref(msg))

    def _poll_loop(self) -> None:
        while True:
            try:
                hwnd = win32gui.GetForegroundWindow()
                self._handle_focus(hwnd)
            except Exception as e:
                logger.debug("poll error: %s", e)
            time.sleep(self.config.window_poll_interval)

    def _on_event(self, hook, event, hwnd, id_object, id_child, thread, event_time) -> None:
        try:
            self._handle_focus(hwnd)
        except Exception as e:
            logger.debug("on_event error: %s", e)

    # ── Focus handling ────────────────────────────────────────────────────────

    def _handle_focus(self, hwnd: int) -> None:
        if hwnd == 0:
            return

        info = _get_window_info(hwnd)
        if not info:
            return

        app = info["app"]
        title = info["title"]

        if self.config.should_ignore_app(app):
            return
        if self.config.should_ignore_title(title):
            return

        focus_key = (app, title)
        now = time.time()

        # Dedup: same (app, title) within cooldown window — skip
        # This prevents rapid sub-window flicker (e.g. dialog open/close in WPS)
        if focus_key == self._last_focus_key and (now - self._last_focus_ts) < _FOCUS_DEDUP_SECS:
            # Still update hwnd so we don't re-fire on the next actual change
            self._last_hwnd = hwnd
            return

        # Also deduplicate by HWND (original check)
        if hwnd == self._last_hwnd:
            return

        prev_app = self._last_app
        prev_app_name = self._last_app_name
        self._last_hwnd = hwnd
        self._last_app = app
        self._last_app_name = info["app_name"]
        self._last_focus_key = focus_key
        self._last_focus_ts = now

        # Track browser window for URL poller
        if app.lower() in _BROWSER_EXES:
            self._browser_hwnd = hwnd
            self._last_url = ""       # reset so URL is re-read immediately
            self._last_url_title = ""
        else:
            self._browser_hwnd = 0

        display_name = info["app_name"] or title or app

        labels: dict = {"app": app}
        if display_name and display_name != app:
            labels["app_name"] = display_name
        if title:
            labels["title"] = title

        payload: dict = {"pid": info["pid"]}
        if info["exe"]:
            payload["exe"] = info["exe"]
        if prev_app:
            payload["prev_app"] = prev_app
            if prev_app_name:
                payload["prev_app_name"] = prev_app_name

        # For browsers: include URL at focus time
        if app.lower() in _BROWSER_EXES:
            url = _read_browser_url(hwnd)
            if url:
                payload["url"] = url
                labels["url"] = url

        self.queue.put(make_event(
            source="os",
            event_type="window_focus",
            sensitivity=1,
            labels=labels,
            payload=payload,
        ))

    # ── Browser URL poller ────────────────────────────────────────────────────

    def _url_poll_loop(self) -> None:
        """Detect in-browser navigation (same HWND, URL changes) every few seconds."""
        _ensure_com()
        while True:
            time.sleep(_URL_POLL_INTERVAL)
            try:
                self._check_browser_nav()
            except Exception as e:
                logger.debug("url poller error: %s", e)

    def _check_browser_nav(self) -> None:
        hwnd = self._browser_hwnd
        if not hwnd:
            return

        # Verify the window still exists and is still a browser
        try:
            if not win32gui.IsWindow(hwnd):
                self._browser_hwnd = 0
                return
            _, pid = win32process.GetWindowThreadProcessId(hwnd)
            proc = psutil.Process(pid)
            if proc.name().lower() not in _BROWSER_EXES:
                self._browser_hwnd = 0
                return
        except Exception:
            self._browser_hwnd = 0
            return

        url = _read_browser_url(hwnd)
        title = win32gui.GetWindowText(hwnd) or ""

        # Only emit if something actually changed
        if url == self._last_url and title == self._last_url_title:
            return
        if not url and not title:
            return

        prev_url = self._last_url
        self._last_url = url
        self._last_url_title = title

        # Don't emit on very first read after focus-gain (window_focus already covers it)
        if not prev_url and url:
            return

        app = self._last_app
        app_name = self._last_app_name

        labels: dict = {"app": app}
        if app_name:
            labels["app_name"] = app_name
        if title:
            labels["title"] = title
        if url:
            labels["url"] = url

        payload: dict = {"url": url, "title": title}
        if prev_url:
            payload["prev_url"] = prev_url

        logger.info("browser_nav: %s → %s", prev_url[:60] if prev_url else "(none)", url[:60])
        self.queue.put(make_event(
            source="os",
            event_type="browser_nav",
            sensitivity=1,
            labels=labels,
            payload=payload,
        ))
