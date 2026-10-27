package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/gin-gonic/gin"
)

func registerAccountOperationsRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	if v := h.Admin.Serverless; v != nil {
		r := admin.Group("/serverless")
		r.GET("", v.Get)
		r.PUT("", v.RequireStepUp, v.Save)
	}
	if v := h.Admin.ExcelGrant; v != nil {
		r := admin.Group("/excel-grants")
		r.GET("", v.List)
		r.PUT("/:id", v.Save)
		r.POST("/:id/authorize", v.Authorize)
		r.POST("/:id/callback", v.Callback)
	}
	if v := h.Admin.OpsAutomation; v != nil {
		r := admin.Group("/ops-automation")
		r.GET("", v.Get)
		r.PUT("", v.Save)
		r.GET("/reasoning", v.Plans)
		r.PUT("/reasoning", v.SavePlan)
		r.POST("/reasoning/:id/run", v.RunPlan)
		r.GET("/reasoning/:id/ownership", v.Ownership)
		r.POST("/reasoning/:id/ownership", v.Ownership)
	}
	if v := h.Admin.CredentialGuard; v != nil {
		r := admin.Group("/credential-guard")
		r.GET("", v.List)
		r.PUT("/:id", v.Save)
		r.POST("/:id/probe", v.Probe)
		r.POST("/:id/relogin", v.Relogin)
	}
	if h.Admin.AccountOps == nil {
		return
	}
	v := h.Admin.AccountOps
	r := admin.Group("/account-ops")
	r.GET("/config", v.GetConfig)
	r.PUT("/config", v.RequireStepUp, v.SaveConfig)
	r.PUT("/notification-settings", v.SaveNotificationSettings)
	r.GET("/alerts", v.List)
	r.GET("/balance-accounts", v.BalanceAccounts)
	r.GET("/threshold-accounts", v.ThresholdAccounts)
	r.GET("/groups", v.Groups)
	r.PUT("/groups", v.SaveGroups)
	r.PUT("/webhooks/:id", v.RequireStepUp, v.SaveWebhook)
	r.DELETE("/webhooks/:id", v.RequireStepUp, v.DeleteWebhook)
	r.POST("/webhooks/:id/test", v.RequireStepUp, v.TestWebhook)
	r.PUT("/rules/batch", v.SaveRulesBatch)
	r.PUT("/rules/:id", v.SaveRule)
	r.DELETE("/rules/:id", v.DeleteRule)
}

func registerMihomoRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	if h.Admin.Mihomo == nil {
		return
	}
	v := h.Admin.Mihomo
	r := admin.Group("/system/mihomo")
	r.GET("", v.GetMihomo)
	r.POST("", v.ManageMihomo)
	r.PUT("/download-mode", v.SetMihomoDownloadMode)
	r.PUT("/exposure", v.SetMihomoExposure)
	r.POST("/nodes/:name/test", v.TestMihomoNode)
	r.POST("/nodes/:name/quality-check", v.CheckMihomoNodeQuality)
	r.POST("/nodes/static-proxies", v.AddMihomoNodesAsStaticProxies)
	r.POST("/nodes/static-proxies/remove", v.RemoveMihomoNodesAsStaticProxies)
}
