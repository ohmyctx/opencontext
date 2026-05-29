#!/usr/bin/env python3
"""macOS UI Activity Collector for OpenContext.

Monitors user activity on macOS and pushes structured events to the OpenContext daemon:
  - os.window_focus   — which app/window is in focus (+ URL for browsers)
  - os.browser_nav    — URL changes within a browser tab
  - os.ui_click       — UI element clicks (requires Accessibility permission)
  - os.text_input     — text submitted in input fields (L2)
  - os.app_launch     — new applications launched
  - os.clipboard_copy — clipboard content changes (L3)
  - os.screenshot     — local screenshot path (L3, opt-in)
  - os.key_press      — individual keystrokes (L3, opt-in)

Permissions required:
  System Settings → Privacy & Security → Accessibility → allow this terminal/app
  (Screen Recording not needed unless you enable future OCR features)

Usage:
  python collector.py               # run in foreground, push to OpenContext daemon
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
                   help="OpenContext daemon base URL (default: http://localhost:6060)")
    p.add_argument("--config", default=None, metavar="PATH",
                   help="path to YAML config file")
    p.add_argument("--dry-run", action="store_true",
                   help="print events as JSON instead of pushing to OpenContext daemon")
    p.add_argument("--debug", action="store_true",
                   help="enable debug-level logging")
    p.add_argument("--no-clicks", action="store_true", help="disable click monitoring")
    p.add_argument("--no-keys", action="store_true",   help="disable keyboard monitoring")
    p.add_argument("--no-processes", action="store_true", help="disable process monitoring")
    p.add_argument("--check-permissions", "--check-permission", action="store_true",
                   help="print macOS permission status and exit")
    p.add_argument("--prompt-permissions", action="store_true",
                   help="ask macOS to show the Accessibility permission prompt")
    p.add_argument("--run", action="store_true",
                   help="run the collector daemon (used by run.sh)")
    return p.parse_args()


def _sleep_interval(seconds: float) -> None:
    """Sleep while pumping the Cocoa run loop (required inside a .app bundle)."""
    if sys.platform != "darwin":
        time.sleep(seconds)
        return
    try:
        from Foundation import NSDate, NSDefaultRunLoopMode, NSRunLoop  # type: ignore

        run_loop = NSRunLoop.currentRunLoop()
        deadline = time.time() + seconds
        while time.time() < deadline:
            remaining = max(0.05, deadline - time.time())
            run_loop.runMode_beforeDate_(
                NSDefaultRunLoopMode,
                NSDate.dateWithTimeIntervalSinceNow_(min(0.25, remaining)),
            )
    except Exception:
        time.sleep(seconds)


def print_permission_status(prompt: bool = False) -> int:
    import os

    from monitors.helpers import (
        check_accessibility_functional,
        check_accessibility_permission,
        get_permission_diagnostics,
        get_session_context,
        prompt_accessibility_permission,
    )

    accessibility = prompt_accessibility_permission() if prompt else check_accessibility_permission()
    functional = check_accessibility_functional()
    session = get_session_context()
    diag = get_permission_diagnostics()

    if session == "ssh":
        ok = None
        verified = False
        check_source = "ssh_not_verifiable"
    else:
        ok = functional or accessibility
        verified = True
        check_source = "local"

    status = {
        "accessibility": accessibility,
        "accessibility_trusted": diag.get("accessibility_trusted", accessibility),
        "accessibility_functional": functional,
        "ok": ok,
        "verified": verified if session == "ssh" else bool(ok),
        "session": session,
        "check_source": check_source,
        "required_for": [
            "window titles",
            "browser URLs",
            "UI click element names",
            "submitted text input",
            "keyboard listener",
        ],
        "settings_path": "System Settings -> Privacy & Security -> Accessibility",
        "grant_helper": "bash grant-accessibility.sh",
        "suggestion": (
            "Add ~/Applications/OpenContext Collector.app in System Settings → "
            "Privacy & Security → Accessibility. Run: bash grant-accessibility.sh"
        ),
        **{k: v for k, v in diag.items() if k not in {
            "accessibility", "accessibility_trusted", "accessibility_functional", "launched_via_ssh",
        }},
    }
    if session == "ssh":
        status["verify_on_mac"] = (
            f"cd {os.path.dirname(os.path.abspath(__file__))} && "
            "bash run.sh --check-permissions"
        )
        status["suggestion"] = (
            "Cannot verify Accessibility over SSH. Run verify_on_mac on the Mac. "
            "If opencontext-mac-collector is enabled in Accessibility, the collector works."
        )

    print(json.dumps(status, ensure_ascii=False, indent=2), flush=True)
    if session == "ssh":
        return 0
    return 0 if ok else 4


def _drain(q: Queue) -> list[dict]:
    events: list[dict] = []
    try:
        while True:
            events.append(q.get_nowait())
    except Empty:
        pass
    return events


def main() -> None:
    from client import OpenContextClient
    from config import Config
    from monitors.click_monitor import ClickMonitor
    from monitors.clipboard_monitor import ClipboardMonitor
    from monitors.keyboard_monitor import KeyboardMonitor
    from monitors.process_monitor import ProcessMonitor
    from monitors.screenshot_monitor import ScreenshotMonitor
    from monitors.window_monitor import WindowMonitor

    args = parse_args()
    if args.debug:
        logging.getLogger().setLevel(logging.DEBUG)

    if args.check_permissions or args.prompt_permissions:
        sys.exit(print_permission_status(prompt=args.prompt_permissions))

    if getattr(sys, "frozen", False) and not args.run:
        print(
            "This binary is the background collector. Run:\n"
            "  bash grant-accessibility.sh   # setup UI\n"
            "  bash run.sh --run             # start collector\n",
            file=sys.stderr,
        )
        sys.exit(2)

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

    # ── Screenshots ──────────────────────────────────────────────────
    if config.collect_screenshots:
        try:
            sm = ScreenshotMonitor(event_queue, config)
            sm.start()
            started.append(sm)
            logger.info("screenshot monitor started (os.screenshot L3)")
        except Exception as e:
            logger.error("screenshot monitor failed to start: %s", e)

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
        _sleep_interval(config.flush_interval)
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
