# 管理面 2FA 触发必要性矩阵（2026-11-22）

- 仓库：`kiss-kedaya/sub2api`
- 基线：`origin/main` @ `24d6d4956`（VERSION 2.3.152）
- 分支：`feat/2fa-trigger-matrix`
- 实现真源：`backend/internal/pkg/adminroutepolicy/policy.go`（默认拒绝 + 显式分类）
- 复现命令（见 §7）

## 0. 结论

| 指标 | 数量 |
| --- | ---: |
| 管理面路由总数（`/api/v1/admin*`，含 payment 管理面） | 641 |
| `machine_allowlisted` | 28 |
| `secret_free` | 405 |
| `step_up_required` | 208 |
| 其中 step-up 按方法 | GET 11 / POST 116 / PUT 54 / DELETE 27 |

本次把 13 条**低风险**管理面动作从 `step_up_required` 降到 `secret_free`（4 条只读 GET + 9 条探测/预览/聚合 POST），
没有放开任何「返回密钥 / 动钱 / 改权限 / 改安全配置」的接口；灰区 10 条列在 §5，等老板确认后再动。
上游模型目录同步（会写库）与预览（接受任意 `base_url`+`api_key` 出站，SSRF 面）经复核**不属于低风险**，
仍留在 `step_up_required`（见 §3 上游账号）。

## 1. 三个分类（policy.go）

| Class | 含义 | 机器凭据（x-api-key / adm_sc_） | 人只会话 |
| --- | --- | --- | --- |
| `machine_allowlisted` | 只读聚合、健康探针、非敏感自动化触发；**不得返回任何秘密** | 允许（仅此一类） | 直接放行 |
| `secret_free` | 显式声明不向调用方暴露秘密材料（Reason 记录理由） | 默认拒绝 | 直接放行 |
| `step_up_required` | 可能返回/改写密钥、资金、权限或安全配置；任何方法（含 GET）都算 | 一律 403 | 需近期 TOTP step-up |

未归类路由在运行时按 step-up fail-closed，且 `admin_route_coverage_test` 直接让 CI 变红。

## 2. 判定口径

矩阵列含义：

- **读取密钥**：响应体是否携带可用凭据/密码/密钥/卡密或优惠码明文/私钥。
- **写入密钥**：请求是否创建、替换、轮换或导入凭据（含 OAuth token、session、S3/SMTP/支付商户密钥）。
- **动钱**：是否改变余额、退款、可兑价值、订阅权益、倍率/价格。
- **改权限**：是否改变用户角色/分组/配额/停用状态，或创建新的机器身份、扩展插件能力。
- **只读/低风险**：不持久化关键状态、不改配置，或只做一次探测/预览/聚合。
- **建议**：`保持 2FA` / `放开`（本次已落地）/ `改为近期登录`（灰区候选，需产品确认后再实现中间档）。

## 3. `step_up_required` 全量矩阵（208 条）

按资源族分组；同一族内的判定依据写在表前。**建议=保持 2FA 的条目本次全部未改动。**

### 卡密（redeem-codes）（6 条）

写操作会生成/改变可兑价值，保留 2FA；读模型已掩码，见 §4。

| 方法+路径 | 读取密钥 | 写入密钥 | 动钱 | 改权限 | 只读/低风险 | 建议 |
| --- | :---: | :---: | :---: | :---: | :---: | --- |
| `DELETE /api/v1/admin/redeem-codes/:id` | 否 | 否 | 是 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/redeem-codes/:id/expire` | 否 | 否 | 是 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/redeem-codes/batch-delete` | 否 | 否 | 是 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/redeem-codes/batch-update` | 否 | 否 | 是 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/redeem-codes/create-and-redeem` | 否 | 否 | 是 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/redeem-codes/generate` | 否 | 否 | 是 | 否 | 否 | 保持 2FA |

### 卡密批量导出（1 条）

| 方法+路径 | 读取密钥 | 写入密钥 | 动钱 | 改权限 | 只读/低风险 | 建议 |
| --- | :---: | :---: | :---: | :---: | :---: | --- |
| `GET /api/v1/admin/redeem-codes/export` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |

### 优惠码（promo-codes）（5 条）

列表/详情仍返回明文优惠码（可兑赠送余额），保留 2FA；使用记录已放开。

| 方法+路径 | 读取密钥 | 写入密钥 | 动钱 | 改权限 | 只读/低风险 | 建议 |
| --- | :---: | :---: | :---: | :---: | :---: | --- |
| `GET /api/v1/admin/promo-codes` | 是 | 否 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/promo-codes` | 否 | 否 | 是 | 否 | 否 | 保持 2FA |
| `DELETE /api/v1/admin/promo-codes/:id` | 否 | 否 | 是 | 否 | 否 | 保持 2FA |
| `GET /api/v1/admin/promo-codes/:id` | 是 | 否 | 否 | 否 | 否 | 保持 2FA |
| `PUT /api/v1/admin/promo-codes/:id` | 否 | 否 | 是 | 否 | 否 | 保持 2FA |

