// Package routes provides HTTP route registration and handlers.
package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// RegisterAdminRoutes 注册管理员路由。
//
// adminAuthProbes 为可选的 401/403 探测监视器（variadic 保持既有测试/调用方
// 签名不变）：传入时挂到 adminAuth 之前做轻量计数，不传时行为与之前完全一致。
func RegisterAdminRoutes(
	v1 *gin.RouterGroup,
	h *handler.Handlers,
	adminAuth middleware.AdminAuthMiddleware,
	auditLog middleware.AuditLogMiddleware,
	stepUpAuth middleware.StepUpAuthMiddleware,
	settingService *service.SettingService,
	panelRateLimiter *middleware.PanelRateLimiter,
	adminAuthProbes ...*service.AdminAuthProbeMonitor,
) {
	// 插件 UI 使用短时能力 URL，仅提供经过安装校验的静态资源。
	v1.GET("/plugin-ui/:token/*path", h.Admin.Plugin.ServeUIAsset)

	admin := v1.Group("/admin")
	var adminAuthProbe *service.AdminAuthProbeMonitor
	if len(adminAuthProbes) > 0 {
		adminAuthProbe = adminAuthProbes[0]
	}
	// 401/403 探测计数必须挂在 adminAuth 之前：先 c.Next() 才能拿到 Abort 后的状态码。
	// 它只做 Redis 计数，阈值命中后复用 ops_alert 写事件，不改变鉴权结果。
	if adminAuthProbe != nil {
		admin.Use(middleware.AdminAuthProbeMonitor(adminAuthProbe))
	}
	admin.Use(gin.HandlerFunc(adminAuth))
	// 面板全局按用户限流（默认管理员豁免，可在系统设置中关闭豁免）
	admin.Use(panelRateLimiter.Global())
	admin.Use(middleware.TablePagination(settingService))
	// 审计中间件挂在认证之后：所有管理面变更类操作 + 敏感读取入审计日志
	admin.Use(gin.HandlerFunc(auditLog))
	admin.Use(middleware.AdminComplianceGuard(settingService))
	// 集中注册表驱动的 step-up 门控：敏感资源（含 GET）统一要求真人 2FA。
	// 机器凭据已在 adminAuth 里默认拒绝，这里只约束真人会话；未归类路由
	// fail-closed 同样要求 step-up。禁止在路由文件里散落分类常量。
	admin.Use(adminStepUpRouteGuard(stepUpAuth))
	if h.ModelPlaza != nil {
		admin.GET("/model-plaza", h.ModelPlaza.AdminGet)
	}
	// Read-only refresh of the models.dev metadata registry used by /v1/models.
	// It never reads or writes pricing/billing configuration.
	if h.Gateway != nil {
		admin.POST("/models/metadata/sync", h.Gateway.SyncModelMetadata)
	}
	registerAdminTicketRoutes(admin, h.Ticket)
	registerAccountOperationsRoutes(admin, h)
	registerMihomoRoutes(admin, h)
	if h.Admin.IPRisk != nil {
		risk := admin.Group("/settings/ip-risk")
		risk.GET("", h.Admin.IPRisk.GetIPRisk)
		risk.PUT("", h.Admin.IPRisk.SaveIPRisk)
		risk.GET("/events", h.Admin.IPRisk.GetIPRiskEvents)
		risk.DELETE("/events", h.Admin.IPRisk.ClearIPRiskEvents)
		risk.GET("/bans", h.Admin.IPRisk.GetIPRiskBans)
		risk.POST("/unban", h.Admin.IPRisk.UnbanIPRisk)
		risk.GET("/anonymous-bans", h.Admin.IPRisk.GetAnonymousAPIBans)
		risk.POST("/anonymous-unban", h.Admin.IPRisk.UnbanAnonymousAPI)
		risk.POST("/check", h.Admin.IPRisk.CheckIPRisk)
		admin.GET("/settings/ip-audit", h.Admin.IPRisk.GetIPAudit)
		admin.POST("/settings/ip-audit/release", h.Admin.IPRisk.ReleaseIPAudit)
	}

	if h.Admin.RequestCapture != nil {
		capture := admin.Group("/request-captures")
		capture.GET("/config", h.Admin.RequestCapture.GetConfig)
		capture.PUT("/config", h.Admin.RequestCapture.UpdateConfig)
		capture.PUT("/small/config", h.Admin.RequestCapture.SmallUpdateConfig)
		small := capture.Group("/small", h.Admin.RequestCapture.SmallGate)
		small.GET("/stats", h.Admin.RequestCapture.SmallStats)
		small.GET("", h.Admin.RequestCapture.SmallList)
		small.GET("/:record", h.Admin.RequestCapture.SmallDetail)
		small.DELETE("", h.Admin.RequestCapture.SmallClear)
		capture.Use(h.Admin.RequestCapture.Gate)
		capture.GET("/sensitive-access", h.Admin.RequestCapture.AuthorizeSensitiveRead)
		capture.GET("", h.Admin.RequestCapture.List)
		capture.POST("", h.Admin.RequestCapture.Create)
		capture.POST("/:task/stop", h.Admin.RequestCapture.Stop)
		capture.DELETE("/:task", h.Admin.RequestCapture.Delete)
		capture.GET("/:task/requests", h.Admin.RequestCapture.Records)
		capture.GET("/:task/requests/:record", h.Admin.RequestCapture.Detail)
		capture.GET("/:task/requests/:record/content/:part", h.Admin.RequestCapture.Content)
		capture.GET("/:task/export", h.Admin.RequestCapture.Export)
		capture.GET("/:task/requests/:record/export", h.Admin.RequestCapture.Export)
	}

	if h.Admin.UpstreamMonitor != nil {
		monitor := admin.Group("/upstream-monitor")
		monitor.GET("/overview", h.Admin.UpstreamMonitor.Overview)
		monitor.GET("/accounts", h.Admin.UpstreamMonitor.Accounts)
		monitor.GET("/accounts/:id/history", h.Admin.UpstreamMonitor.AccountHistory)
		monitor.GET("/rate-changes", h.Admin.UpstreamMonitor.RateChanges)
		monitor.GET("/trend", h.Admin.UpstreamMonitor.Trend)
		monitor.GET("/alerts", h.Admin.UpstreamMonitor.Alerts)
		monitor.POST("/alerts/:id/ack", h.Admin.UpstreamMonitor.AckAlert)
		monitor.GET("/alert-rules", h.Admin.UpstreamMonitor.ListAlertRules)
		monitor.PUT("/alert-rules", h.Admin.UpstreamMonitor.SaveAlertRule)
		monitor.DELETE("/alert-rules/:id", h.Admin.UpstreamMonitor.DeleteAlertRule)
		monitor.GET("/export", h.Admin.UpstreamMonitor.Export)
		monitor.POST("/sample", h.Admin.UpstreamMonitor.Sample)
	}

	if h.Admin.UpstreamChannel != nil {
		channel := admin.Group("/upstream-monitor")
		channel.GET("/channels", h.Admin.UpstreamChannel.List)
		// 渠道凭证/中转站点配置：创建与改写等于写入或替换上游访问凭据，
		// 与"账号导出/API Key 明文回读"同级，要求 step-up 2FA（机器凭据一律 403）。
		channel.POST("/channels", gin.HandlerFunc(stepUpAuth), h.Admin.UpstreamChannel.Create)
		channel.GET("/channels/:id", h.Admin.UpstreamChannel.Get)
		channel.PUT("/channels/:id", gin.HandlerFunc(stepUpAuth), h.Admin.UpstreamChannel.Update)
		channel.DELETE("/channels/:id", gin.HandlerFunc(stepUpAuth), h.Admin.UpstreamChannel.Delete)
		channel.POST("/channels/:id/login", h.Admin.UpstreamChannel.Login)
		channel.GET("/channels/:id/balance", h.Admin.UpstreamChannel.Balance)
		channel.GET("/announcements", h.Admin.UpstreamChannel.Announcements)
		channel.POST("/channels/:id/announcements/sync", h.Admin.UpstreamChannel.SyncAnnouncements)
		channel.GET("/channels/:id/api-keys", h.Admin.UpstreamChannel.APIKeys)
		channel.GET("/channels/:id/api-key-groups", h.Admin.UpstreamChannel.APIKeyGroups)
		// 上游子密钥的创建/修改/删除可直接产出可用的上游调用凭据：要求 step-up 2FA。
		channel.POST("/channels/:id/api-keys", gin.HandlerFunc(stepUpAuth), h.Admin.UpstreamChannel.CreateAPIKey)
		channel.PUT("/channels/:id/api-keys/:key_id", gin.HandlerFunc(stepUpAuth), h.Admin.UpstreamChannel.UpdateAPIKey)
		channel.DELETE("/channels/:id/api-keys/:key_id", gin.HandlerFunc(stepUpAuth), h.Admin.UpstreamChannel.DeleteAPIKey)
		channel.POST("/channels/:id/api-keys/:key_id/reveal", gin.HandlerFunc(stepUpAuth), h.Admin.UpstreamChannel.RevealAPIKey)
		channel.GET("/channels/:id/recharge", h.Admin.UpstreamChannel.RechargeInfo)
		// 上游账户充值/兑换码/订阅采购会直接消耗或新增上游额度（资金操作）：要求 step-up 2FA。
		channel.POST("/channels/:id/recharge", gin.HandlerFunc(stepUpAuth), h.Admin.UpstreamChannel.CreateRecharge)
		channel.POST("/channels/:id/redeem", gin.HandlerFunc(stepUpAuth), h.Admin.UpstreamChannel.Redeem)
		channel.GET("/channels/:id/subscriptions", h.Admin.UpstreamChannel.SubscriptionInfo)
		channel.GET("/channels/:id/subscriptions/usage", h.Admin.UpstreamChannel.SubscriptionUsage)
		channel.POST("/channels/:id/subscriptions", gin.HandlerFunc(stepUpAuth), h.Admin.UpstreamChannel.CreateSubscription)
	}
	// 受限密钥的账户操作走独立资源校验，普通管理员保持原处理器。
	admin.Use(h.Admin.Account.ScopedAdminKeyAccounts)
	{
		// 部署与运营合规确认
		registerAdminComplianceRoutes(admin, h)

		// 仪表盘
		registerDashboardRoutes(admin, h)

		// 用户管理
		registerUserManagementRoutes(admin, h, stepUpAuth)

		// 分组管理
		registerGroupRoutes(admin, h, stepUpAuth)

		// 账号管理
		registerAccountRoutes(admin, h, stepUpAuth)

		// 公告管理
		registerAnnouncementRoutes(admin, h)

		// OpenAI OAuth
		registerOpenAIOAuthRoutes(admin, h)

		// Gemini OAuth
		registerGeminiOAuthRoutes(admin, h)

		// Antigravity OAuth
		registerAntigravityOAuthRoutes(admin, h)

		// Grok OAuth
		registerGrokOAuthRoutes(admin, h)

		// 国产供应商（kimi/zhipu/deepseek）额度与余额
		registerCNProviderRoutes(admin, h)

		// 代理管理
		registerProxyRoutes(admin, h, stepUpAuth)

		// 卡密管理
		registerRedeemCodeRoutes(admin, h, stepUpAuth)

		// 优惠码管理
		registerPromoCodeRoutes(admin, h, stepUpAuth)

		// 系统设置
		registerSettingsRoutes(admin, h, stepUpAuth)

		// 数据管理
		registerDataManagementRoutes(admin, h, stepUpAuth)

		// 数据库备份恢复
		registerBackupRoutes(admin, h, stepUpAuth)

		// 运维监控（Ops）
		registerOpsRoutes(admin, h)

		// 系统管理
		registerSystemRoutes(admin, h, stepUpAuth)

		// 订阅管理
		registerSubscriptionRoutes(admin, h, stepUpAuth)

		// 使用记录管理
		registerUsageRoutes(admin, h)

		// 用户属性管理
		registerUserAttributeRoutes(admin, h)

		// 错误透传规则管理
		registerErrorPassthroughRoutes(admin, h)

		// TLS 指纹模板管理
		registerTLSFingerprintProfileRoutes(admin, h)

		// 本地进程插件管理
		registerPluginRoutes(admin, h, stepUpAuth)

		// API Key 管理
		registerAdminAPIKeyRoutes(admin, h)

		// 定时测试计划
		registerScheduledTestRoutes(admin, h)

		// 分组降智检测
		registerGroupQualityCheckRoutes(admin, h)

		// 渠道管理
		registerChannelRoutes(admin, h)

		// 渠道监控
		registerChannelMonitorRoutes(admin, h, settingService)
		registerChannelMonitorV2Routes(admin, h, settingService)

		// 风控中心
		registerContentModerationRoutes(admin, h, stepUpAuth)

		// 独立提示词输入审计
		registerPromptAuditRoutes(admin, h)

		// 邀请返利（专属用户管理）
		registerAffiliateRoutes(admin, h)

		// 链上直充（内置渠道：配置 / 地址 / 归集）
		RegisterCryptoAdminRoutes(admin, h.Crypto)

		// 操作审计日志
		registerAuditLogRoutes(admin, h, stepUpAuth)
	}
}

func registerPromptAuditRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	penalties := admin.Group("/security-penalties")
	penalties.GET("/config", h.Admin.PromptAudit.GetPenaltyConfig)
	penalties.PUT("/config", h.Admin.PromptAudit.SavePenaltyConfig)
	penalties.GET("/events", h.Admin.PromptAudit.AdminPenaltyEvents)
	penalties.POST("/:user_id/release", h.Admin.PromptAudit.ReleasePenalty)
	promptAudit := admin.Group("/prompt-audit")
	{
		promptAudit.GET("/config", h.Admin.PromptAudit.GetConfig)
		promptAudit.PUT("/config", h.Admin.PromptAudit.UpdateConfig)
		promptAudit.POST("/endpoints/probe", h.Admin.PromptAudit.ProbeEndpoint)
		promptAudit.GET("/runtime", h.Admin.PromptAudit.GetRuntime)
		promptAudit.GET("/events", h.Admin.PromptAudit.ListEvents)
		promptAudit.GET("/events/:id", h.Admin.PromptAudit.GetEvent)
		promptAudit.DELETE("/events/:id", h.Admin.PromptAudit.DeleteEvent)
		promptAudit.POST("/events/batch-delete", h.Admin.PromptAudit.BatchDelete)
		promptAudit.POST("/events/delete-preview", h.Admin.PromptAudit.DeletePreview)
		promptAudit.POST("/events/delete-by-filter", h.Admin.PromptAudit.DeleteByFilter)
	}
}

func registerAuditLogRoutes(admin *gin.RouterGroup, h *handler.Handlers, _ middleware.StepUpAuthMiddleware) {
	auditLogs := admin.Group("/audit-logs")
	{
		auditLogs.GET("", h.Admin.AuditLog.List)
		// 倍率配置变更的真实修改时间（来自审计日志，不要用 usage_logs 推断）
		auditLogs.GET("/group-rate-changes", h.Admin.AuditLog.GroupRateChanges)
		auditLogs.GET("/:id", h.Admin.AuditLog.Get)
		// 后台已禁止清理审计日志：固定 403，只能由 DBA 在数据库层清理
		auditLogs.POST("/clear", h.Admin.AuditLog.Clear)
	}
}

func registerAdminComplianceRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	compliance := admin.Group("/compliance")
	{
		compliance.GET("", h.Admin.Compliance.GetStatus)
		compliance.POST("/accept", h.Admin.Compliance.Accept)
	}
}

func registerContentModerationRoutes(admin *gin.RouterGroup, h *handler.Handlers, stepUpAuth middleware.StepUpAuthMiddleware) {
	risk := admin.Group("/risk-control")
	{
		risk.GET("/config", h.Admin.ContentModeration.GetConfig)
		risk.PUT("/config", h.Admin.ContentModeration.UpdateConfig)
		// 测试入口会用服务端身份向任意 base_url 发请求：本身就是一个出站探测
		// 原语，和改配置同级，必须 step-up（机器凭据一律 403）。
		risk.POST("/api-keys/test", gin.HandlerFunc(stepUpAuth), h.Admin.ContentModeration.TestAPIKeys)
		risk.GET("/status", h.Admin.ContentModeration.GetStatus)
		risk.GET("/logs", h.Admin.ContentModeration.ListLogs)
		risk.POST("/users/:user_id/unban", h.Admin.ContentModeration.UnbanUser)
		risk.DELETE("/hashes", h.Admin.ContentModeration.DeleteFlaggedHash)
		risk.DELETE("/hashes/all", h.Admin.ContentModeration.ClearFlaggedHashes)
	}
}

func registerAdminAPIKeyRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	apiKeys := admin.Group("/api-keys")
	{
		apiKeys.PUT("/:id", h.Admin.APIKey.UpdateGroup)
	}
}

func registerOpsRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	ops := admin.Group("/ops")
	{
		// Realtime ops signals
		ops.GET("/concurrency", h.Admin.Ops.GetConcurrencyStats)
		ops.GET("/user-concurrency", h.Admin.Ops.GetUserConcurrencyStats)
		ops.GET("/account-availability", h.Admin.Ops.GetAccountAvailability)
		ops.GET("/realtime-traffic", h.Admin.Ops.GetRealtimeTrafficSummary)

		// Alerts (rules + events)
		ops.GET("/alert-rules", h.Admin.Ops.ListAlertRules)
		ops.POST("/alert-rules", h.Admin.Ops.CreateAlertRule)
		ops.PUT("/alert-rules/:id", h.Admin.Ops.UpdateAlertRule)
		ops.DELETE("/alert-rules/:id", h.Admin.Ops.DeleteAlertRule)
		ops.GET("/alert-events", h.Admin.Ops.ListAlertEvents)
		ops.GET("/alert-events/:id", h.Admin.Ops.GetAlertEvent)
		ops.PUT("/alert-events/:id/status", h.Admin.Ops.UpdateAlertEventStatus)
		ops.POST("/alert-silences", h.Admin.Ops.CreateAlertSilence)

		// Email notification config (DB-backed)
		ops.GET("/email-notification/config", h.Admin.Ops.GetEmailNotificationConfig)
		ops.PUT("/email-notification/config", h.Admin.Ops.UpdateEmailNotificationConfig)

		// Runtime settings (DB-backed)
		runtime := ops.Group("/runtime")
		{
			runtime.GET("/alert", h.Admin.Ops.GetAlertRuntimeSettings)
			runtime.PUT("/alert", h.Admin.Ops.UpdateAlertRuntimeSettings)
			runtime.GET("/logging", h.Admin.Ops.GetRuntimeLogConfig)
			runtime.PUT("/logging", h.Admin.Ops.UpdateRuntimeLogConfig)
			runtime.POST("/logging/reset", h.Admin.Ops.ResetRuntimeLogConfig)
		}

		// Advanced settings (DB-backed)
		ops.GET("/advanced-settings", h.Admin.Ops.GetAdvancedSettings)
		ops.PUT("/advanced-settings", h.Admin.Ops.UpdateAdvancedSettings)

		// Settings group (DB-backed)
		settings := ops.Group("/settings")
		{
			settings.GET("/metric-thresholds", h.Admin.Ops.GetMetricThresholds)
			settings.PUT("/metric-thresholds", h.Admin.Ops.UpdateMetricThresholds)
		}

		// WebSocket realtime (QPS/TPS)
		ws := ops.Group("/ws")
		{
			ws.GET("/qps", h.Admin.Ops.QPSWSHandler)
		}

		// Error logs (legacy)
		ops.GET("/errors", h.Admin.Ops.GetErrorLogs)
		ops.GET("/errors/:id", h.Admin.Ops.GetErrorLogByID)
		ops.PUT("/errors/:id/resolve", h.Admin.Ops.UpdateErrorResolution)

		// Request errors (client-visible failures)
		ops.GET("/request-errors", h.Admin.Ops.ListRequestErrors)
		ops.GET("/request-errors/:id", h.Admin.Ops.GetRequestError)
		ops.GET("/request-errors/:id/upstream-errors", h.Admin.Ops.ListRequestErrorUpstreamErrors)
		ops.PUT("/request-errors/:id/resolve", h.Admin.Ops.ResolveRequestError)

		// Bounded ingress-admission rejection aggregates.
		ops.GET("/ingress-rejections", h.Admin.Ops.ListIngressRejects)
		ops.GET("/ingress-rejections/health", h.Admin.Ops.GetIngressRejectHealth)
		ops.GET("/auth-cache-invalidation/health", h.Admin.Ops.GetAuthCacheInvalidationHealth)
		ops.GET("/live-balance-outbox/health", h.Admin.Ops.GetLiveBalanceAdjustmentOutboxHealth)
		ops.GET("/scheduler-freshness/health", h.Admin.Ops.GetSchedulerFreshnessHealth)

		// Upstream errors (independent upstream failures)
		ops.GET("/upstream-errors", h.Admin.Ops.ListUpstreamErrors)
		ops.GET("/upstream-errors/:id", h.Admin.Ops.GetUpstreamError)
		ops.PUT("/upstream-errors/:id/resolve", h.Admin.Ops.ResolveUpstreamError)

		// Request drilldown (success + error)
		ops.GET("/requests", h.Admin.Ops.ListRequestDetails)

		// Indexed system logs
		ops.GET("/system-logs", h.Admin.Ops.ListSystemLogs)
		ops.POST("/system-logs/cleanup", h.Admin.Ops.CleanupSystemLogs)
		ops.GET("/system-logs/health", h.Admin.Ops.GetSystemLogIngestionHealth)

		// Dashboard (vNext - raw path for MVP)
		ops.GET("/dashboard/snapshot-v2", h.Admin.Ops.GetDashboardSnapshotV2)
		ops.GET("/dashboard/overview", h.Admin.Ops.GetDashboardOverview)
		ops.GET("/dashboard/throughput-trend", h.Admin.Ops.GetDashboardThroughputTrend)
		ops.GET("/dashboard/latency-histogram", h.Admin.Ops.GetDashboardLatencyHistogram)
		ops.GET("/dashboard/error-trend", h.Admin.Ops.GetDashboardErrorTrend)
		ops.GET("/dashboard/error-distribution", h.Admin.Ops.GetDashboardErrorDistribution)
		ops.GET("/dashboard/openai-token-stats", h.Admin.Ops.GetDashboardOpenAITokenStats)
	}
}

func registerDashboardRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	dashboard := admin.Group("/dashboard")
	{
		dashboard.GET("/snapshot-v2", h.Admin.Dashboard.GetSnapshotV2)
		dashboard.GET("/stats", h.Admin.Dashboard.GetStats)
		dashboard.GET("/realtime", h.Admin.Dashboard.GetRealtimeMetrics)
		dashboard.GET("/trend", h.Admin.Dashboard.GetUsageTrend)
		dashboard.GET("/models", h.Admin.Dashboard.GetModelStats)
		dashboard.GET("/groups", h.Admin.Dashboard.GetGroupStats)
		dashboard.GET("/api-keys-trend", h.Admin.Dashboard.GetAPIKeyUsageTrend)
		dashboard.GET("/users-trend", h.Admin.Dashboard.GetUserUsageTrend)
		dashboard.GET("/users-ranking", h.Admin.Dashboard.GetUserSpendingRanking)
		dashboard.POST("/users-usage", h.Admin.Dashboard.GetBatchUsersUsage)
		dashboard.POST("/api-keys-usage", h.Admin.Dashboard.GetBatchAPIKeysUsage)
		dashboard.GET("/user-breakdown", h.Admin.Dashboard.GetUserBreakdown)
		dashboard.POST("/aggregation/backfill", h.Admin.Dashboard.BackfillAggregation)
	}
}

func registerUserManagementRoutes(admin *gin.RouterGroup, h *handler.Handlers, stepUpAuth middleware.StepUpAuthMiddleware) {
	users := admin.Group("/users")
	{
		users.GET("", h.Admin.User.List)
		users.GET("/:id", h.Admin.User.GetByID)
		users.GET("/:id/load", h.Admin.User.GetLoad)
		// 绑定第三方登录身份等于给目标账号增加一条登录路径（对管理员账号是持久化后门），
		// 与改密码/改角色同级：要求 step-up 2FA，机器凭据一律拒绝。
		users.POST("/:id/auth-identities", gin.HandlerFunc(stepUpAuth), h.Admin.User.BindAuthIdentity)
		// 建号可携带初始余额与授权分组：由集中注册表 adminroutepolicy 的
		// step_up_required 规则统一门控（fail-closed），handler 内再按请求体
		// 是否含非零余额/授权分组做一层条件校验。不要在路由文件里另加分类常量。
		users.POST("", h.Admin.User.Create)
		users.PUT("/:id", h.Admin.User.Update)
		// 删除用户属不可逆敏感操作：要求 step-up 二次验证（机器凭据返回 403）。
		users.DELETE("/:id", gin.HandlerFunc(stepUpAuth), h.Admin.User.Delete)
		// 增减余额属资金操作：要求 step-up 二次验证（机器凭据返回 403）。
		users.POST("/:id/balance", gin.HandlerFunc(stepUpAuth), h.Admin.User.UpdateBalance)
		// 查看用户 API Key 列表会暴露 key 前缀/掩码与绑定关系：要求 step-up 二次验证
		// （机器凭据返回 403），且不属于日常维护，每次打开都重新过门控。
		users.GET("/:id/api-keys", gin.HandlerFunc(stepUpAuth), h.Admin.User.GetUserAPIKeys)
		users.GET("/:id/usage", h.Admin.User.GetUserUsage)
		users.GET("/:id/balance-history", h.Admin.User.GetBalanceHistory)
		// 改用户所属分组等于改其可用分组权限（含专属/受限分组）：要求 step-up。
		users.POST("/:id/replace-group", gin.HandlerFunc(stepUpAuth), h.Admin.User.ReplaceGroup)
		users.GET("/:id/rpm-status", h.Admin.User.GetUserRPMStatus)
		// 批量改并发/额度、批量停用账号属批量权限变更：漏挂会让一条机器凭据
		// 一次影响成批用户，与单个用户编辑同级要求 step-up 2FA。
		users.POST("/batch-concurrency", gin.HandlerFunc(stepUpAuth), h.Admin.User.BatchUpdateConcurrency)
		users.POST("/batch-limits", gin.HandlerFunc(stepUpAuth), h.Admin.User.BatchUpdateLimits)
		users.POST("/batch-disable", gin.HandlerFunc(stepUpAuth), h.Admin.User.BatchDisable)
		users.POST("/blacklist-matches", h.Admin.User.FindBlacklistedEmailUsers)
		users.GET("/:id/platform-quotas", h.Admin.User.GetUserPlatformQuotas)
		// 平台配额直接决定用户可用额度：改配额/重置配额窗口要求 step-up。
		users.PUT("/:id/platform-quotas", gin.HandlerFunc(stepUpAuth), h.Admin.User.UpdateUserPlatformQuotas)
		users.POST("/:id/platform-quotas/reset", gin.HandlerFunc(stepUpAuth), h.Admin.User.ResetUserPlatformQuotaWindow)
		// 破玻璃重置 2FA：清空目标用户的 TOTP 并撤销其全部因子的 step-up 授权，
		// 是「能降低他人账号安全强度」的高风险动作。必须真人 step-up（机器凭据
		// 一律 403），原因必填并写审计，同时邮件通知目标用户。Passkey 保留。
		users.POST("/:id/reset-2fa", gin.HandlerFunc(stepUpAuth), h.Admin.User.ResetUser2FA)

		// User attribute values
		users.GET("/:id/attributes", h.Admin.UserAttribute.GetUserAttributes)
		users.PUT("/:id/attributes", h.Admin.UserAttribute.UpdateUserAttributes)
	}
}

