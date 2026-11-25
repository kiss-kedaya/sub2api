package handler

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"log/slog"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// 登录首步「凭据无效」挑战的稳定伪装（审计 A-P1-02）：handler 侧。
// 设计与取舍见 service/login_shape_disguise.go；开关 SettingKeyLoginShapeDisguiseEnabled，默认关闭。
//
// 开关关闭时本文件里「伪装」相关的函数一个都不会被调用：Login 走原来的 createLoginChallenge，
// Login2FA / Login2FAPasskeyFinish 只会遇到旧的（Decoy=false）诱饵会话。
// 唯一会在真实会话路径上被调用的是 loginShapeDisguiseActive / realSessionIPBucket，开关关闭时它们
// 返回 false / 0，调用方据此跳过额外的限速，响应与旧实现逐字节一致。

// loginShapeDisguiseActive 报告本次登录失败是否走伪装挑战：开关打开且有可用的伪装密钥。
func (h *AuthHandler) loginShapeDisguiseActive(ctx context.Context) bool {
	return h != nil && h.disguise != nil && h.totpService != nil && h.settingSvc != nil &&
		h.settingSvc.LoginShapeDisguiseEnabled(ctx)
}

// 限速桶的取舍（审计 AUD-6）。
//
// 伪装会话（密码错）与真实会话（密码对）必须消耗「同一个桶」，否则攻击者可以先用必错的密码把伪装桶打满，
// 再对候选密码走一遍首步 + 第二步：落在被打满的桶里 → 429（密码错），落在新鲜的桶里 → 401 / 清除头（密码对），
// 密码是否正确就从限速反应里读出来了。
//
// 但共用桶不能直接共用真实账号按用户 ID 计的账号桶：那等于让任何人不需要密码，就能靠伪装会话把别人的登录第二步
// 锁死。所以第二步用「账号 + 客户端 IP」的桶（loginStepTwoIPBucketKey）：
//   - 伪装会话（镜像账号或邮箱散列）只消耗这个桶，不碰账号桶；
//   - 真实会话先过这个桶，再照旧消耗账号桶（账号级的爆破限制一点没弱化）；
//
// 攻击者只能把「自己这个 IP」的桶打满，换个 IP 就和真实用户互不影响。首步的 5 分钟凭证校验限速本来就是
// 「用户 ID + IP」的桶，伪装只需要改用同一个用户 ID。残余风险见 PR 描述。

// disguiseAccountKey 返回伪装挑战在限速里「扮演」的账号键：镜像真实账号时就是它的真实用户 ID（与真实会话同键），
// 否则是邮箱散列键（>= 2^40，不会与真实用户 ID 相撞）。
func disguiseAccountKey(email string, mirror *service.User) int64 {
	if mirror != nil && mirror.ID > 0 {
		return mirror.ID
	}
	return loginChallengeRateLimitKey(email)
}

// decoySessionAccountKey 是第二步里伪装会话扮演的账号键，与首步 disguiseAccountKey 的结果一致。
func decoySessionAccountKey(session *service.TotpLoginSession) int64 {
	if session.DecoyAccountID > 0 {
		return session.DecoyAccountID
	}
	return loginChallengeRateLimitKey(session.Email)
}

// loginStepTwoIPBucketKey 把（账号键, 客户端 IP）压成第二步「账号 + IP」限速桶的键。取 SHA-256 的前 62 位并强制
// >= 2^40：不会与自增的真实用户 ID 相撞，也不需要改限速器的接口（它只认一个数字键）。
func loginStepTwoIPBucketKey(accountKey int64, clientIP string) int64 {
	sum := sha256.Sum256([]byte("login-2fa-ip-bucket\x00" + strconv.FormatInt(accountKey, 10) + "\x00" + clientIP))
	key := int64(binary.BigEndian.Uint64(sum[:8]) & 0x3fffffffffffffff)
	const minBucketKey = int64(1) << 40
	if key < minBucketKey {
		key |= minBucketKey
	}
	return key
}

// realSessionIPBucket 返回真实会话（UserID > 0）在第二步要多消耗的「账号 + IP」桶的键；开关关闭返回 0（不多消耗）。
func (h *AuthHandler) realSessionIPBucket(c *gin.Context, userID int64) int64 {
	if userID <= 0 || !h.loginShapeDisguiseActive(c.Request.Context()) {
		return 0
	}
	return loginStepTwoIPBucketKey(userID, ip.GetClientIP(c))
}

