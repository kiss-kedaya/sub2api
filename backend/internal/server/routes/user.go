package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// RegisterUserRoutes 注册用户相关路由（需要认证）
func RegisterUserRoutes(
	v1 *gin.RouterGroup,
	h *handler.Handlers,
	jwtAuth middleware.JWTAuthMiddleware,
	auditLog middleware.AuditLogMiddleware,
	keyRotationGate middleware.KeyRotationGateMiddleware,
	settingService *service.SettingService,
	panelRateLimiter *middleware.PanelRateLimiter,
) {
	authenticated := v1.Group("")
	authenticated.Use(gin.HandlerFunc(jwtAuth))
	authenticated.Use(middleware.BackendModeUserGuard(settingService))
	// 面板全局按用户限流：防止单个账号高频刷接口打爆数据库
	authenticated.Use(panelRateLimiter.Global())
	authenticated.Use(middleware.TablePagination(settingService))
	// 用户管理面变更类操作入审计（含 TOTP 启用/禁用、step-up 验证、密码修改等安全事件）
	authenticated.Use(gin.HandlerFunc(auditLog))
	registerTicketRoutes(authenticated, h.Ticket)
	{
		// 用户接口
		user := authenticated.Group("/user")
		{
			user.GET("/profile", h.User.GetProfile)
			if h.Admin != nil && h.Admin.PromptAudit != nil {
				user.GET("/security/summary", h.Admin.PromptAudit.MyPenaltySummary)
				user.GET("/security/events", h.Admin.PromptAudit.MyPenaltyEvents)
			}
			user.GET("/custom-endpoints", h.Setting.GetCustomEndpoints)
			// IP 白名单助手：返回当前访问者的可信客户端 IP、归属地和最近常用 IP。
			// 与密钥 IP ACL 使用同一套客户端 IP 解析逻辑。
			if h.IPWhitelistAssist != nil {
				user.GET("/ip-whitelist-assist", h.IPWhitelistAssist.Get)
			}
			user.PUT("/password", h.User.ChangePassword)
			user.PUT("", h.User.UpdateProfile)
			user.GET("/aff", h.User.GetAffiliate)
			user.POST("/aff/transfer", h.User.TransferAffiliateQuota)
			user.POST("/aff/codes", h.User.CreateAffiliateCode)
			user.DELETE("/aff/codes/:id", h.User.DeleteAffiliateCode)
			user.POST("/account-bindings/email/send-code", h.User.SendEmailBindingCode)
			user.POST("/account-bindings/email", h.User.BindEmailIdentity)
			user.DELETE("/account-bindings/:provider", h.User.UnbindIdentity)
			user.POST("/auth-identities/bind/start", h.User.StartIdentityBinding)
			// API Key 三选一身份校验（登录密码 / TOTP / Passkey）：
			//   - GET  factors            该账号实际可用的因子与推荐顺序；
			//   - POST passkey verify /*  Passkey assertion → 一次性校验票据；
			//   - POST reveal-authorize   二次查看授权 → 5 分钟一次性 reveal grant。
			// 校验接口统一按用户 10 次/分钟限流（创建 / 单条轮换 / 批量轮换 / 二次查看授权 /
			// Passkey 校验共用一个桶），避免成为密码/动态码在线爆破入口。
			user.GET("/security/verification-factors", h.APIKey.GetVerificationFactors)
			// 用户自己决定「创建 / 轮换 API Key 时要不要先验证身份」（on / off / auto）。
			// 只在降低保护强度（on > auto > off 的任何一步下降）时要求一次三选一校验，与创建 Key 共用同一个 10 次/分钟的按用户限流桶
			// 和同一份密码失败预算，避免成为密码 / 动态码的另一个在线爆破入口。
			user.PUT("/security/api-key-create-verification", panelRateLimiter.KeyVerification(), h.APIKey.SetAPIKeyCreateVerification)
			user.POST("/security/passkey/verify/begin", panelRateLimiter.KeyVerification(), h.APIKey.BeginKeyPasskeyVerification)
			user.POST("/security/passkey/verify/finish", panelRateLimiter.KeyVerification(), h.APIKey.FinishKeyPasskeyVerification)
			// 用户个人接口密钥（Personal API Key）：仅限交互式 JWT 会话管理，
			// 创建/重置强制走三选一身份校验并与 KeyVerification 共用 10 次/分钟限流桶。
			user.GET("/personal-api-key", h.APIKey.GetPersonalAPIKey)
			user.POST("/personal-api-key", panelRateLimiter.KeyVerification(), h.APIKey.CreateOrRegeneratePersonalAPIKey)
			user.DELETE("/personal-api-key", h.APIKey.DeletePersonalAPIKey)
			user.POST("/api-keys/:id/reveal-authorize", panelRateLimiter.KeyVerification(), h.APIKey.AuthorizeReveal)
			// 浏览器端封存 API Key 密文：服务端只存取、不解密；封存请求会带登录密码，只用于 bcrypt 校验
			// （见 api_key_secret_handler.go），不落库、不进日志、不进审计。
			// PUT 限制 8KB 请求体并在封存前校验登录密码（每用户 60 次/分钟，另有按用户的
			// 密码失败预算兜底口令猜测）；GET 需要 reveal grant，另有每用户 20 次/分钟的固定限流。
			user.PUT("/api-keys/:id/secret-seal", panelRateLimiter.SecretSealWrite(), middleware.RequestBodyLimit(service.APIKeySecretMaxBodyBytes), h.APIKey.PutSecretSeal)
			user.GET("/api-keys/:id/secret-seal", panelRateLimiter.SecretSealRead(), h.APIKey.GetSecretSeal)
			user.DELETE("/api-keys/:id/secret-seal", h.APIKey.DeleteSecretSeal)
			user.GET("/api-keys/:id/usage/daily", panelRateLimiter.Heavy(), h.Usage.GetMyAPIKeyDailyUsage)
			user.GET("/api-keys/:id/usage", panelRateLimiter.Heavy(), h.Usage.GetMyAPIKeyUsage)
			user.GET("/platform-quotas", h.User.GetMyPlatformQuotas)
			// 密钥轮换门禁状态：前端充值 / 兑换 / USDT 页进入时读取，
			// 用于在提交前展示整改提示卡片。该接口本身不拦截。
			if h.KeyRotation != nil {
				user.GET("/key-rotation-gate", h.KeyRotation.GetStatus)
				// 显式风险确认：不愿轮换 Key / 加 IP 白名单的用户二次确认后
				// 在有效期内继续充值/兑换（写入审计 KEY_ROTATION_RISK_ACK）。
				user.POST("/security/key-rotation-risk-ack", h.KeyRotation.AcknowledgeRisk)
			}

			// 通知邮箱管理
			notifyEmail := user.Group("/notify-email")
			{
				notifyEmail.POST("/send-code", h.User.SendNotifyEmailCode)
				notifyEmail.POST("/verify", h.User.VerifyNotifyEmail)
				notifyEmail.PUT("/toggle", h.User.ToggleNotifyEmail)
				notifyEmail.DELETE("", h.User.RemoveNotifyEmail)
			}
			if h.Bark != nil {
				bark := user.Group("/bark")
				{
					bark.GET("", h.Bark.GetBarkSettings)
					bark.PUT("", h.Bark.UpdateBarkSettings)
				}
			}

			// TOTP 双因素认证
			totp := user.Group("/totp")
			{
				totp.GET("/status", h.Totp.GetStatus)
				totp.GET("/verification-method", h.Totp.GetVerificationMethod)
				totp.POST("/send-code", h.Totp.SendVerifyCode)
				totp.POST("/setup", h.Totp.InitiateSetup)
				totp.POST("/enable", h.Totp.Enable)
				totp.POST("/disable", h.Totp.Disable)
				// 敏感操作二次验证：授予当前会话一段时间的 TOTP 因子 step-up 权限
				totp.POST("/step-up", h.Totp.StepUp)
			}

			// 双因子策略：前端弹窗前读取「该账号必须完成哪些因子」。
			user.GET("/2fa/policy", h.Totp.GetPolicy)

			// 敏感操作 Passkey step-up：与 /user/totp/step-up 平行的 Passkey 因子入口。
			stepUp := user.Group("/step-up")
			{
				stepUp.POST("/passkey/begin", h.Passkey.StepUpBegin)
				stepUp.POST("/passkey/finish", h.Passkey.StepUpFinish)
			}

			passkeys := user.Group("/passkeys")
			{
				passkeys.GET("", h.Passkey.List)
				passkeys.POST("/register/begin", h.Passkey.BeginRegistration)
				passkeys.POST("/register/finish", h.Passkey.FinishRegistration)
				passkeys.PATCH("/:id", h.Passkey.Rename)
				passkeys.DELETE("/:id", h.Passkey.Delete)
			}
		}

		// API Key管理
		keys := authenticated.Group("/keys")
		{
			keys.GET("", h.APIKey.List)
			keys.GET("/concurrency", h.APIKey.GetConcurrencyQueue)
			// 一屏概览：近 24h 调用次数 / 消耗额度，一次批量查询，避免逐行 N+1。
			keys.GET("/overview", panelRateLimiter.Heavy(), h.Usage.GetAPIKeysOverview)
			// 批量轮换：仅本人 Key、单次上限 50、逐条事务、逐条状态；
			// 会下发每把 Key 的新明文，和创建同样要身份校验，同档限流。
			keys.POST("/batch-rotate", panelRateLimiter.KeyVerification(), h.APIKey.BatchRotate)
			keys.GET("/:id", h.APIKey.GetByID)
			// 创建前必须完成一次身份校验（密码 / TOTP / Passkey 三选一），
			// 校验接口同档限流；账号一个可用因子都没有时服务端放行一次性显示。
			keys.POST("", panelRateLimiter.KeyVerification(), h.APIKey.Create)
			// 单条轮换（regenerate_key=true）同样下发新明文：只有这类请求计入校验档的限流，
			// 改名 / 启停 / 配额等不下发明文的编辑不受影响。
			keys.PUT("/:id", panelRateLimiter.KeyVerificationOnRotation(), h.APIKey.Update)
			keys.POST("/:id/block-datacenter-ips", h.APIKey.BlockDatacenterIPs)
			keys.DELETE("/:id", h.APIKey.Delete)
		}

		// 用户可用分组（非管理员接口）
		groups := authenticated.Group("/groups")
		{
			groups.GET("/available", h.APIKey.GetAvailableGroups)
			groups.GET("/rates", h.APIKey.GetUserGroupRates)
		}

		// 用户可用渠道（非管理员接口）
		channels := authenticated.Group("/channels")
		{
			channels.GET("/available", h.AvailableChannel.List)
		}

		// 使用记录（聚合统计属重查询，叠加更严格的按用户限流）
		usage := authenticated.Group("/usage")
		usage.GET("/traffic", h.Usage.Traffic)
		usage.GET("/traffic/keys", h.Usage.TrafficKeys)
		usage.Use(panelRateLimiter.Heavy())
		{
			usage.GET("", h.Usage.List)
			usage.GET("/errors", h.Usage.ListErrors)
			usage.GET("/errors/:id", h.Usage.GetErrorDetail)
			usage.GET("/:id", h.Usage.GetByID)
			usage.GET("/stats", h.Usage.Stats)
			// User dashboard endpoints
			usage.GET("/dashboard/stats", h.Usage.DashboardStats)
			usage.GET("/dashboard/trend", h.Usage.DashboardTrend)
			usage.GET("/dashboard/models", h.Usage.DashboardModels)
			usage.GET("/dashboard/snapshot-v2", h.Usage.DashboardSnapshotV2)
			usage.POST("/dashboard/api-keys-usage", h.Usage.DashboardAPIKeysUsage)
		}

		// 公告（用户可见）
		announcements := authenticated.Group("/announcements")
		{
			announcements.GET("", h.Announcement.List)
			announcements.GET("/summary", h.Announcement.Summary)
			announcements.GET("/popups", h.Announcement.Popups)
			announcements.GET("/item/:id", h.Announcement.Get)
			announcements.POST("/:id/read", h.Announcement.MarkRead)
		}

		// 卡密兑换
		// 密钥轮换门禁：未完成「更换 Key / 设置 IP 白名单」的用户禁止兑换，
		// 兑换历史查询同样拦截，避免绕过前端直接读兑换面数据。
		redeem := authenticated.Group("/redeem")
		applyKeyRotationGate(redeem, keyRotationGate)
		{
			redeem.POST("", h.Redeem.Redeem)
			redeem.GET("/history", h.Redeem.GetHistory)
		}

		// 用户订阅
		subscriptions := authenticated.Group("/subscriptions")
		{
			subscriptions.GET("", h.Subscription.List)
			subscriptions.GET("/active", h.Subscription.GetActive)
			subscriptions.GET("/progress", h.Subscription.GetProgress)
			subscriptions.GET("/summary", h.Subscription.GetSummary)
		}

		// 渠道监控（用户只读）
		monitors := authenticated.Group("/channel-monitors")
		{
			monitors.GET("", h.ChannelMonitor.List)
			monitors.GET("/:id/status", h.ChannelMonitor.GetStatus)
		}

		// V2 passive views require feature on + mode=v2.
		monitorV2 := authenticated.Group("/channel-monitor-v2")
		monitorV2.Use(panelRateLimiter.Heavy())
		monitorV2.Use(channelMonitorModeV2Guard(settingService))
		{
			monitorV2.GET("/dimensions", h.ChannelMonitorV2.Dimensions)
			monitorV2.GET("/snapshot", h.ChannelMonitorV2.Snapshot)
			monitorV2.GET("/models", h.ChannelMonitorV2.Models)
			monitorV2.GET("/matrix", h.ChannelMonitorV2.Matrix)
			monitorV2.GET("/errors", h.ChannelMonitorV2.Errors)
			monitorV2.GET("/users", h.ChannelMonitorV2.Users)
			monitorV2.GET("/quality-events", h.ChannelMonitorV2.QualityEvents)
			monitorV2.GET("/quality-events/:id/artwork", h.ChannelMonitorV2.QualityArtwork)
		}
	}
}
