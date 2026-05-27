#!/usr/bin/env bash
# bridge.sh (runs on the Mac)
# Tails /tmp/oc-mac-events.jsonl and POSTs each JSON line to contextd.
# contextd must be reachable at CONTEXTD_URL (default: localhost:6060).
# When used with the WSL2 setup, contextd is tunneled via SSH reverse-forward.
#
# Usage (run on Mac):
#   bash bridge.sh
#
# On the WSL2 side, establish the tunnel first:
#   ssh -p 2222 -R 6060:localhost:6060 chicken@localhost -N &

set -euo pipefail

EVENTS_FILE="${EVENTS_FILE:-/tmp/oc-mac-events.jsonl}"
CONTEXTD_URL="${CONTEXTD_URL:-http://localhost:6060}"
POLL_SECS=5
BATCH_SIZE=20
LOG_TAG="[mac-bridge]"

log() { echo "$LOG_TAG $(date '+%H:%M:%S') $*" >&2; }

flush_batch() {
  local -n _lines=$1
  [[ ${#_lines[@]} -eq 0 ]] && return
  local n=${#_lines[@]}
  local payload
  if command -v jq &>/dev/null; then
    payload=$(printf '%s\n' "${_lines[@]}" | jq -sc '{events: .}' 2>/dev/null)
    curl -sf -X POST "$CONTEXTD_URL/api/v1/events/batch" \
      -H "Content-Type: application/json" \
      -d "$payload" &>/dev/null \
      && log "flushed $n events" \
      || log "flush failed (contextd down?)"
  else
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

# Wait for events file
until [[ -f "$EVENTS_FILE" ]]; do
  log "waiting for $EVENTS_FILE …"
  sleep 2
done

# Wait for contextd
curl -sf "$CONTEXTD_URL/api/v1/health" &>/dev/null \
  && log "contextd OK at $CONTEXTD_URL" \
  || log "WARNING: contextd not reachable at $CONTEXTD_URL — will retry"

log "watching $EVENTS_FILE → $CONTEXTD_URL (poll every ${POLL_SECS}s)"

pos=0
declare -a buffer=()

while true; do
  sleep "$POLL_SECS"

  # Read new lines since last position
  new_lines=$(tail -n "+$((pos + 1))" "$EVENTS_FILE" 2>/dev/null || true)
  if [[ -z "$new_lines" ]]; then
    continue
  fi

  while IFS= read -r line; do
    [[ -z "$line" ]] && continue
    # Only accept JSON event lines (not log lines)
    [[ "$line" == "{"* ]] || continue
    buffer+=("$line")
    pos=$(( pos + 1 ))
    if [[ ${#buffer[@]} -ge $BATCH_SIZE ]]; then
      flush_batch buffer
    fi
  done <<< "$new_lines"

  flush_batch buffer
done
