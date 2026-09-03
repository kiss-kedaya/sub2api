package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const openAIAPIKeyHealthBreakerSettingsCacheTTL = 30 * time.Second
const openAIAPIKeyHealthBreakerSettingsErrorTTL = 5 * time.Second
const openAIAPIKeyHealthBreakerSettingsDBTimeout = 5 * time.Second
const openAIAPIKeyHealthBreakerSettingsRefreshKey = "openai_apikey_health_breaker_settings"

type cachedOpenAIAPIKeyHealthBreakerSettings struct {
	settings  OpenAIAPIKeyHealthBreakerSettings
	expiresAt time.Time
}

func normalizeOpenAIAPIKeyHealthBreakerSettings(settings *OpenAIAPIKeyHealthBreakerSettings) *OpenAIAPIKeyHealthBreakerSettings {
	if settings == nil {
		return DefaultOpenAIAPIKeyHealthBreakerSettings()
	}
	result := *settings
	if result.WindowMinutes < 1 {
		result.WindowMinutes = 1
	} else if result.WindowMinutes > 60 {
		result.WindowMinutes = 60
	}
	if result.FailureThreshold < 1 {
		result.FailureThreshold = 1
	} else if result.FailureThreshold > 10000 {
		result.FailureThreshold = 10000
	}
	if result.CooldownMinutes < 1 {
		result.CooldownMinutes = 1
	} else if result.CooldownMinutes > 60 {
		result.CooldownMinutes = 60
	}
	return &result
}

func (s *SettingService) GetOpenAIAPIKeyHealthBreakerSettings(ctx context.Context) (*OpenAIAPIKeyHealthBreakerSettings, error) {
	if s == nil || s.settingRepo == nil {
		return DefaultOpenAIAPIKeyHealthBreakerSettings(), nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if cached, ok := s.openAIAPIKeyHealthBreakerCache.Load().(*cachedOpenAIAPIKeyHealthBreakerSettings); ok && cached != nil {
		if time.Now().Before(cached.expiresAt) {
			result := cached.settings
			return &result, nil
		}
		if schedulerSnapshotOnlyFromContext(ctx) {
			// Health breaker settings are control-plane data. Keep the last known
			// policy usable while one detached refresh updates it in the background;
			// an expired cache must never add a synchronous DB read to a model turn.
			s.refreshOpenAIAPIKeyHealthBreakerSettingsAsync()
			result := cached.settings
			return &result, nil
		}
	}
	if schedulerSnapshotOnlyFromContext(ctx) {
		s.refreshOpenAIAPIKeyHealthBreakerSettingsAsync()
		return DefaultOpenAIAPIKeyHealthBreakerSettings(), nil
	}

	resultCh := s.openAIAPIKeyHealthBreakerSF.DoChan(openAIAPIKeyHealthBreakerSettingsRefreshKey, func() (any, error) {
		loadCtx := context.WithoutCancel(ctx)
		loadCtx, cancel := context.WithTimeout(loadCtx, openAIAPIKeyHealthBreakerSettingsDBTimeout)
		defer cancel()
		return s.loadOpenAIAPIKeyHealthBreakerSettings(loadCtx)
	})
	select {
	case result := <-resultCh:
		if result.Err != nil {
			return nil, result.Err
		}
		if settings, ok := result.Val.(*OpenAIAPIKeyHealthBreakerSettings); ok && settings != nil {
			return settings, nil
		}
		return DefaultOpenAIAPIKeyHealthBreakerSettings(), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *SettingService) loadOpenAIAPIKeyHealthBreakerSettings(ctx context.Context) (*OpenAIAPIKeyHealthBreakerSettings, error) {
	if s == nil || s.settingRepo == nil {
		return DefaultOpenAIAPIKeyHealthBreakerSettings(), nil
	}
	if cached, ok := s.openAIAPIKeyHealthBreakerCache.Load().(*cachedOpenAIAPIKeyHealthBreakerSettings); ok && cached != nil && time.Now().Before(cached.expiresAt) {
		result := cached.settings
		return &result, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	settings := DefaultOpenAIAPIKeyHealthBreakerSettings()
	value, err := s.settingRepo.GetValue(ctx, SettingKeyOpenAIAPIKeyHealthBreakerSettings)
	if errors.Is(err, ErrSettingNotFound) {
		err = nil
	}
	if err == nil && strings.TrimSpace(value) != "" {
		var stored OpenAIAPIKeyHealthBreakerSettings
		if json.Unmarshal([]byte(value), &stored) == nil {
			settings = normalizeOpenAIAPIKeyHealthBreakerSettings(&stored)
		}
	}
	return s.storeOpenAIAPIKeyHealthBreakerSettings(settings, err)
}

func (s *SettingService) storeOpenAIAPIKeyHealthBreakerSettings(settings *OpenAIAPIKeyHealthBreakerSettings, loadErr error) (*OpenAIAPIKeyHealthBreakerSettings, error) {
	if settings == nil {
		settings = DefaultOpenAIAPIKeyHealthBreakerSettings()
	}
	ttl := openAIAPIKeyHealthBreakerSettingsCacheTTL
	if loadErr != nil {
		ttl = openAIAPIKeyHealthBreakerSettingsErrorTTL
		if cached, ok := s.openAIAPIKeyHealthBreakerCache.Load().(*cachedOpenAIAPIKeyHealthBreakerSettings); ok && cached != nil {
			settings = &cached.settings
		}
	}
	s.openAIAPIKeyHealthBreakerCache.Store(&cachedOpenAIAPIKeyHealthBreakerSettings{
		settings:  *settings,
		expiresAt: time.Now().Add(ttl),
	})
	result := *settings
	if loadErr != nil {
		return nil, fmt.Errorf("get OpenAI API key health breaker settings: %w", loadErr)
	}
	return &result, nil
}

func (s *SettingService) refreshOpenAIAPIKeyHealthBreakerSettingsAsync() {
	if s == nil || s.settingRepo == nil || !s.openAIAPIKeyHealthBreakerRefresh.tryStart(time.Now()) {
		return
	}
	resultCh := s.openAIAPIKeyHealthBreakerSF.DoChan(openAIAPIKeyHealthBreakerSettingsRefreshKey, func() (any, error) {
		ctx, cancel := context.WithTimeout(context.Background(), openAIAPIKeyHealthBreakerSettingsDBTimeout)
		defer cancel()
		return s.loadOpenAIAPIKeyHealthBreakerSettings(ctx)
	})
	go func() {
		result := <-resultCh
		s.openAIAPIKeyHealthBreakerRefresh.finish(result.Err)
	}()
}
