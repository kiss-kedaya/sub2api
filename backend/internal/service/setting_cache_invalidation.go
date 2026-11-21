package service

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// SettingCacheInvalidationBus carries the cross-instance "settings have
// changed" signal. The infrastructure layer implements it with Redis pub/sub so
// this package keeps no direct Redis dependency.
//
// A single settings write used to refresh only the writer process: every other
// replica kept serving its own cached public settings (and, worse, the rendered
// index.html embedding window.__APP_CONFIG__) until an unknown-length TTL
// expired. Two replicas then injected different flags for the same site, which
// is how /deposit ended up guarded by stale crypto_enabled=false.
type SettingCacheInvalidationBus interface {
	// PublishSettingsChanged notifies every subscriber, including the caller.
	PublishSettingsChanged(ctx context.Context) error
	// SubscribeSettingsChanged blocks, invoking handler for each notification,
	// until ctx is cancelled.
	SubscribeSettingsChanged(ctx context.Context, handler func()) error
}

// SettingsInjectionVersionProvider exposes the monotonic counter bumped on
// every settings change observed by this process. The embedded frontend server
// keys its rendered-HTML cache on it so a broadcast (or the fallback TTL) can
// never keep serving an old window.__APP_CONFIG__.
type SettingsInjectionVersionProvider interface {
	SettingsInjectionVersion() uint64
}

// SettingsInjectionCacheTTLProvider exposes the fallback TTL for the rendered
// index.html cache. It bounds staleness even when a broadcast is lost.
type SettingsInjectionCacheTTLProvider interface {
	SettingsInjectionCacheTTL() time.Duration
}

const (
	// defaultSettingsInjectionCacheTTL is the fallback lifetime of the rendered
	// index.html. The value is deliberately short: it caps how long a replica
	// can inject flags another replica already changed when a pub/sub message
	// never arrives.
	defaultSettingsInjectionCacheTTL = 45 * time.Second
	minSettingsInjectionCacheTTL     = 5 * time.Second
	maxSettingsInjectionCacheTTL     = 300 * time.Second

	// settingsInvalidationPublishTimeout bounds the write-path broadcast. A
	// settings write must not block on a degraded Redis, so the publish gets a
	// short budget and a failure is logged, never propagated.
	settingsInvalidationPublishTimeout = 750 * time.Millisecond

	settingsInvalidationRetryMin = time.Second
	settingsInvalidationRetryMax = 30 * time.Second
)

// SetSettingsCacheInvalidationBus connects the cross-instance transport. Safe to
// call before or after StartSettingsCacheInvalidationSubscriber.
func (s *SettingService) SetSettingsCacheInvalidationBus(bus SettingCacheInvalidationBus) {
	if s == nil {
		return
	}
	s.settingsInvalidationBusMu.Lock()
	s.settingsInvalidationBus = bus
	s.settingsInvalidationBusMu.Unlock()
}

func (s *SettingService) settingsInvalidationTransport() SettingCacheInvalidationBus {
	if s == nil {
		return nil
	}
	s.settingsInvalidationBusMu.RLock()
	defer s.settingsInvalidationBusMu.RUnlock()
	return s.settingsInvalidationBus
}

// SettingsInjectionVersion returns the number of settings-change notifications
// this process has observed. It is monotonic per process and never persisted.
func (s *SettingService) SettingsInjectionVersion() uint64 {
	if s == nil {
		return 0
	}
	return s.settingsVersion.Load()
}

// SettingsInjectionCacheTTL returns the configured fallback TTL, clamped to a
// sane range. Unset/invalid configuration yields the 45s default.
func (s *SettingService) SettingsInjectionCacheTTL() time.Duration {
	if s == nil || s.cfg == nil {
		return defaultSettingsInjectionCacheTTL
	}
	return normalizeSettingsInjectionCacheTTL(time.Duration(s.cfg.Server.SettingsInjectionCacheTTLSeconds) * time.Second)
}

