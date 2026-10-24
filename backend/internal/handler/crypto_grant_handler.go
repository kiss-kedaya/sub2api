package handler

import (
	"context"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// Kept separate from the enrollment verifier so existing integrations can use
// per-operation TOTP until their verifier implements short-lived grants.
type cryptoGrantSecurityVerifier interface {
	IssueRiskGrant(context.Context, int64, string, string) (*service.CryptoVerificationGrant, error)
	VerifyRiskGrant(context.Context, int64, string, string) error
}

// AdminCryptoSecurityVerify issues a fixed five-minute payment capability only
// after a fresh authenticator code. A previous grant cannot mint another grant.
func (h *CryptoHandler) AdminCryptoSecurityVerify(c *gin.Context) {
	cryptoVaultHeaders(c)
	if len(c.Request.Header.Values("X-Crypto-Grant")) != 0 || len(c.Request.Header.Values("X-Crypto-TOTP")) != 0 {
		response.BadRequest(c, "verification requires a fresh TOTP code in the request body")
		return
	}
	var request struct {
		TotpCode string `json:"totp_code"`
	}
	if !decodeCryptoRequest(c, &request, 4<<10, "invalid verification request") {
		return
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "authentication required")
		return
	}
	verifier, ok := h.security.(cryptoGrantSecurityVerifier)
	if !ok {
		response.ErrorFrom(c, service.ErrCryptoTotpUnavailable)
		return
	}
	grant, err := verifier.IssueRiskGrant(c.Request.Context(), subject.UserID, cryptoSessionID(c), request.TotpCode)
	request.TotpCode = ""
	if response.ErrorFrom(c, err) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": grant})
}
