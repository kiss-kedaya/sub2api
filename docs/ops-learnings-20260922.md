# 2026-09-22 运维经验

这几小时的实操结论。正式仓 `F:\GO\sub2-official-work`。版本笔记在 `docs/release-3xx-*.md`。`.learnings/LEARNINGS.md` 是 9/3 旧条目，不当现网手册。

## 现网门牌

- 公网 `https://kedaya.ai`。旧机 `root@51.222.42.218`（ns575199，40%），新机 `debian@51.161.119.83`（ns572368，60%）。SSH 不要用 `ubuntu@51.222.42.218`。
- Postgres / Redis 都在旧机。新机走 `DATABASE_HOST=REDIS_HOST=10.254.0.2`。改库只连旧机。
- 现网主流量是 342，内网 `8126` / pprof `6088`。341=`8124` 作 nginx backup，340=`8122` 仍在，都不关。旧机数据目录 `/opt/sub2api/data-316`。
- 342 的祖先里已经有 339 利润门、340 非池模式冻结、341 并发换号和 401 黏连。342 自己只加了法律页和 `/canvas` 同源可嵌。工作树里还躺着池模式冻结和删号清黏连，下一次发版不要把没测完的网关 diff 顺手打进去，也不要漏掉已经验过的那两处。
- `DATABASE_MIGRATION_MODE=validate`，预扣关闭，`GATEWAY_GROK_RESPONSE_HEADER_TIMEOUT=600`。不要改价格、倍率、表结构。

## 冻结号

修前：上游 `计费账户已被冻结` / `ref_code=400901` 仍继续接流，用户直接看到 400。
340 已经上的：非池模式命中冻结后账号 `status=error`、`schedulable=false`，换下一号。普通参数 400 不禁用。后台点测试打到同一错误也会停调度。
还没上的：DeepSeek 是池模式。`HandleUpstreamError` 非 401 直接 return，340 的识别走不到。补丁在工作树，必须在这次早退之前处理冻结。342 没带上。

现网止血：账号 `29396`（名 `16213737342`）已软删；流量曾黏到 `29414`（名 `16282198912`）。组 36 可调度只剩两个 hongai：`29159`、`29161`。不要批量打开那 158 个不可调度号。

## 黏连死号

修前：后台删号或标不可调度后，同一会话还打旧号，一堆人跟着报错，不换号。
341 已经上的：401 / 凭证错误会让黏连让位。本地并发打满会换号，全组忙才把并发错误给用户。
还在工作树的：黏连号取不到 / 不可调度 / 软删时，快路径立刻清绑定，换号后黏到新号。利润门取号失败也让位，不要把新号绑回死号。342 没带上。

根因：

1. 网关 `WithSchedulerSnapshotOnly`。黏连从 `sched:acc:{id}` 单独取号，不看组 ZSET。删号后这条 key 仍可能 `schedulable=true`。
2. `tryStickySessionHit` 取号失败原来不清 sticky；高级调度会清，快路径不会。
3. `stickySessionShouldYieldToFailover` 取号失败原来 return false，利润门把流量拧回旧号。
4. failover 分类层会转，所以日志大量 `recovered 200`，但黏连不清、账号不 SetError。
5. `BlockAccountScheduling` 只认 OpenAI/Grok，DeepSeek 是空操作。

## Redis 红线

- Redis 约 540 万 key。禁止整库 `SCAN`，会卡死。只扫 `sticky_session:{group}:*`。
- 禁止 `DEL sched:{group}:*` 整组快照。网关 snapshot-only miss = 用户 502。
- 软删 / 不可调度后至少 `DEL sched:acc:{id}` 和 `sched:meta:{id}`。outbox 可能把 `sched:acc` 写回来，必要时再删，不要手改账号 status 当修复。
- `docker exec kedaya-sub2-redis-1 env -u REDISCLI_AUTH redis-cli`。回复里不要贴 `sched:acc:*` 的 api_key。

