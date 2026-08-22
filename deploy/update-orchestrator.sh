#!/usr/bin/env bash
set -Eeuo pipefail

# Host-side updater for Docker Compose deployments.
# The application calls this script only when UPDATE_STRATEGY=orchestrated.
# It deliberately owns the full transaction so a failed health check restores
# the previous image before returning an error to the admin API.

COMPOSE_FILE="${SUB2API_UPDATE_COMPOSE_FILE:-}"
COMPOSE_PROJECT="${SUB2API_UPDATE_PROJECT:-}"
SERVICES_RAW="${SUB2API_UPDATE_SERVICES:-sub2api}"
HEALTH_URLS_RAW="${SUB2API_UPDATE_HEALTH_URLS:-${SUB2API_UPDATE_HEALTH_URL:-}}"
HEALTH_TIMEOUT="${SUB2API_UPDATE_HEALTH_TIMEOUT_SECONDS:-120}"
ENV_FILE="${SUB2API_UPDATE_ENV_FILE:-}"

CURRENT_VERSION=""
TARGET_VERSION=""
RELEASE_URL=""

# Service names are identifiers, so whitespace around comma-separated values
# is unambiguous and can be ignored safely.
SERVICES_RAW="${SERVICES_RAW//[[:space:]]/}"

log() {
  printf '[sub2api-update] %s\n' "$*"
}

fail() {
  printf '[sub2api-update] ERROR: %s\n' "$*" >&2
  exit 1
}

usage() {
  cat <<'EOF'
Usage: update-orchestrator.sh --current-version X.Y.Z --target-version X.Y.Z [--release-url URL]

Required environment:
  SUB2API_UPDATE_COMPOSE_FILE   Compose file used by the production service

Optional environment:
  SUB2API_UPDATE_PROJECT         Compose project name
  SUB2API_UPDATE_SERVICES        Comma-separated app services, in rollout order
  SUB2API_UPDATE_HEALTH_URLS     Comma-separated health URLs, one per service
  SUB2API_UPDATE_ENV_FILE        Env file used by Compose
  SUB2API_UPDATE_HEALTH_TIMEOUT_SECONDS (default: 120)
  SUB2API_UPDATE_VERSION_ENV     Variable name used by the image tag (default: SUB2API_VERSION)
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --current-version) CURRENT_VERSION="${2:-}"; shift 2 ;;
    --target-version) TARGET_VERSION="${2:-}"; shift 2 ;;
    --release-url) RELEASE_URL="${2:-}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) fail "unknown argument: $1" ;;
  esac
done

[ -n "$COMPOSE_FILE" ] || fail 'SUB2API_UPDATE_COMPOSE_FILE is not configured'
[ -f "$COMPOSE_FILE" ] || fail "compose file not found: $COMPOSE_FILE"
[ -n "$CURRENT_VERSION" ] || fail '--current-version is required'
[ -n "$TARGET_VERSION" ] || fail '--target-version is required'
if [[ ! "$CURRENT_VERSION" =~ ^[0-9]+(\.[0-9]+){0,2}$ ]]; then
  fail "invalid current version: $CURRENT_VERSION"
fi
if [[ ! "$TARGET_VERSION" =~ ^[0-9]+(\.[0-9]+){0,2}$ ]]; then
  fail "invalid target version: $TARGET_VERSION"
fi

if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
  COMPOSE=(docker compose)
elif command -v docker-compose >/dev/null 2>&1; then
  COMPOSE=(docker-compose)
else
  fail 'docker compose is not available'
fi

compose_args=(-f "$COMPOSE_FILE")
if [ -n "$COMPOSE_PROJECT" ]; then
  compose_args+=(-p "$COMPOSE_PROJECT")
fi
if [ -n "$ENV_FILE" ]; then
  [ -f "$ENV_FILE" ] || fail "env file not found: $ENV_FILE"
  compose_args+=(--env-file "$ENV_FILE")
fi

IFS=',' read -r -a SERVICES <<< "$SERVICES_RAW"
[ "${#SERVICES[@]}" -gt 0 ] || fail 'no services configured'

