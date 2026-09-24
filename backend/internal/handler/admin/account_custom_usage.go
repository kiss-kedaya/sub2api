package admin

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// CustomUsageHandler deliberately shares the existing billing probe repository.
// Route construction creates exactly one cache per server, not one per request.
type CustomUsageHandler struct{ usage *service.CustomUsageService }

// NewCustomUsageHandler connects the dedicated admin endpoints without new Wire providers.
func (h *AccountHandler) NewCustomUsageHandler() *CustomUsageHandler {
	if h == nil {
		return &CustomUsageHandler{usage: service.NewCustomUsageService(nil)}
	}
	return &CustomUsageHandler{usage: h.upstreamBillingProbe.NewCustomUsageService()}
}
func customUsageID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid account ID")
		return 0, false
	}
	return id, true
}
func customUsageBind(c *gin.Context, target any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32*1024)
	dec := json.NewDecoder(c.Request.Body)
	if dec.Decode(target) != nil {
		response.BadRequest(c, "invalid custom usage request")
		return false
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		response.BadRequest(c, "invalid custom usage request")
		return false
	}
	return true
}

// GetConfig never exposes custom query credentials.
func (h *CustomUsageHandler) GetConfig(c *gin.Context) {
	id, ok := customUsageID(c)
	if !ok {
		return
	}
	data, err := h.usage.GetConfig(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, data)
}

// PutConfig stores a validated public config and private credentials atomically.
func (h *CustomUsageHandler) PutConfig(c *gin.Context) {
	id, ok := customUsageID(c)
	if !ok {
		return
	}
	var input service.CustomUsageConfig
	if !customUsageBind(c, &input) {
		return
	}
	data, err := h.usage.PutConfig(c.Request.Context(), id, input)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, data)
}

// Query previews a draft without saving it; errors are fixed server-side codes.
func (h *CustomUsageHandler) Query(c *gin.Context) {
	id, ok := customUsageID(c)
	if !ok {
		return
	}
	var input struct {
		Force  bool                       `json:"force"`
		Config *service.CustomUsageConfig `json:"config"`
	}
	if !customUsageBind(c, &input) {
		return
	}
	data, err := h.usage.Query(c.Request.Context(), id, input.Force, input.Config)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, data)
}

// Batch returns only cached snapshots and config summaries, never triggers HTTP.
func (h *CustomUsageHandler) Batch(c *gin.Context) {
	var input struct {
		AccountIDs []int64 `json:"account_ids"`
	}
	if !customUsageBind(c, &input) {
		return
	}
	if len(input.AccountIDs) == 0 || len(input.AccountIDs) > service.CustomUsageMaxBatchSize {
		response.BadRequest(c, "account_ids must contain between 1 and 50 items")
		return
	}
	for _, id := range input.AccountIDs {
		if id <= 0 {
			response.BadRequest(c, "invalid account ID")
			return
		}
	}
	data, err := h.usage.Batch(c.Request.Context(), input.AccountIDs)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"items": data})
}
