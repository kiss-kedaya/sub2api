package handler

import (
	"context"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"

	"github.com/gin-gonic/gin"
)

// sweepRecordPager is the part of cryptopay.Service the sweep history needs.
type sweepRecordPager interface {
	ListSweepRecordsPage(ctx context.Context, chain string, limit int, beforeID int64) ([]cryptopay.SweepRecord, error)
}

// parseSweepRecordsQuery reads the optional paging parameters. Absent values
// keep the old behaviour (the 100 newest rows). Present values must be in
// range, so a malformed cursor fails loudly instead of silently restarting
// the list from the top.
func parseSweepRecordsQuery(c *gin.Context) (limit int, beforeID int64, ok bool) {
	limit = cryptopay.SweepRecordsDefaultLimit
	if raw := c.Query("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > cryptopay.SweepRecordsMaxLimit {
			response.BadRequest(c, "invalid limit")
			return 0, 0, false
		}
		limit = parsed
	}
	if raw := c.Query("before_id"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 1 {
			response.BadRequest(c, "invalid before_id")
			return 0, 0, false
		}
		beforeID = parsed
	}
	return limit, beforeID, true
}

// serveSweepRecords answers with a plain array under "data", as the endpoint
// always did, so an already-open browser tab keeps working.
func serveSweepRecords(c *gin.Context, pager sweepRecordPager) {
	limit, beforeID, ok := parseSweepRecordsQuery(c)
	if !ok {
		return
	}
	records, err := pager.ListSweepRecordsPage(c.Request.Context(), c.Query("chain"), limit, beforeID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if records == nil {
		records = []cryptopay.SweepRecord{}
	}
	c.JSON(http.StatusOK, gin.H{"data": records})
}
