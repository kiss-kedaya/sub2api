package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type cryptoAdminSecurityVerifier interface {
	CurrentOperator(context.Context, int64) (*service.User, error)
	VerifyRisk(context.Context, int64, string) error
	Status(context.Context, int64) (*service.CryptoSecurityStatus, error)
	InitiateSetup(context.Context, int64, string) (*service.TotpSetupResponse, error)
	Enable(context.Context, int64, string, string, string) error
}

// CryptoAdminSecurity protects the entire group, including future endpoints.
// Enrollment is exempt only from the already-bound-code requirement.
func (h *CryptoHandler) CryptoAdminSecurity(c *gin.Context) {
	// Admin responses may contain RPC credentials, wallet addresses, or payment
	// history. Never let browsers or intermediary caches persist them.
	cryptoNoStore(c)
	if !service.CryptoInstanceEnabled() {
		response.ErrorWithDetails(c, 503, "crypto is disabled on this instance", "CRYPTO_DISABLED", nil)
		c.Abort()
		return
	}
	_, ok := h.cryptoJWTAdmin(c)
	if !ok {
		return
	}
	path := c.FullPath()
	setup := c.Request.Method == http.MethodPost && (strings.HasSuffix(path, "/security/setup") || strings.HasSuffix(path, "/security/enable"))
	verify := c.Request.Method == http.MethodPost && strings.HasSuffix(path, "/security/verify")
	intent := c.Request.Method == http.MethodPost && strings.HasSuffix(path, "/security/operation-intent")
	configPatch := c.Request.Method == http.MethodPut && strings.HasSuffix(path, "/crypto/config")
	risky := c.Request.Method != http.MethodGet || strings.HasSuffix(path, "/deposits.csv") ||
		strings.HasSuffix(path, "/config/private")
	if service.CryptoAdminTestReadOnly() && (c.Request.Method != http.MethodGet || strings.HasSuffix(path, "/deposits.csv")) {
		response.ErrorWithDetails(c, http.StatusForbidden, "crypto test instance is read-only", "CRYPTO_TEST_MODE_READ_ONLY", nil)
		c.Abort()
		return
	}
	if !service.CryptoAdminAllowHTTP() && !cryptoSecureTransport(c.Request, h.securityConfig) {
		response.ErrorWithDetails(c, 403, "HTTPS or a loopback tunnel is required", "CRYPTO_HTTPS_REQUIRED", nil)
		c.Abort()
		return
	}
	if risky && !setup && !verify && !intent && !configPatch {
		if !h.requireCryptoRisk(c) {
			return
		}
		if action := cryptoVerifiedOperation(c); action != "" && !h.notifyWalletAction(c, action, "verified") {
			c.Abort()
			return
		}
	}
	c.Next()
}

// requireCryptoRisk verifies a session-bound grant or a fresh backend code. PUT /config calls this
// after inspecting the actual patch; other sensitive routes call it in their
// group middleware. A missing verifier always fails closed.
func (h *CryptoHandler) requireCryptoRisk(c *gin.Context) bool {
	if h.security == nil {
		response.ErrorFrom(c, service.ErrCryptoTotpUnavailable)
		c.Abort()
		return false
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.ErrorFrom(c, service.ErrCryptoAdminRequired)
		c.Abort()
		return false
	}
	codes := c.Request.Header.Values("X-Crypto-TOTP")
	grants := c.Request.Header.Values("X-Crypto-Grant")
	if len(grants) != 0 {
		if len(grants) != 1 || len(codes) != 0 {
			response.ErrorFrom(c, service.ErrCryptoGrantRequired)
			c.Abort()
			return false
		}
		verifier, ok := h.security.(cryptoGrantSecurityVerifier)
		if !ok {
			response.ErrorFrom(c, service.ErrCryptoTotpUnavailable)
			c.Abort()
			return false
		}
		if response.ErrorFrom(c, verifier.VerifyRiskGrant(c.Request.Context(), subject.UserID, cryptoSessionID(c), grants[0])) {
			c.Abort()
			return false
		}
		return true
	}
	code := ""
	if len(codes) == 1 {
		code = codes[0]
	}
	if response.ErrorFrom(c, h.security.VerifyRisk(c.Request.Context(), subject.UserID, code)) {
		c.Abort()
		return false
	}
	return true
}

// CryptoUserAccess leaves ordinary JWT access unchanged outside admin test mode.
func (h *CryptoHandler) CryptoUserAccess(c *gin.Context) {
	if !service.CryptoInstanceEnabled() {
		response.ErrorWithDetails(c, 503, "crypto is disabled on this instance", "CRYPTO_DISABLED", nil)
		c.Abort()
		return
	}
	if service.CryptoAdminTestReadOnly() {
		response.ErrorWithDetails(c, http.StatusForbidden, "customer crypto routes are disabled on this test instance", "CRYPTO_TEST_MODE_READ_ONLY", nil)
		c.Abort()
		return
	}
	c.Next()
}

