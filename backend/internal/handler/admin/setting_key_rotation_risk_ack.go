package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// GetKeyRotationRiskAckSettings 返回密钥轮换「风险确认跳过」的开关与有效期。
// 这两个值来自 settings 表（security.key_rotation_risk_ack_*），未配置时返回默认值
// （开启 / 24 小时）。
func (h *SettingHandler) GetKeyRotationRiskAckSettings(c *gin.Context) {
	settings, err := h.settingService.GetKeyRotationRiskAckSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}

// UpdateKeyRotationRiskAckSettings 修改风险确认跳过策略。
// 关闭后：已有确认立即不再参与门禁评估，用户也不能再写新确认（403
// KEY_ROTATION_RISK_ACK_DISABLED）；hours 限定 1..720。
func (h *SettingHandler) UpdateKeyRotationRiskAckSettings(c *gin.Context) {
	if !middleware.EnforceStepUp(c, h.totpService, h.userService, h.settingService) {
		return
	}
	var settings service.KeyRotationRiskAckSettings
	if err := c.ShouldBindJSON(&settings); err != nil {
		response.BadRequest(c, "Invalid key rotation risk ack settings")
		return
	}
	normalized, err := h.settingService.SetKeyRotationRiskAckSettings(c.Request.Context(), settings)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, normalized)
}
