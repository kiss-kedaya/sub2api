# 智能路由热路径与逻辑审查

日期：2026-09-17，时间均为北京时间。正式目录 `F:/GO/sub2-official-work`，基线 `6cec564c11962ebc9d9f530c093d061a0f0bdbe6`。

首轮为只读审查，未修改业务代码、生产数据库、缓存、服务器配置或流量比例。用户随后授权修复并提交 GitHub，修复阶段记录见文末；本次仍不部署生产。

## 结论与优先级

| 优先级 | 用户可见影响 | 证据范围 |
| --- | --- | --- |
| P1 | 备用分组撤权后仍可被选择 | 最新鉴权数据 + 完整选号入口的本地复现 |
| P1 | 备用组停用且快照清退后，旧会话仍可命中 | 本地真实清退逻辑 + 粘性选号复现 |
| P1 | 切换分组后，利润门使用主组倍率，结算使用新组倍率 | service 复现 + handler 调用链核对；未证明现网已有亏损 |
| P2 | 模型目录重复同步回源，放大数据库和 CPU 开销 | 双机真实 CPU profile + 3 个失败复现场景 |
| P2 | 主组 Fast 策略或模型映射影响备用组 | service 复现 + handler 调用链核对 |

另有 Gemini 通用桥接的缓存计费、缺失 usage 和协议选择问题，见下文。它们不是智能路由特有问题，不能据此宣称 Grok 正常流量都在丢缓存。

## 线上采样

- 两机当前接流实例均为 `0.1.326-grok600-canary`，HTTP 8097，各自版本权重 100%；物理旧机 40%、新机 60%。原 326 的 8095 实例仍在线备用。
- 旧机 CPU 采样始于 01:19:46，新机始于 01:19:58，各 15 秒。另取 heap、goroutine profile；没有压测、强制 GC 或重启。
- 原始证据保留于正式目录的 `.artifacts/smart-route-{old,new}-{cpu,heap,goroutine}.pb.gz`，不随报告公开上传。
- 01:33--01:34 复核：两个接流实例均未重启，回退守护 active、verified、100%。最近 5 分钟未匹配到 panic、fatal runtime、OOM、数据库连接满、鉴权超时日志。
- 01:38 左右，两台服务器分别访问公网 `https://kedaya.ai/health` 均返回 HTTP 200、`{"status":"ok"}`。本机访问返回既有地区限制页面 HTTP 403，不是网关 502；未改动该策略。
- 这些结果只证明当时的健康状态，不代表所有模型业务请求零错误，也不是业务端到端验收。

### 热点数据

| 指标 | 旧机 | 新机 |
| --- | --- | --- |
| 15 秒窗口采样 CPU 总量 | 19.64 CPU 秒 | 34.40 CPU 秒 |
| `catalogModels -> loadAvailableModelsFromStore` 累计 CPU | 1.23 秒，6.26% | 1.65 秒，4.80% |
| 上述路径的账号仓库调用累计 CPU | 1.02 秒，5.19% | 1.51 秒，4.39% |
| `gjson.parseSquash` 累计 CPU | 24.29% | 25.49% |
| `inuse_space` 采样堆 | 1018.22 MB | 1606.47 MB |
| `readRequestBodyChunks` 驻留 | 679.07 MB，66.69% | 1036.65 MB，64.53% |

百分比是采样 CPU 占比，不是整机使用率或承诺的优化收益。JSON 扫描主要属于转发正文处理，并非全部由智能路由造成。新机智能路由分配栈约 7.5 MB，仅占采样堆 0.47%；不能把在途请求体或单次 heap 直接判定为内存泄漏，也没有证据宣称已排除泄漏。

## 已确认的智能路由缺陷

### 1. 备用组未重新校验权限和状态

