package cryptopay

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

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
