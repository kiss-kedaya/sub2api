package middleware

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// SessionBindingContext 全局中间件：将请求的客户端 IP 与 User-Agent 注入
// request context，供 token 签发路径（登录 / 刷新 / OAuth 回调）读取并写入会话绑定，
// 同时作为审计日志、会话绑定校验的统一客户端 IP 来源。
// IP 取值与 API Key IP 限制共用转发 IP 开关：开启时只在 TCP 对端属于
// server.trusted_proxies 的前提下解析原始转发头；关闭时使用 Gin 的
// server.trusted_proxies 可信代理链。
func SessionBindingContext(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		forwardedIPSettings := cfg.ForwardedClientIPSettings()
		// 原始转发头只有在 TCP 对端属于 server.trusted_proxies 时才会被采信；
		// 直连源站的请求一律使用真实对端地址，避免伪造 CF-Connecting-IP / XFF。
		ip.SetForwardedIPSettings(c, forwardedIPSettings.TrustForwardedIP, forwardedIPSettings.Headers, cfg.Server.TrustedProxies)
		ip.SetTrustedProxyConfig(c, cfg.Server.TrustedProxies, cfg.Server.TrustedProxiesConfigured)
		userAgent := normalizePersistentText(c.Request.UserAgent(), maxPersistentUserAgentBytes)
		c.Request.Header.Set("User-Agent", userAgent)
		binding := &service.SessionBinding{
			IP:        ip.GetSecurityClientIP(c, forwardedIPSettings.TrustForwardedIP),
			UserAgent: userAgent,
		}
		c.Request = c.Request.WithContext(service.WithSessionBinding(c.Request.Context(), binding))
		c.Next()
	}
}

// requestSessionBinding 返回当前请求的会话指纹，优先取 SessionBindingContext
// 注入的解析结果（保证与 token 签发路径取值一致）；注入缺失时使用安全回退。
func requestSessionBinding(c *gin.Context) *service.SessionBinding {
	if binding := service.SessionBindingFromContext(c.Request.Context()); binding != nil {
		return binding
	}
	return &service.SessionBinding{
		IP:        ip.GetTrustedClientIP(c),
		UserAgent: normalizePersistentText(c.Request.UserAgent(), maxPersistentUserAgentBytes),
	}
}

// SecurityClientIP 返回当前请求用于安全敏感记录（审计日志等）的客户端 IP。
// 与会话绑定、API Key IP 限制共用同一套客户端 IP 来源。
func SecurityClientIP(c *gin.Context) string {
	if binding := service.SessionBindingFromContext(c.Request.Context()); binding != nil &&
		strings.TrimSpace(binding.IP) != "" {
		return binding.IP
	}
	return ip.GetTrustedClientIP(c)
}

// enforceSessionBinding 校验 access token 的会话指纹（IP/UA 绑定）。
// 指纹不匹配时一律写入审计安全事件并返回 401（前端遇到 401 会刷新令牌）；是否同时撤销该会话家族的
// 所有 refresh token 由 security.session_binding_on_mismatch 决定：
//   - rebind（默认）：不撤销。401 触发前端刷新，refresh 路径只放行「浏览器家族 + 系统家族没变、
//     只是 IP 变了」的重绑，换网段/浏览器升级不会掉线；
//   - revoke：撤销整个会话家族，必须重新登录（升级前的旧行为）。
//
// 这个校验是漂移检测器，不是防盗屏障：loose 模式下两边 IP 家族不同（IPv4 <-> IPv6）时不比较 IP，
// 只比浏览器家族 + 系统家族，而 UA 是客户端自报的——所以偷到的 access token 换个 IP 家族、
// 把 UA 写成同样的浏览器家族 + 系统家族就能通过。这种「靠忽略 IP 家族才通过」的请求单独计数
// （access_ip_family_switch）。令牌被盗要靠 refresh 轮换的重放检测发现。
//
// 返回 false 表示请求已被中断。
//
// 兼容性：claims.BindingVersion == 0（本特性上线前签发的旧 token）时放行，
// 该会话在下一次 refresh 轮转时会自动获得新版本指纹；BindingHash 为空
// （security.session_binding_mode=off）时也不校验。
func enforceSessionBinding(
	c *gin.Context,
	authService *service.AuthService,
	settingService *service.SettingService,
	auditService *service.AuditLogService,
	claims *service.JWTClaims,
) bool {
	if settingService == nil || !settingService.IsSessionBindingEnabled(c.Request.Context()) {
		return true
	}
	if claims == nil || claims.BindingHash == "" || claims.BindingVersion <= 0 {
		return true
	}
	binding := requestSessionBinding(c)
	match := settingService.SessionBindingMatch(binding, claims.BindingHash, claims.BindingCoarse)
	if match != service.BindingMismatch {
		if match == service.BindingMatchedAcrossIPFamily && authService != nil {
			authService.NoteAccessIPFamilySwitch()
		}
		return true
	}

	revoke := settingService.SessionBindingRevokesOnMismatch()
	if authService != nil {
		if revoke {
			_ = authService.RevokeSessionFamily(c.Request.Context(), claims.SessionID)
		}
		authService.NoteAccessBindingMismatch(claims.UserID, revoke)
	}
	if auditService != nil {
		uid := claims.UserID
		path := c.FullPath()
		if path == "" {
			path = c.Request.URL.Path
		}
		auditService.Record(&service.AuditLog{
			ActorUserID: &uid,
			ActorEmail:  claims.Email,
			ActorRole:   claims.Role,
			AuthMethod:  service.AuditAuthMethodJWT,
			Action:      service.AuditActionSessionBindingMismatch,
			Method:      c.Request.Method,
			Path:        path,
			ClientIP:    binding.IP,
			UserAgent:   normalizePersistentText(c.Request.UserAgent(), maxPersistentUserAgentBytes),
			StatusCode:  401,
		})
	}
	if revoke {
		AbortWithError(c, 401, "SESSION_BINDING_MISMATCH", "Session network fingerprint changed, please login again")
	} else {
		AbortWithError(c, 401, "SESSION_BINDING_MISMATCH", "Session network fingerprint changed, refresh the token to continue")
	}
	return false
}
