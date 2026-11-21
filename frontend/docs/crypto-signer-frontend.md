# 签名分片前端（数据密钥 / Shamir 2-of-3）交付说明

本文件配套前端 PR：后台「加密支付 → 签名分片」页面与「立即归集」解锁交互。
后端内核在 PR **#519**（`feat/vault-shards-backend`），本 PR **只含前端**，依赖 #519 先合并。

> ⚠️ 合并前必读：PR #519 **没有注册 `/admin/crypto/signer/*` 路由**（`backend/internal/server/routes/crypto.go` 未改动），当前分支上 handler 方法虽然存在但不可达。前端按 PR 描述与 handler 内部路径判断逻辑对接，若 #519 不合入下面第 2 节的路由补丁，页面会 404。建议在 #519 合并前补上。

## 1. 页面入口

| 位置 | 组件 | 作用 |
| --- | --- | --- |
| 加密支付 → 概览（钱包安全监控下方） | `frontend/src/components/crypto/signer/SignerShardsPanel.vue` | 分片状态、生成/重新拆分/验证入口、锁定引导 |
| 生成分片弹窗 | `SignerShardSetupWizard.vue` | 当场 TOTP → 阻断式风险确认 → 浏览器生成 DK/拆分 → 展示 B/C 一次 → 回填校验 → 提交 A |
| 展示单份分片 | `ShardRevealCard.vue` | 掩码→显示一次、复制（定时清剪贴板）、下载 txt、A4 打印（含二维码/时间/恢复说明/警告） |
| 归集解锁弹窗 | `ShardUnlockDialog.vue` | 输入分片 B/C（粘贴/相机扫码/上传二维码图片）+ 当场 TOTP → 提交归集 → 审计信息（地址数/金额/TxHash） |
| 分片输入 | `ShareInputPanel.vue` + `QrCameraScanner.vue` | 粘贴归一化、本地二维码解码、相机降级 |
| 归集页 | `frontend/src/views/admin/CryptoView.vue` | 锁定横幅、`立即归集` 按钮在锁态改为引导解锁 |

纯逻辑在 `frontend/src/utils/signer/`：
`gf256.ts`、`shardFormat.ts`（S2V1）、`cryptoProvider.ts`（WebCrypto/noble）、`walletVaultV3.ts`（v3 信封）、`material.ts`、`clipboard.ts`、`qr.ts`、`download.ts`、`errors.ts`、`audit.ts`。

## 2. 与后端 #519 的接口契约（按 handler 反推，已逐项核对）

| 方法 | 路径 | 请求 | 说明 |
| --- | --- | --- | --- |
| GET | `/api/v1/admin/crypto/signer/status` | - | 公开的 `ShardStatus`（不泄密） |
| GET | `/api/v1/admin/crypto/signer/risk-notice` | - | 3 条风险要点 + 中/英短语 + schemes |
| POST | `/api/v1/admin/crypto/signer/setup` | `{totp_code, risk_phrase, data_key, share_a, confirm_share, dk_commitment}` | `data_key`=base64(32B)、`dk_commitment`=base64(SHA-256(域前缀‖DK))；**body TOTP**，不走 5 分钟 grant |
| POST | `/api/v1/admin/crypto/signer/resplit` | 同上 | 要求 DK 承诺值与既有记录一致，旧 B/C 全部作废 |
| POST | `/api/v1/admin/crypto/signer/verify-share` | `{share}` + 头 `X-Crypto-TOTP` | 验证分片属于本钱包；中间件要求第二因子头 |
| POST | `/api/v1/admin/crypto/signer/auto-sweep` | `{enabled,totp_code,...}` | 自动归集分片开关（前端本轮只展示状态，未做开关交互） |
| POST | `/api/v1/admin/crypto/sweep/run?async=1&dry_run=0` | `{share}`（锁态时） | 归集带上分片 B/C；未锁态可不带 |
| POST | `/api/v1/admin/crypto/wallet/export-mnemonics`、`/addresses/:id/export-private-key`、`/wallet/hot-mnemonic` | body 增加 `{share}` | 锁态导出需要分片；API 客户端已支持，本轮未做输入 UI（见第 7 节） |

