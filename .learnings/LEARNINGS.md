## [LRN-20260924-002] release-352

**Logged**: 2026-09-24T19:25:00+08:00
**Priority**: high
**Status**: active
**Area**: production

### Summary
现网已切 0.1.352。管理员批量测模型和 DeepSeek 400001 停号换号都在这个包。nginx 主端口 8214，回退 351 / 8212。

### Details
旧机 40% `10.254.0.2:8214`，新机 60% `10.254.0.1:8214`。新机 SPA 入口 `index-xst0NDWy.js`，账号页 `AccountsView-D4LwHa59.js`。回退：`python3 /opt/sub2api/rollout-352-20260924/rollout352_guard.py rollback`，新机加 `sudo -n`。351/350/349 进程不关。切流量前必须 disarm 上一版 timer。大包走新机再内网拉旧机，拷完关临时端口。DeepSeek 必须同时命中 `ref_code=400001` 和「已达到使用限制或余额不足」，普通 400 不停号。

### Suggested Action
先读 `docs/release-352-20260924.md`。不要把没测完的网关 diff 打进下一包。

### Metadata
- Source: production_release
- Related Files: docs/release-352-20260924.md
- Tags: accounts, batch-test, deepseek, rollout

## [LRN-20260924-001] tickets-351

**Logged**: 2026-09-24T16:56:00+08:00
**Priority**: high
**Status**: active
**Area**: production

### Summary
现网已切 0.1.351。工单回复 Enter 发送、Shift+Enter 换行。nginx 主端口 8212，回退 350 / 8210。DeepSeek 400001 停号换号当时还在 stash，已随 0.1.352 上线。

### Details
旧机 40% `10.254.0.2:8212`，新机 60% `10.254.0.1:8212`。新机 SPA 入口 `index-buE-iNc7.js`，工单块 `TicketsView-BOoG0S55.js`。回退：`python3 /opt/sub2api/rollout-351-20260924/rollout351_guard.py rollback`，新机加 `sudo -n`。350/349 进程不关。切流量前必须 disarm 上一版 timer。大包走新机再内网拉旧机，拷完关临时端口。

### Suggested Action
先读 `docs/release-351-20260924.md`。不要把 DeepSeek stash 打进下一包以外的热路径。

### Metadata
- Source: production_release
- Related Files: docs/release-351-20260924.md
- Tags: tickets, rollout, enter-to-send

## [LRN-20260922-001] ops

**Logged**: 2026-09-22T20:00:00+08:00
**Priority**: high
**Status**: active
**Area**: production

### Summary
这几小时的现网经验已经写进项目目录 `docs/ops-learnings-20260922.md`。版本滚动看 `docs/release-3xx-*.md`。本文件旧条目只保留工作目录校验，不当现网手册。

### Details
冻结号必须在池模式早退前处理。黏连取号失败必须清绑定。Redis 禁止整库 SCAN，禁止删除整组调度快照。PowerShell 不能直接跑带括号的 SQL。组 36 可调度只剩两个 hongai，不要批量打开不可调度号。利润门跟着入口组走，切到组 25 / 36 后必须卸掉组 43 的门。白屏先看 hashed JS 是不是回源到 8095/8097。批量改分组是加绑定不是替换。

### Suggested Action
先读 `docs/ops-learnings-20260922.md`，再动生产。

### Metadata
- Source: production_incident
- Related Files: docs/ops-learnings-20260922.md, docs/release-342-20260922.md
- Tags: sticky-session, billing-frozen, redis, rollout

---

## [LRN-20260903-001] correction

**Logged**: 2026-09-03T20:40:00+08:00
**Priority**: high
**Status**: pending
**Area**: workflow

### Summary
恢复会话时必须先验证实际工作目录、HEAD 和工作树，不能直接沿用上一份汇报中的路径或版本。

### Details
会话摘要指向 `F:\\GO\\sub2-official-work` 和 `v0.1.245`，但当前会话初始目录是另一个只读浅克隆。重新扫描后才定位到包含未提交优化的真实工作树。

### Suggested Action
每次恢复任务先运行 `git status --short --branch`、`git rev-parse HEAD`、`git remote -v`，再读取历史交接资料。

### Metadata
- Source: user_feedback
- Related Files: HANDOFF_2026-09-03.md
- Tags: workspace-validation, handoff

---

## [LRN-20260923-001] codex-sessions

**Logged**: 2026-09-23T04:20:00+08:00
**Priority**: high
**Status**: active
**Area**: production