// disguiseFactors 决定伪装挑战对外宣称的因子形状，并在能镜像时返回真实账号（用于镜像它的 Passkey 凭据）。
//
//   - 邮箱对应已启用且至少有一个登录因子的真实账号：镜像它真实的形状——「密码错」和「密码对」
//     的响应形状因此一致，攻击者无法靠形状变化确认密码正确；
//   - 否则（账号不存在 / 未启用 / 没有 2FA，或读取失败）：按 HMAC 稳定取一个形状。
//
// 真实形状里单 Passkey 形状需要 Passkey 服务才能给出 assertion 选项，服务不可用时无法镜像，
// 退回稳定伪装（这种部署里真实 Passkey 账号本来就登录不了，见 respondLoginFactorChallenge）。
func (h *AuthHandler) disguiseFactors(ctx context.Context, email string, passkeyReady bool) ([]service.Factor, *service.User) {
	user, factors, err := h.totpService.LoginFactorsForEmail(ctx, email)
	if err != nil {
		slog.Warn("login disguise: load real factor shape failed, using stable decoy shape", "error", err)
	} else if len(factors) > 0 && (passkeyReady || factors[0] != service.FactorPasskey) {
		return factors, user
	}
	return h.disguise.Shape(email, passkeyReady), nil
}

// mimicTwoFactorGrantChecks 对伪装挑战宣称的每个因子，做真实登录路径在「密码正确」时对 5 分钟因子凭证
// Cookie 做的同一件事：带了该因子的凭证 Cookie 就校验它，校验不过（未知 / 过期 / 用户或指纹不匹配）就
// 在响应里清掉这个 Cookie，超出「每用户 + IP 每分钟 10 次」的校验限速后不再清。
//
// 这一步必须有：攻击者可以在首步请求里随便带一个假的凭证 Cookie（Cookie 名是公开常量），
// 「响应里有没有 Set-Cookie 清除头」就成了「密码是否正确 + 账号要求哪些因子」的信号，
// 绕过响应体上所有的形状伪装。
//
// accountKey 用 disguiseAccountKey 的结果：镜像真实账号时就是它的真实用户 ID，伪装路径因此与真实路径落在
// 同一个「用户 ID + IP」校验桶里——攻击者先用必错密码把桶打满，再试候选密码，两条路径一起被限速，不再有
// 「错密码没有清除头、对密码仍有清除头」的差别（审计 AUD-6）。桶本来就按 IP 分，只能打满攻击者自己 IP 的桶。
// 没有可镜像账号时用邮箱散列键（>= 2^40，不会与任何真实用户 ID 相撞），永远校验不过。
// 真实用户带着自己有效的凭证输错密码时，凭证会被照常校验并续签（而不是被清掉）；它不会放行任何登录：
// 返回值不参与任何判断，伪装会话永远不会通过第二步。
func (h *AuthHandler) mimicTwoFactorGrantChecks(c *gin.Context, accountKey int64, factors []service.Factor) {
	for _, factor := range factors {
		_ = consumeTwoFactorGrant(c, h.totpService, accountKey, factor)
	}
}

// respondDisguisedLoginChallenge 下发「凭据无效」时的伪装挑战：结构与 respondLoginFactorChallenge
// 的真实挑战完全一致（同一个响应结构、同样的邮箱掩码、同样的 temp_token 形态、同样的 Passkey 选项结构），
// 只有随机值不同。会话是 Decoy：UserID 恒为 0，永远不会放行。
func (h *AuthHandler) respondDisguisedLoginChallenge(c *gin.Context, email, password string) {
	ctx := c.Request.Context()
	passkeyReady := h.passkeys != nil && h.passkeys.Enabled()
	factors, mirror := h.disguiseFactors(ctx, email, passkeyReady)
	h.mimicTwoFactorGrantChecks(c, disguiseAccountKey(email, mirror), factors)

	// 伪装 ceremony 的用户 ID 段同时记进伪装登录会话（对应真实会话里的 UserID），第二步据此判断 ceremony 是否属于它。
	var scope int64
	if passkeyReady {
		scope = h.passkeys.DisguiseScope(ctx, h.disguise, email, mirror)
	}
	// 镜像的真实账号 ID 也记进会话：第二步用它选限速桶，让伪装会话与真实会话落在同一个桶里。
	var mirrorID int64
	if mirror != nil {
		mirrorID = mirror.ID
	}
	tempToken, err := h.totpService.CreateDecoyLoginSession(ctx, email, factors, h.disguise.PasswordProbe(email, password), scope, mirrorID)
	if err != nil {
		slog.Warn("create login challenge session failed", "error", err)
		response.InternalError(c, "Failed to create 2FA session")
		return
	}
	resp := TotpLoginResponse{
		Requires2FA:     true,
		TempToken:       tempToken,
		UserEmailMasked: service.MaskEmail(email),
		RequiredFactors: service.FactorStrings(factors),
		NextFactor:      factors[0].String(),
	}
	if factors[0] == service.FactorPasskey {
		assertion, sessionToken, err := h.passkeys.BeginDisguisedLogin(ctx, h.disguise, email, mirror)
		if err != nil {
			slog.Warn("begin disguised passkey assertion failed", "error", err)
			response.ErrorFrom(c, err)
			return
		}
		resp.PasskeySessionToken = sessionToken
		resp.PasskeyOptions = assertion
	}
	response.Success(c, resp)
}