错误码映射（前端 `errors.ts` → `admin.crypto.signer.errors.*`）：
`CRYPTO_SIGNER_SHARE_REQUIRED`、`CRYPTO_SIGNER_SHARE_INVALID`、`CRYPTO_SIGNER_RATE_LIMITED`（5 次失败锁 15 分钟文案）、`CRYPTO_SIGNER_SHARDS_NOT_CONFIGURED`、`CRYPTO_SIGNER_SHARDS_EXIST`、`CRYPTO_SIGNER_STATE_INVALID`、`CRYPTO_SIGNER_RISK_ACK_REQUIRED`、`CRYPTO_SIGNER_TOTP_REQUIRED`、`CRYPTO_HTTPS_REQUIRED`、`CRYPTO_TEST_MODE_READ_ONLY`。

### 若 #519 未补路由，在 `backend/internal/server/routes/crypto.go` 的 `RegisterCryptoAdminRoutes` 中追加

```go
crypto.GET("/signer/status", cryptoHandler.AdminSignerStatus)
crypto.GET("/signer/risk-notice", cryptoHandler.AdminSignerRiskNotice)
crypto.POST("/signer/setup", cryptoHandler.AdminSignerSetup)
crypto.POST("/signer/resplit", cryptoHandler.AdminSignerResplit)
crypto.POST("/signer/verify-share", cryptoHandler.AdminSignerVerifyShare)
crypto.POST("/signer/auto-sweep", cryptoHandler.AdminSignerAutoSweep)
```

（`CryptoAdminSecurity` 中间件已按路径后缀识别 `/signer/setup|resplit|auto-sweep` 并要求 body TOTP；`verify-share` 需 `X-Crypto-TOTP` 头，前端已按此实现。）

## 3. 前后端逐字节互操作证明

- 分片 golden vector 由 PR #519 的 Go 包 `cryptopay/wallet/shards` 生成，固化为
  `frontend/src/utils/signer/__tests__/fixtures/goInteropVectors.json`；前端在 **WebCrypto 与 noble 两条后端** 下都必须复现同样的 3 个 share 字符串、commitment 与 2-of-3 还原结果。
- 例：`secret={0x01,0x02}`、系数 `{0x01,0x01}` → 分片 1 = `S2V1-AEBA-GAIC-AAB6-ZCMX-QY`（与 Go `TestGoldenVector` 相同）。
- 双向验证（本地执行，未提交临时文件）：
  1. 前端生成 3 份分片 + 承诺值 → Go `ParseAndCombine` 任意两份还原同一 secret、承诺值一致；
  2. Go 生成 `sub2api-wallet:v3:` 固定 nonce 密文 → 前端 `openWalletVaultV3` 解出完全相同的 JSON；
  3. 前端生成固定 nonce 密文 → Go 用同一 AAD/前缀解密成功（另含随机 nonce 向量）。
- v3 信封：`AES-256-GCM`，输出 `"sub2api-wallet:v3:" + base64(nonce[12] || ct||tag)`，AAD=`sub2api-wallet:v3:`（与 Go `cipher.NewGCMWithRandomNonce` 一致）。

## 4. 浏览器兼容与降级矩阵

| 能力 | 正常路径 | 降级 / 禁用 | 说明 |
| --- | --- | --- | --- |
| 分片/加密 | WebCrypto（AES-GCM + SHA-256 + getRandomValues） | `@noble/hashes` + `@noble/ciphers` 纯 JS | 两条路径输出逐字节一致；两条都由 `getRandomValues` 供随机 |
| 非安全上下文（http） | - | **直接禁用**并提示用 HTTPS/localhost | 绝不做弱随机兜底 |
| 无 `getRandomValues` | - | 禁用 | 即使有 `subtle` 也不启用 |
| 剪贴板 | `navigator.clipboard.writeText` | `document.execCommand('copy')` → 再失败提示手动抄写 | 复制成功后 30s best-effort 清空剪贴板并提示 |
| 二维码生成 | 本地 `qrcode`（打包内） | - | 不依赖 CDN |
| 扫码 | `getUserMedia`（HTTPS + 授权），iOS `playsinline`、`facingMode: environment`，失败重试裸 `video:true` | 被拒/不可用/非 HTTPS → 自动降级手动粘贴 + 上传二维码图片（`jsqr` 本地解码） | iOS Safari 需在用户手势内打开相机 |
| 下载 | Blob + `<a download>` | iOS 可能打开预览 → UI 始终提供「复制」兜底 | 下载失败给出明确文案 |
| 打印 | 新窗口 A4 打印页（分片/时间/恢复说明/警告/二维码） | 弹窗被拦截 → 提示改用下载/复制 | 打印页含敏感信息警示 |
| 输入框 | 等宽、可换行，`autocomplete/autocapitalize/autocorrect/spellcheck` 全关 | 非法字符明确报错 | 粘贴归一化：NFKC 全角→半角、去空格/换行/各类连字符/零宽字符、转大写 |
| 移动端 | iPhone SE（320px 起）单列、不横向溢出；暗色跟随主题 | - | `max-sm:flex-col`、`wrap-anywhere`、`break-all` |
| i18n | zh/en 全量键 | - | `check:i18n` 通过 |

