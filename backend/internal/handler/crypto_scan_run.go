package handler

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

// startAsyncScan is the ?async=1 branch of AdminRunScan. The scan outlives the
// request: StartScanAsync drops the request's cancellation, so a browser that
// leaves does not stop it. The caller has already enforced the from_block
// step-up rule.
func (h *CryptoHandler) startAsyncScan(c *gin.Context, fromBlock uint64) {
	state, err := h.service.StartScanAsync(c.Request.Context(), fromBlock)
	if err != nil {
		status, message := scanStartFailure(err)
		if status >= http.StatusInternalServerError {
			slog.WarnContext(c.Request.Context(), "[cryptopay] catch-up scan did not start", "error", err)
		}
		response.Error(c, status, message)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"data": state})
}

// scanStartFailure maps a start error to a status and a fixed message.
func scanStartFailure(err error) (int, string) {
	switch {
	case errors.Is(err, cryptopay.ErrScanJobRunning), errors.Is(err, cryptopay.ErrScanBusy):
		return http.StatusConflict, "A catch-up scan is already running; follow it instead of starting another"
	case errors.Is(err, mode.ErrReadOnly):
		return http.StatusForbidden, "Payment scanning is read-only on this instance"
	case errors.Is(err, cryptopay.ErrScanUnavailable):
		return http.StatusServiceUnavailable, "Payment scanner unavailable; enable the payment channel first"
	}
	return http.StatusServiceUnavailable, "Payment scan could not start"
}

// AdminScanRunStatus reports one asynchronous catch-up scan.
// GET /api/v1/admin/crypto/scan/runs/:id
func (h *CryptoHandler) AdminScanRunStatus(c *gin.Context) {
	if h.service == nil {
		response.BadRequest(c, "crypto channel unavailable")
		return
	}
	run := h.service.ScanRun(c.Param("id"))
	if run == nil {
		response.Error(c, http.StatusNotFound, "scan run not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": run})
}

// AdminScanRuns lists recent catch-up scans, newest first.
// GET /api/v1/admin/crypto/scan/runs
func (h *CryptoHandler) AdminScanRuns(c *gin.Context) {
	if h.service == nil {
		response.BadRequest(c, "crypto channel unavailable")
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": h.service.ScanRuns()})
}

// AdminCancelScanRun asks a running catch-up scan to stop. Progress already
// saved stays saved: the scan cursor only ever moves after a finished batch.
// POST /api/v1/admin/crypto/scan/runs/:id/cancel
// The route group treats every non-GET request as a payment operation, so this
// needs a fresh second factor; it is deliberately not exempted like scan/run.
func (h *CryptoHandler) AdminCancelScanRun(c *gin.Context) {
	if h.service == nil {
		response.BadRequest(c, "crypto channel unavailable")
		return
	}
	run, err := h.service.CancelScanRun(c.Param("id"))
	if err != nil {
		status, message := scanCancelFailure(err)
		response.Error(c, status, message)
		return
	}
	// 202: the run reaches "cancelled" when its goroutine notices.
	c.JSON(http.StatusAccepted, gin.H{"data": run})
}

// scanCancelFailure maps a cancel error to a status and a fixed message.
func scanCancelFailure(err error) (int, string) {
	switch {
	case errors.Is(err, cryptopay.ErrScanRunNotFound):
		return http.StatusNotFound, "scan run not found"
	case errors.Is(err, cryptopay.ErrScanRunFinished):
		return http.StatusConflict, "scan run already finished"
	}
	return http.StatusInternalServerError, "scan run could not be cancelled"
}
