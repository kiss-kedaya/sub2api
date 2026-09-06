<div align="center">

<img src="assets/logo.svg" alt="Sub2API" width="128" />

# Sub2API

[![Go](https://img.shields.io/badge/Go-1.26.6-00ADD8.svg)](https://go.dev/)
[![Vue](https://img.shields.io/badge/Vue-3.4+-4FC08D.svg)](https://vuejs.org/)
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
- **Gateway reliability** — WebSocket client-close is not treated as account failure; oversized passthrough can fall back to HTTP bridge; Spark 429 stays model-scoped; failed forwards release session slots immediately.
- **Ops-safe upgrades** — production `DATABASE_MIGRATION_MODE=validate` on the live process; schema changes are applied out of band. Version is a single file: `backend/cmd/server/VERSION`. GitHub Releases fire only on `v*` tags.

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
| Backend | Go 1.26.6, Gin, Ent |
| Frontend | Vue 3, Vite, TailwindCSS, pnpm |
| Database | PostgreSQL 15+ |
| Cache | Redis 7+ |

## Repository layout

```
sub2api/
├── backend/                 # Go service
│   ├── cmd/server/          # entry + VERSION
│   ├── internal/            # handlers, services, repositories
│   ├── migrations/          # numbered SQL
│   └── Makefile
├── frontend/                # Vue app (build output → backend/internal/web/dist)
├── deploy/                  # systemd, Docker, install scripts, config.example.yaml
└── docs/                    # payment, composite groups, plugins
```

## Build from source

Prerequisites: Go 1.26+, Node.js 18+, pnpm, PostgreSQL, Redis.

```bash
git clone https://github.com/kiss-kedaya/sub2api.git
cd sub2api

# Frontend (writes into backend/internal/web/dist)
pnpm --dir frontend install
pnpm --dir frontend build

# Backend with embedded UI
cd backend
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags embed -o sub2api ./cmd/server
```

Or from the repo root: `make build`.

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

GitHub Actions [release.yml](.github/workflows/release.yml) publishes assets when a `v*` tag is pushed (for example `v0.1.258`). A branch push only runs CI.

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

## Production notes

- Keep the Go process on `127.0.0.1:8080` and put Nginx (or another reverse proxy) in front. Enable `underscores_in_headers on;` so sticky-session headers survive.
- Live traffic process: `DATABASE_MIGRATION_MODE=validate`. Do not let the primary 8080 process apply migrations.
- Canary a new binary on another port (for example 8081), then switch Nginx. Never kill the primary while it still serves traffic.
- WebSocket / SSE: if an egress proxy breaks OpenAI WS, `GATEWAY_OPENAI_WS_FORCE_HTTP=true` forces upstream HTTP/SSE without changing the client protocol.

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
