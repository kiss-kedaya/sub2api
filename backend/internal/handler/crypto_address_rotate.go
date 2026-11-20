package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/addresses"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

// cryptoAddressRotation is the admin-facing snapshot of one deposit assignment.
// Addresses are not secrets, but the row is kept minimal: no user PII here.
type cryptoAddressRotation struct {
	ID              int64  `json:"id"`
	UserID          int64  `json:"user_id"`
	Family          string `json:"family"`
	Address         string `json:"address"`
	DerivationIndex int64  `json:"derivation_index"`
	Status          string `json:"status"`
}

func newCryptoAddressRotation(a *addresses.Assignment) *cryptoAddressRotation {
	if a == nil {
		return nil
	}
	return &cryptoAddressRotation{
		ID: a.ID, UserID: a.UserID, Family: a.Family,
		Address: a.Address, DerivationIndex: a.DerivationIndex, Status: a.Status,
	}
}

// AdminRotateAddress retires one active deposit assignment and binds the next
// free pool address for the same user and family. The retired row is kept as
// disabled history so a late deposit to the old address is still detected and
// credited; the response returns both rows so the operator can record what
// changed.
//
// POST /api/v1/admin/crypto/addresses/:id/rotate
func (h *CryptoHandler) AdminRotateAddress(c *gin.Context) {
	cryptoVaultHeaders(c)
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid address ID")
		return
	}
	var req struct{}
	if !decodeCryptoRequest(c, &req, 4<<10, "Invalid address rotation request") {
		return
	}
	if h.addresses == nil {
		response.Error(c, http.StatusServiceUnavailable, "Deposit addresses unavailable")
		return
	}
	oldAssignment, newAssignment, err := h.addresses.Rotate(c.Request.Context(), id)
	if err != nil {
		switch {
		case errors.Is(err, addresses.ErrAssignmentNotFound):
			response.NotFound(c, "Deposit address not found")
		case errors.Is(err, addresses.ErrAssignmentNotActive):
			response.Error(c, http.StatusConflict, "Deposit address is not active")
		case errors.Is(err, addresses.ErrNoFreeAddress):
			response.Error(c, http.StatusServiceUnavailable, "No free deposit address available")
		default:
			response.Error(c, http.StatusInternalServerError, "Failed to rotate deposit address")
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"old": newCryptoAddressRotation(oldAssignment),
		"new": newCryptoAddressRotation(newAssignment),
	}})
}
