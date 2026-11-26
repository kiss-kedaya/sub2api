# sub2api 项目开发指南

> 本文档记录项目环境配置、常见坑点、仓库结构和变更规范，供 Claude Code 和团队成员参考。
> 术语速查见 [GLOSSARY.md](GLOSSARY.md)。仓库是**公开**的：部署与运维记录不在本仓库，本文不写任何服务器、凭据和运维拓扑。
> 第六～十一章是维护指南：仓库地图、请求链路、测试与 CI、变更规范、「改 X 要改哪几处」清单、工程规则。

## 一、项目基本信息

| 项目 | 说明 |
|------|------|
| **上游仓库** | Wei-Shaw/sub2api |
| **Fork 仓库** | kiss-kedaya/sub2api（origin）；upstream 为 Wei-Shaw/sub2api |
| **技术栈** | Go 后端 (Ent ORM + Gin + Wire) + Vue 3 SPA 前端 (Vite，Nuxt UI 只当组件库，pnpm；产物嵌入二进制，不跑 Nuxt/Node 服务) |
| **数据库** | PostgreSQL（README 口径 15+）+ Redis（README 口径 7+）；集成测试容器用 PostgreSQL 18、Redis 8.4，CI 另有 15/16 的作业 |
| **包管理** | 后端: go modules, 前端: **pnpm**（不是 npm） |

## 二、本地环境配置（Windows 开发机示例）

### PostgreSQL 16 (Windows 服务)

| 配置项 | 值 |
|--------|-----|
| 端口 | 5432 |
| psql 路径 | `C:\Program Files\PostgreSQL\16\bin\psql.exe` |
| pg_hba.conf | `C:\Program Files\PostgreSQL\16\data\pg_hba.conf` |
| 数据库凭据 | 本地开发默认 user=`sub2api`、dbname=`sub2api`；密码写在本地 `.env` 或 shell 环境里，不要提交到仓库 |
| 超级用户 | user=`postgres`；密码由本地安装时设置，不要提交到仓库 |

### Redis

| 配置项 | 值 |
|--------|-----|
| 端口 | 6379 |
| 密码 | 无 |

### 开发工具

```bash
# golangci-lint（CI 用 v2.13，本地装同一版以免版本差异带来的噪音；scripts/prepush.sh 提示的也是这个版本）
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.0

# pnpm (前端包管理；Dockerfile 与 package.json 钉的是 9.15.5，CI 用 pnpm 9)
npm install -g pnpm
```

前端要求 Node >= 24.15（`frontend/package.json` 的 `engines`；CI 用 24.22）。

## 三、CI/CD 流水线

### GitHub Actions Workflows

每个工作流一行；`backend-ci.yml` 里各作业守什么、怎么在本地复现，见第八章。

| Workflow | 触发条件 | 做什么 |
|----------|----------|--------|
| **backend-ci.yml**（名称 `CI`） | push 到 main、pull_request、手动 | Go 单测 / 集成 / race 分片 / golangci-lint、前端校验与构建、嵌入式二进制构建 |
| **security-scan.yml**（`Security Scan`） | push、pull_request、每周一 03:00 UTC、手动 | govulncheck + 模块级漏洞清单 + pnpm audit（含豁免清单校验） |
| **sensitive-check.yml**（`Sensitive Data Check`） | push、pull_request、手动 | 公开仓库敏感信息扫描（`tools/check_sensitive.py --all` + 新提交元数据） |
| **release.yml**（`Release`） | 推送 `v*` tag、手动 | 构建前端、GoReleaser 出制品与 `checksums.txt`、同步 VERSION（PR 不触发） |
| **navigation-ci.yml**（`Navigation CI`） | 改了导航相关前端路径的 PR / main 推送 | 导航回归 vitest |
| **release-notes.yml**（`Release Notes`） | 手动 | 生成发版说明并上传 artifact（只读，不发布） |
| **orm-codegen.yml**（`ORM code generation`） | 推送 `feat/ops-capabilities` 分支且改了 `backend/ent/schema/**` 或本工作流、手动 | 从 schema 生成 Ent 代码并上传 artifact |
| **crypto-test-release.yml**（`Crypto Test Release`） | 推送 `feat/crypto-deposit` 分支（限定路径）、手动 | 打隔离的测试包并发 prerelease，不部署、不同步 VERSION |
| **cla.yml**（`CLA Assistant`） | PR；只在 `Wei-Shaw/sub2api` 仓库生效 | 贡献者协议签署 |

### CI 要求

- Go 版本由 `backend/go.mod` 指定；CI 里的版本断言（`go version | grep -q 'go1.26.11'`，在 `backend-ci.yml`、`release.yml`、`security-scan.yml`）、三个 Dockerfile（`Dockerfile`、`backend/Dockerfile`、`deploy/Dockerfile`）的 Go 构建镜像和 README 的 Go 徽章也必须同步更新。当前锁定 **1.26.11**，用于包含 `net/http` 安全修复（GO-2026-6859）。
- 前端使用 `pnpm install --frozen-lockfile`，必须提交 `pnpm-lock.yaml`

### 本地测试命令

```bash
# 后端单元测试（在 backend/ 下；= make test-unit）
cd backend && go test -tags=unit ./...

# 后端集成测试（= make test-integration）；需要 Docker，或用 SUB2API_TEST_POSTGRES_DSN 指向现成 PostgreSQL
cd backend && go test -tags=integration ./...

# 代码质量检查
cd backend && golangci-lint run ./...

# 前端依赖安装（必须用 pnpm）
pnpm --dir frontend install --frozen-lockfile

# 前端构建（输出到 backend/internal/web/dist，随后嵌进 Go 二进制）并校验清单
pnpm --dir frontend run build
pnpm --dir frontend run verify:embed
```

## 四、常见坑点 & 解决方案

### 坑 1：pnpm-lock.yaml 必须同步提交

**问题**：`package.json` 新增依赖后，CI 的 `pnpm install --frozen-lockfile` 失败。

**原因**：上游 CI 使用 pnpm，lock 文件不同步会报错。

**解决**：
```bash
cd frontend
pnpm install  # 更新 pnpm-lock.yaml
git add pnpm-lock.yaml
git commit -m "chore: update pnpm-lock.yaml"
```

---

### 坑 2：npm 和 pnpm 的 node_modules 冲突

**问题**：之前用 npm 装过 `node_modules`，pnpm install 报 `EPERM` 错误。

**解决**：
```bash
cd frontend
rm -rf node_modules  # 或 PowerShell: Remove-Item -Recurse -Force node_modules
pnpm install
```

---

### 坑 3：PowerShell 中 bcrypt hash 的 `$` 被转义

**问题**：bcrypt hash 格式如 `$2a$10$xxx...`，PowerShell 把 `$2a` 当变量解析，导致数据丢失。

**解决**：将 SQL 写入文件，用 `psql -f` 执行：
```bash
# 错误示范（PowerShell 会吃掉 $）
psql -c "INSERT INTO users ... VALUES ('$2a$10$...')"

# 正确做法
echo "INSERT INTO users ... VALUES ('\$2a\$10\$...')" > temp.sql
psql -U sub2api -h 127.0.0.1 -d sub2api -f temp.sql
```

---

### 坑 4：psql 不支持中文路径

**问题**：`psql -f "D:\中文路径\file.sql"` 报错找不到文件。

**解决**：复制到纯英文路径再执行：
```bash
cp "D:\中文路径\file.sql" "C:\temp.sql"
psql -f "C:\temp.sql"
```

---

### 坑 5：PostgreSQL 密码重置流程

**场景**：忘记 PostgreSQL 密码。

**步骤**：
1. 修改 `C:\Program Files\PostgreSQL\16\data\pg_hba.conf`
   ```
   # 将 scram-sha-256 改为 trust
   host    all    all    127.0.0.1/32    trust
   ```
2. 重启 PostgreSQL 服务
   ```powershell
   Restart-Service postgresql-x64-16
   ```
3. 无密码登录并重置
   ```bash
   psql -U postgres -h 127.0.0.1
   ALTER USER sub2api WITH PASSWORD '<new-local-password>';
   ALTER USER postgres WITH PASSWORD '<new-local-password>';
   ```
4. 改回 `scram-sha-256` 并重启

---

### 坑 6：Go interface 新增方法后 test stub 必须补全

**问题**：给 interface 新增方法后，编译报错 `does not implement interface (missing method XXX)`。

**原因**：所有测试文件中实现该 interface 的 stub/mock 都必须补上新方法；`internal/testutil` 里的共享桩还带编译期断言（`var _ service.X = Stub{}`）。

**解决**：
```bash
# 搜索所有实现该 interface 的 struct
cd backend
grep -r "type.*Stub.*struct" internal/
grep -r "type.*Mock.*struct" internal/

# 逐一补全新方法
```

写新桩时优先「嵌入接口、只实现用到的方法」（例如 `type fakeRepo struct { service.UsageLogRepository }`），以后接口再加方法就不用逐个补。

---

### 坑 7：Windows 上 psql 连 localhost 的 IPv6 问题

**问题**：psql 连 `localhost` 先尝试 IPv6 (::1)，可能报错后再回退 IPv4。

**建议**：直接用 `127.0.0.1` 代替 `localhost`。

---

### 坑 8：Windows 没有 make 命令

**问题**：CI 里用 `make test-unit`（在 `backend/` 下）和根目录的 `make test-frontend`，本地 Windows 没有 make。

**解决**：直接用 Makefile 里的原始命令：
```bash
# 代替 make test-unit
go test -tags=unit ./...

# 代替 make test-integration
go test -tags=integration ./...

# 代替 make test-frontend：逐行执行根 Makefile 里该目标下的 pnpm 命令
```

---

### 坑 9：Ent Schema 修改后必须重新生成

**问题**：修改 `ent/schema/*.go` 后，代码不生效。

