# 术语速查（GLOSSARY）

一页讲清这个项目里的词：每条给「含义」和「代码位置」。只写代码里能核实的含义；代码里的含义变了，同步改这里。
读代码、写 issue、写测试名时用这里的说法，不要另造同义词。路径都相对 `backend/internal/`，除非另有说明。

## 账号与分发

| 术语 | 含义 | 代码位置 |
|---|---|---|
| 用户 User | 本站注册用户，是余额、订阅和 API Key 的所有者。 | `service/user.go` |
| API Key | 用户调用网关的密钥。库里只存 `KeyHash`（sha256）和 `KeyPrefix`（前 8 位），明文只在创建/轮换的那次响应里出现；带 USD 额度、过期时间、5h/1d/7d 限额、IP 黑白名单；绑 1 个分组，或绑有序的多个分组（`RouteGroupIDs`）。不是「上游账号」，也不是「管理员 API Key」。 | `service/api_key.go` |
| 管理员 API Key | 后台自动化用的 `x-api-key`。新建的是「受限密钥」（权限目录里登记过的接口才放行）；旧版全权密钥只能读状态和删除，不能新建。 | `docs/admin-api-keys.md` |
| 账号 Account（上游账号） | 一份上游凭据：平台 + 类型（`oauth` / `setup-token` / `apikey` / `upstream` / `cloudflare` / `bedrock` / `service_account`），带优先级、并发、负载因子、代理、模型映射。不是用户账户。 | `service/account.go`、`domain/constants.go` |
| 平台 Platform | 上游协议族标识：`anthropic`、`openai`、`gemini`、`antigravity`、`grok`、`kimi`、`zhipu`、`deepseek`、`minimax`、`cline`、`command_code`、`typesafe`，外加分组专用的 `composite`。 | `domain/constants.go` |
| 分组 Group | 账号池 + 计价配置：平台、倍率、高峰倍率、标准/订阅计费、模型路由、模型白名单、跨协议开关等。API Key 通过分组选账号、定价。 | `service/group.go` |
| 智能路由 | 一把 Key 绑多个分组（`RouteGroupIDs`）：候选组 ≥2 时按可用率/缓存率/首字/倍率/配置顺序五项综合评分排组（分差 <0.02 按配置顺序），并用 1 小时会话粘组（`route_group:*`）固定同会话路由；粘组不可用或故障转移时再按序顺延。 | `service/group_route_plan.go`、`service/gateway_key_routes.go` |
| 复合分组 Composite | 平台为 `composite` 的分组：按请求的模型解析出目标平台和上游模型，再去对应平台选号。 | `service/composite_route_resolver.go`、`docs/COMPOSITE_GROUPS.md` |
| 渠道 Channel | 挂在分组上的定价与模型映射配置（按平台的模型价格、映射、`BillingModelSource`、是否限制模型），用于计价和用户端的渠道定价展示。不是上游账号，也不是「渠道监控」。 | `service/channel.go` |
| 渠道监控 Channel monitor | 周期性探测模型可用性，保存历史并做汇总展示。 | `service/channel_monitor_*.go` |
| 订阅 Subscription | 分组的 `SubscriptionType` 为 `subscription` 时，用户购买的订阅：按日/周/月 USD 限额控制，不扣余额（`standard` 才按余额扣费）。 | `service/user_subscription.go`、`domain/constants.go` |
| 兑换码（卡密） | 库里只存哈希和前缀，读接口只返回掩码，明文只在生成那次出现。 | `service/redeem_code.go` |
| 简易模式 Simple mode | `run_mode: simple`：跳过计费与配额检查；默认是 `standard`。 | `config/config.go`、`server/middleware/api_key_auth.go` |

## 调度与转发

| 术语 | 含义 | 代码位置 |
|---|---|---|
| 调度快照 Scheduler snapshot | Redis 里按桶（分组/平台）存的可调度账号快照。请求只读快照、不查 PG（snapshot-only）；账号变更写 outbox，由后台 worker 重建快照。 | `service/scheduler_snapshot_service.go`、`service/scheduler_freshness.go`、`repository/scheduler_cache.go` |
| Outbox | PG 里的变更事件表，worker 消费后更新 Redis 或下游，保证最终一致。例如：调度、认证缓存失效、余额调整、工单邮件。 | `repository/scheduler_outbox_repo.go`、`repository/auth_cache_invalidation_outbox_repo.go`、`repository/live_balance_adjustment_outbox_repo.go`、`repository/ticket_email_outbox.go` |
| 认证快照 | API Key 鉴权所需字段的缓存：进程内 L1 + Redis L2 + singleflight 回源 PG；L2 键带 schema 版本号。 | `service/api_key_auth_cache.go`、`service/api_key_auth_cache_impl.go`、`repository/api_key_cache.go` |
| 粘性会话 Sticky session | 由请求内容算出 session hash，把同一会话钉在同一个账号上（绑定存 Redis），提高上游缓存命中。 | `service/gateway_service.go`（`GenerateSessionHash`） |
| 并发槽 Slot | 用户级和账号级的并发配额，计数在 Redis；账号槽满了先排队（等待计划），队列满返回 429。 | `service/concurrency_service.go`、`repository/concurrency_cache.go` |
| Failover（切号） | 上游失败时排除该账号、换下一个重试；次数上限 `gateway.max_account_switches`；SSE 内容已开始写给客户端后不再切号。 | `handler/failover_loop.go` |
| 池模式 Pool mode | API Key / Bedrock 类账号在凭据里开 `pool_mode`：表示上游本身是个池（例如另一个 sub2api）。上游 429/403/401 先在同账号原地重试（默认 3 次，上限 10），且不把本地账号标成限流或错误。 | `service/account.go`（`IsPoolMode`） |
| 临时不可调度 TempUnschedulable | 账号带 `TempUnschedulableUntil`：到点自动恢复，期间调度器跳过它。与 `RateLimitResetAt`（上游 429）、`OverloadUntil`（过载）是并列的三种暂时摘除。 | `service/account.go`（`IsSchedulable`） |
| 健康熔断 | OpenAI API Key 池模式账号：滚动窗口内失败数达阈值就写临时不可调度，冷却到期自动恢复；后台设置开关，默认关闭。 | `service/openai_apikey_health_breaker.go` |
| 模型映射 | 账号级 `model_mapping` 和渠道级 `ModelMapping`：把客户端写的模型名换成上游模型名。 | `service/account.go`、`service/channel.go` |
| 模型白名单 | 分组级准入：只放行名单内的模型，发生在选号之前。 | `server/middleware/group_model_allowlist.go` |
| 跨协议转换 | Anthropic 协议族与 OpenAI 协议族互转；分组级开关，默认关；转换实现在 `pkg/apicompat`。 | `service/cross_protocol_conversion.go` |
| ParsedRequest | handler 里一次解析出的请求视图（模型、是否流式、system/messages 的偏移范围），后续各环复用，不再重复解析请求体。 | `service/gateway_request.go` |

