"""macOS app launch monitor.

Uses NSWorkspace notifications (didLaunchApplication) to detect new apps.
No polling required — purely event-driven.

Emits:
  os.app_launch  — when a user-facing application is launched
"""

from __future__ import annotations

import logging
import threading
import time
from queue import Queue

from client import make_event
from config import Config

logger = logging.getLogger(__name__)

# Bundle ID fragments that indicate system/background agents — skip them
_NOISE_BUNDLE_FRAGMENTS = (
    "com.apple.security",
    "com.apple.coreservices",
    "com.apple.xpc",
    "com.apple.accountsd",
    "com.apple.sharingd",
    "com.apple.bird",          # iCloud
    "com.apple.cloudphotod",
    "com.apple.backupd",
    "com.apple.mdworker",
    "com.apple.spotlight",
    "com.apple.trustd",
    "com.apple.systempreferences.extensionhost",
    "com.apple.privatewindow",
    "com.apple.dock.extra",
    "apple.notificationcenter",
    ".helper",                 # e.g. com.google.Chrome.helper
    ".agent",
    ".daemon",
    ".extension",
    ".plugin",
    ".crashreporter",
    ".installer",
)

# Apps that typically run in background and are not user-initiated
_NOISE_APP_NAMES = {
    "Spotlight",
    "Notification Center",
    "Control Center",
    "loginwindow",
    "Dock",
    "SystemUIServer",
    "WindowServer",
    "UserEventAgent",
    "coreautha",
    "cfprefsd",
    "distnoted",
}


class ProcessMonitor:
    """Subscribes to NSWorkspace app-launch notifications."""

    def __init__(self, queue: Queue, config: Config) -> None:
        self.queue = queue
        self.config = config
        self._stop = threading.Event()
        self._thread = threading.Thread(target=self._run, daemon=True, name="ProcessMonitor")

    def start(self) -> None:
        self._thread.start()

    def stop(self) -> None:
        self._stop.set()

    def _run(self) -> None:
        """Poll for new processes every 2s using psutil as a reliable fallback.

        NSWorkspace notifications require a GUI NSRunLoop which may not be
        available in SSH/daemon contexts.  psutil polling avoids that dependency.
        """
        try:
            import psutil  # type: ignore
        except ImportError:
            logger.error("psutil not available — process monitor disabled")
            return

        # Also try NSWorkspace notifications (best-effort, may not fire via SSH)
        self._try_register_notifications()

        seen_pids: set = {p.pid for p in psutil.process_iter()}

        while not self._stop.is_set():
            time.sleep(2.0)
            try:
                current = {p.pid for p in psutil.process_iter()}
                new_pids = current - seen_pids
                seen_pids = current
                for pid in new_pids:
                    try:
                        p = psutil.Process(pid)
                        name = p.name()
                        self._handle_new_process(name, p)
                    except (psutil.NoSuchProcess, psutil.AccessDenied):
                        pass
            except Exception as e:
                logger.debug("process poll error: %s", e)

    def _try_register_notifications(self) -> None:
        try:
            from AppKit import NSWorkspace  # type: ignore
            ws = NSWorkspace.sharedWorkspace()
            nc = ws.notificationCenter()
            observer = _make_launch_observer(self._on_launch)
            nc.addObserver_selector_name_object_(
                observer, "handleNotification:",
                "NSWorkspaceDidLaunchApplicationNotification", None,
            )
            logger.debug("NSWorkspace app-launch notifications registered")
        except Exception as e:
            logger.debug("NSWorkspace notifications unavailable: %s", e)

    def _handle_new_process(self, name: str, proc) -> None:
        """Filter and emit app_launch for a newly detected process."""
        if not name or name in _NOISE_APP_NAMES:
            return
        # Only care about .app bundles (have a bundle ID) — skip daemons
        try:
            exe = proc.exe()
        except (psutil.AccessDenied, psutil.NoSuchProcess):
            return
        # macOS GUI apps live under /Applications or ~/Applications
        if not exe or ("/Applications/" not in exe and "/Contents/MacOS/" not in exe):
            return
        self._on_launch({"app": name, "bundle_id": "", "pid": proc.pid})

    def _on_launch(self, app_info: dict) -> None:
        app_name: str = app_info.get("app", "")
        bundle_id: str = app_info.get("bundle_id", "")

        if not app_name or app_name in _NOISE_APP_NAMES:
            return
        if self.config.should_ignore_app(app_name):
            return
        if bundle_id and any(frag in bundle_id for frag in _NOISE_BUNDLE_FRAGMENTS):
            logger.debug("skip app_launch (noise bundle): %s / %s", app_name, bundle_id)
            return

        labels: dict = {"app": app_name}
        payload: dict = {"app": app_name}
        if bundle_id:
            labels["bundle_id"] = bundle_id
            payload["bundle_id"] = bundle_id

        self.queue.put(make_event(
            source="os",
            event_type="app_launch",
            sensitivity=1,
            labels=labels,
            payload=payload,
        ))


def _make_launch_observer(callback):
    """Create an NSObject to receive NSWorkspace app-launch notifications."""
    from Foundation import NSObject  # type: ignore
    import objc  # type: ignore

    cls_name = "_PMAppLaunchObserver_oc"
    try:
        cls = objc.lookUpClass(cls_name)
    except objc.nosuchclass_error:
        cls = type(cls_name, (NSObject,), {
            "handleNotification_": lambda self, n: _pm_obs_handle(self, n),
        })

    obs = cls.alloc().init()
    obs._pm_callback = callback
    return obs


def _pm_obs_handle(self, notification) -> None:
    try:
        user_info = notification.userInfo()
        if user_info is None:
            return
        app = user_info.get("NSWorkspaceApplicationKey")
        if app is None:
            return
        info = {
            "app": app.localizedName() or "",
            "bundle_id": app.bundleIdentifier() or "",
            "pid": app.processIdentifier(),
        }
        cb = getattr(self, "_pm_callback", None)
        if cb:
            cb(info)
    except Exception as e:
        logger.debug("launch observer error: %s", e)
