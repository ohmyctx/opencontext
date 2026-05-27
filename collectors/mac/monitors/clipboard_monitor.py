"""macOS clipboard monitor.

Polls NSPasteboard for changes using the changeCount mechanism.
NSPasteboard increments changeCount on every write, making polling cheap.

Emits:
  os.clipboard_copy  — when clipboard content changes (L3)

Content handling:
  - Text: captured up to 500 chars (head + tail), full length stored
  - Files: paths listed (up to 20), no file content
  - Images: dimensions + format only, no pixel data
  - HTML: stripped to plain text
"""

from __future__ import annotations

import hashlib
import logging
import threading
import time
from queue import Queue
from typing import Optional

from client import make_event
from config import Config
from monitors.helpers import get_frontmost_app

logger = logging.getLogger(__name__)

_POLL_INTERVAL = 1.0
_DEDUP_SECS = 60.0
_MIN_TEXT_LEN = 3
_TEXT_HEAD = 400
_TEXT_TAIL = 100
_MAX_FILES = 20


class ClipboardMonitor(threading.Thread):
    def __init__(self, queue: Queue, config: Config) -> None:
        super().__init__(daemon=True, name="ClipboardMonitor")
        self.queue = queue
        self.config = config
        self._last_change_count: int = -1
        self._last_fingerprint: str = ""
        self._last_emit_ts: float = 0.0

    def run(self) -> None:
        try:
            from AppKit import NSPasteboard  # type: ignore
        except ImportError:
            logger.error("AppKit not available — clipboard monitor disabled")
            return

        pb = NSPasteboard.generalPasteboard()
        logger.debug("clipboard monitor running")

        while True:
            time.sleep(_POLL_INTERVAL)
            try:
                self._check(pb)
            except Exception as e:
                logger.debug("clipboard check error: %s", e)

    def _check(self, pb) -> None:
        change_count = pb.changeCount()
        if change_count == self._last_change_count:
            return
        self._last_change_count = change_count

        content = self._read(pb)
        if content is None:
            return

        fp = self._fingerprint(content)
        now = time.monotonic()
        if fp == self._last_fingerprint and (now - self._last_emit_ts) < _DEDUP_SECS:
            return
        self._last_fingerprint = fp
        self._last_emit_ts = now

        fg = get_frontmost_app()
        app = fg["app"] if fg else ""
        if self.config.should_ignore_app(app):
            return

        self._emit(content, app, fg)

    # ------------------------------------------------------------------
    # Read clipboard
    # ------------------------------------------------------------------

    def _read(self, pb) -> Optional[dict]:
        try:
            from AppKit import (  # type: ignore
                NSStringPboardType,
                NSFilenamesPboardType,
                NSTIFFPboardType,
                NSPasteboardTypeString,
                NSPasteboardTypePNG,
                NSPasteboardTypeHTML,
            )
        except ImportError:
            pass

        types = pb.types()
        if not types:
            return None

        type_strs = [str(t) for t in types]

        # Priority: files > text > html > image
        if "NSFilenamesPboardType" in type_strs or "public.file-url" in type_strs:
            return self._read_files(pb)

        for text_type in ("public.utf8-plain-text", "NSStringPboardType", "public.plain-text"):
            if text_type in type_strs:
                result = self._read_text(pb, text_type)
                if result:
                    return result

        for html_type in ("public.html", "NSHTMLPboardType"):
            if html_type in type_strs:
                result = self._read_html(pb, html_type)
                if result:
                    return result

        for img_type in ("public.tiff", "public.png", "NSTIFFPboardType"):
            if img_type in type_strs:
                return {"content_type": "image", "image_format": img_type}

        return None

    def _read_text(self, pb, ptype: str) -> Optional[dict]:
        text = pb.stringForType_(ptype)
        if not text:
            return None
        text = str(text)
        if len(text) < _MIN_TEXT_LEN:
            return None
        preview, total_len, truncated = _make_preview(text)
        return {
            "content_type": "text",
            "text": preview,
            "text_len": total_len,
            "truncated": truncated,
        }

    def _read_html(self, pb, ptype: str) -> Optional[dict]:
        raw = pb.dataForType_(ptype)
        if not raw:
            return None
        try:
            html = bytes(raw).decode("utf-8", errors="replace")
            text = _strip_html(html)
            if len(text) < _MIN_TEXT_LEN:
                return None
            preview, total_len, truncated = _make_preview(text)
            return {
                "content_type": "text",
                "text": preview,
                "text_len": total_len,
                "truncated": truncated,
                "source_format": "html",
            }
        except Exception:
            return None

    def _read_files(self, pb) -> Optional[dict]:
        # Try NSFilenamesPboardType first (older API, returns full paths)
        paths = pb.propertyListForType_("NSFilenamesPboardType")
        if not paths:
            # Try public.file-url (NSURL-based)
            urls = pb.propertyListForType_("public.file-url")
            paths = [str(urls)] if urls else []

        if not paths:
            return None
        paths = [str(p) for p in paths][:_MAX_FILES]
        return {
            "content_type": "files",
            "files": paths,
            "file_count": len(paths),
        }

    # ------------------------------------------------------------------
    # Emit event
    # ------------------------------------------------------------------

    def _emit(self, content: dict, app: str, fg: Optional[dict]) -> None:
        ct = content.get("content_type", "unknown")

        labels: dict = {"content_type": ct}
        payload: dict = dict(content)
        if app:
            labels["app"] = app
            payload["app"] = app
        if fg and fg.get("bundle_id"):
            labels["bundle_id"] = fg["bundle_id"]

        # Promote preview text to labels for summary generation
        if ct == "text" and content.get("text"):
            labels["text"] = content["text"][:120]
        elif ct == "files" and content.get("files"):
            labels["files"] = content["files"][:3]

        self.queue.put(make_event(
            source="os",
            event_type="clipboard_copy",
            sensitivity=3,
            labels=labels,
            payload=payload,
        ))
        logger.info(
            "clipboard_copy %s: app=%s len=%s preview=%r",
            ct,
            app,
            content.get("text_len", content.get("file_count", "")),
            content.get("text", content.get("files", ""))[:80],
        )

    @staticmethod
    def _fingerprint(content: Optional[dict]) -> str:
        if content is None:
            return ""
        key = f"{content.get('content_type')}:{content.get('text', '')[:200]}:{content.get('files', '')}"
        return hashlib.sha1(key.encode()).hexdigest()


# ---------------------------------------------------------------------------
# Utilities
# ---------------------------------------------------------------------------

def _make_preview(text: str) -> tuple[str, int, bool]:
    total = len(text)
    max_len = _TEXT_HEAD + _TEXT_TAIL
    if total <= max_len:
        return text, total, False
    preview = text[:_TEXT_HEAD] + " … " + text[-_TEXT_TAIL:]
    return preview, total, True


def _strip_html(html: str) -> str:
    import re
    text = re.sub(r"<[^>]+>", " ", html)
    text = re.sub(r"&nbsp;", " ", text)
    text = re.sub(r"&amp;", "&", text)
    text = re.sub(r"&lt;", "<", text)
    text = re.sub(r"&gt;", ">", text)
    text = re.sub(r"\s+", " ", text).strip()
    return text