**解决**：
```bash
cd backend
go generate ./ent  # 重新生成 ent 代码（保持 json.RawMessage，生产构建不依赖 jsonv2 实验特性）
git add ent/       # 生成的文件也要提交
```

注意：Ent 只负责生成客户端代码，**数据库表结构以 `backend/migrations/*.sql` 为准**（见第九章）。

---

### 坑 10：前端测试看似正常，但后端调用失败（模型映射被批量误改）

**典型现象**：
- 前端按钮点测看起来正常；
- 实际通过 API/客户端调用时返回 `Service temporarily unavailable` 或提示无可用账号；
- 常见于 OpenAI 账号（例如 Codex 模型）在批量修改后突然不可用。

**根因**：
- OpenAI 账号编辑页默认不显式展示映射规则，容易让人误以为“没映射也没关系”；
- 但在**批量修改同时选中不同平台账号**（OpenAI + Antigravity/Gemini）时，模型白名单/映射可能被跨平台策略覆盖；
- 结果是 OpenAI 账号的关键模型映射丢失或被改坏，后端选不到可用账号。

**修复方案（按优先级）**：
1. **快速修复（推荐）**：在批量修改中补回正确的透传映射（例如 `gpt-5.3-codex -> gpt-5.3-codex-spark`）。
2. **彻底重建**：删除并重新添加全部相关账号（最稳但成本高）。

**关键经验**：
- 如果某模型已被软件内置默认映射覆盖，通常不需要额外再加透传；
- 但当上游模型更新快于本仓库默认映射时，**手动批量添加透传映射**是最简单、最低风险的临时兜底方案；
- 批量操作前尽量按平台分组，不要混选不同平台账号。

---

### 坑 11：PR 提交前检查清单

提交 PR 前务必本地验证：

- [ ] `python tools/check_sensitive.py --all` 通过（公开仓库，CI 的 `Sensitive Data Check` 会再扫一遍）
- [ ] `go test -tags=unit ./...` 通过（Windows 上的已知天然失败见 8.4）
- [ ] `go test -tags=integration ./...` 通过（改了仓储/迁移/SQL 时必跑）
- [ ] `golangci-lint run ./...` 无新增问题（CI 用 v2.13，以 CI 为准）；懒得全跑就 `bash scripts/prepush.sh`
- [ ] `pnpm-lock.yaml` 已同步（如果改了 package.json）
- [ ] 所有 test stub 补全新接口方法（如果改了 interface）
- [ ] Ent 生成的代码已提交（如果改了 schema）；Wire 生成物已更新（如果改了依赖装配，见第十章）
- [ ] 改了前端时，`pnpm run build` 通过（含 i18n 校验），`verify:embed` 与 `make verify-spa` 无报错
- [ ] 新增的前端 spec 已登记进 CI 清单（见 8.5），新增迁移/配置项/路由按第十章清单对过一遍

## 五、常用命令速查

### 数据库操作

```bash
# 连接数据库
psql -U sub2api -h 127.0.0.1 -d sub2api

# 查看所有用户
psql -U postgres -h 127.0.0.1 -c "\du"

# 查看所有数据库
psql -U postgres -h 127.0.0.1 -c "\l"

# 执行 SQL 文件
psql -U sub2api -h 127.0.0.1 -d sub2api -f migration.sql
```

### Git 操作

```bash
# 同步上游
git fetch upstream
git checkout main
git merge upstream/main
git push origin main

# 创建功能分支（分支命名见 9.1）
git checkout -b feature/xxx

# Rebase 到最新 main
git fetch upstream
git rebase upstream/main

# 首次 clone 后启用提交钩子（敏感信息扫描，只做一次）
git config core.hooksPath .githooks
```

### 前端操作

```bash
# 安装依赖（必须用 pnpm）
cd frontend
pnpm install

# 开发服务器
pnpm dev

# SPA 构建（输出到 backend/internal/web/dist，随后嵌入 Go 二进制）
pnpm build
pnpm run verify:embed

# 契约校验 + Go 二进制
cd ..
make verify-spa
make release-spa
```

### 后端操作

```bash
# 运行服务器
cd backend
go run ./cmd/server/

# 生成 Ent 代码
go generate ./ent

# 运行测试
go test -tags=unit ./...
go test -tags=integration ./...

# Lint 检查
golangci-lint run ./...
```

## 六、仓库地图

> 这章解决什么问题：想改某个功能时，先知道它住在哪个目录、边界在哪。

### 6.1 顶层目录

| 路径 | 作用 |
|------|------|
| `backend/` | Go 服务（`go.mod` 在这里，module `github.com/Wei-Shaw/sub2api`） |
| `frontend/` | Vue 3 SPA（Vite 构建，产物输出到 `backend/internal/web/dist`） |
| `deploy/` | 自建部署资源：compose 文件、`config.example.yaml`、`.env.example`、systemd unit、安装脚本、反向代理示例、`spa/`（SPA 产物契约校验）、`tests/`（部署脚本的 shell 测试）、`copilot-sdk/`（Python 适配器 sidecar）、各类 `*.md` 部署说明 |
| `docs/` | 产品/集成文档与发版说明；`docs/*` 默认被 `.gitignore` 忽略，只有白名单的才进库（见 9.8） |
| `scripts/` | 仓库级脚本：`prepush.sh`（推送前检查）、`release/gen-release-notes.sh`（生成发版说明）、`verify-embedded-assets.sh`（探测运行实例的嵌入资源是否完整）。根 `scripts/` 被 `.gitignore` 忽略，新增要 `git add -f` |
| `tools/` | CI 与钩子用的 Python 工具：`check_sensitive.py`（敏感信息扫描）、`check_govulncheck_modules.py`、`check_pnpm_audit_exceptions.py` 及其单测 |
| `catalog/` | `models.json` 首方模型目录：运行中的服务按固定间隔从 main 上拉取这份文件，**合入 main 即生效、不需要发版**，评审时按线上变更对待（说明见 `catalog/README.md`） |
| `openspec/` | spec-driven 变更提案（`changes/<name>/` 下 proposal / design / tasks / specs），较大的特性先写提案 |
| `skills/` | `sub2api-admin` 技能：用管理 API Key 调后台接口的 CLI 与说明 |
| `assets/` | logo、合作方图片 |
| `.githooks/` | `pre-commit`（敏感信息扫描）、`commit-msg`（提交信息扫描）；`git config core.hooksPath .githooks` 启用 |
| `.github/` | `workflows/`、CI 辅助脚本 `ci/`、复合 action `actions/go-cache`、`audit-exceptions.yml`（前端依赖漏洞豁免）、`privacy-fixture-allowlist.json`（敏感扫描的夹具白名单） |
| 根文件 | `Makefile`（前端与发布构建）、`Dockerfile`、`Dockerfile.goreleaser`、`.goreleaser*.yaml`、`CONTRIBUTING.md`、`CLA.md`、`README*.md` |

### 6.2 backend/

| 路径 | 作用 |
|------|------|
| `cmd/server/` | 主程序入口 `main.go`、`VERSION` 文件、Wire 装配（`wire.go` 为输入、`wire_gen.go` 为生成物）、命令行子命令（`-version`、`-migrate-only`、`-check-migrations`、`-atrest-migrate` 等） |
| `cmd/<其它>/` | 辅助命令：`auth-subject-backfill`、`cleanup-ingress-reject-logs`、`dev-hash`、`jwtgen`、`models-pricing-preview`、`profit-preview`、`watchdog`；GoReleaser 只构建 `./cmd/server`，它们不随发布制品打包 |
| `ent/` | Ent ORM：`schema/` 是手写的，其余是 `go generate ./ent` 生成的（提交进库） |
| `migrations/` | 编号 SQL 迁移，由 `migrations.FS` 嵌入二进制；`manual/` 下是手动执行的脚本（不嵌入）；`*_migration_test.go` 是迁移的文本断言测试 |
| `internal/` | 业务代码，见 6.3 |
| `pkg/pluginapi/` | 对外公开的本地插件协议（proto、清单 schema、文档） |
| `resources/model-pricing/` | 价格表兜底副本 |
| `scripts/` | 后端脚本：`resolve-version.sh`（Makefile 取版本号）等 |
| `.golangci.yml` | lint 配置，含分层规则（见 6.3） |
| `local-dev.ps1`、`start-pg.ps1`、`start-watchdog.ps1` | Windows 本机起便携 PostgreSQL 与服务的脚本 |

### 6.3 backend/internal

分层规则写在 `backend/.golangci.yml` 的 depguard 里：**`handler` 和 `service` 不许 import `repository`、`gorm`、`go-redis`**（个别文件在白名单里）。依赖方向是 `handler → service ← repository`：`service` 定义接口，`repository` 实现，`cmd/server` 用 Wire 装配。

