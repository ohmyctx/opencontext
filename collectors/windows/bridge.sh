#!/usr/bin/env bash
# bridge.sh — forward Windows UI collector events to local WSL2 contextd.
#
# LOCAL SETUP HELPER — not part of the collector itself.
# Bridges the gap: Windows collector writes JSON to a shared file (via /mnt/c/),
# this script tails the file and batch-POSTs events to the WSL2 contextd daemon.
#
# Architecture:
#   Windows: python -u collector.py --dry-run >> C:\oc-collector\events.jsonl
#   WSL2:    bridge.sh  tail -f /mnt/c/oc-collector/events.jsonl → contextd
#
# Usage:
#   ./bridge.sh [--events-file PATH] [--url URL]

set -euo pipefail

EVENTS_FILE="/mnt/c/oc-collector/events.jsonl"
CONTEXTD_URL="${OPENCONTEXT_URL:-http://localhost:6060}"
BATCH_SIZE=20
FLUSH_TIMEOUT_SECS=5
PID_FILE="/tmp/oc-bridge.pid"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --events-file) EVENTS_FILE="$2"; shift 2 ;;
    --url)         CONTEXTD_URL="$2"; shift 2 ;;
    *) shift ;;
  esac
done

# ── singleton guard ───────────────────────────────────────────────────────────
if [[ -f "$PID_FILE" ]]; then
  old_pid=$(cat "$PID_FILE")
  if kill -0 "$old_pid" 2>/dev/null; then
    echo "[bridge] already running (PID $old_pid). Use 'make stop' first." >&2
    exit 1
  fi
  rm -f "$PID_FILE"
fi
echo $$ > "$PID_FILE"
trap 'rm -f "$PID_FILE"; log "stopped"' EXIT

# ── helpers ───────────────────────────────────────────────────────────────────
log() { echo "[bridge] $(date '+%H:%M:%S') $*" >&2; }

flush_batch() {
  local -n _lines=$1
  [[ ${#_lines[@]} -eq 0 ]] && return
  local n=${#_lines[@]}
  local payload
  if command -v jq &>/dev/null; then
    payload=$(printf '%s\n' "${_lines[@]}" | jq -sc '{events: .}' 2>/dev/null)
    curl -sf -X POST "$CONTEXTD_URL/api/v1/events/batch" \
      -H "Content-Type: application/json" \
      -d "$payload" &>/dev/null && log "flushed $n events" || log "flush failed (contextd down?)"
  else
    # jq not available — fall back to one-by-one
    local ok=0
    for ln in "${_lines[@]}"; do
      curl -sf -X POST "$CONTEXTD_URL/api/v1/events" \
        -H "Content-Type: application/json" \
        -d "$ln" &>/dev/null && (( ok++ )) || true
    done
    log "flushed $ok/$n events (no jq, one-by-one)"
  fi
  _lines=()
}

# ── wait for events file ──────────────────────────────────────────────────────
until [[ -f "$EVENTS_FILE" ]]; do
  log "waiting for events file: $EVENTS_FILE"
  sleep 3
done

curl -sf "$CONTEXTD_URL/api/v1/health" &>/dev/null \
  && log "contextd OK at $CONTEXTD_URL" \
  || log "WARNING: contextd not reachable — will retry on each flush"

log "watching $EVENTS_FILE → $CONTEXTD_URL (poll every ${FLUSH_TIMEOUT_SECS}s, batch=$BATCH_SIZE)"
log "note: using poll mode (inotify not supported on /mnt/c/ DrvFs)"

# ── main loop (poll-based, avoids inotify limitation on DrvFs) ────────────────
# Track how many lines we have already processed
processed_lines=$(wc -l < "$EVENTS_FILE" 2>/dev/null || echo 0)
log "skipping $processed_lines existing lines in file"

while true; do
  sleep "$FLUSH_TIMEOUT_SECS"

  current_lines=$(wc -l < "$EVENTS_FILE" 2>/dev/null || echo 0)

  # Handle file truncation (e.g. collector restarted with fresh file)
  if (( current_lines < processed_lines )); then
    log "file truncated, resetting position to 0"
    processed_lines=0
  fi

  new_count=$(( current_lines - processed_lines ))
  if (( new_count <= 0 )); then
    continue
  fi

  # Read only the new lines
  mapfile -t new_lines < <(tail -n "+$(( processed_lines + 1 ))" "$EVENTS_FILE" | head -n "$new_count")
  processed_lines=$(( processed_lines + ${#new_lines[@]} ))

  buffer=("${new_lines[@]}")
  flush_batch buffer
done
