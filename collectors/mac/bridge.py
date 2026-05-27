#!/usr/bin/env python3
"""
bridge.py — Mac collector bridge
Tails /tmp/oc-mac-events.jsonl and POSTs each JSON event to contextd.
contextd must be reachable at CONTEXTD_URL (default: http://localhost:6060).

When used with the WSL2 setup, contextd is tunneled via SSH reverse-forward:
  WSL2> ssh -p 2222 -R 6060:127.0.0.1:6060 chicken@localhost -N &
  Mac>  python3 bridge.py
"""

import json
import os
import sys
import time
import urllib.request
import urllib.error

EVENTS_FILE = os.environ.get("EVENTS_FILE", "/tmp/oc-mac-events.jsonl")
CONTEXTD_URL = os.environ.get("CONTEXTD_URL", "http://localhost:6060")
POLL_SECS = float(os.environ.get("POLL_SECS", "3"))
BATCH_SIZE = int(os.environ.get("BATCH_SIZE", "20"))

TAG = "[mac-bridge]"

def log(msg):
    print(f"{TAG} {time.strftime('%H:%M:%S')} {msg}", flush=True)

def post_event(event: dict) -> bool:
    data = json.dumps(event, ensure_ascii=False).encode()
    req = urllib.request.Request(
        f"{CONTEXTD_URL}/api/v1/events",
        data=data,
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(req, timeout=5) as resp:
            return resp.status < 300
    except urllib.error.URLError:
        return False

def post_batch(events: list) -> int:
    if not events:
        return 0
    data = json.dumps({"events": events}, ensure_ascii=False).encode()
    req = urllib.request.Request(
        f"{CONTEXTD_URL}/api/v1/events/batch",
        data=data,
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(req, timeout=5) as resp:
            if resp.status < 300:
                return len(events)
    except urllib.error.URLError:
        pass
    # Fallback: one by one
    ok = sum(1 for e in events if post_event(e))
    return ok

def health_check() -> bool:
    try:
        with urllib.request.urlopen(f"{CONTEXTD_URL}/api/v1/health", timeout=3) as r:
            return r.status < 300
    except Exception:
        return False

def main():
    log(f"bridge starting — {EVENTS_FILE} → {CONTEXTD_URL}")

    # Wait for file
    while not os.path.exists(EVENTS_FILE):
        log(f"waiting for {EVENTS_FILE} …")
        time.sleep(2)

    # Health check
    if health_check():
        log(f"contextd OK at {CONTEXTD_URL}")
    else:
        log(f"WARNING: contextd not reachable — events will be retried each poll")

    line_num = 0
    total_pushed = 0

    log(f"watching events (poll every {POLL_SECS}s) …")
    while True:
        time.sleep(POLL_SECS)
        try:
            with open(EVENTS_FILE, "r", encoding="utf-8") as f:
                lines = f.readlines()
        except OSError:
            continue

        new_lines = lines[line_num:]
        if not new_lines:
            continue

        batch = []
        for raw in new_lines:
            raw = raw.strip()
            line_num += 1
            if not raw or not raw.startswith("{"):
                continue
            try:
                event = json.loads(raw)
                batch.append(event)
            except json.JSONDecodeError:
                continue

            if len(batch) >= BATCH_SIZE:
                n = post_batch(batch)
                total_pushed += n
                log(f"flushed {n}/{len(batch)} events  (total: {total_pushed})")
                batch = []

        if batch:
            n = post_batch(batch)
            total_pushed += n
            log(f"flushed {n}/{len(batch)} events  (total: {total_pushed})")

if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:
        print(f"\n{TAG} stopped")
        sys.exit(0)