- 位置：`backend/internal/service/openai_key_routes.go:58`、`api_key_routes.go:365`。
- 中间件校验的是主组；沿路由选择备用组时，没有再次验证当前用户能否使用该组。组选成功后只是 hydrate 新 Key。
- 使用最新鉴权快照，撤销专属组权限和撤销受限公共组权限两种场景，仍选择被撤权的备用组 27；正确结果应跳到有权限的组 28。ID 均为本地测试数据。
- 分组停用事件清退快照后，无粘性会话的对照能跳过；已有粘性会话仍通过 `openai_gateway_scheduling.go:994` 直接读取账号，在 1032 行续期并返回。
- 自定义白名单场景即使禁止目录数据库回源，仍能复现停用组旧会话命中，因此不能只修目录性能问题。
- 后台修改用户和分组已有鉴权缓存失效，仓库也覆盖备用组引用。本问题不是单纯漏清缓存。
- 建议最小修复：在两套沿组选入口统一检查候选组状态及当前用户授权，再进入普通、粘性、previous-response 选号。不可把单组停用拦截直接全局放开。

### 2. 跨组的准入、策略和结算上下文不一致

- 入口：`backend/internal/handler/openai_gateway_handler.go:662` 在选号前装配定价和预扣；705 行选号、719 行只替换局部 `apiKey`，原请求 Group context 和订阅仍保留。
- 利润门：`backend/internal/service/openai_profit_control.go:274` 优先用原 Group context 的倍率；实际结算 `openai_gateway_usage.go:217` 使用选中 Key 的倍率。
- 本地例：主组倍率 10，备用倍率 1、最低利润 20%。门阈值仍是 8，放行成本倍率 5 的账号；备用组对应阈值应为 0.8。反向组合也可能误拒绝可用账号。
- Fast：`openai_gateway_request_body.go:1781` 读原组强制 Fast，而 `openai_gateway_usage.go:332` 按新组判断免费 Fast。本地复现原组强制且免费、新组均关闭，仍发送 priority，结算却不享受免费 Fast。
- 线上只读发现 981 个 active 智能 Key 存在主/备用倍率不同的配置。这个数字不是受损账单数，也不代表每把 Key 都发生过跨组或启用了利润门。
- 建议保持请求定价时间不变，但按实际候选组装配授权、利润、预扣和转发策略。必须保留 composite 父组计费语义，不能简单把所有调度子组都当成计费组。

### 3. 目录热路径绕过 snapshot-only 并重复扫描

- `backend/internal/service/openai_key_routes.go:19` 直接读模型目录，没有复用 `GatewayService` 现有目录缓存和 singleflight。
- 34 行先检查所有候选的目录；53 行选号循环再次查目录。`api_key_routes.go:188` 每次依次探测 9 种平台。
- `gateway_service.go:1455` 在快照失败后无条件调用账号仓库，绕过 snapshot-only 不回源契约。真实线上 profile 已出现这条链，不只是冷启动假设。
- 本地复现：全冷快照时，单组一次选号触发 18 次账号仓库扫描，10 组触发 180 次；GPT 自身快照已热、其他平台冷时，20 次成功选号仍触发 80 次无关账号仓库扫描。
- 这些是仓库方法调用次数，不是精确 SQL 条数；仓库内部还可能加载关联数据。普通单组 Key 也会走这里。
- 建议复用 `gateway_models_catalog.go` 已有的按组目录缓存和并发合并，并在底层严格遵守快照请求模式；单次请求内复用候选目录判断。必须保留未知目录与确认不支持模型的区别。

### 4. 主组模型映射使备用组不可达

- `backend/internal/handler/openai_chat_completions.go:119`、`openai_gateway_handler.go:580` 在选择候选组前，先按主组渠道映射改写模型。
- `openai_key_routes.go:44` 随后用这个私有映射名验证备用组白名单。
- 本地例：主组把 `gpt-5.1` 映射为自己的私有名称，主组无号后，支持原模型的备用组也被跳过；保持原模型时两组依次尝试且备用组成功。
- 建议用客户端请求模型选择候选组，再按候选组解析渠道映射和限制；预扣、日志原始模型及实际计费模型需同时核对。

## 停用主组的现有行为

- 普通及 Google 鉴权入口均在路由前检查主组状态。主组 inactive 返回 403；主组删除，即使仍有备用路由，也返回 403。
- 本地 HTTP 中间件测试共 8 个场景确认：active 对照能进入 handler，其余相应请求在鉴权阶段终止。
- 线上只读看到 64 把 active 智能 Key 引用 inactive 组 27，其中 2 把为主组，62 把为备用。没有使用这两把真实 Key 调用上游，不能声称已经真实验证它们的业务请求失败。
- 当前实现不会在主组停用时自动提升备用组。若产品要求继续使用其他已授权组，需要明确修改此策略，并补上前述备用权限校验；不能仅删除 403 检查。

