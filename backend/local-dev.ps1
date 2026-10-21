# Starts the local crypto-deposit test stack: portable Postgres and the
# sub2api server with its built-in wallet. Idempotent: safe to re-run.
$ErrorActionPreference = 'Stop'
$root = 'F:\GO\sub2-wt-crypto-deposit\backend'
$dl = 'E:\Users\34438\Downloads'
$pgbin = "$dl\pgsql-portable\bin"

# 1. Postgres
$pgUp = (Test-NetConnection 127.0.0.1 -Port 5433 -InformationLevel Quiet -WarningAction SilentlyContinue)
if (-not $pgUp) {
    & "$pgbin\pg_ctl.exe" -D "$dl\pgdata-sub2api" -o '-p 5433' -l "$dl\pgdata-sub2api\server.log" start | Out-Null
    Start-Sleep -Seconds 3
    Write-Host 'postgres started'
} else {
    Write-Host 'postgres already running'
}

# 2. sub2api (wallet starts locked; unlock in the crypto admin page with
# a fresh 2FA code and the vault password, separate from the login password).
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
Write-Host 'Admin:    admin@local.test / LocalAdmin123!'
