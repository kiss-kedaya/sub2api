package repository

import (
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// usageLogAccountEntityToService decrypts credentials for the usage-log account
// hydration path (which reads accounts outside the account repository). A value
// that cannot be decrypted fails the load instead of being projected empty.
func usageLogAccountEntityToService(vault service.SecretVault, m *dbent.Account) (*service.Account, error) {
	credentials, _, err := service.DecryptCredentialsFromStorage(vault, m.Credentials)
	if err != nil {
		return nil, err
	}
	if m.Credentials == nil {
		credentials = nil
	}
	return accountEntityToService(m, credentials), nil
}
