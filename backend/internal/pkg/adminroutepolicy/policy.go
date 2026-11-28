// Package adminroutepolicy is the single source of truth for how every
// /api/v1/admin* route is protected.
//
// Before this package existed, step-up 2FA was opt-in per route: only handlers
// explicitly wrapped with NewStepUpAuthMiddleware demanded TOTP, while every
// other admin route accepted the legacy full-power machine credential
// (x-api-key) with no second factor. Read-only routes were intentionally left
// ungated ("只读所以不用 2FA"), so the same machine key could page through
// plaintext redeem codes / proxy passwords even though the export endpoints
// demanded TOTP. Security therefore depended on nobody ever forgetting a
// wrapper — an architectural hole, not a missed line.
//
// The fix is default-deny + explicit classification: every admin route must
// resolve to exactly one of the three classes below, otherwise the route
// coverage test fails CI and the runtime treats it as step-up required
// (fail-closed). Adding a route without classifying it is no longer silent.
package adminroutepolicy

import "strings"

// Class is the protection level of a single admin route.
type Class string

const (
	// ClassMachineAllowlisted routes may be called by machine credentials
	// (legacy x-api-key or scoped adm_sc_ keys). They MUST NOT return any
	// secret material: only health probes, read-only aggregate statistics and
	// non-sensitive automation triggers belong here.
	ClassMachineAllowlisted Class = "machine_allowlisted"
	// ClassStepUpRequired routes can return or mutate secret material for ANY
	// method, including GET. They require a human JWT session with a recent
	// TOTP step-up grant; machine credentials are always rejected.
	ClassStepUpRequired Class = "step_up_required"
	// ClassSecretFree routes are explicitly declared not to expose secret
	// material to callers (the Reason constant records why). Machine
	// credentials are still default-denied: only ClassMachineAllowlisted routes
	// accept them.
	ClassSecretFree Class = "secret_free"
)

// Reason explains why a route is classified the way it is. Values are stable
// identifiers so audit logs and tests never depend on prose.
type Reason string

const (
	// machine_allowlisted reasons
	ReasonDashboardAggregate  Reason = "read_only_dashboard_aggregate"
	ReasonOpsHealthProbe      Reason = "read_only_ops_health_probe"
	ReasonOpsAggregate        Reason = "read_only_ops_telemetry_aggregate"
	ReasonModelRegistrySync   Reason = "models_dev_registry_sync_trigger_no_secret"
	ReasonVersionInfo         Reason = "system_version_info_no_secret"
	ReasonChannelMonitorAggr  Reason = "read_only_channel_monitor_aggregate"
	ReasonUsageTrafficAggr    Reason = "read_only_usage_traffic_aggregate"
	ReasonAccountAvailability Reason = "read_only_account_availability_aggregate"
	// ReasonUpstreamBillingProbe covers the on-demand upstream declared-rate
	// probe. It returns public rate numbers (never credentials), and the admin
	// console fires it from a per-row refresh button that must not ask for a
	// TOTP grant on every click.
	ReasonUpstreamBillingProbe Reason = "upstream_declared_rate_probe_no_secret"

	// step_up_required reasons
	ReasonUpstreamCredentials Reason = "upstream_account_credentials"
	ReasonRedeemCodes         Reason = "redeem_code_secrets"
	// ReasonRedeemCodeExport covers the bulk CSV export. The CSV itself is now
	// masked metadata (code prefix/mask + accounting columns), but a single
	// request still walks up to 10000 business records off the box, so it keeps
	// the same step-up bar as the backup exports.
	ReasonRedeemCodeExport    Reason = "redeem_code_bulk_export_metadata"
	ReasonPromoCodes          Reason = "promo_code_grant_value"
	ReasonProxyPasswords      Reason = "proxy_passwords"
	ReasonChannelKeys         Reason = "channel_upstream_keys"
	ReasonAdminSecrets        Reason = "settings_secret_writes_and_key_management"
	ReasonPaymentSecrets      Reason = "payment_provider_keys_and_finance"
	ReasonBackupSecrets       Reason = "backup_artifacts_and_s3_credentials"
	ReasonCapturedTraffic     Reason = "captured_traffic_write_operations"
	ReasonPluginCode          Reason = "plugin_install_and_config_writes"
	ReasonDebugDumps          Reason = "debug_dump_payloads"
	ReasonAccountOpsSecrets   Reason = "account_ops_webhook_and_channel_writes"
	ReasonCredentialGuard     Reason = "credential_guard_writes_and_relogin"
	ReasonExcelGrantSecrets   Reason = "excel_grant_authorization_writes"
	ReasonMihomoConfig        Reason = "mihomo_proxy_config_writes"
	ReasonUserAPIKeys         Reason = "user_api_key_material"
	ReasonRevenueOperations   Reason = "money_moving_operation"
	ReasonDataManagementWrite Reason = "backup_source_and_s3_writes"
	ReasonHostLifecycleWrite  Reason = "dangerous_host_lifecycle_action"
	// ReasonAccountSecurityReset covers the break-glass 2FA reset: an admin
	// wipes another account's TOTP (including their own when they locked
	// themselves out) and revokes every factor's step-up grant. It weakens the
	// target account's security, so it needs the operator's own recent 2FA and
	// can never be driven by a machine credential.
	ReasonAccountSecurityReset Reason = "account_security_factor_reset"
	ReasonModerationWrite      Reason = "content_moderation_config_and_credentials_writes"
	ReasonPromptAuditWrite     Reason = "prompt_audit_config_and_credentials_writes"
	ReasonChannelMonitorWrite  Reason = "channel_monitor_config_and_credentials_writes"
	ReasonChannelPricingWrite  Reason = "channel_pricing_and_operations_writes"
	ReasonSecurityPenaltyWrite Reason = "security_penalty_policy_and_release_writes"

	// secret_free reasons
	ReasonUsageAnalytics           Reason = "usage_analytics_no_secrets"
	ReasonUserAdministration       Reason = "user_administration_no_secret_material"
	ReasonGroupRoutingConfig       Reason = "group_routing_config_no_secret_material"
	ReasonPublicContent            Reason = "announcements_and_public_content"
	ReasonComplianceState          Reason = "compliance_acknowledgement_state"
	ReasonHostLifecycleInfo        Reason = "host_lifecycle_status_no_secret_material"
	ReasonPublicModelCatalog       Reason = "public_model_catalog"
	ReasonModelRegistryMeta        Reason = "model_registry_metadata_no_secret"
	ReasonUpstreamQuotaSummary     Reason = "upstream_quota_summary_no_credentials"
	ReasonSupportTickets           Reason = "support_ticket_content_no_credentials"
	ReasonAuditTrail               Reason = "audit_trail_no_secret_material"
	ReasonModerationConfig         Reason = "content_moderation_config_no_secret_material"
	ReasonChannelMonitorConfig     Reason = "channel_monitor_telemetry_no_secret_material"
	ReasonTestPlanning             Reason = "scheduled_test_plan_config"
	ReasonQualityCheckResults      Reason = "group_quality_check_results"
	ReasonPenaltyState             Reason = "security_penalty_state"
	ReasonUserAttributeCatalog     Reason = "user_attribute_catalog"
	ReasonErrorPassthrough         Reason = "error_passthrough_rules"
	ReasonTLSProfile               Reason = "tls_fingerprint_profiles"
	ReasonServerlessConfig         Reason = "serverless_runtime_config"
	ReasonOpsAutomationConfig      Reason = "ops_automation_config"
	ReasonAffiliateAccounting      Reason = "affiliate_accounting_reads"
	ReasonSubscriptionState        Reason = "subscription_state_reads"
	ReasonProviderOAuthStatus      Reason = "provider_oauth_status_reads"
	ReasonAuthMetadata             Reason = "auth_metadata_no_secret_material"
	ReasonAccountReadsRedacted     Reason = "account_reads_redact_credentials"
	ReasonSettingsReadNoSecret     Reason = "settings_read_returns_no_secret_material"
	ReasonPaymentReadsReadOnly     Reason = "payment_reads_expose_no_provider_secret"
	ReasonBackupReadsMetadataOnly  Reason = "backup_reads_expose_job_metadata_only"
	ReasonCaptureReadsHandlerGate  Reason = "capture_reads_use_dedicated_sensitive_access_gate"
	ReasonPluginReadsMetadataOnly  Reason = "plugin_reads_expose_install_metadata_only"
	ReasonChannelPricingConfig     Reason = "channel_pricing_config_no_credentials"
	ReasonUpstreamMonitorTelemetry Reason = "upstream_monitor_telemetry_no_credentials"
	ReasonPromptAuditNoCredential  Reason = "prompt_audit_content_has_no_credential_secret"
	ReasonCryptoDedicatedGate      Reason = "crypto_wallet_has_dedicated_totp_risk_gate"
	ReasonOpsReadOnlyConfig        Reason = "ops_read_only_runtime_config"
	ReasonMihomoReadsRedacted      Reason = "mihomo_reads_redact_proxy_credentials"
	ReasonProxyReadsRedacted       Reason = "proxy_reads_redact_passwords"
	ReasonChannelReadsRedacted     Reason = "channel_reads_redact_upstream_keys"
	ReasonAccountUsageAggregate    Reason = "read_only_account_usage_aggregate"
	// ReasonAccountConnectivityTest covers the admin "test" action on an
	// upstream account: it dials the upstream with the stored credential and
	// streams back connectivity / model-availability results. It never returns
	// the credential itself, and the console fires one request per model in the
	// "测试设置 / 测试全部" panel, so a TOTP prompt per click would make batch
	// testing unusable. Credential reads (GET /:id/credential) and exports
	// (GET /data) stay step-up gated.
	ReasonAccountConnectivityTest Reason = "account_connectivity_test_no_secret"
	// ReasonUpstreamModelCatalogSync covers the admin "同步最新支持模型" button on
	// an upstream account. The handler fetches the live model list from the
	// account's configured upstream (outbound target goes through the same URL
	// allowlist / private-host guard as the connectivity test), enriches missing
	// capability fields from the public models.dev registry and persists only a
	// model-catalog snapshot under accounts.extra[upstream_models]. It never
	// reads or returns a credential, never moves money and never changes account
	// permissions; the response carries model IDs plus public capability
	// metadata. The mutation (POST) is audited by the audit middleware.
	ReasonUpstreamModelCatalogSync Reason = "upstream_model_catalog_metadata_sync_no_secret"
	// ReasonUpstreamModelCatalogPreview covers the "同步上游模型" preview fired
	// while creating/editing an account. It uses the not-yet-saved credentials
	// from the request body for one upstream /models call, validates base_url
	// through the same allowlist / private-host guard, writes nothing (temp
	// account id is 0, so no snapshot is persisted) and returns model IDs plus
	// public capability metadata only.
	ReasonUpstreamModelCatalogPreview Reason = "upstream_model_catalog_preview_no_secret"
	// ReasonEmailTemplatePreview covers the notification email template preview:
	// it renders the unsaved subject/HTML with sample variables in memory and
	// returns the rendered subject/HTML. No DB write, no outbound request and no
	// credential material is involved.
	ReasonEmailTemplatePreview Reason = "email_template_preview_no_secret"
	// The following reasons cover the 2026-11 "2FA trigger matrix" pass: admin
	// actions that look alarming by resource name but only probe, aggregate or
	// preview, and whose response never carries secret material.
	ReasonProxyConnectivityTest   Reason = "proxy_connectivity_and_quality_test_no_secret"
	ReasonAccountBindingPreflight Reason = "account_binding_preflight_check_no_secret"
	ReasonAccountTimerPreview     Reason = "account_schedulable_timer_preview_no_secret"
	ReasonChannelAnnouncementSync Reason = "channel_announcement_sync_counters_no_secret"
	ReasonSMTPConnectivityTest    Reason = "smtp_connectivity_test_no_secret"
	ReasonWebSearchEmulationProbe Reason = "web_search_emulation_probe_no_secret"
	ReasonIPRiskPreflightCheck    Reason = "ip_risk_preflight_check_no_secret"
	ReasonRedeemCodeReadsMasked   Reason = "redeem_code_reads_return_masked_values_only"
	ReasonPromoUsageReadsNoCode   Reason = "promo_usage_reads_exclude_grant_code"
)

