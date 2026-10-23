package service

import (
	"context"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// AtomicSettingRepository is an optional repository capability for settings
// whose writes must use the same uncached value that an operator reviewed.
// A nil expected value means the key was absent, not an empty stored value.
type AtomicSettingRepository interface {
	GetValueUncached(context.Context, string) (string, error)
	CompareAndSwapValue(context.Context, string, *string, string) (bool, error)
}

var ErrAtomicSettingUnavailable = infraerrors.ServiceUnavailable("ATOMIC_SETTING_UNAVAILABLE", "Atomic settings updates are unavailable")

func (s *SettingService) GetRawSettingUncached(ctx context.Context, key string) (string, error) {
	if s == nil || s.settingRepo == nil {
		return "", ErrAtomicSettingUnavailable
	}
	repo, ok := s.settingRepo.(AtomicSettingRepository)
	if !ok {
		return "", ErrAtomicSettingUnavailable
	}
	return repo.GetValueUncached(ctx, key)
}

// CompareAndSwapRawSetting never falls back to a read followed by Set: that
// would turn a concurrent configuration change into an unguarded overwrite.
func (s *SettingService) CompareAndSwapRawSetting(ctx context.Context, key string, expected *string, value string) (bool, error) {
	if s == nil || s.settingRepo == nil {
		return false, ErrAtomicSettingUnavailable
	}
	repo, ok := s.settingRepo.(AtomicSettingRepository)
	if !ok {
		return false, ErrAtomicSettingUnavailable
	}
	changed, err := repo.CompareAndSwapValue(ctx, key, expected, value)
	if err == nil && changed && s.onUpdate != nil {
		s.onUpdate()
	}
	return changed, err
}
