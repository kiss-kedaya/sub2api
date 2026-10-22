> **历史设计说明**：本文件保留最初提案，尚未同步后续实现调整，不作为当前部署或安全契约。独立签名进程、签名 socket、钱包解锁口令及 Solana 收款等描述不代表现行能力；当前实现与验收边界见 [当前实现说明](README.md)。

# 链上直充与自动归集（内置加密支付渠道）

## Why

当前链上充值依赖外挂 epusdt（共享收款地址 + 金额匹配模式）：多个用户共用一个地址，靠精确金额区分订单；金额尾数一旦碰撞或用户打错金额，就产生人工对账。用户要求改为**每用户独立充值地址**（像交易所充值页那样），并担心归集 USDT 的手续费成本。

同时，sub2api 现有支付层已是 provider 抽象，但链上能力全部在外挂服务里，无法被后台统一管理，也无法让分叉仓库按需接入。因此把 epusdt 中真正需要的功能模块用 Go 重构，作为**内置渠道**直接落进 sub2api，归集/地址/记录全部纳入后台管理。

已对齐的决策（2026-10-12）：
1. 归集走**独立签名服务**（种子与签名隔离于 Web 进程）
2. **Go 原生重构**所需模块和前端，整体进 sub2api（不做外挂 HTTP 调用，不塞进单进程巨石）
3. 地址用 **HD 种子统一派生**（惰性分配）
4. 接受**热钱包私钥加密存本机**（签名服务侧持有）
5. **全切地址模式**（新用户默认直充地址；旧共享地址+金额模式保留为存量兼容，可下架）
6. **不做 ETH 主网与 TRON**（归集复杂度与手续费大头砍掉）
7. 归集链：**BSC / Polygon / Plasma / Solana**；EVM 走"gas 预充→归集"两段式，Solana 费用忽略不计
8. 归集触发：**全局合计 ≥ 1000 USDT（可配）** + 单地址最低额（可配），定时批量作业

## What Changes

- 新增内置链上直充渠道：BSC / Polygon / Plasma（EVM）与 Solana，支持 USDT（首期）+ USDC（次期），每用户独立充值地址。
- 新增 **HD 地址派生与惰性分配**：种子只在签名服务；sub2api 维护地址池（批量派生、按需分配、可回收）。
- 新增**独立签名服务** `cmd/signer`：持有加密种子与热钱包私钥，只对白名单目标（归集热钱包）签名，带单笔/日限额与审计。
- 新增**扫链入账**：自有 RPC（多端点）+ 批量日志过滤 + 确认数策略 + 重组处理；入账复用现有 fulfillment 管线（余额、审计、通知、返佣全兼容）。
- 新增**归集引擎**：阈值判定（单地址下限 + 按链 + 全局触发）、EVM gas 预充、签名广播、确认跟踪、`sweep_records` 全程留痕、dry-run 模式。
- 新增**管理后台页面**：地址管理（用户/链/地址/余额/活跃度/停用）、归集管理（配置/手动触发/记录）、扫码对账（异常入账处理）。
- 新增**用户充值页直充 Tab**：各链专属地址 + 二维码 + 到账记录 + 自动刷新。
- **解耦保证**：全部新代码收敛在 `backend/internal/cryptopay/**` 与 `cmd/signer`；对既有支付层只做**增量**接线（新 ProviderKey 注册、新 PaymentType 常量、fulfillment 入口复用）；默认关闭，未配置不影响任何既有行为；其他 fork 不配置即无感。

## Capabilities

### New Capabilities

- `deposit-addresses`: HD 派生、地址池、惰性分配、地址展示、停用与回收。
- `deposit-scanning`: 多链扫块、转账匹配、确认数、入账（余额增加）与幂等。
- `sweep-engine`: 阈值归集、gas 预充、签名广播、记录与对账、dry-run。
- `signing-service`: 种子/私钥保管、派生 API、白名单签名、限额与审计。
- `deposit-admin-console`: 管理后台地址/归集/记录页面与用户直充页。

### Modified Capabilities

- `payment-provider-registry`: 注册新的内置 ProviderKey（`crypto`），只增不改。
- `payment-fulfillment`: 增加"内部确认"入口（链上到账直接进入既有 fulfillment），复用幂等与审计。

## Impact

- 新增表：`deposit_addresses`、`deposit_transactions`、`sweep_records`、`crypto_scan_cursors`、`crypto_address_pool`（共 5 张，纯新增）。
- 新增代码：`backend/internal/cryptopay/**`（模块）、`backend/cmd/signer/**`（独立二进制）、`backend/cmd/server` 少量接线、前端新页面/组件。
- 依赖新增：`go-ethereum`（EVM 构造/签名/RLP）、`solana-go`（Solana）、`go-bip39` + `hdkeychain`（HD 派生）。
- 配置新增：RPC 端点（各链）、归集阈值、gas 预充额度、确认数、签名服务 socket 路径等，全部走 settings/环境变量，默认关闭。
- 不影响：现有 epusdt 渠道、其他支付 provider、订单/退款/对账核心流程。
