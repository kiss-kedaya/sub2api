package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/google/wire"
)

// ProvideAdminHandlers creates the AdminHandlers struct
func ProvideAdminHandlers(
	dashboardHandler *admin.DashboardHandler,
	userHandler *admin.UserHandler,
	groupHandler *admin.GroupHandler,
	accountHandler *admin.AccountHandler,
	announcementHandler *admin.AnnouncementHandler,
	dataManagementHandler *admin.DataManagementHandler,
	backupHandler *admin.BackupHandler,
	oauthHandler *admin.OAuthHandler,
	openaiOAuthHandler *admin.OpenAIOAuthHandler,
	geminiOAuthHandler *admin.GeminiOAuthHandler,
	antigravityOAuthHandler *admin.AntigravityOAuthHandler,
	grokOAuthHandler *admin.GrokOAuthHandler,
	cnProviderHandler *admin.CNProviderHandler,
	proxyHandler *admin.ProxyHandler,
	redeemHandler *admin.RedeemHandler,
	promoHandler *admin.PromoHandler,
	settingHandler *admin.SettingHandler,
	opsHandler *admin.OpsHandler,
	systemHandler *admin.SystemHandler,
	subscriptionHandler *admin.SubscriptionHandler,
	usageHandler *admin.UsageHandler,
	userAttributeHandler *admin.UserAttributeHandler,
	errorPassthroughHandler *admin.ErrorPassthroughHandler,
	tlsFingerprintProfileHandler *admin.TLSFingerprintProfileHandler,
	pluginHandler *admin.PluginHandler,
	apiKeyHandler *admin.AdminAPIKeyHandler,
	scheduledTestHandler *admin.ScheduledTestHandler,
	channelHandler *admin.ChannelHandler,
	channelMonitorHandler *admin.ChannelMonitorHandler,
	channelMonitorTemplateHandler *admin.ChannelMonitorRequestTemplateHandler,
	contentModerationHandler *admin.ContentModerationHandler,
	promptAuditHandler *securityaudit.PromptAdminHandler,
	paymentHandler *admin.PaymentHandler,
	affiliateHandler *admin.AffiliateHandler,
	complianceHandler *admin.ComplianceHandler,
	auditLogHandler *admin.AuditLogHandler,
	groupQualityCheckHandler *admin.GroupQualityCheckHandler,
	upstreamBillingProbe *service.UpstreamBillingProbeService,
	ollamaCloudUsage *service.OllamaCloudUsageService,
	settingService *service.SettingService,
	claudeResetCredits *service.ClaudeResetCreditService,
	accountOps *admin.AccountOpsHandler,
	mihomo *admin.MihomoHandler,
	credentialGuard *admin.CredentialGuardHandler,
	opsAutomation *admin.OpsAutomationHandler,
	excelGrant *admin.ExcelGrantHandler,
	totpService *service.TotpService,
	userService *service.UserService,
	admin2FAReset *service.Admin2FAResetService,
) *AdminHandlers {
	accountHandler.SetUpstreamBillingProbeService(upstreamBillingProbe)
	accountHandler.SetOllamaCloudUsageService(ollamaCloudUsage)
	accountHandler.SetCodexTicketSettings(settingService)
	accountHandler.SetClaudeResetCreditService(claudeResetCredits)
	accountHandler.SetStepUpDependencies(totpService, userService, settingService)
	// 破玻璃重置 2FA：装配在用户 handler 上，路由 POST /users/:id/reset-2fa。
	userHandler.SetAdmin2FAResetService(admin2FAReset)
	return &AdminHandlers{
		DebugDump:              admin.NewDebugDumpHandler(),
		ExcelGrant:             excelGrant,
		OpsAutomation:          opsAutomation,
		CredentialGuard:        credentialGuard,
		AccountOps:             accountOps,
		Mihomo:                 mihomo,
		Dashboard:              dashboardHandler,
		User:                   userHandler,
		Group:                  groupHandler,
		Account:                accountHandler,
		Announcement:           announcementHandler,
		DataManagement:         dataManagementHandler,
		Backup:                 backupHandler,
		OAuth:                  oauthHandler,
		OpenAIOAuth:            openaiOAuthHandler,
		GeminiOAuth:            geminiOAuthHandler,
		AntigravityOAuth:       antigravityOAuthHandler,
		GrokOAuth:              grokOAuthHandler,
		CNProvider:             cnProviderHandler,
		Proxy:                  proxyHandler,
		Redeem:                 redeemHandler,
		Promo:                  promoHandler,
		Setting:                settingHandler,
		Ops:                    opsHandler,
		System:                 systemHandler,
		Subscription:           subscriptionHandler,
		Usage:                  usageHandler,
		UserAttribute:          userAttributeHandler,
		ErrorPassthrough:       errorPassthroughHandler,
		TLSFingerprintProfile:  tlsFingerprintProfileHandler,
		Plugin:                 pluginHandler,
		APIKey:                 apiKeyHandler,
		ScheduledTest:          scheduledTestHandler,
		Channel:                channelHandler,
		ChannelMonitor:         channelMonitorHandler,
		ChannelMonitorTemplate: channelMonitorTemplateHandler,
		ContentModeration:      contentModerationHandler,
		PromptAudit:            promptAuditHandler,
		Payment:                paymentHandler,
		Affiliate:              affiliateHandler,
		Compliance:             complianceHandler,
		AuditLog:               auditLogHandler,
		GroupQualityCheck:      groupQualityCheckHandler,
	}
}