## 已复现但现网暂无配置的边界

- 跨订阅组：旧订阅对象继续传给结算，两套 `RecordUsage` 都可出现用量记 B、扣 A 订阅、缓存更新 B。位置 `gateway_usage_billing.go:337`、430；本地样例扣款 0.00500000，仅为测试金额。
- 免费主组到付费备用：预扣在切组前完成，guard 未更新。本地复现预扣 0、流式补扣 0、最终实际结算 0.30050000。不是免费结算，而是付费输出缺少相应预扣保护。
- 01:33 只读核实，12 个未删除组全部为 standard；active 智能 Key 中订阅主/备用及免费主组到付费备用配置数量均为 0。没有按这些测试修改或补偿任何真实余额。

## 额外发现：Gemini 通用协议桥

这些是共享桥接代码的问题，可被智能路由触达，但不限定智能 Key。

| 问题 | 位置 | 本地运行证据 |
| --- | --- | --- |
| P1：缓存输入重复计费 | `backend/internal/handler/endpoint.go:357` | 总输入 1000、缓存读 600、写 100，普通输入仍为 1000，应为 300；实际扣款命令 0.00365000，互斥分桶后应为 0.00190000 |
| P1：原生流式桥未请求 usage | `backend/internal/service/gemini_native_openai_compat.go:139` | 没有 `stream_options.include_usage`；假上游正常出字且 usage 为 null，转发和结算得到 0 token、0 扣款 |
| P2：账号配置 Responses 仍发 Chat | `gemini_native_openai_compat.go:33` | 假上游仅支持 Responses，真实转发函数仍请求 Chat，返回 404，且不是可切号错误 |
| P2：原生桥丢缓存用量 | `gemini_native_openai_compat.go:176` | 上游缓存读 600，转换后为 0；测试价格下收费 0.00110000，正确分桶为 0.00056000 |

- 缓存重复计费调用点已核对 `gateway_handler_responses.go:302`、`gateway_handler_chat_completions.go:311`、`gateway_handler.go:551`，最终进入通用计费。OpenAI 专用结算已有正确扣除缓存的实现，不应在那里再扣一次。
- 上表金额来自内存仓库和测试价格，不是线上用户账单。没有真实 HTTP handler 端到端扣款验收，不据此推断或自动补偿历史账单。
- 最近 3 分钟、最多 5000 条 usage 样本的前 20 类入口统计未见 Gemini 平台，不能据此断言它完全没有流量。Grok 多个入口存在缓存读记录，这也不证明所有下游缓存标签完整。

## 复现与收尾

三个独立工作树均基于同一正式基线：

- `F:/GO/sub2-review-smart-hotpath-20260917`：`smart_route_hotpath_audit_test.go`。运行 `go test -tags=unit ./internal/service -run '^TestAuditSmartRoutes' -count=1 -v`，3 个场景按正确行为断言失败，分别得到 18、180、80 次回源。
- `F:/GO/sub2-review-smart-auth-20260917`：service 和 middleware 的 `smart_route_auth_audit_test.go`。运行 `go test -tags=unit ./internal/service ./internal/server/middleware -run '^TestSmartRouteAuthAudit_' -count=1 -v`；service 5 个缺陷场景失败、2 个对照通过，middleware 8 个行为测试通过。
- `F:/GO/sub2-review-smart-billing-20260917`：service 和 handler 的 `smart_billing_review_20260917_test.go`。运行 `go test -tags=unit ./internal/service ./internal/handler -run '^TestReviewSmartBilling' -count=1 -v`；9 个顶层测试、10 个场景通过，断言的是现有缺陷表现，不是修复后的正确行为。

首轮主代理独立重跑以上三组并复核关键调用链，随后收回两个审查子代理。当时尚未修复或执行全库回归，也未使用真实用户余额或上游凭据进行构造测试。报告初稿只保留于正式目录，后续随修复提交。

修复顺序建议：先补备用组授权/状态检查与快照回源保护，再处理跨组计费上下文；Gemini 桥接作为单独小补丁，复用既有用量转换和自适应协议。任何上线都需另行完成真实请求验证及 1% 灰度，不把本报告当成已修复或已发布。

## 修复阶段

