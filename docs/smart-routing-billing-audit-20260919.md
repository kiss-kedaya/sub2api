# 智能路由与计费审查记录（2026-09-19）

## 范围与结论

正式仓库：`F:/GO/sub2-official-work`，`main`，基线 `cf9ac5e3d695a157855325d22e3494c21b4f16a5`。
线上版本：`0.1.331`，发布提交 `ff7dd3bee30b52531de33e89b6d5de453649b75b`。

本轮确认 9 项逻辑问题或条件性资金风险。优先处理 WebSocket 后续轮次不检查余额，以及异步媒体的结算生命周期。其余问题必须结合功能开关判断，不能把离线复现写成现网已发生损失。

本轮只读审查实现和生产配置，在隔离工作树新增离线测试；正式目录仅新增本记录。没有改服务器、业务代码、价格、余额、数据库数据或结构，没有发布新版本。模拟上游和账本的测试金额均不是实际用户损失金额。

## 现网快照

| 项目 | 本轮只读核查结果 | 含义 |
| --- | --- | --- |
| 余额预扣 | 两机 `BILLING_BALANCE_PREAUTHORIZATION_ENABLED=false` | 追加预扣恢复问题暂不触发；直接打开开关不能替代修复 |
| Live | 无 `allow_live=true` 分组 | Live 问题当前未开放给用户 |
| 退款 | 三个支付配置的退款及用户退款开关均关闭 | pending 退款缺陷是启用前必须修的问题 |
| 免费 Fast | 无分组启用 `free_openai_fast` | 对应错误扣款暂不触发；最近 15 分钟未查到总成本为零但实扣大于零的记录 |
| 分组自定义模型价卡 | 当前无 `ModelPricing` 价卡 | 后缀漏价不等于现网渠道价漏价；渠道价已有基名回退 |
| 批量生图 | 最近 24 小时无任务；`submitted + QUEUE_FAILED` 为零 | 未发现这类现网卡单 |
| Grok 视频 | 当前没有可调度的视频账号 | 未做真实视频生成或扣费验证，未擅自恢复账号或模型 |
| WS 用量 | 最近 15 分钟有一条 `openai_ws_mode=true` 记录 | 该字段也涵盖 HTTP 入站转 WS 上游，不能据此认定发生入站 WS 透支 |

生产核查通过只读事务执行，查询设置 5 秒超时。上述是审查时点的快照，不是持续监控结果。

## 1. P1：WebSocket 余额用完后仍能继续新一轮请求

用户体验：同一连接第一轮已经扣完余额，第二轮仍正常发往上游。订阅到期或用量耗尽后的后续轮次也没有重新检查。

- 入口只在 `backend/internal/handler/openai_gateway_handler.go:2284` 调用一次 `CheckBillingEligibility`。
- `openai_gateway_handler.go:2635` 的 `BeforeTurn` 仅刷新账号、检查利润和并发，没有余额、订阅限额检查，也没有每轮预扣。
- `openai_gateway_handler.go:2769` 在完成后才异步记账；`backend/internal/repository/usage_billing_repo.go:327` 的不足额后扣允许余额变负。
- 智能路由绑定备用组也没有补上每轮资金准入。选号、模型权限和并发槽不能代替资金限制。

完整离线回归通过真实 Gin handler、WebSocket 客户端和模拟上游执行。配置显式关闭预扣，与现网一致。第一轮结算完成后模拟账本余额归零，再发第二轮，安全断言失败：`turn 2 reached upstream ... eligibility_reads=1`。主线程独立复跑得到同一失败，原有正常双轮对照测试通过。子代理的 race 运行同样因业务断言失败，未报告数据竞争。

此回归证明“后续轮次没有重查余额”，没有伪造实际单位价格来估算损失。订阅分支目前是同一调用链的静态证据，尚无单独的订阅两轮动态回归。按 P1 处理，不能将其描述为无前提的 P0。

修复要求：每轮发上游前按实际路由组重查资格，并与上一轮结算/可用余额同步；接入每轮独立预扣、失败释放和幂等结算，覆盖异步结算尚未完成就收到下一轮的窗口。验收应断言余额归零后第二轮在到达上游前被拒绝，同时覆盖订阅日限额。

## 2. P1：Grok 视频不再轮询就不会结算

用户体验：提交视频拿到任务 ID 后关闭客户端、不再查状态或下载，平台没有独立的完成结算入口。上游可能继续生成并产生费用。

