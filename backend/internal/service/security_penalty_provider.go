package service

import (
	"database/sql"
	"log/slog"
)

func ProvideSecurityPenaltyService(db *sql.DB, settings SettingRepository) *SecurityPenaltyService {
	svc := NewSecurityPenaltyService(db, settings)
	if err := svc.Start(); err != nil {
		slog.Warn("security_penalty.initial_refresh_deferred")
	}
	return svc
}

func ProvideContentModerationWithPenalties(
	settings SettingRepository,
	repo ContentModerationRepository,
	hashCache ContentModerationHashCache,
	groups GroupRepository,
	users UserRepository,
	proxies ProxyRepository,
	invalidator APIKeyAuthCacheInvalidator,
	email *EmailService,
	penalties *SecurityPenaltyService,
) *ContentModerationService {
	svc := NewContentModerationService(settings, repo, hashCache, groups, users, proxies, invalidator, email)
	svc.SetSecurityPenalties(penalties)
	return svc
}
