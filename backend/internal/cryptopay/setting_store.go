package cryptopay

import (
	"context"
	"database/sql"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/addresses"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// NewAddressService builds the address pool service: reads the signer socket
// path from config (falling back to the default) and wires the signer client.
func NewAddressService(db *sql.DB, settings *service.SettingService) *addresses.Service {
	socket := DefaultCryptoConfig().SignerSocket
	if settings != nil {
		if cfg, err := LoadConfig(context.Background(), NewServiceSettingStore(settings)); err == nil && cfg.SignerSocket != "" {
			socket = cfg.SignerSocket
		}
	}
	return addresses.NewService(addresses.NewRepo(db), addresses.NewSignerClient(socket))
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
	return s.settings.SetRawSetting(ctx, key, value)
}
