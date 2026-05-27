@echo off
:: run_local.bat — launcher for WSL2+Windows local dev setup.
::
:: Instead of pushing directly to contextd (which lives inside WSL2 and
:: is not reachable from Windows), this runs the collector in --dry-run mode
:: and appends JSON events to a shared file that the WSL2 bridge can read.
::
:: Usage: double-click or call from Task Scheduler.
:: Pair with: bridge.sh running inside WSL2.

setlocal

set EVENTS_FILE=C:\oc-collector\events.jsonl
set LOG_FILE=C:\oc-collector\collector.log
set PYTHON=C:\Users\Administrator\AppData\Local\Programs\Python\Python312\python.exe
set COLLECTOR=C:\oc-collector\collector.py

echo [%DATE% %TIME%] Starting Windows UI Collector (WSL2 bridge mode) >> "%LOG_FILE%"

"%PYTHON%" -u "%COLLECTOR%" --dry-run >> "%EVENTS_FILE%" 2>> "%LOG_FILE%"

echo [%DATE% %TIME%] Collector exited >> "%LOG_FILE%"
