package service

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const codexQuotaOverdraftRuntimeCacheTTL = 5 * time.Second

// CodexQuotaOverdraftRuntimeSettings is the effective, process-local view of
// the two independent quota-overdraft modes. The deployment values provide a
// legacy bootstrap for missing keys; the master value is also an emergency
// upper bound, while the business-injection value remains an opt-in fallback.
type CodexQuotaOverdraftRuntimeSettings struct {
	Enabled                  bool
	BusinessInjectionEnabled bool
}

type cachedCodexQuotaOverdraftRuntime struct {
	settings  CodexQuotaOverdraftRuntimeSettings
	expiresAt time.Time
}

// readCodexQuotaOverdraftSettings keeps the optional runtime gate defensive
// around lightweight repository adapters used by tests and embedded callers.
// A partially implemented adapter can otherwise promote a nil embedded
// SettingRepository method and panic during gateway construction. Treat that
// the same as a failed settings read and let the caller fail closed.
func readCodexQuotaOverdraftSettings(ctx context.Context, repo SettingRepository) (values map[string]string, err error) {
	if repo == nil {
		return nil, fmt.Errorf("settings repository is nil")
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			values = nil
			err = fmt.Errorf("read codex quota overdraft settings: %v", recovered)
		}
	}()
	return repo.GetMultiple(ctx, []string{
		SettingKeyCodexQuotaOverdraftEnabled,
		SettingKeyCodexQuotaOverdraftBusinessInjectionEnabled,
	})
}

// GetCodexQuotaOverdraftRuntime returns the effective settings using a short
// stale-while-revalidate cache.  Missing keys fall back to the deployment
// defaults, which preserves existing installations that predate the DB keys.
func (s *SettingService) GetCodexQuotaOverdraftRuntime(ctx context.Context) CodexQuotaOverdraftRuntimeSettings {
	if ctx == nil {
		ctx = context.Background()
	}
	fallback := codexQuotaOverdraftRuntimeFromConfig(s)
	if s == nil || s.settingRepo == nil {
		return fallback
	}
	now := time.Now()
	if cached, _ := s.codexQuotaOverdraftRuntimeCache.Load().(*cachedCodexQuotaOverdraftRuntime); cached != nil {
		if cached.expiresAt.After(now) {
			return cached.settings
		}
		// Do not make the gateway wait behind a settings refresh.  A bounded
		// background read will update this process and the next request.
		if s.codexQuotaOverdraftRuntimeRefreshing.CompareAndSwap(false, true) {
			go func() {
				defer s.codexQuotaOverdraftRuntimeRefreshing.Store(false)
				s.refreshCodexQuotaOverdraftRuntime(context.WithoutCancel(ctx), fallback)
			}()
		}
		return cached.settings
	}

	// First use performs one bounded read so a freshly started replica does not
	// accidentally use stale config for its first request.
	return s.refreshCodexQuotaOverdraftRuntime(ctx, fallback)
}

func codexQuotaOverdraftRuntimeFromConfig(s *SettingService) CodexQuotaOverdraftRuntimeSettings {
	if s == nil || s.cfg == nil {
		return CodexQuotaOverdraftRuntimeSettings{}
	}
	return CodexQuotaOverdraftRuntimeSettings{
		Enabled:                  s.cfg.Gateway.CodexQuotaOverdraftEnabled,
		BusinessInjectionEnabled: s.cfg.Gateway.CodexQuotaOverdraftBusinessInjectionEnabled,
	}
}

func (s *SettingService) refreshCodexQuotaOverdraftRuntime(ctx context.Context, fallback CodexQuotaOverdraftRuntimeSettings) CodexQuotaOverdraftRuntimeSettings {
	if s == nil || s.settingRepo == nil {
		return fallback
	}
	value, err, _ := s.codexQuotaOverdraftRuntimeSF.Do("codex_quota_overdraft_runtime", func() (any, error) {
		readCtx := ctx
		if readCtx == nil {
			readCtx = context.Background()
		}
		readCtx, cancel := context.WithTimeout(context.WithoutCancel(readCtx), time.Second)
		defer cancel()
		values, err := readCodexQuotaOverdraftSettings(readCtx, s.settingRepo)
		if err != nil {
			return fallback, err
		}
		effective := fallback
		if raw, ok := values[SettingKeyCodexQuotaOverdraftEnabled]; ok && strings.TrimSpace(raw) != "" {
			effective.Enabled = strings.EqualFold(strings.TrimSpace(raw), "true")
		}
		if raw, ok := values[SettingKeyCodexQuotaOverdraftBusinessInjectionEnabled]; ok && strings.TrimSpace(raw) != "" {
			effective.BusinessInjectionEnabled = strings.EqualFold(strings.TrimSpace(raw), "true")
		}
		return effective, nil
	})
	if err != nil {
		if cached, _ := s.codexQuotaOverdraftRuntimeCache.Load().(*cachedCodexQuotaOverdraftRuntime); cached != nil {
			return cached.settings
		}
		// A database read failure must not resurrect a setting that an admin
		// explicitly disabled. Legacy config values are only a bootstrap for a
		// successful read with missing keys; on an unavailable database, fail
		// closed until the next refresh can establish the durable value.
		return CodexQuotaOverdraftRuntimeSettings{}
	}
	effective, ok := value.(CodexQuotaOverdraftRuntimeSettings)
	if !ok {
		return CodexQuotaOverdraftRuntimeSettings{}
	}
	// The deployment master switch is an emergency upper bound. A database
	// preference may enable/disable the detector normally, but an operator can
	// still turn the whole mode off without editing every replica's settings.
	if s.cfg != nil && !s.cfg.Gateway.CodexQuotaOverdraftEnabled {
		effective.Enabled = false
	}
	if !effective.Enabled {
		effective.BusinessInjectionEnabled = false
	}
	s.codexQuotaOverdraftRuntimeCache.Store(&cachedCodexQuotaOverdraftRuntime{
		settings:  effective,
		expiresAt: time.Now().Add(codexQuotaOverdraftRuntimeCacheTTL),
	})
	SetCodexQuotaOverdraftEnabled(effective.Enabled)
	SetCodexQuotaOverdraftBusinessInjectionEnabled(effective.BusinessInjectionEnabled)
	return effective
}

// publishCodexQuotaOverdraftRuntime is called after a successful settings
// write, making the change effective immediately on the writing replica.
func (s *SettingService) publishCodexQuotaOverdraftRuntime(settings CodexQuotaOverdraftRuntimeSettings) {
	if s == nil {
		return
	}
	if s.cfg != nil && !s.cfg.Gateway.CodexQuotaOverdraftEnabled {
		settings.Enabled = false
	}
	if !settings.Enabled {
		settings.BusinessInjectionEnabled = false
	}
	s.codexQuotaOverdraftRuntimeCache.Store(&cachedCodexQuotaOverdraftRuntime{
		settings:  settings,
		expiresAt: time.Now().Add(codexQuotaOverdraftRuntimeCacheTTL),
	})
	SetCodexQuotaOverdraftEnabled(settings.Enabled)
	SetCodexQuotaOverdraftBusinessInjectionEnabled(settings.BusinessInjectionEnabled)
}
