<div align="center">

<img src="assets/logo.svg" alt="Sub2API" width="128" />

# Sub2API

[![Go](https://img.shields.io/badge/Go-1.26.10-00ADD8.svg)](https://go.dev/)
[![Nuxt UI](https://img.shields.io/badge/Nuxt_UI-4-00DC82.svg)](https://ui.nuxt.com/)
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
- **Nuxt UI 前端**：Vue 页面使用 `@nuxt/ui` 组件，运行方式为 Vite SPA。完整前端嵌入 Go 二进制，无需启动 Node、Nitro 或 BFF 服务。
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
| 前端 | Vue 3、@nuxt/ui 4、Vite SPA、TailwindCSS、pnpm |
| UI 打包 | 完整 SPA 嵌入 Go 二进制（`-tags=embed`） |
| 数据库 | PostgreSQL 15+ |
| 缓存 | Redis 7+ |

## 目录

```
sub2api/
├── backend/                 # Go 服务
│   ├── cmd/server/          # 入口和 VERSION
│   ├── internal/            # handler / service / repository
│   ├── internal/web/dist/   # 完整 SPA 构建产物与文件清单
│   ├── migrations/          # 编号 SQL
│   └── Makefile
├── frontend/                # 使用 Nuxt UI 组件的 Vite SPA
│   ├── src/                 # Vue 视图、store、路由与 API 客户端
│   └── scripts/             # 前端清单与产物校验工具
├── deploy/                  # systemd、Docker、安装与 SPA 反代模板
└── docs/                    # 支付、复合分组、运维、插件
```

## 从源码编译

需要 Go 1.26+、Node.js 24.15+（CI 用 24.22）、pnpm 9、PostgreSQL、Redis。

```bash
git clone https://github.com/kiss-kedaya/sub2api.git
cd sub2api
pnpm --dir frontend install --frozen-lockfile
```

当前只支持一套 SPA 产物。Nuxt UI 是组件库，不需要运行 Nuxt 服务。

### 嵌进 Go 二进制的 SPA

```bash
pnpm --dir frontend run build        # 输出到 backend/internal/web/dist
pnpm --dir frontend run verify:embed # 清单必须来自当前 checkout

cd backend
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags embed -o sub2api ./cmd/server
```

仓库根目录的 `make build` 会跑 SPA 构建加后端构建。这个二进制在一个端口上同时提供 API 和页面。

### 合成一个发布

```bash
make release-spa   # 构建 SPA + Go，校验清单与静态产物
```

`frontend-build.json` 记录源码提交、构建模式和每个文件的哈希。`make verify-embed` 校验来源，`make verify-spa` 检查入口、哈希资源及是否混入停用的 SSR 产物。见 [部署文档](deploy/README.md#frontend)。

版本字符串来自 `backend/cmd/server/VERSION`（打了 `v*` 标签的检出则以标签为准）。不要在别处再放一份 VERSION。

本地只跑后端、不嵌入前端：

```bash
cd backend
go run ./cmd/server
```

前端开发服务器：

```bash
pnpm --dir frontend dev
```

二进制部署把 `deploy/config.example.yaml` 拷到 `backend/config.yaml`。没有配置文件时，第一次访问 `http://<host>:8080` 会进安装向导。

## 发版与安装

GitHub Actions [release.yml](.github/workflows/release.yml) 只在推送语义化 `v*` 标签时出 Release。普通分支推送只跑 CI。

Release 压缩包包含内嵌完整 SPA 的 Go 二进制，并提供 `checksums.txt`。生产使用 GitHub Actions 的正式制品，部署前校验压缩包哈希。同一次发布工作流也会上传 `frontend-dist` Actions 产物，供反向代理从磁盘提供页面；Actions 产物有保留期限，完整内嵌 UI 则一直保存在正式发布二进制中。

反向代理直接提供磁盘 UI 时，后端和完整静态目录都要更新。先解压到新目录，校验后原子切换目录指针；旧目录和旧哈希资源保留到既有浏览器会话与回退均不再依赖。只换二进制或只换 `index.html`，可能让客户端加载到不兼容的资源。

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

生产布局：

```
浏览器 ──► 反向代理 ──► Go 服务
                         /api/**       API 接口
                         /v1/**        网关、SSE 与 WebSocket
                         /assets/**    完整的 SPA 哈希资源
                         页面路由      SPA index.html
```

- SPA 页面同源调用 Go API，不经过 BFF 会话层。
- API 和网关路由必须排在 SPA 页面兜底之前，JSON 请求不能拿到 `index.html`。
- 反向代理也可直接提供静态 SPA 目录；资源必须与发布版本对应，并在切换期间保持可读。

模板：[SPA 服务](deploy/nginx/sub2api-spa-server.conf)、[API 路由](deploy/nginx/sub2api-backend-locations.conf)、[静态 SPA 路由](deploy/nginx/sub2api-spa.conf)。

## 生产注意

- Go 进程绑 `127.0.0.1:8080`，前面用 Nginx（或其它反代）。必须打开 `underscores_in_headers on;`，否则粘性会话头会被丢掉。
- Node.js 与 pnpm 只用于构建，生产由 Go 或反向代理提供编译后的 SPA。
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
