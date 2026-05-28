# start_collector.ps1 — launch Windows UI collector as a hidden background process.
param(
    [string]$Python     = "C:\Users\Administrator\AppData\Local\Programs\Python\Python312\python.exe",
    [string]$CollectorDir = "C:\oc-collector",
    [string]$Url        = "http://localhost:6060",
    [string]$LogFile    = "C:\oc-collector\collector.log",
    [string]$OutFile    = "C:\oc-collector\collector.out.log"
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
    -ArgumentList "-u", "$CollectorDir\collector.py", "--url", $Url `
    -WorkingDirectory $CollectorDir `
    -RedirectStandardOutput $OutFile `
    -RedirectStandardError  $LogFile `
    -PassThru `
    -WindowStyle Hidden

Write-Host "[collector] started  PID: $($proc.Id)  URL: $Url"
