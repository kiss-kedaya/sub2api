package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/sweeper"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

type cryptoMnemonicExporter interface {
	ExportMnemonics(ctx context.Context, passphrase string) (*sweeper.MnemonicExport, error)
}

// CryptoMnemonicExportRequest is accepted once per explicit user action.
type CryptoMnemonicExportRequest struct {
	Passphrase string `json:"passphrase"`
}

// CryptoMnemonicExportResponse contains exactly the two independent seed fields.
type CryptoMnemonicExportResponse struct {
	DepositMnemonic string `json:"deposit_mnemonic"`
	HotMnemonic     string `json:"hot_mnemonic"`
}

// AdminExportMnemonics returns plaintext only after the wallet has durably
// audited the successful, passphrase-authenticated vault decryption.
func (h *CryptoHandler) AdminExportMnemonics(c *gin.Context) {
	cryptoVaultHeaders(c)
	var req CryptoMnemonicExportRequest
	if !decodeCryptoMnemonicRequest(c, &req) {
		return
	}
	if strings.TrimSpace(req.Passphrase) == "" || len(req.Passphrase) > 1024 {
		req.Passphrase = ""
		response.BadRequest(c, "Vault passphrase is required (maximum 1024 bytes)")
		return
	}
	if h.mnemonicExporter == nil {
		req.Passphrase = ""
		response.Error(c, http.StatusServiceUnavailable, "Mnemonic export unavailable")
		return
	}
	result, err := h.mnemonicExporter.ExportMnemonics(c.Request.Context(), req.Passphrase)
	req.Passphrase = ""
	if err != nil {
		status := http.StatusServiceUnavailable
		message := "Mnemonic export unavailable"
		switch {
		case errors.Is(err, sweeper.ErrMnemonicExportRejected):
			status, message = http.StatusForbidden, "Vault authentication failed"
		case errors.Is(err, sweeper.ErrMnemonicExportLimited):
			status, message = http.StatusTooManyRequests, "Mnemonic export is temporarily rate limited"
		}
		response.Error(c, status, message)
		return
	}
	if result == nil || strings.TrimSpace(result.DepositMnemonic) == "" || strings.TrimSpace(result.HotMnemonic) == "" {
		response.Error(c, http.StatusServiceUnavailable, "Mnemonic export unavailable")
		return
	}
	defer func() { result.DepositMnemonic, result.HotMnemonic = "", "" }()
	c.JSON(http.StatusOK, CryptoMnemonicExportResponse{
		DepositMnemonic: result.DepositMnemonic,
		HotMnemonic:     result.HotMnemonic,
	})
}

func decodeCryptoMnemonicRequest(c *gin.Context, target any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4<<10)
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		response.BadRequest(c, "Invalid mnemonic export request")
		return false
	}
	defer func() { clear(raw) }()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil || decoder.Decode(new(any)) != io.EOF {
		response.BadRequest(c, "Invalid mnemonic export request")
		return false
	}
	return true
}
