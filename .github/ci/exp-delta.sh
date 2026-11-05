#!/usr/bin/env bash
# Experiment only. Simulates a pull request that changes a widely imported
# package, inside the throw-away CI workspace. The edit is never committed.
# Mode comes from .github/ci/exp-delta.txt: none | service-api
set -euo pipefail
mode="$(tr -d '[:space:]' < .github/ci/exp-delta.txt)"
case "$mode" in
  none)
    echo "delta: none"
    ;;
  service-api)
    target=backend/internal/service/gateway_service.go
    printf '\n// ExpDeltaMarker%s simulates an exported API change.\nfunc ExpDeltaMarker%s() int { return %s }\n' \
      "${GITHUB_RUN_ID}" "${GITHUB_RUN_ID}" "${GITHUB_RUN_ID}" >> "$target"
    echo "delta: appended an exported function to $target"
    ;;
  *)
    echo "unknown delta mode: $mode" >&2
    exit 1
    ;;
esac
