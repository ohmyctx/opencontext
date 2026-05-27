"""Shared helpers for Windows UI monitors.

get_friendly_name() — reads the Windows ProductName from file version info,
    giving human-readable app names like "微信" instead of "Weixin.exe".
    Results are cached in-process.

get_foreground_info() — one-stop call to get exe name, friendly name,
    window title and PID for the currently active foreground window.
"""

from __future__ import annotations

import logging
import threading
from typing import Optional

import psutil
import win32api
import win32gui
import win32process

logger = logging.getLogger(__name__)

_name_cache: dict[str, str] = {}
_cache_lock = threading.Lock()

# Static fallback for common apps whose ProductName differs from what Windows
# returns, or where the ProductName is too generic (e.g. "Application").
_STATIC_NAMES: dict[str, str] = {
    "explorer.exe":    "文件资源管理器",
    "notepad.exe":     "记事本",
    "mspaint.exe":     "画图",
    "calc.exe":        "计算器",
    "taskmgr.exe":     "任务管理器",
    "SearchApp.exe":   "Windows 搜索",
    "SearchHost.exe":  "Windows 搜索",
    "ShellExperienceHost.exe": "开始菜单",
    "StartMenuExperienceHost.exe": "开始菜单",
    "SystemSettings.exe": "Windows 设置",
}


def get_friendly_name(exe_name: str, exe_path: str = "") -> str:
    """Return a human-readable app name.

    Priority:
      1. Static mapping (exact exe name match)
      2. Windows ProductName from file version info (cached by exe_path)
      3. Empty string (caller should fall back to window title or exe_name)
    """
    # 1. static mapping
    static = _STATIC_NAMES.get(exe_name, "")
    if static:
        return static

    if not exe_path:
        return ""

    # 2. cache hit
    with _cache_lock:
        if exe_path in _name_cache:
            return _name_cache[exe_path]

    # 3. read from file version info
    name = _read_product_name(exe_path)
    with _cache_lock:
        _name_cache[exe_path] = name
    return name


def _read_product_name(exe_path: str) -> str:
    try:
        translations = win32api.GetFileVersionInfo(exe_path, r"\VarFileInfo\Translation")
        if not translations:
            return ""
        lang, codepage = translations[0]
        key = r"\StringFileInfo\%04X%04X\ProductName" % (lang, codepage)
        raw = win32api.GetFileVersionInfo(exe_path, key)
        if raw and raw.strip():
            name = raw.strip()
            # Skip overly generic names
            if name.lower() not in ("application", "program", "executable"):
                return name
    except Exception as e:
        logger.debug("get_product_name(%s): %s", exe_path, e)
    return ""


def get_foreground_info() -> Optional[dict]:
    """Return metadata for the currently focused foreground window.

    Returns a dict with keys:
        app           exe file name (e.g. "chrome.exe")
        exe           full exe path
        app_name      friendly ProductName (e.g. "Google Chrome") — may be ""
        title         window title
        pid           process ID
    Returns None on any error.
    """
    try:
        hwnd = win32gui.GetForegroundWindow()
        if not hwnd:
            return None

        _, pid = win32process.GetWindowThreadProcessId(hwnd)
        proc = psutil.Process(pid)
        exe_name = proc.name()

        try:
            exe_path = proc.exe()
        except (psutil.AccessDenied, psutil.NoSuchProcess):
            exe_path = ""

        friendly = get_friendly_name(exe_name, exe_path)
        title = win32gui.GetWindowText(hwnd) or ""

        return {
            "app": exe_name,
            "exe": exe_path,
            "app_name": friendly,
            "title": title,
            "pid": pid,
        }
    except Exception as e:
        logger.debug("get_foreground_info: %s", e)
        return None
