# 追加任务：确认入账 / 看门狗 / Bark 通知

> 2026-10-12 追加。三项独立，可分别验证。

## A. 快速确认入账（避免广播期撤单）

现状：EVM 扫链按固定 `confirmations`（默认 5）等待；速度与安全都不够明确。

- [ ] A1 缩短轮询间隔：EVM 3s → 1.5s（可配），Solana 10s
- [ ] A2 确认数按链可配（已有），BSC/Polygon 默认从 5 降到 3（BSC 3 块 ≈ 9s）
- [ ] A3 **确认判定改为"最终性"而非"计数"**：
  - BSC/Polygon：等待 `safe` 块标签（`eth_getBlockByNumber("safe")`）或 N 块
  - 每笔入账前复核 `eth_getTransactionReceipt.status == 1`（成功而非 revert）
- [ ] A4 撤单保护：入账后记录 block_hash；后续检测重组时**冻结**该笔（不自动回滚余额，进对账）
- [ ] A5 入账前二次校验：receipt 存在 + status=1 + 块已确认 + tx 未被替换

## B. 看门狗合并进 Go（替代 bash + python）

现状：`/usr/local/bin/sub2api-watchdog.sh`（bash，60s 探测 /health，3 次失败重启 unit）。

- [ ] B1 新建 `backend/cmd/watchdog`（Go 单二进制）
- [ ] B2 功能对齐 bash 版：HTTP /health 探测、失败计数、达阈值重启 unit、never touch nginx
- [ ] B3 新增：**RPC 健康检查**（复用 cryptopay rpc 池，逐链探块高）
- [ ] B4 新增：**上游余额阈值**检查（读 sub2api 库的账户余额字段，低于阈值告警）
- [ ] B5 新增：**Bark 告警**（复用 A 节的 bark 客户端）
- [ ] B6 保留为独立 systemd unit（`sub2api-watchdog.service`），替换现有 bash 脚本

## C. Bark 通知（管理端 + 用户端）

现状：epusdt 有 barkwatch（systemd 凭据 + Bark API），sub2api 只有邮件通知。

- [ ] C1 新建 `backend/internal/notify/bark`：Bark 客户端（对齐 epusdt 的 bark.go 协议）
  - POST `https://api.day.app/{key}`，字段 title/subtitle/body/group/level
- [ ] C2 管理端 Bark 配置：settings 存 `bark.admin.device_key` + 开关
- [ ] C3 用户端 Bark 配置：用户表加 `bark_device_key`（用户自己填），个人设置页可配
- [ ] C4 用户通知场景：**余额不足提醒**（复用现有 BalanceLowNotify 触发点，加 Bark 通道）
- [ ] C5 管理端通知场景：**看门狗消息**（B5）+ **上游余额低于阈值**（B4）
- [ ] C6 通知去重/限流：同一事件 N 分钟内不重复推送
- [ ] C7 凭据保护：device_key 加密存储（复用现有 SecretEncryptor），API 不回显明文

## 验证

- [ ] V1 单测：确认判定（receipt 校验、重组冻结）、Bark 客户端字段、限流
- [ ] V2 实机：真实转账 → 观察确认到入账耗时；手动 revert 场景（测试网）
- [ ] V3 实机：看门狗探测真实 /health + 一次模拟失败重启
- [ ] V4 实机：Bark 真机收到管理端与用户端推送