func normalizeSettingsInjectionCacheTTL(ttl time.Duration) time.Duration {
	switch {
	case ttl <= 0:
		return defaultSettingsInjectionCacheTTL
	case ttl < minSettingsInjectionCacheTTL:
		return minSettingsInjectionCacheTTL
	case ttl > maxSettingsInjectionCacheTTL:
		return maxSettingsInjectionCacheTTL
	default:
		return ttl
	}
}

// notifySettingsChanged bumps the local injection version and runs the
// registered onUpdate callback (HTML/logo cache invalidation). Callers are the
// local write path and the cross-instance subscriber.
func (s *SettingService) notifySettingsChanged() {
	if s == nil {
		return
	}
	s.settingsVersion.Add(1)
	if s.onUpdate != nil {
		s.onUpdate()
	}
}

// broadcastSettingsChanged publishes the notification after a write has landed.
// A publishing failure is logged and otherwise ignored: local caches are already
// refreshed and peers still converge through SettingsInjectionCacheTTL.
func (s *SettingService) broadcastSettingsChanged(ctx context.Context) {
	bus := s.settingsInvalidationTransport()
	if bus == nil {
		return
	}
	publishCtx := context.Background()
	if ctx != nil {
		publishCtx = context.WithoutCancel(ctx)
	}
	publishCtx, cancel := context.WithTimeout(publishCtx, settingsInvalidationPublishTimeout)
	defer cancel()
	if err := bus.PublishSettingsChanged(publishCtx); err != nil {
		slog.Warn("settings changed broadcast failed; peers will converge on the injection cache TTL",
			"error", err)
	}
}

// StartSettingsCacheInvalidationSubscriber keeps this replica in step with
// settings written elsewhere. It is idempotent and retries with backoff.
func (s *SettingService) StartSettingsCacheInvalidationSubscriber(ctx context.Context) {
	bus := s.settingsInvalidationTransport()
	if bus == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.settingsInvalidationStart.Do(func() {
		subscriberCtx, cancel := context.WithCancel(ctx)
		s.settingsInvalidationCancel = cancel
		s.settingsInvalidationWG.Add(1)
		go func() {
			defer s.settingsInvalidationWG.Done()
			backoff := settingsInvalidationRetryMin
			for {
				err := bus.SubscribeSettingsChanged(subscriberCtx, func() {
					s.handleRemoteSettingsChanged()
				})
				if subscriberCtx.Err() != nil {
					return
				}
				if err == nil {
					err = errors.New("settings cache invalidation subscription closed")
				}
				slog.Warn("settings cache invalidation subscriber stopped; retrying",
					"error", err, "retry_in", backoff)
				timer := time.NewTimer(backoff)
				select {
				case <-subscriberCtx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
				if backoff < settingsInvalidationRetryMax {
					backoff *= 2
					if backoff > settingsInvalidationRetryMax {
						backoff = settingsInvalidationRetryMax
					}
				}
			}
		}()
	})
}

// handleRemoteSettingsChanged applies a notification produced by another
// replica. This process never wrote the value, so the only correct reaction is
// to drop what it cached: the version bump makes the embedded frontend server
// re-render index.html from the database on the next request, and onUpdate
// clears the rendered HTML/logo caches immediately.
func (s *SettingService) handleRemoteSettingsChanged() {
	if s == nil {
		return
	}
	s.notifySettingsChanged()
	s.notifyChannelMonitorRuntimeListeners()
}

// StopSettingsCacheInvalidationSubscriber stops the subscriber goroutine.
func (s *SettingService) StopSettingsCacheInvalidationSubscriber() {
	if s == nil {
		return
	}
	s.settingsInvalidationStop.Do(func() {
		if s.settingsInvalidationCancel != nil {
			s.settingsInvalidationCancel()
		}
		s.settingsInvalidationWG.Wait()
	})
}