| 包 | 作用 |
|----|------|
| `handler/` | HTTP handler（用户端、网关、认证、支付等）；`admin/` 是管理端 handler；`dto/` 是响应 DTO 与映射；`quotaview/`、`trafficquery/` 是共享的小块 |
| `service/` | 业务逻辑与领域服务，最大的包：网关转发与调度、计费、设置、各平台适配…；子包 `basispoints/`、`openai_ws_v2/`、`prompts/` |
| `repository/` | 数据访问：Ent/SQL、Redis 缓存实现、outbox、迁移执行器（`migrations_runner.go`） |
| `server/` | `http.go` 构造 HTTP server；`router.go` 注册全局中间件；`routes/` 按模块注册路由；`middleware/` 是各类中间件（API Key 鉴权、JWT、step-up、审计、限速、路径别名…） |
| `middleware/` | 共享的 Redis 限流器（`rate_limiter.go`），被 `server/middleware` 使用 |
| `config/` | 配置加载（viper）、默认值、校验 |
| `domain/` | 跨层共享的常量与小型值对象：状态、角色、平台、模型白名单、reasoning effort 等 |
| `model/` | `service` 层用的纯数据模型 |
| `web/` | 嵌入前端：`embed_on.go`（`-tags embed`）与 `embed_off.go`（桩）、首页设置注入与缓存、静态文件缓存、画布代理 |
| `setup/` | 首次安装向导（CLI 与 Web） |
| `payment/` | 支付抽象（`types.go` 的 `Provider` 接口）、注册表、负载均衡、费率；`provider/` 是各服务商实现（easypay、alipay、wxpay、stripe、airwallex、epusdt、crypto） |
| `cryptopay/` | 内置加密货币充值：`addresses/`（HD 地址池）、`rpc/`（轮转 JSON-RPC 客户端池）、`scanner/`（链上扫描）、`ledger/`（入账）、`sweeper/`（归集）、`wallet/`（内置加密钱包，`shards/` 是 2-of-3 分片）、`explorer/`、`nativeprice/`、`notify/`、`mode/` |
| `notify/` | 通知通道（含 `bark/`）与通知枢纽适配 |
| `securityaudit/` | Prompt 审计与惩罚判定 |
| `iprisk/` | 可信来源 IP 准入与认证风险控制 |
| `requestcapture/` | 可选的请求/响应采集（默认关闭） |
| `accounttimer/` | 账号定时可调度 |
| `serverless/` | 把已认证请求路由到注册的 Pod（区域网关） |
| `chanshui/` | 质检审计客户端 |
| `mihomo/` | 受管代理（mihomo）进程管理 |
| `localreasoning/` | 本地质检题的生成与判分 |
| `atrestmigrate/` | 敏感列的 at-rest 加密迁移（`-atrest-migrate`） |
| `platform/liveattestation/` | Live 接口需要的设备证明（仅 macOS） |
| `testutil/` | 单测共享的 stub、fixture、miniredis 入口 |
| `integration/` | e2e 测试（`e2e` 标签） |
| `util/` | `clientprivacy/`（对外错误脱敏）、`httputil/`、`logredact/`、`responseheaders/`、`textsanitize/`、`transportdiag/`、`urlvalidator/`（SSRF / 内网地址校验） |
| `pkg/` | 无业务依赖的共享库：协议适配（`apicompat`、`claude`、`anthropicfp`、`openai`、`openai_compat`、`gemini`、`geminicli`、`googleapi`、`antigravity`、`xai`、`typesafe`、`websearch`）；请求工具（`httpclient`、`httputil`、`requestmodel`、`urljoin`、`proxyurl`、`proxyutil`、`tlsfingerprint`）；横切（`logger`、`errors`、`response`、`ctxkey`、`pagination`、`timezone`、`timewindow`、`ip`、`ipnotice`、`ipquality`、`requesttiming`、`servertiming`）；安全（`adminaccess`、`adminroutepolicy`、`atrest`、`authsubject`、`redissession`、`oauth`）；其它（`usagestats`、`sysutil`、`ssefixture`） |

### 6.4 frontend/

| 路径 | 作用 |
|------|------|
| `src/api/` | axios 客户端（`client.ts`，401 刷新与 step-up 弹窗都在响应拦截器里）与按模块封装的请求函数；`admin/` 是管理端接口 |
| `src/views/` | 页面：`admin/`、`user/`、`auth/`、`public/`、`setup/` 与顶层页面 |
| `src/components/` | 可复用组件，按领域分目录（`account/`、`admin/`、`keys/`、`crypto/`、`layout/`、`common/`…） |
| `src/router/` | 路由表、守卫、简易模式、页面标题 |
| `src/stores/` | Pinia stores |
| `src/composables/`、`src/utils/`、`src/types/` | 组合式函数、工具函数、类型 |
| `src/i18n/` | `locales/en` 与 `locales/zh`，两边的键必须一致（见 9.7） |
| `src/data/` | `apiDocs.ts`：站内「API 文档」页的数据 |
| `src/constants/` | 平台、账号、渠道等常量（如 `platforms.ts`） |
| `src/features/` | 按功能切片的模块（`channel-monitor-v2`、`ip-audit`、`ip-risk`、`mihomo`、`prompt-audit`） |
| `src/legal/` | 法律页目录；构建时读取 `docs/legal/*.md` |
| `src/theme/`、`src/styles/`、`src/assets/` | 主题、样式、静态资源 |
| `src/runtime/`、`src/plugins/` | 应用运行期上下文、Nuxt UI 插件注册 |
| `dev/` | 本地控制台预览（mock API）及其 spec，有独立的 `vitest.config.ts` / `tsconfig.json` |
| `scripts/` | `build-manifest.mjs`（构建清单 emit / verify）、`stepup-call-sites.mjs`（step-up 调用点扫描） |

## 七、请求链路与关键概念

> 这章解决什么问题：一次 API 请求从进来到计费落库经过哪些环节，出问题时该看哪个文件。术语见 [GLOSSARY.md](GLOSSARY.md)。

### 7.1 一次网关请求（以 `POST /v1/messages` 为例）

| 环节 | 做什么 | 看哪个文件 |
|------|--------|-----------|
| 入口 | 全局请求体上限 → 路径别名（gin 路由前：折叠重复的 `/v1`、补漏写的 `/v1`）→ gin | `server/http.go`、`server/middleware/gateway_path_alias.go` |
| 全局中间件 | Recovery → 区域网关入口 → IP 风险 → 请求计时 → 请求日志 → 会话绑定上下文 → CORS → 安全头 → 蜜罐 → 嵌入前端；顺序以代码为准 | `server/router.go`（`SetupRouter`）、`server/http.go`（`ProvideRouter`） |
| `/v1` 路由链 | bodyLimit → clientRequestID → opsErrorLogger → endpointNorm → **API Key 鉴权** → 区域路由 → 请求采集 → ipSharing → ipNotice →（Key 自助接口在这里注册）→ 分组模型白名单 → 重复字段拒绝 → 复合路由解析 → 分组校验 → handler。`/v1beta`（Gemini）、`/antigravity/*`、根路径别名、`/backend-api/codex`、`/api/v3` 是同构的变体链 | `server/routes/gateway.go`（`RegisterGatewayRoutes`） |
| API Key 鉴权 | 从 `Authorization: Bearer` / `x-api-key` / `x-goog-api-key` 取 key（query 里的 `key` / `api_key` 直接拒）→ `GetByKey`（认证快照：L1 → Redis L2 → singleflight 回源 PG）→ Key / 用户 / 分组状态、IP 黑白名单 → 计费闸（过期、额度、订阅、余额）→ 写入 context。Key 自助接口只做鉴权、跳过计费闸 | `server/middleware/api_key_auth.go`、`service/api_key_auth_cache*.go` |
| handler 预处理 | 读请求体一次 → `ParseGatewayRequest` → 参数校验与策略 → 渠道级模型映射 → 安全审计 → 用户并发槽 → 计费资格检查 →（预扣，默认关）→ 在途余额预留 | `handler/gateway_handler.go`（`Messages`）；其它协议同构：`gateway_handler_responses.go`、`gateway_handler_chat_completions.go`、`openai_gateway_handler.go`、`gemini_v1beta_handler.go` |
| 选号 | session hash → 粘性会话 → 按分组 / 平台 / 模型 / 优先级 / 负载 / 排除集选账号；智能路由按综合评分与会话粘组排定候选组顺序；读调度快照，不查 PG | `service/gateway_scheduling.go`、`group_route_plan.go`、`gateway_key_routes.go`、`scheduler_snapshot_service.go`、`openai_account_scheduler.go` |
| failover 循环 | 拿账号并发槽 → 转发 → 失败则同账号重试（池模式）或排除该账号换号；成功、候选耗尽或客户端断开才出循环；SSE 已开始写后不再切号 | `handler/failover_loop.go` |
| 转发 | 构造上游请求、重试（`maxRetryAttempts = 5`、总耗时 `maxRetryElapsed = 10s`）、流式转发、读取 usage；每个平台一套 `*_gateway_*.go` | `service/gateway_forward.go`、`openai_gateway_forward.go` |
| 用量与计费 | handler 把任务提交到有界 worker 池（队列满时同步执行，不丢）→ `RecordUsage` 算费 → `UsageBillingRepository.Apply`（PG 事务里扣余额 / 订阅 / Key 配额，按 request id 去重）→ 同步余额缓存、发通知 → 批量写 `usage_logs` | `handler/gateway_handler.go`（`submitUsageRecordTask`）、`service/usage_record_worker_pool.go`、`service/gateway_usage_billing.go`、`service/billing_service.go`、`repository/usage_log_repo*.go` |

### 7.2 Redis 与 outbox 的角色

Redis 只放可重建的状态：缓存、计数、凭证窗口；账号、用户、用量的真相在 PostgreSQL。会让 Redis 或下游变化的数据库变更走 outbox：先写 PG 的事件表，再由后台 worker 消费、更新 Redis 或下游。

| 用途 | 实现 |
|------|------|
| 调度快照（`sched:*` 前缀，带版本与世代） | `repository/scheduler_cache.go`；事件来自 `repository/scheduler_outbox_repo.go`，`service/scheduler_snapshot_service.go` 消费并重建 |
| 认证快照 L2（`apikey:auth:v<版本>:<hash>`）与失效广播 | `repository/api_key_cache.go`、`service/api_key_auth_cache_impl.go`、`repository/auth_cache_invalidation_outbox_repo.go` |
| 用户 / 账号并发槽与等待计数 | `repository/concurrency_cache.go` |
| 粘性会话绑定 | `repository/gateway_cache.go` |
| 余额 / 订阅用量缓存、余额预留 | `repository/billing_cache.go`、`service/billing_cache_service.go`、`service/billing_inflight_reservation.go` |
| 限流计数 | `internal/middleware/rate_limiter.go`、`server/middleware/key_info_rate_limit.go` |
| 用量统计缓存与在途展示 | `repository/usage_stats_cache.go`、`repository/usage_inflight_cache.go` |
| step-up 凭证（`totp:stepup:<用户>:<因子>:<会话>`，5 分钟） | `repository/totp_cache.go`、`docs/two-factor-factor-policy.md` |

