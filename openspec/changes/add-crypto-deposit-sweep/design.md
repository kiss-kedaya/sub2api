# 技术设计

## 1. 总体架构

```
┌─ sub2api server（Web 进程，无种子无私钥）───────────────┐
│  cryptopay/                                             │
│   ├─ addresses   地址池/惰性分配/展示                   │
│   ├─ scanner     扫链（BSC/Polygon/Plasma/Solana）      │
│   ├─ ledger      入账 → 复用 payment fulfillment        │
│   ├─ sweeper     归集调度（阈值/两段式/记录）           │
│   └─ rpc         自有 RPC 轮换（多端点）                │
│                                                         │
│  admin UI / 用户直充页（Vue 组件增量）                  │
└──────────────┬──────────────────────────────────────────┘
               │ unix socket（仅本机，双向鉴权 token）
┌──────────────▼──────────────────────────────────────────┐
│  cmd/signer（独立进程，持有加密种子与热钱包私钥）        │
│   ├─ /derive   HD 批量派生（BIP39/BIP32）               │
│   ├─ /sign/evm ERC20 transfer 签名（白名单+限额）       │
│   ├─ /sign/sol SPL transfer 签名（白名单+限额）         │
│   └─ audit     全量签名审计日志                         │
└─────────────────────────────────────────────────────────┘
```

- Web 进程**永不接触**种子/私钥；签名服务只认白名单目标（归集热钱包）。
- 扫链只读（自有 RPC），不需要签名；归集才调 signer。
- 全部新增能力**默认关闭**，未配置时进程行为与现状完全一致。

## 2. 与既有支付层的接缝（解耦点）

| 接缝 | 做法 | 影响面 |
|---|---|---|
| ProviderKey | 新增 `payment.TypeCrypto = "crypto"` 常量；`factory.go` 注册 | 纯增量 |
| 入账 | 复用 `PaymentService.HandlePaymentNotification` 语义：建 PAID 订单 → `ExecuteBalanceFulfillment` | 不改造核心，只调用 |
| 订单类型 | `OrderTypeBalance`，`provider_key=crypto`，`payment_type=crypto` | 既有查询/筛选兼容 |
| 前端 | 新页面/组件目录；充值页加 Tab；admin 侧栏加分组 | 既有页面不动 |
| 配置 | `settings` 表新 key 命名空间 `crypto.*`；不启用即无感 | fork 零负担 |

**解耦红线**：不改 `payment/*` 现有文件的行为；如必须扩展接口，只加新方法/新可选接口，不改既有方法签名。

## 3. 地址模型

- **EVM 复用**：一个 EVM 地址（BIP44 `m/44'/60'/0'/0/{i}`）在 BSC/Polygon/Plasma 三链共用。用户只需记住一个 0x 地址；扫描器按链分别扫。
- **Solana 独立**：`m/44'/501'/{i}'/0'`，单独地址。
- **惰性分配**：用户首次访问直充页时从池中取号绑定；池低水位（<10）自动补货（批量派生 50）。
- **回收（二期）**：归集完成 + 90 天无到账 → 标记回收；**不重发**给其他用户（避免历史转账误认），只归档。

## 4. 扫链策略（性能关键）

| 链 | 方法 | 批量 | 轮询间隔 | 确认数 |
|---|---|---|---|---|
| BSC | `eth_getLogs`（address 数组分批 200） | 200/req | 3s | 5 |
| Polygon | 同上 | 200/req | 3s | 5 |
| Plasma | 同上 | 200/req | 3s | 5 |
| Solana | `getSignaturesForAddress` 逐地址 | — | 10s | finalized |

- 游标：`crypto_scan_cursors`（network, last_block / last_signature），每批提交后持久化。
- 重组：确认数窗口内记录 block_hash；发现不一致 → 撤销该块未确认记录（入账前）。
- **地址集合来源**：内存缓存（5 分钟）+ 增量失效；避免每轮全表查地址。
- 降级：RPC 失败轮换端点；连续失败告警（复用既有通知渠道），暂停该链扫描不阻塞其他链。

## 5. 入账规则

