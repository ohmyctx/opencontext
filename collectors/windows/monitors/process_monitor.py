"""Monitors new process launches on Windows.

Polls psutil for new PIDs every process_poll_interval seconds.
Filters out short-lived background helper processes (no window, very short-lived)
by doing a second-look check after a brief delay.

Events emitted:
  - os.app_launch   (sensitivity L1)
"""

from __future__ import annotations

import logging
import threading
import time
from queue import Queue
from typing import Dict, Set

import psutil
import win32gui
import win32process

from client import make_event
from config import Config
from monitors.helpers import get_friendly_name

logger = logging.getLogger(__name__)

# System / background processes that are almost never interesting to a user.
# These are exe names (lowercase) to skip outright.
_NOISE_PROCESSES: Set[str] = {
    "svchost.exe", "csrss.exe", "lsass.exe", "smss.exe", "wininit.exe",
    "services.exe", "winlogon.exe", "dwm.exe", "conhost.exe", "dllhost.exe",
    "taskhostw.exe", "sihost.exe", "runtimebroker.exe", "searchindexer.exe",
    "searchprotocolhost.exe", "searchfilterhost.exe", "wuauclt.exe",
    "audiodg.exe", "fontdrvhost.exe", "spoolsv.exe", "ctfmon.exe",
    "system", "registry", "memory compression", "secure system",
    # Common antivirus / telemetry noise
    "mpcmdrun.exe", "mssense.exe", "antimalware service executable",
    # Windows Update
    "trustedinstaller.exe", "tiworker.exe", "wuapihost.exe",
    # WMI / management instrumentation
    "wmiprvse.exe", "wmiapsrv.exe", "wmihost.exe",
    # Background task infrastructure
    "backgroundtaskhost.exe", "backgroundtransferhost.exe", "taskhostw.exe",
    # SmartScreen / UAC
    "smartscreen.exe", "consent.exe",
    # Common system helpers / installer agents
    "msiexec.exe", "rundll32.exe", "regsvr32.exe",
    "werfault.exe", "werhelper.exe", "wermgr.exe",
    # Windows WinSxS / servicing
    "tiworker.exe", "trustedinstaller.exe", "wuauclt.exe",
    # Screen/sleep/power
    "powercfg.exe", "scrnsave.exe",
}

# Path substrings (lowercase) that indicate a system/agent process, not user-launched.
_NOISE_PATH_FRAGMENTS: tuple[str, ...] = (
    "\\windows\\system32\\wbem",
    "\\windows\\servicing",
    "\\syswow64\\isagent",
    "\\syswo64\\isagent",
    "\\windows\\system32\\isagent",
    "\\programdata\\microsoft\\windows defender",
    "\\windowsapps\\",           # UWP / Microsoft Store background extensions
)

# app_name patterns (lowercase substrings) that identify generic Windows infrastructure.
# These are apps whose ProductName comes from the Windows OS itself.
_NOISE_APP_NAME_FRAGMENTS: tuple[str, ...] = (
    "microsoft\u00ae windows\u00ae operating system",   # "Microsoft® Windows® Operating System"
    "windows\u00ae internet explorer",
)

# Seconds to wait before checking if a newly launched process has a visible window.
# Most user-launched apps create a window within 2s; background services rarely do.
_WINDOW_CHECK_DELAY_SECS = 2.5


