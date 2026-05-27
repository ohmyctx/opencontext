"""Monitors keyboard activity to detect text submission in UI fields.

Strategy (privacy-preserving, default):
  - Does NOT log individual keystrokes.
  - Two complementary mechanisms to capture submitted/entered text:

  1. FOCUS-CHANGE SAMPLER (primary): polls the focused control every 0.5s.
     When focus leaves an edit field, emits the text that was in it.
     Catches web inputs, IME input, search boxes, chat messages — anything
     where Enter is not the submit trigger.

  2. ENTER/TAB TRIGGER (supplementary): on Enter or Tab, immediately reads
     the focused edit field value. Useful for forms and terminal-style apps.

  Password fields (UIAutomation IsPassword == True) are always skipped.

Optional (collect_raw_keys = True, sensitivity L3):
  - Records individual key names as they are pressed.
  - Emits os.key_press events — never captures passwords.

Events emitted:
  - os.text_input   (sensitivity L2, default on)
  - os.key_press    (sensitivity L3, opt-in only)
"""

from __future__ import annotations

import ctypes
import logging
import threading
import time
from queue import Queue
from typing import Optional

import psutil
import win32gui
import win32process

from client import make_event
from config import Config
from monitors.helpers import get_foreground_info, get_friendly_name

logger = logging.getLogger(__name__)

_com_initialized = threading.local()

# Control types that can hold user-typed text
_TEXT_CONTROL_TYPES = {"Edit", "Document", "ComboBox", "DataItem", "Text"}

# Minimum text length to bother emitting
_MIN_TEXT_LEN = 3

# Poll interval for the focus-change sampler (seconds)
_SAMPLE_INTERVAL = 0.5

# Don't emit the same text for the same field twice unless it changed
_DEDUP_WINDOW_SECS = 30.0


def _ensure_com() -> None:
    if not getattr(_com_initialized, "done", False):
        ctypes.windll.ole32.CoInitialize(None)
        _com_initialized.done = True


def _get_focused_field() -> Optional[dict]:
    """Return info about the currently focused text control, or None.

    Returns a dict with: control_type, control_name, window_title, text, app, app_name.
    Returns None if the focused control is not a text-input type.
    """
    _ensure_com()
    try:
        import uiautomation as auto

        element = auto.GetFocusedControl()
        if element is None:
            return None

        control_type = element.ControlTypeName or ""
        if control_type not in _TEXT_CONTROL_TYPES:
            logger.debug("focused control type=%s (not a text field)", control_type)
            return None

        if element.IsPassword:
            return None

        text = ""
        try:
            vp = element.GetValuePattern()
            if vp:
                text = vp.Value or ""
        except Exception:
            pass

        # Fallback: try TextPattern for Document/rich-text controls
        if not text:
            try:
                tp = element.GetTextPattern()
                if tp:
                    text = tp.DocumentRange.GetText(2000) or ""
            except Exception:
                pass

        name = element.Name or ""
        window_title = ""
        try:
            top = element.GetTopLevelControl()
            if top:
                window_title = top.Name or ""
        except Exception:
            pass

        fg = get_foreground_info()
        app = fg["app"] if fg else ""
        app_name = fg["app_name"] if fg else ""

        return {
            "control_type": control_type,
            "control_name": name,
            "window_title": window_title,
            "text": text,
            "app": app,
            "app_name": app_name,
        }

    except ImportError:
        return None
    except Exception as e:
        logger.debug("get_focused_field error: %s", e)
        return None


