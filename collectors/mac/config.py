"""Configuration loader for the macOS UI Collector."""

from __future__ import annotations

import logging
from dataclasses import dataclass, field
import os
from pathlib import Path
from typing import List

import yaml

logger = logging.getLogger(__name__)

CONFIG_ENV = "OPENCONTEXT_MAC_COLLECTOR_CONFIG"
DEFAULT_CONFIG_PATH = Path.home() / ".config" / "opencontext" / "collectors" / "macos.yaml"
LEGACY_CONFIG_PATH = Path.home() / ".opencontext" / "mac-collector.yaml"
DEFAULT_SCREENSHOT_DIR = Path.home() / "Library" / "Application Support" / "OpenContext" / "screenshots" / "macos"


@dataclass
class Config:
    # OpenContext daemon endpoint
    daemon_url: str = "http://localhost:6060"

    # How often to flush buffered events (seconds)
    flush_interval: float = 5.0

    # How often to poll clipboard for changes (seconds)
    clipboard_poll_interval: float = 1.0

    # Default sensitivity (1=metadata, 2=structured content, 3=sensitive)
    sensitivity: int = 2

    # Capture text submitted in text fields (AXValue on focus-leave / Return)
    collect_text_input: bool = True

    # Capture raw individual keystrokes (L3 opt-in)
    collect_raw_keys: bool = False

    # Capture clipboard content on copy; on by default for personal monitoring
    collect_clipboard: bool = True

    # Capture periodic screenshots. Sensitive L3, off by default.
    collect_screenshots: bool = False
    screenshot_interval_secs: float = 300.0
    screenshot_dir: str = str(DEFAULT_SCREENSHOT_DIR)
    screenshot_max_width: int = 1440
    screenshot_format: str = "jpg"
    screenshot_retention_days: int = 3
    screenshot_max_total_mb: int = 1024

    # Bundle IDs (e.g. "com.apple.finder") or app names to ignore entirely
    ignore_apps: List[str] = field(default_factory=list)

    # Window titles containing these substrings will be skipped
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
            logger.warning("failed to load config %s: %s — using defaults", config_path, e)
            return cls()

    def should_ignore_app(self, app: str) -> bool:
        if not app:
            return False
        lower = app.lower()
        return any(ig.lower() in lower or lower in ig.lower() for ig in self.ignore_apps)

    def should_ignore_title(self, title: str) -> bool:
        if not title:
            return False
        lower = title.lower()
        return any(frag.lower() in lower for frag in self.ignore_window_titles)

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
