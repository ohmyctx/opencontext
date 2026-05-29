"""Periodic Windows screenshot monitor.

Emits:
  os.screenshot — local screenshot file path only (L3)

The image is stored locally and never uploaded in the event payload.
"""

from __future__ import annotations

import logging
import threading
import time
from pathlib import Path
from queue import Queue

from client import make_event
from config import Config
from monitors.helpers import get_foreground_info

logger = logging.getLogger(__name__)


class ScreenshotMonitor(threading.Thread):
    def __init__(self, queue: Queue, config: Config) -> None:
        super().__init__(daemon=True, name="ScreenshotMonitor")
        self.queue = queue
        self.config = config
        self._stop_event = threading.Event()

    def stop(self) -> None:
        self._stop_event.set()

    def run(self) -> None:
        interval = max(30.0, float(self.config.screenshot_interval_secs))
        logger.info("screenshot monitor started (interval %.0fs, L3)", interval)
        while not self._stop_event.wait(interval):
            try:
                keep = self._capture_once()
                self._cleanup(keep)
            except Exception as e:
                logger.debug("screenshot capture failed: %s", e)

    def _capture_once(self) -> Path | None:
        try:
            from PIL import ImageGrab  # type: ignore
        except ImportError:
            logger.error("Pillow is not installed; screenshot monitor disabled")
            self._stop_event.set()
            return None

        directory = self.config.screenshot_path()
        directory.mkdir(parents=True, exist_ok=True)
        ts = int(time.time() * 1000)
        fmt = _normal_format(self.config.screenshot_format)
        path = directory / f"screenshot-{ts}.{fmt}"

        image = ImageGrab.grab(all_screens=True)
        image = _resize(image, int(self.config.screenshot_max_width))
        if fmt == "jpg" and image.mode not in ("RGB", "L"):
            image = image.convert("RGB")
        if fmt == "jpg":
            image.save(path, quality=85)
        else:
            image.save(path)

        stat = path.stat()
        fg = get_foreground_info() or {}

        labels = {
            "content_type": "image",
            "storage": "local_path",
            "platform": "windows",
        }
        if app := fg.get("app"):
            labels["app"] = app
        if app_name := fg.get("app_name"):
            labels["app_name"] = app_name
        if title := fg.get("title"):
            labels["title"] = title[:120]

        payload = {
            "path": str(path),
            "format": fmt,
            "width": image.width,
            "height": image.height,
            "size_bytes": stat.st_size,
            "max_width": int(self.config.screenshot_max_width),
            "retention_days": int(self.config.screenshot_retention_days),
            "max_total_mb": int(self.config.screenshot_max_total_mb),
        }
        for key in ("app", "app_name", "exe", "pid", "title"):
            if fg.get(key):
                payload[key] = fg[key]

        self.queue.put(make_event(
            source="os",
            event_type="screenshot",
            sensitivity=3,
            labels=labels,
            payload=payload,
        ))
        logger.info("screenshot captured path=%s size=%d", path, stat.st_size)
        return path

    def _cleanup(self, keep: Path | None) -> None:
        directory = self.config.screenshot_path()
        now = time.time()
        retention_secs = max(0, int(self.config.screenshot_retention_days)) * 86400
        files = sorted(
            [p for p in directory.glob("screenshot-*") if p.is_file() and p != keep],
            key=lambda p: p.stat().st_mtime,
        )

        if retention_secs > 0:
            for path in list(files):
                try:
                    if now - path.stat().st_mtime > retention_secs:
                        path.unlink(missing_ok=True)
                except OSError:
                    pass

        max_bytes = max(0, int(self.config.screenshot_max_total_mb)) * 1024 * 1024
        if max_bytes <= 0:
            return
        files = sorted(
            [p for p in directory.glob("screenshot-*") if p.is_file() and p != keep],
            key=lambda p: p.stat().st_mtime,
        )
        total = sum(p.stat().st_size for p in files)
        for path in files:
            if total <= max_bytes:
                break
            try:
                size = path.stat().st_size
                path.unlink(missing_ok=True)
                total -= size
            except OSError:
                pass


def _resize(image, max_width: int):
    if max_width <= 0 or image.width <= max_width:
        return image
    ratio = max_width / float(image.width)
    height = max(1, int(image.height * ratio))
    return image.resize((max_width, height))


def _normal_format(fmt: str) -> str:
    fmt = (fmt or "jpg").lower().strip()
    if fmt in ("jpeg", "jpg"):
        return "jpg"
    return "png"
