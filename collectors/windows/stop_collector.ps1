# stop_collector.ps1 — stop the Windows UI collector (python process)
$p = Get-Process python -ErrorAction SilentlyContinue
if ($p) {
    $p | Stop-Process -Force
    Write-Host "[collector] stopped  (PID $($p.Id))"
} else {
    Write-Host "[collector] not running"
}
