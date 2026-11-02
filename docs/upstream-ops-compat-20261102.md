# upstream-ops 兼容评估与最小导入适配（2026-11-02）

> 独立侧任务。未改 VERSION / Tag / 发布 / 部署 / 生产库，未改本站鉴权、BFF、网关协议与主题文件。
> upstream-ops 快照：`F:\GO\upstream-ops` @ `821e613779a72bcf426577e0f37920315f25b77f`（2026-09-25，v0.0.10，**未声明 license → 不复制其源码**）。

## 1. 结论摘要

| 问题 | 结论 | 依据 |
| --- | --- | --- |
| upstream-ops 有没有「一键导入」 | **没有**。管理端只有渠道 / 网关通道的增删改查与同步，没有 import/export 路由，也没有批量导入 UI | `backend/api/channels.go:22-49`、`backend/api/gateway_admin.go:25-60`；全仓 `导入/导出/import` 检索无管理端实现 |
| 它的 sub2api connector 能不能吃我们的 Cookie 会话 | **不能**。connector 只读 `access_token`，没有 cookie jar / CSRF / `session_managed` 分支 | `backend/connector/sub2api/sub2api.go:78-131` |
| 它能不能用 token 模式接我们的站点 | 语法上可以（`{"access_token": "...", "refresh_token": "..."}`），但那要求把 Bearer 交给它 | `backend/channel/service.go:139`、`:461-467`、`:803-808` |
| 2FA 怎么办 | upstream-ops 遇到 `requires_2fa` 直接报错并要求关闭 2FA（`sub2api.go:120`）。**我们不在本站做任何绕过，也不改别人仓库** | 同上 |
| 浏览器里能不能直接 fetch 它的 API | **不能**。upstream-ops 没有 CORS 中间件，跨源读取会被浏览器拦；本适配器因此只吃粘贴的 JSON 快照 | 上游全仓无 `Access-Control-Allow-Origin` |

**因此最小可用入口定为：JSON 快照 → 预览 / 校验 / 去重 → 产出既有 `sub2api-data` v1 导入载荷。** 不自动登录、不搬凭据、不关 2FA。

## 2. upstream-ops 真实 API 契约（只列与本适配相关的）

管理端鉴权：`Authorization: Bearer <token>`（`backend/auth/auth.go:160-162`）；登录 `POST /api/auth/login` 返回 `{data:{token,expires_at,username}}`，鉴权关闭时返回 `{data:{auth_disabled:true}}`（`backend/api/auth.go`）。

| 方法 | 路径 | 响应形状 | 备注 |
| --- | --- | --- | --- |
| GET | `/api/channels` | `{data: Channel[]}`；带 `page/page_size` 时为 `{data:{items,total,page,page_size,pages}}` | `Channel` 的 `password_cipher` 为 `json:"-"`，**接口不返回密码** |
| GET | `/api/gateway/providers` | `{data:[{id,name,base_url,api_key_hint,upstream_protocol,default_billing_rate,enabled}]}` | 只有 `api_key_hint`；明文 key 在 `POST /api/gateway/providers/:id/reveal`，本适配器**不调用** |
| POST | `/api/gateway/providers/:id/reveal` | `{...}` 明文 key | 禁止接入：会把密钥带进浏览器/日志 |
| GET | `/api/channels/:id/api-keys` | 分页 key 元数据 | 同上，不用于导入 |

`Channel` 可用字段：`id,name,type(newapi|sub2api),site_url,username,sort_order,credential_mode(password|token),monitor_enabled,balance_threshold,recharge_multiplier*,tags,notes,last_balance,today_cost,total_cost,last_error`（`backend/storage/channels.go`）。

## 3. 认证契约差异（已核对两边真实实现）

| 维度 | upstream-ops 的 sub2api connector | 本站（Nuxt BFF）浏览器登录 |
| --- | --- | --- |
| 登录请求 | `POST /api/v1/auth/login {email,password[,turnstile_token]}` | 同路径，但响应经 BFF 处理 |
| 令牌去向 | 要求响应体 `data.access_token`（`:78-131`） | **BFF 把 `access_token`/`refresh_token` 置空，`session_managed: true`，会话落 HttpOnly Cookie**（`frontend/server/utils/proxyPolicy.ts:324-346`、`frontend/server/utils/session.ts:138-160`） |
| Cookie 支持 | 无 cookie jar | Cookie 即会话 |
| CSRF | 无处理 | 由 BFF/接口自己维护 |
| 2FA | `requires_2fa` → 直接失败并提示关闭 | 保持开启，不绕过 |
| token 模式 | 必须 `access_token`，`refresh_token` 可选（`backend/channel/service.go:139,461-467,803-808`） | 只有服务端到服务端登录才拿得到原始 Bearer 契约 |

> 主代理正在做的是 BFF 侧「无 Origin/Referer/Sec-Fetch-Site、无 Cookie 的服务端登录/刷新返回原始 Bearer」的兼容；本适配器不依赖也不修改它，浏览器这条路径继续是 `session_managed`。

## 4. 本站既有导入流程（复用，不新增后端接口）