// Resource names the sensitive resource family a step-up route protects.
type Resource string

const (
	ResourceNone              Resource = ""
	ResourceUpstreamAccounts  Resource = "upstream_accounts"
	ResourceRedeemCodes       Resource = "redeem_codes"
	ResourcePromoCodes        Resource = "promo_codes"
	ResourceProxies           Resource = "proxies"
	ResourceChannels          Resource = "channels"
	ResourceAdminSettings     Resource = "admin_settings"
	ResourcePayments          Resource = "payments"
	ResourceBackups           Resource = "backups"
	ResourceCapturedTraffic   Resource = "captured_traffic"
	ResourcePlugins           Resource = "plugins"
	ResourceDebugDumps        Resource = "debug_dumps"
	ResourceAccountOps        Resource = "account_ops"
	ResourceCredentialGuard   Resource = "credential_guard"
	ResourceExcelGrants       Resource = "excel_grants"
	ResourceMihomo            Resource = "mihomo"
	ResourceUserAPIKeys       Resource = "user_api_keys"
	ResourceRevenueOperations Resource = "revenue_operations"
	// ResourceAccountSecurity 覆盖「降低目标账号安全强度」的管理动作：
	// 目前只有破玻璃重置 2FA。它与资金、上行凭证无关，单独成族便于按资源检索审计。
	ResourceAccountSecurity   Resource = "account_security"
	ResourceModeration        Resource = "content_moderation"
	ResourcePromptAudit       Resource = "prompt_audit"
	ResourceChannelMonitors   Resource = "channel_monitors"
	ResourceSecurityPenalties Resource = "security_penalties"
)

// Resolved is the classification of one route.
type Resolved struct {
	Class    Class
	Reason   Reason
	Resource Resource
	// Rule names the registry entry that produced the classification. Useful
	// for audit logs and for debugging coverage-test failures.
	Rule string
}

type exactRule struct {
	method string
	path   string
	res    Resolved
}

type prefixRule struct {
	prefix  string
	methods map[string]struct{} // nil => any method
	res     Resolved
}

func anyMethod() map[string]struct{} { return nil }

func mutatingMethods() map[string]struct{} {
	return map[string]struct{}{"POST": {}, "PUT": {}, "PATCH": {}, "DELETE": {}}
}

