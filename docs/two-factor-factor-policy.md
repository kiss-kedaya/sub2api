# 双因子因子策略（TOTP + Passkey）

状态：已实现（本仓库 `feat/passkey-2fa-policy`）
适用版本：2.3.153 之后

> 2026-11-26 变更：**step-up（敏感操作二次验证）改为任选其一**——同时绑定 TOTP 与 Passkey 的账号，验证其中任意一个因子即可放行；**密码登录不变**，仍要求全部已绑定因子。下文各处已按新规则更新。

## 策略（老板口径，代码唯一入口：`service.TotpService.RequiredFactors*` / `service.StepUpSatisfiedFactors`）

| 账号已绑定 | 密码登录 | 管理面风险操作（step-up） |
| --- | --- | --- |
| 仅 TOTP | 密码 + TOTP（现状不变） | TOTP step-up 5 分钟凭证（现状不变） |
| 仅 Passkey | 密码 + Passkey assertion | Passkey step-up 5 分钟凭证 |
| TOTP + Passkey | 密码 + TOTP + Passkey（顺序固定先 TOTP 后 Passkey） | **TOTP 或 Passkey 任选其一**有有效 step-up 凭证即可 |
| 都没有 | 密码（现状不变） | 403 拒绝（`STEP_UP_TOTP_NOT_ENABLED`，提示启用 TOTP 或注册 Passkey） |

补充规则：

- **判定依据**只有两条：`users.totp_enabled = true`；`passkey_credentials` 存在该用户记录。全局功能开关只影响「能否注册/验证」，不参与「是否必须验证」的判定。
- **5 分钟窗口**：每个因子各自独立签发 5 分钟凭证（TOTP / Passkey 各一枚），同一因子 5 分钟内免重复验证；凭证过期、绑定不匹配、Redis 故障一律回到验证挑战。
- **fail-closed**：任何一步失败（策略读取失败、凭证存储故障、凭证缺失/过期）都不签发令牌、不放行风险操作。step-up 只统计**已绑定因子**的凭证：未绑定因子的凭证（例如刚解绑的 Passkey 残留的 5 分钟授权）永远不算，「零个已验证因子」永远不算满足。登录没有降级路径，必须全部已绑定因子都完成。
- 同时绑定两者时，Passkey 服务全局不可用不再锁死 step-up（TOTP 已验证即通过）；只绑 Passkey 的账号仍然 fail-closed。登录仍要求 Passkey。
- 登录 5 分钟凭证跨入口复用（login / password 两种 scope），但**按因子隔离**：TOTP 凭证不能当 Passkey 凭证用，反之亦然。

登录时 TOTP 是否要求仍沿用既有全局开关语义（`SettingService.LoginRequiresTOTP`），保证只绑 TOTP 的账号行为完全不变；Passkey 因子只看绑定状态，不受该开关影响。

## 凭证结构

### 1. 会话级 step-up 授权（风险操作门控）

Redis key（每因子独立）：

```
totp:stepup:<user_id>:<factor>:<session_key>
value = "1"，TTL = 5m
factor ∈ {totp, passkey}；session_key = refresh token family（无则为 u<user_id>）
```

- TOTP 入口：`POST /api/v1/user/totp/step-up` → 写 `factor=totp`。
- Passkey 入口：`POST /api/v1/user/step-up/passkey/begin` + `finish` → 断言成功后写 `factor=passkey`。
- 管理面中间件只读**已绑定因子**（`RequiredFactors`）的授权，任一因子有效即放行（`service.StepUpSatisfiedFactors`），并回传 `X-StepUp-Skipped: 1`、`X-StepUp-Remaining`（取满足 step-up 的授权里最晚过期的一个）与 `X-StepUp-Factors`（满足的因子，如 `totp` 或 `totp,passkey`）；一个有效授权都没有返回 `403 STEP_UP_REQUIRED`，响应体 `metadata` 带 `step_up_mode=any` 与 `factors=totp,passkey`（可选因子），审计记录 `two_factor_satisfied_factors`（放行）/ `two_factor_missing_factors`（拒绝）。授权读取故障仍是 `503 STEP_UP_UNAVAILABLE`。
- `GET /api/v1/user/2fa/policy` 的 `required_factors` 仍是「已绑定因子」（登录与对话框都要用），新增 `step_up_mode: "any"` 说明 step-up 规则。
- 「校验当前密码」与 Passkey 增删这两类入口用 5 分钟不透明 Cookie 凭证门控（`requirePasswordVerificationGrant`），与 step-up 中间件同一条规则：任一已绑定因子的凭证有效即可。

