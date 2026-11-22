package handler

import (
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"

	"github.com/gin-gonic/gin"
)

// auditActionCryptoSweepDestinationChanged is the audit action recorded for a
// verified sweep-destination change. It is intentionally the security-facing
// constant rather than a derived "config.update" name: auditors filter on it.
const auditActionCryptoSweepDestinationChanged = "CRYPTO_SWEEP_DESTINATION_CHANGED"

// sweepDestinationRequest is the dedicated destination-change payload.
//
// The generic config endpoint refuses to move a pinned destination; this
// endpoint is the only way to do it, and it requires a fresh authenticator
// code, the verbatim risk phrase and an explicit acknowledgement flag.
type sweepDestinationRequest struct {
	HotWalletEVM    *string `json:"hot_wallet_evm"`
	HotWalletSolana *string `json:"hot_wallet_solana"`
	TotpCode        string  `json:"totp_code"`
	RiskPhrase      string  `json:"risk_phrase"`
	AcknowledgeRisk bool    `json:"acknowledge_risk"`
}

// AdminUpdateSweepDestination moves the pinned sweep destination.
//
// POST /api/v1/admin/crypto/signer/sweep-destination
//
// Fail-closed: a wrong code, a wrong phrase or a missing acknowledgement is
// rejected before anything is written. Re-submitting the addresses that are
// already pinned succeeds without moving the lock timestamp.
func (h *CryptoHandler) AdminUpdateSweepDestination(c *gin.Context) {
	cryptoVaultHeaders(c)
	var request sweepDestinationRequest
	if !decodeCryptoRequest(c, &request, 8<<10, "invalid sweep destination request") {
		return
	}
	defer func() { request.TotpCode = "" }()
	if !request.AcknowledgeRisk {
		response.ErrorWithDetails(c, http.StatusBadRequest,
			"changing the sweep destination requires explicit risk acknowledgement",
			"CRYPTO_SIGNER_RISK_ACK_REQUIRED", nil)
		return
	}
	// Reuse the exact signer risk phrase (and its whitespace tolerance) so the
	// server-side phrase can never drift from the one the browser acknowledges.
	if !requireSignerRiskPhrase(c, request.RiskPhrase) {
		return
	}
	if !h.requireFreshSignerTOTP(c, request.TotpCode) {
		return
	}
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "authentication required")
		return
	}
	if h.store == nil {
		response.Error(c, http.StatusServiceUnavailable, "crypto configuration unavailable")
		return
	}
	change, err := cryptopay.UpdateSweepDestination(c.Request.Context(), h.store, cryptopay.SweepDestinationUpdate{
		HotWalletEVM:    request.HotWalletEVM,
		HotWalletSolana: request.HotWalletSolana,
		ActorUserID:     subject.UserID,
		Now:             time.Now(),
	})
	if response.ErrorFrom(c, err) {
		return
	}

	// Audit the address change with old -> new. The authenticator code and the
	// risk phrase never enter the detail; the allowlisted keys are scalar and
	// only carry destination addresses.
	middleware2.SetAuditAction(c, auditActionCryptoSweepDestinationChanged)
	middleware2.SetAuditExtra(c, map[string]any{
		"hot_wallet_evm_old":    change.Previous.EVM,
		"hot_wallet_evm_new":    change.Current.EVM,
		"hot_wallet_solana_old": change.Previous.Solana,
		"hot_wallet_solana_new": change.Current.Solana,
		"changed":               change.Changed,
		"locked":                change.Locked,
		"lock_created":          change.LockCreated,
	})

	if h.service != nil {
		if change.Changed || change.Locked {
			// Best effort: the channel never blocks or fails the operation.
			h.service.NotifyEvent(c.Request.Context(), cryptopay.EventSweepDestinationChanged, map[string]any{
				"previous_hot_wallet_evm":    change.Previous.EVM,
				"hot_wallet_evm":             change.Current.EVM,
				"previous_hot_wallet_solana": change.Previous.Solana,
				"hot_wallet_solana":          change.Current.Solana,
				"changed":                    change.Changed,
				"lock_created":               change.LockCreated,
			})
		}
		// Rebuild the sweep planner with the new destination immediately.
		h.service.Reload(c.Request.Context())
	}
	response.Success(c, change.JSON())
}
