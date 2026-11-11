<div align="center">

<img src="assets/logo.svg" alt="Sub2API" width="128" />

# Sub2API

[![Go](https://img.shields.io/badge/Go-1.26.11-00ADD8.svg)](https://go.dev/)
[![Nuxt UI](https://img.shields.io/badge/Nuxt_UI-4-00DC82.svg)](https://ui.nuxt.com/)
[![Vue](https://img.shields.io/badge/Vue-3.5+-4FC08D.svg)](https://vuejs.org/)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-15+-336791.svg)](https://www.postgresql.org/)
[![Redis](https://img.shields.io/badge/Redis-7+-DC382D.svg)](https://redis.io/)
[![License](https://img.shields.io/badge/License-LGPL--3.0-blue.svg)](LICENSE)

**Self-hosted AI API gateway for Claude Code, Codex, Gemini CLI, and Grok.**

This repository is [kiss-kedaya/sub2api](https://github.com/kiss-kedaya/sub2api), a maintained fork of [Wei-Shaw/sub2api](https://github.com/Wei-Shaw/sub2api).

[English](README.md) | [中文](README_CN.md)

</div>

## What this fork adds

Compared with upstream, this tree is built around a production site that already runs payment, mixed-platform routing, and WebSocket traffic:

- **API key smart routing** — one key can bind several groups in strict order; protocol mismatches (for example an OpenAI account on an Anthropic path) are skipped instead of being forced.
- **Built-in recharge** — EasyPay (including relative `payurl`/`qrcode`), per-provider fee and multiplier, instance recharge terms. See [docs/PAYMENT_CN.md](docs/PAYMENT_CN.md).
- **Usage dashboard** — 30-day window with hourly rollup when data exists, otherwise a direct `usage_logs` fallback.
- **Operator controls** — connection timelines, scoped payload capture, cache tests, traffic reports, notifications, channel drilldowns, and managed proxies, with opt-in automation and provider channels. See the [operator capability guide](docs/operator-capabilities.md).
- **Gateway reliability** — WebSocket client-close is not treated as account failure; oversized passthrough can fall back to HTTP bridge; Spark 429 stays model-scoped; failed forwards release session slots immediately.
- **Nuxt UI frontend** — Vue pages use `@nuxt/ui` inside a Vite SPA. The complete UI is embedded in the Go binary; no Node, Nitro or BFF service is required.
- **Ops-safe upgrades** — production `DATABASE_MIGRATION_MODE=validate` on the live process; schema changes are applied out of band. Version is a single file: `backend/cmd/server/VERSION`. GitHub Releases fire only on semantic `v*` tags.

Balance preauthorization stays **off** in this deployment (`BILLING_BALANCE_PREAUTHORIZATION_ENABLED=false`). Do not turn it on without a dedicated review.

## Features

- Multi-account upstreams: Anthropic OAuth / setup token, OpenAI API key and OAuth, Gemini, Grok, Antigravity
- API keys, groups, sticky sessions, concurrency and rate limits
- Token-level usage and billing
- Admin and user dashboards
- Composite groups for multi-provider model routing ([docs/COMPOSITE_GROUPS.md](docs/COMPOSITE_GROUPS.md))
- Codex / Claude Code / Gemini CLI / Grok CLI configuration from the key page

## Stack

| Layer | Tech |
|-------|------|
| Backend | Go 1.26.11, Gin, Ent |
| Frontend | Vue 3, @nuxt/ui 4, Vite SPA, TailwindCSS, pnpm |
| UI packaging | Complete SPA embedded in the Go binary (`-tags=embed`) |
| Database | PostgreSQL 15+ |
| Cache | Redis 7+ |

## Repository layout

```
sub2api/
├── backend/                 # Go service
│   ├── cmd/server/          # entry + VERSION
│   ├── internal/            # handlers, services, repositories
│   ├── internal/web/dist/   # complete SPA build output and file manifest
│   ├── migrations/          # numbered SQL
│   └── Makefile
├── frontend/                # Vite SPA with Nuxt UI components
│   ├── src/                 # Vue views, stores, router and API clients
│   └── scripts/             # frontend manifest and verification tools
├── deploy/                  # systemd, Docker, install and SPA proxy templates
└── docs/                    # payment, composite groups, operators, plugins
```

## Build from source

Prerequisites: Go 1.26+, Node.js 24.15+ (CI uses 24.22), pnpm 9, PostgreSQL, Redis.

```bash
git clone https://github.com/kiss-kedaya/sub2api.git
cd sub2api
pnpm --dir frontend install --frozen-lockfile
```

The supported frontend is one SPA bundle. Nuxt UI is the component library; it does not require running a Nuxt server.

### Embedded SPA

```bash
pnpm --dir frontend run build        # writes backend/internal/web/dist
pnpm --dir frontend run verify:embed # manifest must match this checkout

cd backend
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags embed -o sub2api ./cmd/server
```

`make build` at the repo root runs the SPA build and the backend build. This binary serves the API and UI on one port.

### Co-packaged release

```bash
make release-spa   # build SPA + Go, verify the manifest and static bundle
```

`frontend-build.json` records the source commit, build mode and every file hash. `make verify-embed` checks its provenance; `make verify-spa` checks the entry, hashed assets and absence of retired SSR output. See [deploy/README.md](deploy/README.md#frontend).

Version string comes from `backend/cmd/server/VERSION` (or the `v*` tag when building a tagged checkout). Do not scatter version files elsewhere.

Run without embedding UI only for local backend work:

```bash
cd backend
go run ./cmd/server
```

Frontend dev server:

```bash
pnpm --dir frontend dev
```

Copy `deploy/config.example.yaml` to `backend/config.yaml` for binary deploys. First boot without a config file opens the setup wizard at `http://<host>:8080`.

## Releases and install

GitHub Actions [release.yml](.github/workflows/release.yml) publishes assets when a semantic `v*` tag is pushed. A branch push only runs CI.

Release archives contain the Go binary with the complete SPA embedded, together with `checksums.txt`. Use the official GitHub Actions artifacts and verify the archive checksum before deployment. The matching workflow run also uploads a `frontend-dist` Actions artifact for deployments that serve assets from disk; Actions artifacts have limited retention. The embedded UI remains part of the permanent release binary.

When a reverse proxy serves a disk-based UI, deploy the complete matching static directory as well as the backend. Stage a new directory and atomically switch its pointer. Preserve the previous directory and old hashed assets until existing browser sessions and rollback no longer need them; replacing only `index.html` or the binary can leave clients loading incompatible assets.

Linux install from a release:

```bash
curl -sSL https://raw.githubusercontent.com/kiss-kedaya/sub2api/main/deploy/install.sh | sudo bash
sudo systemctl enable --now sub2api
```

Docker:

```bash
mkdir -p sub2api-deploy && cd sub2api-deploy
curl -sSL https://raw.githubusercontent.com/kiss-kedaya/sub2api/main/deploy/docker-deploy.sh | bash
docker compose up -d
```

Details: [deploy/README.md](deploy/README.md), [deploy/DOCKER.md](deploy/DOCKER.md).

## Deployment topology

Production layout:

```
browser ──► reverse proxy ──► Go service
                              /api/**       API handlers
                              /v1/**        gateway, SSE and WebSocket
                              /assets/**    complete hashed SPA assets
                              page routes   SPA index.html
```

- SPA pages call the Go API on the same origin; there is no BFF session layer.
- API and gateway routes must be matched before the SPA fallback, so JSON requests never receive `index.html`.
- A proxy may serve the static SPA directory directly. Its assets must match the deployed release and remain available across cutover.

Templates: [SPA server](deploy/nginx/sub2api-spa-server.conf), [API locations](deploy/nginx/sub2api-backend-locations.conf), [static SPA locations](deploy/nginx/sub2api-spa.conf).

## Production notes

- Keep the Go process on `127.0.0.1:8080` and put Nginx (or another reverse proxy) in front. Enable `underscores_in_headers on;` so sticky-session headers survive.
- Node.js and pnpm are build tools only; production serves the compiled SPA through Go or the reverse proxy.
- Live traffic process: `DATABASE_MIGRATION_MODE=validate`. Do not let the primary 8080 process apply migrations.
- Before any upgrade, take a full database backup. Never let a migration containing `DROP` run through the default auto-apply path (`DATABASE_MIGRATION_MODE=apply`); apply destructive migrations deliberately, out of band.
- Canary a new binary on another port (for example 8081), then switch Nginx. Never kill the primary while it still serves traffic.
- WebSocket / SSE: if an egress proxy breaks OpenAI WS, `GATEWAY_OPENAI_WS_FORCE_HTTP=true` forces upstream HTTP/SSE without changing the client protocol.
- Logs, metrics, and error pages must not contain upstream credentials, internal hostnames, or full provider endpoints.

## Nginx

```nginx
underscores_in_headers on;
```

Without this, Nginx drops headers such as `session_id` and sticky routing breaks.

## Simple mode

`RUN_MODE=simple` hides SaaS billing UI. In production also set `SIMPLE_MODE_CONFIRM=true`.

## License

[GNU Lesser General Public License v3.0](LICENSE) (or later).

Upstream copyright remains with the original Sub2API authors. This fork does not grant extra commercial rights.
