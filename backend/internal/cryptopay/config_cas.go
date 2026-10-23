package cryptopay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// AtomicSettingStore is required for live configuration patches. Reading and
// conditionally writing the raw value prevents stale whole-config overwrites.
type AtomicSettingStore interface {
	GetCryptoSettingUncached(context.Context, string) (string, error)
	CompareAndSwapCryptoSetting(context.Context, string, *string, string) (bool, error)
}

var ErrConfigConflict = infraerrors.Conflict("CRYPTO_CONFIG_CONFLICT", "充值配置已被其他请求修改，请刷新后重新验证并保存")

// LoadConfigForUpdate bypasses settings caches. expected retains the exact raw
// value read, including whitespace, or nil for a genuinely absent first-run key.
func LoadConfigForUpdate(ctx context.Context, store SettingStore) (cfg CryptoConfig, expected *string, err error) {
	atomicStore, ok := store.(AtomicSettingStore)
	if !ok {
		return cfg, nil, service.ErrAtomicSettingUnavailable
	}
	raw, err := atomicStore.GetCryptoSettingUncached(ctx, SettingKeyCryptoConfig)
	if errors.Is(err, service.ErrSettingNotFound) {
		return DefaultCryptoConfig(), nil, nil
	}
	if err != nil {
		return cfg, nil, err
	}
	cfg = DefaultCryptoConfig()
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			return cfg, nil, fmt.Errorf("crypto config: invalid JSON: %w", err)
		}
	}
	cfg, err = NormalizeConfig(cfg)
	return cfg, &raw, err
}

// SaveConfigIfUnchanged commits only against the exact snapshot used for the
// patch and destination notification. A conflict requires a fresh user action;
// retrying here could silently change which destination was approved/notified.
func SaveConfigIfUnchanged(ctx context.Context, store SettingStore, cfg CryptoConfig, expected *string) error {
	if err := mode.CheckWritable(); err != nil {
		return err
	}
	atomicStore, ok := store.(AtomicSettingStore)
	if !ok {
		return service.ErrAtomicSettingUnavailable
	}
	normalized, err := NormalizeConfig(cfg)
	if err != nil {
		return err
	}
	data, err := json.Marshal(normalized)
	if err != nil {
		return err
	}
	changed, err := atomicStore.CompareAndSwapCryptoSetting(ctx, SettingKeyCryptoConfig, expected, string(data))
	if err != nil {
		return err
	}
	if !changed {
		return ErrConfigConflict
	}
	return nil
}