### Summary
`C:\Users\34438\.codex\sessions` 下 489 个 jsonl（约 5.6GB，2025-09 到 2026-09-22）已拆成两份全文。现网 2026-09-22 那几小时仍以 `docs/ops-learnings-20260922.md` 为准。本条是全量会话里反复出现、下次还会踩的做法。

### 原文在哪
- 我发的：`.learnings/codex-sessions-user-messages.md`（约 16MB）。每轮自动塞进来的 AGENTS.md 和环境块已去掉。
- 模型回的：`.learnings/codex-sessions-model-replies.md`（约 57MB）。只留 assistant 正文，没有工具参数、工具结果和内部 reasoning。
- 两份都在 `.learnings/`，这个目录被 git exclude，不要提交。里面有他当时贴进来的口令、连接串、token，经验正文不抄这些。
- fork 会话会把同一段历史再写一遍。同一句「继续 / 提交 github」出现几十次，是副本，不是几十起独立事故。

### 认门
- 中转正式仓是 `F:\GO\sub2-official-work`。`sub2-review-*`、`sub2-sync-*`、`sub2-fix-*` 是审查树，改那里不等于上线。
- 恢复会话先看 `git status --short --branch`、HEAD、remote。摘要里的版本号经常是旧的，9 月 3 日就把 `v0.1.245` 当成了当前树。
- 公网 `https://kedaya.ai`。旧机 `root@51.222.42.218`（40%），新机 `debian@51.161.119.83`（60%）。不要用 `ubuntu@` 连旧机。库在旧机。
- 2026-09-22 现网是 342 / 8126。341=8124、340=8122 留着，不关。细节和回退命令在 `docs/ops-learnings-20260922.md`。

### 计费
- 修前：首授做成了「这辈子第一次授权免费」，用户连着授权 30 天都不扣。修后：免费只覆盖新 uid 的 6 小时。补扣用 node 脚本先出名单，备份后再 `-apply`。不要手写 SQL 在 PowerShell 里跑。
- 修前：充值是 USDT，余额和月授权价按人民币算。修后：全站统一 USDT。当时汇率按 7.15 把余额和自定义价除回去，官方价向上取整。换过汇率就不能再按这个数除。
- 预扣、倍率、免单以账单为准，禁止口算。组 9 / 组 43 大于 1M token 的免单记录在 `docs/ops-refund-g9-g43-1m-20260922.md`。
- 智能路由生视频按入口第一组计价会亏。pending 必须记下实际选中组。
- 利润门装在入口组上下文上。组 43 开着门，路由到组 25 或 DeepSeek 组 36 后旧门没卸，会 50ms 502 或 `all candidates rejected by group profit control`，请求没出站。339 起切组重绑。组 43 关着门时看不出来，再开会炸旧二进制。
- 分组用量汇总禁止扫全表 `usage_logs`。千万行从几十秒掉到毫秒，靠索引，不靠加机器。

### 调度和黏连
- 用户要的填充调度：同一分组、同一平台、支持该模型的上游，按优先级把池模式重试次数跑完再切下一家，全部试完才把错误还给用户。不要一超时就返回。
- `sub.kedaya.xyz` 套着 Cloudflare。同步把所有上游试完会撞 524（大约 120 秒）。填充调度不能在一条 CF 请求里干等。
- 修前：本地并发打满，直接把 `Concurrency limit exceeded` 给用户。修后：忙号换号，全组忙才返回网关并发错误。用户自己的并发上限满了，换号没用，仍是 429。
- 修前：401 或删掉的号还黏在同一会话。修后：341 对 401 / 凭证错误会让位。删号、不可调度、软删后快路径清黏连，这截还在工作树，342 没带上。
- 黏连从 `sched:acc:{id}` 单独取号，不看组 ZSET。删号后这条 key 仍可能 `schedulable=true`。禁止整库 SCAN，禁止 `DEL sched:{group}:*`。snapshot miss 就是用户 502。
- 软删后至少删 `sched:acc:{id}` 和 `sched:meta:{id}`。outbox 会把 `sched:acc` 写回来。
- 池模式 `HandleUpstreamError` 对非 401 直接 return。340 的冻结识别（`400901` / 计费账户已被冻结）在 DeepSeek 池模式走不到，必须在早退之前禁用并换号。普通参数 400 不禁用。
- 上游短暂 402、超时、`http2: timeout awaiting response headers` 不要把调度开关关掉。恢复后还得人工打开。只在明确的冻结、或 `seven_day > 98` 且 429 时，重置这一只号的额度并恢复这一只。不要批量把 1-112 或那 158 个不可调度号打开。
- 备用组权限收回后，选号成功仍可能换组返回，必须复查用户权限。停用且清退的组，旧会话 sticky 仍可能打进去。
- 请求内 hydration cache 没有版本。failover / 同号重试会拿着旧 credentials、模型映射、代理。WS 没有装上 request-scoped state。账号负载 singleflight 的等待者取消不了。
- 带 `groupID` 的正常请求仍会 `GetByIDLite`。不能宣称 scheduler snapshot 命中就是 0 次数据库。热路径往 PostgreSQL 打，旧机 CPU 和 swap 会先爆。
- Grok 视频查状态绑死创建号。号忙了不能换号，换了就找不到任务。
- 带 `previous_response_id` 的对话，粘号一忙就换号，上一轮上下文会断。

