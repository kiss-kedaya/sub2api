#!/usr/bin/env bash
# Verify that a running sub2api instance can serve every asset its entry bundle
# references. A release whose embedded dist is missing chunks returns 200 for
# "/" and for the entry script, then 404s on the first lazy route, which is how
# 2.3.93 took the whole site down.
#
# Usage: scripts/verify-embedded-assets.sh http://10.254.0.2:18532 [--timeout 10]
set -euo pipefail

usage() {
  echo "usage: $0 <base-url> [--timeout <seconds>]" >&2
  exit 2
}

[ $# -ge 1 ] || usage
base="${1%/}"
timeout_s=10
shift
while [ $# -gt 0 ]; do
  case "$1" in
    --timeout)
      timeout_s="$2"
      shift 2
      ;;
    *) usage ;;
  esac
done

fail=0
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

status() {
  curl -s -o /dev/null -w '%{http_code}' -m "$timeout_s" "$1" || echo 000
}

home_code="$(status "$base/")"
if [ "$home_code" != "200" ]; then
  echo "FAIL  home  $home_code  $base/"
  exit 1
fi
echo "ok    home  200"

curl -s -m "$timeout_s" "$base/" >"$tmp/index.html"

# The entry bundle carries a dependency map with every lazily imported chunk.
entries="$(grep -oE '(^|/)?assets/[A-Za-z0-9._-]+\.js' "$tmp/index.html" | sed 's|^|/|; s|^//|/|' | sort -u || true)"
if [ -z "$entries" ]; then
  echo "FAIL  no entry bundle referenced by index.html"
  exit 1
fi

assets=""
for entry in $entries; do
  code="$(status "$base$entry")"
  if [ "$code" != "200" ]; then
    echo "FAIL  entry $code  $base$entry"
    fail=1
    continue
  fi
  echo "ok    entry 200  $entry"
  curl -s -m "$timeout_s" "$base$entry" >"$tmp/entry.js"
  # Vite emits chunk paths without a leading slash in __vite__mapDeps. Only
  # hashed build outputs are embedded; third-party strings such as
  # elements.bundle.min.js come from vendor SDK snippets and must be skipped.
  assets="$assets $(grep -oE '(^|/)?assets/[A-Za-z0-9._-]+-[A-Za-z0-9_-]{8}\.js' "$tmp/entry.js" | sed 's|^|/|; s|^//|/|' | sort -u || true)"
done

# Chunk names also appear in the CSS/preload hints; check those files too.
assets="$assets $(grep -oE '(^|/)?assets/[A-Za-z0-9._-]+\.(js|css)' "$tmp/index.html" | sed 's|^|/|; s|^//|/|' | sort -u || true)"

missing=0
checked=0
for asset in $(echo "$assets" | tr ' ' '\n' | sort -u | grep -v '^$'); do
  case " $entries " in
    *" $asset "*) continue ;;
  esac
  checked=$((checked + 1))
  code="$(status "$base$asset")"
  if [ "$code" != "200" ]; then
    echo "FAIL  chunk $code  $asset"
    missing=$((missing + 1))
    fail=1
  fi
done

echo "checked $checked chunk reference(s), missing $missing"
if [ "$fail" != "0" ]; then
  echo "embedded assets incomplete: do NOT switch traffic to this instance" >&2
  exit 1
fi
echo "embedded assets complete"
