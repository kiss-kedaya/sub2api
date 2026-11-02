<div align="center">

<img src="assets/logo.svg" alt="Sub2API" width="128" />

# Sub2API

[![Go](https://img.shields.io/badge/Go-1.26.10-00ADD8.svg)](https://go.dev/)
[![Nuxt](https://img.shields.io/badge/Nuxt-4-00DC82.svg)](https://nuxt.com/)
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
- **Nuxt 4 SSR frontend** — server-rendered pages plus a small BFF that keeps access tokens out of the browser payload. The legacy Vite SPA stays embedded in the Go binary as a rollback path.
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
| Backend | Go 1.26.10, Gin, Ent |
| Frontend | Nuxt 4 (SSR, Nitro `node-server`), Vue 3, @nuxt/ui, TailwindCSS, pnpm |
| Frontend rollback | Legacy Vite SPA embedded in the Go binary (`-tags=embed`) |
| Database | PostgreSQL 15+ |
| Cache | Redis 7+ |

## Repository layout

```
sub2api/
├── backend/                 # Go service
│   ├── cmd/server/          # entry + VERSION
│   ├── internal/            # handlers, services, repositories
│   ├── internal/web/dist/   # legacy Vite SPA build output (embedded)
│   ├── migrations/          # numbered SQL
│   └── Makefile
├── frontend/                # Nuxt 4 app (SSR + BFF) and legacy SPA sources
│   ├── app/                 # Nuxt pages, plugins, runtime context
│   ├── server/              # Nitro routes: /api/v1 proxy, session, health
│   ├── src/                 # shared Vue views, stores, API clients
│   └── .output/             # Nuxt/Nitro build output (generated)
├── deploy/                  # systemd, Docker, install scripts, nuxt SSR templates
└── docs/                    # payment, composite groups, operators, plugins
```

## Build from source

Prerequisites: Go 1.26+, Node.js 24.15+ (CI uses 24.22), pnpm 9, PostgreSQL, Redis.

```bash
git clone https://github.com/kiss-kedaya/sub2api.git
cd sub2api
pnpm --dir frontend install --frozen-lockfile
```

There are two frontend artifacts. A full release ships both, and they must come from the same commit.

### 1. Legacy SPA embedded in the Go binary

```bash
pnpm --dir frontend run build        # writes backend/internal/web/dist
pnpm --dir frontend run verify:embed # manifest must match this checkout

cd backend
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags embed -o sub2api ./cmd/server
```

`make build` at the repo root runs the SPA build and the backend build. This binary serves the API and the rollback UI on one port.

### 2. Nuxt SSR bundle for Node

```bash
pnpm --dir frontend run build:ssr    # writes frontend/.output (Nitro node-server)
pnpm --dir frontend run verify:ssr
node frontend/.output/server/index.mjs
```

The SSR process needs `NUXT_BACKEND_ORIGIN` pointing at the Go API slot and a `NUXT_SESSION_PASSWORD` of at least 32 random bytes. See [deploy/nuxt/README.md](deploy/nuxt/README.md) for the full environment contract, session/CSRF behavior, and the systemd and nginx templates.

### Co-packaged release

```bash
make release-ssr   # build SPA + SSR + Go, then seal → verify → stage
```

`make release-ssr` writes `release.json`, which binds the Go binary, the embedded SPA manifest, and the SSR manifest to one commit. `verify` fails if any side was built from a different checkout or if a file was swapped. Details: [deploy/nuxt/RELEASE.md](deploy/nuxt/RELEASE.md).

Version string comes from `backend/cmd/server/VERSION` (or the `v*` tag when building a tagged checkout). Do not scatter version files elsewhere.

Run without embedding UI only for local backend work:

```bash
cd backend
go run ./cmd/server
```

Frontend dev server (Vite, legacy SPA sources):

```bash
pnpm --dir frontend dev
```

Nuxt dev server (SSR + BFF):

```bash
pnpm --dir frontend exec nuxt dev
```

Copy `deploy/config.example.yaml` to `backend/config.yaml` for binary deploys. First boot without a config file opens the setup wizard at `http://<host>:8080`.

## Releases and install

GitHub Actions [release.yml](.github/workflows/release.yml) publishes assets when a semantic `v*` tag is pushed. A branch push only runs CI.

A release contains two artifacts from the same commit:

1. the Go binary with the legacy SPA embedded;
2. a Nuxt/Nitro SSR bundle, produced by [ssr-release.yml](.github/workflows/ssr-release.yml) and bound to the binary through `release.json`.

Do not mix an SSR bundle from one release with a binary from another. The contract check in `deploy/nuxt/release.mjs` rejects that.

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

Recommended production layout with the SSR frontend:

```
browser ──► reverse proxy ──► 127.0.0.1:3000  Nuxt SSR (Node/Nitro)
                                  └─ /api/v1/** ──► 127.0.0.1:8080  Go API
browser ──► gateway paths (/v1/**, WebSocket) ──► 127.0.0.1:8080  Go API
```

- Node renders pages and proxies the admin API only. It seals access and refresh tokens into an HttpOnly session cookie, so the browser payload never carries a usable token.
- Gateway traffic (`/v1/messages`, `/v1/responses`, `/v1/chat/completions`, SSE, WebSocket upgrades) stays on Go. Do not route it through Node.
- The Go binary still contains the legacy SPA. Pointing the reverse proxy back at the Go port is the page-level rollback.

Templates: [deploy/nuxt/nginx-ssr.example.conf](deploy/nuxt/nginx-ssr.example.conf), [deploy/nuxt/sub2api-ssr.service](deploy/nuxt/sub2api-ssr.service), [deploy/nuxt/ssr.env.example](deploy/nuxt/ssr.env.example).

## Production notes

- Keep the Go process on `127.0.0.1:8080` and put Nginx (or another reverse proxy) in front. Enable `underscores_in_headers on;` so sticky-session headers survive.
- Keep the Node SSR process on a loopback port (default `3000`) and let only the reverse proxy reach it.
- Live traffic process: `DATABASE_MIGRATION_MODE=validate`. Do not let the primary 8080 process apply migrations.
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