### 上游账号（accounts）（48 条）

建号/改号/导入/duplicate/refresh 会写入或轮换上游凭据（写密钥=是）；其余为账号状态/配置写。刷新 token 类列灰区。
`POST /accounts/:id/models/sync-upstream` 会 `UpdateExtra` 写账号 `upstream_models`（配置写）；
`POST /accounts/models/sync-upstream-preview` 接受请求体 `base_url`+`api_key` 做任意出站（SSRF 面）。
两者均不属于「只读/低风险」，保持 2FA。

| 方法+路径 | 读取密钥 | 写入密钥 | 动钱 | 改权限 | 只读/低风险 | 建议 |
| --- | :---: | :---: | :---: | :---: | :---: | --- |
| `POST /api/v1/admin/accounts` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `DELETE /api/v1/admin/accounts/:id` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |
| `PUT /api/v1/admin/accounts/:id` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/accounts/:id/apply-oauth-credentials` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/accounts/:id/claude/reset-credits/redeem` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/accounts/:id/clear-error` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/accounts/:id/clear-rate-limit` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |
| `PUT /api/v1/admin/accounts/:id/custom-usage-config` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/accounts/:id/duplicate` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `PUT /api/v1/admin/accounts/:id/grok-media-eligibility` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/accounts/:id/model-whitelist-auto-sync/run` | 否 | 否 | 否 | 否 | 否 | 保持 2FA（会写账号模型映射，见灰区） |
| `POST /api/v1/admin/accounts/:id/models/sync-upstream` | 否 | 否 | 否 | 否 | 否 | 保持 2FA（`UpdateExtra` 写 upstream_models，非只读） |
| `PUT /api/v1/admin/accounts/:id/ollama-cloud-usage/auto-refresh` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |
| `DELETE /api/v1/admin/accounts/:id/ollama-cloud-usage/session` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `PUT /api/v1/admin/accounts/:id/ollama-cloud-usage/session` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/accounts/:id/recover-state` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/accounts/:id/refresh` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/accounts/:id/refresh-tier` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/accounts/:id/reset-quota` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/accounts/:id/revert-proxy-fallback` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/accounts/:id/schedulable` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/accounts/:id/schedulable-timers` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |
| `DELETE /api/v1/admin/accounts/:id/schedulable-timers/:action` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/accounts/:id/set-privacy` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/accounts/:id/shadow` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |
| `DELETE /api/v1/admin/accounts/:id/temp-unschedulable` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |
| `PUT /api/v1/admin/accounts/:id/upstream-billing-probe` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/accounts/batch` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/accounts/batch-clear-error` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/accounts/batch-delete` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/accounts/batch-refresh` | 否 | 否 | 否 | 否 | 否 | 保持 2FA（会轮换上游 token，见灰区） |
| `POST /api/v1/admin/accounts/batch-refresh-tier` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/accounts/batch-update-credentials` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/accounts/bulk-update` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/accounts/cookie-auth` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/accounts/data` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/accounts/exchange-code` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/accounts/exchange-setup-token-code` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/accounts/generate-auth-url` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/accounts/generate-setup-token-url` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/accounts/import/codex-session` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/accounts/model-whitelist-auto-sync/run` | 否 | 否 | 否 | 否 | 否 | 保持 2FA（会写账号模型映射，见灰区） |
| `POST /api/v1/admin/accounts/models/sync-upstream-preview` | 否 | 否 | 否 | 否 | 否 | 保持 2FA（接受 `base_url`+`api_key` 任意出站，SSRF 面） |
| `PUT /api/v1/admin/accounts/ollama-cloud-usage/settings` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/accounts/setup-token-cookie-auth` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/accounts/sync/crs` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/accounts/sync/crs/preview` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `PUT /api/v1/admin/accounts/upstream-billing-probe/settings` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |

### 账号凭据回读/导出（2 条）

| 方法+路径 | 读取密钥 | 写入密钥 | 动钱 | 改权限 | 只读/低风险 | 建议 |
| --- | :---: | :---: | :---: | :---: | :---: | --- |
| `GET /api/v1/admin/accounts/:id/credential` | 是 | 否 | 否 | 否 | 否 | 保持 2FA |
| `GET /api/v1/admin/accounts/data` | 是 | 否 | 否 | 否 | 否 | 保持 2FA |

