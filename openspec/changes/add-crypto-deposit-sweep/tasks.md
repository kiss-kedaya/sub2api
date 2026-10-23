> **历史设计说明**：本文件保留最初提案，尚未同步后续实现调整，不作为当前部署或安全契约。独立签名进程、签名 socket、钱包解锁口令及 Solana 收款等描述不代表现行能力；当前实现与验收边界见 [当前实现说明](README.md)。

# 实施任务清单

> 原则：每个阶段独立可验证；先内部自测（单测+本地链 mock），再 dry-run，最后灰度。
> 分支：`feat/crypto-deposit`（sub2api 仓库，main @ 干净起点）。

## Phase 0 · 骨架与依赖

- [ ] 0.1 新建分支 `feat/crypto-deposit`，提交本 openspec 文档
- [ ] 0.2 `backend/go.mod` 增依赖：`go-ethereum`、`solana-go`、`go-bip39`、`btcd/btcutil/hdkeychain`（对齐 epusdt 版本）
- [ ] 0.3 `internal/cryptopay` 包骨架 + 配置结构（全部默认关闭）
- [ ] 0.4 迁移文件 `261_crypto_deposit.sql`：5 张新表 + 索引（见 design.md §7）

## Phase 1 · 签名服务（独立进程）

- [ ] 1.1 `cmd/signer` 骨架：unix socket HTTP、systemd 凭据读取种子、健康检查
- [ ] 1.2 HD 派生：BIP39→BIP32，EVM `m/44'/60'/0'/0/{i}`、Solana `m/44'/501'/{i}'/0'`；`POST /derive`（批量）
- [ ] 1.3 EVM 签名：ERC20 transfer 构造（nonce/gas 由调用方传入）+ 白名单校验（目标地址、单笔上限）
- [ ] 1.4 Solana 签名：SPL transfer 构造 + 白名单校验
- [ ] 1.5 审计与限额：全部签名请求落审计日志；日限额、速率限制
- [ ] 1.6 单测：派生确定性（对照 epusdt/已知向量）、白名单拒绝、限额拒绝、错误网络拒绝
- [ ] 1.7 种子生成工具（一次性）：`cmd/signer -gen-seed` 输出助记词，人工离线备份

## Phase 2 · 地址分配与展示

- [ ] 2.1 地址池：批量派生（默认 50/批）、`crypto_address_pool` 落库、低水位补货 worker
- [ ] 2.2 惰性分配：用户首次打开直充页/首次建单意图时分配（EVM 一个地址覆盖 bsc/polygon/plasma；Solana 独立）
- [ ] 2.3 用户 API：`GET /api/v1/crypto/deposit-addresses`（列表 + 到账记录分页）
- [ ] 2.4 地址状态：停用/回收（归集完成且长期无活跃 → 可回收进池，二期）
- [ ] 2.5 单测：分配幂等（同一用户重复请求返回同一地址）、池耗尽降级、并发分配

## Phase 3 · 扫链与入账

- [ ] 3.1 RPC 层：各链端点配置 + 失败轮换（复用 epusdt rpc_nodes 思路，简化版）
- [ ] 3.2 EVM 扫描器：`eth_getLogs` 按地址分批（200/批）过滤 USDT/USDC Transfer；游标持久化；确认数策略（默认 BSC/Polygon/Plasma=5）
- [ ] 3.3 Solana 扫描器：`getSignaturesForAddress` 轮询 + 交易解析；确认数 final
- [ ] 3.4 入账：`deposit_transactions` 唯一约束（network+tx_hash+log_index）→ 匹配用户 → 建 PaymentOrder（PAID）→ 复用 fulfillment（余额+审计+通知+返佣）
- [ ] 3.5 汇率/费率：USDT→余额乘数（默认 1.0）+ 手续费率（默认沿用全局），后台可配
- [ ] 3.6 重组处理：确认数内 block hash 变化 → 撤销未入账记录；已入账异常走人工对账页
- [ ] 3.7 单测：金额精度（6/18 位小数）、重复投递幂等、重组场景、限额边界

## Phase 4 · 归集引擎

- [ ] 4.1 余额聚合：按地址/链统计"已到账未归集"金额（索引查询，禁全表扫）
- [ ] 4.2 阈值判定：单地址下限（默认 BSC/Polygon/Plasma=3 USDT，Solana=1）+ 全局触发（默认 1000 USDT，可配）
- [ ] 4.3 EVM gas 预充：余额低于阈值 → 主钱包打 gas（BNB/POL/Plasma 代币）→ 确认 → 归集
- [ ] 4.4 归集执行：构造 transfer（全额）→ signer 签名 → 广播 → 确认跟踪 → `sweep_records`
- [ ] 4.5 调度：定时任务（默认每日）+ 管理端手动触发 + dry-run（只出计划不执行）
- [ ] 4.6 单实例锁：PG advisory lock（对齐 `UsageBalanceSettlementWorker` 模式）
- [ ] 4.7 单测：阈值边界、gas 不足降级、广播失败重试、幂等（同地址同金额不重复归集）

## Phase 5 · 前后端界面

- [ ] 5.1 用户直充页：链 Tab + 地址 + 二维码（复用 qrcode 依赖）+ 到账记录 + 轮询刷新
- [ ] 5.2 充值方式选择：直充（默认）/ 第三方渠道（epusdt 等）并列，admin 可控制显隐
- [ ] 5.3 后台-地址管理：列表（用户/链/地址/余额/最后活跃/状态）、筛选、停用
- [ ] 5.4 后台-归集管理：配置（阈值/调度/gas 额度）、手动触发、记录列表（含失败原因）
- [ ] 5.5 后台-对账页：异常入账（重组撤销、金额异常）人工处理
- [ ] 5.6 i18n：中英文案（`check:i18n` 门禁通过）

## Phase 6 · 配置、部署与文档

- [ ] 6.1 settings 项：开关、RPC 端点、阈值、确认数、签名服务 socket、热钱包地址（白名单）
- [ ] 6.2 部署文档：signer systemd unit、种子备份流程、热钱包初始 gas 准备
- [ ] 6.3 运维文档：归集对账流程、失败处置、RPC 切换
- [ ] 6.4 README/接入文档：fork 用户如何启用/不启用（默认无感）

## Phase 7 · 验证与灰度

- [ ] 7.1 全量单测 + `go vet` + race（新增包纳入 CI）
- [ ] 7.2 本地端到端：mock RPC 全流程（分配→打款→扫描→入账→归集）
- [ ] 7.3 链上小额真实测试：每条链打 1 USDT → 验证入账 → dry-run 归集 → 真归集
- [ ] 7.4 灰度：单链（BSC）开直充 → 观察 48h → 全链
- [ ] 7.5 存量兼容回归：epusdt 渠道与既有订单流不受影响