### 协议
- 修前：入站 `/v1/responses` 被预降成 `/v1/chat/completions`，或上游其实是 responses 但网关改成同步非流。用户侧首字等于总耗时，Agent 终端工具不可用。
- 修后：上游真支持 responses 就保留，缺 `/v1` 再补。只有 404/405，或后台强制 Chat，才降 Chat。裸 `/responses` 不要当成已经带版本的完整地址。
- Grok 分组要能走 `/v1/messages`。组开关关掉 `allow /v1/messages dispatch` 时，Claude Code 直接 403，模型名还是空的。
- 入站和上游路径在后台日志里会显示反的。先对真实出站 URL 和是不是流，不要只看使用记录那一列。
- 稳定高缓上游的 `input_too_small` 是业务 400，不要记进渠道状态错误。
- newapi 还在 SQLite 时，`/v1/responses` 会 `SQLITE_BUSY`，外面再包一层 Cloudflare 403。库要迁到 PostgreSQL，不要在锁库上重试把连接打满。

### 滚动上线
- 1% 看错，没新增平台 panic 再切全量。失败只切流量回上一稳定版，不停旧进程、不删旧资源。纯上游 502/503/429 不回退。
- 切 1% 前先 disarm 上一版守卫，否则改 nginx 会被拧回去。
- 前端白屏先看 `/assets/index-*.js` 是不是 502。新机死回源 8095，旧机和 `sub.kedaya.xyz` 死回源 8097。不要先改 Vue。
- 新机 SPA 根是 `/opt/sub2api/ui`。只换二进制，法律页看起来没上线。旧机 HTML 走后端嵌入。公网往旧机 scp 大约 1MB/s，包走两机内网。
- `docs/*` 被 gitignore。发版记录和经验要 `git add -f`。法律页发版不要捎上没测完的网关 diff。
- CI 从 339 到 342 一直红的两条不是回退条件：Grok 会话 ID 改走请求头；未映射 GPT 按官方打同平台组。
- `apply_patch` 对 `F:` 上带 tab 的 Go 文件对不上。改完把那一段重新打开看，函数边界外不能剩半截。

### 提号 / 卡密
- 目录 `F:\JS\提号`，Cloudflare D1。公开接口先止血，不要为了抢号先把校验拆掉。
- `claim-history` 用 `bindIfMissing: true` 时，没绑指纹的卡密会被请求者抢走，还能看见历史账号。
- 扣库存的 batch 和 `claim_records` 插入不是一个事务。插入失败就会扣了额度却没记录。
- 合并卡密后，用户页剩余额度和后台不一致。先对同一张卡的领取记录、剩余额度、合并来源，再改表。
- 账号去重要先归一大小写。大小写不同会被当成两只号，卡密能把「已用过」的号再提出去。
- 单次提号上限跟剩余额度走，不要另写死 50 或 100。
- 管理员要能查指定卡密的合成链路。查剩余额度用聚合，不要把 D1 打满。

### 支付 / 迁移
- 旧 newapi 迁到 sub2api：兑换码金额必须对上真实可用余额，不能只读 `quota`。签到赠送会让迁移页出现负数。GitHub 登录和卡密反查都要能迁，迁完旧余额清零，同一只号不能迁两次。
- 旧站和新站账号、使用记录、余额要同一套，不要并行再写一套用户系统。
- epusdt / 发卡是另一套目录。到账失败先对回调、订单状态、余额，不要先改表。
- 注册流水里的 4060 是上游风控拒绝，同一单重试不会变成功。201 是钱包余额还没到。这不是网关 bug。