### 代理（proxies）（7 条）

创建/修改携带明文代理密码；列表/详情已掩码，只有导出与写操作 gated。

| 方法+路径 | 读取密钥 | 写入密钥 | 动钱 | 改权限 | 只读/低风险 | 建议 |
| --- | :---: | :---: | :---: | :---: | :---: | --- |
| `POST /api/v1/admin/proxies` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `DELETE /api/v1/admin/proxies/:id` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `PUT /api/v1/admin/proxies/:id` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/proxies/batch` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/proxies/batch-delete` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `GET /api/v1/admin/proxies/data` | 是 | 否 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/proxies/data` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |

### 上游渠道（upstream-monitor/channels）（11 条）

渠道配置与子密钥写、登录、充值/兑换/订阅采购；reveal 读取明文子密钥。公告同步已放开。

| 方法+路径 | 读取密钥 | 写入密钥 | 动钱 | 改权限 | 只读/低风险 | 建议 |
| --- | :---: | :---: | :---: | :---: | :---: | --- |
| `POST /api/v1/admin/upstream-monitor/channels` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `DELETE /api/v1/admin/upstream-monitor/channels/:id` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `PUT /api/v1/admin/upstream-monitor/channels/:id` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/upstream-monitor/channels/:id/api-keys` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `DELETE /api/v1/admin/upstream-monitor/channels/:id/api-keys/:key_id` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `PUT /api/v1/admin/upstream-monitor/channels/:id/api-keys/:key_id` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/upstream-monitor/channels/:id/api-keys/:key_id/reveal` | 是 | 否 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/upstream-monitor/channels/:id/login` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/upstream-monitor/channels/:id/recharge` | 否 | 否 | 是 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/upstream-monitor/channels/:id/redeem` | 否 | 否 | 是 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/upstream-monitor/channels/:id/subscriptions` | 否 | 否 | 是 | 否 | 否 | 保持 2FA |

### 系统设置（settings）（30 条）

整表单 PUT 携带 SMTP/支付/安全密钥；admin-api-key* 创建机器身份；其余为安全/行为配置写。

| 方法+路径 | 读取密钥 | 写入密钥 | 动钱 | 改权限 | 只读/低风险 | 建议 |
| --- | :---: | :---: | :---: | :---: | :---: | --- |
| `PUT /api/v1/admin/settings` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `DELETE /api/v1/admin/settings/admin-api-key` | 否 | 是 | 否 | 是 | 否 | 保持 2FA |
| `POST /api/v1/admin/settings/admin-api-key/regenerate` | 否 | 是 | 否 | 是 | 否 | 保持 2FA |
| `POST /api/v1/admin/settings/admin-api-keys` | 否 | 是 | 否 | 是 | 否 | 保持 2FA |
| `DELETE /api/v1/admin/settings/admin-api-keys/:id` | 否 | 是 | 否 | 是 | 否 | 保持 2FA |
| `PUT /api/v1/admin/settings/admin-api-keys/:id` | 否 | 是 | 否 | 是 | 否 | 保持 2FA |
| `DELETE /api/v1/admin/settings/admin-api-keys/:id/record` | 否 | 是 | 否 | 是 | 否 | 保持 2FA |
| `POST /api/v1/admin/settings/admin-api-keys/:id/rotate` | 否 | 是 | 否 | 是 | 否 | 保持 2FA |
| `PUT /api/v1/admin/settings/beta-policy` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/settings/email-template-preview` | 否 | 否 | 否 | 否 | 是 | 改为近期登录（渲染预览，不回密钥；待确认） |
| `PUT /api/v1/admin/settings/email-templates/:event/:locale` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/settings/email-templates/:event/:locale/restore-official` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |
| `PUT /api/v1/admin/settings/gateway-ip-notice` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/settings/ip-audit/release` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `PUT /api/v1/admin/settings/ip-risk` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/settings/ip-risk/anonymous-unban` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |
| `DELETE /api/v1/admin/settings/ip-risk/events` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/settings/ip-risk/unban` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |
| `PUT /api/v1/admin/settings/key-rotation-risk-ack` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |
| `PUT /api/v1/admin/settings/openai-images-oauth-unavailable-cooldown` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |
| `PUT /api/v1/admin/settings/overload-cooldown` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |
| `PUT /api/v1/admin/settings/panel-rate-limit` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |
| `PUT /api/v1/admin/settings/rate-limit-429-cooldown` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |
| `PUT /api/v1/admin/settings/rectifier` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |
| `PUT /api/v1/admin/settings/registration-email-blacklist` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/settings/send-test-email` | 否 | 否 | 否 | 否 | 否 | 保持 2FA（会真实发信，可被滥用） |
| `PUT /api/v1/admin/settings/stream-timeout` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |
| `PUT /api/v1/admin/settings/theme` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |
| `PUT /api/v1/admin/settings/web-search-emulation` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/settings/web-search-emulation/reset-usage` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |

### 支付（payment）（11 条）

商户密钥配置、退款/取消/重试、价格计划；退款查询列灰区。

| 方法+路径 | 读取密钥 | 写入密钥 | 动钱 | 改权限 | 只读/低风险 | 建议 |
| --- | :---: | :---: | :---: | :---: | :---: | --- |
| `PUT /api/v1/admin/payment/config` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/payment/orders/:id/cancel` | 否 | 否 | 是 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/payment/orders/:id/refund` | 否 | 否 | 是 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/payment/orders/:id/refund/query` | 否 | 否 | 否 | 否 | 是 | 改为近期登录（只查退款状态；支付面待确认） |
| `POST /api/v1/admin/payment/orders/:id/retry` | 否 | 否 | 是 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/payment/plans` | 否 | 否 | 是 | 否 | 否 | 保持 2FA |
| `DELETE /api/v1/admin/payment/plans/:id` | 否 | 否 | 是 | 否 | 否 | 保持 2FA |
| `PUT /api/v1/admin/payment/plans/:id` | 否 | 否 | 是 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/payment/providers` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `DELETE /api/v1/admin/payment/providers/:id` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `PUT /api/v1/admin/payment/providers/:id` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |

### 备份（backups）（8 条）

S3/图床配置写携带对象存储凭据；连通性测试列灰区。

| 方法+路径 | 读取密钥 | 写入密钥 | 动钱 | 改权限 | 只读/低风险 | 建议 |
| --- | :---: | :---: | :---: | :---: | :---: | --- |
| `POST /api/v1/admin/backups` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `DELETE /api/v1/admin/backups/:id` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/backups/:id/restore` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `PUT /api/v1/admin/backups/image-storage` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/backups/image-storage/test` | 否 | 否 | 否 | 否 | 是 | 改为近期登录（S3/图床连通性探测；见灰区） |
| `PUT /api/v1/admin/backups/s3-config` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/backups/s3-config/test` | 否 | 否 | 否 | 否 | 是 | 改为近期登录（S3/图床连通性探测；见灰区） |
| `PUT /api/v1/admin/backups/schedule` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |

### 数据管理（data-management）（11 条）

S3 profile 写携带凭据；s3/test 列灰区。

| 方法+路径 | 读取密钥 | 写入密钥 | 动钱 | 改权限 | 只读/低风险 | 建议 |
| --- | :---: | :---: | :---: | :---: | :---: | --- |
| `POST /api/v1/admin/data-management/backups` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `PUT /api/v1/admin/data-management/config` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/data-management/s3/profiles` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `DELETE /api/v1/admin/data-management/s3/profiles/:profile_id` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `PUT /api/v1/admin/data-management/s3/profiles/:profile_id` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/data-management/s3/profiles/:profile_id/activate` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/data-management/s3/test` | 否 | 否 | 否 | 否 | 是 | 改为近期登录（任意 endpoint 连通性探测；见灰区） |
| `POST /api/v1/admin/data-management/sources/:source_type/profiles` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `DELETE /api/v1/admin/data-management/sources/:source_type/profiles/:profile_id` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `PUT /api/v1/admin/data-management/sources/:source_type/profiles/:profile_id` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/data-management/sources/:source_type/profiles/:profile_id/activate` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |

### 备份读取（2 条）

| 方法+路径 | 读取密钥 | 写入密钥 | 动钱 | 改权限 | 只读/低风险 | 建议 |
| --- | :---: | :---: | :---: | :---: | :---: | --- |
| `GET /api/v1/admin/backups/:id/download-url` | 是 | 否 | 否 | 否 | 否 | 保持 2FA |
| `GET /api/v1/admin/backups/s3-config` | 是 | 否 | 否 | 否 | 否 | 保持 2FA |

### 请求抓包（request-captures）（6 条）

开启/停止会捕获线上真实请求体，涉及用户数据隐私。

