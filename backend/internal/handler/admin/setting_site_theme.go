package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// UpdateSiteThemeRequest is a partial update: omitted or null fields keep their stored value.
type UpdateSiteThemeRequest struct {
	Primary     *string  `json:"theme_primary"`
	Neutral     *string  `json:"theme_neutral"`
	Radius      *float64 `json:"theme_radius"`
	Font        *string  `json:"theme_font"`
	DefaultMode *string  `json:"theme_default_mode"`
}

// GetSiteTheme returns the stored site theme.
// GET /api/v1/admin/settings/theme
func (h *SettingHandler) GetSiteTheme(c *gin.Context) {
	theme, err := h.settingService.GetSiteTheme(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, theme)
}

// UpdateSiteTheme changes the site theme. Only the primary administrator may write it.
// PUT /api/v1/admin/settings/theme
func (h *SettingHandler) UpdateSiteTheme(c *gin.Context) {
	if !middleware.IsPrimaryAdmin(c) {
		response.Forbidden(c, "Primary administrator access required")
		return
	}
	var req UpdateSiteThemeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	theme, err := h.settingService.UpdateSiteTheme(c.Request.Context(), service.SiteThemePatch{
		Primary:     req.Primary,
		Neutral:     req.Neutral,
		Radius:      req.Radius,
		Font:        req.Font,
		DefaultMode: req.DefaultMode,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, theme)
}
