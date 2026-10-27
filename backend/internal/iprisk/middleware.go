package iprisk

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	ippkg "github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

const managedKey = "sub2api.ip_risk_managed"

// Managed lets the legacy login-ban guard retain signup accounting while the
// configurable guard owns IP bans. Observation keeps the existing guard active
// for clients outside the whitelist.
func Managed(c *gin.Context) bool { return c.GetBool(managedKey) }

func authCategory(method, path string) string {
	if !strings.HasPrefix(path, "/api/v1/auth/") {
		return ""
	}
	if strings.Contains(path, "/oauth/") && strings.Contains(path, "/payment/") {
		return ""
	}
	if method == http.MethodPost {
		switch {
		case strings.HasSuffix(path, "/login"), strings.HasSuffix(path, "/login/2fa"), strings.HasSuffix(path, "/bind-login"), strings.HasSuffix(path, "/passkey/login/begin"), strings.HasSuffix(path, "/passkey/login/finish"):
			return "login"
		case strings.HasSuffix(path, "/register"), strings.HasSuffix(path, "/create-account"), strings.HasSuffix(path, "/complete-registration"):
			return "register"
		case strings.HasSuffix(path, "/send-verify-code"), strings.HasSuffix(path, "/forgot-password"), strings.HasSuffix(path, "/reset-password"):
			return "verification"
		}
	}
	if strings.Contains(path, "/oauth/") {
		if method == http.MethodGet && strings.HasSuffix(path, "/callback") {
			return "register"
		}
		if strings.HasSuffix(path, "/start") || strings.HasSuffix(path, "/exchange") {
			return "verification"
		}
	}
	return ""
}

func credentialFailure(c *gin.Context) bool {
	if c.Writer.Status() < 400 || c.Writer.Status() >= 500 || c.Writer.Status() == 429 {
		return false
	}
	switch response.ErrorReason(c) {
	case "INVALID_CREDENTIALS", "TOTP_INVALID_CODE", "PASSKEY_VERIFICATION_FAILED":
		return true
	default:
		return false
	}
}

// Machine callbacks retain their existing signatures / capability / worker tokens.
// Match registered routes and methods exactly, never arbitrary URL prefixes.
func machineCallback(method, path string) bool {
	if path == "/api/bps-images/:token" {
		return method == http.MethodGet || method == http.MethodHead
	}
	if path == "/api/v1/payment/webhook/easypay" {
		return method == http.MethodGet || method == http.MethodPost
	}
	if method != http.MethodPost {
		return false
	}
	switch path {
	case "/api/v1/payment/webhook/alipay", "/api/v1/payment/webhook/wxpay",
		"/api/v1/payment/webhook/stripe", "/api/v1/payment/webhook/airwallex",
		"/api/v1/internal/openai-reauth/claim", "/api/v1/internal/openai-reauth/session-export/claim",
		"/api/v1/internal/openai-reauth/totp-claim", "/api/v1/internal/openai-reauth/runtime-settings",
		"/api/v1/internal/openai-reauth/:task_id/session-export/progress",
		"/api/v1/internal/openai-reauth/:task_id/session-export/result",
		"/api/v1/internal/openai-reauth/:task_id/session-export/fail",
		"/api/v1/internal/openai-reauth/:task_id/totp-phase", "/api/v1/internal/openai-reauth/:task_id/totp-finish",
		"/api/v1/internal/openai-reauth/:task_id/totp-recover", "/api/v1/internal/openai-reauth/:task_id/progress",
		"/api/v1/internal/openai-reauth/:task_id/callback", "/api/v1/internal/openai-reauth/:task_id/credentials",
		"/api/v1/internal/openai-reauth/:task_id/fail":
		return true
	}
	return false
}

