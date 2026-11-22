package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"

	"github.com/gin-gonic/gin"
)

// GetAdminAuthProbeSettings 管理端 401/403 探测告警配置。
// GET /api/v1/admin/settings/admin-auth-probe
func (h *SettingHandler) GetAdminAuthProbeSettings(c *gin.Context) {
	settings, err := h.settingService.GetAdminAuthProbeSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}

// UpdateAdminAuthProbeSettingsRequest 更新管理端 401/403 探测告警配置请求。
type UpdateAdminAuthProbeSettingsRequest struct {
	Enabled         *bool `json:"enabled"`
	Threshold       *int  `json:"threshold"`
	DistinctPaths   *int  `json:"distinct_paths"`
	WindowMinutes   *int  `json:"window_minutes"`
	CooldownMinutes *int  `json:"cooldown_minutes"`
}

// UpdateAdminAuthProbeSettings 更新管理端 401/403 探测告警配置。
// 未传字段沿用当前值，避免旧客户端整表单保存时把默认值覆盖成 0。
// PUT /api/v1/admin/settings/admin-auth-probe
func (h *SettingHandler) UpdateAdminAuthProbeSettings(c *gin.Context) {
	var req UpdateAdminAuthProbeSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	current, err := h.settingService.GetAdminAuthProbeSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	next := *current
	if req.Enabled != nil {
		next.Enabled = *req.Enabled
	}
	if req.Threshold != nil {
		next.Threshold = *req.Threshold
	}
	if req.DistinctPaths != nil {
		next.DistinctPaths = *req.DistinctPaths
	}
	if req.WindowMinutes != nil {
		next.WindowMinutes = *req.WindowMinutes
	}
	if req.CooldownMinutes != nil {
		next.CooldownMinutes = *req.CooldownMinutes
	}

	if err := h.settingService.SetAdminAuthProbeSettings(c.Request.Context(), &next); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	updated, err := h.settingService.GetAdminAuthProbeSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, updated)
}
