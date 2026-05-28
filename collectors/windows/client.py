"""HTTP client for pushing ActivityEvents to the OpenContext daemon."""

from __future__ import annotations

import logging
import platform
import socket
import time
from typing import Optional

import requests

logger = logging.getLogger(__name__)

COLLECTOR_NAME = "opencontext-windows"
COLLECTOR_VERSION = "0.1.0"
PLATFORM = "windows"
HOSTNAME = socket.gethostname()


def make_event(
    source: str,
    event_type: str,
    sensitivity: int,
    labels: dict,
    payload: dict,
    ts: Optional[int] = None,
) -> dict:
    """Build a well-formed ActivityEvent dict ready to POST to the OpenContext daemon."""
    # Strip empty string values — the daemon rejects them
    base_labels = {
        "platform": PLATFORM,
        "os": platform.platform(),
        "host": HOSTNAME,
        "collector": COLLECTOR_NAME,
        "collector_version": COLLECTOR_VERSION,
    }
    base_labels.update(labels)
    clean_labels = {k: v for k, v in base_labels.items() if v and v != ""}
    clean_payload = {k: v for k, v in payload.items() if v != "" and v is not None}
    return {
        "ts": ts if ts is not None else int(time.time() * 1000),
        "source": source,
        "type": event_type,
        "sensitivity": sensitivity,
        "labels": clean_labels,
        "payload": clean_payload,
    }


class OpenContextClient:
    def __init__(self, url: str = "http://localhost:6060"):
        self.url = url.rstrip("/")
        self._session = requests.Session()
        self._session.headers.update({"Content-Type": "application/json"})

    def is_alive(self) -> bool:
        try:
            r = self._session.get(f"{self.url}/api/v1/health", timeout=1)
            return r.ok
        except Exception:
            return False

    def push(self, event: dict) -> Optional[str]:
        try:
            r = self._session.post(
                f"{self.url}/api/v1/events",
                json=event,
                timeout=2,
            )
            r.raise_for_status()
            return r.json().get("id")
        except Exception as e:
            logger.debug("push failed: %s", e)
            return None

    def push_batch(self, events: list[dict]) -> dict:
        if not events:
            return {}
        try:
            r = self._session.post(
                f"{self.url}/api/v1/events/batch",
                json={"events": events},
                timeout=5,
            )
            r.raise_for_status()
            result = r.json()
            logger.debug(
                "pushed %d events (%d rejected)",
                result.get("accepted", 0),
                result.get("rejected", 0),
            )
            return result
        except Exception as e:
            logger.debug("batch push failed: %s", e)
            return {}


ContextdClient = OpenContextClient