| 方法+路径 | 读取密钥 | 写入密钥 | 动钱 | 改权限 | 只读/低风险 | 建议 |
| --- | :---: | :---: | :---: | :---: | :---: | --- |
| `POST /api/v1/admin/request-captures` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `DELETE /api/v1/admin/request-captures/:task` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/request-captures/:task/stop` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `PUT /api/v1/admin/request-captures/config` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `DELETE /api/v1/admin/request-captures/small` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `PUT /api/v1/admin/request-captures/small/config` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |

### 插件（plugins）（7 条）

安装/启用/配置会执行第三方代码并扩展能力（改权限=是）。

| 方法+路径 | 读取密钥 | 写入密钥 | 动钱 | 改权限 | 只读/低风险 | 建议 |
| --- | :---: | :---: | :---: | :---: | :---: | --- |
| `DELETE /api/v1/admin/plugins/:id` | 否 | 是 | 否 | 是 | 否 | 保持 2FA |
| `PUT /api/v1/admin/plugins/:id/config` | 否 | 是 | 否 | 是 | 否 | 保持 2FA |
| `POST /api/v1/admin/plugins/:id/disable` | 否 | 是 | 否 | 是 | 否 | 保持 2FA |
| `POST /api/v1/admin/plugins/:id/enable` | 否 | 是 | 否 | 是 | 否 | 保持 2FA |
| `POST /api/v1/admin/plugins/:id/test` | 否 | 否 | 否 | 否 | 是 | 保持 2FA（会执行插件代码） |
| `POST /api/v1/admin/plugins/:id/ui-session` | 否 | 是 | 否 | 是 | 否 | 保持 2FA |
| `POST /api/v1/admin/plugins/upload` | 否 | 是 | 否 | 是 | 否 | 保持 2FA |

### 账号运维（account-ops）（10 条）

webhook 密钥/通知配置写；测试动作列灰区。

| 方法+路径 | 读取密钥 | 写入密钥 | 动钱 | 改权限 | 只读/低风险 | 建议 |
| --- | :---: | :---: | :---: | :---: | :---: | --- |
| `POST /api/v1/admin/account-ops/admin-defaults/:channel/test` | 否 | 否 | 否 | 否 | 是 | 改为近期登录（向已存 webhook/通道发测试消息；见灰区） |
| `PUT /api/v1/admin/account-ops/config` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `PUT /api/v1/admin/account-ops/groups` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `PUT /api/v1/admin/account-ops/notification-settings` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `DELETE /api/v1/admin/account-ops/rules/:id` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `PUT /api/v1/admin/account-ops/rules/:id` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `PUT /api/v1/admin/account-ops/rules/batch` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `DELETE /api/v1/admin/account-ops/webhooks/:id` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `PUT /api/v1/admin/account-ops/webhooks/:id` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/account-ops/webhooks/:id/test` | 否 | 否 | 否 | 否 | 是 | 改为近期登录（向已存 webhook/通道发测试消息；见灰区） |

### 凭据守卫（credential-guard）（3 条）

relogin/配置写会碰凭据；probe 列灰区。