1. 匹配：`to` ∈ 我们的地址池（含未分配池）→ 找到归属用户。
2. 金额：按代币精度转 decimal；`>= 单笔最小入账`（默认 0.1 USDT）才入账。
3. 幂等：唯一键 `(network, tx_hash, log_index)`；重复投递直接跳过。
4. 建单：`PaymentOrder{OrderType: balance, Amount: 到账USDT × 充值倍率, PayAmount: 到账, ProviderKey: crypto, Status: PAID}` → fulfillment（余额、审计、邮件、返佣全复用）。
5. 归集排除：`sweep_records` 的 tx（from ∈ 我们的地址）**不触发入账**——扫链时先按 from 排除；这是必须的护栏，否则归集会自我入账。

## 6. 归集引擎

**触发（全可配）**：
- 单地址下限：BSC/Polygon/Plasma ≥ 3 USDT，Solana ≥ 1 USDT
- 全局触发：合计 ≥ 1000 USDT
- 调度：每日一次（默认 03:00 UTC）+ 手动触发；dry-run 只出计划

**执行（EVM 两段式）**：
```
1. 汇总候选地址（余额≥下限）
2. 读链上实时余额（计划来自入账台账，实际金额以链上为准）
3. gas 不足 → 热钱包从 hot_index 打一笔原生币到子地址，等确认
4. 子地址自己签一笔 ERC20 transfer 到热钱包，等确认
5. 写 sweep_records（gas tx / 归集 tx / 状态 / 失败原因）
```

**执行（Solana）**：费用极低，直接由热钱包给子地址打少量 SOL → 签名归集。

**为什么不用合约 / 第三方托管（2026-10-12 定案）**：

- 这是**开源项目**，部署方式必须对每个 fork 开箱即用。自部署
  `SweepDelegator` 要求每个使用者先部署合约、再配地址，等于把门槛
  转嫁给用户，因此否掉。
- 第三方托管（Fireblocks / Vaultody / MPCVault / ChainUp）同样否掉：
  API 托管会把用户充值资金放进第三方账户体系，平台被风控冻结即资金
  卡死；且多为企业 KYC + 月费（实测 Vaultody 入口档 $249/月起），
  对开源用户不成立。
- Permit2 / EIP-3009 免首笔 gas 的路线在本场景不成立：BSC USDT、
  Polygon USDT、BSC/Polygon USDC 的 runtime bytecode 里都没有
  `transferWithAuthorization`(0xe3ee160e) 或 `permit`(0xd505accf)
  选择器，子地址无 gas 时连 approve 都发不出去。
- 成本实测：两段式每个地址 < 1 美分（BSC gasPrice 0.05 gwei 时约
  $0.0015，Polygon 约 $0.006），可忽略。
- **EIP-7702 留档备用**：BSC/Polygon 已在用 type-4，但省下的 gas 换
  一个自部署合约不值当。若将来链上 gas 长期高企，再在此处切换。

**signer 限额单位**：`max_per_tx` / `max_daily` 用 **raw 单位**（整数
字符串），不是代币个数。BSC USDT 是 18 位小数，1 USDT = 1e18 raw：
配 `"5000000000"` 等于 5e-9 USDT，会把所有签名拒掉。示例见
`deploy/signer-policy.example.json`。

**安全**：
- signer 白名单：只允许转出到归集热钱包地址；单笔上限（默认 5000 USDT）；日累计上限。
- 单实例：PG advisory lock，多实例部署只有一个在跑。
- 失败：重试 3 次（指数退避）→ 标记失败 → 后台可见 → 手动重试。

## 7. 数据库（迁移 `257_crypto_deposit.sql`）