func registerGroupRoutes(admin *gin.RouterGroup, h *handler.Handlers, stepUpAuth middleware.StepUpAuthMiddleware) {
	groups := admin.Group("/groups")
	groups.Use(h.Admin.Group.ScopedAdminKeyGroupModels)
	{
		groups.GET("", h.Admin.Group.List)
		groups.GET("/all", h.Admin.Group.GetAll)
		groups.GET("/usage-summary", h.Admin.Group.GetUsageSummary)
		groups.GET("/capacity-summary", h.Admin.Group.GetCapacitySummary)
		groups.GET("/live-capability", h.Admin.Group.GetLiveCapability)
		groups.PUT("/sort-order", h.Admin.Group.UpdateSortOrder)
		groups.GET("/:id/model-allowlist-candidates", h.Admin.Group.GetGroupModelAllowlistCandidates)
		groups.GET("/:id/composite-routes", h.Admin.Group.ListCompositeRoutes)
		groups.POST("/:id/composite-routes", h.Admin.Group.CreateCompositeRoute)
		groups.POST("/:id/composite-routes/preview", h.Admin.Group.PreviewCompositeRoute)
		groups.PUT("/:id/composite-routes/:route_id", h.Admin.Group.UpdateCompositeRoute)
		groups.DELETE("/:id/composite-routes/:route_id", h.Admin.Group.DeleteCompositeRoute)
		groups.GET("/:id", h.Admin.Group.GetByID)
		groups.POST("", h.Admin.Group.Create)
		groups.POST("/:id/duplicate", h.Admin.Group.Duplicate)
		groups.PUT("/:id", h.Admin.Group.Update)
		groups.DELETE("/:id", h.Admin.Group.Delete)
		groups.GET("/:id/stats", h.Admin.Group.GetStats)
		groups.GET("/:id/rate-multipliers", h.Admin.Group.GetGroupRateMultipliers)
		// 分组专属倍率 = 用户实付价格：批量改价/清空会直接改钱，要求 step-up 2FA。
		// （分组默认倍率与各模态单价走 PUT /groups/:id，由 handler 按实际改动判定。）
		groups.PUT("/:id/rate-multipliers", gin.HandlerFunc(stepUpAuth), h.Admin.Group.BatchSetGroupRateMultipliers)
		groups.DELETE("/:id/rate-multipliers", gin.HandlerFunc(stepUpAuth), h.Admin.Group.ClearGroupRateMultipliers)
		groups.PUT("/:id/rpm-overrides", h.Admin.Group.BatchSetGroupRPMOverrides)
		groups.DELETE("/:id/rpm-overrides", h.Admin.Group.ClearGroupRPMOverrides)
		groups.GET("/:id/api-keys", gin.HandlerFunc(stepUpAuth), h.Admin.Group.GetGroupAPIKeys)
	}
}

func registerAccountRoutes(admin *gin.RouterGroup, h *handler.Handlers, stepUpAuth middleware.StepUpAuthMiddleware) {
	accounts := admin.Group("/accounts")
	accounts.GET("/:id/schedulable-timers", h.Admin.Account.ListSchedulableTimers)
	accounts.POST("/:id/schedulable-timers/preview", h.Admin.Account.PreviewSchedulableTimer)
	accounts.POST("/:id/schedulable-timers", h.Admin.Account.SaveSchedulableTimer)
	accounts.DELETE("/:id/schedulable-timers/:action", h.Admin.Account.CancelSchedulableTimer)

	accounts.Use(func(c *gin.Context) {
		if subject, ok := middleware.GetAuthSubjectFromContext(c); ok && subject.UserID > 0 {
			c.Request = c.Request.WithContext(service.EnsureAccountOwnerScope(c.Request.Context(), subject.UserID))
		}
		c.Next()
	})
	accounts.Use(h.Admin.Account.RequireAccountOwnerAccess)
	customUsage := h.Admin.Account.NewCustomUsageHandlerWithService(h.Admin.AccountOps.SharedCustomUsageService())
	{
		accounts.GET("/:id/custom-usage-config", customUsage.GetConfig)
		accounts.PUT("/:id/custom-usage-config", customUsage.PutConfig)
		accounts.POST("/:id/custom-usage-query", customUsage.Query)
		accounts.POST("/custom-usage-batch", customUsage.Batch)
		accounts.GET("", h.Admin.Account.List)
		accounts.GET("/upstream-billing-rates", h.Admin.Account.GetUpstreamBillingRates)
		accounts.GET("/upstream-billing-probe/settings", h.Admin.Account.GetUpstreamBillingProbeSettings)
		accounts.PUT("/upstream-billing-probe/settings", h.Admin.Account.UpdateUpstreamBillingProbeSettings)
		accounts.POST("/upstream-billing-probe/batch", h.Admin.Account.ProbeUpstreamBillingBatch)
		accounts.GET("/ollama-cloud-usage/settings", h.Admin.Account.GetOllamaCloudUsageSettings)
		accounts.PUT("/ollama-cloud-usage/settings", h.Admin.Account.UpdateOllamaCloudUsageSettings)
		accounts.GET("/:id", h.Admin.Account.GetByID)
		accounts.GET("/:id/claude/reset-credits", h.Admin.Account.ClaudeResetCredits)
		// Same protection as the Codex reset-quota route (admin auth, audit, compliance guard).
		accounts.POST("/:id/claude/reset-credits/redeem", h.Admin.Account.RedeemClaudeResetCredit)
		accounts.POST("", h.Admin.Account.Create)
		accounts.POST("/:id/duplicate", h.Admin.Account.Duplicate)
		accounts.POST("/check-mixed-channel", h.Admin.Account.CheckMixedChannel)
		accounts.POST("/import/codex-session", h.Admin.Account.ImportCodexSession)
		accounts.POST("/sync/crs", h.Admin.Account.SyncFromCRS)
		accounts.POST("/sync/crs/preview", h.Admin.Account.PreviewFromCRS)
		accounts.PUT("/:id", h.Admin.Account.Update)
		accounts.GET("/:id/model-whitelist-auto-sync", h.Admin.Account.GetModelWhitelistAutoSync)
		accounts.POST("/:id/model-whitelist-auto-sync/run", h.Admin.Account.RunModelWhitelistAutoSync)
		accounts.POST("/model-whitelist-auto-sync/run", h.Admin.Account.RunModelWhitelistAutoSyncBatch)
		accounts.GET("/:id/grok-media-eligibility", h.Admin.Account.GetGrokMediaEligibility)
		accounts.PUT("/:id/grok-media-eligibility", h.Admin.Account.UpdateGrokMediaEligibility)
		accounts.PUT("/:id/upstream-billing-probe", h.Admin.Account.SetUpstreamBillingProbeEnabled)
		accounts.POST("/:id/upstream-billing-probe", h.Admin.Account.ProbeUpstreamBilling)

		// 上游失败现场：SUB2API_DEBUG_DUMP_DIR 打开时落盘的 txt，可直接下载。
		// dump 内含客户端请求体与上游响应体（用户提示词、工具结果），与
		// request-capture 的正文读取同级：列目录保留元数据，读取正文要求 step-up 2FA。
		debugDumps := admin.Group("/debug-dumps")
		{
			debugDumps.GET("", h.Admin.DebugDump.List)
			debugDumps.GET("/:name", gin.HandlerFunc(stepUpAuth), h.Admin.DebugDump.Download)
		}
		accounts.GET("/:id/ollama-cloud-usage", h.Admin.Account.GetOllamaCloudUsage)
		accounts.PUT("/:id/ollama-cloud-usage/session", h.Admin.Account.SaveOllamaCloudUsageSession)
		accounts.DELETE("/:id/ollama-cloud-usage/session", h.Admin.Account.DeleteOllamaCloudUsageSession)
		accounts.PUT("/:id/ollama-cloud-usage/auto-refresh", h.Admin.Account.SetOllamaCloudUsageAutoRefresh)
		accounts.POST("/:id/ollama-cloud-usage/refresh", h.Admin.Account.RefreshOllamaCloudUsage)
		accounts.DELETE("/:id", h.Admin.Account.Delete)
		accounts.POST("/:id/test", h.Admin.Account.Test)
		accounts.POST("/:id/recover-state", h.Admin.Account.RecoverState)
		accounts.POST("/:id/refresh", h.Admin.Account.Refresh)
		accounts.POST("/:id/apply-oauth-credentials", h.Admin.Account.ApplyOAuthCredentials)
		accounts.POST("/:id/set-privacy", h.Admin.Account.SetPrivacy)
		accounts.POST("/:id/refresh-tier", h.Admin.Account.RefreshTier)
		accounts.GET("/:id/stats", h.Admin.Account.GetStats)
		accounts.POST("/:id/clear-error", h.Admin.Account.ClearError)
		accounts.POST("/:id/revert-proxy-fallback", h.Admin.Account.RevertProxyFallback)
		accounts.GET("/:id/usage", h.Admin.Account.GetUsage)
		accounts.GET("/:id/today-stats", h.Admin.Account.GetTodayStats)
		accounts.POST("/usage/batch", h.Admin.Account.GetBatchUsage)
		accounts.POST("/today-stats/batch", h.Admin.Account.GetBatchTodayStats)
		accounts.POST("/:id/clear-rate-limit", h.Admin.Account.ClearRateLimit)
		accounts.POST("/:id/reset-quota", h.Admin.Account.ResetQuota)
		accounts.GET("/:id/temp-unschedulable", h.Admin.Account.GetTempUnschedulable)
		accounts.DELETE("/:id/temp-unschedulable", h.Admin.Account.ClearTempUnschedulable)
		accounts.POST("/:id/schedulable", h.Admin.Account.SetSchedulable)
		accounts.POST("/models/sync-upstream-preview", h.Admin.Account.SyncUpstreamModelsPreview)
		accounts.GET("/:id/models", h.Admin.Account.GetAvailableModels)
		accounts.POST("/:id/models/sync-upstream", h.Admin.Account.SyncUpstreamModels)
		accounts.POST("/batch", h.Admin.Account.BatchCreate)
		// 账号导出泄露上游凭证原文——要求 step-up 2FA
		accounts.GET("/data", gin.HandlerFunc(stepUpAuth), h.Admin.Account.ExportData)
		// 单个账号 API Key 明文回读：与整站导出同一 step-up 2FA 门控，只回 api_key
		accounts.GET("/:id/credential", gin.HandlerFunc(stepUpAuth), h.Admin.Account.RevealAPIKey)
		accounts.POST("/data", h.Admin.Account.ImportData)
		accounts.POST("/batch-update-credentials", h.Admin.Account.BatchUpdateCredentials)
		accounts.POST("/batch-refresh-tier", h.Admin.Account.BatchRefreshTier)
		accounts.POST("/bulk-update", h.Admin.Account.BulkUpdate)
		accounts.POST("/batch-delete", h.Admin.Account.BatchDelete)
		accounts.POST("/batch-clear-error", h.Admin.Account.BatchClearError)
		accounts.POST("/batch-refresh", h.Admin.Account.BatchRefresh)

		// Antigravity 默认模型映射
		accounts.GET("/antigravity/default-model-mapping", h.Admin.Account.GetAntigravityDefaultModelMapping)

		// Spark 影子账号
		accounts.POST("/:id/shadow", h.Admin.OpenAIOAuth.CreateShadow)

		// Claude OAuth routes
		accounts.POST("/generate-auth-url", h.Admin.OAuth.GenerateAuthURL)
		accounts.POST("/generate-setup-token-url", h.Admin.OAuth.GenerateSetupTokenURL)
		accounts.POST("/exchange-code", h.Admin.OAuth.ExchangeCode)
		accounts.POST("/exchange-setup-token-code", h.Admin.OAuth.ExchangeSetupTokenCode)
		accounts.POST("/cookie-auth", h.Admin.OAuth.CookieAuth)
		accounts.POST("/setup-token-cookie-auth", h.Admin.OAuth.SetupTokenCookieAuth)
	}
}

func registerAnnouncementRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	announcements := admin.Group("/announcements")
	{
		announcements.GET("", h.Admin.Announcement.List)
		announcements.POST("", h.Admin.Announcement.Create)
		announcements.GET("/:id", h.Admin.Announcement.GetByID)
		announcements.PUT("/:id", h.Admin.Announcement.Update)
		announcements.DELETE("/:id", h.Admin.Announcement.Delete)
		announcements.GET("/:id/read-status", h.Admin.Announcement.ListReadStatus)
	}
}

func registerOpenAIOAuthRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	openai := admin.Group("/openai")
	{
		openai.POST("/generate-auth-url", h.Admin.OpenAIOAuth.GenerateAuthURL)
		openai.POST("/exchange-code", h.Admin.OpenAIOAuth.ExchangeCode)
		openai.POST("/refresh-token", h.Admin.OpenAIOAuth.RefreshToken)
		openai.POST("/accounts/:id/refresh", h.Admin.OpenAIOAuth.RefreshAccountToken)
		openai.POST("/create-from-oauth", h.Admin.OpenAIOAuth.CreateAccountFromOAuth)
		openai.POST("/create-from-codex-pat", h.Admin.OpenAIOAuth.CreateAccountFromCodexPAT)
		openai.GET("/accounts/:id/quota", h.Admin.OpenAIOAuth.QueryQuota)
		openai.POST("/accounts/:id/quota/refresh", h.Admin.OpenAIOAuth.RefreshQuota)
		openai.POST("/accounts/:id/reset-quota", h.Admin.OpenAIOAuth.ResetQuota)
	}
}

func registerGeminiOAuthRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	gemini := admin.Group("/gemini")
	{
		gemini.POST("/oauth/auth-url", h.Admin.GeminiOAuth.GenerateAuthURL)
		gemini.POST("/oauth/exchange-code", h.Admin.GeminiOAuth.ExchangeCode)
		gemini.GET("/oauth/capabilities", h.Admin.GeminiOAuth.GetCapabilities)
	}
}

func registerAntigravityOAuthRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	antigravity := admin.Group("/antigravity")
	{
		antigravity.POST("/oauth/auth-url", h.Admin.AntigravityOAuth.GenerateAuthURL)
		antigravity.POST("/oauth/exchange-code", h.Admin.AntigravityOAuth.ExchangeCode)
		antigravity.POST("/oauth/refresh-token", h.Admin.AntigravityOAuth.RefreshToken)
	}
}

func registerGrokOAuthRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	grok := admin.Group("/grok")
	{
		grok.GET("/oauth/capabilities", h.Admin.GrokOAuth.GetCapabilities)
		grok.POST("/oauth/auth-url", h.Admin.GrokOAuth.GenerateAuthURL)
		grok.POST("/oauth/exchange-code", h.Admin.GrokOAuth.ExchangeCode)
		grok.POST("/oauth/refresh-token", h.Admin.GrokOAuth.RefreshToken)
		grok.POST("/oauth/sso-token", h.Admin.GrokOAuth.ValidateSSOToken)
		grok.POST("/oauth/password", h.Admin.GrokOAuth.AuthorizePassword)
		grok.POST("/oauth/create-from-oauth", h.Admin.GrokOAuth.CreateAccountFromOAuth)
		grok.POST("/sso-to-oauth", h.Admin.GrokOAuth.CreateAccountsFromSSO)
		grok.POST("/oauth/reconcile", h.Admin.GrokOAuth.ReconcileOAuthAccounts)
		grok.POST("/accounts/:id/refresh", h.Admin.GrokOAuth.RefreshAccountToken)
		grok.GET("/accounts/:id/quota", h.Admin.GrokOAuth.QueryQuota)
		grok.POST("/accounts/:id/reset-quota", h.Admin.GrokOAuth.ResetQuota)
		grok.GET("/runtime-sanity", h.Admin.GrokOAuth.RuntimeSanity)
	}
}

// registerCNProviderRoutes 注册国产供应商（kimi/zhipu/deepseek）的额度与余额查询端点。
func registerCNProviderRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	cn := admin.Group("/cn-providers")
	{
		// Coding Plan 滚动窗口用量（kimi/zhipu coding 账号）。
		cn.GET("/accounts/:id/quota", h.Admin.CNProvider.QueryQuota)
		// payg 账号余额（kimi/deepseek；zhipu 无余额端点）。
		cn.GET("/accounts/:id/balance", h.Admin.CNProvider.QueryBalance)
	}
}

func registerProxyRoutes(admin *gin.RouterGroup, h *handler.Handlers, stepUpAuth middleware.StepUpAuthMiddleware) {
	proxies := admin.Group("/proxies")
	{
		proxies.GET("", h.Admin.Proxy.List)
		proxies.GET("/all", h.Admin.Proxy.GetAll)
		// 代理导出泄露账号密码原文——要求 step-up 2FA
		proxies.GET("/data", gin.HandlerFunc(stepUpAuth), h.Admin.Proxy.ExportData)
		proxies.POST("/data", h.Admin.Proxy.ImportData)
		proxies.GET("/:id", h.Admin.Proxy.GetByID)
		proxies.POST("", h.Admin.Proxy.Create)
		proxies.PUT("/:id", h.Admin.Proxy.Update)
		proxies.DELETE("/:id", h.Admin.Proxy.Delete)
		proxies.POST("/:id/test", h.Admin.Proxy.Test)
		proxies.POST("/:id/quality-check", h.Admin.Proxy.CheckQuality)
		proxies.GET("/:id/stats", h.Admin.Proxy.GetStats)
		proxies.GET("/:id/accounts", h.Admin.Proxy.GetProxyAccounts)
		proxies.POST("/batch-delete", h.Admin.Proxy.BatchDelete)
		proxies.POST("/batch", h.Admin.Proxy.BatchCreate)
	}
}

