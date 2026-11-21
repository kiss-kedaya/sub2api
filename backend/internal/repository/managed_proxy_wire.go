package repository

import (
	"database/sql"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func ProvideManagedProxyRepository(client *dbent.Client, db *sql.DB, vault service.SecretVault) service.ProxyRepository {
	inner := newProxyRepositoryWithSQL(client, db)
	inner.setCredentialVault(vault)
	return service.WrapEncryptedProxyRepository(inner, vault, db)
}

func ProvideManagedAccountRepository(client *dbent.Client, db *sql.DB, cache service.SchedulerCache, vault service.SecretVault) service.AccountRepository {
	repo := newAccountRepositoryWithSQL(client, db, cache)
	useAccountCredentialVault(repo, vault)
	repo.proxyVault = vault
	return repo
}
