package repository

import (
	"database/sql"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

// ProvideSecretsVault builds the installation-key vault from the bootstrapped
// configuration. Production bootstrap always supplies the durable installation
// key; a missing key yields an unavailable vault and encrypted rows fail closed.
func ProvideSecretsVault(cfg *config.Config) service.SecretVault {
	return service.NewSecretVault(cfg)
}

// ProvideAdminAccountRepository wires the installation-key vault into the admin
// account repository.
func ProvideAdminAccountRepository(client *dbent.Client, db *sql.DB, cache service.SchedulerCache, vault service.SecretVault) service.AdminAccountRepository {
	repo := newAccountRepositoryWithSQL(client, db, cache)
	useAccountCredentialVault(repo, vault)
	repo.proxyVault = vault
	return repo
}

// ProvideUsageLogRepository wires the vault so usage-log joins can decrypt the
// account credentials they project.
func ProvideUsageLogRepository(client *dbent.Client, db *sql.DB, rdb *redis.Client, cfg *config.Config, vault service.SecretVault) service.UsageLogRepository {
	return NewUsageLogRepository(client, db, rdb, cfg, vault)
}

// ProvideCredentialGuardRepository wires the vault for its credential
// compare-and-swap writes.
func ProvideCredentialGuardRepository(db *sql.DB, vault service.SecretVault) service.CredentialGuardRepository {
	return NewCredentialGuardRepository(db, vault)
}

// ProvideExcelGrantRepository wires the vault for its account credential read.
func ProvideExcelGrantRepository(db *sql.DB, vault service.SecretVault) service.ExcelGrantRepository {
	return NewExcelGrantRepository(db, vault)
}

// ProvideSettingRepository wires the vault into the settings store so sensitive
// setting rows are encrypted at rest.
func ProvideSettingRepository(client *dbent.Client, vault service.SecretVault) service.SettingRepository {
	return NewSettingRepository(client, vault)
}