// machineAllowlisted is the ONLY set of routes a machine credential may call.
//
// Rules for adding a route:
//  1. it must be read-only OR a non-sensitive automation trigger;
//  2. its response must not contain any secret material (no codes, keys,
//     passwords, credentials, tokens, wallet data, exported rows);
//  3. it must also match a secret-free family below (asserted by tests).
var machineAllowlisted = []exactRule{
	{method: "GET", path: "/api/v1/admin/dashboard/stats", res: Resolved{ClassMachineAllowlisted, ReasonDashboardAggregate, ResourceNone, "machine:dashboard.stats"}},
	{method: "GET", path: "/api/v1/admin/dashboard/trend", res: Resolved{ClassMachineAllowlisted, ReasonDashboardAggregate, ResourceNone, "machine:dashboard.trend"}},
	{method: "GET", path: "/api/v1/admin/dashboard/models", res: Resolved{ClassMachineAllowlisted, ReasonDashboardAggregate, ResourceNone, "machine:dashboard.models"}},
	{method: "GET", path: "/api/v1/admin/dashboard/groups", res: Resolved{ClassMachineAllowlisted, ReasonDashboardAggregate, ResourceNone, "machine:dashboard.groups"}},
	{method: "GET", path: "/api/v1/admin/dashboard/realtime", res: Resolved{ClassMachineAllowlisted, ReasonDashboardAggregate, ResourceNone, "machine:dashboard.realtime"}},
	{method: "GET", path: "/api/v1/admin/dashboard/snapshot-v2", res: Resolved{ClassMachineAllowlisted, ReasonDashboardAggregate, ResourceNone, "machine:dashboard.snapshot-v2"}},
	{method: "GET", path: "/api/v1/admin/dashboard/users-trend", res: Resolved{ClassMachineAllowlisted, ReasonDashboardAggregate, ResourceNone, "machine:dashboard.users-trend"}},
	{method: "GET", path: "/api/v1/admin/dashboard/api-keys-trend", res: Resolved{ClassMachineAllowlisted, ReasonDashboardAggregate, ResourceNone, "machine:dashboard.api-keys-trend"}},
	{method: "GET", path: "/api/v1/admin/dashboard/user-breakdown", res: Resolved{ClassMachineAllowlisted, ReasonDashboardAggregate, ResourceNone, "machine:dashboard.user-breakdown"}},
	{method: "GET", path: "/api/v1/admin/dashboard/users-ranking", res: Resolved{ClassMachineAllowlisted, ReasonDashboardAggregate, ResourceNone, "machine:dashboard.users-ranking"}},

	{method: "GET", path: "/api/v1/admin/ops/dashboard/overview", res: Resolved{ClassMachineAllowlisted, ReasonOpsAggregate, ResourceNone, "machine:ops.dashboard.overview"}},
	{method: "GET", path: "/api/v1/admin/ops/dashboard/error-distribution", res: Resolved{ClassMachineAllowlisted, ReasonOpsAggregate, ResourceNone, "machine:ops.dashboard.error-distribution"}},
	{method: "GET", path: "/api/v1/admin/ops/dashboard/error-trend", res: Resolved{ClassMachineAllowlisted, ReasonOpsAggregate, ResourceNone, "machine:ops.dashboard.error-trend"}},
	{method: "GET", path: "/api/v1/admin/ops/dashboard/latency-histogram", res: Resolved{ClassMachineAllowlisted, ReasonOpsAggregate, ResourceNone, "machine:ops.dashboard.latency-histogram"}},
	{method: "GET", path: "/api/v1/admin/ops/dashboard/openai-token-stats", res: Resolved{ClassMachineAllowlisted, ReasonOpsAggregate, ResourceNone, "machine:ops.dashboard.openai-token-stats"}},
	{method: "GET", path: "/api/v1/admin/ops/dashboard/snapshot-v2", res: Resolved{ClassMachineAllowlisted, ReasonOpsAggregate, ResourceNone, "machine:ops.dashboard.snapshot-v2"}},
	{method: "GET", path: "/api/v1/admin/ops/dashboard/throughput-trend", res: Resolved{ClassMachineAllowlisted, ReasonOpsAggregate, ResourceNone, "machine:ops.dashboard.throughput-trend"}},
	{method: "GET", path: "/api/v1/admin/ops/account-availability", res: Resolved{ClassMachineAllowlisted, ReasonAccountAvailability, ResourceNone, "machine:ops.account-availability"}},

	{method: "GET", path: "/api/v1/admin/ops/auth-cache-invalidation/health", res: Resolved{ClassMachineAllowlisted, ReasonOpsHealthProbe, ResourceNone, "machine:ops.auth-cache-invalidation.health"}},
	{method: "GET", path: "/api/v1/admin/ops/ingress-rejections/health", res: Resolved{ClassMachineAllowlisted, ReasonOpsHealthProbe, ResourceNone, "machine:ops.ingress-rejections.health"}},
	{method: "GET", path: "/api/v1/admin/ops/live-balance-outbox/health", res: Resolved{ClassMachineAllowlisted, ReasonOpsHealthProbe, ResourceNone, "machine:ops.live-balance-outbox.health"}},
	{method: "GET", path: "/api/v1/admin/ops/scheduler-freshness/health", res: Resolved{ClassMachineAllowlisted, ReasonOpsHealthProbe, ResourceNone, "machine:ops.scheduler-freshness.health"}},
	{method: "GET", path: "/api/v1/admin/ops/system-logs/health", res: Resolved{ClassMachineAllowlisted, ReasonOpsHealthProbe, ResourceNone, "machine:ops.system-logs.health"}},
	{method: "GET", path: "/api/v1/admin/usage/traffic/health", res: Resolved{ClassMachineAllowlisted, ReasonOpsHealthProbe, ResourceNone, "machine:usage.traffic.health"}},
	{method: "GET", path: "/api/v1/admin/usage/traffic", res: Resolved{ClassMachineAllowlisted, ReasonUsageTrafficAggr, ResourceNone, "machine:usage.traffic"}},

	{method: "GET", path: "/api/v1/admin/channel-monitor-v2/snapshot", res: Resolved{ClassMachineAllowlisted, ReasonChannelMonitorAggr, ResourceNone, "machine:channel-monitor-v2.snapshot"}},
	{method: "GET", path: "/api/v1/admin/system/version", res: Resolved{ClassMachineAllowlisted, ReasonVersionInfo, ResourceNone, "machine:system.version"}},
	{method: "POST", path: "/api/v1/admin/models/metadata/sync", res: Resolved{ClassMachineAllowlisted, ReasonModelRegistrySync, ResourceNone, "machine:models.metadata.sync"}},
}

