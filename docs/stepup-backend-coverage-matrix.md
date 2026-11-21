# 管理端 step-up 2FA 后端覆盖矩阵（独立审计）

- 审计日期：2026-11-21
- 基线：`origin/main` @ `3be7f6a7b`
- 分支：`audit/stepup-backend-matrix`
- 关联：`fix/stepup-default-deny`（默认拒绝机器凭据 + 路由归类注册表，另一分支并行进行）

## 0. 结论（先看这里）

| 指标 | 数值 |
| --- | ---: |
| `routes.Setup` 装配到的全量路由（admin + payment + plugin-ui） | 654 |
| 管理面路由（`/api/v1/admin*`） | 632 |
| `origin/main` 已挂**路由级** step-up 的管理面路由 | 64（10.1%） |
| 本 PR 后已挂路由级 step-up | 75 |
| 注册表判定「需 step-up」（另一分支，含只读 GET） | 382（其中只读 GET 146） |
| 注册表判定「机器凭据白名单」 | 28 |
| 注册表未归类管理面路由 | 0（632/632 有归类） |

三句话：

1. `origin/main` 的 step-up 是逐路由 opt-in，632 条管理面路由只有 64 条挂了门控，其余 568 条中 318 条按敏感资源分类「该挂没挂」——这是老板看到「2FA 可绕过」的根因，属架构缺口而非漏一行。
2. 该架构缺口由并行的「默认拒绝机器凭据 + 集中分类注册表」分支解决（**本 PR 不重复造注册表**，只做独立复核 + 补齐注册表口径没覆盖到的真实缺口）。
3. 本 PR 独立发现并修复 11 条路由级缺口 + 2 个 handler 级条件门控：分组改价、批量用户权限/配额变更、主机生命周期（部署/回滚/重启）。

## 1. 审计方法（可复现）

gin 路由树全量 dump，不靠人肉读 `routes/admin.go`：

```bash
cd backend
go test -tags=unit ./internal/server/routes -run 'TestStepUpMatrixDump' -count=1 -v
```

- 用**生产装配方式**注册 `RegisterAdminRoutes` / `RegisterPaymentRoutes`，条件注册的 handler（`UpstreamChannel` / `IPRisk` / `RequestCapture` / `UpstreamMonitor` / `PromptAudit` / `AccountOps` / `Mihomo` / `Serverless` / `ExcelGrant` / `OpsAutomation` / `CredentialGuard` / `Crypto`）全部用零值占位，保证「handler 是否装配」不会让矩阵漏族。
- 把 `stepUpAuth` 换成只打标记的探针，对每条已注册路由发一次请求：响应头带标记 = 该路由挂了**路由级** step-up。
- 输出字段：`METHOD<TAB>ROUTE<TAB>gated|ungated`。
- `TestStepUpMatrixKeepsDocumentedRouteGates` 是回归闸门：文档里列出的敏感路由若被人删掉 `stepUpAuth` 包装，CI 直接红。

## 2. 分类矩阵（按任务给定的「是否返回/修改敏感数据」口径）

判定口径：**只要响应体或请求体涉及秘密材料（卡密明文、上游凭证、代理密码、渠道密钥、用户 API Key、私钥/助记词、支付商户密钥、webhook secret、SMTP/S3 凭据、抓包正文、dump 正文），或动作直接移动/改变资金与权限（余额、退款、改价、批量停用、部署回滚），即算敏感；只读同样算。**