// registerRedeemCodeRoutes 注册卡密（兑换码）管理路由。
//
// 卡密本身就是可兑现的价值载体：生成、批量改值、作废、删除都会新增/改变可兑价值，
// 批量导出还会一次性带走最多 1 万条仍可使用的卡密。这些写操作与"改余额"同口径，
// 要求 step-up 2FA（机器凭据一律 403）。
//
// 只读的列表 / 详情 / 统计现在只返回掩码（明文只在生成回执里出现一次），但仍不允许
// 机器凭据访问：旧版全权 x-api-key 可以逐页把全量卡密元数据读走，等于绕过导出接口的
// step-up（#489 审计 B-2）。人只会话可直接读，不需要再输 TOTP。
func registerRedeemCodeRoutes(admin *gin.RouterGroup, h *handler.Handlers, stepUpAuth middleware.StepUpAuthMiddleware) {
	codes := admin.Group("/redeem-codes")
	{
		codes.GET("", middleware.ForbidMachineCredentials(), h.Admin.Redeem.List)
		codes.GET("/stats", middleware.ForbidMachineCredentials(), h.Admin.Redeem.GetStats)
		// 导出会落地卡片元数据 CSV：要求 step-up 2FA
		codes.GET("/export", gin.HandlerFunc(stepUpAuth), h.Admin.Redeem.Export)
		codes.GET("/:id", middleware.ForbidMachineCredentials(), h.Admin.Redeem.GetByID)
		// 固定卡密 + 直接发放给目标用户（一步产出可兑价值）：要求 step-up 2FA
		codes.POST("/create-and-redeem", gin.HandlerFunc(stepUpAuth), h.Admin.Redeem.CreateAndRedeem)
		// 生成卡密即产出可兑价值（且响应里带一次性明文）：要求 step-up 2FA
		codes.POST("/generate", gin.HandlerFunc(stepUpAuth), h.Admin.Redeem.Generate)
		codes.DELETE("/:id", gin.HandlerFunc(stepUpAuth), h.Admin.Redeem.Delete)
		codes.POST("/batch-delete", gin.HandlerFunc(stepUpAuth), h.Admin.Redeem.BatchDelete)
		// 批量改值/改状态/改过期时间会直接改变可兑价值：要求 step-up 2FA
		codes.POST("/batch-update", gin.HandlerFunc(stepUpAuth), h.Admin.Redeem.BatchUpdate)
		codes.POST("/:id/expire", gin.HandlerFunc(stepUpAuth), h.Admin.Redeem.Expire)
	}
}

// registerPromoCodeRoutes 注册优惠码管理路由。
// 优惠码携带赠送余额（bonus_amount），创建/改值/删除都直接改变资金权益，要求 step-up 2FA；
// 列表与使用记录是只读查询，不额外打断。
func registerPromoCodeRoutes(admin *gin.RouterGroup, h *handler.Handlers, stepUpAuth middleware.StepUpAuthMiddleware) {
	promoCodes := admin.Group("/promo-codes")
	{
		promoCodes.GET("", h.Admin.Promo.List)
		promoCodes.GET("/:id", h.Admin.Promo.GetByID)
		promoCodes.POST("", gin.HandlerFunc(stepUpAuth), h.Admin.Promo.Create)
		promoCodes.PUT("/:id", gin.HandlerFunc(stepUpAuth), h.Admin.Promo.Update)
		promoCodes.DELETE("/:id", gin.HandlerFunc(stepUpAuth), h.Admin.Promo.Delete)
		promoCodes.GET("/:id/usages", h.Admin.Promo.GetUsages)
	}
}

func registerSettingsRoutes(admin *gin.RouterGroup, h *handler.Handlers, stepUpAuth middleware.StepUpAuthMiddleware) {
	adminSettings := admin.Group("/settings")
	registerScopedAdminKeyRoutes(adminSettings, h, stepUpAuth)
	{
		adminSettings.GET("", h.Admin.Setting.GetSettings)
		// 系统设置包含支付/邮件/注册等敏感配置：整表单保存要求 step-up 二次验证。
		adminSettings.PUT("", gin.HandlerFunc(stepUpAuth), h.Admin.Setting.UpdateSettings)
		adminSettings.POST("/test-smtp", h.Admin.Setting.TestSMTPConnection)
		adminSettings.POST("/send-test-email", h.Admin.Setting.SendTestEmail)
		adminSettings.GET("/email-templates", h.Admin.Setting.ListEmailTemplates)
		adminSettings.POST("/email-template-preview", h.Admin.Setting.PreviewEmailTemplate)
		adminSettings.GET("/email-templates/:event/:locale", h.Admin.Setting.GetEmailTemplate)
		adminSettings.PUT("/email-templates/:event/:locale", h.Admin.Setting.UpdateEmailTemplate)
		adminSettings.POST("/email-templates/:event/:locale/restore-official", h.Admin.Setting.RestoreOfficialEmailTemplate)
		// Admin API Key 管理
		adminSettings.GET("/admin-api-key", h.Admin.Setting.GetAdminAPIKey)
		// 旧版全权密钥已退役：路由保留用于返回迁移提示，不再生成新密钥。
		adminSettings.POST("/admin-api-key/regenerate", h.Admin.Setting.RegenerateAdminAPIKey)
		adminSettings.DELETE("/admin-api-key", gin.HandlerFunc(stepUpAuth), h.Admin.Setting.DeleteAdminAPIKey)
		// 529过载冷却配置
		adminSettings.GET("/overload-cooldown", h.Admin.Setting.GetOverloadCooldownSettings)
		adminSettings.PUT("/overload-cooldown", h.Admin.Setting.UpdateOverloadCooldownSettings)
		// 429默认回避配置
		adminSettings.GET("/rate-limit-429-cooldown", h.Admin.Setting.GetRateLimit429CooldownSettings)
		adminSettings.PUT("/rate-limit-429-cooldown", h.Admin.Setting.UpdateRateLimit429CooldownSettings)
		// OpenAI OAuth image-tool unavailable cooldown configuration
		adminSettings.GET("/openai-images-oauth-unavailable-cooldown", h.Admin.Setting.GetOpenAIImagesOAuthUnavailableCooldownSettings)
		adminSettings.PUT("/openai-images-oauth-unavailable-cooldown", h.Admin.Setting.UpdateOpenAIImagesOAuthUnavailableCooldownSettings)
		// 注册邮箱精确黑名单
		adminSettings.GET("/registration-email-blacklist", h.Admin.Setting.GetRegistrationEmailBlacklist)
		adminSettings.PUT("/registration-email-blacklist", h.Admin.Setting.UpdateRegistrationEmailBlacklist)
		// 面板 API 限流配置
		adminSettings.GET("/panel-rate-limit", h.Admin.Setting.GetPanelRateLimitSettings)
		adminSettings.PUT("/panel-rate-limit", h.Admin.Setting.UpdatePanelRateLimitSettings)
		// 管理端 401/403 探测告警配置（阈值/窗口/开关）
		adminSettings.GET("/admin-auth-probe", h.Admin.Setting.GetAdminAuthProbeSettings)
		adminSettings.PUT("/admin-auth-probe", h.Admin.Setting.UpdateAdminAuthProbeSettings)
		adminSettings.GET("/gateway-ip-notice", h.Admin.Setting.GetGatewayIPNotice)
		adminSettings.PUT("/gateway-ip-notice", h.Admin.Setting.UpdateGatewayIPNotice)
		// 密钥轮换门禁的风险确认跳过（开关 + 有效期），用户侧充值/兑换页的次级按钮依赖它。
		adminSettings.GET("/key-rotation-risk-ack", h.Admin.Setting.GetKeyRotationRiskAckSettings)
		adminSettings.PUT("/key-rotation-risk-ack", h.Admin.Setting.UpdateKeyRotationRiskAckSettings)
		// 站点主题（主色/中性色/圆角/字体/默认明暗）
		adminSettings.GET("/theme", h.Admin.Setting.GetSiteTheme)
		adminSettings.PUT("/theme", h.Admin.Setting.UpdateSiteTheme)
		// 流超时处理配置
		adminSettings.GET("/stream-timeout", h.Admin.Setting.GetStreamTimeoutSettings)
		adminSettings.PUT("/stream-timeout", h.Admin.Setting.UpdateStreamTimeoutSettings)
		// 请求整流器配置
		adminSettings.GET("/rectifier", h.Admin.Setting.GetRectifierSettings)
		adminSettings.PUT("/rectifier", h.Admin.Setting.UpdateRectifierSettings)
		// Beta 策略配置
		adminSettings.GET("/beta-policy", h.Admin.Setting.GetBetaPolicySettings)
		adminSettings.PUT("/beta-policy", h.Admin.Setting.UpdateBetaPolicySettings)
		// Web Search 模拟配置
		adminSettings.GET("/web-search-emulation", h.Admin.Setting.GetWebSearchEmulationConfig)
		adminSettings.PUT("/web-search-emulation", h.Admin.Setting.UpdateWebSearchEmulationConfig)
		adminSettings.POST("/web-search-emulation/test", h.Admin.Setting.TestWebSearchEmulation)
		adminSettings.POST("/web-search-emulation/reset-usage", h.Admin.Setting.ResetWebSearchUsage)
	}
}