### 分组和号池
- 批量改分组是往 `account_groups` 加一条，不是替换。误挂 DeepSeek 组 36 时，只删「除了 36 还有别的组」的那条。只属于 36 的号不动。备份表留着。
- 组 36 可调度曾经只剩两只 hongai。容量紧就加号，不要把不可调度池一次性打开。

### 前端
- 无限画布是同源 iframe。全站 `X-Frame-Options: DENY` 加 `frame-ancestors 'none'` 时，Zen 会弹出拦截页。只放开 `/canvas`。
- 条款空正文前台回退内置稿。后台必须点保存才入库。
- 页面没变，先看静态入口 hash 和 `/opt/sub2api/ui/index.html`，不要只看二进制 VERSION。

### 以后怎么用这两份原文
1. 先读本条和 `docs/ops-learnings-20260922.md`。
2. 要原话再搜 `.learnings/codex-sessions-user-messages.md`。
3. 要当时怎么收口再搜 `.learnings/codex-sessions-model-replies.md`。
4. 搜到口令、连接串、token，用完即止，不要写回本文件，不要进 git。

### Metadata
- Source: codex rollout jsonl 489 files
- Related Files: .learnings/codex-sessions-user-messages.md, .learnings/codex-sessions-model-replies.md, docs/ops-learnings-20260922.md
- Tags: sessions, billing, scheduler, rollout, card-claim, protocol

---

## [LRN-20260923-002] openclaw-habits

**Logged**: 2026-09-23T12:00:00+08:00
**Priority**: high
**Status**: active
**Area**: workflow

### Summary
从 `C:\Users\34438\.openclaw` 的 workspace 记忆、日记和会话里抽出的习惯。只记做法和历史错误。口令不抄。目录按 2026-09-23 实际存在为准。

### 目录核对
还在：
- `F:\GO\sub2-official-work`
- `E:\Users\34438\Downloads\dujiao`
- `F:\JS\提号`
- `F:\JS\mailbox-pool-vercel`
- `F:\JS\wa-local-api`
- `E:\Users\34438\Desktop\常用项目\openai`
- `F:\JS\quark-auto-save`
- `F:\JS\binanceauth-69`
- `F:\JS\dujiaoka`
- `F:\JS\kedaya-card-claim`
- `F:\JS\LangBot`
- `F:\JS\infinite-canvas`
- `E:\Users\34438\Desktop\常用项目\CLIProxyAPI`
- `E:\Users\34438\Desktop\常用项目\dujiao-next-server`
- `E:\Users\34438\Desktop\常用项目\grokzhuce`
- `E:\Users\34438\Desktop\常用项目\mailfree`

不在：`C:\Users\34438\Desktop\常用项目\openai`。桌面常用项目在 E 盘，不在 C 盘。

支付有两套。到账问题先确认是 `E:\Users\34438\Downloads\dujiao` 还是桌面 `dujiao-next-server`，不要改错目录。

### 他的习惯
- 中文，短，先结果。先做完再汇报，不要中途刷屏。
- 搜索、检查、读取必须真的用工具。构建、修复、部署没验证不准说完成。
- 高风险才问：删文件、改系统配置、重启服务、不可逆改库、对外发消息、改生产。
- Windows，只用 PowerShell。不要套 Linux 命令。GBK 控制台打印中文会乱，留档写 UTF-8。
- 代理先核实。旧档案写的是 `127.0.0.1:7890`。
- Python 用 `uv run --directory <项目>`。pytest 排除 `release/staging` 复制目录。
- GitHub `kiss-kedaya`，主分支 `main`。改前做检查点，构建不过不准推。
- Wails：`wails dev` 看日志端口。`wails build` 不要 `-clean`。
- 改 `openclaw.json` 后重启 gateway 才生效。
- Claude Code MCP 用户配置是 `C:\Users\34438\.claude.json`。带空格的路径直接改 JSON。
- 容易封号的注册、社媒、批量登录，不要写成无人值守死循环。他说停就停进程。
- 口令进配置，不进 md，不进 git，不进回复。