| # | 分类 | 涉及路由族（管理面） | `origin/main` 现状 | 修复归属 |
| --- | --- | --- | --- | --- |
| 1 | 卡密 / 兑换码 | `redeem-codes`(10)、`promo-codes`(6) | 生成/改值/作废/删除/批量/导出 7+3 条已挂；列表/详情/统计只读不要求 step-up，但 `#489` 已用 `ForbidMachineCredentials()` 拒绝机器凭据（`MACHINE_CREDENTIAL_FORBIDDEN`） | 已覆盖（人只会话可读）；注册表分支会再要求 step-up |
| 2 | 上游凭证 | `accounts`(78)、`upstream-monitor`(33)、`channels`(10)、`openai/gemini/antigravity/grok` OAuth(28) | 仅 `accounts/:id/credential`、`accounts/data` 2 条 + 渠道凭据 10 条已挂；**账号列表/详情、上游渠道配置读、渠道密钥列表均未挂** | 注册表分支：`accounts`/`channels`/`upstream-monitor` 前缀 + provider OAuth 写操作 |
| 3 | 代理（含密码） | `proxies`(14) | 仅 `proxies/data`、`proxy` 导出类 1 条；**代理列表/详情返回密码未挂** | 注册表分支：`/proxies` 前缀全方法 |
| 4 | 渠道密钥 | `channels`(10)、`upstream-monitor` 渠道子密钥 | 子密钥创建/修改/删除/reveal 4 条已挂；`GET /channels`、`GET /channels/:id`、`GET .../api-keys` 未挂 | 注册表分支 |
| 5 | 用户 API Key | `users/:id/api-keys`、`groups/:id/api-keys`、`api-keys/:id` | 3 条查看/改绑已挂 | 已覆盖 + 注册表分支兜底 |
| 6 | 钱包/私钥/提现 | `crypto`(40) | 路由级 0，但整组由 `CryptoAdminSecurity` **组级 2FA**（`requireCryptoRisk`）保护：私钥导出、助记词导出、提现确认、归集、水龙头等均需二次验证 | 已完成（独立机制，错误码非 `STEP_UP_REQUIRED`，见 §5 第 5 条） |
| 7 | 支付服务商配置 | `payment`(17) | 支付配置/服务商增删改/退款 6 条已挂；**`GET /payment/config`、`GET /payment/providers`（含商户密钥配置）未挂** | 注册表分支：`/payment` 前缀全方法 |
| 8 | webhook secret | `payment`、`settings`、`account-ops/webhooks` | account-ops webhook 保存/删除/测试已 handler 级 `RequireStepUp`；`settings` PUT 已挂；GET 配置未挂 | 注册表分支 |
| 9 | SMS / SMTP / S3 配置 | `settings`(53)、`data-management`(17)、`backups`(14) | `PUT /settings` + S3 profile 写 + 备份写/下载/恢复 16 条已挂；**`GET /settings`（含 SMTP/支付/验证码配置的脱敏读）、`GET /backups/s3-config`、`GET /data-management/s3/profiles` 未挂** | 注册表分支 |
| 10 | 导出类 | `accounts/data`、`proxies/data`、`redeem-codes/export`、`backups/:id/download-url`、`debug-dumps/:name`、`request-captures/:task/export`、`crypto/deposits.csv` | 前 5 条已挂；`request-captures` 导出由 handler 级 `EnforceStepUpAlways` 拦截；`crypto` 由组级 2FA 拦截 | 已覆盖 |
| 11 | 批量删除 | `redeem-codes/batch-delete`、`accounts/batch-delete`、`proxies/batch-delete`、`prompt-audit/events/batch-delete`、`data-management` 删备份 | 仅 redeem 已挂；其余未挂（一条机器凭据可删成批数据） | 注册表分支（accounts/proxies/prompt-audit/data-management 前缀） |
| 12 | 余额 / 退款 / 改价 | `users/:id/balance`、`payment/orders/:id/refund*`、`subscriptions/*`、`affiliates/*`、`groups/*` | 余额/退款/订阅/返利 20 条已挂；**分组改价（倍率/各模态单价/利润阈值）完全无门控** | **本 PR 修复**（见 §4） |
| 13 | 2FA / TOTP 重置 | 无「管理员重置他人 TOTP」路由 | 用户 TOTP 自助走 `/api/v1/user/totp/*`（本人会话 + 密码/验证码）；管理端 `settings/admin-api-keys/:id/rotate` 已挂 step-up | 无缺口（已确认不存在该能力） |
| 14 | 用户权限变更 | `users/*`(23) | `PUT /users/:id`（角色/状态/分组/倍率/改密/管理员邮箱）与 `POST /users`（建管理员）已在 handler 内按真实改动做条件 step-up；**`batch-disable`/`batch-limits`/`batch-concurrency`/`replace-group`/`platform-quotas` 未挂** | **本 PR 修复**（见 §4） |
| 15 | 主机生命周期 | `system`(14) | `update`/`rollback`/`restart` 完全无 2FA（可被一次管理端请求换二进制/重启） | **本 PR 修复** |