func (m *Manager) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(anonymousManagerKey, m)
		if m.emergencyDisabled || (c.Request.Method == http.MethodGet && c.FullPath() == "/health") || machineCallback(c.Request.Method, c.FullPath()) {
			c.Next()
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()
		p, err := m.snapshot(ctx)
		if err != nil {
			reject(c, 503, 0)
			return
		}
		if !p.config.Enabled {
			c.Next()
			return
		}
		cfg := p.config
		category := authCategory(c.Request.Method, c.FullPath())
		c.Set(managedKey, !cfg.ObserveOnly)
		address, parseErr := netip.ParseAddr(ippkg.GetTrustedClientIP(c))
		address = address.Unmap()
		peer, _, _ := net.SplitHostPort(c.Request.RemoteAddr)
		source := "trusted_proxy_chain"
		if peerIP, err := netip.ParseAddr(peer); err == nil && peerIP.Unmap() == address {
			source = "direct_peer"
		}
		if _, exists := c.Get("sub2api.verified_forwarded_ip"); exists {
			source = "verified_serverless"
		}
		path := c.FullPath()
		if path == "" {
			path = "<static-or-unmatched>"
		}
		event := Event{At: time.Now().Unix(), IP: address.String(), Peer: cleanText(peer, 128), IPSource: source,
			Method: cleanText(c.Request.Method, 16), Category: category, Path: cleanText(path, 256), UserAgent: cleanText(c.Request.UserAgent(), 1024), Decision: "allowed"}
		defer func() {
			if (category == "" && event.Reason == "") || event.Reason == "risk_store_unavailable" {
				return
			}
			event.Status = c.Writer.Status()
			event.ErrorReason = cleanText(response.ErrorReason(c), 128)
			recordCtx, recordCancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 500*time.Millisecond)
			defer recordCancel()
			m.record(recordCtx, cfg, event)
		}()
		if parseErr != nil || address.Zone() != "" {
			event.Reason = "invalid_client_ip"
			if !cfg.ObserveOnly {
				event.Decision = "blocked"
				reject(c, 403, 0)
				return
			}
			event.Decision = "would_block"
			c.Next()
			return
		}
		event.Network = riskNetwork(address, cfg.IPv6Prefix)
		if p.allowed(address) {
			c.Set(managedKey, true)
			event.Decision = "whitelisted"
			c.Next()
			return
		}
		reason, retry, err := m.assess(ctx, p, address, category, c.Request.UserAgent(), &event)
		if err != nil {
			event.Reason = "risk_store_unavailable"
			event.Decision = "unavailable"
		} else if reason != "" {
			event.Reason = reason
			if !cfg.ObserveOnly {
				event.Decision = "blocked"
				status := 403
				if retry > 0 {
					status = 429
				}
				reject(c, status, retry)
				return
			}
			event.Decision = "would_block"
		}
		c.Next()
		// Never count capacity failures or service errors as bad credentials.
		failureReason := ""
		var threshold Threshold
		if category == "login" && credentialFailure(c) {
			failureReason = "login_failures"
			threshold = cfg.LoginFailures
		}
		if category == "register" && c.Writer.Status() >= 400 && c.Writer.Status() < 500 && c.Writer.Status() != 429 {
			failureReason = "register_failures"
			threshold = cfg.RegisterFailures
		}
		if failureReason != "" {
			failureCtx, failureCancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 500*time.Millisecond)
			defer failureCancel()
			ban, failureErr := m.count(failureCtx, cfg, event.Network, failureReason, threshold, true)
			if failureErr != nil && event.Reason == "" {
				event.Reason = "risk_store_unavailable"
			}
			if ban != nil && event.Reason == "" {
				event.Reason = ban.Reason
				event.Decision = "auto_banned"
				if cfg.ObserveOnly {
					event.Decision = "would_ban"
				}
			}
		}
	}
}

func reject(c *gin.Context, status int, retrySeconds int64) {
	c.Header("Cache-Control", "no-store")
	if retrySeconds > 0 {
		c.Header("Retry-After", strconv.FormatInt(retrySeconds, 10))
	}
	reason, message := "IP_RISK_DENIED", "Access denied"
	if status == 503 {
		reason = "IP_RISK_UNAVAILABLE"
		message = "Access control temporarily unavailable"
	}
	response.ErrorWithDetails(c, status, message, reason, nil)
	c.Abort()
}

func (m *Manager) assess(ctx context.Context, p *policy, address netip.Addr, category, ua string, event *Event) (string, int64, error) {
	c := p.config
	if p.denied(address, time.Now()) {
		return "ip_blacklist", 0, nil
	}
	if hasCountryRules(c) {
		event.Country = m.country(ctx, address)
		if countryDenied(c, address, event.Country) {
			if event.Country == "" {
				return "country_unknown", 0, nil
			}
			if len(c.AllowedCountries) > 0 {
				return "country_not_allowed", 0, nil
			}
			return "country_blocked", 0, nil
		}
	}
	if category != "" {
		if reason := uaDenied(c, ua); reason != "" {
			return reason, 0, nil
		}
	}
	ban, err := m.activeBan(ctx, event.Network, c.ObserveOnly)
	if err != nil {
		return "", 0, err
	}
	if ban != nil {
		return ban.Reason, max(int64(1), ban.ExpiresAt-time.Now().Unix()), nil
	}
	if category == "" {
		return "", 0, nil
	}
	var reason string
	var t Threshold
	switch category {
	case "login":
		reason = "login_attempts"
		t = c.LoginAttempts
	case "register":
		reason = "register_attempts"
		t = c.RegisterAttempts
	case "verification":
		reason = "verification_attempts"
		t = c.VerificationAttempts
	}
	ban, err = m.count(ctx, c, event.Network, reason, t, false)
	if err != nil {
		return "", 0, err
	}
	if ban != nil {
		return ban.Reason, max(int64(1), ban.ExpiresAt-time.Now().Unix()), nil
	}
	return "", 0, nil
}

func uaDenied(c Config, ua string) string {
	if c.BlockEmptyUA && strings.TrimSpace(ua) == "" {
		return "empty_user_agent"
	}
	lowerUA := strings.ToLower(ua)
	for _, pattern := range c.DenyUserAgents {
		if strings.Contains(lowerUA, pattern) {
			return "user_agent_blocked"
		}
	}
	return ""
}
