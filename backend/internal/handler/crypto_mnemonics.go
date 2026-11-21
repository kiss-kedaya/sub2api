package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/sweeper"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

type cryptoMnemonicExporter interface {
	ExportMnemonics(ctx context.Context) (*sweeper.MnemonicExport, error)
}

// CryptoMnemonicExportRequest is accepted once per explicit user action. A
// data-key vault additionally requires one operator-held recovery share.
type CryptoMnemonicExportRequest struct {
	Share string `json:"share"`
}

// cryptoMnemonicShareExporter is implemented by the wallet facade for vaults
// sealed with a data key.
type cryptoMnemonicShareExporter interface {
	ExportMnemonicsWithShare(context.Context, string) (*sweeper.MnemonicExport, error)
}

// CryptoMnemonicExportResponse contains exactly the two independent seed fields.
type CryptoMnemonicExportResponse struct {
	DepositMnemonic string `json:"deposit_mnemonic"`
	HotMnemonic     string `json:"hot_mnemonic"`
}

// AdminExportMnemonics returns plaintext only after the wallet has durably
// audited the successful decryption after fresh backend 2FA.
func (h *CryptoHandler) AdminExportMnemonics(c *gin.Context) {
	cryptoVaultHeaders(c)
	var req CryptoMnemonicExportRequest
	if !decodeCryptoMnemonicRequest(c, &req) {
		return
	}
	if h.mnemonicExporter == nil {
		response.Error(c, http.StatusServiceUnavailable, "Mnemonic export unavailable")
		return
	}
	defer func() { req.Share = "" }()
	var result *sweeper.MnemonicExport
	var err error
	if strings.TrimSpace(req.Share) != "" {
		shareExporter, ok := h.mnemonicExporter.(cryptoMnemonicShareExporter)
		if !ok {
			response.Error(c, http.StatusConflict, "recovery shard is required to export mnemonics")
			return
		}
		result, err = shareExporter.ExportMnemonicsWithShare(cryptoAuditContext(c), req.Share)
	} else {
		result, err = h.mnemonicExporter.ExportMnemonics(cryptoAuditContext(c))
	}
	if writeCryptoSignerError(c, err) {
		return
	}
	if err != nil {
		status := http.StatusServiceUnavailable
		message := "Mnemonic export unavailable"

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