### 已经踩过
- 参数粘成 `5--workers`，先看少空格，不要先改脚本逻辑。
- 宝塔报找不到 main、缺 `requests`：不要用系统 Python 在 `/www/server/panel/install` 里启动。用面板解释器，cwd 设到项目目录。
- Telegram 周报失败是因为接收方不是数字 chat id。
- Notion token 用 PowerShell `Write-Host` 接不回来。要在脚本内部拿，再发请求。追加 block 的 body 是 `{children: [...]}`，不是裸数组。curl 走代理时会把 rich_text 剥掉，用 `Invoke-RestMethod` 加 UTF-8 字节。
- `lcm_grep` 报 `no such table: conversations` 时，改查会话文件和 memory。
- ida-mcp 服务端日志正常、客户端报 initialize：先彻底重启客户端。
- `openclaw cron status` 可能先打出结果再被 SIGKILL。看输出，不要只看退出码。
- 类型加字段后，手写 fallback 没补，`npm run build` 报缺字段。样式连改后回读 CSS 文件末尾，minify warning 当失败。
- 三段式仓库只做 build 的 CI，会把 fmt/类型漂移带进 main。
- 本地 skill 装进 Codex 的命令是 `npx skills add <仓库路径> -a codex -g --copy -y`。

### 不要做
- 把 `C:\Users\34438\Desktop\常用项目` 当成号池目录。
- 把桌面 dujiao-next 和下载目录里的 dujiao 当成同一个项目。
- 把 jailbreak 提示词写进生产配置或业务日志。
- 口头说记住了，却不写进 `LEARNINGS.md` 或 `memory/`。

### Metadata
- Source: .openclaw/workspace MEMORY、USER、RULES、TOOLS、memory/*.md，以及 agents 会话抽样
- Related Files: C:\Users\34438\.codex\kedaya-system-prompt.md, docs/ops-learnings-20260922.md
- Tags: habits, paths, windows, openclaw

---

## [LRN-20260923-003] openclaw-pass2

**Logged**: 2026-09-23T18:40:00+08:00
**Priority**: high
**Status**: active
**Area**: workflow

### Summary
第二遍读了 `C:\Users\34438\.openclaw` 的日记、期刊、agent 记忆，以及 `agents/*/sessions` 里 51 个 jsonl。只补习惯和历史错误。口令不记。现网口径仍以 `docs/ops-learnings-20260922.md` 为准。

### 读了什么
- 日记：`workspace/memory/2026-03-24.md` 到 `2026-04-20-hermes-fix.md`，加上 `lessons.md`、`projects.md`、根上的 `MEMORY.md` / `USER.md` / `RULES.md` / `TOOLS.md`。
- 期刊：`journals/digest/digest-2026-03-20.md` 到 `digest-2026-04-19.md`。
- 会话：`main`、`openclaw-expert`、`full-stack-architect`、`full-stack-architect-2`、`cntent-ops-agent`。消息在 `type=message`，`message.role`，`content[].text`。这 51 个文件里用户消息 954 条，去重后大量是飞书包装和同一句重复，不是 954 起新事故。
- `memory/dreaming` 的 2026-06-08 和 2026-06-15 没有可晋升的长期事实。
- 助手侧反复报错：`Connection error`、`Request aborted`、Gemini 429、账号被停、缺 token/projectId、上下文满、上游不认 `developer` role。这些不是网关业务代码坏了。

### 目录再核对（2026-09-23）
还在：
- 上一版已经核对过的中转、支付、提号、号池、桌面常用项目。
- `F:\GO\label-printer`
- `F:\GO\freight-order-placer`
- `F:\PY\goofish\goofish-browser`
- `F:\IDA Professional 9.2\ida-mcp.exe`
- `C:\Users\34438\.openclaw\workspace\tools` 下的 `antigravity-rotator-v2`、`geetest`、`tg-video-automation`、`script-runner`、`content-analyzer`、`CSAI`

不在：
- `C:\Users\34438\Desktop\常用项目`
- `workspace\tools\clawpanel`
- `workspace\tools\clawpanel-plans`
- `workspace\tools\vnetplay`
- `C:\Users\34438\.hermes`

支付二开仍是 `E:\Users\34438\Downloads\dujiao`。桌面 `dujiao-next-server` 不是同一个目录。

### 要复用的习惯
- 中文，先做完再汇报。禁止道歉、奉承、免责套话、emoji。不确定写未经验证。
- 口头「记住了」不算。写进 md。
- Windows 只用 PowerShell。禁止 `ls` / `rm` / `grep` / `cat`。禁止新建 `.bat`。
- 中文脚本用 PowerShell 或 `.cmd`，UTF-8 BOM。Python 设 `PYTHONIOENCODING=utf-8`，控制台再加 `PYTHONUTF8=1`。
- 复杂 JSON 不用 `ConvertTo-Json`。路径写进 JSON 用正斜杠。带空格的路径直接改 JSON。
- 大文件回滚用 `git checkout <commit> -- <file>`。禁止 `git show` 管道进 `Set-Content`。
- 改 `openclaw.json` 前对照 `openclaw.json.last-good`。文件突然缩到几百或几千字节就是写坏，先恢复。改完重启 gateway，当前这轮不生效。2026-04-15 和 04-18 留过 clobbered 备份。
- 重复劳动要自动化。注册、登录、社媒不要无人值守硬跑。
- 高风险才确认：生产库、删文件、改系统配置、对外发消息。其余直接做。不要把 OpenClaw 里后加的「一律不许问」搬过来。
- Codex 自己改代码。2026-03-07 那句「主 agent 别改，交给子 agent」只针对当时的 OpenClaw 架构师，当时 token 不够。
- 不要把 2026-04-11 的人设漂移、积分、回复开头 UUID 写进生产提示词。
- Telegram 的 `allowFrom` 和接收方都要数字 id，不认 `@用户名`。发文件用消息工具加绝对路径，不要写 `MEDIA:`。提人用 `tg://user?id=`，不要用反引号包。
- 子代理只做目录分开、文件不撞车的并行。不要去调另一个主 agent。收口自己做。
- 构建没本地跑通，不准说做完，不准推。`npm` 先 install 再 build。

