# Sub2API Docker Image

Sub2API is an AI API Gateway Platform for distributing and managing AI product subscription API quotas.

## Quick Start

```bash
docker run -d \
  --name sub2api \
  -p 8080:8080 \
  -e DATABASE_URL="postgres://user:pass@host:5432/sub2api" \
  -e REDIS_URL="redis://host:6379" \
  ghcr.io/kiss-kedaya/sub2api:0.1.186
```

## Docker Compose

```yaml
version: '3.8'

services:
  sub2api:
    image: ghcr.io/kiss-kedaya/sub2api:0.1.186
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
Compose deployments should opt into the host-side orchestrator instead.

The image contains `update-orchestrator.sh` at
`/usr/local/bin/sub2api-update`. Configure the application container with:

```yaml
environment:
  UPDATE_STRATEGY: orchestrated
  UPDATE_ORCHESTRATOR: /usr/local/bin/sub2api-update
  SUB2API_UPDATE_COMPOSE_FILE: /opt/sub2api/docker-compose.yml
  SUB2API_UPDATE_SERVICES: sub2api-1,sub2api-2,sub2api-3
  SUB2API_UPDATE_HEALTH_URLS: http://127.0.0.1:7101/health,http://127.0.0.1:7102/health,http://127.0.0.1:7103/health
  SUB2API_UPDATE_PROJECT: sub2api
volumes:
  - /var/run/docker.sock:/var/run/docker.sock
  - /opt/sub2api:/opt/sub2api:ro
```

The application image includes the Docker CLI, but Docker socket access is
intentionally opt-in because it grants host-level control. The Compose project
must be mounted at the same path inside the updater container. The script then
pulls the target image,
recreates each configured service in order, waits for its health endpoint, and
restores the previous version if any step fails. Compose files should use the
version variable so the target image is unambiguous:

```yaml
image: ghcr.io/kiss-kedaya/sub2api:${SUB2API_VERSION:-0.1.186}
```

The updater API does not expose arbitrary shell commands; it executes only the
absolute path configured in `UPDATE_ORCHESTRATOR` and passes the current and
target versions as arguments.

## Links

- [GitHub Repository](https://github.com/weishaw/sub2api)
- [Documentation](https://github.com/weishaw/sub2api#readme)