## 5. 自动化测试

```bash
cd frontend
pnpm vitest run src/utils/signer src/components/crypto/signer src/api/__tests__/admin.crypto.signer.spec.ts src/views/admin/__tests__/CryptoView.signer.spec.ts
pnpm run check:i18n
pnpm exec vue-tsc -b
pnpm run lint:check
pnpm run build
```

覆盖：Go golden vector 双后端复现、任意 2 份还原/单份不可/重复拒绝/checksum 检测、跨 split 承诺值不匹配、粘贴归一化与非法字符、v3 信封固定 nonce 向量 + AAD 防重放、WebCrypto/noble 输出一致、非安全上下文/无 CSPRNG 禁用、剪贴板回退与定时清空、二维码不可用降级、风险短语精确校验、生成→回填→提交全流程（断言只提交 A + 回填那份）、提交失败保留材料、不落 localStorage/sessionStorage/IndexedDB/Cookie/URL（含源码静态扫描测试）、归集锁态引导与审计展示。

## 6. 人工验证清单（三引擎 E2E 未跑：环境只有 Python Playwright chromium-headless，且 #519 路由缺失，无法跑通真实后端流程）

前置：HTTPS 或 `https://localhost`（否则正确行为是禁用并提示）；已绑定 2FA；已初始化 v2 钱包；**先做数据库备份**。

1. Chrome / Edge / Firefox / Safari(iOS)：
   - 概览出现「签名分片」卡片，显示密码学后端（WebCrypto）。
   - 点「生成分片」→ 6 位码（当场输入）→ 阻断式风险确认：3 条要点可见；不输入短语时按钮禁用；输入错误短语不可提交；输入后端下发短语（中/英）后可继续。
   - 展示 B/C 各一次：点「显示分片」看到文本与二维码；复制后提示 30s 清剪贴板；下载 txt 成功（iOS 变预览时用复制）；打印打开 A4 页。
   - 勾选「我已抄写」→ 回填其中一份（粘贴/扫码/上传图片）→ 校验通过才可提交；提交成功后页面显示 vault v3，B/C 从 DOM 消失，刷新后无法再查看。
   - 概览显示「已启用 / 已锁定」，归集页出现锁定横幅。
2. 归集：点「立即归集」→ 弹窗输入 B（粘贴/扫码/上传二维码）→ 6 位码 → 提交 → 显示地址数、金额、TxHash；错误分片提示「无法解开本钱包」；连续错 5 次提示 15 分钟锁定。
3. 浏览器降级：
   - 用 `http://<非 localhost>` 打开 → 分片入口禁用并提示 HTTPS，无弱随机路径。
   - 拒绝相机权限 → 扫码自动降级为手动粘贴/上传，不阻塞。
   - 断开网络后（页面已加载）生成流程仍能本地完成加密（提交时才会失败）。
4. 移动端：iPhone SE 宽度（320×568）下概览/弹窗/展示页不横向溢出；iOS Safari 相机扫码可用、`playsinline` 不跳全屏；暗色模式文字可读。
5. i18n：切 en，风险要点/短语/错误提示均为英文且短语可提交（后端同时接受中/英）。

## 7. 未完成 / 后续

- **#519 路由注册缺失**（见第 2 节补丁），需先补再合本 PR。
- 导出助记词 / 导出单地址私钥 / 更换 Gas 助记词在 v3 锁态同样需要分片：API 客户端已加可选 `share` 参数，本轮未做这三个入口的分片输入 UI，锁态下会显示后端 `CRYPTO_SIGNER_SHARE_REQUIRED` 错误。
- 自动归集分片开关（`/signer/auto-sweep`）只展示状态，未做开启/关闭交互与二次风险确认。
- 三引擎 Playwright E2E 与真实链 RPC 端到端未跑（环境/前置限制），以上人工清单替代。
