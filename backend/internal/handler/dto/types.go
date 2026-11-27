package dto

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type User struct {
	ID            int64      `json:"id"`
	Email         string     `json:"email"`
	Username      string     `json:"username"`
	Role          string     `json:"role"`
	Balance       float64    `json:"balance"`
	FrozenBalance float64    `json:"frozen_balance"`
	Concurrency   int        `json:"concurrency"`
	Status        string     `json:"status"`
	AllowedGroups []int64    `json:"allowed_groups"`
	LastActiveAt  *time.Time `json:"last_active_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	DeletedAt     *time.Time `json:"deleted_at,omitempty"`

	// 余额不足通知
	BalanceNotifyEnabled       bool               `json:"balance_notify_enabled"`
	BalanceNotifyThresholdType string             `json:"balance_notify_threshold_type"`
	BalanceNotifyThreshold     *float64           `json:"balance_notify_threshold"`
	BalanceNotifyExtraEmails   []NotifyEmailEntry `json:"balance_notify_extra_emails"`
	TotalRecharged             float64            `json:"total_recharged"`

	// RPMLimit 用户级每分钟请求数上限（0 = 不限制），仅在所用分组未设置 rpm_limit 时作为兜底生效。
	RPMLimit int `json:"rpm_limit"`

	APIKeys       []APIKey           `json:"api_keys,omitempty"`
	Subscriptions []UserSubscription `json:"subscriptions,omitempty"`
}

// AdminUser 是管理员接口使用的 user DTO（包含敏感/内部字段）。
// 注意：普通用户接口不得返回 notes 等管理员备注信息。
type AdminUser struct {
	User

	Notes      string     `json:"notes"`
	LastUsedAt *time.Time `json:"last_used_at"`
	// GroupRates 用户专属分组倍率配置
	// map[groupID]rateMultiplier
	GroupRates map[int64]float64 `json:"group_rates,omitempty"`
	// RestrictPublicGroups 为 true 时，该用户仅可使用 allowed_groups 中列出的
	// 公开分组。这是管理侧的权限开关，不下发给用户自身的接口。
	RestrictPublicGroups bool `json:"restrict_public_groups"`
}

// APIKey 是 API Key 的对外展示模型。
//
// 明文 key 只有两个出口：创建和轮换的当次响应（见 APIKeySecret）。
// 列表、详情、管理员视图、导出、usage 等所有其它出口一律只带
// KeyPrefix（前 8 位）与 KeyMasked（前 8 + **** + 后 4）。
// 这里刻意不存在 Key 字段：脱敏发生在 DTO 边界，任何映射疏漏都无法
// 把明文塞进响应。
type APIKey struct {
	ConcurrencyLimit int     `json:"concurrency_limit"`
	ID               int64   `json:"id"`
	UserID           int64   `json:"user_id"`
	KeyPrefix        string  `json:"key_prefix"`
	KeyMasked        string  `json:"key_masked"`
	Name             string  `json:"name"`
	GroupID          *int64  `json:"group_id"`
	GroupIDs         []int64 `json:"group_ids,omitempty"`
	// RouteMode 是管理端列表展示用的分组路由模式：none / single / smart。
	// 判定与网关选路同源（service.APIKey.RouteMode → CandidateGroupIDs），
	// 前端不再自己数 group_ids，旧服务缺字段时前端回退到 group_ids。
	RouteMode string `json:"route_mode"`
	// RouteGroupCount 是去重后的候选分组数量（主分组 + 备用路由）。
	RouteGroupCount int `json:"route_group_count"`
	// IsSmartRouting 等价于 route_mode == smart，保留布尔字段方便徽章直接渲染。
	IsSmartRouting bool       `json:"is_smart_routing"`
	Status         string     `json:"status"`
	IPWhitelist    []string   `json:"ip_whitelist"`
	IPBlacklist    []string   `json:"ip_blacklist"`
	LastUsedAt     *time.Time `json:"last_used_at"`
	LastUsedIP     *string    `json:"last_used_ip"`
	Quota          float64    `json:"quota"`      // Quota limit in USD (0 = unlimited)
	QuotaUsed      float64    `json:"quota_used"` // Used quota amount in USD
	ExpiresAt      *time.Time `json:"expires_at"` // Expiration time (nil = never expires)
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	KeyRotatedAt   *time.Time `json:"key_rotated_at,omitempty"` // Last successful credential replacement, not a metadata edit.
	// NeedsRotation 为 true 时表示这把启用中的 Key 仍在充值/兑换门禁的待整改
	// 名单里（创建/轮换时间早于门槛且没有 IP 白名单）。判定与门禁同源。
	NeedsRotation bool `json:"needs_rotation"`
	// HasSealedSecret 表示这把 Key 有浏览器端封存的密文，用户可以在输入登录
	// 密码后重新查看明文。它只暴露布尔值：密文本体只有 GET secret-seal 才能取。
	HasSealedSecret bool `json:"has_sealed_secret"`
	// SealAuthFactor 是封存时授权的因子（password/totp/passkey）；空串表示
	// 改造前的旧密文（无因子记录，查看时不限制因子）。前端据此预选同一因子。
	SealAuthFactor string `json:"seal_auth_factor,omitempty"`
	// RotationReasons 是待整改原因分类：created_before_cutoff / no_ip_allowlist。
	RotationReasons []string `json:"rotation_reasons,omitempty"`
	// CurrentConcurrency is the real-time active request count for this API key.
	CurrentConcurrency int `json:"current_concurrency"`

	// Rate limit fields
	RateLimit5h float64 `json:"rate_limit_5h"`
	RateLimit1d float64 `json:"rate_limit_1d"`
	RateLimit7d float64 `json:"rate_limit_7d"`
	// AnthropicCacheTTLMode 密钥级 Anthropic cache_control TTL 覆盖：inherit/off/1h/5m。
	AnthropicCacheTTLMode        string `json:"anthropic_cache_ttl_mode"`
	DeepSeekToolInjectionEnabled bool   `json:"deepseek_tool_injection_enabled"`
	// AutoBlockDatacenterIPs 打开后由后台定时任务追加机房/代理 IP 到黑名单。
	AutoBlockDatacenterIPs bool       `json:"auto_block_datacenter_ips"`
	Usage5h                float64    `json:"usage_5h"`
	Usage1d                float64    `json:"usage_1d"`
	Usage7d                float64    `json:"usage_7d"`
	Window5hStart          *time.Time `json:"window_5h_start"`
	Window1dStart          *time.Time `json:"window_1d_start"`
	Window7dStart          *time.Time `json:"window_7d_start"`
	Reset5hAt              *time.Time `json:"reset_5h_at,omitempty"`
	Reset1dAt              *time.Time `json:"reset_1d_at,omitempty"`
	Reset7dAt              *time.Time `json:"reset_7d_at,omitempty"`

	// RiskDisabled 表示这把 key 是被网关风控自动停用的（而不是用户手动停用）。
	// 用户端据此展示"此 key 有被盗用风险"提示与轮换按钮。
	RiskDisabled bool       `json:"risk_disabled"`
	RiskHitIP    *string    `json:"risk_hit_ip,omitempty"`
	RiskHitAt    *time.Time `json:"risk_hit_at,omitempty"`

	User  *User  `json:"user,omitempty"`
	Group *Group `json:"group,omitempty"`
}

// APIKeyUsageOverview 是一屏概览里「近 24h」的批量统计项。
// 只对 Key 的所有者返回，且只有计数与用户口径费用，不含账号成本/模型/IP。
type APIKeyUsageOverview struct {
	ID          int64   `json:"id"`
	Requests24h int64   `json:"requests_24h"`
	Cost24h     float64 `json:"cost_24h"`
}

// APIKeySecret 是「密钥明文唯一一次出口」的响应模型：只在创建（POST /api/v1/keys）// 与轮换（PUT /api/v1/keys/:id 且 regenerate_key=true）的当次响应里使用。
// 其它任何接口都必须返回 APIKey（只含 key_prefix / key_masked）。
// 新代码不要复用这个类型，除非确实是"刚刚生成/替换了凭据"的那一次回执。
type APIKeySecret struct {
	APIKey
	Key string `json:"key"`
}

// ToAPIKey 丢弃明文，只保留脱敏视图（需要把创建结果并回列表时使用）。
func (s *APIKeySecret) ToAPIKey() *APIKey {
	if s == nil {
		return nil
	}
	base := s.APIKey
	return &base
}

type Group struct {
	ID             int64   `json:"id"`
	Name           string  `json:"name"`
	Description    string  `json:"description"`
	Platform       string  `json:"platform"`
	RateMultiplier float64 `json:"rate_multiplier"`
	IsExclusive    bool    `json:"is_exclusive"`
	Status         string  `json:"status"`

	SubscriptionType          string   `json:"subscription_type"`
	DailyLimitUSD             *float64 `json:"daily_limit_usd"`
	WeeklyLimitUSD            *float64 `json:"weekly_limit_usd"`
	MonthlyLimitUSD           *float64 `json:"monthly_limit_usd"`
	LongContextPricingEnabled bool     `json:"long_context_pricing_enabled"`

	// 图片生成计费配置（仅 antigravity 平台使用）
	AllowImageGeneration         bool    `json:"allow_image_generation"`
	AllowBatchImageGeneration    bool    `json:"allow_batch_image_generation"`
	ImageRateIndependent         bool    `json:"image_rate_independent"`
	ImageRateMultiplier          float64 `json:"image_rate_multiplier"`
	BatchImageDiscountMultiplier float64 `json:"batch_image_discount_multiplier"`
	BatchImageHoldMultiplier     float64 `json:"batch_image_hold_multiplier"`
	VideoRateIndependent         bool    `json:"video_rate_independent"`
	VideoRateMultiplier          float64 `json:"video_rate_multiplier"`
	// 高峰时段倍率配置
	PeakRateEnabled    bool     `json:"peak_rate_enabled"`
	PeakStart          string   `json:"peak_start"`
	PeakEnd            string   `json:"peak_end"`
	PeakRateMultiplier float64  `json:"peak_rate_multiplier"`
	ImagePrice1K       *float64 `json:"image_price_1k"`
	ImagePrice2K       *float64 `json:"image_price_2k"`
	ImagePrice4K       *float64 `json:"image_price_4k"`
	VideoPrice480P     *float64 `json:"video_price_480p"`
	VideoPrice720P     *float64 `json:"video_price_720p"`
	VideoPrice1080P    *float64 `json:"video_price_1080p"`
	// VideoModelPrices 可选按模型族×分辨率覆盖视频每秒单价 (USD/s)。
	VideoModelPrices map[string]map[string]float64 `json:"video_model_prices,omitempty"`
	// Codex alpha/search 网页搜索单次价格（USD/次）；null 表示使用默认价 0.01
	WebSearchPricePerCall *float64 `json:"web_search_price_per_call"`
	ToolRateIndependent   bool     `json:"tool_rate_independent"`
	ToolRateMultiplier    float64  `json:"tool_rate_multiplier"`
	SearchPricePer1k      *float64 `json:"search_price_per_1k"`
	FetchPricePer1k       *float64 `json:"fetch_price_per_1k"`
	OCRPricePer1kPages    *float64 `json:"ocr_price_per_1k_pages"`
	// GrokModerationRefusalPrice Grok 安全拒绝单次扣费（USD/次）；nil 使用默认 0.1，0 表示不扣费。
	GrokModerationRefusalPrice   *float64 `json:"grok_moderation_refusal_price"`
	AudioRealtimePricePerMin     *float64 `json:"audio_realtime_price_per_min"`
	AudioTtsPricePerMillionChars *float64 `json:"audio_tts_price_per_million_chars"`
	AudioSttPricePerHour         *float64 `json:"audio_stt_price_per_hour"`

	// Claude Code 客户端限制
	ClaudeCodeOnly  bool   `json:"claude_code_only"`
	FallbackGroupID *int64 `json:"fallback_group_id"`
	// 无效请求兜底分组
	FallbackGroupIDOnInvalidRequest *int64 `json:"fallback_group_id_on_invalid_request"`

	// OpenAI Messages 调度开关（用户侧需要此字段判断是否展示 Claude Code 教程）
	AllowMessagesDispatch bool `json:"allow_messages_dispatch"`
	// 分组级跨协议转换开关（Anthropic 协议族 <-> OpenAI 协议族），默认 false。
	CrossProtocolConversionEnabled bool `json:"cross_protocol_conversion_enabled"`
	// OpenAI Live 接口开关
	AllowLive bool `json:"allow_live"`

	// 账号过滤控制（仅 OpenAI/Antigravity 平台有效）
	RequireOAuthOnly  bool `json:"require_oauth_only"`
	RequirePrivacySet bool `json:"require_privacy_set"`

	// RPMLimit 分组级每分钟请求数上限（0 = 不限制），设置后覆盖用户级 rpm_limit。
	RPMLimit int `json:"rpm_limit"`
	// MaxReasoningEffort Anthropic/OpenAI 请求的推理强度上限，空字符串表示不限制。
	MaxReasoningEffort string `json:"max_reasoning_effort"`
	// MaxReasoningEffortOverLimit 超过上限时的访问控制：downgrade（默认）或 deny。
	MaxReasoningEffortOverLimit string `json:"max_reasoning_effort_over_limit"`
	// ReasoningEffortMappings Anthropic/OpenAI 推理强度映射，可按模型精确名、前缀或后缀限定。
	ReasoningEffortMappings []domain.ReasoningEffortMapping `json:"reasoning_effort_mappings"`

	// Display hints for the viewer, filled only by GET /groups/available. They
	// never affect pricing. recent_rate_change is the latest base rate edit of
	// the last 48h and is absent when the viewer pays a personal rate; is_new,
	// launched_at and launch_rate describe a group created in the last 24h.
	RecentRateChange *GroupRecentRateChange `json:"recent_rate_change,omitempty"`
	IsNew            bool                   `json:"is_new,omitempty"`
	LaunchedAt       *time.Time             `json:"launched_at,omitempty"`
	LaunchRate       *float64               `json:"launch_rate,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// GroupRecentRateChange is the latest base rate edit of a group. delta is the
// absolute size of the change; direction ("up" or "down") carries the sign.
type GroupRecentRateChange struct {
	Direction    string    `json:"direction"`
	Delta        float64   `json:"delta"`
	PreviousRate float64   `json:"previous_rate"`
	ChangedAt    time.Time `json:"changed_at"`
}

// AdminGroup 是管理员接口使用的 group DTO（包含敏感/内部字段）。
// 注意：普通用户接口不得返回 model_routing/account_count/account_groups 等内部信息。
type AdminGroup struct {
	Group
	// ReturnRequestedModel controls response model metadata for this group.
	ReturnRequestedModel bool `json:"return_requested_model"`
	// SaveResponseMsgID controls persisting Claude/Anthropic response message IDs on usage logs.
	SaveResponseMsgID bool `json:"save_response_msg_id"`
	// 搜索上游配置仅供主管理员读取，普通分组及其嵌套 DTO 不携带。
	// 密钥本身永不返回，只暴露是否已配置。
	SearchProviderBaseURL   *string `json:"search_provider_base_url"`
	SearchProviderAPIKeySet bool    `json:"search_provider_api_key_set"`
	// ForceOpenAIFast 是管理端请求策略，用户侧分组 DTO 无需暴露。
	ForceOpenAIFast bool `json:"force_openai_fast"`
	// FreeOpenAIFast 是管理端计费策略，用户侧分组 DTO 无需暴露。
	FreeOpenAIFast bool `json:"free_openai_fast"`
	// OpenAIFastMultiplier / OpenAIUltrafastMultiplier 是管理端档位倍率（叠加在分组倍率上），null=官方档位价。
	OpenAIFastMultiplier      *float64 `json:"openai_fast_multiplier"`
	OpenAIUltrafastMultiplier *float64 `json:"openai_ultrafast_multiplier"`

	// 分组利润控制（五个 token 平台分组可启用；margin/buffer 为小数存储）。
	// 仅管理员可见：这三个字段与同响应中的 rate_multiplier 相乘即可反推出
	// 运营方的上游成本上限，属于内部经营信息，不得下放到 dto.Group。
	ProfitControlEnabled bool                          `json:"profit_control_enabled"`
	ProfitMinMargin      float64                       `json:"profit_min_margin"`
	ProfitSafetyBuffer   float64                       `json:"profit_safety_buffer"`
	ModelPricing         []service.ChannelModelPricing `json:"model_pricing"`

	// 模型路由配置（仅 anthropic 平台使用）
	ModelRouting        map[string][]int64 `json:"model_routing"`
	ModelRoutingEnabled bool               `json:"model_routing_enabled"`

	// MCP XML 协议注入（仅 antigravity 平台使用）
	MCPXMLInject bool `json:"mcp_xml_inject"`

	// OpenAI Messages 调度配置（仅 openai 平台使用）
	DefaultMappedModel          string                                   `json:"default_mapped_model"`
	MessagesDispatchModelConfig domain.OpenAIMessagesDispatchModelConfig `json:"messages_dispatch_model_config"`
	ModelAllowlist              service.GroupModelAllowlist              `json:"model_allowlist"`
	// 固定账号获取 Codex Model Manifest 配置（仅 openai 平台使用）。
	CodexModelsManifestConfig domain.GroupCodexModelsManifestConfig `json:"codex_models_manifest_config"`

	// 支持的模型系列（仅 antigravity 平台使用）
	SupportedModelScopes    []string       `json:"supported_model_scopes"`
	AccountGroups           []AccountGroup `json:"account_groups,omitempty"`
	AccountCount            int64          `json:"account_count,omitempty"`
	ActiveAccountCount      int64          `json:"active_account_count,omitempty"`
	RateLimitedAccountCount int64          `json:"rate_limited_account_count,omitempty"`

	// 分组排序
	SortOrder int `json:"sort_order"`
}

type Account struct {
	ID       int64   `json:"id"`
	Name     string  `json:"name"`
	Notes    *string `json:"notes"`
	Platform string  `json:"platform"`
	Type     string  `json:"type"`
	// Credentials 经 RedactCredentials 处理后只含非敏感子键；敏感 token / api_key / 私钥
	// 的存在性通过 CredentialsStatus（has_<key>）暴露，原始值不返回前端。
	Credentials             map[string]any                    `json:"credentials"`
	CredentialsStatus       map[string]bool                   `json:"credentials_status,omitempty"`
	Extra                   map[string]any                    `json:"extra"`
	OllamaCloudUsage        *service.OllamaCloudUsageState    `json:"ollama_cloud_usage,omitempty"`
	CodexTurnTickets        []service.OpenAICodexTicketStatus `json:"codex_turn_tickets,omitempty"`
	ProxyID                 *int64                            `json:"proxy_id"`
	ProxyFallbackOriginID   *int64                            `json:"proxy_fallback_origin_id"`
	ProxyFallbackOriginName *string                           `json:"proxy_fallback_origin_name,omitempty"`
	Concurrency             int                               `json:"concurrency"`
	LoadFactor              *int                              `json:"load_factor,omitempty"`
	Priority                int                               `json:"priority"`
	RateMultiplier          float64                           `json:"rate_multiplier"`
	Status                  string                            `json:"status"`
	ErrorMessage            string                            `json:"error_message"`
	LastUsedAt              *time.Time                        `json:"last_used_at"`
	ExpiresAt               *int64                            `json:"expires_at"`
	AutoPauseOnExpired      bool                              `json:"auto_pause_on_expired"`
	CreatedAt               time.Time                         `json:"created_at"`
	UpdatedAt               time.Time                         `json:"updated_at"`

	Schedulable bool `json:"schedulable"`

	RateLimitedAt    *time.Time `json:"rate_limited_at"`
	RateLimitResetAt *time.Time `json:"rate_limit_reset_at"`
	OverloadUntil    *time.Time `json:"overload_until"`

	TempUnschedulableUntil  *time.Time `json:"temp_unschedulable_until"`
	TempUnschedulableReason string     `json:"temp_unschedulable_reason"`

	SessionWindowStart  *time.Time `json:"session_window_start"`
	SessionWindowEnd    *time.Time `json:"session_window_end"`
	SessionWindowStatus string     `json:"session_window_status"`

	// 5h窗口费用控制（仅 Anthropic OAuth/SetupToken 账号有效）
	// 从 extra 字段提取，方便前端显示和编辑
	WindowCostLimit         *float64 `json:"window_cost_limit,omitempty"`
	WindowCostStickyReserve *float64 `json:"window_cost_sticky_reserve,omitempty"`

	// 会话数量控制（仅 Anthropic OAuth/SetupToken 账号有效）
	// 从 extra 字段提取，方便前端显示和编辑
	MaxSessions           *int `json:"max_sessions,omitempty"`
	SessionIdleTimeoutMin *int `json:"session_idle_timeout_minutes,omitempty"`

	// RPM 限制（仅 Anthropic OAuth/SetupToken 账号有效）
	// 从 extra 字段提取，方便前端显示和编辑
	BaseRPM          *int    `json:"base_rpm,omitempty"`
	RPMStrategy      *string `json:"rpm_strategy,omitempty"`
	RPMStickyBuffer  *int    `json:"rpm_sticky_buffer,omitempty"`
	UserMsgQueueMode *string `json:"user_msg_queue_mode,omitempty"`

	// TLS指纹伪装（仅 Anthropic OAuth/SetupToken 账号有效）
	// 从 extra 字段提取，方便前端显示和编辑
	EnableTLSFingerprint    *bool  `json:"enable_tls_fingerprint,omitempty"`
	TLSFingerprintProfileID *int64 `json:"tls_fingerprint_profile_id,omitempty"`

	// 会话ID伪装（仅 Anthropic OAuth/SetupToken 账号有效）
	// 启用后将在15分钟内固定 metadata.user_id 中的 session ID
	// 从 extra 字段提取，方便前端显示和编辑
	EnableSessionIDMasking *bool `json:"session_id_masking_enabled,omitempty"`

	// 缓存 TTL 强制替换（仅 Anthropic OAuth/SetupToken 账号有效）
	// 启用后将所有 cache creation tokens 归入指定的 TTL 类型计费
	CacheTTLOverrideEnabled *bool   `json:"cache_ttl_override_enabled,omitempty"`
	CacheTTLOverrideTarget  *string `json:"cache_ttl_override_target,omitempty"`

	// 自定义 Base URL 中继转发（仅 Anthropic OAuth/SetupToken 账号有效）
	CustomBaseURLEnabled *bool   `json:"custom_base_url_enabled,omitempty"`
	CustomBaseURL        *string `json:"custom_base_url,omitempty"`

	// API Key 账号配额限制
	QuotaLimit       *float64 `json:"quota_limit,omitempty"`
	QuotaUsed        *float64 `json:"quota_used,omitempty"`
	QuotaDailyLimit  *float64 `json:"quota_daily_limit,omitempty"`
	QuotaDailyUsed   *float64 `json:"quota_daily_used,omitempty"`
	QuotaWeeklyLimit *float64 `json:"quota_weekly_limit,omitempty"`
	QuotaWeeklyUsed  *float64 `json:"quota_weekly_used,omitempty"`

	// 配额固定时间重置配置
	QuotaDailyResetMode  *string `json:"quota_daily_reset_mode,omitempty"`
	QuotaDailyResetHour  *int    `json:"quota_daily_reset_hour,omitempty"`
	QuotaWeeklyResetMode *string `json:"quota_weekly_reset_mode,omitempty"`
	QuotaWeeklyResetDay  *int    `json:"quota_weekly_reset_day,omitempty"`
	QuotaWeeklyResetHour *int    `json:"quota_weekly_reset_hour,omitempty"`
	QuotaResetTimezone   *string `json:"quota_reset_timezone,omitempty"`
	QuotaDailyResetAt    *string `json:"quota_daily_reset_at,omitempty"`
	QuotaWeeklyResetAt   *string `json:"quota_weekly_reset_at,omitempty"`

	// 配额通知配置
	QuotaNotifyDailyEnabled    *bool    `json:"quota_notify_daily_enabled,omitempty"`
	QuotaNotifyDailyThreshold  *float64 `json:"quota_notify_daily_threshold,omitempty"`
	QuotaNotifyWeeklyEnabled   *bool    `json:"quota_notify_weekly_enabled,omitempty"`
	QuotaNotifyWeeklyThreshold *float64 `json:"quota_notify_weekly_threshold,omitempty"`
	QuotaNotifyTotalEnabled    *bool    `json:"quota_notify_total_enabled,omitempty"`
	QuotaNotifyTotalThreshold  *float64 `json:"quota_notify_total_threshold,omitempty"`

	// 影子账号关系（spark 维度影子）
	ParentAccountID *int64 `json:"parent_account_id,omitempty"`
	QuotaDimension  string `json:"quota_dimension,omitempty"`

	// 影子账号回填的母账号信息（仅影子非空，源自母账号 Credentials/Extra）
	ParentEmail                 string `json:"parent_email,omitempty"`
	ParentPlanType              string `json:"parent_plan_type,omitempty"`
	ParentPrivacyMode           string `json:"parent_privacy_mode,omitempty"`
	ParentSubscriptionExpiresAt string `json:"parent_subscription_expires_at,omitempty"`
	ParentChatGPTAccountID      string `json:"parent_chatgpt_account_id,omitempty"`

	Proxy         *Proxy         `json:"proxy,omitempty"`
	AccountGroups []AccountGroup `json:"account_groups,omitempty"`

	GroupIDs []int64  `json:"group_ids,omitempty"`
	Groups   []*Group `json:"groups,omitempty"`
}

// AccountListItem is the compact representation returned by the admin account
// list when the lite query parameter is enabled. It contains the fields used
// by the account table and runtime indicators, but intentionally omits the
// repeated account_groups and groups object graphs. Fetch /admin/accounts/:id
// for the complete Account DTO when editing or inspecting an account.
type AccountListItem struct {
	ID       int64   `json:"id"`
	Name     string  `json:"name"`
	Notes    *string `json:"notes"`
	Platform string  `json:"platform"`
	Type     string  `json:"type"`

	Credentials       map[string]any                    `json:"credentials,omitempty"`
	CredentialsStatus map[string]bool                   `json:"credentials_status,omitempty"`
	Extra             map[string]any                    `json:"extra,omitempty"`
	OllamaCloudUsage  *service.OllamaCloudUsageState    `json:"ollama_cloud_usage,omitempty"`
	CodexTurnTickets  []service.OpenAICodexTicketStatus `json:"codex_turn_tickets,omitempty"`

	ProxyID                 *int64     `json:"proxy_id"`
	ProxyFallbackOriginID   *int64     `json:"proxy_fallback_origin_id"`
	ProxyFallbackOriginName *string    `json:"proxy_fallback_origin_name,omitempty"`
	Concurrency             int        `json:"concurrency"`
	LoadFactor              *int       `json:"load_factor,omitempty"`
	Priority                int        `json:"priority"`
	RateMultiplier          float64    `json:"rate_multiplier"`
	Status                  string     `json:"status"`
	ErrorMessage            string     `json:"error_message"`
	LastUsedAt              *time.Time `json:"last_used_at"`
	ExpiresAt               *int64     `json:"expires_at"`
	AutoPauseOnExpired      bool       `json:"auto_pause_on_expired"`
	CreatedAt               time.Time  `json:"created_at"`
	UpdatedAt               time.Time  `json:"updated_at"`

	Schedulable bool `json:"schedulable"`

	RateLimitedAt    *time.Time `json:"rate_limited_at"`
	RateLimitResetAt *time.Time `json:"rate_limit_reset_at"`
	OverloadUntil    *time.Time `json:"overload_until"`

	TempUnschedulableUntil  *time.Time `json:"temp_unschedulable_until"`
	TempUnschedulableReason string     `json:"temp_unschedulable_reason"`

	SessionWindowStart  *time.Time `json:"session_window_start"`
	SessionWindowEnd    *time.Time `json:"session_window_end"`
	SessionWindowStatus string     `json:"session_window_status"`

	WindowCostLimit         *float64 `json:"window_cost_limit,omitempty"`
	WindowCostStickyReserve *float64 `json:"window_cost_sticky_reserve,omitempty"`
	MaxSessions             *int     `json:"max_sessions,omitempty"`
	SessionIdleTimeoutMin   *int     `json:"session_idle_timeout_minutes,omitempty"`
	BaseRPM                 *int     `json:"base_rpm,omitempty"`
	RPMStrategy             *string  `json:"rpm_strategy,omitempty"`
	RPMStickyBuffer         *int     `json:"rpm_sticky_buffer,omitempty"`
	UserMsgQueueMode        *string  `json:"user_msg_queue_mode,omitempty"`
	EnableTLSFingerprint    *bool    `json:"enable_tls_fingerprint,omitempty"`
	TLSFingerprintProfileID *int64   `json:"tls_fingerprint_profile_id,omitempty"`
	EnableSessionIDMasking  *bool    `json:"session_id_masking_enabled,omitempty"`
	CacheTTLOverrideEnabled *bool    `json:"cache_ttl_override_enabled,omitempty"`
	CacheTTLOverrideTarget  *string  `json:"cache_ttl_override_target,omitempty"`
	CustomBaseURLEnabled    *bool    `json:"custom_base_url_enabled,omitempty"`
	CustomBaseURL           *string  `json:"custom_base_url,omitempty"`

	QuotaLimit       *float64 `json:"quota_limit,omitempty"`
	QuotaUsed        *float64 `json:"quota_used,omitempty"`
	QuotaDailyLimit  *float64 `json:"quota_daily_limit,omitempty"`
	QuotaDailyUsed   *float64 `json:"quota_daily_used,omitempty"`
	QuotaWeeklyLimit *float64 `json:"quota_weekly_limit,omitempty"`
	QuotaWeeklyUsed  *float64 `json:"quota_weekly_used,omitempty"`

	QuotaDailyResetMode  *string `json:"quota_daily_reset_mode,omitempty"`
	QuotaDailyResetHour  *int    `json:"quota_daily_reset_hour,omitempty"`
	QuotaWeeklyResetMode *string `json:"quota_weekly_reset_mode,omitempty"`
	QuotaWeeklyResetDay  *int    `json:"quota_weekly_reset_day,omitempty"`
	QuotaWeeklyResetHour *int    `json:"quota_weekly_reset_hour,omitempty"`
	QuotaResetTimezone   *string `json:"quota_reset_timezone,omitempty"`
	QuotaDailyResetAt    *string `json:"quota_daily_reset_at,omitempty"`
	QuotaWeeklyResetAt   *string `json:"quota_weekly_reset_at,omitempty"`

	QuotaNotifyDailyEnabled    *bool    `json:"quota_notify_daily_enabled,omitempty"`
	QuotaNotifyDailyThreshold  *float64 `json:"quota_notify_daily_threshold,omitempty"`
	QuotaNotifyWeeklyEnabled   *bool    `json:"quota_notify_weekly_enabled,omitempty"`
	QuotaNotifyWeeklyThreshold *float64 `json:"quota_notify_weekly_threshold,omitempty"`
	QuotaNotifyTotalEnabled    *bool    `json:"quota_notify_total_enabled,omitempty"`
	QuotaNotifyTotalThreshold  *float64 `json:"quota_notify_total_threshold,omitempty"`

	ParentAccountID             *int64 `json:"parent_account_id,omitempty"`
	QuotaDimension              string `json:"quota_dimension,omitempty"`
	ParentEmail                 string `json:"parent_email,omitempty"`
	ParentPlanType              string `json:"parent_plan_type,omitempty"`
	ParentPrivacyMode           string `json:"parent_privacy_mode,omitempty"`
	ParentSubscriptionExpiresAt string `json:"parent_subscription_expires_at,omitempty"`
	ParentChatGPTAccountID      string `json:"parent_chatgpt_account_id,omitempty"`

	Proxy    *Proxy  `json:"proxy,omitempty"`
	GroupIDs []int64 `json:"group_ids,omitempty"`
}

type AccountGroup struct {
	AccountID int64     `json:"account_id"`
	GroupID   int64     `json:"group_id"`
	Priority  int       `json:"priority"`
	CreatedAt time.Time `json:"created_at"`

	Account *Account `json:"account,omitempty"`
	Group   *Group   `json:"group,omitempty"`
}

type Proxy struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Protocol  string    `json:"protocol"`
	Host      string    `json:"host"`
	Port      int       `json:"port"`
	Username  string    `json:"username"`
	Password  string    `json:"-"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	ExpiresAt      *time.Time `json:"expires_at"`
	FallbackMode   string     `json:"fallback_mode"`
	BackupProxyID  *int64     `json:"backup_proxy_id"`
	ExpiryWarnDays int        `json:"expiry_warn_days"`
}

type ProxyWithAccountCount struct {
	Proxy
	AccountCount   int64  `json:"account_count"`
	LatencyMs      *int64 `json:"latency_ms,omitempty"`
	LatencyStatus  string `json:"latency_status,omitempty"`
	LatencyMessage string `json:"latency_message,omitempty"`
	IPAddress      string `json:"ip_address,omitempty"`
	Country        string `json:"country,omitempty"`
	CountryCode    string `json:"country_code,omitempty"`
	Region         string `json:"region,omitempty"`
	City           string `json:"city,omitempty"`
	QualityStatus  string `json:"quality_status,omitempty"`
	QualityScore   *int   `json:"quality_score,omitempty"`
	QualityGrade   string `json:"quality_grade,omitempty"`
	QualitySummary string `json:"quality_summary,omitempty"`
	QualityChecked *int64 `json:"quality_checked,omitempty"`
}

// AdminProxy 是管理员接口使用的 proxy DTO（包含密码等敏感字段）。
// 注意：普通接口不得使用此 DTO。
type AdminProxy struct {
	Proxy
	Password string `json:"password,omitempty"`
}

// AdminProxyWithAccountCount 是管理员接口使用的带账号统计的 proxy DTO。
type AdminProxyWithAccountCount struct {
	AdminProxy
	AccountCount   int64  `json:"account_count"`
	LatencyMs      *int64 `json:"latency_ms,omitempty"`
	LatencyStatus  string `json:"latency_status,omitempty"`
	LatencyMessage string `json:"latency_message,omitempty"`
	IPAddress      string `json:"ip_address,omitempty"`
	Country        string `json:"country,omitempty"`
	CountryCode    string `json:"country_code,omitempty"`
	Region         string `json:"region,omitempty"`
	City           string `json:"city,omitempty"`
	QualityStatus  string `json:"quality_status,omitempty"`
	QualityScore   *int   `json:"quality_score,omitempty"`
	QualityGrade   string `json:"quality_grade,omitempty"`
	QualitySummary string `json:"quality_summary,omitempty"`
	QualityChecked *int64 `json:"quality_checked,omitempty"`
}

type ProxyAccountSummary struct {
	ID       int64   `json:"id"`
	Name     string  `json:"name"`
	Platform string  `json:"platform"`
	Type     string  `json:"type"`
	Notes    *string `json:"notes,omitempty"`
}

type RedeemCode struct {
	ID int64 `json:"id"`
	// Code 永远只是掩码（前 4 位 + ****）。完整卡密只在「生成」接口的当次响应里
	// 出现一次，任何列表 / 详情 / 历史 / 导出都不会、也不得返回明文。
	Code string `json:"code"`
	// CodePrefix 是落库的展示前缀（前 8 位）。它不是凭据，只用于后台识别与搜索；
	// 短于 8 位的自定义卡密没有前缀。
	CodePrefix string     `json:"code_prefix,omitempty"`
	Type       string     `json:"type"`
	Value      float64    `json:"value"`
	Status     string     `json:"status"`
	UsedBy     *int64     `json:"used_by"`
	UsedAt     *time.Time `json:"used_at"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`

	GroupID      *int64 `json:"group_id"`
	ValidityDays int    `json:"validity_days"`

	// Notes is only populated for admin_balance/admin_concurrency types
	// so users can see why they were charged or credited
	Notes *string `json:"notes,omitempty"`

	User  *User  `json:"user,omitempty"`
	Group *Group `json:"group,omitempty"`
}

// GeneratedRedeemCode 是「生成卡密」接口的一次性响应元素。
//
// 它是整个系统里唯一携带完整卡密明文的 DTO：只由 Generate handler 在首次执行时
// 构造，绝不用于列表 / 详情 / 导出 / 幂等重放（重放走 AdminRedeemCode 掩码形态）。
type GeneratedRedeemCode struct {
	ID           int64      `json:"id"`
	Code         string     `json:"code"`
	CodePrefix   string     `json:"code_prefix,omitempty"`
	Type         string     `json:"type"`
	Value        float64    `json:"value"`
	Status       string     `json:"status"`
	CreatedAt    time.Time  `json:"created_at"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	GroupID      *int64     `json:"group_id,omitempty"`
	ValidityDays int        `json:"validity_days"`
}

