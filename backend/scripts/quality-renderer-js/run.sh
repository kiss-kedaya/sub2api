#!/usr/bin/env bash
# Four-frame capture for JS-driven artwork, inside a network-isolated container.
#
# Usage: run.sh < artwork.html  > frames.json
#
# Isolation boundary is the container, not the browser sandbox:
#   --network none-alike internal bridge : no outbound route
#   --cap-drop ALL                       : only SYS_ADMIN for chromium startup
#   --memory/--cpus/--pids-limit         : bounded blast radius
#   --rm                                 : one shot, nothing persists
# Artwork JS runs here and nowhere else. Do not relax these flags without
# replacing them with an equivalent boundary.
set -euo pipefail

IMAGE="${QR_IMAGE:-mcr.microsoft.com/playwright:v1.63.0-noble}"
CHROME="${QR_CHROME:-/ms-playwright/chromium-1243/chrome-linux64/chrome}"
RENDERER_DIR="${QR_DIR:-/opt/sub2api/qr-js}"
NETWORK="${QR_NETWORK:-qr-isolated}"
TIMEOUT="${QR_TIMEOUT:-240}"

docker network inspect "$NETWORK" >/dev/null 2>&1 || \
  docker network create --driver bridge --internal "$NETWORK" >/dev/null

exec timeout "$TIMEOUT" docker run --rm -i \
  --network "$NETWORK" \
  --cap-drop ALL --cap-add SYS_ADMIN \
  --security-opt no-new-privileges \
  --security-opt seccomp=unconfined \
  --memory 1g --cpus 1 --pids-limit 512 \
  -e QUALITY_CHROMIUM_EXECUTABLE_PATH="$CHROME" \
  -v "$RENDERER_DIR":/qr:ro \
  --entrypoint node "$IMAGE" /qr/capture.mjs