func ProvideGatewayHandler(
	gatewayService *service.GatewayService,
	openAIGatewayService *service.OpenAIGatewayService,
	geminiCompatService *service.GeminiMessagesCompatService,
	antigravityGatewayService *service.AntigravityGatewayService,
	userService *service.UserService,
	concurrencyService *service.ConcurrencyService,
	billingCacheService *service.BillingCacheService,
	usageService *service.UsageService,
	apiKeyService *service.APIKeyService,
	usageRecordWorkerPool *service.UsageRecordWorkerPool,
	balancePreauthorizationService *service.BalancePreauthorizationService,
	errorPassthroughService *service.ErrorPassthroughService,
	contentModerationService *service.ContentModerationService,
	userMsgQueueService *service.UserMessageQueueService,
	cfg *config.Config,
	settingService *service.SettingService,
	coordinator *securityaudit.Coordinator,
) *GatewayHandler {
	h := NewGatewayHandler(gatewayService, openAIGatewayService, geminiCompatService, antigravityGatewayService,
		userService, concurrencyService, billingCacheService, usageService, apiKeyService, usageRecordWorkerPool,
		errorPassthroughService, contentModerationService, userMsgQueueService, cfg, settingService)
	h.balancePreauthorizer = balancePreauthorizationService
	h.securityAuditCoordinator = coordinator
	// Public model discovery merges catalogue metadata (description, limits,
	// modalities, capabilities, prices) through the gateway service.
	if gatewayService != nil {
		h.modelMetadata = gatewayService
	}
	return h
}

func ProvideOpenAIGatewayHandler(
	gatewayService *service.OpenAIGatewayService,
	pluginManager *service.PluginManager,
	concurrencyService *service.ConcurrencyService,
	billingCacheService *service.BillingCacheService,
	apiKeyService *service.APIKeyService,
	usageRecordWorkerPool *service.UsageRecordWorkerPool,
	balancePreauthorizationService *service.BalancePreauthorizationService,
	errorPassthroughService *service.ErrorPassthroughService,
	contentModerationService *service.ContentModerationService,
	opsService *service.OpsService,
	grokQuotaService *service.GrokQuotaService,
	cfg *config.Config,
	coordinator *securityaudit.Coordinator,
) *OpenAIGatewayHandler {
	gatewayService.SetPluginManager(pluginManager)
	h := NewOpenAIGatewayHandler(gatewayService, concurrencyService, billingCacheService, apiKeyService,
		usageRecordWorkerPool, errorPassthroughService, contentModerationService, opsService, cfg)
	h.balancePreauthorizer = balancePreauthorizationService
	h.securityAuditCoordinator = coordinator
	h.grokMediaEligibilityProber = grokQuotaService
	return h
}

