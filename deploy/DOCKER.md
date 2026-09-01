# Sub2API Docker Image

Sub2API is an AI API Gateway Platform for distributing and managing AI product subscription API quotas.

## Quick Start

```bash
docker run -d \
  --name sub2api \
  -p 8080:8080 \
  -e DATABASE_URL="postgres://user:pass@host:5432/sub2api" \
  -e REDIS_URL="redis://host:6379" \
  ghcr.io/kiss-kedaya/sub2api:0.1.234
```

## Docker Compose

```yaml
version: '3.8'

services:
  sub2api:
    image: ghcr.io/kiss-kedaya/sub2api:0.1.234
    ports:
      - "8080:8080"
    environment:
      - DATABASE_URL=postgres://postgres:postgres@db:5432/sub2api?sslmode=disable
      - REDIS_URL=redis://redis:6379
    depends_on:
      - db
      - redis

  db:
    image: postgres:15-alpine
    environment:
      - POSTGRES_USER=postgres
      - POSTGRES_PASSWORD=postgres
      - POSTGRES_DB=sub2api
    volumes:
      - postgres_data:/var/lib/postgresql/data

  redis:
    image: redis:7-alpine
    volumes:
      - redis_data:/data

volumes:
  postgres_data:
  redis_data:
```

## Environment Variables

| Variable | Description | Required | Default |
|----------|-------------|----------|---------|
| `DATABASE_URL` | PostgreSQL connection string | Yes | - |
| `REDIS_URL` | Redis connection string | Yes | - |
| `PORT` | Server port | No | `8080` |
| `GIN_MODE` | Gin framework mode (`debug`/`release`) | No | `release` |

## Supported Architectures

- `linux/amd64`
- `linux/arm64`

## Tags

- `latest` - Latest stable release
- `x.y.z` - Specific version
- `x.y` - Latest patch of minor version
- `x` - Latest minor of major version

## One-click rolling updates

The admin version menu uses the regular binary updater by default. A Docker
container cannot safely replace the executable inside its image, so production
Compose deployments should opt into the orchestrated updater instead.

The image contains `update-orchestrator.sh` at
`/usr/local/bin/sub2api-update`. Configure the application container with:

```yaml
environment:
  UPDATE_STRATEGY: orchestrated
  UPDATE_ORCHESTRATOR: /usr/local/bin/sub2api-update
  SUB2API_UPDATE_MODE: image
  SUB2API_UPDATE_COMPOSE_FILE: /opt/sub2api/docker-compose.yml
  SUB2API_UPDATE_SERVICES: sub2api-1,sub2api-2,sub2api-3
  SUB2API_UPDATE_HEALTH_URLS: http://127.0.0.1:7101/health,http://127.0.0.1:7102/health,http://127.0.0.1:7103/health
  SUB2API_UPDATE_PROJECT: sub2api
  SERVER_SHUTDOWN_TIMEOUT_SECONDS: "600"
  # Keep Docker's hard stop above the application drain window.
  SUB2API_UPDATE_DRAIN_TIMEOUT_SECONDS: "610"
  # Optional command wrapper when the container cannot use the socket group.
  # Example: sudo -n docker (requires a narrowly-scoped host sudo rule).
  SUB2API_UPDATE_DOCKER_COMMAND: docker
  SUB2API_UPDATE_AUTO_DOCKER_GROUP: "true"
  # Optional when .env is mode 600 and the app runs as a non-root user.
  SUB2API_UPDATE_HELPER_IMAGE: ghcr.io/kiss-kedaya/sub2api:0.1.234
volumes:
  - /var/run/docker.sock:/var/run/docker.sock
  - /opt/sub2api:/opt/sub2api:ro
# Match the host socket group (get it with: stat -c '%g' /var/run/docker.sock).
group_add:
  - "989"
```

The image automatically mirrors the mounted socket's numeric group into the
`sub2api` user's supplementary groups before dropping privileges. This keeps
the documented `group_add` form optional for standard Compose deployments; set
`SUB2API_UPDATE_AUTO_DOCKER_GROUP=false` when group membership must remain
explicit. The socket is still a deliberate host-control capability and should
only be mounted for a trusted deployment.

The application image includes the Docker CLI, but Docker socket access is
intentionally opt-in because it grants host-level control. The Compose project
must be mounted at the same path inside the updater container. In `image` mode
the script pulls the target image, recreates each configured service in order,
waits for its health endpoint, and restores the previous version if any step
fails. If the mounted `.env` is root-only, the script starts the configured
helper image as UID 0 with only the Compose file, env file, and Docker socket
mounted; it never changes secret-file permissions. Compose files should use the
version variable so the target image is unambiguous:

```yaml
image: ghcr.io/kiss-kedaya/sub2api:${SUB2API_VERSION:-0.1.234}
```

During each replacement the updater sends `SIGTERM` to exactly one replica.
The Go server closes its listener immediately, which prevents new requests
from entering that replica, and then waits for active HTTP/SSE requests to
finish. Docker applies `SUB2API_UPDATE_DRAIN_TIMEOUT_SECONDS` as the hard stop;
it must remain greater than `SERVER_SHUTDOWN_TIMEOUT_SECONDS`. Only after the
replica exits, starts on the new version, and passes its health check does the
updater continue to the next service.

When the release image is private or the deployment uses a locally built image,
use `runtime` mode instead. It downloads and verifies the matching release
binary, atomically replaces a mounted runtime path, and then performs the same
rolling health checks and rollback. For Docker, the path must be the path inside
the updater container and services are found by Compose labels, so no Compose
file or `.env` read is needed during the rollout:

```yaml
environment:
  UPDATE_STRATEGY: orchestrated
  UPDATE_ORCHESTRATOR: /usr/local/bin/sub2api-update
  SUB2API_UPDATE_MODE: runtime
   SUB2API_UPDATE_RUNTIME_PATH: /app/runtime/sub2api
   SUB2API_UPDATE_REPOSITORY: kiss-kedaya/sub2api
   SUB2API_UPDATE_PROJECT: sub2api
   SUB2API_UPDATE_SERVICES: api,worker
command: ["/app/runtime/sub2api"]
volumes:
   - /opt/sub2api/runtime:/app/runtime
   - /var/run/docker.sock:/var/run/docker.sock
group_add:
   - "989"
```

For a source-built systemd service, keep `UPDATE_STRATEGY=orchestrated`, set
`SUB2API_UPDATE_MODE=runtime`, point `SUB2API_UPDATE_RUNTIME_PATH` at the
installed binary, and provide a fixed command such as
`SUB2API_UPDATE_RESTART_COMMAND="systemctl restart sub2api"`. Use health URLs
for every service; the command is executed only after checksum verification and
is never accepted from the HTTP request.

The updater API does not expose arbitrary shell commands; it executes only the
absolute path configured in `UPDATE_ORCHESTRATOR` and passes the current and
target versions as arguments.

## Links

- [GitHub Repository](https://github.com/weishaw/sub2api)
- [Documentation](https://github.com/weishaw/sub2api#readme)