- `backend/internal/handler/grok_media.go:698` 明确排除视频 create 的即时计费。
- `grok_media.go:576` 的状态/下载分支才触发视频结算。
- `backend/internal/service/grok_media.go:592` 附近定义 pending 默认保存 24 小时，claim 默认 48 小时；未找到任务完成后的后台扫描结算路径。
- 现有 `TestGrokVideoSmartRouteBillsSelectedGroup` 的真实 handler fixture 证明创建后尚无结算命令。

331 修的是“有人查询时按实际路由组收费”，没有覆盖“客户端不再查询”。当前视频账号不可调度，未证明本轮现网有这种损失。如果产品明确规定未领取结果免费，则需将其记录为接受的商业成本；当前未找到该政策。

修复要求：创建时保留可回滚预扣和持久化任务，由后台独立确认终态并结算/退款，不能让客户端是否查询决定是否收费。验收应提交后断开客户端，后台仍能完成唯一一次结算或失败退款。

## 3. P1：媒体结果已交付，结算仍只在内存队列

用户体验：用户已经下载视频或拿到 completed 图片，进程恰好在异步扣费前退出，结果无法追回，费用也缺少可靠补账入口。

- Grok 状态 JSON 在 `backend/internal/service/grok_media.go:981` 写出，视频内容在 `:1161` 写出；之后 handler 才在 `backend/internal/handler/grok_media.go:756` claim，并在 `:867` 附近提交异步 `RecordUsage`。
- claim 仅是 Redis 去重占位。claim 成功但实际结算前崩溃，会让后续查询在默认 48 小时内跳过结算；正常失败虽可释放 claim，后续仍依赖查询重试。
- `backend/internal/handler/image_task_handler.go:232` 执行网关，`:247` 发布 completed；`backend/internal/service/image_task.go:34` 的任务记录不保存计费组、订阅身份或结算状态。
- mandatory worker 能处理队列满时的同步回退，不能防已入队任务随进程退出丢失。

主线程独立运行 `TestAuditGrokVideoContentIsWrittenBeforeBilling` 和 `TestAuditAsyncImagePublishesCompletedResultBeforeQueuedBilling`，均通过。这两条测试断言的是缺陷现状，不代表已修复。未在生产杀进程制造丢单。

修复要求：结果发布前持久化可重放的结算意图，保存实际组、订阅和定价上下文；重试沿用同一结算 ID，区分处理中和已结算。不得把 Redis claim 视作已扣款，也不应粗暴阻塞所有文本流式输出。验收应模拟发布/结算边界的进程恢复，最终恰好扣一次。

## 4. P1：批量生图提交成功但首次入队失败，缺自动恢复

用户体验：接口返回 `BATCH_IMAGE_QUEUE_FAILED`，上游却已经接单，预扣保持冻结，后台无人继续查询结果。

- `backend/internal/service/batch_image_public.go:375` 已保存上游任务名；`:391` 入队失败仅记录 `QUEUE_FAILED`，没有确认取消上游或释放预扣。
- `backend/internal/service/batch_image_billing_recovery.go:39` 的扫描与 `backend/internal/repository/batch_image_repo.go:612` 仅覆盖 created/uploading 且没有上游任务名的记录。
- Redis active 集合恢复也不包含从未成功入队的任务。
- 存在人工恢复路径：`batch_image_public.go:221` 使用同一 `Idempotency-Key` 再次提交可补入队，不能写成永久不可恢复；普通 Get/List 不补入队。

现有 `TestBatchImagePublicService_Submit/queue_failure_is_recorded_after_provider_submit` 复现一次预扣、零次释放、状态 Submitted、错误 QUEUE_FAILED，主线程独立运行通过。现网未查到该类卡单。

修复要求：数据库提交后使用持久化入队意图，或扫描已提交但未入队的记录补队，避免重复上游提交。只有确认上游取消后才能选择释放预扣。验收应让首次入队失败、恢复队列后自动完成处理，无需客户端再次 Submit。

## 5. P1（退款启用后）：pending 退款提前放回余额

用户体验：退款已被支付方受理但还在等待确认，余额先变回可用；用户继续消费后，外部退款成功，平台只扣回剩余余额。

- `backend/internal/service/payment_refund.go:661` 的 `markRefundPending` 调 `RollbackRefund` 释放原扣减。
- `payment_refund.go:549` 的最终扣减只取当前可用余额，不保留退款所需资金，也没有补记差额债务。
- 离线执行非强制退款完整 pending 到成功流程：请求退 100，pending 放回 100，期间消费 80，外部确认退 100，平台最终只收回 20。安全断言失败。