### 已经踩过
- 宝塔 Python 用系统解释器，会在 `/www/server/panel/install` 里找不到 main，还会缺 `requests`。用面板解释器，cwd 设到项目。
- 注册脚本参数粘成 `5--workers`，先看空格。停进程先看 PID。
- `tools.exec.host` 默认值和 sandbox 冲突过，要改成 `gateway`。
- `openclaw` 全局升级失败过：旧目录被占用，Node/npm 的 PATH 和 shim 混在一起。
- Hermes：补丁不进旧进程；多开 gateway 会抢 `getUpdates`；Windows 控制台编码会把启动打印打崩。先清进程和过期锁，只留一份。
- `daily-evolution` 文档写过 `digest_daily.py`，磁盘上没有。文档和实现必须一致。
- `lcm_grep` 报 `no such table: conversations` 就改查会话和 memory。
- ida-mcp 日志正常、客户端报 initialize：先重启客户端。二进制在 `F:\IDA Professional 9.2\ida-mcp.exe`。Claude 用户级 MCP 在 `C:\Users\34438\.claude.json`，不是 `.claude\settings.json`。
- `openclaw cron status` 可能先给出结果，进程再被 SIGKILL。看输出，不看退出码。
- Windows 装本地 skill 到 Codex：`npx skills add <仓库路径> -a codex -g --copy -y`。
- 启动慢先查顶层 `cv2` 一类重 import。
- freight-order-placer 猜过 API 参数，出过 401/500。参数按源码对。
- antigravity-rotator-v2 曾经配额空白、刷新按钮没反应。后端取数和前端绑定要一起看。
- clawpanel 曾经要先 `npm install`，否则找不到 vite。这个目录 2026-09-23 已经不在。
- 模型 429、账号被停、缺登录、上下文满、`developer` role 被拒、连接被中断：先换档、重新登录或缩短会话，不要改业务代码。
- `SOUL.md` 进过 jailbreak 文本。不要再写进生产配置、业务日志和仓库。

### 不要做
- 去 `C:\Users\34438\Desktop\常用项目`。
- 去已经不在的 `clawpanel`、`clawpanel-plans`、`vnetplay`。
- 把桌面 dujiao-next 和下载目录的 dujiao 当成同一个项目。
- 把 OpenClaw 工作流习惯写进现网手册。
- 把会话里的口令、key、连接串抄进 md、git 或回复。

### Metadata
- Source: .openclaw memory、journals、agents MEMORY/RULES/ERRORS、sessions jsonl
- Related Files: C:\Users\34438\.codex\kedaya-system-prompt.md, C:\Users\34438\.codex\AGENTS.md
- Tags: habits, paths, openclaw, windows, corrections

---

## [LRN-20260923-004] grok-sessions

**Logged**: 2026-09-23T19:10:00+08:00
**Priority**: high
**Status**: active
**Area**: workflow