func (h *CryptoHandler) cryptoJWTAdmin(c *gin.Context) (int64, bool) {
	// Reject machine credentials even if accompanied by a valid JWT.
	if c.GetString("auth_method") != service.AuditAuthMethodJWT || c.GetHeader("X-API-Key") != "" {
		response.ErrorWithDetails(c, 403, "an administrator JWT session is required", "CRYPTO_ADMIN_REQUIRED", nil)
		c.Abort()
		return 0, false
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 || cryptoSessionID(c) == "" {
		response.ErrorWithDetails(c, 403, "an administrator JWT session is required", "CRYPTO_ADMIN_REQUIRED", nil)
		c.Abort()
		return 0, false
	}
	if h.security == nil {
		response.ErrorFrom(c, service.ErrCryptoTotpUnavailable)
		c.Abort()
		return 0, false
	}
	_, err := h.security.CurrentOperator(c.Request.Context(), subject.UserID)
	if response.ErrorFrom(c, err) {
		c.Abort()
		return 0, false
	}
	return subject.UserID, true
}

func cryptoSessionID(c *gin.Context) string {
	parts := strings.Fields(c.GetHeader("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	if sid := c.GetString(middleware.ContextKeySessionID); sid != "" {
		return sid
	}
	// Legacy JWTs bind to the exact validated token, never to a user-only key.
	digest := sha256.Sum256([]byte(parts[1]))
	return "jwt:" + hex.EncodeToString(digest[:])
}

func cryptoNoStore(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
}

func cryptoSecureTransport(r *http.Request, cfg *config.Config) bool {
	if r.TLS != nil {
		return true
	}
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		peer = r.RemoteAddr
	}
	peerIP := net.ParseIP(peer)
	if peerIP == nil {
		return false
	}
	// Forwarded schemes are accepted only from explicitly configured proxies.
	if cfg != nil && cfg.Server.TrustedProxiesConfigured && len(r.Header.Values("X-Forwarded-Proto")) == 1 &&
		strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		for _, entry := range cfg.Server.TrustedProxies {
			if ip := net.ParseIP(entry); ip != nil && ip.Equal(peerIP) {
				return true
			}
			if _, network, err := net.ParseCIDR(entry); err == nil {
				bits, _ := network.Mask.Size()
				// A trust-all configuration cannot authorize a public HTTPS claim.
				if bits > 0 && network.Contains(peerIP) {
					return true
				}
			}
		}
	}
	// Loopback means both the actual socket peer and Host. Forwarded requests
	// cannot disguise a public HTTP request as a localhost SSH tunnel.
	if !peerIP.IsLoopback() {
		return false
	}
	for _, header := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Proto", "X-Forwarded-Host", "X-Real-IP"} {
		if len(r.Header.Values(header)) > 0 {
			return false
		}
	}
	host := r.Host
	if name, _, err := net.SplitHostPort(host); err == nil {
		host = name
	}
	host = strings.Trim(host, "[]")
	return strings.EqualFold(host, "localhost") || (net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback())
}

// AdminCryptoSecurityStatus reports effective payment-TOTP readiness.
func (h *CryptoHandler) AdminCryptoSecurityStatus(c *gin.Context) {
	cryptoNoStore(c)
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "authentication required")
		return
	}
	status, err := h.security.Status(c.Request.Context(), subject.UserID)
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, status)
}

// AdminCryptoSecuritySetup returns a short-lived seed bound to the current JWT session.
func (h *CryptoHandler) AdminCryptoSecuritySetup(c *gin.Context) {
	cryptoNoStore(c)
	var req struct{}
	if !decodeCryptoRequest(c, &req, 4<<10, "invalid setup request") {
		return
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "authentication required")
		return
	}
	result, err := h.security.InitiateSetup(c.Request.Context(), subject.UserID, cryptoSessionID(c))
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, result)
}

// AdminCryptoSecurityEnable never trusts a client enabled flag.
func (h *CryptoHandler) AdminCryptoSecurityEnable(c *gin.Context) {
	cryptoNoStore(c)
	var req struct {
		TotpCode   string `json:"totp_code"`
		SetupToken string `json:"setup_token"`
	}
	if !decodeCryptoRequest(c, &req, 4<<10, "invalid enable request") {
		return
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "authentication required")
		return
	}
	if response.ErrorFrom(c, h.security.Enable(c.Request.Context(), subject.UserID, cryptoSessionID(c), req.TotpCode, req.SetupToken)) {
		return
	}
	response.Success(c, gin.H{"success": true})
}
