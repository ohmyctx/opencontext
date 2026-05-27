# start_collector.ps1 — launch Windows UI collector as a hidden background process.
# Called by 'make start-collector' from WSL2.
param(
    [string]$Python     = "C:\Users\Administrator\AppData\Local\Programs\Python\Python312\python.exe",
    [string]$CollectorDir = "C:\oc-collector",
    [string]$EventsFile = "C:\oc-collector\events.jsonl",
    [string]$LogFile    = "C:\oc-collector\collector.log"
)

# Stop any existing python collector
$existing = Get-Process python -ErrorAction SilentlyContinue
if ($existing) {
    $existing | Stop-Process -Force
    Start-Sleep 1
    Write-Host "[collector] stopped old process(es)"
}

$proc = Start-Process `
    -FilePath $Python `
    -ArgumentList "-u", "$CollectorDir\collector.py", "--dry-run" `
    -WorkingDirectory $CollectorDir `
    -RedirectStandardOutput $EventsFile `
    -RedirectStandardError  $LogFile `
    -PassThru `
    -WindowStyle Hidden

Write-Host "[collector] started  PID: $($proc.Id)"
