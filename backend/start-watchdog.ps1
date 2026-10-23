# Local watchdog test run: probes the gateway health endpoint and one chain
# RPC. Mirrors the production systemd invocation.
$ErrorActionPreference = 'Stop'
$root = $PSScriptRoot
$dl = if ($env:SUB2API_DEV_STATE_DIR) { $env:SUB2API_DEV_STATE_DIR } else {
    Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'sub2api-dev'
}
New-Item -ItemType Directory -Path $dl -Force | Out-Null

Start-Process -FilePath "$root\bin\watchdog.exe" `
    -ArgumentList '-health-url', 'http://127.0.0.1:8090/health', `
                  '-state-dir', "$dl\wd-state", `
                  '-interval', '10s', `
                  '-rpc', 'https://bsc-rpc.publicnode.com' `
    -WindowStyle Hidden `
    -RedirectStandardOutput "$dl\wd.out.log" `
    -RedirectStandardError "$dl\wd.err.log"

Write-Host ('watchdog started (check {0})' -f (Join-Path $dl 'wd.err.log'))
