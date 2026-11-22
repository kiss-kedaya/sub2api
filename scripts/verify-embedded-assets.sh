#!/usr/bin/env bash
# Verify that a running sub2api instance can serve every embedded frontend file.
# An instance whose frontend is incomplete returns 200 for "/" and for the entry
# script, then 404s on the first lazy route (white screen).
#
# Usage: scripts/verify-embedded-assets.sh http://127.0.0.1:8080 [--timeout 10]
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

home="$(curl -s -o /dev/null -w '%{http_code} %{content_type}' -m "$timeout_s" "$base/" || echo 000)"
home_code="${home%% *}"
if [ "$home_code" != "200" ]; then
  echo "FAIL  home  $home_code  $base/"
  exit 1
fi
case "$home" in
  *text/html*) echo "ok    home  200 text/html" ;;
  *)
    echo "FAIL  home is not text/html ($home): binary built without the embedded frontend?"
    exit 1
    ;;
esac

# Preferred check: the build writes /frontend-build.json listing every embedded
# file with its size and sha256. Fetching that list covers what parsing the
# entry bundle cannot see: chunks imported by lazy chunks, CSS and fonts, and a
# wrong body served with status 200. Instances without the manifest fall back
# to the entry-bundle scan below.
manifest_code="$(curl -s -o "$tmp/manifest.json" -w '%{http_code}' -m "$timeout_s" "$base/frontend-build.json" || echo 000)"
if [ "$manifest_code" = "200" ]; then
  if ! python3 - "$tmp/manifest.json" "$tmp/files.tsv" <<'PY'; then
import json, re, sys

src, dst = sys.argv[1:3]
try:
    manifest = json.load(open(src, encoding='utf-8'))
except Exception as exc:
    sys.exit(f'FAIL  manifest is not JSON: {exc}')
files = manifest.get('files')
if manifest.get('schema') != 1 or manifest.get('product') != 'sub2api-frontend':
    sys.exit('FAIL  manifest schema/product is not sub2api-frontend v1')
if not isinstance(files, list) or not files:
    sys.exit('FAIL  manifest lists no files')
entry = manifest.get('entry') or 'index.html'
rows = []
for item in files:
    path, digest, size = item.get('path'), item.get('sha256'), item.get('size')
    if not isinstance(path, str) or not re.fullmatch(r'[A-Za-z0-9._/@+~-]+', path) or '..' in path or path.startswith('/'):
        sys.exit(f'FAIL  manifest has an unsafe path: {path!r}')
    if not isinstance(digest, str) or not re.fullmatch(r'[0-9a-f]{64}', digest) or not isinstance(size, int):
        sys.exit(f'FAIL  manifest entry is malformed: {path!r}')
    if path == entry:
        continue  # served with injected settings/nonce, so its bytes differ
    rows.append(f'{path} {digest} {size}')
if not rows:
    sys.exit('FAIL  manifest lists nothing besides the entry page')
with open(dst, 'w', encoding='utf-8') as fh:
    for row in rows:
        print(row, file=fh)
print(f"ok    manifest version={manifest.get('version')} commit={str(manifest.get('commit'))[:9]} files={len(rows)}")
PY
    echo "embedded assets incomplete: do NOT switch traffic to this instance" >&2
    exit 1
  fi
  bad=0
  total=0
  while read -r path digest size; do
    [ -n "$path" ] || continue
    total=$((total + 1))
    got="$(curl -s -o "$tmp/file" -w '%{http_code} %{size_download}' -m "$timeout_s" "$base/$path" || echo "000 0")"
    if [ "$got" != "200 $size" ]; then
      echo "FAIL  file  got=[$got] want=[200 $size]  $path"
      bad=$((bad + 1))
      continue
    fi
    if [ "$(sha256sum "$tmp/file" | cut -d' ' -f1)" != "$digest" ]; then
      echo "FAIL  file  sha256 mismatch  $path"
      bad=$((bad + 1))
    fi
  done <"$tmp/files.tsv"
  echo "checked $total manifest file(s), bad $bad"
  if [ "$bad" != "0" ]; then
    echo "embedded assets incomplete: do NOT switch traffic to this instance" >&2
    exit 1
  fi
  echo "embedded assets complete"
  exit 0
fi
echo "note  no /frontend-build.json ($manifest_code); falling back to the entry-bundle scan"

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
