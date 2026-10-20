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
	authenticated.Use(cryptoHandler.CryptoUserAccess)
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
	crypto.Use(cryptoHandler.CryptoAdminSecurity)
	{
		crypto.GET("/security/status", cryptoHandler.AdminCryptoSecurityStatus)
		crypto.POST("/security/setup", cryptoHandler.AdminCryptoSecuritySetup)
		crypto.POST("/security/enable", cryptoHandler.AdminCryptoSecurityEnable)
		crypto.GET("/config", cryptoHandler.AdminGetConfig)
		crypto.PUT("/config", cryptoHandler.AdminUpdateConfig)
		crypto.GET("/addresses", cryptoHandler.AdminListAddresses)
		crypto.POST("/addresses/:id/status", cryptoHandler.AdminSetAddressStatus)
		crypto.GET("/sweep/plan", cryptoHandler.AdminSweepPlan)
		crypto.POST("/sweep/run", cryptoHandler.AdminRunSweep)
		crypto.GET("/sweep/records", cryptoHandler.AdminSweepRecords)
		crypto.GET("/deposits", cryptoHandler.AdminListDeposits)
		crypto.GET("/signer/status", cryptoHandler.AdminSignerStatus)
		crypto.POST("/signer/unlock", cryptoHandler.AdminUnlockSigner)
		crypto.POST("/signer/export-mnemonics", cryptoHandler.AdminExportMnemonics)
		crypto.POST("/signer/hot-mnemonic", cryptoHandler.AdminImportHotMnemonic)
		crypto.GET("/pool", cryptoHandler.AdminPoolStatus)
		crypto.GET("/hot-wallet", cryptoHandler.AdminHotWalletStatus)
		crypto.GET("/hot-wallet/address", cryptoHandler.AdminHotWalletAddress)
		crypto.GET("/deposits.csv", cryptoHandler.AdminExportDeposits)
		crypto.POST("/rpc/test", cryptoHandler.AdminTestRPC)
		crypto.GET("/rpc/candidates", cryptoHandler.AdminChainlistCandidates)
		crypto.POST("/rpc/auto-pick", cryptoHandler.AdminAutoPickRPC)
	}
}