// AdminRedeemCode 是管理员接口使用的 redeem code DTO（包含 notes 等字段）。
// 注意：普通用户接口不得返回 notes 等内部信息。
type AdminRedeemCode struct {
	RedeemCode

	Notes string `json:"notes"`
	// SourceRef 是合成流水行的结构化来源引用（当前只有链上充值扣回：
	// crypto_deposit:<deposit_id>:<chain>:<tx_hash>:<log_index>）。
	// 它是审计/对账指针，不是凭据；真实卡密行不带。
	SourceRef string `json:"source_ref,omitempty"`
}

type NullableTimeField struct {
	Set   bool
	Value *time.Time
}

func (f *NullableTimeField) UnmarshalJSON(data []byte) error {
	f.Set = true
	if bytes.Equal(data, []byte("null")) {
		f.Value = nil
		return nil
	}
	var value time.Time
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	f.Value = &value
	return nil
}

type NullableInt64Field struct {
	Set   bool
	Value *int64
}

func (f *NullableInt64Field) UnmarshalJSON(data []byte) error {
	f.Set = true
	if bytes.Equal(data, []byte("null")) {
		f.Value = nil
		return nil
	}
	var value int64
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	f.Value = &value
	return nil
}

type BatchUpdateRedeemCodeFields struct {
	Status    *string            `json:"status,omitempty"`
	ExpiresAt NullableTimeField  `json:"expires_at,omitempty"`
	Notes     *string            `json:"notes,omitempty"`
	GroupID   NullableInt64Field `json:"group_id,omitempty"`

	Type  *string  `json:"type,omitempty"`
	Value *float64 `json:"value,omitempty"`
}