- 入口：`POST /api/v1/admin/accounts/data`（`backend/internal/server/routes/admin.go:480-481`）。
- 载荷：`{type:"sub2api-data"|"sub2api-bundle", version:1, exported_at, proxies:[], accounts:[{name,platform,type,credentials,...}]}`（`backend/internal/handler/admin/account_data.go:27`）。
- 硬约束：`credentials` 必须非空（`validateDataAccount`，`:682`）；`type` 仅 `oauth|setup-token|apikey|cloudflare|upstream|bedrock|service_account`。
- 该接口只建账号/代理；**不涉及余额、倍率、支付、删除**。

## 5. 适配器契约

实现：`frontend/src/utils/upstreamOpsCompat.ts`（纯函数，无网络、无 DOM、无日志）。

| 函数 | 作用 |
| --- | --- |
| `parseUpstreamOpsSnapshot(input)` | 识别 `{data:[…]}` / `{data:{items:[…]}}` / 裸数组；按 `site_url` / `base_url` 分渠道与网关通道；逐行问题进 `issues`，不抛异常 |
| `buildUpstreamOpsPreview(snapshot, {existingAccounts, platformOverrides})` | 生成预览行与统计；状态：`needs-credential` / `needs-platform` / `duplicate` / `unsupported` / `invalid` |
| `buildUpstreamOpsDataPayload(rows, credentials)` | 只把「网关通道 + 明确平台 + 操作者手填 key」的行转成 `sub2api-data` v1；其余进 `skipped` |
| `describeUpstreamOpsAuthContract()` | 固定输出 `browserSessionMode=session_managed`、`connectorAuthMode=bearer-token-only`、`supportsCookieJar=false`、`disablesTwoFactor=false` |

映射规则（不静默替换）：

| 来源 | sub2api 落点 | 说明 |
| --- | --- | --- |
| gateway provider，`upstream_protocol=openai/openai_responses` | `platform=openai`，`type=upstream` | 走 `credentials.base_url + credentials.api_key` |
| gateway provider，`anthropic` | `platform=anthropic`，`type=upstream` | 同上 |
| gateway provider，`auto`/未知 | `platform=null` → `needs-platform` | 必须人工选平台，禁止猜 |
| channel（`newapi` / `sub2api` 站点） | `unsupported` | 快照不含密码与模型平台，sub2api 侧没有对应账号语义；仅参与去重与清单核对 |

去重键：地址（scheme/host 小写、去尾斜杠）优先，其次名称（trim + 小写 + 合并空格）；先比本站已有账号，再比批内。重复行永远不进载荷。

调度占位：`concurrency=1`、`priority=0`（最保守，不抢现有流量），管理员导入后再按需调整。

## 6. 明确不做

- 不调用 `reveal`、不读 `password` / `token_credential` / `api_key` / `cookie`，不把任何凭据写进浏览器状态或日志。
- 不导出/迁移余额、充值倍率、支付配置，不删除任何数据。
- 不要求也不实现关闭 2FA；不改本站 BFF / auth / 网关协议。
- 不复制 upstream-ops 源码（无 license），只按公开 API 契约做适配层。
- 不改 `frontend/src/styles/*`、`nuxt-ui-theme.css` 等主题文件（夜间提亮由另一分支处理）。

## 7. 交付文件与集成点

新增（全部为独立文件，未改共享文件）：

- `frontend/src/utils/upstreamOpsCompat.ts`
- `frontend/src/utils/__tests__/upstreamOpsCompat.spec.ts`
- `frontend/src/components/admin/account/UpstreamOpsImportPanel.vue`（Nuxt UI；语义色 token；无硬编码深色）
- `frontend/src/views/admin/UpstreamOpsImportView.vue`（懒加载面板的薄壳）

接线已在本 PR 完成（只动本功能小块）：

1. 路由：`frontend/src/router/records.ts` 新增 `/admin/accounts/upstream-ops`（`AdminAccountsUpstreamOps`），懒加载 `UpstreamOpsImportView`。
   `{ path: '/admin/upstream-ops', component: () => import('@/views/admin/UpstreamOpsImportView.vue'), meta: {...} }`；
2. 入口：账号页「数据操作」菜单新增 `admin.accounts.upstreamOpsImport`，跳转到该子路由。

## 8. CI 边界

- 本地不跑应用 build / 测试（按既有约束）。
- 推送后由 GitHub Actions 覆盖：`frontend` job 的 `make test-frontend`（`lint:check` + `vue-tsc` + 关键 vitest + dev vitest）与 `pnpm run build`。新 spec 已加入 `FRONTEND_CRITICAL_VITEST`，会随关键集执行。
- 不触碰 `release.yml` / tag / VERSION，不产生发布物件。

## 9. 2026-11-02 接线更新

- 导入改为面板内确认：直接调用 `adminAPI.accounts.importData`（`POST /api/v1/admin/accounts/data`），成功/失败逐条展示；**不再提供复制 JSON**，密钥只存在页面内存，导入后立即清空，不进剪贴板、不落 localStorage。
- 去重账号按需分页加载：每页 100、最多 5 页（500 条上限），可随时取消，不再整站预加载。
- URL 硬化：只接受 `http(s)`；`javascript:` / `data:` / `ftp:` / 裸域名一律判 `invalid`（`url_scheme_required`）；query/hash 直接剥掉并标记 `url_query_stripped`，避免 `?key=...` 进入预览、日志或导入载荷。
- 上游源码未复制（项目无 license 声明），仅实现公开 HTTP/JSON 契约。