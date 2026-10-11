// Package handler: crypto deposit channel HTTP surface.
//
// User endpoints expose the caller's deposit addresses and deposit history.
// Admin endpoints manage the channel config (including RPC endpoints),
// address status and sweep records.
package handler

import (
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/addresses"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// CryptoHandler serves the built-in crypto deposit channel.
type CryptoHandler struct {
	addresses *addresses.Service
	settings  *service.SettingService
}

func NewCryptoHandler(addressService *addresses.Service, settings *service.SettingService) *CryptoHandler {
	return &CryptoHandler{addresses: addressService, settings: settings}
}

// GetDepositAddresses returns (or lazily assigns) the caller's addresses.
// GET /api/v1/crypto/deposit-addresses
func (h *CryptoHandler) GetDepositAddresses(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	cfg, err := cryptopay.LoadConfig(c.Request.Context(), h.settings)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if !cfg.Enabled {
		response.ErrorFrom(c, errCryptoDisabled)
		return
	}
	assigned, err := h.addresses.GetOrAssign(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	chains := make([]gin.H, 0, len(cfg.Chains))
	for _, chain := range cfg.Chains {
		family := addresses.FamilyEVM
		if chain == cryptopay.ChainSolana {
			family = addresses.FamilySolana
		}
		chains = append(chains, gin.H{
			"chain":   chain,
			"address": assigned[family],
		})
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"evm":    assigned[addresses.FamilyEVM],
		"solana": assigned[addresses.FamilySolana],
		"chains": chains,
	}})
}

// AdminGetConfig returns the channel config (admin).
// GET /api/v1/admin/crypto/config
func (h *CryptoHandler) AdminGetConfig(c *gin.Context) {
	cfg, err := cryptopay.LoadConfig(c.Request.Context(), h.settings)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": cfg})
}

// AdminUpdateConfig validates and persists the channel config (admin).
// PUT /api/v1/admin/crypto/config
func (h *CryptoHandler) AdminUpdateConfig(c *gin.Context) {
	var cfg cryptopay.CryptoConfig
	if err := c.ShouldBindJSON(&cfg); err != nil {
		response.BadRequest(c, "Invalid config payload")
		return
	}
	if err := cryptopay.SaveConfig(c.Request.Context(), h.settings, cfg); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"saved": true}})
}

// AdminListAddresses lists assignments with optional family/status filters.
// GET /api/v1/admin/crypto/addresses
func (h *CryptoHandler) AdminListAddresses(c *gin.Context) {
	status := c.Query("status")
	idParam := c.Query("user_id")
	if idParam == "" {
		response.BadRequest(c, "user_id is required")
		return
	}
	userID, err := strconv.ParseInt(idParam, 10, 64)
	if err != nil {
		response.BadRequest(c, "invalid user_id")
		return
	}
	list, err := h.addresses.ListByUser(c.Request.Context(), userID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out := make([]gin.H, 0, len(list))
	for _, a := range list {
		if status != "" && a.Status != status {
			continue
		}
		out = append(out, gin.H{
			"id": a.ID, "user_id": a.UserID, "family": a.Family,
			"address": a.Address, "status": a.Status,
		})
	}
	c.JSON(http.StatusOK, gin.H{"data": out})
}

// AdminSetAddressStatus enables/disables an assignment.
// POST /api/v1/admin/crypto/addresses/:id/status
func (h *CryptoHandler) AdminSetAddressStatus(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "invalid id")
		return
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		response.BadRequest(c, "Invalid status payload")
		return
	}
	if err := h.addresses.SetStatus(c.Request.Context(), id, body.Status); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"updated": true}})
}

var errCryptoDisabled = &cryptoDisabledError{}

type cryptoDisabledError struct{}

func (e *cryptoDisabledError) Error() string { return "crypto deposit channel is disabled" }
