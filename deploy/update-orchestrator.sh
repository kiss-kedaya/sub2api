#!/usr/bin/env bash
set -Eeuo pipefail

# Host-side updater for Docker Compose deployments. The application invokes
# this script only when UPDATE_STRATEGY=orchestrated. Image mode pulls a new
# image; runtime mode downloads the release binary into a mounted path. Both
# modes roll services one at a time and restore the previous state on failure.

COMPOSE_FILE="${SUB2API_UPDATE_COMPOSE_FILE:-}"
COMPOSE_PROJECT="${SUB2API_UPDATE_PROJECT:-}"
SERVICES_RAW="${SUB2API_UPDATE_SERVICES:-sub2api}"
HEALTH_URLS_RAW="${SUB2API_UPDATE_HEALTH_URLS:-${SUB2API_UPDATE_HEALTH_URL:-}}"
HEALTH_TIMEOUT="${SUB2API_UPDATE_HEALTH_TIMEOUT_SECONDS:-120}"
ENV_FILE="${SUB2API_UPDATE_ENV_FILE:-}"
UPDATE_MODE="${SUB2API_UPDATE_MODE:-image}"
RUNTIME_PATH="${SUB2API_UPDATE_RUNTIME_PATH:-}"
REPOSITORY="${SUB2API_UPDATE_REPOSITORY:-kiss-kedaya/sub2api}"

CURRENT_VERSION=""
TARGET_VERSION=""
RELEASE_URL=""
RUNTIME_BACKUP=""
RUNTIME_HAD_PREVIOUS=false
RUNTIME_CHANGED=false

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
  SUB2API_UPDATE_MODE            image (default) or runtime
  SUB2API_UPDATE_RUNTIME_PATH    Absolute host path for runtime mode
  SUB2API_UPDATE_REPOSITORY      GitHub repository (default: kiss-kedaya/sub2api)
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
case "$UPDATE_MODE" in
  image|runtime) ;;
  *) fail "invalid SUB2API_UPDATE_MODE: $UPDATE_MODE (expected image or runtime)" ;;
