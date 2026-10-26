package handler

import (
	"context"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

type cryptoWalletOperationNotifier interface {
	NotifyWalletOperation(context.Context, string, string) error
}

// AdminWalletOperationIntent runs under the ID1 JWT/HTTPS group guard but
// precedes code entry. It never verifies a code or authorizes a wallet action.
func (h *CryptoHandler) AdminWalletOperationIntent(c *gin.Context) {
	var req struct {
		Action string `json:"action"`
	}
	if !decodeCryptoRequest(c, &req, 4<<10, "Invalid wallet operation") {
		return
	}
	switch req.Action {
	case "sweep", "destination_change", "export_mnemonics", "export_private_key":
	default:
		response.BadRequest(c, "Invalid wallet operation")
		return
	}
	if !h.notifyWalletAction(c, req.Action, "requested") {
		return
	}
	response.Success(c, gin.H{"accepted": true})
}

func (h *CryptoHandler) notifyWalletAction(c *gin.Context, action, phase string) bool {
	if h.operationNotifier == nil || h.operationNotifier.NotifyWalletOperation(c.Request.Context(), action, phase) != nil {
		response.ErrorWithDetails(c, http.StatusServiceUnavailable,
			"钱包安全通知暂未送达，请检查 Bark 通道后重试；本次操作未执行",
			"CRYPTO_NOTIFICATION_UNAVAILABLE", nil)
		return false
	}
	return true
}

func cryptoVerifiedOperation(c *gin.Context) string {
	if c.Request.Method != http.MethodPost {
		return ""
	}
	path := c.FullPath()
	if strings.HasSuffix(path, "/wallet/export-mnemonics") {
		return "export_mnemonics"
	}
	if strings.HasSuffix(path, "/addresses/:id/export-private-key") {
		return "export_private_key"
	}
	if strings.HasSuffix(path, "/sweep/run") && c.DefaultQuery("dry_run", "1") == "0" {
		return "sweep"
	}
	return ""
}
