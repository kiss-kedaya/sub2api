<div align="center">

<img src="assets/logo.svg" alt="Sub2API" width="128" />

# Sub2API

[![Go](https://img.shields.io/badge/Go-1.26.6-00ADD8.svg)](https://go.dev/)
[![Vue](https://img.shields.io/badge/Vue-3.4+-4FC08D.svg)](https://vuejs.org/)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-15+-336791.svg)](https://www.postgresql.org/)
[![Redis](https://img.shields.io/badge/Redis-7+-DC382D.svg)](https://redis.io/)
[![License](https://img.shields.io/badge/License-LGPL--3.0-blue.svg)](LICENSE)

**自建 AI API 网关，对接 Claude Code、Codex、Gemini CLI、Grok。**

本仓库是 [kiss-kedaya/sub2api](https://github.com/kiss-kedaya/sub2api)，基于 [Wei-Shaw/sub2api](https://github.com/Wei-Shaw/sub2api) 的维护分支。

[English](README.md) | 中文

</div>

## 这个分支多了什么

相对上游，这里按线上站点在跑的能力维护：

- **API Key 智能路由**：一把钥匙可按顺序绑定多个分组；协议对不上的账号（例如 Anthropic 路径上的 OpenAI 号）会跳过，而不是硬打。
- **内置充值**：EasyPay（含相对 `payurl`/`qrcode`）、渠道手续费和倍率、实例充值说明。见 [docs/PAYMENT_CN.md](docs/PAYMENT_CN.md)。
- **用量看板**：近 30 天；有小时汇总就走 rollup，没有再回 `usage_logs`。
- **网关可靠性**：客户端正常关 WS 不计账号故障；过大透传可走 HTTP bridge；Spark 429 只限该模型；转发失败立刻释放会话槽。
- **升级纪律**：线上主进程 `DATABASE_MIGRATION_MODE=validate`，迁移单独做。版本号只写在 `backend/cmd/server/VERSION`。GitHub Release 只在推 `v*` 标签时触发。

本部署默认关闭余额预扣（`BILLING_BALANCE_PREAUTHORIZATION_ENABLED=false`）。没有单独评审不要打开。

## 功能

- 上游账号：Anthropic OAuth / Setup Token、OpenAI API Key / OAuth、Gemini、Grok、Antigravity
- API Key、分组、粘性会话、并发与限流
- Token 级用量与计费
- 管理端 / 用户端后台
- 复合分组多厂商路由（[docs/COMPOSITE_GROUPS.md](docs/COMPOSITE_GROUPS.md)）
- 用户页可直接生成 Codex / Claude Code / Gemini CLI / Grok CLI 配置

## 技术栈

| 层 | 技术 |
|----|------|
| 后端 | Go 1.26.6、Gin、Ent |
| 前端 | Vue 3、Vite、TailwindCSS、pnpm |
| 数据库 | PostgreSQL 15+ |
| 缓存 | Redis 7+ |

## 目录

```
sub2api/
├── backend/                 # Go 服务
│   ├── cmd/server/          # 入口和 VERSION
│   ├── internal/            # handler / service / repository
│   ├── migrations/          # 编号 SQL
│   └── Makefile
├── frontend/                # Vue（构建产物写入 backend/internal/web/dist）
├── deploy/                  # systemd、Docker、安装脚本、config.example.yaml
└── docs/                    # 支付、复合分组、插件
```

## 从源码编译

需要 Go 1.26+、Node.js 18+、pnpm、PostgreSQL、Redis。

```bash
git clone https://github.com/kiss-kedaya/sub2api.git
cd sub2api

# 前端（输出到 backend/internal/web/dist）
pnpm --dir frontend install
pnpm --dir frontend build

# 后端，嵌入前端
cd backend
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags embed -o sub2api ./cmd/server
```

仓库根目录也可以 `make build`。

版本字符串来自 `backend/cmd/server/VERSION`（打了 `v*` 标签的检出则以标签为准）。不要在别处再放一份 VERSION。

本地只跑后端、不嵌入前端：

```bash
cd backend
go run ./cmd/server
```

前端开发：

```bash
pnpm --dir frontend dev
```

二进制部署把 `deploy/config.example.yaml` 拷到 `backend/config.yaml`。没有配置文件时，第一次访问 `http://<host>:8080` 会进安装向导。

## 发版与安装

[`.github/workflows/release.yml`](.github/workflows/release.yml) 只在推送 `v*` 标签时出 Release（例如 `v0.1.258`）。普通分支推送只跑 CI。

Linux 用 Release 安装：

```bash
curl -sSL https://raw.githubusercontent.com/kiss-kedaya/sub2api/main/deploy/install.sh | sudo bash
sudo systemctl enable --now sub2api
```

Docker：

```bash
mkdir -p sub2api-deploy && cd sub2api-deploy
curl -sSL https://raw.githubusercontent.com/kiss-kedaya/sub2api/main/deploy/docker-deploy.sh | bash
docker compose up -d
```

细节见 [deploy/README.md](deploy/README.md)、[deploy/DOCKER.md](deploy/DOCKER.md)。

## 生产注意

- Go 进程绑 `127.0.0.1:8080`，前面用 Nginx（或其它反代）。必须打开 `underscores_in_headers on;`，否则粘性会话头会被丢掉。
- 承接流量的进程用 `DATABASE_MIGRATION_MODE=validate`，不要让 8080 主进程自己 apply 迁移。
- 新二进制先挂 canary 端口（例如 8081），Nginx 切流量；主进程还在接流量时不要杀它。
- 出口代理把 OpenAI WS 搞坏时，可设 `GATEWAY_OPENAI_WS_FORCE_HTTP=true`，上游改 HTTP/SSE，客户端协议不变。

## Nginx

```nginx
underscores_in_headers on;
```

## 简单模式

`RUN_MODE=simple` 会藏掉 SaaS 计费界面。生产环境还要设 `SIMPLE_MODE_CONFIRM=true`。

## 许可证

[GNU Lesser General Public License v3.0](LICENSE)（或更新版本）。

上游版权仍属于原 Sub2API 作者。本分支不额外授予商业授权。