// stepUpExact pins individual routes whose resource family is otherwise
// secret-free: credential-revealing reads, money-moving writes and dangerous
// host lifecycle actions.
var stepUpExact = []exactRule{
	// 用户/分组的 API Key 明文与管理
	{method: "GET", path: "/api/v1/admin/users/:id/api-keys", res: Resolved{ClassStepUpRequired, ReasonUserAPIKeys, ResourceUserAPIKeys, "exact:users.api-keys"}},
	{method: "GET", path: "/api/v1/admin/groups/:id/api-keys", res: Resolved{ClassStepUpRequired, ReasonUserAPIKeys, ResourceUserAPIKeys, "exact:groups.api-keys"}},
	{method: "PUT", path: "/api/v1/admin/api-keys/:id", res: Resolved{ClassStepUpRequired, ReasonUserAPIKeys, ResourceUserAPIKeys, "exact:api-keys.update"}},
	// 上游凭证明文回读与整表导出
	{method: "GET", path: "/api/v1/admin/accounts/:id/credential", res: Resolved{ClassStepUpRequired, ReasonUpstreamCredentials, ResourceUpstreamAccounts, "exact:accounts.credential"}},
	{method: "GET", path: "/api/v1/admin/accounts/data", res: Resolved{ClassStepUpRequired, ReasonUpstreamCredentials, ResourceUpstreamAccounts, "exact:accounts.export"}},
	// 代理明文导出（用户名/密码/完整 URL）。代理列表/详情/统计的密码已掩码，
	// 不再因为「代理」这个资源名整片要求 2FA（见 free:proxies）。
	{method: "GET", path: "/api/v1/admin/proxies/data", res: Resolved{ClassStepUpRequired, ReasonProxyPasswords, ResourceProxies, "exact:proxies.data"}},
	// 卡密批量导出：CSV 已改为掩码 + 前缀 + 对账元数据（不含明文代码），但一次最多
	// 带走 1 万条业务凭据元数据，与备份导出同口径保留 step-up。列表/详情/统计只回
	// 掩码且不再是一万条全量，已改为 secret_free，见 maskedReadExact。
	{method: "GET", path: "/api/v1/admin/redeem-codes/export", res: Resolved{ClassStepUpRequired, ReasonRedeemCodeExport, ResourceRedeemCodes, "exact:redeem-codes.export"}},
	// 备份下载与 S3 凭证读取
	{method: "GET", path: "/api/v1/admin/backups/:id/download-url", res: Resolved{ClassStepUpRequired, ReasonBackupSecrets, ResourceBackups, "exact:backups.download"}},
	{method: "GET", path: "/api/v1/admin/backups/s3-config", res: Resolved{ClassStepUpRequired, ReasonBackupSecrets, ResourceBackups, "exact:backups.s3-config"}},
	// 上游失败 dump 正文
	{method: "GET", path: "/api/v1/admin/debug-dumps/:name", res: Resolved{ClassStepUpRequired, ReasonDebugDumps, ResourceDebugDumps, "exact:debug-dumps.payload"}},
	// 用户删号/建号/改余额/新增登录身份
	// 建号会按请求体写入初始余额与授权分组：绕过 step-up 等于凭空发钱与授权。
	// 任何建号都要求真人 2FA；余额写入本身已由 service 拒绝，必须走带流水与
	// step-up 的 POST /api/v1/admin/users/:id/balance。
	{method: "POST", path: "/api/v1/admin/users", res: Resolved{ClassStepUpRequired, ReasonRevenueOperations, ResourceRevenueOperations, "exact:users.create"}},
	{method: "DELETE", path: "/api/v1/admin/users/:id", res: Resolved{ClassStepUpRequired, ReasonRevenueOperations, ResourceRevenueOperations, "exact:users.delete"}},
	{method: "POST", path: "/api/v1/admin/users/:id/balance", res: Resolved{ClassStepUpRequired, ReasonRevenueOperations, ResourceRevenueOperations, "exact:users.balance"}},
	{method: "POST", path: "/api/v1/admin/users/:id/auth-identities", res: Resolved{ClassStepUpRequired, ReasonRevenueOperations, ResourceRevenueOperations, "exact:users.auth-identities"}},
	// 订阅权益资金操作
	{method: "POST", path: "/api/v1/admin/subscriptions/assign", res: Resolved{ClassStepUpRequired, ReasonRevenueOperations, ResourceRevenueOperations, "exact:subscriptions.assign"}},
	{method: "POST", path: "/api/v1/admin/subscriptions/bulk-assign", res: Resolved{ClassStepUpRequired, ReasonRevenueOperations, ResourceRevenueOperations, "exact:subscriptions.bulk-assign"}},
	{method: "POST", path: "/api/v1/admin/subscriptions/:id/extend", res: Resolved{ClassStepUpRequired, ReasonRevenueOperations, ResourceRevenueOperations, "exact:subscriptions.extend"}},
	{method: "POST", path: "/api/v1/admin/subscriptions/:id/reset-quota", res: Resolved{ClassStepUpRequired, ReasonRevenueOperations, ResourceRevenueOperations, "exact:subscriptions.reset-quota"}},
	{method: "POST", path: "/api/v1/admin/subscriptions/:id/revoke", res: Resolved{ClassStepUpRequired, ReasonRevenueOperations, ResourceRevenueOperations, "exact:subscriptions.revoke"}},
	{method: "POST", path: "/api/v1/admin/subscriptions/:id/restore", res: Resolved{ClassStepUpRequired, ReasonRevenueOperations, ResourceRevenueOperations, "exact:subscriptions.restore"}},
	{method: "DELETE", path: "/api/v1/admin/subscriptions/:id", res: Resolved{ClassStepUpRequired, ReasonRevenueOperations, ResourceRevenueOperations, "exact:subscriptions.delete"}},
	// 返利金额与用户费率
	{method: "POST", path: "/api/v1/admin/affiliates/users/:user_id/codes", res: Resolved{ClassStepUpRequired, ReasonRevenueOperations, ResourceRevenueOperations, "exact:affiliates.codes"}},
	{method: "POST", path: "/api/v1/admin/affiliates/users/batch-rate", res: Resolved{ClassStepUpRequired, ReasonRevenueOperations, ResourceRevenueOperations, "exact:affiliates.batch-rate"}},
	{method: "PUT", path: "/api/v1/admin/affiliates/users/:user_id", res: Resolved{ClassStepUpRequired, ReasonRevenueOperations, ResourceRevenueOperations, "exact:affiliates.update"}},
	{method: "DELETE", path: "/api/v1/admin/affiliates/users/:user_id", res: Resolved{ClassStepUpRequired, ReasonRevenueOperations, ResourceRevenueOperations, "exact:affiliates.delete"}},
	// 批量权限/配额变更与改价（与 #499 的路由级门控双保险）
	{method: "POST", path: "/api/v1/admin/users/:id/replace-group", res: Resolved{ClassStepUpRequired, ReasonRevenueOperations, ResourceRevenueOperations, "exact:users.replace-group"}},
	{method: "POST", path: "/api/v1/admin/users/batch-concurrency", res: Resolved{ClassStepUpRequired, ReasonRevenueOperations, ResourceRevenueOperations, "exact:users.batch-concurrency"}},
	{method: "POST", path: "/api/v1/admin/users/batch-limits", res: Resolved{ClassStepUpRequired, ReasonRevenueOperations, ResourceRevenueOperations, "exact:users.batch-limits"}},
	{method: "POST", path: "/api/v1/admin/users/batch-disable", res: Resolved{ClassStepUpRequired, ReasonRevenueOperations, ResourceRevenueOperations, "exact:users.batch-disable"}},
	{method: "PUT", path: "/api/v1/admin/users/:id/platform-quotas", res: Resolved{ClassStepUpRequired, ReasonRevenueOperations, ResourceRevenueOperations, "exact:users.platform-quotas"}},
	{method: "POST", path: "/api/v1/admin/users/:id/platform-quotas/reset", res: Resolved{ClassStepUpRequired, ReasonRevenueOperations, ResourceRevenueOperations, "exact:users.platform-quotas-reset"}},
	{method: "PUT", path: "/api/v1/admin/groups/:id/rate-multipliers", res: Resolved{ClassStepUpRequired, ReasonRevenueOperations, ResourceRevenueOperations, "exact:groups.rate-multipliers"}},
	{method: "DELETE", path: "/api/v1/admin/groups/:id/rate-multipliers", res: Resolved{ClassStepUpRequired, ReasonRevenueOperations, ResourceRevenueOperations, "exact:groups.rate-multipliers.clear"}},
	// 破玻璃重置 2FA：管理员可清空任意用户（含管理员自己）的 TOTP，是一次
	// 「降低目标账号安全强度」的动作，必须先过管理员本人的 step-up 2FA；
	// 机器凭据默认拒绝（只有 machine_allowlisted 才接受机器凭据）。
	// 原因必填、审计同步落库、邮件通知目标用户，详见 service.Admin2FAResetService。
	{method: "POST", path: "/api/v1/admin/users/:id/reset-2fa", res: Resolved{ClassStepUpRequired, ReasonAccountSecurityReset, ResourceAccountSecurity, "exact:users.reset-2fa"}},
	// 危险的主机生命周期操作
	{method: "POST", path: "/api/v1/admin/system/update", res: Resolved{ClassStepUpRequired, ReasonHostLifecycleWrite, ResourceRevenueOperations, "exact:system.update"}},
	{method: "POST", path: "/api/v1/admin/system/rollback", res: Resolved{ClassStepUpRequired, ReasonHostLifecycleWrite, ResourceRevenueOperations, "exact:system.rollback"}},
	{method: "POST", path: "/api/v1/admin/system/restart", res: Resolved{ClassStepUpRequired, ReasonHostLifecycleWrite, ResourceRevenueOperations, "exact:system.restart"}},
}