type BatchUpdateRedeemCodesRequest struct {
	IDs    []int64                     `json:"ids" binding:"required,min=1"`
	Fields BatchUpdateRedeemCodeFields `json:"fields" binding:"required"`
}

// UsageLog 是普通用户接口使用的 usage log DTO（不包含管理员字段）。
type UsageLog struct {
	ID        int64  `json:"id"`
	UserID    int64  `json:"user_id"`
	APIKeyID  int64  `json:"api_key_id"`
	AccountID int64  `json:"account_id"`
	RequestID string `json:"request_id"`
	// ResponseRequestID is the final X-Request-Id response header returned to the client.
	ResponseRequestID *string `json:"response_request_id,omitempty"`
	// ClientRequestID is the X-Client-Request-Id response header returned to the client.
	ClientRequestID *string `json:"client_request_id,omitempty"`
	// ResponseMsgID is the Claude/Anthropic message id (e.g. msg_...) when enabled on the group.
	ResponseMsgID *string `json:"response_msg_id,omitempty"`
	Model         string  `json:"model"`
	// Associated semantic error code, not wire HTTP status or proof of delivery.
	// Only status is exposed; raw provider errors remain in Ops.
	FailureStatusCode *int `json:"failure_status_code,omitempty"`
	// ServiceTier records the OpenAI service tier used for billing, e.g. "priority" / "flex".
	ServiceTier *string `json:"service_tier,omitempty"`
	// ServiceTierMultiplier is the multiplier applied to the customer charge of a
	// Fast/UltraFast request on top of the group rate (null when not recorded).
	ServiceTierMultiplier *float64 `json:"service_tier_multiplier,omitempty"`
	// ReasoningEffort is the client-requested effort (mapping-hidden, like Model).
	// OpenAI: "low"/"medium"/"high"/"xhigh"; Claude: "low"/"medium"/"high"/"max".
	// Historical rows without requested_reasoning_effort fall back to the stored effective value.
	ReasoningEffort *string `json:"reasoning_effort,omitempty"`
	// InboundEndpoint is the client-facing API endpoint path, e.g. /v1/chat/completions.
	InboundEndpoint *string `json:"inbound_endpoint,omitempty"`
	// UpstreamEndpoint is the normalized upstream endpoint path, e.g. /v1/responses.
	UpstreamEndpoint *string `json:"upstream_endpoint,omitempty"`

	GroupID        *int64 `json:"group_id"`
	SubscriptionID *int64 `json:"subscription_id"`

	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	CacheCreationTokens int `json:"cache_creation_tokens"`
	CacheReadTokens     int `json:"cache_read_tokens"`

	CacheCreation5mTokens int `json:"cache_creation_5m_tokens"`
	CacheCreation1hTokens int `json:"cache_creation_1h_tokens"`

	InputCost                 float64 `json:"input_cost"`
	OutputCost                float64 `json:"output_cost"`
	CacheCreationCost         float64 `json:"cache_creation_cost"`
	CacheReadCost             float64 `json:"cache_read_cost"`
	TotalCost                 float64 `json:"total_cost"`
	ActualCost                float64 `json:"actual_cost"`
	RateMultiplier            float64 `json:"rate_multiplier"`
	LongContextBillingApplied bool    `json:"long_context_billing_applied"`

	BillingType  int8   `json:"billing_type"`
	RequestType  string `json:"request_type"`
	Stream       bool   `json:"stream"`
	OpenAIWSMode bool   `json:"openai_ws_mode"`
	// NativeCompactionV2 is true only for requests positively identified at
	// runtime as the native OpenAI remote compaction v2 wire.
	NativeCompactionV2 bool `json:"native_compaction_v2"`
	DurationMs         *int `json:"duration_ms"`
	FirstTokenMs       *int `json:"first_token_ms"`

	// 图片生成字段
	ImageCount         int            `json:"image_count"`
	ImageSize          *string        `json:"image_size"`
	ImageInputSize     *string        `json:"image_input_size"`
	ImageOutputSize    *string        `json:"image_output_size"`
	ImageInputTokens   int            `json:"image_input_tokens"`
	ImageInputCost     float64        `json:"image_input_cost"`
	ImageOutputTokens  int            `json:"image_output_tokens"`
	ImageOutputCost    float64        `json:"image_output_cost"`
	ImageSizeSource    *string        `json:"image_size_source"`
	ImageSizeBreakdown map[string]int `json:"image_size_breakdown"`
	MediaType          *string        `json:"media_type"`

	// User-Agent
	UserAgent *string `json:"user_agent"`
	// IPAddress is visible to the owner of the usage record.
	IPAddress *string `json:"ip_address,omitempty"`
	// SessionID is the explicit client-provided request correlation identifier
	// (e.g. the session_id / X-Session-Id headers). Omitted when absent.
	SessionID *string `json:"session_id,omitempty"`

	// Cache TTL Override 标记
	CacheTTLOverridden bool `json:"cache_ttl_overridden"`

	// BillingMode 计费模式：token/image
	BillingMode *string `json:"billing_mode,omitempty"`

	// Inflight 为 true 时这一行还没结束。ActualCost 是在途余额预留，不是最终价格。
	Inflight bool `json:"inflight,omitempty"`

	CreatedAt time.Time `json:"created_at"`

	User         *User             `json:"user,omitempty"`
	APIKey       *APIKey           `json:"api_key,omitempty"`
	Group        *Group            `json:"group,omitempty"`
	Subscription *UserSubscription `json:"subscription,omitempty"`

	// Compact association summaries. Admin list endpoints use these instead of
	// embedding full User/APIKey/Group/Subscription objects in every row; the
	// list only needs display names and keeps large pricing/credential payloads
	// out of the response.
	UserSummary         *UsageUserSummary         `json:"user_summary,omitempty"`
	APIKeySummary       *UsageAPIKeySummary       `json:"api_key_summary,omitempty"`
	GroupSummary        *UsageGroupSummary        `json:"group_summary,omitempty"`
	SubscriptionSummary *UsageSubscriptionSummary `json:"subscription_summary,omitempty"`
	AccountSummary      *AccountSummary           `json:"account_summary,omitempty"`
}

