package handler

import (
	"context"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"

	"github.com/gin-gonic/gin"
)

// cryptoSweepGateReader is the backend-side source of truth for the "can I run
// a sweep?" question. The console renders its reason code verbatim instead of
// combining wallet/status, config and signer/status on its own.
type cryptoSweepGateReader interface {
	SweepGate(context.Context) (*cryptopay.SweepGate, error)
}

func (h *CryptoHandler) sweepGateReader() (cryptoSweepGateReader, bool) {
	if h.sweepGate != nil {
		return h.sweepGate, true
	}
	reader, ok := any(h.service).(cryptoSweepGateReader)
	return reader, ok && h.service != nil
}

// AdminSweepReadiness reports what a real sweep still needs. It is a read-only
// GET: the destination is only reported as configured/pinned, never echoed.
func (h *CryptoHandler) AdminSweepReadiness(c *gin.Context) {
	reader, ok := h.sweepGateReader()
	if !ok {
		response.Error(c, http.StatusServiceUnavailable, "crypto sweep unavailable")
		return
	}
	gate, err := reader.SweepGate(cryptoAuditContext(c))
	if response.ErrorFrom(c, err) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gate})
}