// secretFreeExact pins read-only routes and non-mutating probe actions that are
// implemented with a POST verb (batch read APIs, connectivity tests). Without an
// exact entry the mutating-resource rule below would classify them as
// step_up_required, which turns every page load that batches aggregates (or
// every model click in the admin test panel) into a TOTP prompt. They must not
// return secret material: only already-redacted account usage/statistics
// aggregates and connectivity/model-availability probe results belong here.
var secretFreeExact = []exactRule{
	{method: "POST", path: "/api/v1/admin/accounts/today-stats/batch", res: Resolved{ClassSecretFree, ReasonAccountUsageAggregate, ResourceNone, "exact:accounts.today-stats-batch"}},
	{method: "POST", path: "/api/v1/admin/accounts/usage/batch", res: Resolved{ClassSecretFree, ReasonAccountUsageAggregate, ResourceNone, "exact:accounts.usage-batch"}},
	// 自定义用量读取：账号列表滚动时自动查询/批量拉缓存，只回用量数字，不含上游凭证。
	{method: "POST", path: "/api/v1/admin/accounts/:id/custom-usage-query", res: Resolved{ClassSecretFree, ReasonAccountUsageAggregate, ResourceNone, "exact:accounts.custom-usage-query"}},
	{method: "POST", path: "/api/v1/admin/accounts/custom-usage-batch", res: Resolved{ClassSecretFree, ReasonAccountUsageAggregate, ResourceNone, "exact:accounts.custom-usage-batch"}},
	// 上游声明倍率刷新：只返回公开的倍率数字，不返回上游凭证；账号列表每行一个
	// 刷新按钮，要求 2FA 会让正常运营点一次弹一次。
	{method: "POST", path: "/api/v1/admin/accounts/upstream-billing-probe/batch", res: Resolved{ClassSecretFree, ReasonUpstreamBillingProbe, ResourceNone, "exact:accounts.upstream-billing-probe"}},
	// 单行「上游声明倍率」刷新按钮走的是这条；同样只回公开倍率数字。
	{method: "POST", path: "/api/v1/admin/accounts/:id/upstream-billing-probe", res: Resolved{ClassSecretFree, ReasonUpstreamBillingProbe, ResourceNone, "exact:accounts.upstream-billing-probe-one"}},
	// 账号连通性/模型可用性测试：用库内凭据打上游，只返回测试状态与上游响应，
	// 不回传凭据。管理端「测试设置」按模型逐个点「测试/测试全部」，要求 2FA 会让
	// 批量测试每点一次弹一次码。凭据回读（GET /:id/credential）与导出（GET /data）
	// 仍保持 step-up，见 stepUpExact。
	{method: "POST", path: "/api/v1/admin/accounts/:id/test", res: Resolved{ClassSecretFree, ReasonAccountConnectivityTest, ResourceNone, "exact:accounts.connectivity-test"}},
	// ── 2026-11 2FA 触发必要性复核（低风险探测/聚合/预览）──────────────────────
	// 以下 POST 动作只是「用既有配置打一次连接、算一次预览、回一类聚合数字」，
	// 响应里没有凭据/密码/密钥/卡密明文，也不动钱、不改权限、不改安全配置。
	// 管理端把它们做成逐行按钮或页面加载自动动作，要求 TOTP 会让正常运营每点
	// 一次弹一次码。写操作、凭据回读、导出与安全配置保存仍保持 step-up。
	//
	// 代理连通性测试与质量检测：用库内代理 URL 探测出口 IP/延迟/目标可达性，
	// 只回 success/延迟/出口地区，不回代理用户名密码。
	{method: "POST", path: "/api/v1/admin/proxies/:id/test", res: Resolved{ClassSecretFree, ReasonProxyConnectivityTest, ResourceNone, "exact:proxies.connectivity-test"}},
	{method: "POST", path: "/api/v1/admin/proxies/:id/quality-check", res: Resolved{ClassSecretFree, ReasonProxyConnectivityTest, ResourceNone, "exact:proxies.quality-check"}},
	// 绑定账号前的混合渠道预检：纯计算，只回 has_risk/风险详情。
	{method: "POST", path: "/api/v1/admin/accounts/check-mixed-channel", res: Resolved{ClassSecretFree, ReasonAccountBindingPreflight, ResourceNone, "exact:accounts.check-mixed-channel"}},
	// 可调度定时器预览：按请求参数回算执行时间（execute_at/local_date/local_time），
	// 不落库、不改账号状态。
	{method: "POST", path: "/api/v1/admin/accounts/:id/schedulable-timers/preview", res: Resolved{ClassSecretFree, ReasonAccountTimerPreview, ResourceNone, "exact:accounts.schedulable-timer-preview"}},
	// Ollama Cloud 用量刷新：用库内会话抓官方用量，响应是脱敏后的用量窗口
	// （used_percent/reset_at/model/request 数），不含 session/HTML/凭据。
	{method: "POST", path: "/api/v1/admin/accounts/:id/ollama-cloud-usage/refresh", res: Resolved{ClassSecretFree, ReasonAccountUsageAggregate, ResourceNone, "exact:accounts.ollama-usage-refresh"}},
	// 上游模型目录同步/预览（管理端「模型限制 → 模型白名单」的「同步最新支持模型 /
	// 同步上游模型」按钮）：只拉上游 /models 列表，再用 models.dev 公开注册表补齐
	// 上下文/模态等能力元数据。
	//   - sync-upstream 的唯一写库动作是把模型能力快照写回
	//     accounts.extra[upstream_models]（模型 ID/上下文/模态等公开元数据，不含
	//     凭据、价格或权限）；
	//   - sync-upstream-preview 用表单里尚未保存的凭据打一次上游 /models，临时账号
	//     ID=0，不落库、不改账号配置。
	// 二者 base_url 都走 validateUpstreamBaseURL（urlvalidator allowlist + 私网拦截），
	// 与已放开的「账号测试」同一出站校验口径；响应只有模型列表与公开元数据。
	// 每次点击都要求 TOTP 会让「配模型白名单」变成不可用的逐次弹码，属误伤。
	// 凭据回读（GET /:id/credential）、账号导出（GET /data）、账号写操作
	// （PUT /:id、batch-update-credentials 等）仍由 stepUpExact /
	// mutating:accounts 保持 step_up_required。
	{method: "POST", path: "/api/v1/admin/accounts/:id/models/sync-upstream", res: Resolved{ClassSecretFree, ReasonUpstreamModelCatalogSync, ResourceNone, "exact:accounts.sync-upstream-models"}},
	{method: "POST", path: "/api/v1/admin/accounts/models/sync-upstream-preview", res: Resolved{ClassSecretFree, ReasonUpstreamModelCatalogPreview, ResourceNone, "exact:accounts.sync-upstream-models-preview"}},
	// 上游渠道公告同步：用库内渠道会话拉公告并 upsert，响应只有 fetched/added/
	// updated/unread 计数，不回渠道密码/子密钥/会话。
	{method: "POST", path: "/api/v1/admin/upstream-monitor/channels/:id/announcements/sync", res: Resolved{ClassSecretFree, ReasonChannelAnnouncementSync, ResourceNone, "exact:upstream-monitor.announcements-sync"}},
	// SMTP 连通性测试：只回 "SMTP connection successful" 或分类后的失败文案
	// （不回显 host:port/源端口/拒绝细节），不含 SMTP 密码。真正发信仍由
	// /settings/send-test-email 承担并保持 step-up（可被滥用发信）。
	{method: "POST", path: "/api/v1/admin/settings/test-smtp", res: Resolved{ClassSecretFree, ReasonSMTPConnectivityTest, ResourceNone, "exact:settings.test-smtp"}},
	// Web Search 模拟测试：用已保存的搜索配置跑一次查询，只回 provider/query/
	// results，不含 provider api_key。
	{method: "POST", path: "/api/v1/admin/settings/web-search-emulation/test", res: Resolved{ClassSecretFree, ReasonWebSearchEmulationProbe, ResourceNone, "exact:settings.web-search-test"}},
	// IP 风控预检：只回该 IP 的风险判定明细，不改封禁状态、不回任何密钥。
	{method: "POST", path: "/api/v1/admin/settings/ip-risk/check", res: Resolved{ClassSecretFree, ReasonIPRiskPreflightCheck, ResourceNone, "exact:settings.ip-risk-check"}},
	// 邮件模板预览：用未保存的 subject/HTML 与示例变量在内存渲染并回渲染结果，
	// 不写库、不出站、不接触任何密钥；保存模板（PUT）与发送测试邮件仍 step-up。
	{method: "POST", path: "/api/v1/admin/settings/email-template-preview", res: Resolved{ClassSecretFree, ReasonEmailTemplatePreview, ResourceNone, "exact:settings.email-template-preview"}},
}

