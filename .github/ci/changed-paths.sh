#!/usr/bin/env bash
# Classify a pull request by its changed paths (one per line on stdin).
# Prints "backend=false" only when every path is under frontend/, docs/ or
# openspec/, or is a Markdown file. Anything else, or no paths at all, prints
# "backend=true", so the Go test jobs run whenever there is doubt.
#
# frontend/ and docs/ still get the frontend and build jobs: the SPA imports
# docs/legal/*.md at build time and the Go binary embeds the SPA.
set -euo pipefail
backend=false
seen=false
while IFS= read -r path; do
  [ -n "$path" ] || continue
  seen=true
  case "$path" in
    frontend/*|docs/*|openspec/*|*.md) ;;
    *) backend=true ;;
  esac
done
[ "$seen" = true ] || backend=true
echo "backend=$backend"
