package repository

import (
	"database/sql"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func ProvideManagedProxyRepository(client *dbent.Client, db *sql.DB, encryptor service.SecretEncryptor) service.ProxyRepository {
	return service.WrapEncryptedManagedProxyRepository(NewProxyRepository(client, db), encryptor)
}

func ProvideManagedAccountRepository(client *dbent.Client, db *sql.DB, cache service.SchedulerCache, encryptor service.SecretEncryptor) service.AccountRepository {
	return EnableManagedAccountProxyEncryption(NewAccountRepository(client, db, cache), encryptor)
}
