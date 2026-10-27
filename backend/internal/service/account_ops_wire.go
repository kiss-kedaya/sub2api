package service

import "github.com/Wei-Shaw/sub2api/internal/config"

func ProvideAccountOpsService(settings SettingRepository, repo AccountOpsRepository, email *EmailService, accounts AccountRepository, encryptor SecretEncryptor, cfg *config.Config, usageCache *UsageCache, usageLogs UsageLogRepository, quota *GeminiQuotaService) *AccountOpsService {
	svc := NewAccountOpsService(settings, repo, email)
	svc.SetNotificationDependencies(accounts, encryptor, cfg.Totp.HasPersistentEncryptionKey(), cfg.Timezone)
	svc.SetNotificationUsageCache(usageCache)
	svc.SetNotificationQuotaReaders(usageLogs, quota)
	// Durable leases deduplicate shared work. Every replica must still persist its
	// own observed signals, including replicas that do not own the test runner.
	if BackgroundJobsDisabled() {
		svc.StartGateway()
	} else {
		svc.Start()
	}
	return svc
}
