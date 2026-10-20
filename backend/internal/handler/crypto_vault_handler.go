package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

func cryptoVaultHeaders(c *gin.Context) {
	c.Header("Cache-Control", "no-store, no-cache, must-revalidate")
	c.Header("Pragma", "no-cache")
	c.Header("Expires", "0")
	c.Header("X-Content-Type-Options", "nosniff")
}

// Both handlers are behind the mandatory per-request Crypto 2FA middleware.
// They never receive a plaintext seed from the isolated signer on export.
func (h *CryptoHandler) AdminExportVaultBackup(c *gin.Context) {
	cryptoVaultHeaders(c)
	var req struct {
		Passphrase string `json:"passphrase"`
	}
	if !decodeCryptoVaultRequest(c, &req) {
		return
	}
	if strings.TrimSpace(req.Passphrase) == "" || len(req.Passphrase) > 1024 {
		response.BadRequest(c, "Vault passphrase is required (maximum 1024 bytes)")
		return
	}
	if h.service == nil {
		response.Error(c, http.StatusServiceUnavailable, "Signer unavailable")
		return
	}
	backup, err := h.service.ExportVaultBackup(c.Request.Context(), req.Passphrase)
	req.Passphrase = ""
	if err != nil {
		// Do not propagate signer/driver bodies or a secret-bearing request.
		response.Error(c, http.StatusForbidden, "Vault backup refused; verify the vault passphrase and signer status")
		return
	}
	c.Header("Content-Disposition", `attachment; filename="sub2api-wallet-backup.json"`)
	c.JSON(http.StatusOK, backup)
}

func (h *CryptoHandler) AdminImportHotMnemonic(c *gin.Context) {
	cryptoVaultHeaders(c)
	var req struct {
		Passphrase  string `json:"passphrase"`
		HotMnemonic string `json:"hot_mnemonic"`
	}
	if !decodeCryptoVaultRequest(c, &req) {
		return
	}
	if strings.TrimSpace(req.Passphrase) == "" || len(req.Passphrase) > 1024 ||
		strings.TrimSpace(req.HotMnemonic) == "" || len(req.HotMnemonic) > 1024 {
		response.BadRequest(c, "Vault passphrase and valid hot-wallet mnemonic are required")
		return
	}
	if h.service == nil {
		response.Error(c, http.StatusServiceUnavailable, "Signer unavailable")
		return
	}
	err := h.service.ImportHotMnemonic(c.Request.Context(), req.Passphrase, req.HotMnemonic)
	req.Passphrase, req.HotMnemonic = "", ""
	if err != nil {
		response.Error(c, http.StatusConflict, "Hot-wallet import refused; check the vault passphrase and unresolved sweeps")
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"saved": true, "requires_unlock": true}})
}

func decodeCryptoVaultRequest(c *gin.Context, target any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4<<10)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil || decoder.Decode(new(any)) != io.EOF {
		response.BadRequest(c, "Invalid vault request")
		return false
	}
	return true
}
