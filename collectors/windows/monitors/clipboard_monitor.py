"""Monitors clipboard changes and emits os.clipboard_copy events.

Polls the Windows clipboard every second. Handles multiple content types:

  - Text    : captured and truncated to _TEXT_PREVIEW_LEN chars.
              If the full text is longer, a head+tail preview is stored so
              the LLM gets the beginning and end without the whole blob.
  - Files   : file paths extracted from CF_HDROP (copy in Explorer, etc.)
  - Image   : pixel dimensions and size recorded; no raw data stored.
  - HTML    : plain-text extracted from CF_HTML; stored like regular text.

Sensitivity: L3 — opt-in only via  collect_clipboard: true  in config.

Events emitted:
  - os.clipboard_copy  (sensitivity L3)
"""

from __future__ import annotations

import logging
import re
import struct
import threading
import time
from queue import Queue
from typing import Optional

import win32clipboard
import win32con

from client import make_event
from config import Config
from monitors.helpers import get_foreground_info

logger = logging.getLogger(__name__)

# ── Tuning constants ──────────────────────────────────────────────────────────

# Characters to keep from the beginning of long text
_TEXT_HEAD = 400
# Characters to keep from the end of long text (gives LLM tail context too)
_TEXT_TAIL = 100
# Total threshold above which we split into head + tail
_TEXT_PREVIEW_LEN = _TEXT_HEAD + _TEXT_TAIL   # 500 chars stored max

# Minimum printable chars — single chars / whitespace are noise
_MIN_TEXT_LEN = 3

# Maximum file count to list (if user copies hundreds of files just note the count)
_MAX_FILES = 20

# Poll interval (seconds)
_POLL_INTERVAL = 1.0

# Minimum seconds before re-emitting identical content
_DEDUP_SECS = 60.0

# ── Clipboard format constants ────────────────────────────────────────────────

CF_HTML = win32clipboard.RegisterClipboardFormat("HTML Format")


# ── Content readers ───────────────────────────────────────────────────────────

def _read_clipboard() -> Optional[dict]:
    """Open clipboard once and extract the most useful available format.

    Returns a dict with keys: content_type, text (optional), files (optional),
    image_info (optional), raw_len.
    Returns None on error or empty clipboard.
    """
    try:
        win32clipboard.OpenClipboard()
        try:
            return _extract_content()
        finally:
            win32clipboard.CloseClipboard()
    except Exception as e:
        logger.debug("clipboard open error: %s", e)
        return None


def _extract_content() -> Optional[dict]:
    # Priority: files > text > HTML > image

    # 1. File list (CF_HDROP) — user copied files/folders in Explorer
    if win32clipboard.IsClipboardFormatAvailable(win32con.CF_HDROP):
        try:
            files = win32clipboard.GetClipboardData(win32con.CF_HDROP)
            if files:
                return {"content_type": "files", "files": list(files)}
        except Exception as e:
            logger.debug("CF_HDROP error: %s", e)

    # 2. Unicode text
    if win32clipboard.IsClipboardFormatAvailable(win32con.CF_UNICODETEXT):
        try:
            text = win32clipboard.GetClipboardData(win32con.CF_UNICODETEXT) or ""
            if text.strip():
                return {"content_type": "text", "text": text}
        except Exception as e:
            logger.debug("CF_UNICODETEXT error: %s", e)

    # 3. HTML (fallback for text)
    if win32clipboard.IsClipboardFormatAvailable(CF_HTML):
        try:
            raw = win32clipboard.GetClipboardData(CF_HTML)
            text = _extract_html_text(raw)
            if text and text.strip():
                return {"content_type": "html", "text": text}
        except Exception as e:
            logger.debug("CF_HTML error: %s", e)

    # 4. Bitmap / DIB image
    for fmt in (win32con.CF_DIB, win32con.CF_BITMAP):
        if win32clipboard.IsClipboardFormatAvailable(fmt):
            try:
                info = _extract_image_info(fmt)
                if info:
                    return {"content_type": "image", "image_info": info}
                # No structured info available, still record that an image was copied
                return {"content_type": "image", "image_info": {}}
            except Exception as e:
                logger.debug("image clipboard error: %s", e)
            break

    return None


def _extract_html_text(raw: bytes | str) -> str:
    """Strip HTML tags from CF_HTML clipboard data and return plain text."""
    if isinstance(raw, bytes):
        raw = raw.decode("utf-8", errors="replace")
    # CF_HTML has a header before the actual HTML; skip to <html or <body
    match = re.search(r"<(?:html|body|span|p|div)", raw, re.IGNORECASE)
    html = raw[match.start():] if match else raw
    # Strip tags
    text = re.sub(r"<[^>]+>", " ", html)
    text = re.sub(r"\s+", " ", text).strip()
    return text


def _extract_image_info(fmt: int) -> dict:
    """Return image metadata (width, height, approx size) from clipboard."""
    try:
        data = win32clipboard.GetClipboardData(fmt)
        if not data or len(data) < 40:
            return {"size_bytes": len(data) if data else 0}
        # BITMAPINFOHEADER: width at offset 4, height at 8 (both LONG = 4 bytes)
        width = struct.unpack_from("<i", data, 4)[0]
        height = abs(struct.unpack_from("<i", data, 8)[0])
        return {
            "width": width,
            "height": height,
            "size_bytes": len(data),
        }
    except Exception:
        return {"size_bytes": 0}