// providerCredentialPrefixes are upstream provider OAuth families: POST/PUT/
// DELETE routes mint or replace upstream credentials and must be step-up
// gated, while GET status reads stay secret-free.
var providerCredentialPrefixes = []string{
	"/api/v1/admin/openai/",
	"/api/v1/admin/gemini/",
	"/api/v1/admin/antigravity/",
	"/api/v1/admin/grok/",
}

// maskedReadExact releases read-only GET routes that belong to a resource family
// which used to return plaintext secrets, but whose current read model only
// returns masked values, aggregates or metadata. It is checked BEFORE
// sensitiveResources/family rules, so a family prefix can no longer gate the
// whole family when the read model itself was hardened.
//
// Rules for adding an entry:
//  1. it must be a GET that returns no credential, no redeem/grant code
//     plaintext and no derivable secret;
//  2. the handler that used to return plaintext must have been changed first
//     (see dto.RedeemCode.Code, which is now always a mask);
//  3. machine credentials stay denied: these routes are NOT machine_allowlisted,
//     and the bulk-read ones keep middleware.ForbidMachineCredentials.
var maskedReadExact = []exactRule{
	// 卡密列表 / 详情 / 统计：dto.RedeemCode.Code 永远是掩码（前 4 位 + ****），
	// 明文只在 POST /generate 的当次回执里出现一次；/stats 目前是 501，不返回
	// 任何财务数字。人只会话读这三条不再弹 TOTP（路由层已挂机器凭据禁止）。
	{method: "GET", path: "/api/v1/admin/redeem-codes", res: Resolved{ClassSecretFree, ReasonRedeemCodeReadsMasked, ResourceNone, "exact:redeem-codes.masked-read"}},
	{method: "GET", path: "/api/v1/admin/redeem-codes/:id", res: Resolved{ClassSecretFree, ReasonRedeemCodeReadsMasked, ResourceNone, "exact:redeem-codes.masked-read-one"}},
	{method: "GET", path: "/api/v1/admin/redeem-codes/stats", res: Resolved{ClassSecretFree, ReasonRedeemCodeReadsMasked, ResourceNone, "exact:redeem-codes.masked-stats"}},
	// 优惠码使用记录只回 user/bonus/used_at，不含优惠码本身（DTO 没有 code 字段），
	// 因此不构成可兑换价值；优惠码列表/详情仍回明文 code，继续留在 step-up。
	{method: "GET", path: "/api/v1/admin/promo-codes/:id/usages", res: Resolved{ClassSecretFree, ReasonPromoUsageReadsNoCode, ResourceNone, "exact:promo-codes.usages"}},
}

// sensitiveResources are path prefixes where EVERY method (GET included) is
// step-up required because the resource itself returns secret material:
// promo grant codes. This is the structural answer to
// "只读所以不用 2FA".
//
// Redeem codes used to live here too, but the 2026-11 hardening turned the
// admin read model into masked values only (only the one-shot generate receipt
// carries plaintext) and the bulk export is pinned separately in stepUpExact.
// Keeping the whole prefix gated would fire a TOTP prompt on every page load of
// the redeem-code list, which is exactly the misclassification this matrix
// fixes. Writes stay gated, see mutating:redeem-codes.
//
// Proxies and upstream channels deliberately do not live here: their read
// models mask passwords / upstream keys (#497 and channel DTO json:"-"), so
// only the plaintext export / reveal POST and the write methods need step-up.
var sensitiveResources = []prefixRule{
	{prefix: "/api/v1/admin/promo-codes", methods: anyMethod(), res: Resolved{ClassStepUpRequired, ReasonPromoCodes, ResourcePromoCodes, "prefix:promo-codes"}},
}

// sensitiveMutatingResources are resources whose GET representations are
// explicitly redacted / non-secret, but whose writes can make the system hold
// or emit secrets (create credentials, change secrets, mint money, upload code).
// Every non-read method is step-up required.
var sensitiveMutatingResources = []prefixRule{
	{prefix: "/api/v1/admin/accounts", methods: mutatingMethods(), res: Resolved{ClassStepUpRequired, ReasonUpstreamCredentials, ResourceUpstreamAccounts, "mutating:accounts"}},
	// 卡密写操作（生成即产出可兑价值、固定码直发、改值/改状态/改过期、删除）
	// 全部保留 step-up；读已改为掩码，见 maskedReadExact。
	{prefix: "/api/v1/admin/redeem-codes", methods: mutatingMethods(), res: Resolved{ClassStepUpRequired, ReasonRedeemCodes, ResourceRedeemCodes, "mutating:redeem-codes"}},
	// 代理写操作可以设置/替换明文密码，必须 step-up；读操作已掩码，见 free:proxies。
	{prefix: "/api/v1/admin/proxies", methods: mutatingMethods(), res: Resolved{ClassStepUpRequired, ReasonProxyPasswords, ResourceProxies, "mutating:proxies"}},
	// 渠道写操作与「查看 API Key 明文」的 reveal POST 会持有上游密钥，必须 step-up；
	// 列表/详情/余额/订阅等读接口的密钥字段已脱敏，见 free:upstream-monitor.channels。
	{prefix: "/api/v1/admin/upstream-monitor/channels", methods: mutatingMethods(), res: Resolved{ClassStepUpRequired, ReasonChannelKeys, ResourceChannels, "mutating:upstream-monitor.channels"}},
	{prefix: "/api/v1/admin/settings", methods: mutatingMethods(), res: Resolved{ClassStepUpRequired, ReasonAdminSecrets, ResourceAdminSettings, "mutating:settings"}},
	{prefix: "/api/v1/admin/payment", methods: mutatingMethods(), res: Resolved{ClassStepUpRequired, ReasonPaymentSecrets, ResourcePayments, "mutating:payment"}},
	{prefix: "/api/v1/admin/data-management", methods: mutatingMethods(), res: Resolved{ClassStepUpRequired, ReasonDataManagementWrite, ResourceBackups, "mutating:data-management"}},
	{prefix: "/api/v1/admin/backups", methods: mutatingMethods(), res: Resolved{ClassStepUpRequired, ReasonBackupSecrets, ResourceBackups, "mutating:backups"}},
	{prefix: "/api/v1/admin/request-captures", methods: mutatingMethods(), res: Resolved{ClassStepUpRequired, ReasonCapturedTraffic, ResourceCapturedTraffic, "mutating:request-captures"}},
	{prefix: "/api/v1/admin/plugins", methods: mutatingMethods(), res: Resolved{ClassStepUpRequired, ReasonPluginCode, ResourcePlugins, "mutating:plugins"}},
	{prefix: "/api/v1/admin/account-ops", methods: mutatingMethods(), res: Resolved{ClassStepUpRequired, ReasonAccountOpsSecrets, ResourceAccountOps, "mutating:account-ops"}},
	{prefix: "/api/v1/admin/credential-guard", methods: mutatingMethods(), res: Resolved{ClassStepUpRequired, ReasonCredentialGuard, ResourceCredentialGuard, "mutating:credential-guard"}},
	{prefix: "/api/v1/admin/excel-grants", methods: mutatingMethods(), res: Resolved{ClassStepUpRequired, ReasonExcelGrantSecrets, ResourceExcelGrants, "mutating:excel-grants"}},
	{prefix: "/api/v1/admin/system/mihomo", methods: mutatingMethods(), res: Resolved{ClassStepUpRequired, ReasonMihomoConfig, ResourceMihomo, "mutating:system.mihomo"}},
	// 风控审计 / 提示词审计 / 渠道监控与模板 / 渠道定价运维 / 安全处罚写操作：
	// 这些入口持有外部审计/探测 API Key 或控制计费渠道与风控处罚策略，必须要求 step-up 2FA；
	// 只读 GET 仍走各自 secretFreeFamilies，不打断页面首屏加载。
	{prefix: "/api/v1/admin/risk-control", methods: mutatingMethods(), res: Resolved{ClassStepUpRequired, ReasonModerationWrite, ResourceModeration, "mutating:risk-control"}},
	{prefix: "/api/v1/admin/prompt-audit", methods: mutatingMethods(), res: Resolved{ClassStepUpRequired, ReasonPromptAuditWrite, ResourcePromptAudit, "mutating:prompt-audit"}},
	{prefix: "/api/v1/admin/channel-monitors", methods: mutatingMethods(), res: Resolved{ClassStepUpRequired, ReasonChannelMonitorWrite, ResourceChannelMonitors, "mutating:channel-monitors"}},
	{prefix: "/api/v1/admin/channel-monitor-templates", methods: mutatingMethods(), res: Resolved{ClassStepUpRequired, ReasonChannelMonitorWrite, ResourceChannelMonitors, "mutating:channel-monitor-templates"}},
	{prefix: "/api/v1/admin/channels", methods: mutatingMethods(), res: Resolved{ClassStepUpRequired, ReasonChannelPricingWrite, ResourceChannels, "mutating:channels"}},
	{prefix: "/api/v1/admin/security-penalties", methods: mutatingMethods(), res: Resolved{ClassStepUpRequired, ReasonSecurityPenaltyWrite, ResourceSecurityPenalties, "mutating:security-penalties"}},
}

