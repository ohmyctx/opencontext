# check_collector.ps1 — print Windows collector process status (one line)
$p = Get-Process python -ErrorAction SilentlyContinue
if ($p) {
    $uptime = [datetime]::Now - $p.StartTime
    Write-Host ("| running  PID: {0}  CPU: {1:F1}s  uptime: {2}" -f $p.Id, $p.CPU, $uptime.ToString("hh\:mm\:ss"))
} else {
    Write-Host "| NOT running"
}