IFS=',' read -r -a HEALTH_URLS <<< "$HEALTH_URLS_RAW"
[ -n "$HEALTH_URLS_RAW" ] || fail 'SUB2API_UPDATE_HEALTH_URLS is required for post-rollout verification'
VERSION_ENV="${SUB2API_UPDATE_VERSION_ENV:-SUB2API_VERSION}"
if [[ ! "$VERSION_ENV" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]]; then
  fail "invalid version variable: $VERSION_ENV"
fi

backup_dir="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-update.XXXXXX")"
cleanup() { rm -rf "$backup_dir"; }
trap cleanup EXIT

cp -p "$COMPOSE_FILE" "$backup_dir/compose.yml"
if [ -n "$ENV_FILE" ]; then
  cp -p "$ENV_FILE" "$backup_dir/env"
fi

compose() {
  "${COMPOSE[@]}" "${compose_args[@]}" "$@"
}

compose_with_version() {
  local version="$1"
  shift
  local had_previous=false
  local previous_value=""
  if [[ -v "$VERSION_ENV" ]]; then
    had_previous=true
    previous_value="${!VERSION_ENV}"
  fi
  export "$VERSION_ENV=$version"
  local status=0
  if compose "$@"; then
    status=0
  else
    status=$?
  fi
  if [ "$had_previous" = true ]; then
    export "$VERSION_ENV=$previous_value"
  else
    unset "$VERSION_ENV"
  fi
  return "$status"
}

health_url_for() {
  local index="$1"
  if [ "${#HEALTH_URLS[@]}" -eq 0 ] || [ -z "${HEALTH_URLS[0]:-}" ]; then
    return 0
  fi
  if [ "$index" -lt "${#HEALTH_URLS[@]}" ] && [ -n "${HEALTH_URLS[$index]}" ]; then
    printf '%s' "${HEALTH_URLS[$index]}"
  else
    printf '%s' "${HEALTH_URLS[0]}"
  fi
}

wait_for_health() {
  local url="$1"
  [ -n "$url" ] || return 0
  command -v curl >/dev/null 2>&1 || fail 'curl is required when a health URL is configured'
  local deadline=$(( $(date +%s) + HEALTH_TIMEOUT ))
  until curl --fail --silent --show-error --max-time 5 "$url" >/dev/null; do
    if [ "$(date +%s)" -ge "$deadline" ]; then
      return 1
    fi
    sleep 2
  done
}

rollback() {
  log "rolling back to $CURRENT_VERSION"
  local index=0
  for service in "${SERVICES[@]}"; do
    service="$(printf '%s' "$service" | xargs)"
    [ -n "$service" ] || continue
    log "restarting $service at $CURRENT_VERSION"
    compose_with_version "$CURRENT_VERSION" up -d --no-deps --force-recreate "$service" || return 1
    local url
    url="$(health_url_for "$index")"
    wait_for_health "$url" || return 1
    index=$((index + 1))
  done
}

log "pulling $TARGET_VERSION from $RELEASE_URL"
if ! compose_with_version "$TARGET_VERSION" pull "${SERVICES[@]}"; then
  fail 'image pull failed; no services were changed'
fi

index=0
for service in "${SERVICES[@]}"; do
  service="$(printf '%s' "$service" | xargs)"
  [ -n "$service" ] || continue
  log "rolling $service to $TARGET_VERSION"
  if ! compose_with_version "$TARGET_VERSION" up -d --no-deps --force-recreate "$service"; then
    log 'rollout command failed; starting rollback'
    rollback || fail 'rollout failed and rollback also failed'
    fail 'rollout failed; previous version restored'
  fi
  url="$(health_url_for "$index")"
  if ! wait_for_health "$url"; then
    log "health check failed for $service; starting rollback"
    rollback || fail 'health check failed and rollback also failed'
    fail 'health check failed; previous version restored'
  fi
  index=$((index + 1))
done

log "update completed: $CURRENT_VERSION -> $TARGET_VERSION"