// respondDecoyLogin2FA 是 Login2FA 对伪装挑战会话的处理：与「真实会话、攻击者没有第二因子」
// 逐步一致，只是校验步骤永远失败。顺序与真实路径相同：
//
//  1. 请求里的邮箱与会话邮箱不一致 → 立刻 INVALID_CREDENTIALS（不耗额度）；
//  2. 提交了密码 → 跑一次等成本的 bcrypt（真实路径在这里重新校验密码）；密码与第一步不一致 →
//     INVALID_CREDENTIALS 且不耗额度（对应真实会话里「第二步密码错」）；一致才继续；
//  3. 「账号 + 客户端 IP」限速（与真实会话共用同一个桶，镜像账号用它的真实用户 ID，其余用邮箱散列；
//     超限 429 + Retry-After；只消耗这个桶，不碰真实账号按用户 ID 计的账号桶，见上面「限速桶的取舍」）；
//  4. 校验失败：会话要求 TOTP → INVALID_CREDENTIALS；单 Passkey 会话 → TOTP_NOT_SETUP
//     （真实的单 Passkey 会话在这里也是这个错误）。
//
// 会话不因失败而销毁：真实会话输错动态码后同样保留到 TTL 过期，旧诱饵「失败一次就删会话」
// 的行为本身就是一个可区分的信号。
func (h *AuthHandler) respondDecoyLogin2FA(c *gin.Context, session *service.TotpLoginSession, req *Login2FARequest) {
	if req.Password != "" {
		if reqEmail := strings.TrimSpace(req.Email); reqEmail != "" &&
			!strings.EqualFold(reqEmail, strings.TrimSpace(session.Email)) {
			slog.Warn("login_2fa_email_mismatch", "user_id", session.UserID)
			markAuthAuditFailureFromError(c, service.ErrInvalidCredentials)
			response.ErrorFrom(c, service.ErrInvalidCredentials)
			return
		}
		h.authService.EqualizePasswordTiming(req.Password)
		if !h.disguise.PasswordProbeMatches(session.Email, req.Password, session.DecoyPasswordProbe) {
			respondLogin2FACredentialOrCodeFailure(c, service.ErrInvalidCredentials)
			return
		}
	}

	if _, ok := checkTwoFactorRateLimit(c, h.twoFactorLimiter, loginStepTwoIPBucketKey(decoySessionAccountKey(session), ip.GetClientIP(c))); !ok {
		return
	}
	if service.HasFactor(session.RequiredFactors, service.FactorTOTP) {
		respondLogin2FACredentialOrCodeFailure(c, service.ErrInvalidCredentials)
		return
	}
	response.ErrorFrom(c, service.ErrTotpNotSetup)
}

// respondDecoyPasskeyFinish 是 Login2FAPasskeyFinish 对伪装挑战会话的处理，同样与真实会话逐步一致：
// 没有要求 Passkey 的会话 → 400「No passkey step...」；限速；然后消费诱饵 ceremony 并用同一个库校验
// assertion，错误码与真实会话里「assertion 校验失败」相同。永远返回错误。
func (h *AuthHandler) respondDecoyPasskeyFinish(c *gin.Context, session *service.TotpLoginSession, req *Login2FAPasskeyFinishRequest) {
	if !service.HasFactor(session.RequiredFactors, service.FactorPasskey) {
		response.BadRequest(c, "No passkey step is required for this login session")
		return
	}
	attemptCtx, ok := checkTwoFactorRateLimit(c, h.twoFactorLimiter, loginStepTwoIPBucketKey(decoySessionAccountKey(session), ip.GetClientIP(c)))
	if !ok {
		return
	}
	credentialRequest := cloneRequestWithJSON(c.Request, req.Credential)
	err := h.passkeys.FinishDisguisedLogin(attemptCtx, req.SessionToken, credentialRequest, session.DecoyScope)
	if err == nil {
		// 不可能：诱饵 ceremony 永远不放行。
		err = service.ErrPasskeyVerify
	}
	response.ErrorFrom(c, err)
}
