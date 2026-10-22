> **历史设计说明**：本文件保留最初提案，尚未同步后续实现调整，不作为当前部署或安全契约。独立签名进程、签名 socket、钱包解锁口令及 Solana 收款等描述不代表现行能力；当前实现与验收边界见 [当前实现说明](../../README.md)。

## ADDED Requirements

### Requirement: 归集触发必须三层可配
系统 SHALL 按「单地址最低归集额 + 按链汇总 + 全局合计触发（默认 1000 USDT）」判定归集；全部参数 MUST 后台可配且默认关闭自动归集。

#### Scenario: 达到全局触发
- **WHEN** 各链待归集合计 ≥ 全局阈值且存在 ≥ 单地址下限的地址
- **THEN** 系统 MUST 生成归集计划

#### Scenario: 未达触发
- **WHEN** 合计 < 全局阈值
- **THEN** 系统 MUST 只更新统计，MUST NOT 发起任何链上交易

### Requirement: EVM 归集必须两段式且 gas 可控
系统 SHALL 对 EVM 链执行「gas 预充 → 归集」两段式：子地址 gas 余额低于阈值时，由热钱包先转入 gas（BNB/POL）；确认后由子地址转出全额代币到热钱包。gas 预充额度 MUST 可配。

#### Scenario: gas 不足
- **WHEN** 子地址 gas 余额不足以完成归集
- **THEN** 系统 MUST 先发送 gas 预充交易并等待确认，再发送归集交易

#### Scenario: gas 充足
- **WHEN** 子地址 gas 余额充足
- **THEN** 系统 MUST 直接发送归集交易，跳过预充

### Requirement: 归集必须通过签名服务并受限额约束
系统 SHALL 把所有归集交易的签名请求发给签名服务；签名服务 MUST 校验目标地址属于白名单（归集热钱包）且单笔/日累计金额在限额内；超限 MUST 拒绝并记录审计。

#### Scenario: 目标不在白名单
- **WHEN** 归集请求的目标地址不是配置的热钱包
- **THEN** 签名服务 MUST 拒绝签名

#### Scenario: 超限额
- **WHEN** 单笔或日累计超过配置上限
- **THEN** 签名服务 MUST 拒绝并记录

### Requirement: 归集执行必须留痕、幂等且可重试
系统 SHALL 为每个地址的归集写 `sweep_records`（计划/发 gas/已归集/失败 + tx + 错误）；同一地址同一余额 MUST NOT 重复归集；失败 MUST 指数退避重试（默认 3 次）后标记失败供人工处理。

#### Scenario: 正常归集
- **WHEN** 归集交易确认
- **THEN** 记录 MUST 更新为 swept 并保存 sweep_tx_hash

#### Scenario: 广播失败
- **WHEN** 广播失败
- **THEN** 系统 MUST 重试；超过重试上限 MUST 标记 failed 并展示在后台

### Requirement: 归集调度必须单实例且支持 dry-run
系统 SHALL 用 PostgreSQL advisory lock 保证多实例部署下同一时刻只有一个归集任务执行；MUST 提供 dry-run 模式：只输出计划（地址/金额/预估 gas）不广播。

#### Scenario: 多实例
- **WHEN** 两个实例同时触发归集
- **THEN** 只有一个 MUST 获得锁执行，另一个 MUST 跳过

#### Scenario: dry-run
- **WHEN** 管理员以 dry-run 触发
- **THEN** 系统 MUST 返回计划列表且 MUST NOT 发送任何交易