### Summary
读了 `C:\Users\34438\.grok\sessions`。146 个 `updates.jsonl`，用户消息块 787 条，去重后约 447 条。只记习惯和历史纠正。口令、key、连接串不抄。原文不再导出成 md。现网版本仍以 `docs/ops-learnings-20260922.md` 和服务器为准，不要改成会话里的 242 到 303。

### 读了什么
- 用户消息在 `session/update` 的 `user_message_chunk`。模型正文在 `agent_message_chunk`。`agent_thought_chunk` 是思考，不当结论。
- 压缩稿在各会话 `compaction/segment_*.md`，看 `## Summary`。反复约束是：中文、禁止 `git reset --hard`、不提交 `.artifacts/` 和交接文档、预扣保持关、现网 `8080` 的迁移模式不要改成 `apply`。
- 定时任务、`<system-reminder>`、别的会话转来的 `session-message` 出现很多次。那不是他的新指令。连通性测试写「别读文件」就不是任务。
- fork 和交接会把同一句话再贴一遍。按一次纠正算，不要按出现次数算事故。

### 目录再核对（2026-09-23）
还在：
- `F:\GO\c3api`
- `F:\GO\c3api-reference-20260903`
- `F:\GO\is7qin-sub2api`
- `F:\GO\new-api-reference`
- `F:\GO\sub2-official-work`
- `F:\GO\sub2-fix-grok-route-340`
- `F:\JS\infinite-canvas`
- `F:\JS\LangBot`
- `E:\Users\34438\Downloads\dsh-lazy-pack-v5`
- `E:\Users\34438\Downloads\Telegram Desktop`

不在，不要再去：
- `F:\GO\sub2-review-20`
- `F:\GO\sub2-sync-upstream`
- `F:\GO\sub2-wt-canvas-sidebar`
- `F:\GO\sub2-wt-channel-gray`
- `F:\GO\sub2-wt-error-503`
- `F:\GO\sub2-wt-gemini-compat`
- `F:\GO\sub2-wt-leak-309`
- `F:\GO\sub2-wt-video-media`
- `F:\GO\sub2-wt-release-01294`

`sub2-fix-grok-route-340` 还在，但它是修复副本。改它不等于改正式目录。

### 要复用的习惯
- 中文，短句，先结果。群机器人可以一两句、不有求必应。那是塔菲 / LangBot 的人设，不是 Codex 的汇报格式。
- 「修复 / 看代码 / 合并 / 只要不上线 / 只读审查」不是上线许可。只有他说上线才动生产。
- 上线后如果出现计费 503，或 Grok 流式被收成非流，先回退，不要在现网上接着改。
- 偶发 502 不要自己重启，不要自己摘流量。他说别停，就看日志和热点。Grok CLI 的 `session/prompt timed out` 不是站点挂了。
- 不要把流量全赶到新机。旧会话说过库机大约留 30%。现网门牌是 40/60。以 nginx / Cloudflare 当时权重为准。改权重先确认是不是有两个池，否则改完用户侧还是 1。
- 管理后台不要挂公网。`51.222.42.218:17777` 他明确反对。没有当次明确要求，不要把 8080 对公网打开。
- 切的是新请求。长请求留在旧实例，下次发版再替换已经空出来的进程。正式版稳定后，金丝雀占 CPU 就下掉。会话里的「先打 10%」和「直接全切」都不要覆盖手册里的 1%。
- 智能路由按他排的顺序逐个试。当前组没有可调度账号才下一组。上游 4xx 和审核失败不换组。不要按平台或白名单预先跳过。同一次请求不要从 OpenAI 协议切到 Claude。
- 模型列表看目录，不看图标。空 mapping 的 OpenAI 组不能抢走 gemini。只绑一个 OpenAI 组还走 `/v1/messages` 时，分组要开允许 messages 分发。
- 不要限制密钥的分组数量。手动加进 Grok 组 25 仍报 `No eligible Grok media accounts`，先查媒体号资格，不要回他「组没绑上」。返回视频 id 不等于生成成功。
- 智能路由的模型列表要包含全部可用分组，不是第一组。计费按实际选中组。列表慢，不要改成扫全表。
- 视频先对路径和字段。他要的是 `/v1/videos` 的 JSON，不是 `/v1/videos/generations` 的 multipart，也不要挂到 chat 或 messages。创建字段对 `id` / `request_id`，查询 404 对抛出点。`Videos API is not supported` 和 xAI `415` 先对文档和真实上游返回。
- 他说这次不要映射模型，发版就不要加别名。他会当成掺水。
- 他要关掉响应头超时并多加重试。出站可以重试。不要用响应头超时切断还在出字的流，也不要在一条 Cloudflare 请求里干等。`http2: timeout awaiting response headers` 不要因此把号的调度关掉。
- 仪表盘、吞吐、趋势、首屏走时间窗或汇总表。禁止扫几百万行错误日志，禁止全表 COUNT/SUM。
- 无限画布仓库是 `basketikun/infinite-canvas`，本地 `F:\JS\infinite-canvas`。侧边栏打开，不要跳页。跨域允许调用方域名。要能一键创建包含全部分组的智能路由 key。生图、生视频的正常调用都要能用。库机可以跑独立进程，不能占 `8080/8081/5432/6379`，也不能启动库机上的网关。
- 支付页：全局倍率会显示实付、手续费、到账。供应商单独设置也要显示。不要只改全局。动 Cloudflare 账单或权重前先说明。
- 群机器人：5 分钟内同一人重复发不回，不是固定每 5 分钟回一次。群友互相解答时别插话。提问、艾特、点名要回。不回复不要发 `Thinking...`。入站用流式 `/v1/responses`，思考档 `medium`。浏览器实例复用，消息并行，输入状态结束要停。状态图用东八区，只发图。20 秒删除规则后来他说去掉。私聊和群分开限流，被限流要告诉剩余时间。删掉的工具不要自己加回来。
- 本地落后 `origin/main` 先认门。仓库收干净，能复用就复用。worktree 不改主工作区未跟踪文件，禁止 `git reset --hard`，不要提交 `.artifacts/`、`plan.md`、`progress.md`、`findings.md`、`task_plan.md`、密钥。
- 错误先确认还在。不在就不要改热路径。CPU 瞬时 80% 先看两台机器，再谈改代码。热路径可参考 `F:\GO\c3api` 和 `F:\GO\is7qin-sub2api`，不要把参考仓库拷进生产。带 groupID 的请求仍可能打一次库，不能宣称严格 0 次数据库。
- 提示词缓存要 1 小时，不要默认 5 分钟。没重启就没生效。