### 2. 不透明 2FA 凭证（登录 / 密码校验免重复）

复用 PR #538 结构，新增 `factor` 维度（Redis key 仍为 `auth:2fa_grant:<sha256(token)>`）：

```json
{
  "user_id": 123,
  "scope": "login | password",
  "factor": "totp | passkey",
  "ip_hash": "...", "ua_hash": "...",
  "issued_at": 1700000000, "expires_at": 1700000300,
  "reissued": false
}
```

- Cookie 名：TOTP 沿用 `sub2_2fa_grant`；Passkey 为 `sub2_2fa_passkey_grant`。两者都是 `HttpOnly; Secure; SameSite=Lax; Path=/`，Body 不回显。
- 服务端只存 `SHA-256(token)`；审计只记录 issued/used/rejected、scope、因子、剩余秒数、IP/UA 不可逆指纹，不记录 token/动态码/断言。
- 凭证只能在其签发因子下消费；跨因子复用按「未知凭证」拒绝并清 Cookie。

## 登录编排（密码登录）

1. `POST /api/v1/auth/login`（邮箱+密码）→ 校验凭据；
2. 计算 `LoginRequiredFactors`（TOTP 依全局开关，Passkey 依绑定）；
3. 对每个因子检查 5 分钟不透明凭证，命中即视为该因子已完成；
4. 全部完成 → 直接签发令牌；否则创建带因子状态的登录会话，返回：

```json
{
  "requires_2fa": true,
  "temp_token": "...",
  "required_factors": ["totp", "passkey"],
  "passed_factors": [],
  "next_factor": "totp",
  "user_email_masked": "u***@example.com"
}
```

5. `next_factor=totp` → `POST /auth/login/2fa`（`temp_token` + `totp_code` + `email` + `password`）。TOTP 通过后：
   - 若还缺 Passkey → 返回 `next_factor=passkey` + `passkey_session_token` + `passkey_options`（WebAuthn assertion 挑战，Redis 5 分钟一次性），**不签发令牌**；
   - 否则签发令牌并下发 TOTP 因子 5 分钟凭证。
6. `next_factor=passkey` → `POST /auth/login/2fa/passkey/finish`（`temp_token` + `session_token` + `credential`）。断言校验通过且登录会话内 `RequiredFactors ⊆ PassedFactors` 才签发令牌；否则 `403 TWO_FACTOR_FACTORS_INCOMPLETE`。

「凭据错误」路径仍下发与「只缺 TOTP」账号逐字节一致的挑战结构，保持防枚举契约不变。

## 前端

- 登录页：按 `required_factors` 依次弹 TOTP / Passkey；Passkey 用 `navigator.credentials.get`，浏览器不支持时展示降级文案（`auth.passkeyUnsupported`）而不是静默失败。
- step-up 弹窗（全局 gate）：打开时读取 `GET /api/v1/user/2fa/policy`，并列展示已绑定的验证方式（TOTP 输入框 + 「用 Passkey 验证」，或只绑一个时只展示那一个）；`step_up_mode=any` 时验证任意一个因子就 `onVerified()` 并立即重试原请求，一个因子失败/被限速不影响改用另一个。Passkey 不可用时展示降级文案（双因子账号仍可用 TOTP；只绑 Passkey 时只能取消，不会让风险操作放行）。
- i18n：`zh` / `en` 同步。

## 测试矩阵