## 查库

PowerShell 会吃 SQL 括号、逗号、`$(docker ...)`。正确做法：本地写 `.sql` → `scp` → `docker cp` → `psql -f`。不要在 PowerShell 里直接拼 SQL。机器时区 UTC，后台时间是 CST，对日志要减 8 小时。

## 滚动上线

- 1% 看错，没问题再切全量。失败只切流量回上一稳定版，不停旧进程、不删旧资源。
- 纯上游 502/503/429 不回退。新增平台 panic、进程重启、公网健康挂了才回。
- 切 1% 前先 disarm 上一版守卫，否则改 nginx 会被自动拧回去。
- 前端白屏先看 `/assets/index-*.js` 是不是 502，不要先改 Vue。
- 冻结/黏连修复必须新二进制才对下一只冻结号生效。342 没带这些改动。

## 342 法律页滚动（2026-09-22 晚）

- 新机 nginx SPA 根是 `/opt/sub2api/ui`。只换二进制，用户还是旧 `index.html`，法律页看起来没上线。必须同步静态入口。
- 旧机 HTML 走后端嵌入，机器上没有可换的 `index.html`。新机必须换 `/opt/sub2api/ui`。
- 公网 scp 到旧机大约 1MB/s。正确做法：新机内网 `python3 -m http.server 18080`，旧机 curl `10.254.0.1:18080`。123MB 几秒到。拷完立刻关 18080。
- 新机 `/legal/*` 被 SPA 本地吃掉，不进 342 业务 2xx 计数。灰度攒 50 次成功用 `/site-logo`（会 `proxy_pass`）。
- 切 1% 前必须 stop 上一版 timer 并 disarm。341 守卫还 armed 时改 nginx 会被拧回 341。
- `docs/*` 被 gitignore。发版记录、经验 md 要 `git add -f`。
- 法律页发版不要带工作树里的网关文件。条款空正文前台回退内置稿；后台要管理员点保存才入库。
- 回退：`python3 /opt/sub2api/rollout-342-20260922/rollout342_guard.py rollback`。只切流量和静态入口，不停 342/341/340。

## 利润门串组

修前：智能路由已经选到组 25 或 DeepSeek 组 36，仍然 50ms 502，或者 `all candidates rejected by group profit control`。请求没出站。组 25 自己没开利润门也会中。
修后：339 起，切到实际被调度的组就重绑利润门；那个组没开门就把入口组残留清掉。342 继承了这段，不是法律页才有的。
根因：门装在请求上下文上，吃的是入口组。组 43 阈值 0.18，Grok/DeepSeek 账号倍率 1.0，选号后旧门没卸。key `31896` 是入口 43 路由到组 25；key `22645` 是入口 43 路由到组 36。
怎么验：把组 43 利润门打开，用带 25 或 36 的智能路由 key 打 `grok-4.7` / `deepseek-v4.1-flash`，应出字。组 43 当天 11:57 关过门，关着时看不出来，再开会炸的是没带 339 的旧二进制。

## 只有自己 502

修前：别人打 `grok-4.7` 正常出字，自己 Infinite Canvas / Mirasim 续旧会话全是 502。同一上游不是挂了。
差别在请求，不在号：resume 旧会话、effort 用 `xhigh`/`max`、body 里带着 `previous_response_id`、`include`、`store`、`tools[].external_web_access`。
Mirasim 要生效不能走 Codex 直连，直连只吃 `set` 头，body 规则不跑。头只留 `remove OpenAI-Beta`。body：`remove $.tools[*].external_web_access`，`set $.store = false`，`remove $.previous_response_id`，`remove $.include`。不要改 Authorization，不要加 `X-Provider`。
立刻能通：新开对话，effort 用 `high`。图里配过但没点保存的规则，磁盘上还是空的。

## 前端白屏和画布狐狸页