class ProcessMonitor(threading.Thread):
    """Polls running processes and emits os.app_launch when a new one appears."""

    def __init__(self, queue: Queue, config: Config):
        super().__init__(daemon=True, name="ProcessMonitor")
        self.queue = queue
        self.config = config
        self._known: Dict[int, str] = {}  # pid -> exe name

    def run(self) -> None:
        # Seed the known set without emitting events for already-running processes
        self._known = self._snapshot()
        logger.debug("process monitor seeded with %d pids", len(self._known))

        while True:
            time.sleep(self.config.process_poll_interval)
            try:
                self._check()
            except Exception as e:
                logger.debug("process check error: %s", e)

    def _snapshot(self) -> Dict[int, str]:
        result = {}
        for proc in psutil.process_iter(["pid", "name"]):
            try:
                result[proc.info["pid"]] = proc.info["name"] or ""
            except (psutil.NoSuchProcess, psutil.AccessDenied):
                pass
        return result

    def _is_system_path(self, pid: int) -> bool:
        """Return True if the process exe lives in a known system/agent directory."""
        try:
            path = psutil.Process(pid).exe().lower().replace("/", "\\")
            return any(frag in path for frag in _NOISE_PATH_FRAGMENTS)
        except Exception:
            return False

    @staticmethod
    def _has_visible_window(pid: int) -> bool:
        """Return True if the process owns at least one visible top-level window."""
        found = [False]

        def _cb(hwnd, _):
            if found[0]:
                return False  # short-circuit
            if win32gui.IsWindowVisible(hwnd) and win32gui.IsWindowEnabled(hwnd):
                try:
                    _, wpid = win32process.GetWindowThreadProcessId(hwnd)
                    if wpid == pid:
                        found[0] = True
                        return False
                except Exception:
                    pass
            return True

        try:
            win32gui.EnumWindows(_cb, None)
        except Exception:
            pass
        return found[0]

    def _check(self) -> None:
        current = self._snapshot()
        new_pids = set(current) - set(self._known)

        for pid in new_pids:
            name = current.get(pid, "")
            if not name:
                continue
            if name.lower() in _NOISE_PROCESSES:
                continue
            if self.config.should_ignore_app(name):
                continue
            if self._is_subprocess(pid, name):
                continue
            if self._is_system_path(pid):
                continue

            self._emit_launch(pid, name)

        self._known = current

    def _is_subprocess(self, pid: int, name: str) -> bool:
        """Return True for known browser/electron renderer sub-processes (high noise, low signal)."""
        if name.lower() not in ("chrome.exe", "msedge.exe", "brave.exe", "firefox.exe",
                                 "opera.exe", "vivaldi.exe"):
            return False
        try:
            proc = psutil.Process(pid)
            cmdline = " ".join(proc.cmdline())
            # Renderer, GPU, network, and extension processes are sub-processes
            noisy_flags = ("--type=renderer", "--type=gpu-process", "--type=network",
                           "--type=utility", "--type=sandbox", "--extension-process",
                           "--type=crashpad-handler")
            return any(f in cmdline for f in noisy_flags)
        except Exception:
            return False

    def _emit_launch(self, pid: int, name: str) -> None:
        """Gather process info now, then check for a visible window after a short delay.

        We do the window check on a background thread so the main poll loop
        isn't blocked for 2+ seconds per process.
        """
        ts = int(time.time() * 1000)
        labels: dict = {"app": name}
        payload: dict = {"pid": pid}

        exe_path = ""
        friendly = ""
        try:
            proc = psutil.Process(pid)
            try:
                exe_path = proc.exe()
                payload["exe"] = exe_path
            except (psutil.AccessDenied, psutil.NoSuchProcess):
                pass
            try:
                cmdline = proc.cmdline()
                if cmdline:
                    payload["cmdline"] = " ".join(cmdline[:6])
            except (psutil.AccessDenied, psutil.NoSuchProcess):
                pass
        except (psutil.NoSuchProcess, psutil.AccessDenied):
            pass

        friendly = get_friendly_name(name, exe_path)

        # Filter by known background app_name patterns
        if friendly and any(frag in friendly.lower() for frag in _NOISE_APP_NAME_FRAGMENTS):
            logger.debug("skip app_launch (system app_name): %s / %s", name, friendly)
            return

        if friendly:
            labels["app_name"] = friendly

        def _deferred_emit():
            # Wait for the app to potentially create a window
            time.sleep(_WINDOW_CHECK_DELAY_SECS)
            if not self._has_visible_window(pid):
                logger.debug("skip app_launch (no visible window): %s (pid=%d)", name, pid)
                return
            self.queue.put(make_event(
                source="os",
                event_type="app_launch",
                sensitivity=1,
                labels=labels,
                payload={k: v for k, v in payload.items() if v},
                ts=ts,
            ))

        t = threading.Thread(target=_deferred_emit, daemon=True, name=f"launch-check-{pid}")
        t.start()