class KeyboardMonitor:
    """Captures text input using focus-change tracking + Enter/Tab triggers."""

    def __init__(self, queue: Queue, config: Config):
        self.queue = queue
        self.config = config
        self._listener = None
        self._sampler_thread: Optional[threading.Thread] = None

        # Focus-change sampler state (written from sampler thread only)
        self._prev_field_key: Optional[tuple] = None  # (control_name, window_title)
        self._prev_text: str = ""
        self._prev_app: str = ""
        self._prev_app_name: str = ""
        self._last_emitted_key: Optional[tuple] = None  # (field_key, text)
        self._last_emitted_ts: float = 0.0

    # ── Lifecycle ────────────────────────────────────────────────────────────

    def start(self) -> None:
        if self.config.collect_text_input:
            self._sampler_thread = threading.Thread(
                target=self._sampler_loop,
                daemon=True,
                name="KeyboardSampler",
            )
            self._sampler_thread.start()
            logger.debug("keyboard sampler started")

        if self.config.collect_raw_keys or self.config.collect_text_input:
            from pynput import keyboard

            self._listener = keyboard.Listener(
                on_press=self._on_press,
                on_release=self._on_release,
            )
            self._listener.daemon = True
            self._listener.start()
            logger.debug("keyboard listener started")

    def stop(self) -> None:
        if self._listener:
            self._listener.stop()

    # ── Focus-change sampler ─────────────────────────────────────────────────

    def _sampler_loop(self) -> None:
        """Poll the focused edit control; emit text when focus leaves the field."""
        while True:
            time.sleep(_SAMPLE_INTERVAL)
            try:
                self._sample_once()
            except Exception as e:
                logger.debug("sampler error: %s", e)

    def _sample_once(self) -> None:
        field = _get_focused_field()

        if field:
            text = (field.get("text") or "").strip()
            field_key = (field["control_name"], field["window_title"])
            app = field["app"]
            app_name = field["app_name"]

            if field_key != self._prev_field_key:
                # Focus moved to a NEW edit field — emit text from the OLD field (if any)
                if self._prev_field_key and self._prev_text:
                    self._maybe_emit(
                        self._prev_field_key,
                        self._prev_text,
                        self._prev_app,
                        self._prev_app_name,
                        trigger="focus_change",
                    )
                self._prev_field_key = field_key
                self._prev_text = text
                self._prev_app = app
                self._prev_app_name = app_name
            else:
                # Same field — just update the tracked text
                if text:
                    self._prev_text = text

        else:
            # No edit control focused — emit text from previous field if meaningful
            if self._prev_field_key and self._prev_text:
                self._maybe_emit(
                    self._prev_field_key,
                    self._prev_text,
                    self._prev_app,
                    self._prev_app_name,
                    trigger="focus_lost",
                )
            self._prev_field_key = None
            self._prev_text = ""
            self._prev_app = ""
            self._prev_app_name = ""

    # ── Enter / Tab trigger ──────────────────────────────────────────────────

    def _on_press(self, key) -> None:
        try:
            self._handle_key(key, pressed=True)
        except Exception as e:
            logger.debug("keyboard on_press error: %s", e)

    def _on_release(self, key) -> None:
        pass

    def _handle_key(self, key, pressed: bool) -> None:
        from pynput.keyboard import Key

        ts = int(time.time() * 1000)

        # --- Text input on Enter / Tab (supplementary to sampler) ---
        if self.config.collect_text_input and key in (Key.enter, Key.tab):
            field = _get_focused_field()
            if field:
                text = (field.get("text") or "").strip()
                app = field["app"]
                app_name = field["app_name"]
                field_key = (field["control_name"], field["window_title"])
                trigger = "enter" if key == Key.enter else "tab"

                if text and not self.config.should_ignore_app(app):
                    wt = field.get("window_title", "")
                    if not self.config.should_ignore_title(wt):
                        self._maybe_emit(field_key, text, app, app_name, trigger, ts=ts)
                        # Reset sampler state so we don't double-emit
                        self._prev_text = ""

        # --- Raw key press capture (opt-in, L3) ---
        if self.config.collect_raw_keys:
            self._emit_key_press(key, ts)

    # ── Emit helpers ─────────────────────────────────────────────────────────

    def _maybe_emit(
        self,
        field_key: tuple,
        text: str,
        app: str,
        app_name: str,
        trigger: str,
        ts: Optional[int] = None,
    ) -> None:
        """Emit a text_input event, skipping duplicates and very short strings."""
        if len(text) < _MIN_TEXT_LEN:
            return
        if self.config.should_ignore_app(app):
            return

        control_name, window_title = field_key
        if window_title and self.config.should_ignore_title(window_title):
            return

        # Dedup: don't re-emit the same (field, text) within the dedup window
        emit_key = (field_key, text)
        now = time.time()
        if emit_key == self._last_emitted_key and (now - self._last_emitted_ts) < _DEDUP_WINDOW_SECS:
            logger.debug("skip duplicate text_input for field %s", field_key)
            return

        self._last_emitted_key = emit_key
        self._last_emitted_ts = now

        labels: dict = {}
        if app:
            labels["app"] = app
        display_name = app_name or window_title or ""
        if display_name and display_name != app:
            labels["app_name"] = display_name
        if control_name:
            labels["control_name"] = control_name
        if window_title:
            labels["window_title"] = window_title

        payload: dict = {
            "text": text[:500],
            "trigger": trigger,
        }

        self.queue.put(make_event(
            source="os",
            event_type="text_input",
            sensitivity=2,
            labels=labels,
            payload=payload,
            ts=ts,
        ))
        logger.info(
            "text_input: app=%s control=%r window=%r len=%d trigger=%s",
            app, control_name, window_title, len(text), trigger,
        )

    def _emit_key_press(self, key, ts: int) -> None:
        """Emit individual key press events (L3 opt-in only)."""
        from pynput.keyboard import Key

        try:
            import uiautomation as auto
            _ensure_com()
            el = auto.GetFocusedControl()
            if el and el.IsPassword:
                return
        except Exception:
            pass

        try:
            key_name = key.char if hasattr(key, "char") and key.char else key.name
        except Exception:
            key_name = str(key)

        if not key_name:
            return

        fg = get_foreground_info()
        app = fg["app"] if fg else ""
        if self.config.should_ignore_app(app):
            return

        labels: dict = {"app": app} if app else {}
        if fg and fg["app_name"]:
            labels["app_name"] = fg["app_name"]

        self.queue.put(make_event(
            source="os",
            event_type="key_press",
            sensitivity=3,
            labels=labels,
            payload={"key": key_name},
            ts=ts,
        ))