## 八、测试与 CI 全景

> 这章解决什么问题：CI 里每个作业在守什么、红了怎么在本机复现，以及写测试时的约定。

### 8.1 构建标签

| 标签 | 含义 | 怎么跑 |
|------|------|--------|
| （无） | 默认测试，`go test ./...` 只跑这部分 | `cd backend && go test ./...` |
| `unit` | 单元测试，不依赖外部服务。标签是**附加**的：`-tags=unit` 会同时编译无标签的测试 | `go test -tags=unit ./...`（`make test-unit`） |
| `integration` | 真 PostgreSQL / Redis 的集成测试 | `go test -tags=integration ./...`（`make test-integration`） |
| `e2e` | `internal/integration` 的端到端测试；`E2E_MOCK=true` 时不需要真实 key | `make test-e2e-local`（即 `go test -tags=e2e ./internal/integration/...`） |
| `embed` | 嵌入前端；缺了它二进制没有页面（见 9.6） | `go test -tags=embed ./internal/web` |
| `wireinject` | 只给 `cmd/server/wire.go` 用，是 Wire 的输入文件 | `go generate ./cmd/server` |

### 8.2 CI 作业（`backend-ci.yml`）

| 作业 | 守什么 |
|------|--------|
| `changes` | 只改了 `frontend/`、`docs/`、`openspec/` 或 `*.md` 的 PR 跳过 Go 测试作业（`.github/ci/changed-paths.sh`）；push 到 main 与手动运行永远全跑 |
| `test-unit` | 全部单测（`make test-unit`），并断言 Go 版本 |
| `test-integration` | 全部集成测试（`make test-integration`） |
| `test-race-a` / `-b` / `-c` | `-race -p 1`，按 `-run` 名称前缀挑出的并发回归：a = 钱包与通知、采集与诊断；b = 缓存与运维安全、供应商与自动化、分层安全与隐私、流式交付与固定工具计费；c = Codex ticket、自定义用量、路由亲和、Anthropic 缓存转换、请求模型回显、DeepSeek、计费与媒体、支付校验授权、钱包 outbox、在途展示 |
| `small-retention-integration`、`account-timer-integration`、`operator-storage-integration`、`crypto-sweep-integration` | 需要真 PostgreSQL 的专项集成（用 15、16 的镜像或测试 harness 默认的 18，部分走专用 DSN 环境变量） |
| `deepseek-native-replay`、`client-error-privacy` | 专项 `-race` 回归 |
| `copilot-adapter` | `deploy/copilot-sdk` 适配器的租户与工具契约（pytest，禁网） |
| `shell` | `deploy/` 下脚本的 shell 测试（在 macOS runner 上跑） |
| `test` | 聚合门：上面所有 Go 测试作业都成功才算过；只改前端/文档时它们全部 skipped 也算过 |
| `frontend` | `make test-frontend` → 运维功能 vitest 清单 → `pnpm run build` → SPA 契约校验（`deploy/spa/`）→ 上传 dist |
| `build` | 取 `frontend` 的 dist：`go test -tags=embed ./internal/web` → `make build` 产出嵌入式二进制 |
| `golangci-lint` | `golangci-lint-action`，`version: v2.13`，配置 `backend/.golangci.yml` |
| `deepseek-search-probe` | 只在手动运行或指定分支：编译 opt-in 的真实搜索验证 |
| `cache-prime`、`cache-gc` | 只在 main 的 push 上跑：预热并保存 Go 编译缓存、清旧缓存；PR 只读缓存、不写 |

其它工作流：`security-scan.yml` 有 `backend-security`（govulncheck，并用 `tools/check_govulncheck_modules.py --fail-on-fixable` 暴露有修复版的模块级漏洞）和 `frontend-security`（`pnpm audit --prod --audit-level=high`，再用 `tools/check_pnpm_audit_exceptions.py` 对照 `.github/audit-exceptions.yml`：当前仍出现的高危 / 严重漏洞必须有未过期的豁免，否则失败；漏洞消失后留下的陈旧豁免不会报错，要及时删）；`sensitive-check.yml` 跑 `tools/check_sensitive.py --all` 并测它自己的单测。

**两个容易漏的点**：`-race` 只覆盖上表点名的用例（`-run` 前缀），新增并发敏感的测试要把名字加进对应作业，否则不会在 race 下跑；前端 spec 同理（见 8.5）。

### 8.3 本地复现

```bash
# 后端（在 backend/ 下）
go test -tags=unit ./...                                  # CI: test-unit
go test -tags=integration ./...                           # CI: test-integration（需要 Docker，或设 SUB2API_TEST_POSTGRES_DSN）
go test -tags=unit -run '^TestXxx$' ./internal/service    # 只跑一个用例（日常最常用）
golangci-lint run ./...                                   # CI: golangci-lint
go test -tags=embed ./internal/web -count=1               # CI: build；要求 backend/internal/web/dist 里已有前端产物

# 前端（仓库根；没有 make 就逐行执行 Makefile 里 test-frontend 目标的命令）
pnpm --dir frontend install --frozen-lockfile
pnpm --dir frontend run lint:check
pnpm --dir frontend run typecheck
pnpm --dir frontend exec vitest run <spec 路径>            # 单个 spec
pnpm --dir frontend exec vitest run --config dev/vitest.config.ts
pnpm --dir frontend run build                              # 含 check:i18n，输出到 backend/internal/web/dist
node --test deploy/spa/verify-spa-bundle.test.mjs
node deploy/spa/verify-spa-bundle.mjs frontend backend/internal/web/dist

# 仓库级
python tools/check_sensitive.py --all                      # CI: Sensitive Data Check（pre-commit 钩子默认只扫暂存区）
python -B -m unittest discover -s tools -p test_check_sensitive.py
bash scripts/prepush.sh                                    # 只看相对 origin/main 改动：gofmt、go vet、golangci-lint 子集、eslint、vue-tsc
```

`-tags=embed` 编译需要 `backend/internal/web/dist` 目录有内容，否则报 `pattern all:dist: no matching files found`：先 `pnpm --dir frontend run build`，或手建一个 `index.html` 占位（CI 的 `cache-prime` 就是这么做的）。`.gitignore` 为 `dist/.keep` 留了白名单，但这个文件目前没有被跟踪。

### 8.4 Windows 上已知的「天然失败」

下面这些用例在 Windows 本机必红、在 Linux CI 上是绿的（在本仓库 `go test -tags=unit ./...` 全量跑出来并逐个复核，根因都在测试或被测代码对 Unix 行为的假设里）；全量跑下来其余的包全部通过。

| 包 | 用例 | 原因 |
|----|------|------|
| `internal/service` | `TestTrafficDelivery*`（11 个） | `user_traffic_delivery.go` 落盘后对**目录**做 `Sync()`，Windows 返回 `Access is denied`；另有 0600 权限位断言 |
| `internal/mihomo` | 19 个（`TestSource*`、`TestProbeNode*`、`TestStaticNodeBindings*`、`TestSubscription*`、`TestUseOnceLease*`、`TestAtomicWritePermissions` 等） | 用 `#!/bin/sh` 脚本伪造内核可执行文件，以及 0600 / 0700 权限位断言 |
| `internal/requestcapture` | `TestExportAndPermissions` | 断言目录权限 0700，Windows 恒为 0777 |
| `internal/service/basispoints` | `TestImageRelayStoresPrivateFilesAndRemovesExpiredImages` | 断言文件权限 0600，Windows 恒为 0666 |

机器忙时偶发红、空闲时通过的时间敏感用例：`internal/requestcapture` 的 `TestCapture200Concurrent`、`internal/cryptopay/notify` 的 `TestDispatcherNeverBlocksCallerEvenWhenReceiverHangs`。`internal/service` 单个包在 Windows 上要跑十分钟以上，日常用 `-run`。

对策：本机全量跑时跳过它们，或者在 WSL / 容器里跑，剩下交给 CI。

```powershell
# PowerShell：整包跳过 mihomo，再按名字跳过其余天然失败的用例（-skip 用正则，Go 1.20+）
cd backend
$pkgs = go list ./... | Where-Object { $_ -notmatch '/internal/mihomo$' }
go test -tags=unit $pkgs -skip '^(TestTrafficDelivery|TestExportAndPermissions|TestImageRelayStoresPrivateFilesAndRemovesExpiredImages)'
```

### 8.5 写测试的约定

