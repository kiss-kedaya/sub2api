package service

import (
	"context"
	"strings"
	"time"
)

const codexQuotaOverdraftRuntimeCacheTTL = 5 * time.Second

// CodexQuotaOverdraftRuntimeSettings is the effective, process-local view of
// the two independent quota-overdraft modes. The deployment values are used
// only as a legacy bootstrap fallback when an older database has not yet
// materialized the corresponding admin key; once a key exists, the admin
// setting is authoritative.
type CodexQuotaOverdraftRuntimeSettings struct {
	Enabled                  bool
	BusinessInjectionEnabled bool
}

type cachedCodexQuotaOverdraftRuntime struct {
	settings  CodexQuotaOverdraftRuntimeSettings
	expiresAt time.Time
}

// GetCodexQuotaOverdraftRuntime returns the effective settings using a short
// stale-while-revalidate cache.  Missing keys fall back to the deployment
// defaults, which preserves existing installations that predate the DB keys.
func (s *SettingService) GetCodexQuotaOverdraftRuntime(ctx context.Context) CodexQuotaOverdraftRuntimeSettings {
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
		values, err := s.settingRepo.GetMultiple(readCtx, []string{
			SettingKeyCodexQuotaOverdraftEnabled,
			SettingKeyCodexQuotaOverdraftBusinessInjectionEnabled,
		})
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
		return fallback
	}
	effective, ok := value.(CodexQuotaOverdraftRuntimeSettings)
	if !ok {
		return fallback
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