| 场景 | 登录 | step-up 门控 | 覆盖位置 |
| --- | --- | --- | --- |
| 只绑 TOTP | 密码+TOTP（现状） | TOTP 凭证通过 | `service/two_factor_policy_test.go`、`middleware/step_up_factor_matrix_test.go`、`middleware/step_up_any_factor_test.go` |
| 只绑 Passkey | 密码+Passkey | 缺 Passkey 凭证 403；有则通过 | 同上 + `handler/login_factor_policy_test.go` |
| 两者都绑 | TOTP 后继续 Passkey，缺一不发令牌 | 任一因子凭证有效即通过；一个都没有 403 | 同上 + `handler/step_up_any_factor_test.go`、`server/routes/stepup_any_factor_e2e_test.go`（真实 Redis 键布局端到端） |
| 都没绑 | 密码直登 | 403 `STEP_UP_TOTP_NOT_ENABLED` | 同上 |
| 5 分钟免重复 | 因子凭证命中直接登录 | 窗口内跳过并回传 headers | `service/twofa_grant_factor_test.go`、`repository/totp_stepup_factor_test.go` |
| 过期重验 | 凭证过期回到挑战 | 窗口过期 403 | 同上 + middleware 矩阵 |
| fail-closed | 策略/存储故障不签发 | 策略/存储故障 503；未绑定因子的凭证不算 | 同上 |
| 跨因子复用 | TOTP 凭证不能跳过 Passkey | — | `service/twofa_grant_factor_test.go`、`handler/login_factor_policy_test.go` |

前端：`LoginView.dualFactor.spec.ts`（Passkey-only / TOTP→Passkey / 不支持降级 / TOTP-only）、`TotpStepUpDialog.dualFactor.spec.ts`（双因子任选其一 / Passkey-only / 降级 / 策略失败 TOTP 兜底 / 无两步走残留文案）。

## 风险与回滚

- **Redis key 结构变化**：step-up 授权 key 由 `totp:stepup:<uid>:<session>` 变为 `totp:stepup:<uid>:<factor>:<session>`。升级后旧 key 不再命中，用户最多重新验证一次（5 分钟窗口），不涉及数据迁移；回滚会同样失效新 key，行为对称。
- **step-up 任选其一的安全影响**：一个因子泄露（连同一个有效会话）就能通过二次验证，此前需要两个因子同时泄露；登录仍要求全部因子，所以拿不到密码+两个因子的人登不进来。
- **已绑 Passkey 的账号被全局关闭 Passkey 服务**：登录仍按绑定状态要求 Passkey，此时登录会 fail-closed；step-up 对同时绑定 TOTP 的账号靠 TOTP 通过，只绑 Passkey 的账号仍 fail-closed。运营上必须先让用户删除 Passkey 或回滚本 PR，不能只关全局开关。
- **移动端/旧浏览器不支持 WebAuthn**：只绑 Passkey 的账号无法在这些环境完成登录（策略要求）。这是策略要求的能力边界，前端给出明确降级文案。
- **证书/断言**：只存储 WebAuthn credential 与用户 handle；challenge 存 Redis 5 分钟一次性，不落日志、不入审计。
- **回滚**：回滚本 PR 即恢复「只校验 TOTP」的行为；已下发的 Passkey 不透明凭证因子记录不会被旧代码读取（按旧格式视为未知凭证，自动失效）。数据库无 schema 变更。

## 未覆盖 / 后续

- OAuth / pending-bind 等第三方登录兑换流程仍只校验 TOTP（其登录会话不携带因子状态），本 PR 未改造；如需「OAuth 登录也强制 Passkey」需另行评审。
- 无密码 Passkey 直登（discoverable credential）入口保持现状，其 2FA 门禁由另一 PR（passkey challenge/登录门禁）负责；本 PR 只做因子策略与密码登录 / step-up 编排。
- Passkey 注册/删除后的因子状态即时生效（无需重新登录），但已存在的 5 分钟凭证不会因解绑而提前失效；最坏窗口 5 分钟。
