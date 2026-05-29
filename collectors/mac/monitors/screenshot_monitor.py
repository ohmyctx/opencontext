"""Periodic macOS screenshot monitor.

Emits:
  os.screenshot — local screenshot file path only (L3)

The image is stored locally and never uploaded in the event payload. Agents may
choose to read the referenced path when the user allows visual context.
"""

from __future__ import annotations

import logging
import subprocess
import threading
import time
from pathlib import Path
from queue import Queue

from client import make_event
from config import Config
from monitors.helpers import get_frontmost_app

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

    def _capture_once(self) -> Path:
        directory = self.config.screenshot_path()
        directory.mkdir(parents=True, exist_ok=True)
        ts = int(time.time() * 1000)
        fmt = _normal_format(self.config.screenshot_format)
        raw_path = directory / f"screenshot-{ts}.png"
        final_path = directory / f"screenshot-{ts}.{fmt}"

        subprocess.run(["/usr/sbin/screencapture", "-x", str(raw_path)], check=True, timeout=20)
        if fmt == "jpg":
            self._resize_and_convert(raw_path, final_path)
            raw_path.unlink(missing_ok=True)
        else:
            self._resize_png(raw_path)
            final_path = raw_path

        stat = final_path.stat()
        width, height = _image_dimensions(final_path)
        fg = get_frontmost_app() or {}

        labels = {
            "content_type": "image",
            "storage": "local_path",
            "platform": "macos",
        }
        if app := fg.get("app"):
            labels["app"] = app
        if title := fg.get("title"):
            labels["title"] = title[:120]

        payload = {
            "path": str(final_path),
            "format": fmt,
            "size_bytes": stat.st_size,
            "width": width,
            "height": height,
            "max_width": int(self.config.screenshot_max_width),
            "retention_days": int(self.config.screenshot_retention_days),
            "max_total_mb": int(self.config.screenshot_max_total_mb),
        }
        for key in ("app", "bundle_id", "pid", "title", "url"):
            if fg.get(key):
                payload[key] = fg[key]

        self.queue.put(make_event(
            source="os",
            event_type="screenshot",
            sensitivity=3,
            labels=labels,
            payload=payload,
        ))
        logger.info("screenshot captured path=%s size=%d", final_path, stat.st_size)
        return final_path

    def _resize_and_convert(self, src: Path, dst: Path) -> None:
        max_width = int(self.config.screenshot_max_width)
        if max_width > 0:
            subprocess.run(
                ["/usr/bin/sips", "-Z", str(max_width), "-s", "format", "jpeg", str(src), "--out", str(dst)],
                check=True,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
                timeout=30,
            )
        else:
            subprocess.run(
                ["/usr/bin/sips", "-s", "format", "jpeg", str(src), "--out", str(dst)],
                check=True,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
                timeout=30,
            )

    def _resize_png(self, path: Path) -> None:
        max_width = int(self.config.screenshot_max_width)
        if max_width > 0:
            subprocess.run(
                ["/usr/bin/sips", "-Z", str(max_width), str(path)],
                check=True,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
                timeout=30,
            )

    def _cleanup(self, keep: Path) -> None:
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


def _normal_format(fmt: str) -> str:
    fmt = (fmt or "jpg").lower().strip()
    if fmt in ("jpeg", "jpg"):
        return "jpg"
    return "png"


def _image_dimensions(path: Path) -> tuple[int, int]:
    try:
        proc = subprocess.run(
            ["/usr/bin/sips", "-g", "pixelWidth", "-g", "pixelHeight", str(path)],
            check=True,
            capture_output=True,
            text=True,
            timeout=10,
        )
        width = 0
        height = 0
        for line in proc.stdout.splitlines():
            line = line.strip()
            if line.startswith("pixelWidth:"):
                width = int(line.split(":", 1)[1].strip())
            elif line.startswith("pixelHeight:"):
                height = int(line.split(":", 1)[1].strip())
        return width, height
    except Exception:
        return 0, 0
