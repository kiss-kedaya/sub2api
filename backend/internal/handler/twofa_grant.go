package handler

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// 五分钟 2FA 凭证（two-factor grant）的 HTTP 层封装。
//
// 这里只做四件事：
//  1. 不透明 token ↔ HttpOnly Cookie 的收发（Secure/HttpOnly/SameSite=Lax/Path=/）；
//  2. 从可信代理解析出来的客户端 IP + User-Agent 构造绑定材料；
//  3. 按因子收发各自独立的 Cookie（TOTP 沿用 sub2_2fa_grant，
//     Passkey 使用 sub2_2fa_passkey_grant）；两个因子各自 5 分钟、互不覆盖；
//  4. 审计埋点（只记录 issued/used/rejected、scope、因子、剩余秒数、
//     IP/UA 指纹，绝不记录 token、动态码、断言或密码）。
//
// Cookie 里没有任何可读信息：JWT、TOTP 种子、密码、user id 都不进 Cookie；
// 响应 body 也不回显凭证。

const twoFactorGrantCookiePath = "/"

// twoFactorGrantCookieName 返回因子对应的 Cookie 名。
func twoFactorGrantCookieName(factor service.Factor) string {
	if factor == service.FactorPasskey {
		return service.TwoFactorPasskeyGrantCookieName
	}
	return service.TwoFactorGrantCookieName
}

// twoFactorGrantBinding 构造本次请求的客户端指纹。
// IP 必须走 ip.GetClientIP（内部按 trusted_proxies 解析可信代理头），
// 不能直接信任 X-Forwarded-For。
func twoFactorGrantBinding(c *gin.Context) service.TwoFactorGrantBinding {
	if c == nil {
		return service.TwoFactorGrantBinding{}
	}
	return service.TwoFactorGrantBinding{
		IP:        ip.GetClientIP(c),
		UserAgent: c.GetHeader("User-Agent"),
	}
}

// readTwoFactorGrantCookie 读取指定因子的不透明凭证。没有 Cookie 返回空串。
func readTwoFactorGrantCookie(c *gin.Context, factor service.Factor) string {
	if c == nil || c.Request == nil {
		return ""
	}
	ck, err := c.Request.Cookie(twoFactorGrantCookieName(factor))
	if err != nil || ck == nil {
		return ""
	}
	return ck.Value
}