## 3. 按路由族统计（管理面 632 条）

| 路由族 | 路由数 | origin/main 路由级 step-up | 本 PR 后 |
| --- | ---: | ---: | ---: |
| `accounts` | 78 | 2 | 2 |
| `settings` | 53 | 7 | 7 |
| `ops` | 50 | 0 | 0 |
| `crypto` | 40 | 0（组级 2FA） | 0（组级 2FA） |
| `upstream-monitor` | 33 | 10 | 10 |
| `groups` | 25 | 1 | 3 |
| `users` | 23 | 4 | 10 |
| `data-management` | 17 | 4 | 4 |
| `payment` | 17 | 6 | 6 |
| `request-captures` | 17 | 0（handler 级） | 0（handler 级） |
| `account-ops` | 15 | 0（handler 级） | 0（handler 级） |
| `backups` | 14 | 5 | 5 |
| `proxies` | 14 | 1 | 1 |
| `system` | 14 | 0 | 3 |
| `dashboard` | 13 | 0 | 0 |
| `grok` | 13 | 0 | 0 |
| `usage` | 12 | 0 | 0 |
| `affiliates` | 11 | 0 | 0 |
| `channels` | 10 | 0 | 0 |
| `plugins` | 10 | 6 | 6 |
| `prompt-audit` | 10 | 0 | 0 |
| `redeem-codes` | 10 | 7 | 7 |
| `subscriptions` | 10 | 7 | 7 |
| 其余 31 个族（openai/gemini/grok OAuth、监控、票据、公告、审计、属性、风控等） | 113 | 0 | 0 |

> 本 PR 后全部 75 条路由级 step-up 路由的完整列表，由 `TestStepUpMatrixDump` 输出（`gated` 行），不在此重复，避免文档与代码双写漂移。

## 4. 本 PR 修复清单

### 4.1 路由级 step-up 新增（11 条）

| 方法 | 路由 | 分类 | 修复前 | 修复后 |
| --- | --- | --- | --- | --- |
| PUT | `/api/v1/admin/groups/:id/rate-multipliers` | 改价 | 无门控 | 403 `STEP_UP_REQUIRED` / 机器凭据 403 `STEP_UP_ADMIN_API_KEY_FORBIDDEN` |
| DELETE | `/api/v1/admin/groups/:id/rate-multipliers` | 改价 | 无门控 | 同上 |
| POST | `/api/v1/admin/users/batch-concurrency` | 批量权限变更 | 无门控 | 同上 |
| POST | `/api/v1/admin/users/batch-limits` | 批量权限变更 | 无门控 | 同上 |
| POST | `/api/v1/admin/users/batch-disable` | 批量权限变更 | 无门控 | 同上 |
| POST | `/api/v1/admin/users/:id/replace-group` | 用户权限变更 | 无门控 | 同上 |
| PUT | `/api/v1/admin/users/:id/platform-quotas` | 配额/额度变更 | 无门控 | 同上 |
| POST | `/api/v1/admin/users/:id/platform-quotas/reset` | 配额/额度变更 | 无门控 | 同上 |
| POST | `/api/v1/admin/system/update` | 主机生命周期 | 无门控 | 同上 |
| POST | `/api/v1/admin/system/rollback` | 主机生命周期 | 无门控 | 同上 |
| POST | `/api/v1/admin/system/restart` | 主机生命周期 | 无门控 | 同上 |

