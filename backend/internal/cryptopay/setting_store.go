package cryptopay

import (
	"context"
	"database/sql"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/addresses"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// NewAddressService builds persistence; the crypto facade attaches its local wallet.
func NewAddressService(db *sql.DB, settings *service.SettingService) *addresses.Service {
	return addresses.NewService(addresses.NewRepo(db), nil)
}

// ServiceSettingStore adapts the shared SettingService to the narrow
// SettingStore the crypto config needs, so the cryptopay package does not
// depend on the full settings repository.
type ServiceSettingStore struct {
	settings *service.SettingService
}

func NewServiceSettingStore(settings *service.SettingService) *ServiceSettingStore {
	return &ServiceSettingStore{settings: settings}
}

// GetCryptoSetting reads one key through the settings repository.
func (s *ServiceSettingStore) GetCryptoSetting(ctx context.Context, key string) (string, error) {
	return s.settings.GetRawSetting(ctx, key)
}

// SetCryptoSetting writes one key through the settings repository.
func (s *ServiceSettingStore) SetCryptoSetting(ctx context.Context, key, value string) error {
	if err := mode.CheckWritable(); err != nil {
		return err
	}
	return s.settings.SetRawSetting(ctx, key, value)
}