func ProvideBatchImageHandler(
	batchService *service.BatchImagePublicService,
	download *service.BatchImageDownloadService,
	cleanup *service.BatchImageCleanupService,
	openAI *OpenAIGatewayHandler,
) *BatchImageHandler {
	h := NewBatchImageHandler(batchService, download, cleanup)
	h.openAI = openAI
	return h
}

// ProvideSystemHandler creates admin.SystemHandler with UpdateService
func ProvideSystemHandler(updateService *service.UpdateService, lockService *service.SystemOperationLockService) *admin.SystemHandler {
	return admin.NewSystemHandler(updateService, lockService)
}

// ProvideSettingHandler creates SettingHandler with version from BuildInfo
func ProvideSettingHandler(settingService *service.SettingService, buildInfo BuildInfo, notificationEmailService *service.NotificationEmailService, userService *service.UserService) *SettingHandler {
	// The entry HTML embeds window.__APP_CONFIG__ built by the settings service,
	// while /settings/public answers from this handler. Nothing ever told the
	// service which release it belongs to, so the embedded copy always said "" and
	// a browser could not tell which release its page came from (the stale-page
	// check in the frontend stays off without it). This is the one place that has
	// both the service and the version.
	if settingService != nil {
		settingService.SetVersion(buildInfo.Version)
	}
	h := NewSettingHandler(settingService, buildInfo.Version)
	h.SetUserService(userService)
	h.SetNotificationEmailService(notificationEmailService)
	return h
}

// ProvideAdminSettingHandler creates admin.SettingHandler with notification template APIs.
func ProvideAdminSettingHandler(settingService *service.SettingService, emailService *service.EmailService, turnstileService *service.TurnstileService, aliyunCaptchaService *service.AliyunCaptchaService, opsService *service.OpsService, paymentConfigService *service.PaymentConfigService, paymentService *service.PaymentService, userAttributeService *service.UserAttributeService, notificationEmailService *service.NotificationEmailService, totpService *service.TotpService, userService *service.UserService) *admin.SettingHandler {
	h := admin.NewSettingHandler(settingService, emailService, turnstileService, opsService, paymentConfigService, paymentService, userAttributeService)
	h.SetNotificationEmailService(notificationEmailService)
	h.SetAliyunCaptchaService(aliyunCaptchaService)
	h.SetStepUpDeps(totpService, userService)
	return h
}

// ProvideAdminAnnouncementHandler creates the admin announcement handler and
// injects step-up dependencies (publishing an announcement requires 2FA).
func ProvideAdminAnnouncementHandler(
	announcementService *service.AnnouncementService,
	totpService *service.TotpService,
	userService *service.UserService,
	settingService *service.SettingService,
) *admin.AnnouncementHandler {
	h := admin.NewAnnouncementHandler(announcementService)
	h.SetStepUpDependencies(totpService, userService, settingService)
	return h
}

// ProvideAdminGroupHandler creates the admin group handler and injects step-up
// dependencies (分组改价 / search_provider_api_key 写入需要 2FA)。
func ProvideAdminGroupHandler(
	adminService service.AdminService,
	dashboardService *service.DashboardService,
	groupCapacityService *service.GroupCapacityService,
	cfg *config.Config,
	totpService *service.TotpService,
	userService *service.UserService,
	settingService *service.SettingService,
) *admin.GroupHandler {
	h := admin.NewGroupHandlerWithConfig(adminService, dashboardService, groupCapacityService, cfg)
	h.SetStepUpDependencies(totpService, userService, settingService)
	return h
}