- **断言与桩**：`testify`（优先 `require`）；Redis 用 `miniredis`（`miniredis.RunT(t)`，共享入口 `internal/testutil/redis.go`）；SQL 用 `go-sqlmock`；HTTP 用 `httptest` + `gin.SetMode(gin.TestMode)`。
- **桩怎么写**：嵌入接口、只实现用到的方法（例：`server/routes/gateway_key_info_test.go` 里的 `keyInfoRouteUsageRepo`），接口加方法时不用逐个补；共享桩放 `internal/testutil`（`//go:build unit`，带编译期接口断言）。
- **真 PG / Redis 集成测试**：`//go:build integration`。`internal/repository/integration_harness_test.go` 的 `TestMain` 用 testcontainers 起 PostgreSQL（`postgres:18.1-alpine3.23`）与 Redis（`redis:8.4-alpine`）并自动 `ApplyMigrations`。`SUB2API_TEST_POSTGRES_DSN` 指向现成的 PostgreSQL 就不起容器（此时需要 Redis 的用例跳过）；`SUB2API_TEST_POSTGRES_IMAGE` 换 PostgreSQL 镜像（CI 用 15 验证低版本兼容）；没有 Docker 时本地整体跳过，`CI` 环境变量非空则失败。其它真 PG 用例用专用 DSN 变量（`ACCOUNT_TIMER_TEST_DSN`、`CRYPTO_SWEEP_TEST_DSN`）。
- **迁移测试**：`backend/migrations/*_migration_test.go` 读 `FS` 里的 SQL 文本断言（有幂等语句、不含 `DROP` / `TRUNCATE`…）；运行器行为在 `repository/migrations_runner_*_test.go`，真 PG 行为在 `migrations_schema_integration_test.go`。
- **守卫测试**：漏改一处就变红的清单式或 grep 式测试，失败信息里写着怎么补：`service/body_top_contract_test.go`、`server/routes/prompt_audit_route_coverage_test.go`、`server/routes/admin_route_coverage_test.go`、`config/env_reachability_test.go`、`server/api_contract_test.go`、`frontend/src/i18n/__tests__/localeKeyCompleteness.spec.ts`、`frontend/src/api/__tests__/admin.stepUpCoverage.spec.ts`。
- **生命周期测试**：用 `weak.Pointer` + `runtime.GC()` 断言大请求体在异步任务入队后可被回收（`service/content_moderation_lifecycle_test.go`）。
- **变异检查**：关键的安全 / 计费分支写完测试后，手工把被测代码里对应的那一行改坏（去掉撤销、翻转前置条件、改掉上限常量），确认至少一个测试变红；活下来的变异就补用例。做法写在测试注释里（例：`service/twofa_admin_reset_hardening_test.go` 里的「变异：…」）和 PR 正文里（列出杀掉的和活下来的），不需要专门工具。
- **假数据**：测试里像密钥的串必须明显是占位（含 `example`、`fake`、`dummy`、`placeholder` 等标记词），否则会被敏感扫描拦下；确需「像真的」的合成夹具，登记到 `.github/privacy-fixture-allowlist.json`（路径 + 整个文件的 SHA-256，文件一改就要重新审）。
- **前端 vitest**：jsdom 环境，spec 放在被测文件旁的 `__tests__/`。CI 只跑固定清单：根 `Makefile` 的 `FRONTEND_CRITICAL_VITEST`、`dev/` 下的 spec、`backend-ci.yml` 里「Operator feature frontend regression」那一串，以及 `navigation-ci.yml` 的几个。其余 spec 只在本地 `pnpm --dir frontend run test:run` 才跑，**新 spec 要进 CI 就得加进清单**。

## 九、变更规范

> 这章解决什么问题：提交、迁移、配置、开关、前端、发版各有哪些约定，违反了谁会拦你。

### 9.1 分支、提交与 PR

- **分支**：`<type>/<短横线描述>[-YYYYMMDD]`，type 用 `feat`、`fix`、`perf`、`docs`、`chore`、`ci`、`audit` 等。
- **PR 标题 = 提交标题**：`<type>(<可选 scope>): <中文一句话>`，说清改了什么、影响谁；合并方式是 squash，main 上一个 PR 一个提交，标题带 `(#PR号)`。前缀要写对：发版说明生成器按前缀分组（`feat` 新增、`fix` 修复、`perf` 性能、`security` 安全，其余进运维 / 工程化），规范见 `docs/agents/release-notes.md` 第 3 节。
- **PR 正文**：写清修前 / 修后怎么变、怎么验；加一段 `## 验证`，生成器会摘取前两条。
- **提交前**：`git config core.hooksPath .githooks` 启用钩子；`python tools/check_sensitive.py`；`bash scripts/prepush.sh`；再按第四章的坑 11 清单过一遍。
- **并行会话 / 自动化**：只暂存本任务的 hunk（`git add -p`，或 `git apply --cached`），不要 `git add -A`；`.artifacts/` 和会话笔记（`plan.md`、`progress.md`、`findings.md`、`task_plan.md`、`HANDOFF_*.md`）已被 `.gitignore` 挡住，别用 `-f` 加进来。
- **lint 以 CI 为准**：CI 的 `golangci-lint` 作业固定 `v2.13`，配置在 `backend/.golangci.yml`：errcheck 打开 `check-type-assertions` 并关掉默认排除项（`strings.Builder.WriteString` 之类的返回值也要处理），gofmt 带 `interface{}` → `any` 的改写规则。本地版本不同会漏报，红了按 CI 日志改。
- **CLA**：`cla.yml` 只在上游 `Wei-Shaw/sub2api` 仓库生效。

### 9.2 公开仓库红线

仓库是公开的，被推送过的内容删不掉。规则全文见 `CONTRIBUTING.md`，摘要：不写生产主机与 IP、账号 / 用户 ID、凭据、内网拓扑、真实订单、用户隐私；需要举例用保留地址（`192.0.2.0/24`、`198.51.100.0/24`、`203.0.113.0/24`、`2001:db8::/32`）。

- 扫描：`tools/check_sensitive.py`。凭据类（`sk-…`、Bearer、JWT、私钥、AWS key）在**所有**文件里查；基础设施类（公网 IP、「账号 + 数字」、主机别名、个人邮箱与 Windows 用户目录、`base_url` / `upstream` / `provider` / `中转` 后面跟着的第三方域名）只查文档与配置类文件（`docs/`、`deploy/`、`.md`、`.yml`、`.json` 等），测试文件与夹具豁免基础设施类。
- 会被扫到的不止文件：提交信息、作者邮箱也在 `commit-msg` / `pre-commit` 钩子和 CI 的元数据扫描里。
- 运维、部署与事故记录不进本仓库；`.gitignore` 挡着 `*credential*.md`、`*secrets*.md`、`*handoff*.md`、`docs-sensitive/`、`.learnings/`、`.env*` 等。

### 9.3 数据库迁移

迁移文件在 `backend/migrations/NNN_描述.sql`，由 `repository/migrations_runner.go` 执行；**SQL 迁移是表结构的唯一真相**（`repository/ent.go` 里写明），Ent schema 只用来生成客户端代码。

| 规则 | 细节 |
|------|------|
| 编号 | 取 main 上当前最大编号 + 1，描述用 snake_case；不回收历史空号。并行 PR 难免撞号（已有同号多文件，如 337），运行器按**完整文件名**排序执行、按文件名登记，所以同号可以共存；合并前 rebase 并确认顺序依赖没问题 |
| 换行 | 必须 LF（`.gitattributes` 对 `backend/migrations/*.sql` 强制 `eol=lf`）。运行器对 `TrimSpace` 之后的内容算 SHA-256，行尾变了 checksum 就变 |
| 幂等、只增 | `IF NOT EXISTS` / `IF EXISTS` / `ON CONFLICT DO NOTHING`；正向迁移，回滚另开新文件 |
| 已执行的不能改 | `schema_migrations` 记录文件名与 checksum；内容变了，`-check-migrations` 和启动都会因 checksum 不一致而失败。改名只能登记到 `renamedMigrations`（先例：337）；历史上误改过的文件登记在 `migrationChecksumCompatibilityRules`，**只收历史，不是新改动的出口** |
| 大表 DDL | 文件开头 `SET LOCAL lock_timeout = '5s';`（先例：`341_`、`342_`），拿不到锁就整个迁移回滚、稍后重试；长回填再加 `SET LOCAL statement_timeout`。一个迁移不要在同一事务里碰两张热表（`342_` 的注释写了原因：和看板查询互相等待会死锁） |
| 建索引 | `CREATE INDEX CONCURRENTLY IF NOT EXISTS` 只能放在 `*_notx.sql` 里，不带 `BEGIN` / `COMMIT`，不和别的语句混写；会话的 `statement_timeout` / `lock_timeout` 由运行器置 0 并在结束后复位。可能留下 invalid 索引的，在 `prepareNonTransactionalMigration` 里按文件名登记清理 |
| 并发安全 | 运行器持 PostgreSQL advisory lock，多实例同时启动只有一个在迁移 |
| 测试 | `backend/migrations/<名字>_migration_test.go`（文本断言，先例：`user_debt_migration_test.go`）；有行为的再加 `internal/repository` 里的真 PG 测试 |

`./sub2api -check-migrations` 只读比对：有待执行迁移或 checksum 不一致就失败；`./sub2api -migrate-only` 执行迁移后退出；两个参数互斥。本地等价：`cd backend && go run ./cmd/server -check-migrations`（读本地配置里的数据库）。配置项 `database.migration_mode`（环境变量 `DATABASE_MIGRATION_MODE`）取 `apply`（默认，启动时执行待办迁移）或 `validate`（启动时只比对，有待办或不一致就拒绝启动）。备份、上线顺序等运维做法不在本仓库。

### 9.4 配置项与环境变量

配置由 viper 加载（`internal/config/config.go`）：配置文件可由 `CONFIG_FILE` 指定路径，`DATA_DIR` 是数据目录；**环境变量名 = 配置键转大写，`.` 换成 `_`**（`AutomaticEnv` + `SetEnvKeyReplacer`），例：`database.migration_mode` → `DATABASE_MIGRATION_MODE`，`gateway.openai_ws.retry_total_budget_ms` → `GATEWAY_OPENAI_WS_RETRY_TOTAL_BUDGET_MS`。

| 要素 | 在哪里 |
|------|--------|
| 结构体字段 | `config.go`，带 `mapstructure` 标签 |
| 默认值 | `setDefaults()` 里的 `viper.SetDefault`；没有自己默认值的键在 `setEnvReachableDefaults()` 里登记零值 |
| 校验 | `(*Config).Validate()` |
| 示例 | `deploy/config.example.yaml` 必须同步（没有自动守卫，靠评审）；容器部署的环境变量清单在 `deploy/.env.example` |

