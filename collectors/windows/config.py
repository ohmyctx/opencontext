"""Configuration loader for the Windows UI Collector."""

from __future__ import annotations

import logging
from dataclasses import dataclass, field
import os
from pathlib import Path
from typing import List

import yaml

logger = logging.getLogger(__name__)

CONFIG_ENV = "OPENCONTEXT_WINDOWS_COLLECTOR_CONFIG"
APPDATA = Path(os.environ.get("APPDATA", Path.home() / "AppData" / "Roaming"))
LOCALAPPDATA = Path(os.environ.get("LOCALAPPDATA", Path.home() / "AppData" / "Local"))
DEFAULT_CONFIG_PATH = APPDATA / "OpenContext" / "collectors" / "windows.yaml"
LEGACY_CONFIG_PATH = Path.home() / ".opencontext" / "windows-collector.yaml"
DEFAULT_SCREENSHOT_DIR = LOCALAPPDATA / "OpenContext" / "screenshots" / "windows"


@dataclass
class Config:
    # OpenContext daemon endpoint
    daemon_url: str = "http://localhost:6060"

    # How often to flush buffered events to the OpenContext daemon (seconds)
    flush_interval: float = 5.0

    # How often to poll the foreground window for changes (seconds)
    window_poll_interval: float = 0.2

    # How often to poll running processes for new launches (seconds)
    process_poll_interval: float = 1.0

    # Default sensitivity level (1=metadata only, 2=structured content, 3=sensitive)
    # L1: app names, process names
    # L2: window titles, control names, submitted text
    # L3: raw keystrokes, clipboard (requires explicit opt-in)
    sensitivity: int = 2

    # Capture text that the user submits in text fields (value on Enter/Tab).
    # Skips password fields automatically. Sensitivity L2.
    collect_text_input: bool = True

    # Capture raw individual keystrokes. Sensitivity L3 — requires explicit opt-in.
    collect_raw_keys: bool = False

    # Capture clipboard content on copy events. On by default since the user
    # explicitly installed this collector to monitor their own activity.
    collect_clipboard: bool = True

    # Capture periodic screenshots. Sensitive L3, off by default.
    collect_screenshots: bool = False
    screenshot_interval_secs: float = 300.0
    screenshot_dir: str = str(DEFAULT_SCREENSHOT_DIR)
    screenshot_max_width: int = 1440
    screenshot_format: str = "jpg"
    screenshot_retention_days: int = 3
    screenshot_max_total_mb: int = 1024

    # App names (exe name, e.g. "msedge.exe") to ignore entirely.
    ignore_apps: List[str] = field(default_factory=list)

    # Window titles containing these strings will be skipped.
    ignore_window_titles: List[str] = field(default_factory=list)

    @classmethod
    def load(cls, path: Path | str | None = None) -> "Config":
        config_path = _resolve_config_path(path)
        if not config_path.exists():
            return cls()
        try:
            with open(config_path, encoding="utf-8") as f:
                data = yaml.safe_load(f) or {}
            valid_keys = set(cls.__dataclass_fields__)
            filtered = {k: v for k, v in data.items() if k in valid_keys}
            return cls(**filtered)
        except Exception as e:
            logger.warning("failed to load config from %s: %s — using defaults", config_path, e)
            return cls()

    def should_ignore_app(self, app_name: str) -> bool:
        if not app_name:
            return False
        lower = app_name.lower()
        return any(ignored.lower() == lower for ignored in self.ignore_apps)

    def should_ignore_title(self, title: str) -> bool:
        if not title:
            return False
        lower = title.lower()
        return any(fragment.lower() in lower for fragment in self.ignore_window_titles)

    def screenshot_path(self) -> Path:
        return Path(os.path.expandvars(os.path.expanduser(self.screenshot_dir)))


def _resolve_config_path(path: Path | str | None) -> Path:
    if path:
        return Path(os.path.expandvars(os.path.expanduser(str(path))))
    if env := os.environ.get(CONFIG_ENV):
        return Path(os.path.expandvars(os.path.expanduser(env)))
    if DEFAULT_CONFIG_PATH.exists() or not LEGACY_CONFIG_PATH.exists():
        return DEFAULT_CONFIG_PATH
    return LEGACY_CONFIG_PATH