| 方法+路径 | 读取密钥 | 写入密钥 | 动钱 | 改权限 | 只读/低风险 | 建议 |
| --- | :---: | :---: | :---: | :---: | :---: | --- |
| `PUT /api/v1/admin/credential-guard/:id` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/credential-guard/:id/probe` | 否 | 否 | 否 | 否 | 是 | 保持 2FA（用库内凭据探测） |
| `POST /api/v1/admin/credential-guard/:id/relogin` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |

### Excel 授权（excel-grants）（3 条）

| 方法+路径 | 读取密钥 | 写入密钥 | 动钱 | 改权限 | 只读/低风险 | 建议 |
| --- | :---: | :---: | :---: | :---: | :---: | --- |
| `PUT /api/v1/admin/excel-grants/:id` | 否 | 是 | 是 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/excel-grants/:id/authorize` | 否 | 是 | 是 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/excel-grants/:id/callback` | 否 | 是 | 是 | 否 | 否 | 保持 2FA |

### Mihomo 代理（7 条）

配置写携带代理凭据；节点探测列灰区。

| 方法+路径 | 读取密钥 | 写入密钥 | 动钱 | 改权限 | 只读/低风险 | 建议 |
| --- | :---: | :---: | :---: | :---: | :---: | --- |
| `POST /api/v1/admin/system/mihomo` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `PUT /api/v1/admin/system/mihomo/download-mode` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `PUT /api/v1/admin/system/mihomo/exposure` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/system/mihomo/nodes/:name/quality-check` | 否 | 否 | 否 | 否 | 是 | 改为近期登录（节点连通性探测；见灰区） |
| `POST /api/v1/admin/system/mihomo/nodes/:name/test` | 否 | 否 | 否 | 否 | 是 | 改为近期登录（节点连通性探测；见灰区） |
| `POST /api/v1/admin/system/mihomo/nodes/static-proxies` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/system/mihomo/nodes/static-proxies/remove` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |

### 用户 API Key（3 条）

| 方法+路径 | 读取密钥 | 写入密钥 | 动钱 | 改权限 | 只读/低风险 | 建议 |
| --- | :---: | :---: | :---: | :---: | :---: | --- |
| `PUT /api/v1/admin/api-keys/:id` | 否 | 是 | 否 | 否 | 否 | 保持 2FA |
| `GET /api/v1/admin/groups/:id/api-keys` | 是 | 否 | 否 | 否 | 否 | 保持 2FA |
| `GET /api/v1/admin/users/:id/api-keys` | 是 | 否 | 否 | 否 | 否 | 保持 2FA |

### 资金/权限操作（users/subscriptions/affiliates/groups）（23 条）

余额/退款/可兑权益/倍率价格，以及用户角色/分组/配额/停用等权限变更。

| 方法+路径 | 读取密钥 | 写入密钥 | 动钱 | 改权限 | 只读/低风险 | 建议 |
| --- | :---: | :---: | :---: | :---: | :---: | --- |
| `DELETE /api/v1/admin/affiliates/users/:user_id` | 否 | 否 | 是 | 是 | 否 | 保持 2FA |
| `PUT /api/v1/admin/affiliates/users/:user_id` | 否 | 否 | 是 | 是 | 否 | 保持 2FA |
| `POST /api/v1/admin/affiliates/users/:user_id/codes` | 否 | 否 | 是 | 是 | 否 | 保持 2FA |
| `POST /api/v1/admin/affiliates/users/batch-rate` | 否 | 否 | 是 | 是 | 否 | 保持 2FA |
| `DELETE /api/v1/admin/groups/:id/rate-multipliers` | 否 | 否 | 是 | 否 | 否 | 保持 2FA |
| `PUT /api/v1/admin/groups/:id/rate-multipliers` | 否 | 否 | 是 | 否 | 否 | 保持 2FA |
| `DELETE /api/v1/admin/subscriptions/:id` | 否 | 否 | 是 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/subscriptions/:id/extend` | 否 | 否 | 是 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/subscriptions/:id/reset-quota` | 否 | 否 | 是 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/subscriptions/:id/restore` | 否 | 否 | 是 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/subscriptions/:id/revoke` | 否 | 否 | 是 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/subscriptions/assign` | 否 | 否 | 是 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/subscriptions/bulk-assign` | 否 | 否 | 是 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/users` | 否 | 否 | 是 | 是 | 否 | 保持 2FA |
| `DELETE /api/v1/admin/users/:id` | 否 | 否 | 是 | 是 | 否 | 保持 2FA |
| `POST /api/v1/admin/users/:id/auth-identities` | 否 | 否 | 是 | 是 | 否 | 保持 2FA |
| `POST /api/v1/admin/users/:id/balance` | 否 | 否 | 是 | 是 | 否 | 保持 2FA |
| `PUT /api/v1/admin/users/:id/platform-quotas` | 否 | 否 | 是 | 是 | 否 | 保持 2FA |
| `POST /api/v1/admin/users/:id/platform-quotas/reset` | 否 | 否 | 是 | 是 | 否 | 保持 2FA |
| `POST /api/v1/admin/users/:id/replace-group` | 否 | 否 | 是 | 是 | 否 | 保持 2FA |
| `POST /api/v1/admin/users/batch-concurrency` | 否 | 否 | 是 | 是 | 否 | 保持 2FA |
| `POST /api/v1/admin/users/batch-disable` | 否 | 否 | 是 | 是 | 否 | 保持 2FA |
| `POST /api/v1/admin/users/batch-limits` | 否 | 否 | 是 | 是 | 否 | 保持 2FA |

### 主机生命周期（system）（3 条）

换二进制/回滚/重启，一次请求改变整机运行态。

