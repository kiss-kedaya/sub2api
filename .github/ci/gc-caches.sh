#!/usr/bin/env bash
# Keep only the newest Actions cache of the main branch for each key prefix.
#
#   gc-caches.sh [--apply] [--keep N] PREFIX [PREFIX...]
#
# Dry run by default: prints what would be deleted. Deletion needs --apply.
# Only caches of refs/heads/main whose key starts with a given prefix are
# considered. A prefix must end with "-" and be at least 4 characters long, so
# an empty or broad prefix can never match unrelated caches.
# Needs: gh, GH_TOKEN with actions: write (only for --apply), GH_REPO.
set -euo pipefail

apply=0; keep=1; prefixes=()
while [ $# -gt 0 ]; do
  case "$1" in
    --apply) apply=1 ;;
    --keep) keep=${2:?--keep needs a number}; shift ;;
    -*) echo "unknown option: $1" >&2; exit 2 ;;
    *) prefixes+=("$1") ;;
  esac
  shift
done
[ ${#prefixes[@]} -gt 0 ] || { echo "no prefix given" >&2; exit 2; }
case "$keep" in ''|*[!0-9]*) echo "--keep must be a number" >&2; exit 2 ;; esac
[ "$keep" -ge 1 ] || { echo "--keep must be at least 1" >&2; exit 2; }

for prefix in "${prefixes[@]}"; do
  case "$prefix" in *-) ;; *) echo "prefix must end with '-': $prefix" >&2; exit 2 ;; esac
  [ "${#prefix}" -ge 4 ] || { echo "prefix too short: $prefix" >&2; exit 2; }

  # id, size, created_at of every matching cache, newest first.
  rows=$(gh cache list --ref refs/heads/main --key "$prefix" --limit 100 \
    --json id,key,sizeInBytes,createdAt \
    --jq 'map(select(.key | startswith("'"$prefix"'"))) | sort_by(.createdAt) | reverse | .[] | "\(.id)\t\(.sizeInBytes)\t\(.key)"')
  total=$(printf '%s\n' "$rows" | grep -c . || true)
  echo "$prefix: $total cache(s), keeping the newest $keep"
  [ "$total" -gt "$keep" ] || continue

  printf '%s\n' "$rows" | tail -n +"$((keep + 1))" | while IFS=$'\t' read -r id size key; do
    mb=$((size / 1048576))
    if [ "$apply" -eq 1 ]; then
      echo "delete $id (${mb} MB) $key"
      gh cache delete "$id"
    else
      echo "would delete $id (${mb} MB) $key"
    fi
  done
done