// ProvideHandlers creates the Handlers struct
func ProvideHandlers(
	authHandler *AuthHandler,
	userHandler *UserHandler,
	apiKeyHandler *APIKeyHandler,
	usageHandler *UsageHandler,
	redeemHandler *RedeemHandler,
	subscriptionHandler *SubscriptionHandler,
	announcementHandler *AnnouncementHandler,
	ticketHandler *TicketHandler,
	channelMonitorUserHandler *ChannelMonitorUserHandler,
	channelMonitorV2Handler *ChannelMonitorV2Handler,
	adminHandlers *AdminHandlers,
	gatewayHandler *GatewayHandler,
	openaiGatewayHandler *OpenAIGatewayHandler,
	settingHandler *SettingHandler,
	totpHandler *TotpHandler,
	passkeyHandler *PasskeyHandler,
	paymentHandler *PaymentHandler,
	paymentWebhookHandler *PaymentWebhookHandler,
	availableChannelHandler *AvailableChannelHandler,
	modelPlazaHandler *ModelPlazaHandler,
	asyncImageHandler *AsyncImageHandler,
	batchImageHandler *BatchImageHandler,
	cryptoHandler *CryptoHandler,
	cryptoDepositHandler *CryptoDepositHandler,
	barkHandler *UserBarkHandler,
	keyRotationHandler *KeyRotationHandler,
	ipWhitelistAssistHandler *IPWhitelistAssistHandler,
	_ *service.IdempotencyCoordinator,
	_ *service.IdempotencyCleanupService,
	_ *service.OpenAIQuotaAutoResetService,
) *Handlers {
	return &Handlers{
		Auth:              authHandler,
		User:              userHandler,
		APIKey:            apiKeyHandler,
		Usage:             usageHandler,
		Redeem:            redeemHandler,
		Subscription:      subscriptionHandler,
		Announcement:      announcementHandler,
		Ticket:            ticketHandler,
		ChannelMonitor:    channelMonitorUserHandler,
		ChannelMonitorV2:  channelMonitorV2Handler,
		Admin:             adminHandlers,
		Gateway:           gatewayHandler,
		OpenAIGateway:     openaiGatewayHandler,
		Setting:           settingHandler,
		Totp:              totpHandler,
		Passkey:           passkeyHandler,
		Payment:           paymentHandler,
		PaymentWebhook:    paymentWebhookHandler,
		AvailableChannel:  availableChannelHandler,
		ModelPlaza:        modelPlazaHandler,
		AsyncImage:        asyncImageHandler,
		BatchImage:        batchImageHandler,
		Crypto:            cryptoHandler,
		CryptoDeposit:     cryptoDepositHandler,
		Bark:              barkHandler,
		KeyRotation:       keyRotationHandler,
		IPWhitelistAssist: ipWhitelistAssistHandler,
	}
}

// ProvideUserHandler creates the user handler and injects the five-minute
// two-factor grant gate used by password-verification entries (change password /
// bind email). Keeping NewUserHandler's signature unchanged preserves the many
// tests that construct it directly.
func ProvideUserHandler(
	userService *service.UserService,
	authService *service.AuthService,
	emailService *service.EmailService,
	emailCache service.EmailCache,
	affiliateService *service.AffiliateService,
	userPlatformQuotaRepo service.UserPlatformQuotaRepository,
	totpService *service.TotpService,
) *UserHandler {
	h := NewUserHandler(userService, authService, emailService, emailCache, affiliateService, userPlatformQuotaRepo)
	h.SetTotpService(totpService)
	return h
}

// ProvideAuthHandler 在原有构造之后注入 Passkey 服务：密码登录的双因子
// 编排需要 Passkey assertion 挑战/校验。NewAuthHandler 的签名保持不变，
// 避免大量既有测试构造点改动。
func ProvideAuthHandler(
	cfg *config.Config,
	authService *service.AuthService,
	userService *service.UserService,
	settingService *service.SettingService,
	promoService *service.PromoService,
	redeemService *service.RedeemService,
	totpService *service.TotpService,
	userAttributeService *service.UserAttributeService,
	passkeys *service.PasskeyService,
) *AuthHandler {
	h := NewAuthHandler(cfg, authService, userService, settingService, promoService, redeemService, totpService, userAttributeService)
	h.SetPasskeyService(passkeys)
	return h
}

