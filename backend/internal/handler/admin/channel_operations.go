package admin

import (
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func operationsInt(c *gin.Context, key string, def int64) (int64, error) {
	if c.Query(key) == "" {
		return def, nil
	}
	return strconv.ParseInt(c.Query(key), 10, 64)
}
func (h *ChannelHandler) OperationsCatalog(c *gin.Context) {
	if !middleware.IsPrimaryAdmin(c) {
		response.Forbidden(c, "Primary administrator access required")
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.ErrorFrom(c, service.ErrChannelOperationsValidation)
		return
	}
	page, e1 := operationsInt(c, "page", 1)
	size, e2 := operationsInt(c, "page_size", 20)
	upPage, e3 := operationsInt(c, "upstream_page", 1)
	up, e4 := operationsInt(c, "upstream_id", 0)
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil {
		response.ErrorFrom(c, service.ErrChannelOperationsValidation)
		return
	}
	out, err := h.channelService.OperationsCatalog(c.Request.Context(), id, service.ChannelOperationsQuery{Search: c.Query("search"), Page: int(page), PageSize: int(size), UpstreamPage: int(upPage), UpstreamSearch: c.Query("upstream_search"), Membership: c.DefaultQuery("membership", "all"), UpstreamID: up})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, out)
}
func (h *ChannelHandler) MutateOperations(c *gin.Context) {
	if !middleware.IsPrimaryAdmin(c) {
		response.Forbidden(c, "Primary administrator access required")
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.ErrorFrom(c, service.ErrChannelOperationsValidation)
		return
	}
	var req service.ChannelOperationsMutation
	if c.ShouldBindJSON(&req) != nil {
		response.ErrorFrom(c, service.ErrChannelOperationsValidation)
		return
	}
	if err := service.ValidateChannelOperationsMutation(&req); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	middleware.SetAuditAction(c, "channel_operations."+req.Action)
	middleware.SetAuditExtra(c, map[string]any{"source": "channel_operations", "config_version": *req.Revision, "confirm": req.Confirm})
	revision, err := h.channelService.MutateOperations(c.Request.Context(), id, req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"revision": revision})
}
func (h *ChannelHandler) OperationsSnapshot(c *gin.Context) {
	if !middleware.IsPrimaryAdmin(c) {
		response.Forbidden(c, "Primary administrator access required")
		return
	}
	channel, e0 := operationsInt(c, "channel_id", 0)
	up, e1 := operationsInt(c, "upstream_id", 0)
	account, e2 := operationsInt(c, "account_id", 0)
	group, e3 := operationsInt(c, "group_id", 0)
	page, e4 := operationsInt(c, "page", 1)
	size, e5 := operationsInt(c, "page_size", 20)
	before, e6 := operationsInt(c, "quality_before", 0)
	from, e7 := time.Parse(time.RFC3339, c.Query("from"))
	until, e8 := time.Parse(time.RFC3339, c.Query("until"))
	if e0 != nil || e1 != nil || e2 != nil || e3 != nil || e4 != nil || e5 != nil || e6 != nil || e7 != nil || e8 != nil {
		response.ErrorFrom(c, service.ErrChannelOperationsValidation)
		return
	}
	f := service.ChannelOperationsFilter{ChannelID: channel, UpstreamID: up, AccountID: account, GroupID: group, From: from, Until: until, Model: c.Query("model"), ModelScope: c.DefaultQuery("model_scope", "public"), Dimension: c.DefaultQuery("dimension", "channel"), Page: int(page), PageSize: int(size), QualityBefore: before}
	out, err := h.channelService.OperationsSnapshot(c.Request.Context(), f)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, out)
}
