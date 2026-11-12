package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ipnotice"
)

// SettingKeyGatewayIPNotice stores the administrator's exact-IP notice policy.
const SettingKeyGatewayIPNotice = "gateway_ip_notice"

type cachedGatewayIPNotice struct {
	settings  ipnotice.Settings
	expiresAt time.Time
}

// GetGatewayIPNotice reads the editable policy on the administration path.
func (s *SettingService) GetGatewayIPNotice(ctx context.Context) (ipnotice.Settings, error) {
	return s.readGatewayIPNotice(ctx)
}

// readGatewayIPNotice is the lock-free read used by both the admin path and
// AppendGatewayIPNoticeAutoEntry (which already holds gatewayIPNoticeMu).
func (s *SettingService) readGatewayIPNotice(ctx context.Context) (ipnotice.Settings, error) {
	if s == nil || s.settingRepo == nil {
		return ipnotice.DefaultSettings(), nil
	}
	read := s.settingRepo.GetValue
	// Bypass the repository's 30-second cache on this bounded background/admin
	// path so another replica observes an IP policy edit within five seconds.
	if fresh, ok := s.settingRepo.(interface {
		GetValueUncached(context.Context, string) (string, error)
	}); ok {
		read = fresh.GetValueUncached
	}
	raw, err := read(ctx, SettingKeyGatewayIPNotice)
	if errors.Is(err, ErrSettingNotFound) || (err == nil && raw == "") {
		return ipnotice.DefaultSettings(), nil
	}
	if err != nil {
		return ipnotice.Settings{}, err
	}
	var settings ipnotice.Settings
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		return ipnotice.Settings{}, fmt.Errorf("decode gateway IP notice: %w", err)
	}
	return settings.Normalize()
}

// SetGatewayIPNotice changes only the policy setting, never users or API keys.
func (s *SettingService) SetGatewayIPNotice(ctx context.Context, settings ipnotice.Settings) error {
	normalized, err := settings.Normalize()
	if err != nil {
		return err
	}
	data, err := json.Marshal(normalized)
	if err != nil {
		return err
	}
	s.gatewayIPNoticeMu.Lock()
	defer s.gatewayIPNoticeMu.Unlock()
	if err := s.settingRepo.Set(ctx, SettingKeyGatewayIPNotice, string(data)); err != nil {
		return err
	}
	s.gatewayIPNoticeCache.Store(&cachedGatewayIPNotice{settings: normalized, expiresAt: time.Now().Add(5 * time.Second)})
	return nil
}

// AppendGatewayIPNoticeAutoEntry appends one gateway-detected IP to the
// "指定 IP 提示与限流" list and returns whether a new entry was written.
//
// It is idempotent: an address that is already listed manually or
// automatically is left untouched, so repeated detections (and multiple
// replicas) converge on a single entry. Only the policy setting is written —
// no user, API key or account is modified anywhere.
func (s *SettingService) AppendGatewayIPNoticeAutoEntry(ctx context.Context, entry ipnotice.AutoEntry) (bool, error) {
	if s == nil || s.settingRepo == nil {
		return false, nil
	}
	s.gatewayIPNoticeMu.Lock()
	defer s.gatewayIPNoticeMu.Unlock()
	current, err := s.readGatewayIPNotice(ctx)
	if err != nil {
		return false, err
	}
	if current.HasListedIP(entry.IP) {
		return false, nil
	}
	if len(current.IPs)+len(current.AutoEntries) >= ipnotice.MaxListedIPs {
		return false, fmt.Errorf("gateway IP notice list is full (%d entries)", ipnotice.MaxListedIPs)
	}
	current.AutoEntries = append(current.AutoEntries, entry)
	normalized, err := current.Normalize()
	if err != nil {
		return false, err
	}
	data, err := json.Marshal(normalized)
	if err != nil {
		return false, err
	}
	if err := s.settingRepo.Set(ctx, SettingKeyGatewayIPNotice, string(data)); err != nil {
		return false, err
	}
	s.gatewayIPNoticeCache.Store(&cachedGatewayIPNotice{settings: normalized, expiresAt: time.Now().Add(5 * time.Second)})
	return true, nil
}

// RefreshGatewayIPNotice primes the snapshot during router construction. A
// failed refresh preserves the last known policy and backs off for five seconds.
func (s *SettingService) RefreshGatewayIPNotice(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.gatewayIPNoticeMu.Lock()
	defer s.gatewayIPNoticeMu.Unlock()
	settings, err := s.GetGatewayIPNotice(ctx)
	if err != nil {
		settings = ipnotice.DefaultSettings()
		if previous := s.gatewayIPNoticeCache.Load(); previous != nil {
			settings = previous.settings
		}
	}
	s.gatewayIPNoticeCache.Store(&cachedGatewayIPNotice{settings: settings, expiresAt: time.Now().Add(5 * time.Second)})
	return err
}

// GatewayIPNoticeSnapshot performs no database I/O on the request goroutine.
// Multi-instance changes are picked up through bounded background refreshes.
func (s *SettingService) GatewayIPNoticeSnapshot() ipnotice.Settings {
	if s == nil {
		return ipnotice.DefaultSettings()
	}
	cached := s.gatewayIPNoticeCache.Load()
	if (cached == nil || time.Now().After(cached.expiresAt)) && s.gatewayIPNoticeRefreshing.CompareAndSwap(false, true) {
		go func() {
			defer s.gatewayIPNoticeRefreshing.Store(false)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := s.RefreshGatewayIPNotice(ctx); err != nil {
				slog.Warn("gateway IP notice refresh failed", "error", err)
			}
		}()
	}
	if cached != nil {
		return cached.settings
	}
	return ipnotice.DefaultSettings()
}
