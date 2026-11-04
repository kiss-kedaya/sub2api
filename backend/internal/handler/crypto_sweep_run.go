package handler

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

// AdminSweepRunStatus reports one asynchronous sweep run.
// GET /api/v1/admin/crypto/sweep/runs/:id
func (h *CryptoHandler) AdminSweepRunStatus(c *gin.Context) {
	if h.service == nil {
		response.BadRequest(c, "crypto channel unavailable")
		return
	}
	run := h.service.SweepRun(c.Param("id"))
	if run == nil {
		response.Error(c, http.StatusNotFound, "sweep run not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": run})
}

// AdminSweepRuns lists recent asynchronous runs, newest first.
// GET /api/v1/admin/crypto/sweep/runs
func (h *CryptoHandler) AdminSweepRuns(c *gin.Context) {
	if h.service == nil {
		response.BadRequest(c, "crypto channel unavailable")
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": h.service.SweepRuns()})
}