**必须登记默认值**：viper 的 `Unmarshal` 只解码已知键，`AutomaticEnv` 只能覆盖已知键、不能凭空增加。结构体字段没有 `SetDefault` 时，只靠环境变量配置的部署会被**静默丢弃**，`config/env_reachability_test.go` 的 `TestConfigKeysAreEnvReachable` 会在这种情况下变红。map 和结构体切片只能用配置文件。

**`GATEWAY_*` 环境变量**：写进 `deploy/.env.example` 的每一个，必须以 `- KEY=${KEY:-默认值}` 的形式在四个 compose 文件（`docker-compose.yml`、`.local.yml`、`.standalone.yml`、`.dev.yml`）里各出现一次，否则 CI 的 `shell` 作业（`deploy/tests/docker-compose-gateway-env-test.sh`）失败。

### 9.5 功能开关默认关闭

新行为先加开关、默认关，评审和验证后再改默认；开关读取失败时按默认处理，不要因为存储抖动改变行为。现有写法：

- 配置项：`billing.balance_preauthorization_enabled`（预扣）、`billing.balance_merge_settlement_enabled`、`gateway.failover_on_400`、`gateway.inject_beta_for_apikey`、`server.h2c.enabled`、`image_storage.enabled` 默认全是 false。
- 后台设置：`parseSettings` 里 `== "true"` 表示默认关（`!= "false"` 才是默认开）；例：`LoginShapeDisguiseEnabled`、`OpenAIAPIKeyHealthBreakerSettings`（`DefaultOpenAIAPIKeyHealthBreakerSettings` 里 `Enabled: false`）。热路径读设置：缺失或读取失败按默认处理（`service/setting_features.go`）；读得很频繁的设置带 TTL 缓存，过期后台刷新而不是同步查库（范例：`service/setting_openai_apikey_health.go`）。
- 分组与密钥的可选字段：用指针（`*bool`）区分「缺失」和「显式 false」，让旧快照按默认值处理（先例：`APIKeyAuthSnapshot.CrossProtocolConversionEnabled`）。

### 9.6 前端构建与嵌入 Go 二进制

- 前端只有一种发布形态：Vite SPA 静态产物嵌进 Go 二进制。`pnpm run build` = `check:i18n` → `vue-tsc -b` → `vite build` → `dev/check-console-assets.mjs` → `build-manifest.mjs emit`，产物与 `frontend-build.json`（版本、commit、文件哈希）写进 `backend/internal/web/dist`。
- 后端构建必须带 **`-tags=embed`**：`backend/Makefile` 的 `make build` 是 `CGO_ENABLED=0 go build -tags embed -ldflags="-s -w -X main.Version=…" -trimpath -o bin/server ./cmd/server`，根 `Dockerfile` 与 `deploy/Dockerfile` 先构建前端再带标签编译。**不带标签**时 `web.HasEmbeddedFrontend()` 恒为 false，路由里不挂前端，`/` 返回 404（`text/plain`），服务照常启动、API 可用，所以出过「根路径静默 404」；`backend/Dockerfile` 是不带前端的纯后端镜像。
- 校验：`make verify-embed`（清单必须来自当前 checkout）、`make verify-spa`（`deploy/spa/verify-spa-bundle.mjs`：入口 `index.html`、被引用的资源都在、没有混进 SSR 产物），`make release-spa` 把构建与两项校验串起来。
- `frontend/src/legal` 构建时读取 `docs/legal/*.md`，所以只改 `docs/` 的 PR 也会跑前端与构建作业。

### 9.7 i18n 中英文同步

文案在 `frontend/src/i18n/locales/en` 与 `zh`，**两边的键必须一致、值不能为空、源码里静态引用的 `t('…')` 键必须存在**——`localeKeyCompleteness.spec.ts` 守这三条，`pnpm run build` 的第一步（`check:i18n`）就会跑它。动态拼接的键它查不到，自己对。新增页面或组件，中英文案一起提交。

### 9.8 新增文档与 `.gitignore` 白名单

`.gitignore` 里 `docs/*` 默认忽略，白名单是 `!docs/<文件>`（`release-notes/`、`legal/` 整目录放行）。新增一份该进库的文档：在 `.gitignore` 白名单区加一行 `!docs/<文件名>`（或 `git add -f`）。同类的坑：根 `scripts/`、`AGENTS.md`、`CLAUDE.md`、`.claude` 被忽略；任何名为 `tests` 的目录都被忽略（`deploy/tests/` 例外）。运维类文档（含地址、策略、脚本）不进本仓库。可以用 `git check-ignore -v <路径>` 看哪一行在挡。

### 9.9 发版的通用机制

不写任何服务器侧步骤，只写代码与 CI 里的机制：

1. **发版说明**：`docs/release-notes/<tag>.md`（推荐，随 PR 评审）或 annotated tag 的 message 正文，二选一，**两者都为空 workflow 直接失败**。生成器：`scripts/release/gen-release-notes.sh <版本>`；规范与模板：`docs/agents/release-notes.md`、`docs/release-notes/TEMPLATE.md`。内容原样进 GitHub Release，公开可见：只写中性指代，不写账号 / 主机 / 取证细节。
2. **打 tag**：`git tag -a vX.Y.Z -m "release vX.Y.Z"`，`git push origin vX.Y.Z`；tag 须匹配 `^v[0-9]+(\.[0-9]+){2}([+-][0-9A-Za-z.+-]+)?$`。`release.yml` 不跑测试，只在 CI 全绿的 main 提交上打。只能有一个触发源（`push: tags`），监听 `create:` 会让同一个 tag 起两次、互相取消。
3. **`release.yml` 的作业**：`resolve_release_ref`（把 tag 固定到具体 commit）→ `update-version`（把版本写进 `backend/cmd/server/VERSION`，仅用于本次构建）→ `build-frontend`（`pnpm run build` + `build-manifest.mjs verify --mode spa --expect-commit`）→ `release`（再校验嵌入的前端属于这个 commit；GoReleaser 构建）→ `sync-version-file`（成功后向默认分支提交 `chore: sync VERSION to <版本> [skip ci]`）。
4. **制品**：默认 `.goreleaser.amd64.yaml`：`CGO_ENABLED=0`、`-tags=embed`、`-X main.Version / Commit / Date / BuildType`，产出 `sub2api_<版本>_linux_amd64.tar.gz` 与 `checksums.txt`；`workflow_dispatch` 的 `all_platforms` 输入（或仓库变量 `RELEASE_ALL_PLATFORMS=true`）走 `.goreleaser.yaml`（多平台与多架构镜像），`simple_release` 走 `.goreleaser.simple.yaml`。校验和用 `sha256sum -c checksums.txt`。
5. **版本号**：来源是 `backend/cmd/server/VERSION`（`go:embed`），构建时可被 `-X main.Version` 覆盖；`backend/scripts/resolve-version.sh` 在打了 tag 的检出上以 tag 为准。不要在别处再放一份 VERSION。`-version` 命令行参数会打印版本、commit、构建时间。

## 十、「新增 X 要改哪几处」清单

> 这章解决什么问题：加一类东西时一次对全需要改的地方，以及漏了谁会拦你。路径相对 `backend/internal/`，前端相对 `frontend/`。

### 10.1 新增 API Key 自助接口（`/v1/sub2api/*`）

先例：`GET /v1/sub2api/account`（`feat(api)` #690，17 个文件）。

1. 读数据的服务层：`service/key_account_summary.go`；handler：`handler/gateway_key_account.go`（共享的时间窗解析、响应帧等在 `gateway_key_info.go`）。
2. 路由：`server/routes/gateway_key_self_service.go` 的 `registerKeySelfServiceRoutes`，每个接口挂自己的限速计数：`middleware.KeyInfoRateLimit(redisClient, "<名字>")`（Redis 计数 `key_info:<名字>:<keyID>`，每 Key 每接口每分钟 `KeyInfoRateLimitPerMinute`）。它注册在 `groupModelAllowlist` 之前，这些请求不带模型。
3. 鉴权跳过计费闸：`server/middleware/key_info_rate_limit.go` 的 **`isKeySelfServicePath`** 加上新路径（漏了，过期或额度用完的 Key 会被计费闸挡在接口之前）。
4. 路径别名：`server/middleware/gateway_path_index.go` 的 **`gatewayEndpointSegments`** 加上路径末段的词（漏了，手写 Base URL 带来的 `/v1/v1/…`、多余斜杠、末尾斜杠等写法都会 404；不带 `/v1` 的裸路径对 `/v1/sub2api/*` 是故意不做别名的）。
5. 前端文档页：`src/data/apiDocs.ts`（`key-self-service` 页；文案中英双语 `l('中文', 'English')`）。
6. 测试：`server/middleware/key_info_rate_limit_test.go`（`TestIsKeySelfServicePath`、各接口独立计数）、`gateway_path_alias_test.go`（`TestGatewayPathAliasRouteClassification`，路由表里加这条）、`gateway_path_base_url_forms_test.go`（各种 Base URL 写法）、`server/routes/gateway_key_info_test.go`（路由已注册）、新 handler / service / 路由的测试；`src/data/__tests__/apiDocs.spec.ts` 与 `src/views/public/__tests__/DocsView.spec.ts`（文档页，含「文档写的每分钟额度与限速器一致」这类断言）。

### 10.2 新增配置项

`config.go` 加字段（`mapstructure` 标签）→ `setDefaults()` 或 `setEnvReachableDefaults()` 登记默认值 → `(*Config).Validate()` 加校验 → `deploy/config.example.yaml` 同步 → 要在容器里用环境变量配置的，补 `deploy/.env.example` 与四个 compose 文件（`GATEWAY_*` 有 shell 测试守）→ 新行为默认关。守卫：`TestConfigKeysAreEnvReachable`、`config/config_test.go`、CI 的 `shell` 作业。细则见 9.4、9.5。