// secretFreeFamilies cover every remaining route. A route that matches none of
// the rules above resolves to "unknown" and is treated as step-up required at
// runtime; the coverage test refuses it outright.
var secretFreeFamilies = []prefixRule{
	{prefix: "/api/v1/admin/dashboard", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonDashboardAggregate, ResourceNone, "free:dashboard"}},
	{prefix: "/api/v1/admin/ops", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonOpsReadOnlyConfig, ResourceNone, "free:ops"}},
	{prefix: "/api/v1/admin/usage", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonUsageAnalytics, ResourceNone, "free:usage"}},
	{prefix: "/api/v1/admin/users", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonUserAdministration, ResourceNone, "free:users"}},
	{prefix: "/api/v1/admin/groups", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonGroupRoutingConfig, ResourceNone, "free:groups"}},
	{prefix: "/api/v1/admin/announcements", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonPublicContent, ResourceNone, "free:announcements"}},
	{prefix: "/api/v1/admin/compliance", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonComplianceState, ResourceNone, "free:compliance"}},
	{prefix: "/api/v1/admin/system", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonHostLifecycleInfo, ResourceNone, "free:system"}},
	{prefix: "/api/v1/admin/model-plaza", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonPublicModelCatalog, ResourceNone, "free:model-plaza"}},
	{prefix: "/api/v1/admin/models", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonModelRegistryMeta, ResourceNone, "free:models"}},
	{prefix: "/api/v1/admin/cn-providers", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonUpstreamQuotaSummary, ResourceNone, "free:cn-providers"}},
	{prefix: "/api/v1/admin/tickets", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonSupportTickets, ResourceNone, "free:tickets"}},
	{prefix: "/api/v1/admin/audit-logs", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonAuditTrail, ResourceNone, "free:audit-logs"}},
	{prefix: "/api/v1/admin/risk-control", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonModerationConfig, ResourceNone, "free:risk-control"}},
	{prefix: "/api/v1/admin/channel-monitors", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonChannelMonitorConfig, ResourceNone, "free:channel-monitors"}},
	{prefix: "/api/v1/admin/channel-monitor-templates", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonChannelMonitorConfig, ResourceNone, "free:channel-monitor-templates"}},
	{prefix: "/api/v1/admin/channel-monitor-v2", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonChannelMonitorConfig, ResourceNone, "free:channel-monitor-v2"}},
	{prefix: "/api/v1/admin/scheduled-test-plans", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonTestPlanning, ResourceNone, "free:scheduled-test-plans"}},
	{prefix: "/api/v1/admin/quality-check", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonQualityCheckResults, ResourceNone, "free:quality-check"}},
	{prefix: "/api/v1/admin/security-penalties", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonPenaltyState, ResourceNone, "free:security-penalties"}},
	{prefix: "/api/v1/admin/user-attributes", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonUserAttributeCatalog, ResourceNone, "free:user-attributes"}},
	{prefix: "/api/v1/admin/error-passthrough-rules", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonErrorPassthrough, ResourceNone, "free:error-passthrough-rules"}},
	{prefix: "/api/v1/admin/tls-fingerprint-profiles", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonTLSProfile, ResourceNone, "free:tls-fingerprint-profiles"}},
	{prefix: "/api/v1/admin/serverless", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonServerlessConfig, ResourceNone, "free:serverless"}},
	{prefix: "/api/v1/admin/ops-automation", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonOpsAutomationConfig, ResourceNone, "free:ops-automation"}},
	{prefix: "/api/v1/admin/affiliates", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonAffiliateAccounting, ResourceNone, "free:affiliates"}},
	{prefix: "/api/v1/admin/subscriptions", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonSubscriptionState, ResourceNone, "free:subscriptions"}},
	{prefix: "/api/v1/admin/openai", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonProviderOAuthStatus, ResourceNone, "free:openai"}},
	{prefix: "/api/v1/admin/gemini", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonProviderOAuthStatus, ResourceNone, "free:gemini"}},
	{prefix: "/api/v1/admin/antigravity", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonProviderOAuthStatus, ResourceNone, "free:antigravity"}},
	{prefix: "/api/v1/admin/grok", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonProviderOAuthStatus, ResourceNone, "free:grok"}},
	{prefix: "/api/v1/admin/channels", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonChannelPricingConfig, ResourceNone, "free:channels"}},
	// 渠道读模型（DTO）不序列化 password/access_token，API Key 列表显式清空 Key，
	// 只有 reveal POST 才返回明文；因此渠道读不弹 TOTP（必须排在 free:upstream-monitor 之前）。
	{prefix: "/api/v1/admin/upstream-monitor/channels", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonChannelReadsRedacted, ResourceNone, "free:upstream-monitor.channels"}},
	{prefix: "/api/v1/admin/upstream-monitor", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonUpstreamMonitorTelemetry, ResourceNone, "free:upstream-monitor"}},
	{prefix: "/api/v1/admin/crypto", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonCryptoDedicatedGate, ResourceNone, "free:crypto"}},
	{prefix: "/api/v1/admin/prompt-audit", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonPromptAuditNoCredential, ResourceNone, "free:prompt-audit"}},
	{prefix: "/api/v1/admin/accounts", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonAccountReadsRedacted, ResourceNone, "free:accounts"}},
	// 代理列表/详情/统计/关联账号的密码字段已在 #497 改掩码；明文只在
	// exact:proxies.data（导出）与写操作里出现，所以读接口不能整片要求 2FA。
	{prefix: "/api/v1/admin/proxies", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonProxyReadsRedacted, ResourceNone, "free:proxies"}},
	{prefix: "/api/v1/admin/settings", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonSettingsReadNoSecret, ResourceNone, "free:settings"}},
	{prefix: "/api/v1/admin/payment", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonPaymentReadsReadOnly, ResourceNone, "free:payment"}},
	{prefix: "/api/v1/admin/data-management", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonBackupReadsMetadataOnly, ResourceNone, "free:data-management"}},
	{prefix: "/api/v1/admin/backups", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonBackupReadsMetadataOnly, ResourceNone, "free:backups"}},
	{prefix: "/api/v1/admin/request-captures", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonCaptureReadsHandlerGate, ResourceNone, "free:request-captures"}},
	{prefix: "/api/v1/admin/plugins", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonPluginReadsMetadataOnly, ResourceNone, "free:plugins"}},
	{prefix: "/api/v1/admin/account-ops", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonOpsReadOnlyConfig, ResourceNone, "free:account-ops"}},
	{prefix: "/api/v1/admin/credential-guard", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonCredentialGuard, ResourceNone, "free:credential-guard"}},
	{prefix: "/api/v1/admin/excel-grants", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonExcelGrantSecrets, ResourceNone, "free:excel-grants"}},
	{prefix: "/api/v1/admin/debug-dumps", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonDebugDumps, ResourceNone, "free:debug-dumps"}},
	{prefix: "/api/v1/admin/system/mihomo", methods: anyMethod(), res: Resolved{ClassSecretFree, ReasonMihomoReadsRedacted, ResourceNone, "free:system.mihomo"}},
}