esac
if [ "$UPDATE_MODE" = runtime ]; then
  [ -n "$RUNTIME_PATH" ] || fail 'SUB2API_UPDATE_RUNTIME_PATH is required in runtime mode'
  [[ "$RUNTIME_PATH" = /* ]] || fail 'SUB2API_UPDATE_RUNTIME_PATH must be absolute'
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
ORIGINAL_SERVICES=("${SERVICES[@]}")

# When the updater runs inside one of the services it is about to recreate,
# move that service to the end. The final restart is delegated to a detached
# helper container so the updater process can return its HTTP response first.
SELF_CONTAINER="${SUB2API_UPDATE_SELF_CONTAINER:-}"
SELF_SERVICE="${SUB2API_UPDATE_SELF_SERVICE:-}"
if [ -z "$SELF_CONTAINER" ] && [ -r /etc/hostname ] && command -v docker >/dev/null 2>&1; then
  self_id="$(cat /etc/hostname 2>/dev/null || true)"
  if [ -n "$self_id" ]; then
    SELF_CONTAINER="$(docker inspect -f '{{.Name}}' "$self_id" 2>/dev/null | sed 's#^/##' || true)"
    SELF_SERVICE="$(docker inspect -f '{{index .Config.Labels "com.docker.compose.service"}}' "$self_id" 2>/dev/null || true)"
  fi
fi
if [ -n "$SELF_SERVICE" ]; then
  reordered=()
  self_seen=false
  for service in "${SERVICES[@]}"; do
    service="$(printf '%s' "$service" | xargs)"
    if [ "$service" = "$SELF_SERVICE" ]; then
      self_seen=true
      continue
    fi
    reordered+=("$service")
  done
  if [ "$self_seen" = true ]; then
    reordered+=("$SELF_SERVICE")
    SERVICES=("${reordered[@]}")
  fi
fi

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
  local configured=""
  if [ "${#HEALTH_URLS[@]}" -eq 0 ] || [ -z "${HEALTH_URLS[0]:-}" ]; then
    return 0
  fi
  if [ "$index" -lt "${#HEALTH_URLS[@]}" ]; then
    configured="${HEALTH_URLS[$index]}"
  elif [ "${#HEALTH_URLS[@]}" -eq 1 ]; then
    # Backward-compatible single URL configuration.
    configured="${HEALTH_URLS[0]}"
  fi
  if [ "$configured" = container ] || [ "$configured" = - ]; then
    return 0
  fi
  if [ -n "$configured" ]; then
    printf '%s' "$configured"
  fi
}

health_url_for_service() {
  local service="$1"
  local index=0
  for original in "${ORIGINAL_SERVICES[@]}"; do
    original="$(printf '%s' "$original" | xargs)"
    if [ "$original" = "$service" ]; then
      health_url_for "$index"
      return 0
    fi
    index=$((index + 1))
  done
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

wait_for_container_health() {
  local service="$1"
  command -v docker >/dev/null 2>&1 || fail 'docker is required for container health checks'
  local deadline=$(( $(date +%s) + HEALTH_TIMEOUT ))
  local container=""
  local state=""
  until [ -n "$container" ] && [ "$state" = healthy ]; do
    container="$(compose ps -q "$service" 2>/dev/null | tail -n 1)"
    if [ -n "$container" ]; then
      state="$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$container" 2>/dev/null || true)"
    fi
    if [ "$state" = unhealthy ] || [ "$state" = exited ] || [ "$state" = dead ]; then
      return 1
    fi
    if [ "$(date +%s)" -ge "$deadline" ]; then
      return 1
    fi
    [ "$state" = healthy ] || sleep 2
  done
}

wait_for_service() {
  local service="$1"
  local url
  url="$(health_url_for_service "$service")"
  if [ -n "$url" ]; then
    wait_for_health "$url"
  else
    wait_for_container_health "$service"
  fi
}

download_runtime_binary() {
  command -v curl >/dev/null 2>&1 || fail 'curl is required for runtime updates'
  command -v tar >/dev/null 2>&1 || fail 'tar is required for runtime updates'
  command -v sha256sum >/dev/null 2>&1 || fail 'sha256sum is required for runtime updates'

  local os arch archive base_url archive_path checksum_path expected actual extracted
  case "$(uname -s)" in
    Linux) os=linux ;;
    Darwin) os=darwin ;;
    *) fail "unsupported update operating system: $(uname -s)" ;;
  esac
  case "$(uname -m)" in
    x86_64|amd64) arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    *) fail "unsupported update architecture: $(uname -m)" ;;
  esac

  archive="sub2api_${TARGET_VERSION}_${os}_${arch}.tar.gz"
  base_url="https://github.com/${REPOSITORY}/releases/download/v${TARGET_VERSION}"
  archive_path="$backup_dir/$archive"
  checksum_path="$backup_dir/checksums.txt"

  log "downloading runtime asset $archive"
  curl --fail --silent --show-error --location --retry 3 --connect-timeout 15 --max-time 900 \
    "$base_url/$archive" -o "$archive_path"
  curl --fail --silent --show-error --location --retry 3 --connect-timeout 15 --max-time 60 \
    "$base_url/checksums.txt" -o "$checksum_path"

  expected="$(awk -v file="$archive" '$2 == file { print $1; exit }' "$checksum_path")"
  [ -n "$expected" ] || fail "checksum entry not found for $archive"
  actual="$(sha256sum "$archive_path" | awk '{print $1}')"
  [ "$actual" = "$expected" ] || fail "checksum mismatch for $archive"

  tar -xzf "$archive_path" -C "$backup_dir"
  extracted="$backup_dir/sub2api"
  [ -f "$extracted" ] || fail "release archive did not contain sub2api"
  chmod 0755 "$extracted"

  mkdir -p "$(dirname "$RUNTIME_PATH")"
  if [ -f "$RUNTIME_PATH" ]; then
    cp -p "$RUNTIME_PATH" "$backup_dir/runtime.backup"
    RUNTIME_BACKUP="$backup_dir/runtime.backup"
    RUNTIME_HAD_PREVIOUS=true
  fi
  local staged="$RUNTIME_PATH.tmp.$$"
  cp -p "$extracted" "$staged"
  chmod 0755 "$staged"
  mv -f "$staged" "$RUNTIME_PATH"
  RUNTIME_CHANGED=true
}

restore_runtime() {
  [ "$RUNTIME_CHANGED" = true ] || return 0
  if [ "$RUNTIME_HAD_PREVIOUS" = true ] && [ -f "$RUNTIME_BACKUP" ]; then
    local staged="$RUNTIME_PATH.rollback.$$"
    cp -p "$RUNTIME_BACKUP" "$staged"
    chmod 0755 "$staged"
    mv -f "$staged" "$RUNTIME_PATH"
  else
    rm -f "$RUNTIME_PATH"
  fi
  RUNTIME_CHANGED=false
}

rollback() {
  log "rolling back to $CURRENT_VERSION"
  if [ "$UPDATE_MODE" = runtime ]; then
    restore_runtime || return 1
  fi
  local index=0
  for service in "${SERVICES[@]}"; do
    service="$(printf '%s' "$service" | xargs)"
    [ -n "$service" ] || continue
    # The current service is still on the previous version until the update
    # succeeds, so it must not be recreated while rollback is in progress.
    [ -n "$SELF_SERVICE" ] && [ "$service" = "$SELF_SERVICE" ] && continue
    log "restarting $service at $CURRENT_VERSION"
    compose_with_version "$CURRENT_VERSION" up -d --no-deps --force-recreate "$service" || return 1
    wait_for_service "$service" || return 1
    index=$((index + 1))
  done
}

schedule_self_restart() {
  [ -n "$SELF_CONTAINER" ] || fail 'self container could not be identified for final restart'
  command -v docker >/dev/null 2>&1 || fail 'docker is required for the final service restart'
  local image helper
  image="$(docker inspect -f '{{.Config.Image}}' "$SELF_CONTAINER" 2>/dev/null || true)"
  [ -n "$image" ] || fail "could not determine image for $SELF_CONTAINER"
  helper="sub2api-update-self-${TARGET_VERSION//./-}-$$"
  log "scheduling detached restart for $SELF_CONTAINER"
  docker run --rm -d --name "$helper" \
    -v /var/run/docker.sock:/var/run/docker.sock \
    --entrypoint /bin/sh "$image" \
    -c "sleep 3; docker restart '$SELF_CONTAINER' >/dev/null" >/dev/null
}

if [ "$UPDATE_MODE" = runtime ]; then
  download_runtime_binary
else
  log "pulling $TARGET_VERSION from $RELEASE_URL"
  if ! compose_with_version "$TARGET_VERSION" pull "${SERVICES[@]}"; then
    fail 'image pull failed; no services were changed'
  fi
fi

for service in "${SERVICES[@]}"; do
  service="$(printf '%s' "$service" | xargs)"
  [ -n "$service" ] || continue
  log "rolling $service to $TARGET_VERSION"
  if [ -n "$SELF_SERVICE" ] && [ "$service" = "$SELF_SERVICE" ]; then
    if ! schedule_self_restart; then
      log 'self restart scheduling failed; starting rollback'
      rollback || fail 'self restart scheduling failed and rollback also failed'
      fail 'self restart scheduling failed; previous version restored'
    fi
    continue
  fi
  if ! compose_with_version "$TARGET_VERSION" up -d --no-deps --force-recreate "$service"; then
    log 'rollout command failed; starting rollback'
    rollback || fail 'rollout failed and rollback also failed'
    fail 'rollout failed; previous version restored'
  fi
  if ! wait_for_service "$service"; then
    log "health check failed for $service; starting rollback"
    rollback || fail 'health check failed and rollback also failed'
    fail 'health check failed; previous version restored'
  fi
done

log "update completed: $CURRENT_VERSION -> $TARGET_VERSION"