# ── Text processing ────────────────────────────────────────────────────────────

def _make_text_preview(text: str) -> tuple[str, int, bool]:
    """Return (preview, original_len, was_truncated).

    For long text, keeps _TEXT_HEAD chars from the start and _TEXT_TAIL from
    the end with a gap indicator in between — gives LLM the most context.
    """
    original_len = len(text)
    if original_len <= _TEXT_PREVIEW_LEN:
        return text, original_len, False
    head = text[:_TEXT_HEAD].rstrip()
    tail = text[-_TEXT_TAIL:].lstrip()
    preview = f"{head}\n…[{original_len - _TEXT_HEAD - _TEXT_TAIL} chars omitted]…\n{tail}"
    return preview, original_len, True


# ── Monitor ───────────────────────────────────────────────────────────────────

class ClipboardMonitor(threading.Thread):
    """Polls the Windows clipboard and emits os.clipboard_copy on changes."""

    def __init__(self, queue: Queue, config: Config):
        super().__init__(daemon=True, name="ClipboardMonitor")
        self.queue = queue
        self.config = config

        # Dedup state — keyed on a fingerprint of the clipboard content
        self._last_fingerprint: str = ""
        self._last_emit_ts: float = 0.0

    def run(self) -> None:
        # Seed to avoid emitting whatever is already on the clipboard at start
        initial = _read_clipboard()
        self._last_fingerprint = self._fingerprint(initial)
        logger.debug("clipboard monitor started")

        while True:
            time.sleep(_POLL_INTERVAL)
            try:
                self._check()
            except Exception as e:
                logger.debug("clipboard check error: %s", e)

    @staticmethod
    def _fingerprint(content: Optional[dict]) -> str:
        """Cheap content fingerprint for dedup."""
        if not content:
            return ""
        ct = content.get("content_type", "")
        if ct in ("text", "html"):
            t = content.get("text", "")
            return f"{ct}:{len(t)}:{t[:80]}"
        if ct == "files":
            return "files:" + "|".join(content.get("files", [])[:5])
        if ct == "image":
            info = content.get("image_info", {})
            return f"image:{info.get('width')}x{info.get('height')}:{info.get('size_bytes')}"
        return ""

    def _check(self) -> None:
        content = _read_clipboard()
        if not content:
            return

        fp = self._fingerprint(content)
        if not fp:
            return

        now = time.time()
        if fp == self._last_fingerprint and (now - self._last_emit_ts) < _DEDUP_SECS:
            return
        if fp == self._last_fingerprint:
            return

        self._last_fingerprint = fp

        fg = get_foreground_info()
        app = fg["app"] if fg else ""
        app_name = fg["app_name"] if fg else ""

        if self.config.should_ignore_app(app):
            return

        self._last_emit_ts = now
        self._emit(content, app, app_name, fg)

    def _emit(self, content: dict, app: str, app_name: str, fg: Optional[dict]) -> None:
        ct = content["content_type"]

        labels: dict = {"content_type": ct}
        if app:
            labels["app"] = app
        display = app_name or (fg["title"] if fg else "") or ""
        if display and display != app:
            labels["app_name"] = display

        payload: dict = {}

        if ct in ("text", "html"):
            raw_text = content.get("text", "")
            stripped = raw_text.strip()
            if len(stripped) < _MIN_TEXT_LEN:
                return
            preview, original_len, truncated = _make_text_preview(stripped)
            payload["text"] = preview
            payload["text_len"] = original_len
            if truncated:
                payload["truncated"] = True
            logger.info("clipboard_copy text: app=%s len=%d preview=%r", app, original_len, stripped[:60])

        elif ct == "files":
            files = content.get("files", [])
            total = len(files)
            listed = files[:_MAX_FILES]
            # Store just filenames for brevity; full paths in payload
            payload["files"] = listed
            payload["file_count"] = total
            if total > _MAX_FILES:
                payload["truncated"] = True
            labels["file_count"] = str(total)
            logger.info("clipboard_copy files: app=%s count=%d", app, total)

        elif ct == "image":
            info = content.get("image_info", {})
            if info.get("width") and info.get("height"):
                size_kb = info.get("size_bytes", 0) // 1024
                payload["width"] = info["width"]
                payload["height"] = info["height"]
                payload["size_kb"] = size_kb
                labels["dimensions"] = f"{info['width']}x{info['height']}"
                logger.info(
                    "clipboard_copy image: app=%s %dx%d ~%dKB",
                    app, info["width"], info["height"], size_kb,
                )
            else:
                payload["note"] = "image (dimensions unavailable)"
                logger.info("clipboard_copy image: app=%s (no dimension info)", app)

        self.queue.put(make_event(
            source="os",
            event_type="clipboard_copy",
            sensitivity=3,
            labels=labels,
            payload=payload,
        ))