// setTwoFactorGrantCookie 下发/刷新指定因子的凭证 Cookie。
//
// 属性固定为 HttpOnly + Secure + SameSite=Lax + Path=/ + Max-Age=剩余秒数。
// Secure 无条件打开：这是一枚可以跳过二次验证的凭证，绝不允许经由明文
// HTTP 传输；本地纯 http 调试环境下浏览器会丢弃它，退化为每次都要求验证
// （fail-closed，可接受）。
func setTwoFactorGrantCookie(c *gin.Context, factor service.Factor, grant *service.TwoFactorGrant) {
	if c == nil || grant == nil || grant.Token == "" {
		return
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     twoFactorGrantCookieName(factor),
		Value:    grant.Token,
		Path:     twoFactorGrantCookiePath,
		MaxAge:   service.TwoFactorGrantCookieMaxAge(grant.Remaining),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	c.Header("Cache-Control", "no-store")
}

// clearTwoFactorGrantCookie 清理指定因子已确定失效的凭证，避免浏览器每次都带着死 Cookie。
func clearTwoFactorGrantCookie(c *gin.Context, factor service.Factor) {
	if c == nil {
		return
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     twoFactorGrantCookieName(factor),
		Value:    "",
		Path:     twoFactorGrantCookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// clearAllTwoFactorGrantCookies 清理请求实际携带的所有因子凭证 Cookie（登出/改密/关闭 2FA 等）。
// 2FA 凭证 Cookie 的 Path 为 "/"，浏览器持有时会在请求中携带；仅对已携带的 Cookie 下发清理头，
// 避免在无 Cookie 场景下追加多余的 Set-Cookie 响应头。
func clearAllTwoFactorGrantCookies(c *gin.Context) {
	present := presentRequestCookieNames(c)
	if len(present) == 0 {
		return
	}
	if _, ok := present[twoFactorGrantCookieName(service.FactorTOTP)]; ok {
		clearTwoFactorGrantCookie(c, service.FactorTOTP)
	}
	if _, ok := present[twoFactorGrantCookieName(service.FactorPasskey)]; ok {
		clearTwoFactorGrantCookie(c, service.FactorPasskey)
	}
}

// markTwoFactorGrantAudit 写入审计附加字段。字段名都在审计中间件的白名单内，
// 且只含标量：签发/使用/拒绝、scope、因子、拒绝原因、剩余秒数、IP/UA 指纹。
func markTwoFactorGrantAudit(c *gin.Context, outcome string, grant *service.TwoFactorGrant, reason string, binding service.TwoFactorGrantBinding) {
	if c == nil {
		return
	}
	fields := map[string]any{
		"two_factor_grant": outcome,
	}
	if factor := currentGrantFactor(c); factor != "" {
		fields["two_factor_grant_factor"] = factor
	}
	ipHash, uaHash := binding.AuditFingerprints()
	if ipHash != "" {
		fields["two_factor_grant_ip_hash"] = ipHash
	}
	if uaHash != "" {
		fields["two_factor_grant_ua_hash"] = uaHash
	}
	if grant != nil {
		if grant.Scope != "" {
			fields["two_factor_grant_scope"] = grant.Scope
		}
		if grant.Factor.Valid() {
			fields["two_factor_grant_factor"] = grant.Factor.String()
		}
		if grant.Remaining > 0 {
			fields["two_factor_grant_remaining_seconds"] = int64(grant.Remaining / time.Second)
		}
	}
	if reason != "" {
		fields["two_factor_grant_reason"] = reason
	}
	servermiddleware.SetAuditExtra(c, fields)
}

const twoFactorGrantFactorCtxKey = "two_factor_grant_factor"

// markRequestFactor 把本次请求正在验证的因子写进上下文，供审计使用。
func markRequestFactor(c *gin.Context, factor service.Factor) {
	if c == nil || !factor.Valid() {
		return
	}
	c.Set(twoFactorGrantFactorCtxKey, factor.String())
	servermiddleware.SetAuditExtra(c, map[string]any{"two_factor_grant_factor": factor.String()})
}

func currentGrantFactor(c *gin.Context) string {
	if c == nil {
		return ""
	}
	value, _ := c.Get(twoFactorGrantFactorCtxKey)
	factor, _ := value.(string)
	return factor
}

// issueTwoFactorGrant 在某个因子验证成功后签发该因子的凭证并写入对应 Cookie。
//
// 签发失败不能把一次已经成功的登录/敏感操作变成失败：只记录日志并继续，
// 浏览器没有合法凭证时下一次会照常要求该因子验证（fail-closed）。
func issueTwoFactorGrant(c *gin.Context, totpService *service.TotpService, userID int64, scope string, factor service.Factor) {
	if c == nil || totpService == nil || userID <= 0 || !factor.Valid() {
		return
	}
	markRequestFactor(c, factor)
	binding := twoFactorGrantBinding(c)
	grant, err := totpService.IssueTwoFactorGrant(c.Request.Context(), userID, scope, factor, binding)
	if err != nil {
		slog.Warn("issue two-factor grant failed",
			"user_id", userID,
			"scope", scope,
			"factor", factor.String(),
			"reason", service.TwoFactorGrantRejectReason(err))
		markTwoFactorGrantAudit(c, "rejected", nil, service.TwoFactorGrantRejectReason(err), binding)
		return
	}
	setTwoFactorGrantCookie(c, factor, grant)
	markTwoFactorGrantAudit(c, "issued", grant, "", binding)
}

// consumeTwoFactorGrant 校验指定因子 Cookie 里的凭证，成功时按需重签并刷新 Cookie。
// 返回 nil 表示该因子没有可用的 2FA 凭证（调用方必须按未验证处理）。
func consumeTwoFactorGrant(c *gin.Context, totpService *service.TotpService, userID int64, factor service.Factor) *service.TwoFactorGrant {
	if c == nil || totpService == nil || userID <= 0 || !factor.Valid() {
		return nil
	}
	markRequestFactor(c, factor)
	token := readTwoFactorGrantCookie(c, factor)
	if token == "" {
		// 没带凭证是正常状态（首次挑战/浏览器不支持 Cookie），不算「拒绝」。
		return nil
	}
	binding := twoFactorGrantBinding(c)
	grant, err := totpService.ConsumeTwoFactorGrant(c.Request.Context(), token, userID, factor, binding)
	if err != nil {
		if service.IsTwoFactorGrantExpiredOrMismatched(err) {
			clearTwoFactorGrantCookie(c, factor)
		}
		markTwoFactorGrantAudit(c, "rejected", nil, service.TwoFactorGrantRejectReason(err), binding)
		return nil
	}
	// 重签后的新 token（或原 token，用于把 Cookie 的 Max-Age 对齐剩余时间）。
	setTwoFactorGrantCookie(c, factor, grant)
	markTwoFactorGrantAudit(c, "used", grant, "", binding)
	return grant
}

// consumeAllTwoFactorGrants 返回「本次请求前已经通过 5 分钟凭证满足的因子」。
// 任一因子存储故障都 fail-closed：调用方必须把它当成未满足。
func consumeAllTwoFactorGrants(
	c *gin.Context,
	totpService *service.TotpService,
	userID int64,
	factors []service.Factor,
) []service.Factor {
	satisfied := make([]service.Factor, 0, len(factors))
	for _, factor := range factors {
		if consumeTwoFactorGrant(c, totpService, userID, factor) != nil {
			satisfied = append(satisfied, factor)
		}
	}
	return satisfied
}

// requirePasswordVerificationGrant 是「校验当前密码」类入口的 2FA 前置门控。
//
// 策略（与 step-up 中间件同一条「任选其一」规则，见 service.StepUpSatisfiedFactors）：
// 账号绑定了 TOTP / Passkey 中的哪些因子，持有其中任一因子有效的 5 分钟凭证即可；
// 一个有效凭证都没有（含存储故障、过期、未绑定因子的凭证）都返回 403 STEP_UP_REQUIRED
// （fail-closed），前端复用全局 step-up 对话框完成 /user/totp/step-up 或 Passkey
// step-up 之一后重试。登录（auth_handler）不走这里，仍要求全部已绑定因子。
//
// 一个因子都没绑定的账号保持原有行为（不额外要求）。
func requirePasswordVerificationGrant(c *gin.Context, totpService *service.TotpService, user *service.User) bool {
	if c == nil || user == nil || totpService == nil {
		return true
	}
	factors, err := totpService.RequiredFactorsForUser(c.Request.Context(), user)
	if err != nil {
		response.ErrorFrom(c, service.ErrTwoFactorPolicyUnavailable)
		return false
	}
	if len(factors) == 0 {
		return true
	}
	c.Header("Cache-Control", "no-store")
	satisfied := service.StepUpSatisfiedFactors(factors, consumeAllTwoFactorGrants(c, totpService, user.ID, factors))
	if len(satisfied) > 0 {
		// 审计里记录是哪个因子的凭证满足了这次校验（标量因子名，不含凭证材料）。
		servermiddleware.SetAuditExtra(c, map[string]any{
			"two_factor_satisfied_factors": strings.Join(service.FactorStrings(satisfied), ","),
		})
		return true
	}
	response.ErrorFrom(c, service.ErrPasswordTwoFactorRequired.WithMetadata(service.StepUpChallengeMetadata(factors)))
	return false
}
