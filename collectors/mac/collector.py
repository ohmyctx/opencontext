#!/usr/bin/env python3
"""macOS UI Activity Collector for OpenContext.

Monitors user activity on macOS and pushes structured events to contextd:
  - os.window_focus   — which app/window is in focus (+ URL for browsers)
  - os.browser_nav    — URL changes within a browser tab
  - os.ui_click       — UI element clicks (requires Accessibility permission)
  - os.text_input     — text submitted in input fields (L2)
  - os.app_launch     — new applications launched
  - os.clipboard_copy — clipboard content changes (L3)
  - os.key_press      — individual keystrokes (L3, opt-in)

Permissions required:
  System Settings → Privacy & Security → Accessibility → allow this terminal/app
  (Screen Recording not needed unless you enable future OCR features)

Usage:
  python collector.py               # run in foreground, push to contextd
  python collector.py --debug       # verbose logging
  python collector.py --dry-run     # print JSON events, don't push
"""

from __future__ import annotations

import argparse
import json
import logging
import signal
import sys
import time
from queue import Empty, Queue

from client import ContextdClient
from config import Config
from monitors.click_monitor import ClickMonitor
from monitors.clipboard_monitor import ClipboardMonitor
from monitors.keyboard_monitor import KeyboardMonitor
from monitors.process_monitor import ProcessMonitor
from monitors.window_monitor import WindowMonitor

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s %(levelname)s [%(name)s] %(message)s",
    datefmt="%H:%M:%S",
)
logger = logging.getLogger("collector")


def parse_args() -> argparse.Namespace:
    p = argparse.ArgumentParser(
        description="OpenContext macOS UI Activity Collector",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=__doc__,
    )
    p.add_argument("--url", default=None, metavar="URL",
                   help="contextd base URL (default: http://localhost:6060)")
    p.add_argument("--config", default=None, metavar="PATH",
                   help="path to YAML config file")
    p.add_argument("--dry-run", action="store_true",
                   help="print events as JSON instead of pushing to contextd")
    p.add_argument("--debug", action="store_true",
                   help="enable debug-level logging")
    p.add_argument("--no-clicks", action="store_true", help="disable click monitoring")
    p.add_argument("--no-keys", action="store_true",   help="disable keyboard monitoring")
    p.add_argument("--no-processes", action="store_true", help="disable process monitoring")
    return p.parse_args()


def _drain(q: Queue) -> list[dict]:
    events: list[dict] = []
    try:
        while True:
            events.append(q.get_nowait())
    except Empty:
        pass
    return events


def main() -> None:
    args = parse_args()
    if args.debug:
        logging.getLogger().setLevel(logging.DEBUG)

    config = Config.load(args.config)
    if args.url:
        config.contextd_url = args.url

    client = ContextdClient(config.contextd_url)

    if not args.dry_run:
        if client.is_alive():
            logger.info("connected to contextd at %s", config.contextd_url)
        else:
            logger.warning(
                "contextd not reachable at %s — events will be dropped until it starts",
                config.contextd_url,
            )

    event_queue: Queue = Queue()
    started = []

    # ── Window focus + browser nav ────────────────────────────────────
    try:
        wm = WindowMonitor(event_queue, config)
        wm.start()
        started.append(wm)
        logger.info("window monitor   started  (os.window_focus, os.browser_nav)")
    except Exception as e:
        logger.error("window monitor failed to start: %s", e)

    # ── UI click ─────────────────────────────────────────────────────
    if not args.no_clicks:
        try:
            cm = ClickMonitor(event_queue, config)
            cm.start()
            started.append(cm)
            logger.info("click monitor    started  (os.ui_click)")
        except Exception as e:
            logger.error("click monitor failed to start: %s", e)

    # ── Keyboard / text input ────────────────────────────────────────
    if not args.no_keys:
        try:
            km = KeyboardMonitor(event_queue, config)
            km.start()
            started.append(km)
            suffix = " + os.key_press L3" if config.collect_raw_keys else ""
            logger.info("keyboard monitor started  (os.text_input%s)", suffix)
        except Exception as e:
            logger.error("keyboard monitor failed to start: %s", e)

    # ── App launch ───────────────────────────────────────────────────
    if not args.no_processes:
        try:
            pm = ProcessMonitor(event_queue, config)
            pm.start()
            started.append(pm)
            logger.info("process monitor  started  (os.app_launch)")
        except Exception as e:
            logger.error("process monitor failed to start: %s", e)

    # ── Clipboard ────────────────────────────────────────────────────
    if config.collect_clipboard:
        try:
            cbm = ClipboardMonitor(event_queue, config)
            cbm.start()
            started.append(cbm)
            logger.info("clipboard monitor started (os.clipboard_copy L3)")
        except Exception as e:
            logger.error("clipboard monitor failed to start: %s", e)

    if not started:
        logger.error("no monitors could start — exiting")
        sys.exit(1)

    logger.info(
        "collector running — flushing every %.1fs  (Ctrl+C to stop)",
        config.flush_interval,
    )

    def _shutdown(sig, frame) -> None:
        logger.info("shutting down…")
        sys.exit(0)

    signal.signal(signal.SIGINT, _shutdown)
    signal.signal(signal.SIGTERM, _shutdown)

    total_pushed = 0
    while True:
        time.sleep(config.flush_interval)
        events = _drain(event_queue)
        if not events:
            continue

        if args.dry_run:
            for e in events:
                print(json.dumps(e, ensure_ascii=False), flush=True)
            continue

        result = client.push_batch(events)
        accepted = result.get("accepted", len(events))
        total_pushed += accepted
        logger.info("flushed %d events  (total: %d)", accepted, total_pushed)


if __name__ == "__main__":
    main()
