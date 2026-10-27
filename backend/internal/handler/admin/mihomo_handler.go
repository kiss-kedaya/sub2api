package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/mihomo"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"net/http"
)

type MihomoHandler struct {
	kernel  *mihomo.Manager
	checker *service.MihomoProxyService
}

func NewMihomoHandler(kernel *mihomo.Manager, checker *service.MihomoProxyService) *MihomoHandler {
	return &MihomoHandler{kernel, checker}
}
func (h *MihomoHandler) guard(c *gin.Context) bool {
	if !middleware.IsPrimaryAdmin(c) {
		response.Forbidden(c, "Only the primary administrator may manage outbound proxies")
		return false
	}
	if h.kernel == nil {
		response.Error(c, 503, "Managed proxy unavailable")
		return false
	}
	wanted := c.GetHeader("X-Mihomo-Host")
	if wanted != "" && wanted != h.kernel.HostID() {
		response.Error(c, 409, "This request reached a different server; retry the selected server")
		return false
	}
	if c.Request.Method != http.MethodGet && wanted == "" {
		response.BadRequest(c, "Select the target server before making changes")
		return false
	}
	return true
}
func (h *MihomoHandler) GetMihomo(c *gin.Context) {
	if h.guard(c) {
		response.Success(c, h.kernel.Status())
	}
}
func (h *MihomoHandler) ManageMihomo(c *gin.Context) {
	if !h.guard(c) {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 256<<10)
	var req struct {
		Name           string                `json:"name"`
		Action         string                `json:"action"`
		Subscriptions  []string              `json:"subscriptions"`
		DynamicProxies []string              `json:"dynamic_proxies"`
		Append         bool                  `json:"append"`
		CountryFilter  *mihomo.CountryFilter `json:"country_filter"`
		ConfirmStop    bool                  `json:"confirm_stop"`
	}
	if c.ShouldBindJSON(&req) != nil {
		response.BadRequest(c, "Invalid managed proxy request")
		return
	}
	if req.Action == "stop" && !req.ConfirmStop {
		response.BadRequest(c, "Stopping the kernel interrupts its proxy connections; explicit confirmation is required")
		return
	}
	if err := h.kernel.SubmitSourceManagement(req.Action, req.Subscriptions, req.DynamicProxies, req.Append, req.Name, req.CountryFilter); err != nil {
		response.Error(c, 409, err.Error())
		return
	}
	middleware.SetAuditAction(c, "admin.mihomo.manage")
	response.Success(c, h.kernel.Status())
}
func (h *MihomoHandler) SetMihomoDownloadMode(c *gin.Context) {
	if !h.guard(c) {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	var req struct {
		Mode *mihomo.SubscriptionDownloadMode `json:"mode"`
	}
	if c.ShouldBindJSON(&req) != nil || req.Mode == nil {
		response.BadRequest(c, "Download mode is required")
		return
	}
	if err := h.kernel.SetSubscriptionDownloadMode(c.Request.Context(), *req.Mode); err != nil {
		response.Error(c, 409, err.Error())
		return
	}
	middleware.SetAuditAction(c, "admin.mihomo.download_mode.update")
	response.Success(c, h.kernel.Status())
}
func (h *MihomoHandler) SetMihomoExposure(c *gin.Context) {
	if !h.guard(c) {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	var req mihomo.ManagedExposure
	if c.ShouldBindJSON(&req) != nil {
		response.BadRequest(c, "Invalid private network settings")
		return
	}
	if err := h.kernel.SetExposure(c.Request.Context(), req); err != nil {
		response.Error(c, 409, err.Error())
		return
	}
	middleware.SetAuditAction(c, "admin.mihomo.network.update")
	response.Success(c, h.kernel.Status())
}
func (h *MihomoHandler) TestMihomoNode(c *gin.Context) {
	if !h.guard(c) {
		return
	}
	result, err := h.checker.TestMihomoNode(c.Request.Context(), h.kernel, c.Param("name"))
	if !response.ErrorFrom(c, err) {
		response.Success(c, result)
	}
}
func (h *MihomoHandler) CheckMihomoNodeQuality(c *gin.Context) {
	if !h.guard(c) {
		return
	}
	result, err := h.checker.CheckMihomoNodeQuality(c.Request.Context(), h.kernel, c.Param("name"))
	if !response.ErrorFrom(c, err) {
		response.Success(c, result)
	}
}
func (h *MihomoHandler) AddMihomoNodesAsStaticProxies(c *gin.Context) {
	if !h.guard(c) {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	var req struct {
		Names []string `json:"names" binding:"required,min=1,max=100"`
	}
	if c.ShouldBindJSON(&req) != nil {
		response.BadRequest(c, "Select between 1 and 100 nodes")
		return
	}
	result, err := h.checker.CreateMihomoNodeProxies(c.Request.Context(), h.kernel, req.Names)
	if response.ErrorFrom(c, err) {
		return
	}
	summaries := func(rows []service.Proxy) []gin.H {
		out := make([]gin.H, 0, len(rows))
		for _, p := range rows {
			out = append(out, gin.H{"id": p.ID, "name": p.Name, "host": p.Host, "port": p.Port})
		}
		return out
	}
	middleware.SetAuditAction(c, "admin.mihomo.static_proxies.import")
	response.Success(c, gin.H{"created": summaries(result.Created), "existing": summaries(result.Existing), "failed": result.Failed})
}
