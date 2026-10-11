# 实施指南（照着写代码版）

## 1. 文件树（新增，全部隔离）

```
backend/
├─ cmd/signer/                      # 独立签名服务
│  ├─ main.go                       # unix socket 启动、配置加载
│  ├─ derive.go                     # BIP39/BIP32 派生
│  ├─ sign_evm.go                   # ERC20 transfer 签名
│  ├─ sign_sol.go                   # SPL transfer 签名
│  ├─ whitelist.go                  # 白名单 + 限额
│  └─ audit.go                      # 审计日志
├─ internal/cryptopay/              # 主模块（Web 侧）
│  ├─ config.go                     # settings 读取与默认值
│  ├─ addresses/
│  │  ├─ pool.go                    # 池管理/补货/惰性分配
│  │  └─ service.go                 # 用户侧 API 逻辑
│  ├─ scanner/
│  │  ├─ evm.go                     # eth_getLogs 扫描
│  │  ├─ solana.go                  # getSignaturesForAddress 扫描
│  │  ├─ cursor.go                  # 游标持久化
│  │  └─ matcher.go                 # 地址匹配 + 归集排除
│  ├─ ledger/
│  │  ├─ credit.go                  # 入账 → 建单 → fulfillment
│  │  └─ idempotency.go             # 唯一约束处理
│  ├─ sweeper/
│  │  ├─ planner.go                 # 阈值判定/计划生成
│  │  ├─ executor.go                # 两段式执行
│  │  ├─ gas.go                     # gas 预充
│  │  └─ scheduler.go               # 定时 + advisory lock
│  ├─ signerclient/
│  │  └─ client.go                  # unix socket 客户端
│  └─ rpc/
│     └─ pool.go                    # RPC 端点轮换
├─ internal/handler/crypto/
│  ├─ user_handler.go               # 用户直充 API
│  └─ admin_handler.go              # 后台 API
└─ migrations/257_crypto_deposit.sql

frontend/src/
├─ views/user/CryptoDepositView.vue         # 直充页（或并入 PaymentView Tab）
├─ views/admin/crypto/
│  ├─ CryptoAddressesView.vue               # 地址管理
│  ├─ CryptoSweepView.vue                   # 归集管理
│  └─ CryptoReconcileView.vue               # 对账
├─ components/crypto/
│  ├─ DepositAddressCard.vue                # 地址+二维码
│  └─ DepositRecordsTable.vue               # 到账记录
└─ api/crypto.ts                            # API 封装
```

## 2. 签名服务协议（unix socket, HTTP/JSON）

```
POST /derive            {"count": 50}
  → {"addresses": [{"index": 0, "evm": "0x..", "solana": "..."}, ...]}

POST /sign/evm          {"index": 3, "chain_id": 56, "token": "0x55d3..",
                         "to": "0xHOT..", "amount": "12345678",
                         "nonce": 12, "gas_price": "1000000000", "gas_limit": 65000}
  → {"signed_tx": "0x02f8..."}

POST /sign/evm/native   {"index": 3, "chain_id": 56, "to": "0xADDR..",
                         "amount": "1000000000000000", "nonce": 13, ...}   # gas 预充
  → {"signed_tx": "0x02f8..."}

POST /sign/sol          {"index": 3, "mint": "Es9v..", "to": "HOT..",
                         "amount": "12345678", "recent_blockhash": "..."}
  → {"signed_tx": "base64..."}

GET  /healthz           → {"ok": true, "seeded": true}
```

校验顺序（每个签名请求）：白名单目标 → 单笔限额 → 日累计限额 → 速率 → 签名 → 审计。

## 3. 关键接口签名（Go）

