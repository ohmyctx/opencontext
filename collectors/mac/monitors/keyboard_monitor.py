"""macOS keyboard / text-input monitor.

Dual mechanism (mirrors the Windows collector):
1. Focus-change sampler: polls the focused AX element every 0.5s.
   When focus leaves a text field, emits the accumulated text.
2. Return/Tab trigger: emits immediately on form submission.

Emits:
  os.text_input  — text submitted or typed in a field (L2)
  os.key_press   — individual keystrokes (L3, opt-in only)
"""

from __future__ import annotations

import logging
import threading
import time
from queue import Queue
from typing import Optional

from pynput import keyboard  # type: ignore

from client import make_event
from config import Config
from monitors.helpers import get_focused_field, get_frontmost_app, check_accessibility_permission

logger = logging.getLogger(__name__)

_MIN_TEXT_LEN = 3
_SAMPLE_INTERVAL = 0.5       # poll focused field every 0.5s
_DEDUP_WINDOW_SECS = 30.0    # don't re-emit same text within 30s

# AX roles treated as text input
_TEXT_ROLES = {"AXTextField", "AXTextArea", "AXComboBox", "AXWebArea", "AXSearchField"}


class KeyboardMonitor:
    def __init__(self, queue: Queue, config: Config) -> None:
        self.queue = queue
        self.config = config

        self._sampler_thread: Optional[threading.Thread] = None
        self._listener: Optional[keyboard.Listener] = None

        # Sampler state
        self._prev_field_key: Optional[tuple] = None
        self._prev_text: str = ""
        self._prev_app: str = ""

        # Dedup state
        self._last_emitted_key: Optional[tuple] = None
        self._last_emitted_ts: float = 0.0

    def start(self) -> None:
        has_ax = check_accessibility_permission()

        if self.config.collect_text_input and has_ax:
            self._sampler_thread = threading.Thread(
                target=self._sampler_loop, daemon=True, name="KeyboardSampler"
            )
            self._sampler_thread.start()
        elif self.config.collect_text_input and not has_ax:
            logger.warning(
                "Accessibility permission not granted — text-input capture disabled.\n"
                "  Fix: add OpenContext Collector.app in Applications → Accessibility"
            )

        if has_ax:
            self._listener = keyboard.Listener(on_press=self._on_key_press)
            self._listener.start()

    def stop(self) -> None:
        if self._listener:
            self._listener.stop()

    # ------------------------------------------------------------------
    # Focus-change sampler
    # ------------------------------------------------------------------

    def _sampler_loop(self) -> None:
        while True:
            time.sleep(_SAMPLE_INTERVAL)
            try:
                self._sample_once()
            except Exception as e:
                logger.debug("sampler error: %s", e)

    def _sample_once(self) -> None:
        field = get_focused_field()
        fg = get_frontmost_app()
        app = fg["app"] if fg else ""

        if self.config.should_ignore_app(app):
            self._prev_field_key = None
            self._prev_text = ""
            return

        if field and field.get("role") in _TEXT_ROLES:
            text = field.get("text", "")
            field_key = (field.get("role", ""), field.get("field_label", ""), app)

            if field_key == self._prev_field_key:
                # Same field — just update accumulated text
                self._prev_text = text
                return

            # Focus moved to a different field — emit previous field's text
            if self._prev_field_key is not None and len(self._prev_text) >= _MIN_TEXT_LEN:
                self._maybe_emit(
                    text=self._prev_text,
                    field_key=self._prev_field_key,
                    app=self._prev_app,
                    trigger="focus_leave",
                )

            self._prev_field_key = field_key
            self._prev_text = text
            self._prev_app = app
        else:
            # Focus left all text fields
            if self._prev_field_key is not None and len(self._prev_text) >= _MIN_TEXT_LEN:
                self._maybe_emit(
                    text=self._prev_text,
                    field_key=self._prev_field_key,
                    app=self._prev_app,
                    trigger="focus_leave",
                )
            self._prev_field_key = None
            self._prev_text = ""

    # ------------------------------------------------------------------
    # Return / Tab trigger (immediate emission)
    # ------------------------------------------------------------------

    def _on_key_press(self, key) -> None:
        try:
            if key in (keyboard.Key.enter, keyboard.Key.tab):
                if self.config.collect_text_input and self._prev_text and self._prev_field_key:
                    if len(self._prev_text) >= _MIN_TEXT_LEN:
                        self._maybe_emit(
                            text=self._prev_text,
                            field_key=self._prev_field_key,
                            app=self._prev_app,
                            trigger="submit",
                        )

            if self.config.collect_raw_keys:
                self._emit_key_press(key)

        except Exception as e:
            logger.debug("key press handler error: %s", e)

    # ------------------------------------------------------------------
    # Emit helpers
    # ------------------------------------------------------------------

    def _maybe_emit(self, text: str, field_key: tuple, app: str, trigger: str) -> None:
        emit_key = (field_key, text[:50])
        now = time.monotonic()
        if emit_key == self._last_emitted_key and (now - self._last_emitted_ts) < _DEDUP_WINDOW_SECS:
            return
        self._last_emitted_key = emit_key
        self._last_emitted_ts = now

        _, field_label, _ = field_key
        labels: dict = {"app": app}
        payload: dict = {"app": app, "text": text, "trigger": trigger}
        if field_label:
            labels["control_name"] = field_label
            payload["control_name"] = field_label

        self.queue.put(make_event(
            source="os",
            event_type="text_input",
            sensitivity=2,
            labels=labels,
            payload=payload,
        ))

    def _emit_key_press(self, key) -> None:
        fg = get_frontmost_app()
        app = fg["app"] if fg else ""
        if self.config.should_ignore_app(app):
            return
        try:
            char = key.char
        except AttributeError:
            char = str(key)
        labels: dict = {"app": app} if app else {}
        self.queue.put(make_event(
            source="os",
            event_type="key_press",
            sensitivity=3,
            labels=labels,
            payload={"key": char, "app": app},
        ))