### 4.2 Handler 级条件门控新增（分组改价的通用入口）

只挂专用端点不够——攻击面还在 `POST /groups`（建组带非默认倍率/单价）与 `PUT /groups/:id`（改倍率/单价/利润阈值/写 `search_provider_api_key`）。这两条不能一刀切（日常改名称/平台/路由不该弹 2FA），所以做**按真实改动判定**：

- `POST /api/v1/admin/groups`：请求体带非默认倍率（≠1）、任一价格指针/映射、`search_provider_base_url`/`search_provider_api_key` → 要求 step-up。
- `PUT /api/v1/admin/groups/:id`：先读存储分组，逐项比较 `rate_multiplier`、高峰倍率、`model_pricing`、图片/视频/音频/搜索/OCR/Grok 单价、利润控制阈值、`search_provider_api_key`；只有**真实改变**才要求 step-up，前端整表单回传不会误伤日常编辑。
- 依赖缺失时 fail-closed 返回 503 `STEP_UP_UNAVAILABLE`（与现有 `AnnouncementHandler` 同口径），不允许装配遗漏把门控静默关掉。

## 5. 独立复核：另一分支的「默认拒绝 + 归类注册表」

复核对象：`fix/stepup-default-deny` 分支的 `backend/internal/pkg/adminroutepolicy/policy.go`（分类注册表）与 `admin_machine_guard.go`（机器凭据默认拒绝）。

### 5.1 复核通过项

- 归类完整性：把本审计的 632 条管理面路由喂给 `Resolve`，**0 条 unclassified**（machine_allowlisted 28 / step_up_required 382 / secret_free 222）。
- 机器凭据白名单：28 条全部能在真实路由树中找到（无幽灵路由），且全部落在注册表声明为 `secret_free` 的家族（告警/健康探针/聚合统计/版本/模型注册表刷新），无一触及敏感资源前缀。
- 默认拒绝语义：注册表之外的路由按 fail-closed 视为 step-up required；`STEP_UP_ADMIN_API_KEY_FORBIDDEN` 与功能开关无关（关闭 `step_up_enabled` 也不能让机器凭据过敏感路由）。

### 5.2 复核发现（需要配合处理）

1. **`/admin/groups` 整族被判 `secret_free`，与「改价」冲突。** 该族包含 `rate_multiplier`、`model_pricing`、图片/视频/音频单价、利润控制阈值、`search_provider_api_key` 写入口，是资金/凭据敏感面。本 PR 已补 2 条专用端点路由级门控 + 2 个通用入口的 handler 级条件门控。**合并后不需要注册表再改**（两组门控可共存，重复校验无副作用）；若希望单一入口，可把 `groups` 的改价子集在注册表标 `step_up_required`，本 PR 的包装可保留作兜底。
2. **`/admin/users` 整族被判 `secret_free`，与「批量权限变更 / 批量停用」冲突。** 单个用户编辑已有 handler 级条件 step-up，但批量接口没有；一条机器凭据可成批停用/改配额。本 PR 已补 6 条路由级门控。
3. **`/admin/system` 被标 `secret_free`，但 `stepUpExact` 已把 `update/rollback/restart` 标为 step-up**——注册表内部口径自洽，`origin/main` 这三条实际没有任何门控。本 PR 提前落地路由级门控，注册表分支合并后为双保险。
4. **注册表里的 `/api/v1/admin/pages` 是幽灵前缀**：真实管理页路由是 `GET /api/v1/pages`（`handler.RegisterPageRoutes` 用 `adminAuth` 挂载，返回页面目录，无敏感数据）。不影响安全（机器凭据在 `adminAuth` 里会被默认拒绝），但建议删掉幽灵前缀，避免误导。
5. **`crypto` 双门控/双错误码**：注册表把 40 条 `/admin/crypto` 全方法标 step-up，而这些路由本就有 `CryptoAdminSecurity` 的独立 2FA（`requireCryptoRisk`，返回自己的错误码，不是 `STEP_UP_REQUIRED`）。合并后真人 JWT 会先撞 step-up 弹框、再撞 crypto 弹框。建议二选一：注册表把 `crypto` 标 `secret_free` 交给 crypto 自身门控（推荐，避免双弹框），或前端同时兼容 `CRYPTO_*` 与 `STEP_UP_*` 错误码。
6. **有意保留、不再加 2FA 的破坏性/内容类接口**（不是秘密材料，且加门控会拖垮日常运营）：`POST /admin/audit-logs/clear`（已硬禁用，固定 403 `AUDIT_LOG_CLEAR_DISABLED`）、`POST /admin/ops/system-logs/cleanup`、`/admin/tickets/:id` 内容、`/admin/request-captures` 列表（正文读取 `sensitive-access`/导出已有 handler 级门控）。