// UsageUserSummary is the minimum user identity needed by usage list rows.
type UsageUserSummary struct {
	ID        int64      `json:"id"`
	Email     string     `json:"email"`
	Username  string     `json:"username,omitempty"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
}

// UsageAPIKeySummary is the minimum API key identity needed by usage list rows.
type UsageAPIKeySummary struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// UsageGroupSummary is the minimum group identity needed by usage list rows.
type UsageGroupSummary struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// UsageSubscriptionSummary is the minimum subscription identity needed by usage list rows.
type UsageSubscriptionSummary struct {
	ID      int64  `json:"id"`
	UserID  int64  `json:"user_id"`
	GroupID int64  `json:"group_id"`
	Status  string `json:"status"`
}

// AdminUsageLog 是管理员接口使用的 usage log DTO（包含管理员字段）。
type AdminUsageLog struct {
	UsageLog

	// UpstreamModel is the actual model sent to the upstream provider after mapping.
	// Omitted when no mapping was applied (requested model was used as-is).
	UpstreamModel *string `json:"upstream_model,omitempty"`
	// UpstreamReasoningEffort is the effort actually forwarded after group policy /
	// model-family remapping. Omitted when it matches the client-requested value.
	UpstreamReasoningEffort *string `json:"upstream_reasoning_effort,omitempty"`
	// UpstreamResponseModel is the raw model declared by the upstream response.
	UpstreamResponseModel *string `json:"upstream_response_model,omitempty"`
	// UpstreamModelMismatch is nil when the upstream did not declare a model.
	UpstreamModelMismatch *bool `json:"upstream_model_mismatch,omitempty"`

	// ChannelID 渠道 ID
	ChannelID *int64 `json:"channel_id,omitempty"`
	// ModelMappingChain 模型映射链，如 "a→b→c"
	ModelMappingChain *string `json:"model_mapping_chain,omitempty"`
	// UpstreamRequestID 是直接上游声明的请求标识，仅管理端可见。
	UpstreamRequestID *string `json:"upstream_request_id,omitempty"`
	// BillingTier 计费层级标签（per_request/image 模式）
	BillingTier *string `json:"billing_tier,omitempty"`

	// AccountRateMultiplier 账号计费倍率快照（nil 表示按 1.0 处理）
	AccountRateMultiplier *float64 `json:"account_rate_multiplier"`
	// AccountStatsCost 自定义定价规则计算的账号统计费用（nil 表示使用默认公式）
	AccountStatsCost *float64 `json:"account_stats_cost,omitempty"`

	// IPAddress 用户请求 IP
	IPAddress *string `json:"ip_address,omitempty"`

	// Account 最小账号信息（避免泄露敏感字段）
	Account *AccountSummary `json:"account,omitempty"`
}

type UsageCleanupFilters struct {
	StartTime             time.Time `json:"start_time"`
	EndTime               time.Time `json:"end_time"`
	UserID                *int64    `json:"user_id,omitempty"`
	APIKeyID              *int64    `json:"api_key_id,omitempty"`
	AccountID             *int64    `json:"account_id,omitempty"`
	GroupID               *int64    `json:"group_id,omitempty"`
	Model                 *string   `json:"model,omitempty"`
	RequestID             *string   `json:"request_id,omitempty"`
	RequestType           *string   `json:"request_type,omitempty"`
	Stream                *bool     `json:"stream,omitempty"`
	NativeCompactionV2    *bool     `json:"native_compaction_v2,omitempty"`
	BillingType           *int8     `json:"billing_type,omitempty"`
	BillingMode           *string   `json:"billing_mode,omitempty"`
	UpstreamModelMismatch *bool     `json:"upstream_model_mismatch,omitempty"`
}

type UsageCleanupPreview struct {
	MatchedRows  int64     `json:"matched_rows"`
	Truncated    bool      `json:"truncated"`
	MaxRangeDays int       `json:"max_range_days"`
	StartTime    time.Time `json:"start_time"`
	EndTime      time.Time `json:"end_time"`
}

type UsageCleanupTask struct {
	ID           int64               `json:"id"`
	Status       string              `json:"status"`
	Filters      UsageCleanupFilters `json:"filters"`
	CreatedBy    int64               `json:"created_by"`
	DeletedRows  int64               `json:"deleted_rows"`
	ErrorMessage *string             `json:"error_message,omitempty"`
	CanceledBy   *int64              `json:"canceled_by,omitempty"`
	CanceledAt   *time.Time          `json:"canceled_at,omitempty"`
	StartedAt    *time.Time          `json:"started_at,omitempty"`
	FinishedAt   *time.Time          `json:"finished_at,omitempty"`
	CreatedAt    time.Time           `json:"created_at"`
	UpdatedAt    time.Time           `json:"updated_at"`
}

// AccountSummary is a minimal account info for usage log display.
// It intentionally excludes sensitive fields like Credentials, Proxy, etc.
type AccountSummary struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type Setting struct {
	ID        int64     `json:"id"`
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}

type UserSubscription struct {
	ID      int64 `json:"id"`
	UserID  int64 `json:"user_id"`
	GroupID int64 `json:"group_id"`

	StartsAt  time.Time `json:"starts_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Status    string    `json:"status"`

	DailyWindowStart   *time.Time `json:"daily_window_start"`
	WeeklyWindowStart  *time.Time `json:"weekly_window_start"`
	MonthlyWindowStart *time.Time `json:"monthly_window_start"`

	DailyUsageUSD   float64 `json:"daily_usage_usd"`
	WeeklyUsageUSD  float64 `json:"weekly_usage_usd"`
	MonthlyUsageUSD float64 `json:"monthly_usage_usd"`

	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`

	User  *User  `json:"user,omitempty"`
	Group *Group `json:"group,omitempty"`
}

// AdminUserSubscription 是管理员接口使用的订阅 DTO（包含分配信息/备注等字段）。
// 注意：普通用户接口不得返回 assigned_by/assigned_at/notes/assigned_by_user 等管理员字段。
type AdminUserSubscription struct {
	UserSubscription

	AssignedBy *int64    `json:"assigned_by"`
	AssignedAt time.Time `json:"assigned_at"`
	Notes      string    `json:"notes"`

	AssignedByUser *User `json:"assigned_by_user,omitempty"`
}

type BulkAssignResult struct {
	SuccessCount  int                     `json:"success_count"`
	CreatedCount  int                     `json:"created_count"`
	ReusedCount   int                     `json:"reused_count"`
	FailedCount   int                     `json:"failed_count"`
	Subscriptions []AdminUserSubscription `json:"subscriptions"`
	Errors        []string                `json:"errors"`
	Statuses      map[string]string       `json:"statuses,omitempty"`
}

// PromoCode 注册优惠码
type PromoCode struct {
	ID          int64      `json:"id"`
	Code        string     `json:"code"`
	BonusAmount float64    `json:"bonus_amount"`
	MaxUses     int        `json:"max_uses"`
	UsedCount   int        `json:"used_count"`
	Status      string     `json:"status"`
	ExpiresAt   *time.Time `json:"expires_at"`
	Notes       string     `json:"notes"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// PromoCodeUsage 优惠码使用记录
type PromoCodeUsage struct {
	ID          int64     `json:"id"`
	PromoCodeID int64     `json:"promo_code_id"`
	UserID      int64     `json:"user_id"`
	BonusAmount float64   `json:"bonus_amount"`
	UsedAt      time.Time `json:"used_at"`

	User *User `json:"user,omitempty"`
}
