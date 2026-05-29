#!/usr/bin/env python3
"""Windows UI Activity Collector for OpenContext.

Monitors user activity on Windows and pushes structured events to the OpenContext daemon:
  - os.window_focus   — which app/window the user is working in
  - os.ui_click       — buttons/controls clicked (with element name & type)
  - os.text_input     — text submitted in input fields (Enter/Tab)
  - os.app_launch     — new applications started
  - os.screenshot     — local screenshot path (L3, opt-in)
  - os.key_press      — individual keystrokes (L3 opt-in only)

Usage:
  python collector.py               # run in foreground
  python collector.py --debug       # verbose logging
  python collector.py --dry-run     # print events, don't push to OpenContext daemon
  pythonw collector.py              # run silently (no console window)
"""

from __future__ import annotations

import argparse
import io
import json
import logging
import signal
import sys
import time
from queue import Empty, Queue

from client import OpenContextClient
from config import Config
from monitors.click_monitor import ClickMonitor
from monitors.clipboard_monitor import ClipboardMonitor
from monitors.keyboard_monitor import KeyboardMonitor
from monitors.process_monitor import ProcessMonitor
from monitors.screenshot_monitor import ScreenshotMonitor
from monitors.window_monitor import WindowMonitor

import sys
import io

# Force UTF-8 output on Windows to handle CJK characters in control names
if sys.stdout.encoding and sys.stdout.encoding.lower() != "utf-8":
    sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding="utf-8", errors="replace")
if sys.stderr.encoding and sys.stderr.encoding.lower() != "utf-8":
    sys.stderr = io.TextIOWrapper(sys.stderr.buffer, encoding="utf-8", errors="replace")

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s %(levelname)s [%(name)s] %(message)s",
    datefmt="%H:%M:%S",
)
# Suppress verbose comtypes internals — they spam at DEBUG level
logging.getLogger("comtypes").setLevel(logging.WARNING)
logging.getLogger("comtypes.client").setLevel(logging.WARNING)

logger = logging.getLogger("collector")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="OpenContext Windows UI Activity Collector",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=__doc__,
    )
    parser.add_argument(
        "--url",
        default=None,
        metavar="URL",
        help="OpenContext daemon base URL (default: http://localhost:6060)",
    )
    parser.add_argument(
        "--config",
        default=None,
        metavar="PATH",
        help="path to YAML config file",
    )
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="print events as JSON instead of pushing to OpenContext daemon",
    )
    parser.add_argument(
        "--debug",
        action="store_true",
        help="enable debug-level logging",
    )
    parser.add_argument(
        "--no-clicks",
        action="store_true",
        help="disable click monitoring",
    )
    parser.add_argument(
        "--no-keys",
        action="store_true",
        help="disable keyboard / text-input monitoring",
    )
    parser.add_argument(
        "--no-processes",
        action="store_true",
        help="disable process-launch monitoring",
    )
    return parser.parse_args()


def _drain_queue(q: Queue) -> list[dict]:
    events = []
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
        config.daemon_url = args.url

    client = OpenContextClient(config.daemon_url)

    if not args.dry_run:
        if client.is_alive():
            logger.info("connected to OpenContext daemon at %s", config.daemon_url)
        else:
            logger.warning(
                "OpenContext daemon not reachable at %s — events will be dropped until it starts",
                config.daemon_url,
            )

    event_queue: Queue = Queue()

    # ── Start monitors ───────────────────────────────────────────────────────
    started = []

    try:
        wm = WindowMonitor(event_queue, config)
        wm.start()
        started.append(wm)
        logger.info("window monitor  started  (os.window_focus)")
    except Exception as e:
        logger.error("window monitor failed to start: %s", e)

    if not args.no_clicks:
        try:
            cm = ClickMonitor(event_queue, config)
            cm.start()
            started.append(cm)
            logger.info("click monitor   started  (os.ui_click)")
        except Exception as e:
            logger.error("click monitor failed to start: %s", e)

    if not args.no_keys:
        try:
            km = KeyboardMonitor(event_queue, config)
            km.start()
            started.append(km)
            if config.collect_raw_keys:
                logger.info("keyboard monitor started  (os.text_input + os.key_press L3)")
            else:
                logger.info("keyboard monitor started  (os.text_input)")
        except Exception as e:
            logger.error("keyboard monitor failed to start: %s", e)

    if not args.no_processes:
        try:
            pm = ProcessMonitor(event_queue, config)
            pm.start()
            started.append(pm)
            logger.info("process monitor started  (os.app_launch)")
        except Exception as e:
            logger.error("process monitor failed to start: %s", e)

    if config.collect_clipboard:
        try:
            cbm = ClipboardMonitor(event_queue, config)
            cbm.start()
            started.append(cbm)
            logger.info("clipboard monitor started (os.clipboard_copy L3)")
        except Exception as e:
            logger.error("clipboard monitor failed to start: %s", e)

    if config.collect_screenshots:
        try:
            sm = ScreenshotMonitor(event_queue, config)
            sm.start()
            started.append(sm)
            logger.info("screenshot monitor started (os.screenshot L3)")
        except Exception as e:
            logger.error("screenshot monitor failed to start: %s", e)

    if not started:
        logger.error("no monitors could be started — exiting")
        sys.exit(1)

    logger.info(
        "collector running — flushing every %.1fs  (Ctrl+C to stop)",
        config.flush_interval,
    )

    # ── Graceful shutdown ────────────────────────────────────────────────────
    def _shutdown(sig, frame) -> None:
        logger.info("shutting down…")
        sys.exit(0)

    signal.signal(signal.SIGINT, _shutdown)
    signal.signal(signal.SIGTERM, _shutdown)

    # ── Main flush loop ──────────────────────────────────────────────────────
    total_pushed = 0
    while True:
        time.sleep(config.flush_interval)

        events = _drain_queue(event_queue)
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