func registerDataManagementRoutes(admin *gin.RouterGroup, h *handler.Handlers, stepUpAuth middleware.StepUpAuthMiddleware) {
	dataManagement := admin.Group("/data-management")
	{
		dataManagement.GET("/agent/health", h.Admin.DataManagement.GetAgentHealth)
		dataManagement.GET("/config", h.Admin.DataManagement.GetConfig)
		dataManagement.PUT("/config", h.Admin.DataManagement.UpdateConfig)
		dataManagement.GET("/sources/:source_type/profiles", h.Admin.DataManagement.ListSourceProfiles)
		dataManagement.POST("/sources/:source_type/profiles", h.Admin.DataManagement.CreateSourceProfile)
		dataManagement.PUT("/sources/:source_type/profiles/:profile_id", h.Admin.DataManagement.UpdateSourceProfile)
		dataManagement.DELETE("/sources/:source_type/profiles/:profile_id", h.Admin.DataManagement.DeleteSourceProfile)
		dataManagement.POST("/sources/:source_type/profiles/:profile_id/activate", h.Admin.DataManagement.SetActiveSourceProfile)
		dataManagement.POST("/s3/test", h.Admin.DataManagement.TestS3)
		dataManagement.GET("/s3/profiles", h.Admin.DataManagement.ListS3Profiles)
		// 修改 S3 目标可将数据备份外泄——要求 step-up 2FA
		dataManagement.POST("/s3/profiles", gin.HandlerFunc(stepUpAuth), h.Admin.DataManagement.CreateS3Profile)
		dataManagement.PUT("/s3/profiles/:profile_id", gin.HandlerFunc(stepUpAuth), h.Admin.DataManagement.UpdateS3Profile)
		dataManagement.DELETE("/s3/profiles/:profile_id", h.Admin.DataManagement.DeleteS3Profile)
		dataManagement.POST("/s3/profiles/:profile_id/activate", gin.HandlerFunc(stepUpAuth), h.Admin.DataManagement.SetActiveS3Profile)
		dataManagement.POST("/backups", gin.HandlerFunc(stepUpAuth), h.Admin.DataManagement.CreateBackupJob)
		dataManagement.GET("/backups", h.Admin.DataManagement.ListBackupJobs)
		dataManagement.GET("/backups/:job_id", h.Admin.DataManagement.GetBackupJob)
	}
}

func registerBackupRoutes(admin *gin.RouterGroup, h *handler.Handlers, stepUpAuth middleware.StepUpAuthMiddleware) {
	backup := admin.Group("/backups")
	{
		// S3 存储配置
		backup.GET("/s3-config", h.Admin.Backup.GetS3Config)
		// 修改 S3 目标可将数据库备份外泄——要求 step-up 2FA
		backup.PUT("/s3-config", gin.HandlerFunc(stepUpAuth), h.Admin.Backup.UpdateS3Config)
		// 测试连接会以服务端身份向管理员填写的 endpoint 发请求（S3 探测原语），
		// 与改配置同级：要求 step-up 2FA。
		backup.POST("/s3-config/test", gin.HandlerFunc(stepUpAuth), h.Admin.Backup.TestS3Connection)

		// 异步生图对象存储配置（与备份共用 S3 客户端，可直接复用备份凭证）
		backup.GET("/image-storage", h.Admin.Backup.GetImageStorageConfig)
		// 同 S3 配置：改写对象存储目标可将生成内容导向外部账号——要求 step-up 2FA
		backup.PUT("/image-storage", gin.HandlerFunc(stepUpAuth), h.Admin.Backup.UpdateImageStorageConfig)
		// 同上：图床"测试连接"也是任意出站目标探测入口。
		backup.POST("/image-storage/test", gin.HandlerFunc(stepUpAuth), h.Admin.Backup.TestImageStorageConnection)

		// 定时备份配置
		backup.GET("/schedule", h.Admin.Backup.GetSchedule)
		backup.PUT("/schedule", h.Admin.Backup.UpdateSchedule)

		// 备份操作
		backup.POST("", gin.HandlerFunc(stepUpAuth), h.Admin.Backup.CreateBackup)
		backup.GET("", h.Admin.Backup.ListBackups)
		backup.GET("/:id", h.Admin.Backup.GetBackup)
		backup.DELETE("/:id", h.Admin.Backup.DeleteBackup)
		// 备份下载链接可直接取走整库数据——要求 step-up 2FA
		backup.GET("/:id/download-url", gin.HandlerFunc(stepUpAuth), h.Admin.Backup.GetDownloadURL)

		// 恢复操作：整库覆盖可回滚安全设置（含 step-up 开关本身）——要求 step-up 2FA
		backup.POST("/:id/restore", gin.HandlerFunc(stepUpAuth), h.Admin.Backup.RestoreBackup)
	}
}

func registerSystemRoutes(admin *gin.RouterGroup, h *handler.Handlers, stepUpAuth middleware.StepUpAuthMiddleware) {
	system := admin.Group("/system")
	{
		system.GET("/version", h.Admin.System.GetVersion)
		system.GET("/check-updates", h.Admin.System.CheckUpdates)
		system.GET("/rollback-versions", h.Admin.System.GetRollbackVersions)
		// 部署/回滚/重启是主机生命周期动作（会中断全部在途请求并更换二进制）：
		// 此前没有任何 2FA 门控，属真实缺口；要求 step-up，机器凭据一律 403。
		system.POST("/update", gin.HandlerFunc(stepUpAuth), h.Admin.System.PerformUpdate)
		system.POST("/rollback", gin.HandlerFunc(stepUpAuth), h.Admin.System.Rollback)
		system.POST("/restart", gin.HandlerFunc(stepUpAuth), h.Admin.System.RestartService)
	}
}

// registerSubscriptionRoutes 注册订阅管理路由。
// 分配/批量分配/延期/重置额度/撤销/恢复都直接改变用户的可用订阅权益（等同资金操作），
// 要求 step-up 2FA；订阅列表与进度查询是只读，不额外打断。
func registerSubscriptionRoutes(admin *gin.RouterGroup, h *handler.Handlers, stepUpAuth middleware.StepUpAuthMiddleware) {
	subscriptions := admin.Group("/subscriptions")
	{
		subscriptions.GET("", h.Admin.Subscription.List)
		subscriptions.GET("/:id", h.Admin.Subscription.GetByID)
		subscriptions.GET("/:id/progress", h.Admin.Subscription.GetProgress)
		subscriptions.POST("/assign", gin.HandlerFunc(stepUpAuth), h.Admin.Subscription.Assign)
		subscriptions.POST("/bulk-assign", gin.HandlerFunc(stepUpAuth), h.Admin.Subscription.BulkAssign)
		subscriptions.POST("/:id/extend", gin.HandlerFunc(stepUpAuth), h.Admin.Subscription.Extend)
		subscriptions.POST("/:id/reset-quota", gin.HandlerFunc(stepUpAuth), h.Admin.Subscription.ResetQuota)
		subscriptions.POST("/:id/revoke", gin.HandlerFunc(stepUpAuth), h.Admin.Subscription.Revoke)
		subscriptions.POST("/:id/restore", gin.HandlerFunc(stepUpAuth), h.Admin.Subscription.Restore)
		subscriptions.DELETE("/:id", gin.HandlerFunc(stepUpAuth), h.Admin.Subscription.Revoke)
	}

	// 分组下的订阅列表
	admin.GET("/groups/:id/subscriptions", h.Admin.Subscription.ListByGroup)

	// 用户下的订阅列表
	admin.GET("/users/:id/subscriptions", h.Admin.Subscription.ListByUser)
}

func registerUsageRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	usage := admin.Group("/usage")
	usage.GET("/connections", h.Admin.Usage.Connections)
	usage.POST("/connections/edge", h.Admin.Usage.ImportEdgeConnections)
	usage.GET("/traffic", h.Admin.Usage.Traffic)
	usage.GET("/traffic/keys", h.Admin.Usage.TrafficKeys)
	usage.GET("/traffic/health", h.Admin.Usage.TrafficHealth)

	{
		usage.GET("", h.Admin.Usage.List)
		usage.GET("/stats", h.Admin.Usage.Stats)
		usage.GET("/search-users", h.Admin.Usage.SearchUsers)
		usage.GET("/search-api-keys", h.Admin.Usage.SearchAPIKeys)
		usage.GET("/cleanup-tasks", h.Admin.Usage.ListCleanupTasks)
		usage.POST("/cleanup-tasks", h.Admin.Usage.CreateCleanupTask)
		usage.POST("/cleanup-tasks/:id/cancel", h.Admin.Usage.CancelCleanupTask)
	}
}

func registerUserAttributeRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	attrs := admin.Group("/user-attributes")
	{
		attrs.GET("", h.Admin.UserAttribute.ListDefinitions)
		attrs.POST("", h.Admin.UserAttribute.CreateDefinition)
		attrs.POST("/batch", h.Admin.UserAttribute.GetBatchUserAttributes)
		attrs.PUT("/reorder", h.Admin.UserAttribute.ReorderDefinitions)
		attrs.PUT("/:id", h.Admin.UserAttribute.UpdateDefinition)
		attrs.DELETE("/:id", h.Admin.UserAttribute.DeleteDefinition)
	}
}

func registerScheduledTestRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	plans := admin.Group("/scheduled-test-plans")
	{
		plans.POST("", h.Admin.ScheduledTest.Create)
		plans.PUT("/:id", h.Admin.ScheduledTest.Update)
		plans.DELETE("/:id", h.Admin.ScheduledTest.Delete)
		plans.GET("/:id/results", h.Admin.ScheduledTest.ListResults)
	}
	// Nested under accounts
	admin.GET("/accounts/:id/scheduled-test-plans", h.Admin.ScheduledTest.ListByAccount)
}

func registerGroupQualityCheckRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	qc := admin.Group("/quality-check")
	{
		qc.GET("/groups", h.Admin.GroupQualityCheck.List)
		qc.GET("/groups/:id", h.Admin.GroupQualityCheck.Get)
		qc.PUT("/groups/:id", h.Admin.GroupQualityCheck.SetEnabled)
		qc.GET("/groups/:id/results", h.Admin.GroupQualityCheck.ListResults)
	}
}

func registerErrorPassthroughRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	rules := admin.Group("/error-passthrough-rules")
	{
		rules.GET("", h.Admin.ErrorPassthrough.List)
		rules.GET("/:id", h.Admin.ErrorPassthrough.GetByID)
		rules.POST("", h.Admin.ErrorPassthrough.Create)
		rules.PUT("/:id", h.Admin.ErrorPassthrough.Update)
		rules.DELETE("/:id", h.Admin.ErrorPassthrough.Delete)
	}
}

func registerTLSFingerprintProfileRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	profiles := admin.Group("/tls-fingerprint-profiles")
	{
		profiles.GET("", h.Admin.TLSFingerprintProfile.List)
		profiles.GET("/:id", h.Admin.TLSFingerprintProfile.GetByID)
		profiles.POST("", h.Admin.TLSFingerprintProfile.Create)
		profiles.PUT("/:id", h.Admin.TLSFingerprintProfile.Update)
		profiles.DELETE("/:id", h.Admin.TLSFingerprintProfile.Delete)
	}
}

func registerPluginRoutes(admin *gin.RouterGroup, h *handler.Handlers, stepUpAuth middleware.StepUpAuthMiddleware) {
	plugins := admin.Group("/plugins")
	{
		plugins.GET("", h.Admin.Plugin.List)
		plugins.GET("/:id", h.Admin.Plugin.Get)
		plugins.POST("/upload", gin.HandlerFunc(stepUpAuth), h.Admin.Plugin.Upload)
		plugins.POST("/:id/enable", gin.HandlerFunc(stepUpAuth), h.Admin.Plugin.Enable)
		plugins.POST("/:id/disable", gin.HandlerFunc(stepUpAuth), h.Admin.Plugin.Disable)
		plugins.DELETE("/:id", gin.HandlerFunc(stepUpAuth), h.Admin.Plugin.Delete)
		plugins.GET("/:id/config", h.Admin.Plugin.GetConfig)
		plugins.PUT("/:id/config", gin.HandlerFunc(stepUpAuth), h.Admin.Plugin.SaveConfig)
		plugins.POST("/:id/test", gin.HandlerFunc(stepUpAuth), h.Admin.Plugin.Test)
		plugins.POST("/:id/ui-session", h.Admin.Plugin.CreateUISession)
	}
}

func registerChannelRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	channels := admin.Group("/channels")
	{
		channels.GET("", h.Admin.Channel.List)
		channels.GET("/model-pricing", h.Admin.Channel.GetModelDefaultPricing)
		channels.GET("/operations/snapshot", h.Admin.Channel.OperationsSnapshot)
		channels.GET("/:id/operations", h.Admin.Channel.OperationsCatalog)
		channels.POST("/:id/operations", h.Admin.Channel.MutateOperations)
		channels.GET("/pricing/sync-models", h.Admin.Channel.SyncPricingModels)
		channels.GET("/:id", h.Admin.Channel.GetByID)
		channels.POST("", h.Admin.Channel.Create)
		channels.PUT("/:id", h.Admin.Channel.Update)
		channels.DELETE("/:id", h.Admin.Channel.Delete)
	}
}

func registerChannelMonitorRoutes(admin *gin.RouterGroup, h *handler.Handlers, settingService *service.SettingService) {
	guard := channelMonitorAdminFeatureGuard(settingService)
	monitors := admin.Group("/channel-monitors")
	monitors.Use(guard)
	{
		monitors.GET("", h.Admin.ChannelMonitor.List)
		monitors.POST("", h.Admin.ChannelMonitor.Create)
		monitors.GET("/:id", h.Admin.ChannelMonitor.Get)
		monitors.POST("/:id/duplicate", h.Admin.ChannelMonitor.Duplicate)
		monitors.PUT("/:id", h.Admin.ChannelMonitor.Update)
		monitors.DELETE("/:id", h.Admin.ChannelMonitor.Delete)
		monitors.POST("/:id/run", h.Admin.ChannelMonitor.Run)
		monitors.GET("/:id/history", h.Admin.ChannelMonitor.History)
	}

	templates := admin.Group("/channel-monitor-templates")
	templates.Use(guard)
	{
		templates.GET("", h.Admin.ChannelMonitorTemplate.List)
		templates.POST("", h.Admin.ChannelMonitorTemplate.Create)
		templates.GET("/:id", h.Admin.ChannelMonitorTemplate.Get)
		templates.PUT("/:id", h.Admin.ChannelMonitorTemplate.Update)
		templates.DELETE("/:id", h.Admin.ChannelMonitorTemplate.Delete)
		templates.GET("/:id/monitors", h.Admin.ChannelMonitorTemplate.AssociatedMonitors)
		templates.POST("/:id/apply", h.Admin.ChannelMonitorTemplate.Apply)
	}
}

// registerAffiliateRoutes 注册邀请返利的管理端路由（专属用户配置）
func registerAffiliateRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	affiliates := admin.Group("/affiliates")
	{
		affiliates.GET("/invites", h.Admin.Affiliate.ListInviteRecords)
		affiliates.GET("/rebates", h.Admin.Affiliate.ListRebateRecords)
		affiliates.GET("/transfers", h.Admin.Affiliate.ListTransferRecords)

		users := affiliates.Group("/users")
		{
			users.GET("", h.Admin.Affiliate.ListUsers)
			users.GET("/lookup", h.Admin.Affiliate.LookupUsers)
			users.POST("/batch-rate", h.Admin.Affiliate.BatchSetRate)
			users.GET("/:user_id/overview", h.Admin.Affiliate.GetUserOverview)
			users.PUT("/:user_id", h.Admin.Affiliate.UpdateUserSettings)
			users.DELETE("/:user_id", h.Admin.Affiliate.ClearUserSettings)
			users.POST("/:user_id/codes", h.Admin.Affiliate.CreateUserAffiliateCode)
			users.DELETE("/:user_id/codes", h.Admin.Affiliate.DeleteUserAffiliateCode)
		}
	}
}

func registerChannelMonitorV2Routes(admin *gin.RouterGroup, h *handler.Handlers, settingService *service.SettingService) {
	// Config GET/PUT: feature enabled only (operators can prepare V2 before flipping mode).
	// Read/matrix endpoints: require mode=v2 so V1 deployments do not serve passive data.
	featureGuard := channelMonitorAdminFeatureGuard(settingService)
	modeV2Guard := channelMonitorModeV2Guard(settingService)

	monitor := admin.Group("/channel-monitor-v2")
	{
		config := monitor.Group("")
		config.Use(featureGuard)
		{
			config.GET("/config", h.ChannelMonitorV2.GetConfig)
			config.PUT("/config", h.ChannelMonitorV2.UpdateConfig)
		}
		reads := monitor.Group("")
		reads.Use(modeV2Guard)
		{
			reads.GET("/dimensions", h.ChannelMonitorV2.Dimensions)
			reads.GET("/snapshot", h.ChannelMonitorV2.AdminSnapshot)
			reads.GET("/models", h.ChannelMonitorV2.AdminModels)
			reads.GET("/matrix", h.ChannelMonitorV2.AdminMatrix)
			reads.GET("/errors", h.ChannelMonitorV2.Errors)
			reads.GET("/users", h.ChannelMonitorV2.AdminUsers)
		}
	}
}

func channelMonitorAdminFeatureGuard(settingService *service.SettingService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if settingService != nil && settingService.GetChannelMonitorRuntime(c.Request.Context()).Enabled {
			c.Next()
			return
		}
		response.ErrorFrom(c, service.ErrChannelMonitorDisabled)
		c.Abort()
	}
}

// channelMonitorModeV2Guard requires feature enabled and channel_monitor_mode=v2.
func channelMonitorModeV2Guard(settingService *service.SettingService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if settingService == nil {
			response.ErrorFrom(c, service.ErrChannelMonitorDisabled)
			c.Abort()
			return
		}
		rt := settingService.GetChannelMonitorRuntime(c.Request.Context())
		if !rt.Enabled {
			response.ErrorFrom(c, service.ErrChannelMonitorDisabled)
			c.Abort()
			return
		}
		if !rt.PassiveAggregationAllowed() {
			response.ErrorFrom(c, service.ErrChannelMonitorModeMismatch)
			c.Abort()
			return
		}
		c.Next()
	}
}
