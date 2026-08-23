#!/bin/sh
set -e

# Fix data directory permissions when running as root.
# Docker named volumes / host bind-mounts may be owned by root,
# preventing the non-root sub2api user from writing files.
if [ "$(id -u)" = "0" ]; then
    exec_user=sub2api
    exec_group=sub2api
    mkdir -p /app/data
    # Use || true to avoid failure on read-only mounted files (e.g. config.yaml:ro)
    chown -R sub2api:sub2api /app/data 2>/dev/null || true

    # A mounted Docker socket keeps the host group ID. Create a matching
    # supplementary group before dropping privileges so orchestrated updates
    # work without hard-coding a host-specific group_add value in Compose.
    if [ "${SUB2API_UPDATE_AUTO_DOCKER_GROUP:-true}" = "true" ] && [ -S /var/run/docker.sock ]; then
        docker_gid="$(stat -c '%g' /var/run/docker.sock 2>/dev/null || true)"
        case "$docker_gid" in
            ''|*[!0-9]*|0) ;;
            *)
                docker_group="$(awk -F: -v gid="$docker_gid" '$3 == gid { print $1; exit }' /etc/group 2>/dev/null || true)"
                if [ -z "$docker_group" ]; then
                    docker_group="sub2api-docker"
                    existing_gid="$(awk -F: -v name="$docker_group" '$1 == name { print $3; exit }' /etc/group 2>/dev/null || true)"
                    if [ -n "$existing_gid" ] && [ "$existing_gid" != "$docker_gid" ]; then
                        docker_group="sub2api-docker-$docker_gid"
                    fi
                    addgroup -g "$docker_gid" "$docker_group" >/dev/null 2>&1 || true
                fi
                addgroup sub2api "$docker_group" >/dev/null 2>&1 || true
                # su-exec intentionally resets supplementary groups. Use the
                # socket group as the process GID so Docker access survives
                # the privilege drop without running the app as root.
                exec_group="$docker_group"
                ;;
        esac
    fi

    # Re-invoke this script as sub2api so the flag-detection below
    # also runs under the correct user.
    exec su-exec "$exec_user:$exec_group" "$0" "$@"
fi

# Compatibility: if the first arg looks like a flag (e.g. --help),
# prepend the default binary so it behaves the same as the old
# ENTRYPOINT ["/app/sub2api"] style.
if [ "${1#-}" != "$1" ]; then
    set -- /app/sub2api "$@"
fi

exec "$@"