用户授权：修复并提交 GitHub。目标为 `kiss-kedaya/sub2api` 的 `main`，不是上游 `Wei-Shaw/sub2api`。未改版本号、未打发布标签、未调用服务器部署或数据库脚本。

### 已合并的处理

- 候选组先验证完整政策、active 状态与当前用户权限，再查询目录、使用粘性绑定或实际选号。缺失组政策不沿用旧快照冒险放行。
- 模型目录按候选组单次构建并复用，底层不再绕过 snapshot-only 回源。冷快照单组、10 组及 20 次热 GPT 请求的账号仓库调用均为 0，且仍验证真实选号尝试次数，避免用拒绝全部请求伪造优化结果。
- 原始请求模型先过候选组白名单，候选渠道映射只执行一层。公开模型 A 映射 B，不因另有 B 映射 C 而连跳；Messages 专属别名与 dispatch 权限单独处理，保留 Grok/CN 兼容规则。
- 候选实际分组进入利润门，保留请求定价时刻及 composite 父组计费规则。16 个沿组选号调用点同步新 Key、Group context、订阅、渠道映射及适用的预扣状态。
- 多组 Key 的初次预扣延后到选号成功、转发上游之前；账号重试不重复预扣。切组在同一钱包 attempt 上补差，持有金额不提前释放，付费转订阅在最终结算时退回原余额 hold。
- 免费转付费建立流式补扣跟踪器；已发出输出、所有权已转移或已结算的 guard 不能再改价格。换组准入计入身份匹配且仍有效的自身 hold，不能借用其他用户或 Key 的预扣。
- 换组检查新组额度和 RPM，但不重复消耗用户全局 RPM，也不沿用主组专属 RPM 覆盖。相同请求重访已检查分组不会再次累加。
- 订阅按实际选中组解析，录入侧拒绝明确的错用户/错组订阅。异步请求可跨过订阅到期时刻，结算侧不以到期为由丢弃已准入请求的用量。
- Gemini 的 Responses/Chat 桥复用已有协议转换器；缓存读写与普通输入互斥记账，流式请求带 include_usage，下游 Gemini usageMetadata 保留缓存读。
- 原生桥流中断保留已报告的部分用量，不再凭 EOF 合成正常 STOP；写失败和取消结束读取，首字计时包含等待上游响应头的时间。流式输出前沿用现有预扣续扣逻辑。
- 明确请求校验错误且尚无输出时，保留诊断 token 但费用为 0；500、已经产生的输出、取消或断流不会被一概免单。OpenAI 转通用结算时也保留该免计费标记。

### 回归记录

- 主代理执行过 `go test -tags=unit ./... -count=1 -timeout=8m`，57 个有测试的包通过。首次合并验证暴露旧 handler fixture 缺少 Hydrated/Status/Platform，已补齐真实鉴权应提供的数据，没有放宽生产权限检查。
- 新增真实 Responses、Chat、Messages handler 加假上游的链路测试：备用组返回 hello、不继承主组强制 Fast，记录新组倍率；总输入 1000、缓存读 600 时普通输入为 400。Messages 另验证新组推理档位覆盖旧组策略。
- 最终合并后的定向 race 通过：service 4.982 秒、handler 3.635 秒，覆盖权限、快照回源、渠道映射、跨组预扣、订阅身份、并发所有权和 Gemini 协议/计费。`go vet` 通过；golangci-lint 2.13.0 对 service/handler 检查为 0 issues。全量跨平台及集成验证以提交后的 CI 为准。
- 所有计费试验使用假钱包、内存仓库和假上游。没有修改或补偿真实用户余额，不把测试金额当线上损失。

### 保留边界

- 主组被停用/删除后在鉴权阶段返回 403 的原策略未放开；不能简单删除该拦截来实现备用组提升。
- 无 provider usage 的异常响应不编造 token；作为失败返回并保留诊断。已有部分用量仍按真实计量结算。
- 沿用既有崩溃恢复策略：尚未提交实际用量的 authorized 记录按最初 durable hold 恢复，不能恢复进程内后来观察到的全部用量。本次未修改数据库账本或扩大恢复协议。
- 本次没有变更服务器、Redis、限额、分流、守护或旧实例。GitHub 源码更新不等于线上生效，发布仍需单独灰度验证。
