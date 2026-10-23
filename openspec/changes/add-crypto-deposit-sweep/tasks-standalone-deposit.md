> **历史设计说明**：本文件保留最初提案，尚未同步后续实现调整，不作为当前部署或安全契约。独立签名进程、签名 socket、钱包解锁口令及 Solana 收款等描述不代表现行能力；当前实现与验收边界见 [当前实现说明](README.md)。

# 计划：独立充值系统 / 管理端可见性 / 7702 免 gas 归集 / Bark UI

> 2026-10-12。四块独立推进，按优先级排列。

## A. 独立充值页（新的支付系统，与 /purchase 完全分开）

用户诉求：选链 + 选数量 → 生成独占地址 → 明确告知「转入多少到账多少」+ 汇率 6.7。

**路由**：`/deposit`（用户端新页面，不动 `/purchase`）

**后端**（新增，全部走 `/api/v1/crypto/*`）：
- [ ] A1 `GET /api/v1/crypto/deposit-info` → 返回：各链的专属地址、最小入账额、汇率（6.7）、到账说明
- [ ] A2 `GET /api/v1/crypto/deposit-history` → 用户自己的到账记录（分页）
- [ ] A3 汇率配置项 `crypto.credit.display_rate` 默认 6.7（**语义待确认**：展示用换算，入账按 1:1）
- [ ] A4 到账走现有 ledger → fulfillment（已在跑），本页只读状态

**前端**：
- [ ] A5 新页面 `DepositView.vue`：链选择（BSC/Polygon/Plasma/Solana）→ 数量输入 → 展示独占地址 + 二维码
- [ ] A6 醒目提示：这是**专属地址**、任意金额有效、转入多少到账多少、汇率 6.7
- [ ] A7 到账记录列表 + 自动轮询刷新（30s）
- [ ] A8 侧栏加入口（「充值」分组，独立于充值中心）

## B. 管理端地址可见性（余额 / 累计转入转出）

用户诉求：后台看不到地址、看不到余额和累计进出。

**后端**：
- [ ] B1 `GET /api/v1/admin/crypto/addresses` 扩展：不传 user_id 时返回**全部分页列表**
- [ ] B2 每行聚合：链上实时余额（RPC balanceOf，按需）+ DB 累计转入（deposit_transactions 已入账合计）+ 累计转出（sweep_records 已归集合计）
- [ ] B3 `GET /api/v1/admin/crypto/addresses/:id` 详情：关联订单、到账记录、归集记录
- [ ] B4 链上余额查询按批（一次最多 50 地址），结果缓存 60s 防打爆 RPC

**前端**：
- [ ] B5 地址 Tab 重做：分页表格（用户/地址/链上余额/累计转入/累计转出/状态），支持按用户和链筛选
- [ ] B6 详情抽屉：该地址的完整流水

## C. EIP-7702 免 gas 归集

**合约**（每链部署一次，同一 operator）：
- [ ] C1 `contracts/SweepDelegator.sol`：`sweep(token, to, amount)`，仅 operator（gas 账户）可调
- [ ] C2 部署脚本（Foundry 或 solc + 手动广播），三链部署并记录地址
- [ ] C3 安全审查：委托后任何人调 `sweep` 都会被 `require(msg.sender == operator)` 挡住

**签名服务**：
- [ ] C4 `signer` 新增 `/sign/evm/auth`：签 EIP-7702 authorization（`keccak(0x05 || rlp([chainId, delegate, nonce]))`）
- [ ] C5 白名单扩展：delegate 合约地址进白名单校验

**归集执行器**：
- [ ] C6 首次归集：构造 type-4 tx（gas 账户发，authorization_list = 子地址 auth，to = 子地址，data = sweep 调用）
- [ ] C7 后续归集：检测委托已生效 → 普通交易调 `sweep()`（无 auth，省 gas）
- [ ] C8 兜底：链不支持 7702 或委托失败 → 回退现有两段式（打 gas + 归集）
- [ ] C9 执行记录：sweep_records 增加 `mode` 字段（`7702` / `legacy`）

**验证**：
- [ ] C10 测试网（或小额主网）跑通：首次 type-4 → 后续普通 sweep → 资金到热钱包
- [ ] C11 gas 对比：7702 vs 两段式的实际成本

## D. Bark 通知 UI

**用户端**：
- [ ] D1 个人资料页新增 Bark 卡片（设备 key 输入 + 开关 + 测试推送按钮）
- [ ] D2 后端已有（`/api/v1/user/bark`），补「发送测试通知」端点

**管理端**：
- [ ] D3 系统设置页新增 Bark 配置（管理员设备 key，存 settings）
- [ ] D4 看门狗告警 + 上游余额告警走管理员 Bark
- [ ] D5 上游余额阈值配置项 + 定时检查（复用 CNProviderBalanceService）

## 依赖与顺序

1. **A + B** 先做（用户能充、管理员能看，闭环）
2. **C** 需要部署合约（有 gas 成本，需要确认 operator 地址 = 热钱包）
3. **D** 独立，随时可插

## 待确认

- [x] 汇率语义（2026-10-12 确认）：**入账倍率 6.7**（1 USDT 入账 6.7 余额单位），配置项 `credit_multiplier` 默认改为 6.7
- [x] gas 账户（2026-10-12 确认）：**不单独开**。归集热钱包即 gas 账户，留少量 BNB 付 gas（BSC ~$0.01-0.03/笔）。子地址 USDT 全额归集，不从用户扣 gas。