// stepUpReadRuleJustifications records, per registry rule, why a read-only GET
// still requires step-up. The 2026-11 incident (admin pages breaking on a TOTP
// prompt fired by page-load GETs) showed that "the resource name sounds
// sensitive" is not a justification. The coverage test fails when a GET route
// resolves to step_up_required through a rule missing here, so adding a new
// read gate requires an explicit, reviewable reason.
var stepUpReadRuleJustifications = map[string]string{
	"exact:users.api-keys":      "GET returns plaintext user API keys",
	"exact:groups.api-keys":     "GET returns plaintext group API keys",
	"exact:accounts.credential": "GET returns the decrypted upstream credential",
	"exact:accounts.export":     "GET streams every upstream credential in plaintext",
	"exact:backups.download":    "GET mints a download URL for a full database backup",
	"exact:backups.s3-config":   "GET returns the S3 object-storage credentials",
	"exact:debug-dumps.payload": "GET returns captured request/response bodies",
	"exact:proxies.data":        "GET exports proxy username/password in plaintext",
	"exact:redeem-codes.export": "GET streams up to 10000 business records as CSV (masked, but bulk)",
	"prefix:promo-codes":        "read model returns promo grant values",
}

// StepUpReadRuleJustification returns the documented reason why a registry rule
// gates read-only GET requests. The second result is false for rules that gate
// writes only (which need no read justification).
func StepUpReadRuleJustification(rule string) (string, bool) {
	reason, ok := stepUpReadRuleJustifications[rule]
	return reason, ok && reason != ""
}

// Resolve classifies an admin route.
//
// Precedence: machine allowlist (exact) → step-up exact → masked read exact
// (hardened read model of a formerly sensitive family) → sensitive resource
// (any method) → secret-free exact (read-only POST probes/batch reads) →
// sensitive mutating resource (non-GET) → provider credential mutation →
// secret-free family. Only routes outside /api/v1/admin are returned as
// unknown; every admin route must match.
func Resolve(method, fullPath string) (Resolved, bool) {
	method = strings.ToUpper(strings.TrimSpace(method))
	if !strings.HasPrefix(fullPath, "/api/v1/admin") {
		return Resolved{}, false
	}
	for i := range machineAllowlisted {
		if machineAllowlisted[i].method == method && machineAllowlisted[i].path == fullPath {
			return machineAllowlisted[i].res, true
		}
	}
	for i := range stepUpExact {
		if stepUpExact[i].method == method && stepUpExact[i].path == fullPath {
			return stepUpExact[i].res, true
		}
	}
	for i := range maskedReadExact {
		if maskedReadExact[i].method == method && maskedReadExact[i].path == fullPath {
			return maskedReadExact[i].res, true
		}
	}
	for i := range sensitiveResources {
		if ruleMatches(&sensitiveResources[i], method, fullPath) {
			return sensitiveResources[i].res, true
		}
	}
	for i := range secretFreeExact {
		if secretFreeExact[i].method == method && secretFreeExact[i].path == fullPath {
			return secretFreeExact[i].res, true
		}
	}
	for i := range sensitiveMutatingResources {
		if ruleMatches(&sensitiveMutatingResources[i], method, fullPath) {
			return sensitiveMutatingResources[i].res, true
		}
	}
	if isProviderCredentialMutation(method, fullPath) {
		return Resolved{ClassStepUpRequired, ReasonUpstreamCredentials, ResourceUpstreamAccounts, "provider-credential-mutation"}, true
	}
	for i := range secretFreeFamilies {
		if ruleMatches(&secretFreeFamilies[i], method, fullPath) {
			return secretFreeFamilies[i].res, true
		}
	}
	return Resolved{}, false
}

func ruleMatches(rule *prefixRule, method, fullPath string) bool {
	if !pathHasPrefix(fullPath, rule.prefix) {
		return false
	}
	if rule.methods == nil {
		return true
	}
	_, ok := rule.methods[method]
	return ok
}

// pathHasPrefix matches a route path against a resource prefix on a path
// segment boundary so "/api/v1/admin/backups" never swallows
// "/api/v1/admin/backupsomething".
func pathHasPrefix(path, prefix string) bool {
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	if len(path) == len(prefix) {
		return true
	}
	return path[len(prefix)] == '/'
}

func isProviderCredentialMutation(method, fullPath string) bool {
	switch method {
	case "POST", "PUT", "PATCH", "DELETE":
	default:
		return false
	}
	for _, prefix := range providerCredentialPrefixes {
		if pathHasPrefix(fullPath, prefix) {
			return true
		}
	}
	return false
}

// IsMachineAllowlisted reports whether a machine credential may call the route.
func IsMachineAllowlisted(method, fullPath string) bool {
	res, ok := Resolve(method, fullPath)
	return ok && res.Class == ClassMachineAllowlisted
}

// IsStepUpRequired reports whether a route requires a human step-up session.
// Unknown admin routes read as step-up required (fail-closed).
func IsStepUpRequired(method, fullPath string) bool {
	res, ok := Resolve(method, fullPath)
	return !ok || res.Class == ClassStepUpRequired
}

// SecretFreeReason returns the secret-free family reason backing a route. It is
// used by the coverage test to assert that machine-allowlisted routes live in
// families explicitly declared to contain no secret material.
func SecretFreeReason(method, fullPath string) (Reason, bool) {
	if !strings.HasPrefix(fullPath, "/api/v1/admin") {
		return "", false
	}
	for i := range secretFreeFamilies {
		if ruleMatches(&secretFreeFamilies[i], method, fullPath) {
			return secretFreeFamilies[i].res.Reason, true
		}
	}
	return "", false
}

// Route is the minimal shape needed for coverage validation, decoupled from gin.
type Route struct {
	Method string
	Path   string
}

// UnclassifiedAdminRoutes returns the method+path of every admin route that no
// registry rule classifies. An empty result means the registry is complete.
func UnclassifiedAdminRoutes(routes []Route) []string {
	var unclassified []string
	for _, route := range routes {
		if !strings.HasPrefix(route.Path, "/api/v1/admin") {
			continue
		}
		if _, ok := Resolve(route.Method, route.Path); !ok {
			unclassified = append(unclassified, route.Method+" "+route.Path)
		}
	}
	return unclassified
}

// MachineAllowlist returns a copy of the machine credential allowlist, for
// tests and documentation.
func MachineAllowlist() []Route {
	out := make([]Route, 0, len(machineAllowlisted))
	for _, rule := range machineAllowlisted {
		out = append(out, Route{Method: rule.method, Path: rule.path})
	}
	return out
}

// SensitiveResourceFor returns the sensitive resource family a path belongs to,
// if any. Used by tests that assert allowlisted routes never touch one.
func SensitiveResourceFor(fullPath string) (Resource, bool) {
	for i := range sensitiveResources {
		if pathHasPrefix(fullPath, sensitiveResources[i].prefix) {
			return sensitiveResources[i].res.Resource, true
		}
	}
	for i := range sensitiveMutatingResources {
		if pathHasPrefix(fullPath, sensitiveMutatingResources[i].prefix) {
			return sensitiveMutatingResources[i].res.Resource, true
		}
	}
	for i := range providerCredentialPrefixes {
		if pathHasPrefix(fullPath, providerCredentialPrefixes[i]) {
			return ResourceUpstreamAccounts, true
		}
	}
	return ResourceNone, false
}