这是模拟金额，不是现网亏损金额。现网退款开关全关，暂未暴露这一路径。

修复要求：支付方受理后持续冻结原退款资金，只有明确失败才释放；强制退款与普通退款采用明确不同的规则。验收需覆盖重复回调、pending 期间消费、最终成功/失败，确保资金不能同时用于消费和退款。

## 6. P1（预扣启用后）：流式追加预扣没有进入持久化恢复记录

用户体验：流式输出已经推进，累计预扣增加；进程退出后恢复只结算最初的保留金额，追加部分被释放。

- `backend/internal/service/balance_preauthorization_guard.go:233` 的 TopUp 修改 Redis，`:248` 只更新内存 `holdAmount`，未同步 SQL 中的累计 hold。
- `backend/internal/service/balance_preauthorization_recovery.go:42` 在 Authorized 状态按原持久化 `HoldAmount` 恢复结算。
- 离线测试中，初始保留 0.269，模拟输出 1280 字节后保留 1.549；仅用持久化记录恢复，结算仍为 0.269，差额 1.28 被释放。安全断言失败。

保留金额是估算，不等于精确上游用量；不能据此声称必然漏扣 1.28。确认的问题是新增保留金额在持久化恢复中丢失，真实费用也无法恢复。现网两机预扣关闭。

修复要求：持久化累计保留/已发生用量及单调状态，恢复时不能只读初始 hold；同时设计 Redis 成功、SQL 失败的恢复顺序。验收应在多次追加后模拟进程退出，能够恢复累计状态且不会重复扣款。不能直接打开预扣开关当作本轮修复。

## 7. P2：免费 Fast 重算覆盖“上游拒绝免扣”

用户体验：上游明确拒绝、原本应免扣的请求，在启用免费 Fast 的组里仍被收标准档费用。

- `backend/internal/service/openai_gateway_usage.go:319` 先因 `NonBillableUpstreamError` 清零。
- `openai_gateway_usage.go:335` 的免费 Fast 分支随后重算并覆盖 `ActualCost`。
- 离线真实 `RecordUsage` 入口得到 `TotalCost=0, ActualCost=1, BalanceCost=1`；期望免扣的断言失败。

现网没有启用免费 Fast 的组。修复要求是让免扣判定优先于所有价格调整，覆盖 priority、订阅和余额路径；正常成功请求仍按免费 Fast 的既定标准档价格收取。

## 8. P2：模型后缀未继承分组自定义价

用户体验：分组为基础模型设置售价，但请求合法变体名时没有命中分组价，回落到更低的内置价格。

- `backend/internal/service/model_pricing_resolver.go:146` 的分组价匹配没有已知模型基名回退；同文件 `:190` 的渠道价匹配已有这一步。
- 补充测试先验证账号允许模型，再走实际 `Forward`，最后把返回结果交给实际 `RecordUsage`。使用兼容 API Key 账号、模拟上游接受请求，没有手工强塞计费模型。
- 基名 `gpt-5.6-luna` 配置输入价 0.4/百万 token，实际扣 0.4；`gpt-5.6-luna-high` 和 `gpt-5.6-luna-2026-08-01` 的转发与计费模型均保留后缀，实际扣 0.2。两个变体安全断言失败，基名对照通过。

前提是账号/上游允许变体，且账号或渠道没有提前映射回基名；不是任意模型改名都能通过。当前现网没有分组自定义价卡，不能将现网渠道价问题与此混为一谈。

修复要求：分组价复用已知模型规范化规则，先保留精确变体价和通配价的优先级，再回退基名；不能粗暴去掉任意后缀，造成跨模型串价。验收还需覆盖显式变体价和账号映射到基名的情况。

## 9. P2：Live 选路丢失身份；恢复可用性会暴露零计费

用户体验：Live 建连前资格检查通过，但选号时丢掉 User/Group，正常单组和智能路由都无可用候选。

- `backend/internal/service/openai_live.go:150` 仅构造带 GroupID/RouteGroupIDs 的薄 APIKey，`:153` 进入路由。
- `backend/internal/service/api_key_routes.go:359` 要求完整 User/Group 身份，因此候选被过滤。
- 离线对照完整 Key 有一个候选，薄 Key 为零；定向测试通过。
- 同时 `openai_live.go:832` 明确使用零费用记录，并绕过 `applyUsageBilling`；现有零费用测试也通过。

当前身份缺失已阻断创建，现网 Live 全关。因此这是相互约束的一项问题，不能把零计费称为现网可反复触发的 P0，也不能只修身份丢失就单独上线。

