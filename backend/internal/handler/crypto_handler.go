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
	store     cryptopay.SettingStore
	service   *cryptopay.Service
}

func NewCryptoHandler(addressService *addresses.Service, settings *service.SettingService, cryptoSvc *cryptopay.Service) *CryptoHandler {
	return &CryptoHandler{
		addresses: addressService,
		store:     cryptopay.NewServiceSettingStore(settings),
		service:   cryptoSvc,
	}
}

// GetDepositAddresses returns (or lazily assigns) the caller's addresses.
// GET /api/v1/crypto/deposit-addresses
func (h *CryptoHandler) GetDepositAddresses(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	cfg, err := cryptopay.LoadConfig(c.Request.Context(), h.store)
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
	cfg, err := cryptopay.LoadConfig(c.Request.Context(), h.store)
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
	if err := cryptopay.SaveConfig(c.Request.Context(), h.store, cfg); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	// Apply immediately: rebuild pools and restart the scan worker.
	if h.service != nil {
		h.service.Reload(c.Request.Context())
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"saved": true}})
}

// AdminSweepPlan returns the current dry-run sweep plan.
// GET /api/v1/admin/crypto/sweep/plan
func (h *CryptoHandler) AdminSweepPlan(c *gin.Context) {
	if h.service == nil {
		response.BadRequest(c, "crypto channel unavailable")
		return
	}
	plan, err := h.service.BuildSweepPlan(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": plan})
}

// AdminSweepRecords lists sweep history.
// GET /api/v1/admin/crypto/sweep/records
func (h *CryptoHandler) AdminSweepRecords(c *gin.Context) {
	if h.service == nil {
		response.BadRequest(c, "crypto channel unavailable")
		return
	}
	records, err := h.service.ListSweepRecords(c.Request.Context(), c.Query("chain"), 100)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": records})
}

// AdminListDeposits lists observed deposits for reconciliation.
// GET /api/v1/admin/crypto/deposits
func (h *CryptoHandler) AdminListDeposits(c *gin.Context) {
	if h.service == nil {
		response.BadRequest(c, "crypto channel unavailable")
		return
	}
	deposits, err := h.service.ListDeposits(c.Request.Context(), c.Query("chain"), c.Query("status"), 100)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": deposits})
}

// AdminListAddresses lists assignments with optional family/status filters.
// GET /api/v1/admin/crypto/addresses
func (h *CryptoHandler) AdminListAddresses(c *gin.Context) {
	// user_id is optional now: without it the console shows every address
	// with aggregated credited/swept totals (the operator's main view).
	var userID int64
	if raw := c.Query("user_id"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			response.BadRequest(c, "invalid user_id")
			return
		}
		userID = parsed
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 50
	}
	list, total, err := h.addresses.AdminList(c.Request.Context(), userID, c.Query("family"), pageSize, (page-1)*pageSize)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"items": list, "total": total}})
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