修前：打开站是白屏，`/assets/index-*.js` 502。点无限画布是拦截页。
白屏根因：nginx 先读 `/opt/sub2api/ui/assets`，没有就回源死端口。新机死端口 8095，旧机和 `sub.kedaya.xyz` 是 8097。340 的 8122 绑在 `10.254.0.1`，不是 `127.0.0.1`。先改回源、把 hashed 文件落到磁盘，不要先改 Vue。
画布根因：全站 `X-Frame-Options: DENY` 加 `frame-ancestors 'none'`，同源 iframe 被 Zen 拦。342 只放开 `/canvas`，其它页继续禁嵌。
怎么验：公网那个 hashed JS 是 200；登录后进无限画布能画，不是狐狸页。

## 误挂分组

修前：批量把账号改成 DeepSeek 之后再改回原组，编辑页仍多挂一个 DeepSeek，组 36 还会选到它们。
根因：批量是往 `account_groups` 加一条，不是替换。2026-09-21 22:53 分组 36 被挂到 207 个号上。
修后：只删「除了 36 还有别的组」的多余绑定。只属于 36 的号不动。备份表 `account_groups_ds36_extra_backup_20260921`。
怎么验：打开那些号的分组，不应再看到 DeepSeek。刷页面即可，不用重启。

## 341 留下的边

这些不是 342 要回退的理由，下次动调度别装没看见：

- Grok 视频查状态/下载绑死创建号。号忙了最多等一会儿，超时仍可能把并发错误给用户。换号会找不到任务。
- 带着上一轮 `previous_response_id` 的对话，粘号一忙就会换号，上一轮上下文会断。
- 用户自己的并发上限满了，还是直接 429，换号没用。
- 分组 9 / 43 的 503「服务暂时不可用」多半是没号，不是平台 panic。分组 44 偶发 `scheduler cache not ready`。纯上游 502/503/429 不回退。
- CI 从 339 到 342 一直红的两条不要当回退：`TestForwardGrokResponsesRetriesInvalidEncryptedContentOnce`（会话 ID 改走请求头），`TestSmartRouteLazyCatalogPreservesSelectionOrder/absent_models_are_skipped`（未映射 GPT 按官方打同平台组）。

## 其它已经踩过的坑

- 入站 `/v1/responses` 被降成 chat，终端工具会坏。上游真支持 responses 就保留；缺 `/v1` 自动补；上游不支持再降 chat。不要全盘强制降级。
- Grok 分组要能走 `/v1/messages`。组开关 `allow /v1/messages dispatch` 关掉，Claude Code 直接 403。
- 本地并发打满不要把 `Concurrency limit exceeded` 回给用户，先换号；全组忙才返回网关并发错误。
- 稳定高缓上游 `input_too_small` 是上游业务 400，不要记进渠道状态错误统计。
- Grok 首字和总耗时几乎一样，优先查入站 `/v1/responses` 有没有被缓冲成非流，不要先改超时。
- 智能路由生视频按入口第一组计价会亏钱。pending 必须记下实际选中组，无人轮询还会漏扣。
- 组 9 / 组 43 大于 1M 的异常请求已按当天要求免单，细节见 `docs/ops-refund-g9-g43-1m-20260922.md`。口算补余额禁止。

## 以后怎么验

1. 打到冻结号：用户应换号成功，后台该号变错误且不可调度，渠道状态不要被这类业务 400 刷红。
2. 删号或标不可调度：同一会话下一发必须落到新号，不能再打旧名。
3. 组 36 快照里只能看到可调度 hongai，不能再出现 29396 / 29414。
4. 公网首页能开，静态入口是 `index-FxUsma-c.js`。`/legal/privacy`、`/legal/disclaimer` 能出条款正文。无限画布不是拦截页。
5. 新机看 `/opt/sub2api/ui/index.html`，不要只看二进制 VERSION。`/assets/index-*.js` 必须 200，不是 502。
6. 组 43 利润门打开时，智能路由打到组 25 / 组 36 仍能出字，不能是 50ms 502。