### 10.3 新增迁移

`backend/migrations/NNN_描述.sql`（LF、幂等、大表加 `lock_timeout`；索引用 `*_notx.sql`）→ `backend/migrations/<名字>_migration_test.go` → 有对应 Ent 实体就改 `ent/schema/*.go` 并 `go generate ./ent` → 对应的 DTO / 仓储 / 服务代码 → `go test -tags=integration ./internal/repository`（真 PG 跑一遍迁移）。守卫：运行器的 checksum 校验，`TestMigrationsRunner_IsIdempotent_AndSchemaIsUpToDate`。细则见 9.3。

### 10.4 新增后台设置项（以布尔开关为例）

先例：`login_shape_disguise_enabled`（#659）。

| 层 | 文件 | 改什么 |
|----|------|--------|
| service | `service/domain_constants.go` | `SettingKey…` 常量 |
| | `service/settings_view.go` | `SystemSettings` 字段 |
| | `service/setting_parse.go`（`parseSettings`） | 从库里读：默认关用 `== "true"` |
| | `service/setting_update.go`（`buildSystemSettingsUpdates`） | 写库 |
| | `service/setting_features.go` | 热路径用的 getter（缺失 / 读取失败按默认） |
| handler | `handler/dto/settings.go` | DTO 字段 |
| | `handler/admin/setting_handler.go`（`GetSettings`）、`setting_handler_update.go`（请求体用 `*bool`，省略 = 保持现值；`UpdateSettings` 合并与回显） | 读写与回显 |
| | `handler/admin/setting_handler_audit.go`（`diffSettings`） | 审计里的变更列表 |
| 前端 | `src/api/admin/settings.ts`、`src/views/admin/SettingsView.vue`、`src/i18n/locales/{en,zh}/admin/settings.ts` | 类型、界面、双语文案 |

守卫：`server/api_contract_test.go` 的 `TestAPIContracts`（设置响应的 JSON 契约，要补新键）、`SettingsView.spec.ts`、i18n 完整性。设置里的敏感值（密钥、口令）要走第 10.5 的路由分类。

### 10.5 新增后台管理接口

1. 路由：`server/routes/admin.go`（或同目录按主题的文件）；handler：`handler/admin/`，DTO：`handler/dto/`。
2. **路由分类**：`pkg/adminroutepolicy/policy.go` 里把路由归入「机器可调用 / 需 step-up / 无密钥」之一（精确或前缀规则 + `Reason`）；未归类的在运行时按需要 step-up 处理，`server/routes/admin_route_coverage_test.go` 的 `TestAdminRouteCoverageClassification` 会在 CI 里拦住。返回或修改密钥类数据（任何方法，包括 GET）一律归 step-up。
3. 前端：`src/api/admin/*.ts` 里的请求都走共享的 `apiClient`（拦截器统一处理 `STEP_UP_*`）；新增写接口会让 `src/api/__tests__/admin.stepUpCoverage.spec.ts` 的调用点基线变红，确认受保护后刷新：`UPDATE_STEP_UP_BASELINE=1 pnpm vitest run src/api/__tests__/admin.stepUpCoverage.spec.ts`。
4. 文案、双语 i18n、测试同上。

### 10.6 新增网关路由

`server/routes/gateway.go` 注册（需要无 `/v1` 前缀别名的用 `rootRoute`）→ POST 路由必须在 `server/routes/prompt_audit_route_coverage_test.go` 的 `audited`（handler 里要有对应的安全审计检查）或 `excluded`（写明不执行模型请求的理由）里登记 → 新的路径末段词补 `gatewayEndpointSegments`（见 10.1）→ 复合路由用的 `compositeRouteEndpointForPath` 视情况补映射 → handler 里并发槽、计费资格、用量记录照 `handler/gateway_handler.go` 的顺序接。

### 10.7 新增 Redis key 族

1. 实现在 `repository/`（`service` 与 `handler` 不许 import go-redis，见 6.3），`service` 里定义接口；**key 的布局（key 形式 / 类型 / 用途）写在文件头注释里**（范例：`repository/usage_inflight_cache.go`），TTL 用命名常量。
2. 不用 `KEYS`；热路径不用 `SCAN`——要按用户 / 对象撤销就维护索引集合（范例：`usage:inflight:users`；`service.TwoFactorGrantStore` 的用户维度索引，实现在 `repository/twofa_grant_cache.go`）。存量里只有 `concurrency_cache.go` 的一次性清扫用了带前缀的 `SCAN`，并用 marker key 保证只跑一次。
3. 跨版本共享、结构会变的缓存，key 带 schema 版本，滚动期间新旧进程各写各的（范例：`apikey:auth:v<版本>:…`，`service.APIKeyAuthCacheSchemaVersion`，测试 `repository/api_key_cache_version_test.go`）。
4. 单测用 `miniredis`；Redis 不可用时的降级方向（放行还是拒绝）要显式写出来并测（限流类放行：`KeyInfoRateLimit`；凭证类 fail-closed：step-up）。
5. Redis 里只放可丢弃或可重建的状态（缓存、计数、凭证窗口），丢了的后果要想清楚（重新验证、重建缓存）；需要持久的数据写 PG。

### 10.8 给 interface 加方法

在 `service/` 的接口加方法 → 实现（通常 `repository/`）→ **所有测试桩补全**：`internal/testutil/stubs.go` 的共享桩、各测试文件里的 `*Stub` / `*Mock` / `*Fake`（编译报 `missing method` 就是漏了，搜索命令见第四章坑 6）→ `go vet -tags=unit ./...` 确认全包能编译。新桩优先「嵌入接口」。

### 10.9 新增调度器 / 平台

没有集中注册表，改动面大（先例：`feat(minimax)`，`git show --stat 82ba8404c` 能看到全貌）。从 `PlatformMiniMax` 全仓 grep 起，主要的点：

- 常量与集合：`domain/constants.go`、`service/domain_constants.go`、`service.IsOpenAIResponsesCompatibleGatewayPlatform`（`service/cross_protocol_conversion.go`，路由与测试共用同一份「OpenAI 兼容网关平台」集合，避免新增平台只改一处）。
- 路由与 handler：`server/routes/gateway.go`（`countTokensHandler` 等按平台分派）、`handler/gateway_handler.go`、`handler/openai_gateway_handler.go`、`handler/admin/group_handler.go`、`handler/admin/channel_handler.go`。
- 调度与账号：`service/account.go`、`service/openai_gateway_scheduling.go`、`service/scheduler_snapshot_service.go`。
- 数据库：表里有平台的 CHECK 约束或 Ent 枚举时，写迁移放宽并改 `ent/schema/*.go`（先例迁移：`237_add_minimax_platform.sql`）。
- 前端：`src/constants/platforms.ts`、`src/components/account/CreateAccountModal.vue`、`credentialsBuilder.ts`、`PlatformIcon.vue`、`PlatformTypeBadge.vue`、`GroupBadge.vue`、`src/components/keys/UseKeyModal.vue`、监控相关组件、双语 i18n。
- 文档：`docs/COMPOSITE_GROUPS.md`。

### 10.10 新增支付渠道

1. `payment/types.go`：加 `Type…` 常量，必要时在 `GetBasePaymentType` 里归并；实现 `payment.Provider` 接口（`CreatePayment`、`QueryOrder`、`VerifyNotification`、`Refund`；要退款查询再实现 `RefundQueryProvider`）。
2. `payment/provider/<名字>.go` 与测试（签名、回调、退款、重放防护都要有）；`payment/provider/factory.go` 的 `CreateProvider` 加分支。
3. 服务层：`service/payment_config_providers.go`（配置字段白名单与可启用集合）、`service/payment_currency.go`、`service/payment_order_provider_snapshot.go`、`service/payment_order.go`；回调入口在 `handler/payment_webhook_handler.go` 与 `server/routes/payment.go`。可用 `payment.TypeAirwallex` 全仓 grep 对照一个完整的先例。
4. 前端：支付页与后台支付配置界面、双语 i18n；文档 `docs/PAYMENT.md`、`docs/PAYMENT_CN.md`。
5. 测试夹具里的「密钥」必须是合成的；确需像真的，登记 `.github/privacy-fixture-allowlist.json`（见 8.5）。

### 10.11 改 Ent schema / 改依赖装配

- **Ent**：改 `ent/schema/*.go` → `cd backend && go generate ./ent` → 生成物一并提交；同时写 SQL 迁移（见 10.3）。生成参数固定在 `ent/generate.go`（`--feature sql/upsert,intercept,sql/execquery,sql/lock --idtype int64`），不要手改生成文件。
- **Wire**：新增构造函数后加进所在包的 `ProviderSet`（`config/`、`repository/`、`service/`、`payment/`、`server/middleware/`、`handler/` 各有 `wire.go`；`securityaudit/prompt_module.go` 和 `server/http.go` 里各有一个），汇总在 `cmd/server/wire.go`；`cd backend && go generate ./cmd/server` 重新生成 `wire_gen.go`（`backend/Makefile` 的 `make generate` 同时生成 Ent 与 Wire）。

## 十一、工程规则（事故沉淀，已脱敏）

> 这章解决什么问题：线上事故里学到的、写进代码就能避免重犯的规则。每条给出代码里对应的位置；只写主线代码里能核实的部分。