// ProvidePasskeyHandler 在原有构造之后注入 TotpService/UserService：
//   - Passkey step-up 需要写入 Passkey 因子的 5 分钟授权并签发同格式不透明凭证；
//   - Passkey 创建/删除需要 5 分钟双因子门禁（账号未绑定 2FA 因子时不受影响）。
//
// NewPasskeyHandler 的签名保持不变，避免大量既有测试构造点改动。
func ProvidePasskeyHandler(
	passkeys *service.PasskeyService,
	authService *service.AuthService,
	settingService *service.SettingService,
	totpService *service.TotpService,
	userService *service.UserService,
) *PasskeyHandler {
	h := NewPasskeyHandler(passkeys, authService, settingService)
	h.SetStepUpDependencies(totpService, userService)
	h.SetTotpService(totpService)
	return h
}

// ProvideAPIKeyHandler 在原有构造之后注入三选一身份校验服务：
//   - 创建 Key 前必须完成 密码 / TOTP / Passkey 之一；
//   - 二次查看封存密文前的 reveal-authorize 与 grant 校验；
//   - Passkey 校验挑战与一次性票据。
//
// NewAPIKeyHandler 的签名保持不变，避免大量既有测试构造点改动。
func ProvideAPIKeyHandler(
	apiKeyService *service.APIKeyService,
	secretService *service.APIKeySecretService,
	verificationService *service.APIKeyVerificationService,
	personalAPIKeyService *service.UserPersonalAPIKeyService,
) *APIKeyHandler {
	h := NewAPIKeyHandler(apiKeyService, secretService)
	h.SetVerificationService(verificationService)
	h.SetPersonalAPIKeyService(personalAPIKeyService)
	return h
}

// ProviderSet is the Wire provider set for all handlers
var ProviderSet = wire.NewSet(
	// Top-level handlers
	ProvideAuthHandler,
	ProvideUserHandler,
	ProvideAPIKeyHandler,
	NewUsageHandler,
	NewRedeemHandler,
	NewSubscriptionHandler,
	NewAnnouncementHandler,
	NewTicketHandler,
	NewChannelMonitorUserHandler,
	NewChannelMonitorV2Handler,
	ProvideGatewayHandler,
	ProvideOpenAIGatewayHandler,
	NewTotpHandler,
	ProvidePasskeyHandler,
	ProvideSettingHandler,
	NewPaymentHandler,
	NewPaymentWebhookHandler,
	NewUserBarkHandler,
	NewKeyRotationHandler,
	NewIPWhitelistAssistHandler,
	NewAvailableChannelHandler,
	NewModelPlazaHandler,
	NewAsyncImageHandler,
	ProvideBatchImageHandler,

	// Admin handlers
	admin.NewDashboardHandler,
	admin.NewUserHandler,
	ProvideAdminGroupHandler,
	admin.ProvideAccountHandler,
	ProvideAdminAnnouncementHandler,
	admin.NewDataManagementHandler,
	admin.NewBackupHandler,
	admin.NewOAuthHandler,
	admin.NewOpenAIOAuthHandler,
	admin.NewGeminiOAuthHandler,
	admin.NewAntigravityOAuthHandler,
	admin.NewGrokOAuthHandler,
	admin.NewCNProviderHandler,
	admin.NewProxyHandler,
	admin.NewRedeemHandler,
	admin.NewPromoHandler,
	ProvideAdminSettingHandler,
	admin.NewOpsHandler,
	ProvideSystemHandler,
	admin.NewSubscriptionHandler,
	admin.NewUsageHandler,
	admin.NewUserAttributeHandler,
	admin.NewErrorPassthroughHandler,
	admin.NewTLSFingerprintProfileHandler,
	admin.NewPluginHandler,
	admin.NewAdminAPIKeyHandler,
	admin.NewScheduledTestHandler,
	admin.NewChannelHandler,
	ProvideAccountOpsHandler,
	ProvideMihomoHandler,
	ProvideCredentialGuardHandler,
	admin.NewOpsAutomationHandler,
	ProvideExcelGrantHandler,
	admin.NewChannelMonitorHandler,
	admin.NewChannelMonitorRequestTemplateHandler,
	admin.NewContentModerationHandler,
	admin.NewPaymentHandler,
	admin.NewAffiliateHandler,
	admin.NewComplianceHandler,
	admin.NewAuditLogHandler,
	admin.NewGroupQualityCheckHandler,

	// AdminHandlers and Handlers constructors
	ProvideAdminHandlers,
	ProvideHandlers,
)
