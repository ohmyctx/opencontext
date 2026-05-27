# Makefile — Windows UI Collector + WSL2 Bridge
# Run from WSL2 inside this directory.
#
# Usage:
#   make start     — start Windows collector + WSL2 bridge (clean slate)
#   make stop      — stop both
#   make status    — show running state
#   make logs      — tail live logs
#   make restart   — stop then start

# ── config (override via env or make start PYTHON=...) ───────────────────────
PYTHON      ?= C:/Users/Administrator/AppData/Local/Programs/Python/Python312/python.exe
EVENTS_FILE ?= /mnt/c/oc-collector/events.jsonl
CONTEXTD_URL?= http://localhost:6060
BRIDGE_LOG  := /tmp/oc-bridge.log
COLLECTOR_LOG:= /mnt/c/oc-collector/collector.log
COLLECTOR_DIR:= C:/oc-collector
POWERSHELL  := /mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe
PID_FILE    := /tmp/oc-bridge.pid
BRIDGE      := $(CURDIR)/bridge.sh

.PHONY: start stop restart status logs \
        start-collector stop-collector \
        start-bridge stop-bridge

# ── start everything ──────────────────────────────────────────────────────────
start: stop-bridge _truncate-events start-collector start-bridge
	@echo ""
	@echo "  Windows collector + WSL2 bridge started."
	@echo "  Run 'make logs' to watch live output."
	@echo "  Run 'make status' to check health."

restart: stop start

# ── stop everything ───────────────────────────────────────────────────────────
stop: stop-collector stop-bridge
	@echo "All stopped."

# ── Windows collector ─────────────────────────────────────────────────────────
start-collector: _sync-to-windows
	@echo "[collector] starting on Windows..."
	@timeout 12 $(POWERSHELL) -NonInteractive -ExecutionPolicy Bypass \
	    -File "C:\\oc-collector\\start_collector.ps1" \
	    -Python "$(subst /,\,$(PYTHON))" \
	    2>&1 \
	  | grep -E "\[collector\]|Error|error" || true
	@echo "[collector] launch done"

stop-collector:
	@timeout 8 $(POWERSHELL) -NonInteractive -ExecutionPolicy Bypass \
	    -File "C:\\oc-collector\\stop_collector.ps1" 2>&1 \
	  | grep -E "\[collector\]" || echo "[collector] not running"

# ── WSL2 bridge ───────────────────────────────────────────────────────────────
start-bridge:
	@echo "[bridge] starting..."
	@if [ -f "$(PID_FILE)" ] && kill -0 "$$(cat $(PID_FILE))" 2>/dev/null; then \
	  echo "[bridge] already running (PID $$(cat $(PID_FILE))) — skipping"; \
	else \
	  nohup bash $(BRIDGE) \
	    --events-file $(EVENTS_FILE) \
	    --url $(CONTEXTD_URL) \
	    >> $(BRIDGE_LOG) 2>&1 & \
	  sleep 1; \
	  echo "[bridge] started (PID $$!), log → $(BRIDGE_LOG)"; \
	fi

stop-bridge:
	@pid=""; \
	[ -f "$(PID_FILE)" ] && pid=$$(cat $(PID_FILE)); \
	if [ -n "$$pid" ] && kill -0 "$$pid" 2>/dev/null; then \
	  kill "$$pid" 2>/dev/null; echo "[bridge] stopped (PID $$pid)"; \
	else \
	  echo "[bridge] not running"; \
	fi; \
	rm -f $(PID_FILE)

# ── status ────────────────────────────────────────────────────────────────────
status:
	@echo "┌─ Windows Collector ──────────────────────────────────────"
	@timeout 8 $(POWERSHELL) -NonInteractive -ExecutionPolicy Bypass \
	    -File "C:\\oc-collector\\check_collector.ps1" 2>&1 \
	  | grep "|" || echo "| (status check timed out)"
	@echo "├─ WSL2 Bridge ────────────────────────────────────────────"
	@if [ -f "$(PID_FILE)" ] && kill -0 "$$(cat $(PID_FILE))" 2>/dev/null; then \
	  echo "│ running  PID: $$(cat $(PID_FILE))"; \
	else \
	  echo "│ NOT running"; \
	fi
	@echo "├─ Events file ────────────────────────────────────────────"
	@if [ -f "$(EVENTS_FILE)" ]; then \
	  echo "│ $(EVENTS_FILE)"; \
	  echo "│ $$(wc -l < $(EVENTS_FILE)) lines  $$(du -sh $(EVENTS_FILE) | cut -f1)"; \
	else \
	  echo "│ not found"; \
	fi
	@echo "├─ contextd ───────────────────────────────────────────────"
	@curl -sf $(CONTEXTD_URL)/api/v1/health 2>/dev/null \
	  | python3 -c "import sys,json; h=json.load(sys.stdin); print('│ ok  events:', h.get('events_stored','-'))" \
	  || echo "│ NOT reachable"
	@echo "└──────────────────────────────────────────────────────────"

# ── logs ──────────────────────────────────────────────────────────────────────
logs:
	@echo "[Ctrl+C to stop]"
	@tail -f $(BRIDGE_LOG) $(COLLECTOR_LOG) 2>/dev/null

# ── internal ──────────────────────────────────────────────────────────────────
_truncate-events:
	@truncate -s 0 $(EVENTS_FILE) 2>/dev/null || true
	@echo "[events] $(EVENTS_FILE) cleared"

# Sync latest Python source files + helper scripts to C:\oc-collector
_sync-to-windows:
	@echo "[sync] copying latest code to /mnt/c/oc-collector/ ..."
	@cp -f $(CURDIR)/*.py $(CURDIR)/*.ps1 /mnt/c/oc-collector/ 2>/dev/null || true
	@cp -rf $(CURDIR)/monitors /mnt/c/oc-collector/
	@echo "[sync] done"
