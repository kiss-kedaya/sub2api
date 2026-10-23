# Starts the local crypto-deposit test stack: portable Postgres and the
# sub2api server with its built-in wallet. Idempotent: safe to re-run.
$ErrorActionPreference = 'Stop'
$root = $PSScriptRoot
# Override these paths when using an existing portable development installation.
$dl = if ($env:SUB2API_DEV_STATE_DIR) { $env:SUB2API_DEV_STATE_DIR } else {
    Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'sub2api-dev'
}
$pgbin = if ($env:SUB2API_DEV_POSTGRES_BIN) { $env:SUB2API_DEV_POSTGRES_BIN } else {
    Join-Path $dl 'pgsql-portable\bin'
}

# 1. Postgres
$pgUp = (Test-NetConnection 127.0.0.1 -Port 5433 -InformationLevel Quiet -WarningAction SilentlyContinue)
if (-not $pgUp) {
    & "$pgbin\pg_ctl.exe" -D "$dl\pgdata-sub2api" -o '-p 5433' -l "$dl\pgdata-sub2api\server.log" start | Out-Null
    Start-Sleep -Seconds 3
    Write-Host 'postgres started'
} else {
    Write-Host 'postgres already running'
}

# 2. sub2api (the built-in wallet loads from the persistent installation key;
# wallet administration requires a fresh backend-verified 2FA code).
if (-not (Get-Process -Name sub2api-local -ErrorAction SilentlyContinue)) {
    $env:DATA_DIR = $root
    Start-Process -FilePath "$root\bin\sub2api-local.exe" -WorkingDirectory $root `
        -RedirectStandardOutput "$dl\sub2api-run.out.log" `
        -RedirectStandardError "$dl\sub2api-run.err.log" -WindowStyle Hidden
    Start-Sleep -Seconds 10
    Write-Host 'sub2api started'
} else {
    Write-Host 'sub2api already running'
}

Write-Host ''
Write-Host 'Website:  http://127.0.0.1:8090'
Write-Host 'Admin:    use the credentials configured for this local installation'