```sql
-- 地址池
CREATE TABLE crypto_address_pool (
  id BIGSERIAL PRIMARY KEY,
  chain_family TEXT NOT NULL,          -- evm | solana
  derivation_index INT NOT NULL UNIQUE,
  address TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'free', -- free | assigned | retired
  assigned_user_id BIGINT,
  assigned_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_pool_status ON crypto_address_pool(chain_family, status);
CREATE INDEX idx_pool_user ON crypto_address_pool(assigned_user_id);

-- 用户地址（分配结果，含链）
CREATE TABLE deposit_addresses (
  id BIGSERIAL PRIMARY KEY,
  user_id BIGINT NOT NULL,
  chain TEXT NOT NULL,                 -- bsc | polygon | plasma | solana
  address TEXT NOT NULL,
  derivation_index INT NOT NULL,
  status TEXT NOT NULL DEFAULT 'active', -- active | disabled
  last_seen_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (chain, address)
);
CREATE INDEX idx_deposit_addr_user ON deposit_addresses(user_id, chain);

-- 到账记录
CREATE TABLE deposit_transactions (
  id BIGSERIAL PRIMARY KEY,
  chain TEXT NOT NULL,
  tx_hash TEXT NOT NULL,
  log_index INT NOT NULL DEFAULT 0,
  from_address TEXT NOT NULL,
  to_address TEXT NOT NULL,
  token TEXT NOT NULL,
  amount NUMERIC(38,18) NOT NULL,
  block_number BIGINT NOT NULL,
  block_hash TEXT NOT NULL DEFAULT '',
  confirmations INT NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT 'pending', -- pending | credited | ignored | reverted
  order_id BIGINT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (chain, tx_hash, log_index)
);
CREATE INDEX idx_deposit_tx_to ON deposit_transactions(to_address, chain);
CREATE INDEX idx_deposit_tx_status ON deposit_transactions(status, chain);

-- 归集记录
CREATE TABLE sweep_records (
  id BIGSERIAL PRIMARY KEY,
  chain TEXT NOT NULL,
  from_address TEXT NOT NULL,
  to_address TEXT NOT NULL,
  token TEXT NOT NULL,
  amount NUMERIC(38,18) NOT NULL,
  gas_tx_hash TEXT DEFAULT '',
  sweep_tx_hash TEXT DEFAULT '',
  status TEXT NOT NULL DEFAULT 'planned', -- planned | gas_sent | swept | failed
  error TEXT DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_sweep_status ON sweep_records(status, chain);

-- 扫链游标
CREATE TABLE crypto_scan_cursors (
  chain TEXT PRIMARY KEY,
  last_block BIGINT NOT NULL DEFAULT 0,
  last_signature TEXT DEFAULT '',
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

## 8. 配置项（settings 命名空间 `crypto.*`）

| key | 默认 | 说明 |
|---|---|---|
| `crypto.enabled` | false | 总开关 |
| `crypto.chains` | `["bsc"]` | 启用的链 |
| `crypto.rpc.bsc` 等 | 空 | 各链 RPC（支持逗号分隔轮换） |
| `crypto.sweep.global_threshold` | 1000 | 全局触发（USDT） |
| `crypto.sweep.min_per_address.bsc` 等 | 3 / 3 / 3 / 1 | 单地址下限 |
| `crypto.sweep.gas_topup.bsc` 等 | 0.002 BNB | gas 预充额 |
| `crypto.sweep.hot_wallet.evm` / `.solana` | 空 | 归集热钱包（白名单目标） |
| `crypto.confirmations.bsc` 等 | 5 | 确认数 |
| `crypto.credit.min_amount` | 0.1 | 单笔最小入账 |
| `crypto.credit.multiplier` | 1.0 | 充值倍率 |
| `crypto.signer.socket` | `/run/sub2api/signer.sock` | 签名服务 |

## 9. 安全清单

- [ ] 种子/私钥只在 signer；Web 进程无任何私钥材料
- [ ] signer 白名单：目标地址固定 + 单笔/日限额 + 速率限制
- [ ] signer 审计：每次签名（派生/归集）全量记录
- [ ] socket 权限 0600 + 调用方 token 双向校验
- [ ] 热钱包只留运行所需 gas + 归集目标，大额定期转冷钱包（运维流程）
- [ ] 扫链只读；RPC key 不入前端/日志
- [ ] 入账幂等 + 归集排除 from 自有地址（防自入账循环）
- [ ] 后台操作审计（触发归集/停用地址/改阈值）

## 10. 性能清单

- [ ] 扫链批量 `eth_getLogs`（200 地址/req），游标增量，禁全表
- [ ] 地址集合内存缓存 + 增量失效
- [ ] 归集错峰（逐地址串行 + 限速），避免 RPC 打爆
- [ ] 索引覆盖：入账匹配（to_address+chain）、记录查询（status+chain）
- [ ] 所有后台列表分页 + 时间窗
