// Package routes: crypto deposit channel endpoints.
//
// Everything here is additive and gated by the channel config
// (crypto.config.enabled); disabled deployments expose nothing new.
package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// RegisterCryptoRoutes registers the user-facing crypto deposit endpoints.
func RegisterCryptoRoutes(
	v1 *gin.RouterGroup,
	cryptoHandler *handler.CryptoHandler,
	depositHandler *handler.CryptoDepositHandler,
	jwtAuth middleware.JWTAuthMiddleware,
	settingService *service.SettingService,
	panelRateLimiter *middleware.PanelRateLimiter,
) {
	if cryptoHandler == nil {
		return
	}
	authenticated := v1.Group("/crypto")
	authenticated.Use(gin.HandlerFunc(jwtAuth))
	authenticated.Use(middleware.BackendModeUserGuard(settingService))
	authenticated.Use(panelRateLimiter.Global())
	{
		authenticated.GET("/deposit-addresses", cryptoHandler.GetDepositAddresses)
		if depositHandler != nil {
			authenticated.GET("/deposit-info", depositHandler.GetDepositInfo)
			authenticated.GET("/deposit-history", depositHandler.GetDepositHistory)
		}
	}
}

// RegisterCryptoAdminRoutes registers the admin crypto management endpoints,
// including channel configuration (chains, RPC endpoints, thresholds).
func RegisterCryptoAdminRoutes(admin *gin.RouterGroup, cryptoHandler *handler.CryptoHandler) {
	if cryptoHandler == nil {
		return
	}
	crypto := admin.Group("/crypto")
	{
		crypto.GET("/config", cryptoHandler.AdminGetConfig)
		crypto.PUT("/config", cryptoHandler.AdminUpdateConfig)
		crypto.GET("/addresses", cryptoHandler.AdminListAddresses)
		crypto.POST("/addresses/:id/status", cryptoHandler.AdminSetAddressStatus)
		crypto.GET("/sweep/plan", cryptoHandler.AdminSweepPlan)
		crypto.GET("/sweep/records", cryptoHandler.AdminSweepRecords)
		crypto.GET("/deposits", cryptoHandler.AdminListDeposits)
	}
}
