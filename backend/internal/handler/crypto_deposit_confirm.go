package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

// cryptoDepositConfirmer is the manual-confirmation surface of the crypto
// service. The handler stays a thin adapter: validation of the ids is repeated
// inside the service, which is the only place that touches the ledger.
type cryptoDepositConfirmer interface {
	ConfirmDeposits(ctx context.Context, adminID int64, ids []int64, note string) (*cryptopay.DepositConfirmSummary, error)
}

const (
	depositConfirmBodyLimit = 8 << 10
	depositConfirmNoteRunes = 200
)

// AdminConfirmDeposits credits pending deposits after an on-chain recheck.
// POST /api/v1/admin/crypto/deposits/confirm
//
// It sits in the CryptoAdminSecurity group, so every call already needs a fresh
// X-Crypto-TOTP or a session-bound X-Crypto-Grant. Nothing here can skip the
// chain recheck: the request has no such field, and unknown fields are refused.
func (h *CryptoHandler) AdminConfirmDeposits(c *gin.Context) {
	cryptoVaultHeaders(c)
	var req struct {
		IDs  []int64 `json:"ids"`
		Note string  `json:"note"`
	}
	if !decodeCryptoRequest(c, &req, depositConfirmBodyLimit, "Invalid deposit confirmation request") {
		return
	}
	ids, err := cryptopay.NormalizeDepositConfirmIDs(req.IDs)
	if err != nil {
		response.BadRequest(c, "ids must contain 1 to 50 distinct positive integers")
		return
	}
	note := strings.TrimSpace(req.Note)
	if utf8.RuneCountInString(note) > depositConfirmNoteRunes {
		response.BadRequest(c, "note must be at most 200 characters")
		return
	}
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	if h.depositConfirmer == nil {
		response.Error(c, http.StatusServiceUnavailable, "Deposit confirmation unavailable")
		return
	}
	// Once started, a batch should finish even if the operator closes the tab:
	// every id is resumable, and abandoning the loop only leaves work half-done.
	// The service applies its own overall deadline.
	summary, err := h.depositConfirmer.ConfirmDeposits(context.WithoutCancel(c.Request.Context()), subject.UserID, ids, note)
	if err != nil {
		switch {
		case errors.Is(err, cryptopay.ErrDepositConfirmInvalid):
			response.BadRequest(c, "ids must contain 1 to 50 distinct positive integers")
		case errors.Is(err, cryptopay.ErrDepositConfirmSecurityLocked):
			response.Error(c, http.StatusConflict, "Wallet security lock is active; deposits cannot be confirmed until it is recovered")
		case errors.Is(err, cryptopay.ErrDepositConfirmReadOnly):
			response.Error(c, http.StatusForbidden, "This instance is read-only")
		default:
			// The service already logged the sanitised cause. Never echo it.
			response.Error(c, http.StatusServiceUnavailable, "Deposit confirmation unavailable")
		}
		return
	}
	if summary == nil {
		response.Error(c, http.StatusServiceUnavailable, "Deposit confirmation unavailable")
		return
	}
	outcome := "success"
	switch {
	case summary.Failed > 0 && summary.Failed == len(summary.Results):
		outcome = "failed"
	case summary.Failed > 0:
		outcome = "partial"
	}
	middleware2.SetAuditExtra(c, map[string]any{"result": outcome, "requested_count": len(ids)})
	c.JSON(http.StatusOK, gin.H{"data": summary})
}
