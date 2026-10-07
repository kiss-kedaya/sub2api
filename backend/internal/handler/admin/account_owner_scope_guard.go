package admin

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// RequireAccountOwnerAccess runs before every /accounts/:id endpoint, including
// cache-backed statistics and operations that otherwise write directly by ID.
func (h *AccountHandler) RequireAccountOwnerAccess(c *gin.Context) {
	if _, _, scoped := service.AccountOwnerScopeDetail(c.Request.Context()); !scoped || c.Param("id") == "" {
		c.Next()
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid account ID")
		c.Abort()
		return
	}
	if _, err := h.adminService.GetAccount(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		c.Abort()
		return
	}
	c.Next()
}

// requireAccountBatchOwnerAccess must run before a batch cache lookup, since
// cached usage payloads are keyed by account IDs rather than administrator IDs.
func (h *AccountHandler) requireAccountBatchOwnerAccess(c *gin.Context, ids []int64) bool {
	if _, _, scoped := service.AccountOwnerScopeDetail(c.Request.Context()); !scoped {
		return true
	}
	accounts, err := h.adminService.GetAccountsByIDs(c.Request.Context(), ids)
	if err != nil {
		response.ErrorFrom(c, err)
		return false
	}
	visible := make(map[int64]struct{}, len(accounts))
	for _, account := range accounts {
		if account != nil {
			visible[account.ID] = struct{}{}
		}
	}
	for _, id := range ids {
		if _, found := visible[id]; !found {
			response.ErrorFrom(c, service.ErrAccountNotFound)
			return false
		}
	}
	return true
}