### 已经踩过
- 把流量全切到新机，库机不再扛请求。他顶回来过。
- 管理口 `17777` 能用公网 IP 打开。他明确反对。
- 上线后计费服务 503，同时 Grok 分组流式变成非流。他要求先回退，不要轻易上线。
- 偶发 502 时主动重启或摘流量。他说可能只是偶发。
- 智能路由预先按平台跳过。他要按列表顺序一个个试。
- 智能路由只给出第一组模型，或最多绑 10 个组。他不要这个限制。组加进去了仍没有可用 Grok 媒体号，是另一个问题。
- 视频走错路径或挂到 chat/messages，创建成功但查询 404，或平台直接说不支持 Videos API。先对官方和画布契约，不要猜。
- 改了 Cloudflare 权重但还有第二个池，用户看到的权重仍是 1。
- 群机器人背课文、艾特必回、不回复还刷 `Thinking...`、入站打在 `/v1/chat/completions`、每次新建浏览器、输入状态不停。
- 状态图时区不是东八区，或者回了文字「渠道状态 · 90m」。20 秒删除是旧要求，后来取消。
- 供应商单独的充值倍率和手续费不显示实付、手续费、到账。
- 在落后 `origin/main` 的本地目录上继续堆提交，分支又脏又重复。
- 只让看代码、合并，结果改了生产，站点 502。
- 把响应头超时打开后，正常长请求被切断。反过来，在一条 Cloudflare 请求里把重试加满会 524。两头都踩过。

### 不要做
- 把 242 到 303 写成现在的线上版本。
- 用会话里的 10% 或「直接全切」覆盖现网 1% 滚动。
- 去上面列出的、已经不在的 worktree。
- 把 Grok 原文、key、上游地址上的口令写进 md、git 或回复。
- 把塔菲的人设、贴纸包、群链接写成 Codex 对他说的话。
- 把「禁止私聊打模型」写成全站规则。那是某一版机器人提示词，后来又要求私聊单独限流。以那只机器人当前配置为准。

### Metadata
- Source: .grok/sessions updates.jsonl 与 compaction summary，临时摘录已删除
- Related Files: C:\Users\34438\.codex\kedaya-system-prompt.md, docs/ops-learnings-20260922.md
- Tags: habits, grok, routing, rollout, video, telegram

---
