# 双因子因子策略（TOTP + Passkey）

状态：已实现（本仓库 `feat/passkey-2fa-policy`）
适用版本：2.3.153 之后

## 策略（老板口径，代码唯一入口：`service.TotpService.RequiredFactors*`）

| 账号已绑定 | 密码登录 | 管理面风险操作（step-up） |
| --- | --- | --- |
| 仅 TOTP | 密码 + TOTP（现状不变） | TOTP step-up 5 分钟凭证（现状不变） |
| 仅 Passkey | 密码 + Passkey assertion | Passkey step-up 5 分钟凭证 |
| TOTP + Passkey | 密码 + TOTP + Passkey（顺序固定先 TOTP 后 Passkey） | TOTP 与 Passkey 两个因子都必须各有有效 step-up 凭证 |
| 都没有 | 密码（现状不变） | 403 拒绝（`STEP_UP_TOTP_NOT_ENABLED`，提示启用 TOTP 或注册 Passkey） |

补充规则：

- **判定依据**只有两条：`users.totp_enabled = true`；`passkey_credentials` 存在该用户记录。全局功能开关只影响「能否注册/验证」，不参与「是否必须验证」的判定。
- **5 分钟窗口**：每个因子各自独立签发 5 分钟凭证（TOTP / Passkey 各一枚），同一因子 5 分钟内免重复验证；凭证过期、绑定不匹配、Redis 故障一律回到验证挑战。
- **fail-closed**：任何一步失败（策略读取失败、凭证存储故障、Passkey 服务不可用、缺少任一因子）都不签发令牌、不放行风险操作；不存在用另一个因子降级通过的路径。
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
- 管理面中间件对 `RequiredFactors` **逐个**校验；缺任一因子返回 `403 STEP_UP_REQUIRED`，全部通过时回传 `X-StepUp-Skipped: 1`、`X-StepUp-Remaining`（取最早过期的因子）与 `X-StepUp-Factors: totp,passkey`。

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
- step-up 弹窗（全局 gate）：打开时读取 `GET /api/v1/user/2fa/policy`，按策略先 TOTP 后 Passkey；两个因子都通过才 `onVerified()` 并重试原请求。Passkey 不可用时展示降级文案并只能取消（不会让风险操作放行）。
- i18n：`zh` / `en` 同步。

## 测试矩阵

| 场景 | 登录 | step-up 门控 | 覆盖位置 |
| --- | --- | --- | --- |
| 只绑 TOTP | 密码+TOTP（现状） | TOTP 凭证通过 | `service/two_factor_policy_test.go`、`middleware/step_up_factor_matrix_test.go` |
| 只绑 Passkey | 密码+Passkey | 缺 Passkey 凭证 403；有则通过 | 同上 + `handler/login_factor_policy_test.go` |
| 两者都绑 | TOTP 后继续 Passkey，缺一不发令牌 | 缺任一因子 403；两枚凭证才通过 | 同上 |
| 都没绑 | 密码直登 | 403 `STEP_UP_TOTP_NOT_ENABLED` | 同上 |
| 5 分钟免重复 | 因子凭证命中直接登录 | 窗口内跳过并回传 headers | `service/twofa_grant_factor_test.go`、`repository/totp_stepup_factor_test.go` |
| 过期重验 | 凭证过期回到挑战 | 窗口过期 403 | 同上 + middleware 矩阵 |
| fail-closed | 策略/存储故障不签发 | 策略/存储故障 503 | 同上 |
| 跨因子复用 | TOTP 凭证不能跳过 Passkey | — | `service/twofa_grant_factor_test.go`、`handler/login_factor_policy_test.go` |

前端：`LoginView.dualFactor.spec.ts`（Passkey-only / TOTP→Passkey / 不支持降级 / TOTP-only）、`TotpStepUpDialog.dualFactor.spec.ts`（双因子顺序 / Passkey-only / 降级 / 策略失败 TOTP 兜底）。

## 风险与回滚

- **Redis key 结构变化**：step-up 授权 key 由 `totp:stepup:<uid>:<session>` 变为 `totp:stepup:<uid>:<factor>:<session>`。升级后旧 key 不再命中，用户最多重新验证一次（5 分钟窗口），不涉及数据迁移；回滚会同样失效新 key，行为对称。
- **已绑 Passkey 的账号被全局关闭 Passkey 服务**：策略按绑定状态要求 Passkey，此时登录/风险操作会 fail-closed。运营上必须先让用户删除 Passkey 或回滚本 PR，不能只关全局开关。
- **移动端/旧浏览器不支持 WebAuthn**：只绑 Passkey 的账号无法在这些环境完成登录（策略要求）。这是策略要求的能力边界，前端给出明确降级文案。
- **证书/断言**：只存储 WebAuthn credential 与用户 handle；challenge 存 Redis 5 分钟一次性，不落日志、不入审计。
- **回滚**：回滚本 PR 即恢复「只校验 TOTP」的行为；已下发的 Passkey 不透明凭证因子记录不会被旧代码读取（按旧格式视为未知凭证，自动失效）。数据库无 schema 变更。

## 未覆盖 / 后续

- OAuth / pending-bind 等第三方登录兑换流程仍只校验 TOTP（其登录会话不携带因子状态），本 PR 未改造；如需「OAuth 登录也强制 Passkey」需另行评审。
- 无密码 Passkey 直登（discoverable credential）入口保持现状，其 2FA 门禁由另一 PR（passkey challenge/登录门禁）负责；本 PR 只做因子策略与密码登录 / step-up 编排。
- Passkey 注册/删除后的因子状态即时生效（无需重新登录），但已存在的 5 分钟凭证不会因解绑而提前失效；最坏窗口 5 分钟。
