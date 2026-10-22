> **历史设计说明**：本文件保留最初提案，尚未同步后续实现调整，不作为当前部署或安全契约。独立签名进程、签名 socket、钱包解锁口令及 Solana 收款等描述不代表现行能力；当前实现与验收边界见 [当前实现说明](../../README.md)。

## ADDED Requirements

### Requirement: 扫链必须使用自有 RPC 且只读
系统 SHALL 使用配置的自有 RPC 端点扫描 BSC、Polygon、Plasma、Solana；扫描 MUST 只读（无需签名）；RPC 失败 MUST 轮换端点并在全部失败时告警，MUST NOT 阻塞其他链。

#### Scenario: RPC 轮换
- **WHEN** 当前 RPC 端点连续失败
- **THEN** 扫描器 MUST 切换到下一端点重试
- **THEN** 全部端点失败 MUST 记录错误并暂停该链，其他链继续

### Requirement: EVM 扫描必须批量过滤并按游标推进
系统 SHALL 用 `eth_getLogs` 按地址分批（默认 200/批）过滤 ERC20 Transfer 事件；游标 MUST 每批提交后持久化；重启 MUST 从游标续扫。

#### Scenario: 批量过滤
- **WHEN** 地址集合为 500 个
- **THEN** 扫描器 MUST 分 3 批（200/200/100）请求日志
- **THEN** MUST NOT 对全链全量拉取日志

#### Scenario: 重启续扫
- **WHEN** 进程重启
- **THEN** MUST 从 `crypto_scan_cursors` 的 last_block 继续，MUST NOT 从头全量重扫

### Requirement: Solana 扫描必须按地址轮询并解析
系统 SHALL 用 `getSignaturesForAddress` 轮询每个 Solana 地址；MUST 解析 SPL Token transfer 的 from/to/amount；确认级别 MUST 为 finalized。

#### Scenario: Solana 到账
- **WHEN** 用户向分配的 Solana 地址转入 USDT
- **THEN** 扫描器 MUST 在 finalized 后解析出转账并进入入账

### Requirement: 入账必须幂等且可重放
系统 SHALL 以 `(chain, tx_hash, log_index)` 唯一约束保证入账幂等；重复投递 MUST 跳过；入账 MUST 复用既有支付 fulfillment 管线（余额、审计、通知、返佣），MUST NOT 另写余额逻辑。

#### Scenario: 正常入账
- **WHEN** 到账金额 ≥ 单笔最小入账
- **THEN** 系统 MUST 创建 `order_type=balance`、`provider_key=crypto`、状态 PAID 的订单
- **THEN** MUST 通过既有 fulfillment 增加余额并触发审计/通知/返佣

#### Scenario: 重复投递
- **WHEN** 同一 `(chain, tx_hash, log_index)` 被扫描两次
- **THEN** 第二次 MUST 被唯一约束/状态检查跳过，余额 MUST 只增加一次

#### Scenario: 低于最小额
- **WHEN** 到账金额 < 单笔最小入账（默认 0.1 USDT）
- **THEN** 记录 MUST 保存且状态为 ignored，MUST NOT 入账

### Requirement: 归集交易必须排除自入账
系统 SHALL 在扫描匹配时排除 `from` 为本系统地址（归集热钱包或子地址）的转账；该护栏 MUST 有测试覆盖。

#### Scenario: 归集 tx 不被入账
- **WHEN** 子地址将资金归集到热钱包（from=子地址）
- **THEN** 该 tx MUST NOT 触发任何余额增加

### Requirement: 确认数与重组必须处理
系统 SHALL 按链配置确认数（默认 5）后才入账；确认窗口内 MUST 记录 block_hash，发现重组 MUST 撤销未入账记录（状态 reverted）且 MUST NOT 产生负余额。

#### Scenario: 重组撤销
- **WHEN** 已记录（未入账）的 tx 所在块被重组
- **THEN** 记录 MUST 标记 reverted
- **THEN** 若已入账且无法自动撤销，MUST 进入人工对账列表