修复要求：先确定 Live 收费单位/价格并接入结算，再恢复完整身份路由；验收应同时证明创建可用、实际扣款正确、重复结束只扣一次。不要凭猜测补定价。

## 已排除与边界

- “重复客户端 request_id 共用一笔预扣”不成立：`backend/internal/server/middleware/client_request_id.go` 每次生成服务端 UUID，计费优先使用该 ID；客户端 `X-Client-Request-ID` 不可直接指定它。真实中间件回归确认同一请求头和请求体得到不同计费身份。直接写内部 context 的测试不证明公开入口可触发。
- 331 已修复的 Grok 视频首组计费没有重复计入本轮；详见 [331 修复和上线记录](grok-video-smart-billing-20260918.md)。
- HTTP 预扣关闭时的小余额并发属于现有事后计费策略风险，不是本轮新引入缺陷。尾差记成负余额与“余额已耗尽后继续允许新轮次”是两件事，不能通过丢弃已发生费用来掩盖透支。
- 未执行真实资金攻击、未人为中断生产进程。没有全量历史对账，不能推导累计损失，也不应自动补扣历史用户余额。

## 离线证据与交接

| 隔离目录 | 测试文件 | 结果含义 |
| --- | --- | --- |
| `F:/GO/sub2-review-route-20260919` | `backend/internal/handler/openai_responses_ws_balance_audit_test.go` | 安全断言失败，证实余额归零后第二轮继续到达上游 |
| 同上 | `backend/internal/service/openai_live_route_audit_test.go` | 现状断言通过，证实薄身份丢失候选 |
| `F:/GO/sub2-review-media-20260919` | `backend/internal/handler/media_billing_audit_test.go` | 现状断言通过，证实交付先于结算 |
| `F:/GO/sub2-review-pricing-20260919` | `backend/internal/service/pricing_review_audit_test.go` | 免费 Fast 断言失败；真实转发后的变体价断言失败，基名对照通过 |
| 同上 | `backend/internal/service/refund_review_audit_test.go` | 安全断言失败，证实 pending 期间放回退款资金 |
| `F:/GO/sub2-review-settlement-20260919` | `backend/internal/service/recovery_hold_audit_test.go` | 安全断言失败，证实恢复丢失追加预扣 |
| 同上 | `backend/internal/server/middleware/client_billing_audit_test.go` | 安全断言通过，排除客户端重复 ID 误报 |

结算审查子代理异常终止后由主线程接管，没有继续等待其结果。它遗留的 `balance_preauthorization_request_id_audit_test.go` 仅测试内部 context 假设，不作为对外缺陷证据。

测试保留在隔离树，没有把故意失败的审计测试并入正式 CI。修复时应将正确的安全断言改绿后再纳入回归。以下命令在对应目录的 `backend` 内执行；包含安全红测的命令预期返回非零，不是编译失败：

```text
go test -tags unit ./internal/handler -run 'TestAuditOpenAIResponsesWebSocketSecondTurnMustNotReachUpstreamAfterBalanceExhaustion|TestOpenAIResponsesWebSocket_PassthroughTracksModelPerTurn' -count=1 -v
go test -tags unit ./internal/service -run 'TestAuditLiveRouteThinKeyDropsEveryAuthorizedCandidate|TestFinalizeLiveCallIsIdempotentAndWritesZeroUsage' -count=1
go test -tags unit ./internal/handler -run 'TestAuditGrokVideoContentIsWrittenBeforeBilling|TestAuditAsyncImagePublishesCompletedResultBeforeQueuedBilling' -count=1 -v
go test -tags unit ./internal/service -run 'TestBatchImagePublicService_Submit/queue_failure_is_recorded_after_provider_submit' -count=1 -v
go test -tags unit ./internal/service -run 'TestAuditFreeFastMustNotChargeRejectedRequest|TestAuditGroupPriceMustCoverAcceptedModelVariant|TestAuditPendingRefundMustRetainReservedFunds' -count=1 -v
go test -tags unit ./internal/service -run TestAuditRecoveryMustNotDiscardStreamingHoldTopUps -count=1 -v
go test -tags unit ./internal/server/middleware -run TestAuditRepeatedClientHeadersCannotReuseBillingIdentity -count=1 -v
```

修复顺序：每轮 WS 资金准入；媒体预扣、持久化结算与补队；退款和预扣恢复；免费 Fast 与模型规范化；最后是包含定价的 Live 恢复。涉及余额、预扣、退款的生产变更仍需备份、可回滚，以及独立的小额真实验账，不能用离线测试替代上线验收。