| 方法+路径 | 读取密钥 | 写入密钥 | 动钱 | 改权限 | 只读/低风险 | 建议 |
| --- | :---: | :---: | :---: | :---: | :---: | --- |
| `POST /api/v1/admin/system/restart` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/system/rollback` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |
| `POST /api/v1/admin/system/update` | 否 | 否 | 否 | 否 | 否 | 保持 2FA |

### 调试 dump（1 条）

| 方法+路径 | 读取密钥 | 写入密钥 | 动钱 | 改权限 | 只读/低风险 | 建议 |
| --- | :---: | :---: | :---: | :---: | :---: | --- |
| `GET /api/v1/admin/debug-dumps/:name` | 是 | 否 | 否 | 否 | 否 | 保持 2FA |

## 4. 本次放开（13 条，已落地）

原则：**不返回密钥、不动钱、不改权限、不改安全配置**。所有条目仍不是 `machine_allowlisted`，
机器凭据照旧默认拒绝；卡密只读三条另有 `middleware.ForbidMachineCredentials` 显式拦截。

| 方法+路径 | 新分类 / Reason | 证据（handler 返回面） |
| --- | --- | --- |
| `GET /api/v1/admin/redeem-codes` | secret_free / redeem_code_reads_return_masked_values_only | `dto.RedeemCode.Code` 恒为掩码（前 4 位 + ****），明文只在 POST /generate 回执出现一次；路由层已挂 `ForbidMachineCredentials` |
| `GET /api/v1/admin/redeem-codes/:id` | secret_free / redeem_code_reads_return_masked_values_only | 同上；`RedeemCodeFromServiceAdmin` 走掩码 DTO |
| `GET /api/v1/admin/redeem-codes/stats` | secret_free / redeem_code_reads_return_masked_values_only | handler 当前返回 501 Not Implemented，不返回任何财务数字 |
| `GET /api/v1/admin/promo-codes/:id/usages` | secret_free / promo_usage_reads_exclude_grant_code | `dto.PromoCodeUsage` 无 code 字段，只回 user/bonus/used_at |
| `POST /api/v1/admin/proxies/:id/test` | secret_free / proxy_connectivity_and_quality_test_no_secret | 用库内代理探测出口 IP/延迟，响应无代理用户名密码 |
| `POST /api/v1/admin/proxies/:id/quality-check` | secret_free / proxy_connectivity_and_quality_test_no_secret | 同上，只回出口地区/评分/目标可达性 |
| `POST /api/v1/admin/accounts/check-mixed-channel` | secret_free / account_binding_preflight_check_no_secret | 纯计算，只回 has_risk/风险详情 |
| `POST /api/v1/admin/accounts/:id/schedulable-timers/preview` | secret_free / account_schedulable_timer_preview_no_secret | 只回算出的 execute_at/local_date/local_time，不落库 |
| `POST /api/v1/admin/accounts/:id/ollama-cloud-usage/refresh` | secret_free / read_only_account_usage_aggregate | 响应为脱敏用量窗口（used_percent/reset_at/model 计数），不含 session/HTML |
| `POST /api/v1/admin/upstream-monitor/channels/:id/announcements/sync` | secret_free / channel_announcement_sync_counters_no_secret | 响应只有 fetched/added/updated/unread 计数，无渠道密码/子密钥/会话 |
| `POST /api/v1/admin/settings/test-smtp` | secret_free / smtp_connectivity_test_no_secret | 只回成功或分类失败文案（不回显 host:port），不含 SMTP 密码；发信仍走 send-test-email |
| `POST /api/v1/admin/settings/web-search-emulation/test` | secret_free / web_search_emulation_probe_no_secret | 只回 provider/query/results，不含 provider api_key |
| `POST /api/v1/admin/settings/ip-risk/check` | secret_free / ip_risk_preflight_check_no_secret | 只回该 IP 的风险判定明细，不改封禁状态 |

保留（明确不放开）：`POST /settings/send-test-email` 会真实发信，可被滥用，保持 2FA。

## 5. 需要老板确认的灰区（10 条，本次未动）

| # | 路由 | 判断与建议 |
| --- | --- | --- |
| 1 | `POST /api/v1/admin/backups/s3-config/test` | 请求体可带任意 endpoint/AK/SK，返回原始错误信息 → 内网连通性/端口探测面。建议改为近期登录，或服务端只允许已保存配置测试。 |
| 2 | `POST /api/v1/admin/backups/image-storage/test` | 图床/对象存储连通性探测，同上（任意 endpoint）。建议改为近期登录。 |
| 3 | `POST /api/v1/admin/data-management/s3/test` | 同上；失败时 `response.ErrorFrom` 回原始错误，可能泄露内网 host/端口细节。建议改为近期登录并统一脱敏。 |
| 4 | `POST /api/v1/admin/account-ops/webhooks/:id/test` | 向已保存 webhook 真实发一条测试消息，可能把内部信息推给错误接收方。建议改为近期登录或保持 2FA。 |
| 5 | `POST /api/v1/admin/account-ops/admin-defaults/:channel/test` | 向管理员本人通道发测试通知（主管理员限定）。建议改为近期登录。 |
| 6 | `POST /api/v1/admin/system/mihomo/nodes/:name/test 与 .../quality-check` | 节点连通性探测，只回延迟/状态；但 Mihomo 配置本身含代理凭据。建议改为近期登录。 |
| 7 | `POST /api/v1/admin/payment/orders/:id/refund/query` | 只查询退款状态、不动钱，但属支付面；建议改为近期登录（需老板确认）。 |
| 8 | `POST /api/v1/admin/accounts/:id/model-whitelist-auto-sync/run（及 batch）` | 拉上游模型后写回账号 model_mapping（有 CAS/lease 保护），属改路由配置。建议保持 2FA。 |
| 9 | `POST /api/v1/admin/accounts/:id/refresh、/:id/refresh-tier、/batch-refresh、/batch-refresh-tier` | 会轮换/刷新上游 OAuth token 并改账号状态，等同写凭据。建议保持 2FA。 |
| 10 | `POST /api/v1/admin/accounts/sync/crs 与 /sync/crs/preview` | CRS 同步可能落/回凭据，需确认真实返回字段后决定。建议保持 2FA。 |

## 6. 明确保留 2FA 的类别

- **凭据回读/导出**：`GET /accounts/:id/credential`、`GET /accounts/data`、`GET /proxies/data`、`GET /backups/:id/download-url`、`GET /backups/s3-config`、`GET /debug-dumps/:name`、`GET /users/:id/api-keys`、`GET /groups/:id/api-keys`、`PUT /api-keys/:id`
- **批量导出/备份**：`GET /redeem-codes/export`（掩码 CSV 但一次最多 1 万条）、备份创建/恢复/删除、data-management S3 profile 写
- **支付密钥与资金**：`PUT /payment/config`、payment providers/plans 增删改、订单退款/取消/重试、`POST /users/:id/balance`、subscriptions/*、affiliates/*、`groups/*/rate-multipliers`
- **权限/身份变更**：`POST /users`（可带初始余额与授权分组）、`DELETE /users/:id`、`POST /users/:id/auth-identities`、`POST /users/:id/replace-group`、`POST /users/batch-*`、`PUT /users/:id/platform-quotas`、settings/admin-api-key* 增删改轮换
- **安全配置/2FA 相关**：`PUT /settings`、`PUT /settings/ip-risk`、`POST /settings/ip-risk/unban`、`DELETE /settings/ip-risk/events`、`POST /settings/send-test-email`（可被滥用发信）、email template 写、web-search-emulation reset-usage
- **危险主机生命周期**：`POST /system/update`、`/system/rollback`、`/system/restart`
- **插件代码/配置**：`POST /plugins/upload`、`/plugins/:id/enable|disable|config|ui-session|test`
- **卡密/优惠码写操作**：`/redeem-codes` 的 generate/create-and-redeem/batch-update/delete/expire、`/promo-codes` 写操作

## 7. 测试与复现

```bash
cd backend
# 1) 构建
go build ./...

# 2) 策略 + 路由门控回归
go test -tags=unit ./internal/pkg/adminroutepolicy/... ./internal/server/routes/...

# 3) 运行时分类 dump（与 adminStepUpRouteGuard 同源）
ADMIN_ROUTE_POLICY_DUMP=1 go test -tags=unit ./internal/server/routes \
  -run TestAdminRoutePolicyRuntimeClassificationDump -v

# 4) 本矩阵的数据来源（临时 dump，不随 PR 提交）
go test -tags=unit ./internal/server/routes -run TestDumpAdminRouteMatrix -v
```

新增/改分类的回归断言：

- `internal/pkg/adminroutepolicy/policy_test.go`
  - `TestRedeemCodeReadsAreMaskedAndSecretFree`
  - `TestPromoCodeUsageReadIsSecretFreeWhileCodeReadsStayGated`
  - `TestLowRiskAdminProbesAreSecretFree`
  - `TestLowRiskAdminProbesDoNotCrossSensitiveBoundaries`
  - `TestMaskedReadExactEntriesShape`
- `internal/server/routes/admin_low_risk_release_test.go`
  - `TestLowRiskReleasedAdminRoutesAreSecretFree`
  - `TestLowRiskReleasedAdminRoutesBypassStepUpGate`
  - `TestSensitiveBoundariesStillStepUpGated`
- `internal/server/routes/admin_route_coverage_test.go`（必须仍全绿：641 条全部归类）
- `internal/server/routes/admin_read_step_up_ux_test.go`（只读 step-up 白名单同步收缩）
