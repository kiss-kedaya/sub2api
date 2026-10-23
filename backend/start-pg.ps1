# Portable Postgres launcher with stale-lock recovery.
# The data dir keeps postmaster.pid when the previous run was killed, and
# pg_ctl then refuses to start ("another server might be running"), which
# looks exactly like a crash. Clear the lock when no process owns it.
$ErrorActionPreference = 'Stop'
$dl = if ($env:SUB2API_DEV_STATE_DIR) { $env:SUB2API_DEV_STATE_DIR } else {
    Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'sub2api-dev'
}
$data = "$dl\pgdata-sub2api"
$bin = if ($env:SUB2API_DEV_POSTGRES_BIN) { $env:SUB2API_DEV_POSTGRES_BIN } else {
    Join-Path $dl 'pgsql-portable\bin'
}
$log  = "$data\server.log"

# Probe with an actual connection: Get-NetTCPConnection can report nothing
# during the first moments of startup, which made this script try to launch a
# second postmaster and fail on the port.
function Test-PgReady {
    try {
        $c = New-Object System.Net.Sockets.TcpClient
        $c.Connect('127.0.0.1', 5433)
        $c.Close()
        return $true
    } catch {
        return $false
    }
}

if (Test-PgReady) {
    Write-Host 'postgres already accepting connections on 5433'
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
    if (Test-PgReady) {
        Write-Host 'postgres ready on 5433'
        exit 0
    }
}
Write-Host 'postgres failed to start; see' $log
exit 1
