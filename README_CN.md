<div align="center">

<img src="assets/logo.svg" alt="Sub2API" width="128" />

# Sub2API

[![Go](https://img.shields.io/badge/Go-1.26.10-00ADD8.svg)](https://go.dev/)
[![Nuxt](https://img.shields.io/badge/Nuxt-4-00DC82.svg)](https://nuxt.com/)
[![Vue](https://img.shields.io/badge/Vue-3.5+-4FC08D.svg)](https://vuejs.org/)
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
- **诊断与运维**：连接时间线、限时请求采集、缓存测试、流量统计、账号通知、渠道下钻和独立代理管理；自动化与专项通道按需开启。见 [运维能力与配置边界](docs/operator-capabilities.md)。
- **网关可靠性**：客户端正常关 WS 不计账号故障；过大透传可走 HTTP bridge；Spark 429 只限该模型；转发失败立刻释放会话槽。
- **Nuxt 4 SSR 前端**：页面服务端渲染，另加一层轻量 BFF，把访问令牌挡在浏览器 payload 之外。旧的 Vite SPA 仍嵌在 Go 二进制里，作为回滚入口。
- **升级纪律**：线上主进程 `DATABASE_MIGRATION_MODE=validate`，迁移单独做。版本号只写在 `backend/cmd/server/VERSION`。GitHub Release 只在推语义化 `v*` 标签时触发。

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
| 后端 | Go 1.26.10、Gin、Ent |
| 前端 | Nuxt 4（SSR，Nitro `node-server`）、Vue 3、@nuxt/ui、TailwindCSS、pnpm |
| 前端回滚 | Vite SPA，嵌在 Go 二进制里（`-tags=embed`） |
| 数据库 | PostgreSQL 15+ |
| 缓存 | Redis 7+ |

## 目录

```
sub2api/
├── backend/                 # Go 服务
│   ├── cmd/server/          # 入口和 VERSION
│   ├── internal/            # handler / service / repository
│   ├── internal/web/dist/   # 旧版 Vite SPA 构建产物（嵌入用）
│   ├── migrations/          # 编号 SQL
│   └── Makefile
├── frontend/                # Nuxt 4 应用（SSR + BFF）与旧版 SPA 源码
│   ├── app/                 # Nuxt 页面、插件、运行期上下文
│   ├── server/              # Nitro 路由：/api/v1 代理、会话、健康检查
│   ├── src/                 # 共用的 Vue 视图、store、API 客户端
│   └── .output/             # Nuxt/Nitro 构建产物（生成）
├── deploy/                  # systemd、Docker、安装脚本、nuxt SSR 模板
└── docs/                    # 支付、复合分组、运维、插件
```

## 从源码编译

需要 Go 1.26+、Node.js 24.15+（CI 用 24.22）、pnpm 9、PostgreSQL、Redis。

```bash
git clone https://github.com/kiss-kedaya/sub2api.git
cd sub2api
pnpm --dir frontend install --frozen-lockfile
```

前端有**两套产物**。完整发版两套都带，且必须来自同一个 commit。

### 1. 嵌进 Go 二进制的旧版 SPA

```bash
pnpm --dir frontend run build        # 输出到 backend/internal/web/dist
pnpm --dir frontend run verify:embed # 清单必须来自当前 checkout

cd backend
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags embed -o sub2api ./cmd/server
```

仓库根目录的 `make build` 会跑 SPA 构建加后端构建。这个二进制在一个端口上同时提供 API 和回滚页面。

### 2. 交给 Node 的 Nuxt SSR 产物

```bash
pnpm --dir frontend run build:ssr    # 输出到 frontend/.output（Nitro node-server）
pnpm --dir frontend run verify:ssr
node frontend/.output/server/index.mjs
```

SSR 进程需要 `NUXT_BACKEND_ORIGIN` 指向 Go API 的槽位，并配置至少 32 字节随机串的 `NUXT_SESSION_PASSWORD`。完整环境变量、会话/CSRF 契约、systemd 与 nginx 模板见 [deploy/nuxt/README.md](deploy/nuxt/README.md)。

### 合成一个发布

```bash
make release-ssr   # 构建 SPA + SSR + Go，再 seal → verify → stage
```

`make release-ssr` 会写 `release.json`，把 Go 二进制、内嵌 SPA 清单、SSR 清单绑到同一个 commit。任意一方来自别的 checkout 或文件被换过，`verify` 都会失败。细节见 [deploy/nuxt/RELEASE.md](deploy/nuxt/RELEASE.md)。

版本字符串来自 `backend/cmd/server/VERSION`（打了 `v*` 标签的检出则以标签为准）。不要在别处再放一份 VERSION。

本地只跑后端、不嵌入前端：

```bash
cd backend
go run ./cmd/server
```

前端开发服务器（Vite，旧版 SPA 源码）：

```bash
pnpm --dir frontend dev
```

Nuxt 开发服务器（SSR + BFF）：

```bash
pnpm --dir frontend exec nuxt dev
```

二进制部署把 `deploy/config.example.yaml` 拷到 `backend/config.yaml`。没有配置文件时，第一次访问 `http://<host>:8080` 会进安装向导。

## 发版与安装

GitHub Actions [release.yml](.github/workflows/release.yml) 只在推送语义化 `v*` 标签时出 Release。普通分支推送只跑 CI。

一个 Release 包含**同一 commit 的两个制品**：

1. 内嵌旧版 SPA 的 Go 二进制；
2. Nuxt/Nitro SSR 产物，由 [ssr-release.yml](.github/workflows/ssr-release.yml) 构建，并通过 `release.json` 与二进制绑定。

不要把一版的 SSR 产物和另一版的二进制混用，`deploy/nuxt/release.mjs` 的契约校验会拒绝。

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

## 部署拓扑

接入 SSR 前端后的推荐生产布局：

```
浏览器 ──► 反向代理 ──► 127.0.0.1:3000  Nuxt SSR（Node/Nitro）
                            └─ /api/v1/** ──► 127.0.0.1:8080  Go API
浏览器 ──► 网关路径（/v1/**、WebSocket）──► 127.0.0.1:8080  Go API
```

- Node 只负责页面渲染和管理 API 代理。它把 access/refresh token 封进 HttpOnly 会话 cookie，浏览器 payload 里不会出现可用令牌。
- 网关流量（`/v1/messages`、`/v1/responses`、`/v1/chat/completions`、SSE、WebSocket 升级）继续直连 Go，不要绕到 Node。
- Go 二进制里仍有旧版 SPA。把反向代理指回 Go 端口，就是页面级回滚。

模板：[deploy/nuxt/nginx-ssr.example.conf](deploy/nuxt/nginx-ssr.example.conf)、[deploy/nuxt/sub2api-ssr.service](deploy/nuxt/sub2api-ssr.service)、[deploy/nuxt/ssr.env.example](deploy/nuxt/ssr.env.example)。

## 生产注意

- Go 进程绑 `127.0.0.1:8080`，前面用 Nginx（或其它反代）。必须打开 `underscores_in_headers on;`，否则粘性会话头会被丢掉。
- Node SSR 进程只监听回环端口（默认 `3000`），只让反向代理访问。
- 承接流量的进程用 `DATABASE_MIGRATION_MODE=validate`，不要让 8080 主进程自己 apply 迁移。
- 新二进制先挂 canary 端口（例如 8081），Nginx 切流量；主进程还在接流量时不要杀它。
- 出口代理把 OpenAI WS 搞坏时，可设 `GATEWAY_OPENAI_WS_FORCE_HTTP=true`，上游改 HTTP/SSE，客户端协议不变。
- 日志、指标和错误页不得出现上游凭据、内部主机名或完整供应商地址。

## Nginx

```nginx
underscores_in_headers on;
```

## 简单模式

`RUN_MODE=simple` 会藏掉 SaaS 计费界面。生产环境还要设 `SIMPLE_MODE_CONFIRM=true`。

## 许可证

[GNU Lesser General Public License v3.0](LICENSE)（或更新版本）。

上游版权仍属于原 Sub2API 作者。本分支不额外授予商业授权。
