package admin

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// GetModelWhitelistAutoSync returns one account's auto-sync configuration and
// the runner's last run/next run/error bookkeeping.
//
// GET /api/v1/admin/accounts/:id/model-whitelist-auto-sync
//
// Response data:
//
//	{
//	  "account_id": 12,
//	  "supported": true,
//	  "config": {"enabled": true, "interval_hours": 24, "mode": "whitelist"},
//	  "state": {
//	    "last_run_at": "...", "last_success_at": "...", "next_run_at": "...",
//	    "last_error": "...", "last_error_at": "...", "last_skipped_reason": "custom_mapping",
//	    "consecutive_failures": 2, "last_applied_models": 41
//	  },
//	  "runner_available": true
//	}
//
// The same two objects are already present on the normal account payload as
// account.extra.model_whitelist_auto_sync and
// account.extra.model_whitelist_auto_sync_state, so list views do not need an
// extra request.
func (h *AccountHandler) GetModelWhitelistAutoSync(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	if h.adminService == nil {
		response.InternalError(c, "Admin service is not configured")
		return
	}
	account, err := h.adminService.GetAccount(c.Request.Context(), accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	config, hasConfig := service.ModelWhitelistAutoSyncConfigFromExtra(account.Extra)
	state := service.ModelWhitelistAutoSyncStateFromExtra(account.Extra)
	supported := account.Type == service.AccountTypeAPIKey
	reason := ""
	if !supported {
		reason = service.ModelWhitelistAutoSyncSkipNotAPIKey
	}
	response.Success(c, gin.H{
		"account_id":       accountID,
		"supported":        supported,
		"reason":           reason,
		"config":           config,
		"has_config":       hasConfig,
		"state":            state,
		"runner_available": h.modelWhitelistAutoSync != nil,
	})
}

// RunModelWhitelistAutoSync fetches the live upstream model catalog and replaces
// credentials.model_mapping with an identity whitelist, atomically.
//
// POST /api/v1/admin/accounts/:id/model-whitelist-auto-sync/run
//
// Guards (a skipped run returns applied=false plus skipped_reason and the
// updated state, with nothing written):
//   - custom_mapping: the current mapping is not a plain identity map, so an
//     administrator's routing is never destroyed;
//   - empty_catalog: upstream returned no models, the old mapping is kept;
//   - concurrent_edit: model_mapping changed while the fetch was in flight
//     (compare-and-set), or another instance holds the per-account lease;
//   - locked: another runner instance is already working this account;
//   - not_apikey_account: only API-key accounts carry model_mapping.
func (h *AccountHandler) RunModelWhitelistAutoSync(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	if h.modelWhitelistAutoSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "Model whitelist auto sync is unavailable")
		return
	}

	result, runErr := h.modelWhitelistAutoSync.RunNow(c.Request.Context(), accountID)
	if runErr != nil {
		if errors.Is(runErr, service.ErrAccountNotFound) {
			response.NotFound(c, "Account not found")
			return
		}
		if result.State == nil {
			response.ErrorFrom(c, runErr)
			return
		}
		// The failure was recorded in the account's state; return it so the UI
		// can show the reason without a second request.
	}
	response.Success(c, result)
}
