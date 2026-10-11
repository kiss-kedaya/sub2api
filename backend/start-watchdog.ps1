# Local watchdog test run: probes the gateway health endpoint and one chain
# RPC. Mirrors the production systemd invocation.
$ErrorActionPreference = 'Stop'
$root = 'F:\GO\sub2-wt-crypto-deposit\backend'
$dl = 'E:\Users\34438\Downloads'

Start-Process -FilePath "$root\bin\watchdog.exe" `
    -ArgumentList '-health-url', 'http://127.0.0.1:8090/health', `
                  '-state-dir', "$dl\wd-state", `
                  '-interval', '10s', `
                  '-rpc', 'https://bsc-rpc.publicnode.com' `
    -WindowStyle Hidden `
    -RedirectStandardOutput "$dl\wd.out.log" `
    -RedirectStandardError "$dl\wd.err.log"

Write-Host 'watchdog started (check E:\Users\34438\Downloads\wd.err.log)'
