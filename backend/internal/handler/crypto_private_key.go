package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/wallet"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

type cryptoAddressPrivateKeyExporter interface {
	ExportAddressPrivateKey(context.Context, int64) (*wallet.AddressPrivateKeyExport, error)
}

type cryptoAddressPrivateKeyShareExporter interface {
	ExportAddressPrivateKeyWithShare(context.Context, int64, string) (*wallet.AddressPrivateKeyExport, error)
}

// AdminExportAddressPrivateKey runs inside the same operator JWT, transport,
// payment verification and security-notification boundary as mnemonic export.
// Only a database assignment ID is accepted; no caller-controlled derivation.
func (h *CryptoHandler) AdminExportAddressPrivateKey(c *gin.Context) {
	cryptoVaultHeaders(c)
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid address ID")
		return
	}
	var req struct {
		Share string `json:"share"`
	}
	if !decodeCryptoRequest(c, &req, 4<<10, "Invalid private key export request") {
		return
	}
	defer func() { req.Share = "" }()
	if h.privateKeyExporter == nil {
		response.Error(c, http.StatusServiceUnavailable, "Private key export unavailable")
		return
	}
	var result *wallet.AddressPrivateKeyExport
	if strings.TrimSpace(req.Share) != "" {
		shareExporter, ok := h.privateKeyExporter.(cryptoAddressPrivateKeyShareExporter)
		if !ok {
			response.Error(c, http.StatusConflict, "recovery shard is required to export a private key")
			return
		}
		result, err = shareExporter.ExportAddressPrivateKeyWithShare(cryptoAuditContext(c), id, req.Share)
	} else {
		result, err = h.privateKeyExporter.ExportAddressPrivateKey(cryptoAuditContext(c), id)
	}
	if result != nil {
		defer func() { result.PrivateKey = "" }()
	}
	if writeCryptoSignerError(c, err) {
		return
	}
	if err != nil {
		if errors.Is(err, wallet.ErrExportAddressNotFound) {
			response.Error(c, http.StatusNotFound, "Deposit address not found")
			return
		}
		// Vault/driver errors can contain sensitive data. Never log or echo them.
		response.Error(c, http.StatusServiceUnavailable, "Private key export unavailable")
		return
	}
	if result == nil || result.AddressID != id || strings.TrimSpace(result.PrivateKey) == "" ||
		strings.TrimSpace(result.Address) == "" {
		response.Error(c, http.StatusServiceUnavailable, "Private key export unavailable")
		return
	}
	c.JSON(http.StatusOK, result)
}