```go
// cryptopay/addresses
func (s *Service) GetOrAssign(ctx context.Context, userID int64) (*UserAddresses, error)
// UserAddresses{ EVM string; Solana string; Chains []string }

// cryptopay/scanner
type Scanner interface {
    ScanOnce(ctx context.Context, cursor Cursor) ([]Transfer, Cursor, error)
}
type Transfer struct {
    Chain, TxHash string; LogIndex int
    From, To, Token string; Amount *big.Int; Decimals int
    BlockNumber uint64; BlockHash string
}

// cryptopay/ledger
func (s *Ledger) Credit(ctx context.Context, t Transfer) error
// 内部：唯一约束插入 → 排除 from∈own → 匹配用户 → 建单 → ExecuteBalanceFulfillment

// cryptopay/sweeper
type Plan struct { Chain string; Entries []PlanEntry; TotalUSD float64 }
type PlanEntry struct { Address string; Amount *big.Int; NeedGas bool; GasAmount *big.Int }
func (s *Sweeper) BuildPlan(ctx context.Context) (*Plan, error)
func (s *Sweeper) Execute(ctx context.Context, plan *Plan, dryRun bool) ([]SweepResult, error)
```

## 4. 用户/后台 API 路由

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/crypto/deposit-addresses` | 获取（或惰性分配）专属地址 + 链列表 |
| GET | `/api/v1/crypto/deposits` | 到账记录（分页） |
| GET | `/api/v1/admin/crypto/addresses` | 地址列表（筛选/分页） |
| POST | `/api/v1/admin/crypto/addresses/:id/status` | 停用/启用 |
| GET | `/api/v1/admin/crypto/addresses/:id/deposits` | 单地址到账明细 |
| GET/PUT | `/api/v1/admin/crypto/sweep/config` | 归集配置 |
| POST | `/api/v1/admin/crypto/sweep/run` | 手动触发（body: `{dry_run: bool}`） |
| GET | `/api/v1/admin/crypto/sweep/records` | 归集记录（分页/筛选） |
| POST | `/api/v1/admin/crypto/sweep/records/:id/retry` | 失败重试 |
| GET | `/api/v1/admin/crypto/reconcile` | 对账列表 |
| POST | `/api/v1/admin/crypto/reconcile/:id/resolve` | 人工处理 |

## 5. 接线点（对既有代码的最小改动）

| 文件 | 改动 | 性质 |
|---|---|---|
| `internal/payment/types.go` | + `TypeCrypto = "crypto"` | 常量新增 |
| `internal/payment/provider/factory.go` | + case `TypeCrypto` | 分支新增 |
| `cmd/server/wire.go` | + 注册 cryptopay workers（Start/Stop 对齐既有模式） | 接线 |
| `internal/server/routes/*.go` | + 新路由组（admin/user） | 接线 |
| `frontend/src/router/index.ts` | + 3 个 admin 路由 + 1 个用户路由 | 接线 |

**禁止**：改 `payment_fulfillment.go` 的行为逻辑（只允许调用）；改现有 provider；改既有订单状态机。

## 6. 阶段验收标准（每阶段可独立验证）

| 阶段 | 验收 |
|---|---|
| P0 | 分支建立；`go build ./...` 过；迁移可在本地 PG 应用与回滚 |
| P1 | signer 单测：派生确定性与已知向量一致；白名单/限额拒绝；socket 权限 0600 |
| P2 | 惰性分配幂等测试；并发测试；池耗尽降级测试 |
| P3 | mock RPC 端到端：转账→扫描→入账→余额增加；重复投递不双计；归集 tx 不自我入账 |
| P4 | dry-run 计划正确；gas 预充分支测试；advisory lock 单实例测试；重试逻辑测试 |
| P5 | 前端 build + `check:i18n` 过；页面手测清单完成 |
| P6 | 部署文档完成；signer unit 文件；种子生成/备份演练一次 |
| P7 | 每链 1 USDT 真链测试：入账正确、归集成功、记录完整 |

## 7. 风险与回滚

| 风险 | 缓解 | 回滚 |
|---|---|---|
| 私钥泄漏 | signer 隔离 + 白名单 + 限额；热钱包小额 | 换钱包+重新派生 |
| RPC 不稳 | 多端点轮换 + 告警 | 暂停该链（配置关闭） |
| 扫链漏单 | 确认数 + 游标持久化 + 对账页 | 手动重扫窗口 |
| 归集失败 | 重试 + 失败列表 + 手动重试 | 停自动归集（配置） |
| 双实例重复 | advisory lock + 唯一约束 | 无 |

**总开关**：`crypto.enabled=false` 时，所有扫描/归集/分配停止，用户侧不展示直充，进程行为与升级前一致。
