package handler

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// 五分钟 2FA 凭证（two-factor grant）的 HTTP 层封装。
//
// 这里只做三件事：
//  1. 不透明 token ↔ HttpOnly Cookie 的收发（Secure/HttpOnly/SameSite=Lax/Path=/）；
//  2. 从可信代理解析出来的客户端 IP + User-Agent 构造绑定材料；
//  3. 审计埋点（只记录 issued/used/rejected、scope、剩余秒数、IP/UA 指纹，
//     绝不记录 token、动态码或密码）。
//
// Cookie 里没有任何可读信息：JWT、TOTP 种子、密码、user id 都不进 Cookie；
// 响应 body 也不回显凭证。

const twoFactorGrantCookiePath = "/"

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

// readTwoFactorGrantCookie 读取浏览器带来的不透明凭证。没有 Cookie 返回空串。
func readTwoFactorGrantCookie(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	ck, err := c.Request.Cookie(service.TwoFactorGrantCookieName)
	if err != nil || ck == nil {
		return ""
	}
	return ck.Value
}

// setTwoFactorGrantCookie 下发/刷新凭证 Cookie。
//
// 属性固定为 HttpOnly + Secure + SameSite=Lax + Path=/ + Max-Age=剩余秒数。
// Secure 无条件打开：这是一枚可以跳过二次验证的凭证，绝不允许经由明文
// HTTP 传输；本地纯 http 调试环境下浏览器会丢弃它，退化为每次都要求动态码
// （fail-closed，可接受）。
func setTwoFactorGrantCookie(c *gin.Context, grant *service.TwoFactorGrant) {
	if c == nil || grant == nil || grant.Token == "" {
		return
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     service.TwoFactorGrantCookieName,
		Value:    grant.Token,
		Path:     twoFactorGrantCookiePath,
		MaxAge:   service.TwoFactorGrantCookieMaxAge(grant.Remaining),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	c.Header("Cache-Control", "no-store")
}

// clearTwoFactorGrantCookie 清理确定失效的凭证，避免浏览器每次都带着死 Cookie。
func clearTwoFactorGrantCookie(c *gin.Context) {
	if c == nil {
		return
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     service.TwoFactorGrantCookieName,
		Value:    "",
		Path:     twoFactorGrantCookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// markTwoFactorGrantAudit 写入审计附加字段。字段名都在审计中间件的白名单内，
// 且只含标量：签发/使用/拒绝、scope、拒绝原因、剩余秒数、IP/UA 指纹。
func markTwoFactorGrantAudit(c *gin.Context, outcome string, grant *service.TwoFactorGrant, reason string, binding service.TwoFactorGrantBinding) {
	if c == nil {
		return
	}
	fields := map[string]any{
		"two_factor_grant": outcome,
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
		if grant.Remaining > 0 {
			fields["two_factor_grant_remaining_seconds"] = int64(grant.Remaining / time.Second)
		}
	}
	if reason != "" {
		fields["two_factor_grant_reason"] = reason
	}
	servermiddleware.SetAuditExtra(c, fields)
}

// issueTwoFactorGrant 在动态码校验成功后签发凭证并写入 Cookie。
//
// 签发失败不能把一次已经成功的登录/敏感操作变成失败：只记录日志并继续，
// 浏览器没有合法凭证时下一次会照常要求动态码（fail-closed）。
func issueTwoFactorGrant(c *gin.Context, totpService *service.TotpService, userID int64, scope string) {
	if c == nil || totpService == nil || userID <= 0 {
		return
	}
	binding := twoFactorGrantBinding(c)
	grant, err := totpService.IssueTwoFactorGrant(c.Request.Context(), userID, scope, binding)
	if err != nil {
		slog.Warn("issue two-factor grant failed",
			"user_id", userID,
			"scope", scope,
			"reason", service.TwoFactorGrantRejectReason(err))
		markTwoFactorGrantAudit(c, "rejected", nil, service.TwoFactorGrantRejectReason(err), binding)
		return
	}
	setTwoFactorGrantCookie(c, grant)
	markTwoFactorGrantAudit(c, "issued", grant, "", binding)
}

// consumeTwoFactorGrant 校验 Cookie 里的凭证，成功时按需重签并刷新 Cookie。
// 返回 nil 表示没有可用的 2FA 凭证（调用方必须按未验证处理）。
func consumeTwoFactorGrant(c *gin.Context, totpService *service.TotpService, userID int64) *service.TwoFactorGrant {
	if c == nil || totpService == nil || userID <= 0 {
		return nil
	}
	token := readTwoFactorGrantCookie(c)
	if token == "" {
		// 没带凭证是正常状态（首次挑战/浏览器不支持 Cookie），不算「拒绝」。
		return nil
	}
	binding := twoFactorGrantBinding(c)
	grant, err := totpService.ConsumeTwoFactorGrant(c.Request.Context(), token, userID, binding)
	if err != nil {
		if service.IsTwoFactorGrantExpiredOrMismatched(err) {
			clearTwoFactorGrantCookie(c)
		}
		markTwoFactorGrantAudit(c, "rejected", nil, service.TwoFactorGrantRejectReason(err), binding)
		return nil
	}
	// 重签后的新 token（或原 token，用于把 Cookie 的 Max-Age 对齐剩余时间）。
	setTwoFactorGrantCookie(c, grant)
	markTwoFactorGrantAudit(c, "used", grant, "", binding)
	return grant
}

// requirePasswordVerificationGrant 是「校验当前密码」类入口的 2FA 前置门控。
//
// 账号未启用 TOTP 时保持原有行为（不额外要求）；启用 TOTP 时：
//   - Cookie 里有有效凭证 → 放行（并按设计重签一次凭证）；
//   - 没有/失效 → 403 STEP_UP_REQUIRED，前端复用全局 TOTP 对话框完成
//     /user/totp/step-up（该接口成功后同样会下发凭证 Cookie），然后重试本请求。
func requirePasswordVerificationGrant(c *gin.Context, totpService *service.TotpService, user *service.User) bool {
	if c == nil || user == nil || totpService == nil || !user.TotpEnabled {
		return true
	}
	c.Header("Cache-Control", "no-store")
	if consumeTwoFactorGrant(c, totpService, user.ID) != nil {
		return true
	}
	response.ErrorFrom(c, service.ErrPasswordTwoFactorRequired)
	return false
}
