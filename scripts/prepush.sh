#!/usr/bin/env bash
# Pre-push checks, limited to what changed relative to origin/main.
#
#   scripts/prepush.sh [--base REF] [--full] [--list] [--skip-go] [--skip-frontend]
#
# Go (backend/), on the changed packages only:
#   gofmt -s and the interface{} -> any rewrite from backend/.golangci.yml,
#   go vet (plain, -tags=unit, and -tags=integration when the package has such files),
#   golangci-lint restricted to errcheck, ineffassign, depguard with backend/.golangci.yml,
#   so check-type-assertions and disable-default-exclusions apply exactly as in CI.
#   --full runs every linter of that config on those packages (slow: staticcheck, unused).
# Frontend (frontend/), when something there changed:
#   eslint on the changed files, then vue-tsc --noEmit for the project.
#
# Every stage runs even if an earlier one failed, so one run lists all problems.
# Tests are not run here; CI runs them.
set -u
cd "$(git rev-parse --show-toplevel)" || exit 2

base_ref=origin/main; full=0; list=0; skip_go=0; skip_fe=0
while [ $# -gt 0 ]; do
  case "$1" in
    --base) base_ref=${2:?--base needs a ref}; shift ;;
    --full) full=1 ;;
    --list) list=1 ;;
    --skip-go) skip_go=1 ;;
    --skip-frontend) skip_fe=1 ;;
    -h|--help) sed -n '2,17p' "$0"; exit 0 ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
  shift
done

base=$(git merge-base "$base_ref" HEAD) || { echo "no merge base with $base_ref (run: git fetch origin)" >&2; exit 2; }
changed=$( { git diff --name-only --diff-filter=ACMR "$base"; git ls-files --others --exclude-standard; } | sort -u)
deleted=$(git diff --name-only --diff-filter=D "$base")

go_files=$(printf '%s\n' "$changed" | grep -E '^backend/.*\.go$' || true)
go_dirs=$( { printf '%s\n' "$go_files"; printf '%s\n' "$deleted" | grep -E '^backend/.*\.go$' || true; } \
  | grep . | while read -r f; do d=$(dirname "$f"); [ -d "$d" ] || continue
      case "$d" in backend) echo . ;; *) echo "${d#backend/}" ;; esac; done | sort -u)
pkgs=$(for d in $go_dirs; do echo "./$d"; done)
int_pkgs=$(for d in $go_dirs; do grep -lqE '^//go:build .*integration' backend/"$d"/*.go 2>/dev/null && echo "./$d"; done)
fe_files=$(printf '%s\n' "$changed" | grep -E '^frontend/.*\.(vue|js|jsx|cjs|mjs|ts|tsx|cts|mts)$' || true)
fe_any=$(printf '%s\n' "$changed" | grep -E '^frontend/' || true)

if [ "$list" -eq 1 ]; then
  echo "base: $base_ref ($(git rev-parse --short "$base"))"
  echo "go files: $(printf '%s' "$go_files" | grep -c .), packages: $(printf '%s' "$pkgs" | grep -c .)"
  printf '%s\n' "$pkgs"; echo "frontend files: $(printf '%s' "$fe_files" | grep -c .)"; exit 0
fi

failed=()
stage() { # stage NAME CMD...
  local name=$1 rc=0 t0=$SECONDS; shift
  printf '\n==> %s\n' "$name"
  "$@" || rc=$?
  printf '<== %s: %s (%ss)\n' "$name" "$([ "$rc" -eq 0 ] && echo ok || echo FAIL)" "$((SECONDS - t0))"
  [ "$rc" -eq 0 ] || failed+=("$name")
}

fmt_check() {
  local out
  out=$( { gofmt -s -l "$@"; gofmt -r 'interface{} -> any' -l "$@"; } 2>&1 | sort -u)
  [ -z "$out" ] || { echo "not formatted (gofmt -s -w, or interface{} -> any):"; echo "$out"; return 1; }
}
vet_go() {
  # shellcheck disable=SC2086
  ( cd backend && go vet $pkgs && go vet -tags=unit $pkgs && { [ -z "$int_pkgs" ] || go vet -tags=integration $int_pkgs; } )
}
lint_go() {
  command -v golangci-lint >/dev/null || { echo "golangci-lint not found: go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.0"; return 1; }
  # shellcheck disable=SC2086
  if [ "$full" -eq 1 ]; then ( cd backend && golangci-lint run --timeout=30m $pkgs )
  else ( cd backend && golangci-lint run --enable-only errcheck,ineffassign,depguard --timeout=15m $pkgs ); fi
}
need_fe_deps() {
  [ -d frontend/node_modules ] || { echo "frontend deps missing: pnpm --dir frontend install --frozen-lockfile"; return 1; }
}
eslint_fe() {
  # shellcheck disable=SC2086
  ( cd frontend && pnpm exec eslint --no-error-on-unmatched-pattern $(printf '%s\n' "$fe_files" | sed 's#^frontend/##') )
}

if [ "$skip_go" -eq 0 ] && [ -n "$pkgs" ]; then
  echo "go: $(printf '%s' "$go_files" | grep -c .) files, $(printf '%s' "$pkgs" | grep -c .) packages (base $base_ref)"
  # shellcheck disable=SC2086
  stage "gofmt" fmt_check $go_files
  stage "go vet" vet_go
  stage "golangci-lint$([ "$full" -eq 1 ] && echo ' (full)' || echo ' (errcheck, ineffassign, depguard)')" lint_go
elif [ "$skip_go" -eq 0 ]; then echo "go: no changed Go files"; fi

if [ "$skip_fe" -eq 0 ] && [ -n "$fe_any" ]; then
  stage "frontend deps" need_fe_deps
  if [ -d frontend/node_modules ]; then
    [ -z "$fe_files" ] || stage "eslint (changed files)" eslint_fe
    stage "vue-tsc" pnpm --dir frontend run typecheck
  fi
elif [ "$skip_fe" -eq 0 ]; then echo "frontend: no changes"; fi

echo
if [ ${#failed[@]} -eq 0 ]; then echo "prepush: all checks passed (${SECONDS}s)"; exit 0; fi
printf 'prepush: FAILED (%ss): %s\n' "$SECONDS" "$(IFS=,; echo "${failed[*]}")"; exit 1
