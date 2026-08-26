#!/usr/bin/env sh
set -eu

PROFILE_SECONDS="${1:-45}"
OUTPUT_ROOT="${2:-./data/diagnostics}"
BASE_URL="${PPROF_URL:-http://127.0.0.1:6060}"

case "$BASE_URL" in
  http://127.0.0.1:*|http://localhost:*|http://\[::1\]:*) ;;
  *)
    echo "PPROF_URL must point to a loopback HTTP address" >&2
    exit 2
    ;;
esac

case "$PROFILE_SECONDS" in
  ''|*[!0-9]*)
    echo "profile duration must be a positive integer" >&2
    exit 2
    ;;
esac
if [ "$PROFILE_SECONDS" -le 0 ] || [ "$PROFILE_SECONDS" -gt 300 ]; then
  echo "profile duration must be between 1 and 300 seconds" >&2
  exit 2
fi

CAPTURE_ID="pprof-$(date -u +%Y%m%dT%H%M%SZ)"
OUTPUT_DIR="$OUTPUT_ROOT/$CAPTURE_ID"
mkdir -p "$OUTPUT_DIR"

fetch() {
  path="$1"
  output="$2"
  curl --fail --silent --show-error --max-time "$((PROFILE_SECONDS + 15))" \
    "$BASE_URL$path" --output "$OUTPUT_DIR/$output"
}

fetch "/debug/pprof/profile?seconds=$PROFILE_SECONDS" cpu.pprof & cpu_pid=$!
fetch "/debug/pprof/mutex?seconds=$PROFILE_SECONDS" mutex.pprof & mutex_pid=$!
fetch "/debug/pprof/block?seconds=$PROFILE_SECONDS" block.pprof & block_pid=$!

status=0
wait "$cpu_pid" || status=1
wait "$mutex_pid" || status=1
wait "$block_pid" || status=1
if [ "$status" -ne 0 ]; then
  echo "one or more timed profiles failed" >&2
  exit 1
fi

fetch "/debug/pprof/heap?gc=1" heap-gc.pprof
fetch "/debug/pprof/allocs" allocs.pprof
fetch "/debug/pprof/goroutine" goroutine.pprof
fetch "/debug/pprof/goroutine?debug=2" goroutine.txt
fetch "/debug/pprof/trace?seconds=5" trace.out

{
  echo "captured_at_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "profile_seconds=$PROFILE_SECONDS"
  uname -a 2>/dev/null || true
} > "$OUTPUT_DIR/metadata.txt"

(
  cd "$OUTPUT_DIR"
  sha256sum cpu.pprof mutex.pprof block.pprof heap-gc.pprof allocs.pprof \
    goroutine.pprof goroutine.txt trace.out metadata.txt > SHA256SUMS
)

echo "$OUTPUT_DIR"
