# Portable Postgres launcher with stale-lock recovery.
# The data dir keeps postmaster.pid when the previous run was killed, and
# pg_ctl then refuses to start ("another server might be running"), which
# looks exactly like a crash. Clear the lock when no process owns it.
$ErrorActionPreference = 'Stop'
$dl  = 'E:\Users\34438\Downloads'
$data = "$dl\pgdata-sub2api"
$bin  = "$dl\pgsql-portable\bin"
$log  = "$data\server.log"

$running = Get-NetTCPConnection -LocalPort 5433 -State Listen -ErrorAction SilentlyContinue
if ($running) {
    Write-Host 'postgres already listening on 5433'
    exit 0
}

$pidFile = "$data\postmaster.pid"
if (Test-Path $pidFile) {
    $stalePid = (Get-Content $pidFile -TotalCount 1).Trim()
    $alive = $null -ne (Get-Process -Id ([int]$stalePid) -ErrorAction SilentlyContinue)
    if (-not $alive) {
        Remove-Item $pidFile -Force
        Write-Host "cleared stale postmaster.pid (dead pid $stalePid)"
    } else {
        Write-Host "postmaster.pid owner $stalePid is alive; waiting"
        Start-Sleep -Seconds 5
    }
}

Start-Process -FilePath "$bin\pg_ctl.exe" `
    -ArgumentList '-D', $data, '-o', '-p 5433', '-l', $log, 'start' `
    -WindowStyle Hidden

for ($i = 0; $i -lt 20; $i++) {
    Start-Sleep -Seconds 1
    if (Get-NetTCPConnection -LocalPort 5433 -State Listen -ErrorAction SilentlyContinue) {
        Write-Host 'postgres ready on 5433'
        exit 0
    }
}
Write-Host 'postgres failed to start; see' $log
exit 1