## 6. 错误码契约（前端弹框依据）

| 错误码 | HTTP | 触发条件 | 前端动作 |
| --- | --- | --- | --- |
| `STEP_UP_REQUIRED` | 403 | 真人 JWT、已启用 TOTP、当前会话无有效 step-up 授权 | 弹 TOTP 框，验证后重试 |
| `STEP_UP_TOTP_NOT_ENABLED` | 403 | 真人 JWT 但账号未启用 TOTP | 引导启用 2FA |
| `STEP_UP_ADMIN_API_KEY_FORBIDDEN` | 403 | 机器凭据（旧版全权 `x-api-key` / 受限 `adm_sc_` 密钥）访问 step-up 敏感路由（开关关闭也一样） | 提示改用真人会话 |
| `STEP_UP_UNAVAILABLE` | 503 | 授权服务/开关读取故障、装配缺失（fail-closed） | 提示稍后重试 |
| `MACHINE_CREDENTIAL_FORBIDDEN` | 403 | 机器凭据访问「只读但成批敏感」接口（`ForbidMachineCredentials()`，#489：卡密列表/详情/统计） | 提示改用真人会话 |

一致性由一个共享中间件保证（`middleware.enforceStepUp`），本 PR 增加全量断言：

- `TestSensitiveAdminRoutesReturnStepUpRequiredCode`：75 条敏感路由逐条断言 JSON `code == "STEP_UP_REQUIRED"`（不是只有 message 含关键字）。
- `TestSensitiveAdminRoutesReturnAdminAPIKeyForbiddenCode`：机器凭据逐条断言 `code == "STEP_UP_ADMIN_API_KEY_FORBIDDEN"`。
- `TestSensitiveAdminRoutesAllowedAfterStepUpGrant`：完成 step-up 后逐条断言不再被门控拦截。

## 7. 验证

```bash
cd backend
go build ./...
go test -tags=unit ./internal/server/routes -run 'TestStepUpMatrix|TestSensitiveAdminRoutes|TestGatedAdminRoutes|TestReadOnlyAdminRoutes' -count=1
go test -tags=unit ./internal/handler/admin -run 'TestGroupPricingChange|TestGroupNonPricing|TestGroupSearchProvider' -count=1
go test -tags=unit ./internal/handler/admin -run 'TestAdminGroupNumericContract' -count=1   # 改价的 handler 改动不破坏既有数值契约
```

回归重点：

- 只读分组倍率 `GET /groups/:id/rate-multipliers` 明确**不**被门控（只读当前配置，不是改价），由 `readOnlyStepUpRoutes` 守护。
- `step_up_enabled=false` 时保持门控引入前的行为（含机器凭据），由 `TestSensitiveAdminRoutesStepUpDisabledKeepsLegacyBehavior` 与 `TestGroupPricingChangeStepUpDisabledKeepsLegacyBehavior` 守护。
- 分组名/描述/平台等非价格编辑不触发门控（`TestGroupNonPricingEditSkipsStepUp`）。
