package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/ipnotice"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

// GetGatewayIPNotice returns only the administrator-owned IP notice setting.
func (h *SettingHandler) GetGatewayIPNotice(c *gin.Context) {
	settings, err := h.settingService.GetGatewayIPNotice(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}

// UpdateGatewayIPNotice changes the IP policy without editing accounts or keys.
func (h *SettingHandler) UpdateGatewayIPNotice(c *gin.Context) {
	if !middleware.EnforceStepUp(c, h.totpService, h.userService, h.settingService) {
		return
	}
	var settings ipnotice.Settings
	if err := c.ShouldBindJSON(&settings); err != nil {
		response.BadRequest(c, "Invalid IP notice settings")
		return
	}
	if err := h.settingService.SetGatewayIPNotice(c.Request.Context(), settings); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	normalized, err := settings.Normalize()
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, normalized)
}