**1. 同一份请求体：读一次、解析一次，复用结果；别在拒绝之前反复整段解析。**
热路径上的请求体可达几 MB，每个顶层字段各 `gjson.GetBytes` 一遍就是整体重扫；过载时「被 429/503 拒绝的请求」也要白付这笔 CPU。
- 读一次：`pkg/httputil` 的 `ReadRequestBodyWithPrealloc` 与 `PrereadBody`；改写后用 `pkg/requestmodel` 的 `ResetRequestBody` 回填，下游零拷贝重读。
- 解析一次：`service/gateway_request.go` 的 `ParseGatewayRequest` 产出 `ParsedRequest`（system / messages / input 按偏移取视图）；改写请求体必须走 `ReplaceBody` / `CloneForBody` 刷新视图，别手改 `Body`。
- 扫一次、查多次：`service/body_top_index.go` 的 `RequestBodyTop(c, body)`（惰性顶层索引，请求体不小于 16 KiB 才建；WebSocket 的 context 不挂，免得长连接钉住请求体）。前提：交给它的切片之后不得原地修改，`service/body_top_contract_test.go` 用 grep 守这条。
- 先用便宜条件短路再看请求体：`handler/anthropic_messages_strict_validation.go` 的 `ValidateStrictOpusMessagesRequest` 先判模型、非目标模型不碰请求体；`server/middleware/request_field_conflict.go` 在选号与计费之前拒绝重复字段。

**2. 异步任务（计费任务池、`usage_logs` 批量写入）的闭包不捕获请求 context / `gin.Context` / 请求体；零拷贝子串要 clone。**
请求结束后 context 会被取消、gin 的 Context 会被复用，大请求体被任务持有则迟迟不能回收。
- `handler/openai_gateway_handler.go` 的 `usageRecordContext` / `wrapUsageRecordTaskContext`：计费在**脱离请求的 context** 上跑，只拷贝需要的不可变值（调度快照标记、请求采集句柄、在途预留、request id），丢弃取消与超时；`service/scheduler_freshness.go` 的 `WithSchedulerSnapshotContext` 同理。
- `service/gateway_usage_billing.go` 的 `RecordUsageInput` 注释：异步 worker 只收计费所需快照，不得持有 `ParsedRequest` / `RequestBodyRef`；批量写入的队列元素 `repository/usage_log_repo_insert.go` 的 `usageLogCreateRequest` 不带 context。
- `pkg/requestmodel/requestmodel.go` 的 `unsafeStringView`：用 `unsafe` 零拷贝读请求体，**返回给外部的值一律 `strings.Clone`**，否则会与调用方的缓冲区共享底层数组。
- 回归测试用 `weak.Pointer` 断言「入队后请求体可被回收」：`service/content_moderation_lifecycle_test.go`。

**3. 一个请求对上游的尝试要有总次数与总时长上限；失败的账号要能被临时摘出调度并自动恢复；网关自己返回的 503/429 要带 `Retry-After`。**
- 次数与时长：`service/gateway_forward.go` 的 `maxRetryAttempts`（5）与 `maxRetryElapsed`（10 秒）卡住单个账号上的重试循环（`gateway_anthropic_passthrough.go`、`gateway_bedrock.go` 共用）；`handler/failover_loop.go` 的 `FailoverState` 管换号：配置项 `gateway.max_account_switches`（默认 10）在填充调度下被抬到不低于 1024（`fillSchedulingSwitchBudget`，候选耗尽才是正常终点，1024 只是防死循环的安全阀），利润门否决另有 `maxProfitVetoAttempts`，同账号重试次数按 `Account.GetPoolModeRetryCount()`、延迟封顶 `maxRequestScopedRetryDelay`；OpenAI WebSocket 重试有总预算 `gateway.openai_ws.retry_total_budget_ms`（`service/openai_gateway_service.go` 的 `openAIWSRetryTotalBudget`）。
- 摘出与恢复：账号的 `TempUnschedulableUntil` 到点自动恢复（`service/account.go` 的 `IsSchedulable`）；`service/openai_apikey_health_breaker.go` 是滚动窗口失败数达阈值后写临时不可调度的实现（后台设置，默认关闭，窗口 / 阈值 / 冷却可配）。
- `Retry-After`：`handler/gateway_handler.go` 的 `billingErrorDetails`（RPM、平台配额 429 带剩余秒数）；`server/routes/gateway.go` 复合路由缓存未就绪的 503 带 `Retry-After: 1`；`server/middleware/key_info_rate_limit.go` 的 429；`repository/usage_stats_gate.go` 的 `ErrUsageStatsBusy`（429，经 `pkg/response` 的 `ErrorFrom` 把错误元数据 `retry_after` 转成响应头）。新增的 429 / 503 拒绝路径照这个做。

**4. go-redis 连接池：FIFO + 空闲回收 + 空闲上限，池耗尽快速失败。**
默认 LIFO 下压在栈底的连接永远取不到，`ConnMaxIdleTime` 与 `ConnMaxLifetime` 也就检查不到它们；`MinIdleConns` 只保底、不收缩。实现在 `repository/redis.go` 的 `buildRedisOptions`：`PoolFIFO`、`ConnMaxIdleTime`（0 要传负数才表示不回收）、`MaxIdleConns`（唯一不依赖流量的收缩手段）、`PoolTimeout`（池耗尽时快速失败，是背压阀）、带抖动的 `ConnMaxLifetime`；默认值与说明在 `config.go` 的 `RedisConfig` 和 `viper.SetDefault("redis.…")`，`redis.pool_stats_interval_seconds` 控制池状态日志。

**5. 热路径禁止每请求查库；缓存键带版本；统计聚合不扫 `usage_logs`；超预算返回 429 + `Retry-After` 而不是 500。**
- 网关请求以调度快照为准（snapshot-only）：`service/gateway_scheduling.go` 的 `PrepareSchedulerRequestContext`、`service/scheduler_freshness.go` 的 `WithSchedulerSnapshotOnly` / `withSchedulerRequestMode`；路由中间件里的复合路由解析也先标记 snapshot-only（`server/routes/gateway.go`）。回归测试：`service/composite_route_resolver_test.go`、`gateway_hotpath_optimization_test.go` 里的 `*SnapshotOnly*` 用例。
- 缓存键带版本：`repository/api_key_cache.go` 的 `apiKeyAuthCacheKey`（`service.APIKeyAuthCacheSchemaVersion`）；调度快照是带版本与世代的 `sched:*`（`repository/scheduler_cache.go`）。
- 聚合：「小时汇总表 + 已关闭的天缓存一整天 + 今天短缓存」：`repository/usage_log_repo_dashboard_split.go`、`usage_key_window_split.go`、`usage_stats_cache.go`（`usageStatsKeyClosedDaysTTL`）、汇总表写入 `dashboard_aggregation_repo.go`。看板与统计接口不要直接 `GROUP BY` 扫 `usage_logs`。
- 超预算：`repository/usage_stats_gate.go` 的 `usageStatsGate` 限制并发聚合，拿不到槽或超时得到 `ErrUsageStatsBusy`（429 + `Retry-After: 2`，不记 `[ERROR]`、不计入 5xx）；每个所有者（用户 / Key）有自己的份额，防止一个客户端占满。

**6. 配置保存接口不被长时间持锁的后台任务卡住：先落库，应用异步，响应带 `applied`。**
`cryptopay` 应用配置要持 `sweepMu`，而手动归集可能持有很久。`handler/crypto_handler.go` 的保存流程：`SaveConfigIfUnchanged`（已持久化）→ `reloadCryptoConfig` → 响应 `{"saved": true, "applied": …}`，等待上限 `cryptoConfigApplyWait`（3 秒）；`cryptopay/config_reload_async.go` 的 `Service.ReloadWithin` 把多次保存合并成一次 reload，等不到锁就返回 `applied=false`，应用在后台继续（`config_reload_async_test.go`）。

**7. 链上扫描（`cryptopay/scanner`）的成本模型。**
- 一个扫描窗口（区块区间，上限 `scan_range`，内置默认 2000）= ERC-20 路径每 200 个地址一次 `eth_getLogs`（`scanner/evm.go` 的 `batchSize`）+ 原生币路径**每块**一次带完整交易的 `eth_getBlockByNumber`（`scanner/native.go` 的 `ScanNativeRange`，请求数跟着出块速度走）。
- **窗口整体成败**：只有区间内每一批都成功才算扫完，游标才前进（`worker.go` 的 `cursor.Save`）；单批失败概率 p、N 批的整体成功率是 (1−p)^N，所以失败的批次要单独重跑（`maxBatchRetryRounds`，且只在部分批次成功时才重跑，每轮并发减半）。
- **时间预算**：每链每轮 `maxScanTimePerChain`（90 秒）、`maxScanBatchesPerChain`（4 个窗口）、整轮 `scanPassDeadline`（15 分钟，防止一个挂住的 RPC 永远占着扫描锁）；落后太多时先扫最近 `evmFirstRunLookback` 块，不让新充值排在历史追赶后面。
- **并发是上限不是目标**：`scanBatchConcurrency`（8）、每个节点 `scanBatchesPerEndpoint`（4）；调得过低，一个窗口的批次串行跑完就会吃光时间预算、追不上出块。节点侧的节流与健康冷却在 `cryptopay/rpc/`（`EndpointThrottle`、`backoff.go` 里的各类冷却、`EndpointLimits`）。
- `native_scan`：请求量 `blocks`（默认）∝ 出块速度（每块一次），`txlist`（区块浏览器的账户接口）∝ 监控地址数 ×（86400 / 轮询间隔）。地址多而出块慢选 `blocks`，地址少而出块快选 `txlist`；`txlist` 需要配 `explorer_api_key`，没配就报错并保持游标，**不会**悄悄退回逐块扫描。

**8. 并行会话与自动化提交的约定。**
只暂存本任务的 hunk，不用 `git add -A`；`.artifacts/` 与会话笔记不入库（`.gitignore` 已挡）；CI 的 lint 版本与本地可能不同，以 CI 为准（见 9.1）。

## 十二、参考资源

- [上游仓库](https://github.com/Wei-Shaw/sub2api)
- [Ent 文档](https://entgo.io/docs/getting-started)
- [Vue3 文档](https://vuejs.org/)
- [pnpm 文档](https://pnpm.io/)