## 计费

| 术语 | 含义 | 代码位置 |
|---|---|---|
| 倍率 Rate multiplier | 用户扣费 = 标准价 × 分组倍率（命中高峰时段再乘高峰倍率），用户在某分组有专属倍率时用专属值。`cost` 是标准价，`actual_cost` 是实际扣费。账号自己的 `RateMultiplier` 只折算账号侧成本/配额。 | `service/group.go`、`service/user_group_rate_resolver.go`、`service/gateway_usage_billing.go` |
| 计费模式 BillingMode | `token`（按 token）、`per_request`（按次）、`image`、`video`。 | `service/channel.go` |
| 预扣 Preauthorization | 余额预授权：请求开始前冻结估算金额，结束后按实际结算。`billing.balance_preauthorization_enabled`，默认关闭；关闭时只走请求结束后的结算扣费。 | `service/balance_preauthorization_*.go`、`config/config.go` |
| 在途预留 Inflight reservation | 与预扣不同：只在 Redis 里登记请求的预估金额，防止并发请求同时看到同一份余额而一起透支；请求结束释放，长流式请求会续期。 | `service/billing_inflight_reservation.go`、`handler/gateway_inflight_reservation.go` |
| 用量记录 usage_logs | 每个请求一行（模型、token、费用、端点、耗时……），由后台 worker 批量写入；按 request id 去重扣费。 | `repository/usage_log_repo.go`、`service/gateway_usage_billing.go` |
| 汇总表 Rollup | 小时级预聚合表 `usage_dashboard_hourly*`：看板和统计读它，不扫 `usage_logs`。 | `repository/dashboard_aggregation_repo.go` |

## 安全与后台

| 术语 | 含义 | 代码位置 |
|---|---|---|
| Step-up（二次验证） | 敏感管理操作要求近期完成 TOTP 或 Passkey 验证；每个因子各发一枚 5 分钟凭证；绑定两种因子时任选其一即可，密码登录仍要求全部已绑定因子。 | `server/middleware/step_up.go`、`docs/two-factor-factor-policy.md` |
| 管理路由分类 | 每条 `/api/v1/admin*` 路由必须归入「机器可调用 / 需 step-up / 无密钥」之一，未归类的按需要 step-up 处理，覆盖率测试会在 CI 里拦住漏登记。 | `pkg/adminroutepolicy/policy.go` |
| 审计日志 | 管理面与敏感操作的审计记录，保留天数可配置。 | `server/middleware/audit_log.go` |
| at-rest 加密 | 上游凭据、代理密码和部分设置的落库加密；`SUB2API_ATREST_ENABLED` 默认关闭，读路径与开关无关。 | `pkg/atrest/atrest.go`、`atrestmigrate/` |
| Prompt 审计 / 内容审核 | 网关请求进入调度前的内容检查与处罚判定。 | `securityaudit/`、`service/content_moderation.go` |
| Ops | 错误日志、指标、告警和入口拒绝统计等运行监控。 | `service/ops_service.go` |

## 支付与充值

| 术语 | 含义 | 代码位置 |
|---|---|---|
| 支付渠道实例 | 一个支付服务商的配置实例（密钥、币种、支持的方式）；服务商实现统一的 `Provider` 接口。 | `payment/types.go`、`payment/provider/` |
| 加密货币充值 cryptopay | 内置的链上充值：每用户 HD 派生充值地址，扫链发现入账，归集到热钱包。 | `cryptopay/` |
| 归集 Sweep | 把各充值地址上的资产转到目标地址；配置里可锁定目标地址。 | `cryptopay/sweeper/` |

## 工程

| 术语 | 含义 | 代码位置 |
|---|---|---|
| embed 构建 | `-tags embed` 把 `backend/internal/web/dist` 嵌进二进制；不带该标签时没有前端页面。 | `internal/web/embed_on.go`、`internal/web/embed_off.go` |
| `_notx.sql` | 含 `CREATE/DROP INDEX CONCURRENTLY` 的迁移，不放进事务执行。 | `repository/migrations_runner.go` |
| 默认关闭开关 | 新行为先加开关、默认关，评审和验证后再改默认。 | `DEV_GUIDE.md` 第九章 |
