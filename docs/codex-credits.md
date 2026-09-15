# Codex 点数查询

Codex 源码核对版本：`41ece455b7fa7166f4fc38522952afdaa2604e18`。

- `codex-rs/backend-client/src/client/rate_limit_resets.rs`：`get_rate_limit_status()` 使用 `GET /wham/usage`；ChatGPT 完整地址为 `https://chatgpt.com/backend-api/wham/usage`。Codex API 路由模式使用 `/api/codex/usage`。
- `codex-rs/backend-client/src/client.rs`：`headers()` 添加身份认证和 `ChatGPT-Account-Id`，`map_credits()` 读取点数对象。
- `codex-rs/codex-backend-openapi-models/src/models/credit_status_details.rs`：`has_credits`、`unlimited` 为布尔值，`balance` 为可缺省、可空的字符串。
- `codex-rs/codex-api/src/rate_limits.rs`：推理响应也可以通过 `x-codex-credits-has-credits`、`x-codex-credits-unlimited` 和 `x-codex-credits-balance` 响应头携带点数快照。

点数是 `credits`；重置卡是 `rate_limit_reset_credits`。两者独立，不能混用。点数余额按原始十进制字符串保留，不折算成 sub2api 用户余额或美元。

## sub2api 接入

复用现有 OAuth token 刷新、账号绑定及代理客户端。管理员账户用量区域点击「点数」或「次数」，通过 `POST /api/v1/admin/openai/accounts/:id/quota/refresh` 一次刷新两种数据。只读查询仍使用 `GET /api/v1/admin/openai/accounts/:id/quota`。

`credits` 随额度查询和重置后的查询结果返回。点数缓存保存在账户 `extra.codex_credits_snapshot`，包含 `credits` 和 Unix 秒时间戳 `fetched_at`，无需数据库迁移。`credits_cache_persisted` 单独报告点数缓存是否保存成功，不受重置卡到期明细缺失影响。余额按钮提示中显示查询时间，点击可刷新。

- 有限点数：显示余额字符串，保留小数。
- `unlimited: true`：显示「无限」，优先于其他字段。
- `has_credits: false`：显示 `0`。
- 有点数但余额未公开：显示「可用」。
- 缺失或空的 `credits`：显示 `—`，不当作零余额；成功刷新会清除旧余额快照。
- 请求失败：保留最近一次查询结果并显示错误。
- Spark 影子账号：使用既有母账号凭证解析逻辑查询，缓存保存在被查询行。

本接入只查询和展示点数，不触发购买或消费；原有重置操作仍只使用重置卡。

## 可邀请次数与邀请用户

邀请接口核对来源为本机桌面应用 `26.908.40834` 的 `app.asar/webview/assets/app-primary-44ec287874b7.js`，对应 `SIt`（资格查询）、`EIt`（发送）、`PIt`（邀请计划）、`FIt`（次数约束）。邀请功能不在本机 Rust CLI 源码中。

上游接口：

```http
GET https://chatgpt.com/backend-api/referrals/invite/eligibility?program_id=codex_referral_consumer&entrypoint=persistent
POST https://chatgpt.com/backend-api/referrals/invite
Content-Type: application/json

{"program_id":"codex_referral_consumer","entrypoint":"persistent","emails":["friend@example.com"]}
```

个人邀请使用 `codex_referral_consumer`，工作空间邀请使用 `codex_referral_workspace`。sub2api 优先使用已保存的 `account_type` 判断工作空间，否则根据 `plan_type` 选择。资格接口返回的 `should_show` 决定是否可发送；`remaining_send_capacity` 是剩余发送次数，有奖励时还受 `remaining_reward_capacity` 限制。保留空值表示未知，不硬编码历史活动的邀请上限。每次发送一个邮箱。

管理员入口：账户用量区域的「可邀请」查询按钮与「邀请用户」弹窗。支持以下接口：

- `POST /api/v1/admin/openai/accounts/:id/referrals/refresh`：查询并缓存资格和次数到 `extra.codex_referral_snapshot`，包含查询时间。返回 `eligibility` 和 `cache_persisted`。
- `POST /api/v1/admin/openai/accounts/:id/referrals/invite`：请求体为 `email`、`program_id`、`confirmed`。后端重新查询资格，验证计划、邮箱、次数及收件人同意状态，再发送邀请。

活动说明和资格规则显示上游的 `title`、`description` 和 `rules`。`requires_explicit_confirmation` 不为 `false` 时必须勾选收件人同意；此值不伪造为已同意。Spark 影子账号可查询母账号次数，但发送需从母账号操作。

发送成功后，后端以有时限的独立上下文刷新次数。回读失败仍返回 `sent: true`、`refresh_failed: true`，并尝试清空过期快照。发送请求不自动重试；网络错误或无法确认的响应要求先在 Codex 核对状态。接口错误不会向前端透传上游原始响应中的凭证或无关个人信息。
