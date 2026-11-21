package repository

import "github.com/Wei-Shaw/sub2api/internal/service"

// EnableManagedAccountProxyEncryption wires decryption before scheduler
// snapshots are populated. Configure once during dependency construction.
func EnableManagedAccountProxyEncryption(repo service.AccountRepository, encryptor service.SecretEncryptor) service.AccountRepository {
	if r, ok := repo.(*accountRepository); ok {
		r.proxySecretEncryptor = encryptor
	}
	return repo
}
