package service

import (
	"context"
	"encoding/json"
	"errors"
	"net"

	"github.com/Wei-Shaw/sub2api/internal/iprisk"
)

type ipRiskStore struct{ settings *SettingService }

func (s ipRiskStore) Load(ctx context.Context) (iprisk.Config, error) {
	c := iprisk.DefaultConfig()
	raw, err := s.settings.settingRepo.GetValue(ctx, iprisk.SettingKey)
	if errors.Is(err, ErrSettingNotFound) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	err = json.Unmarshal([]byte(raw), &c)
	return c, err
}

func (s ipRiskStore) Save(ctx context.Context, c iprisk.Config) error {
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return s.settings.settingRepo.Set(ctx, iprisk.SettingKey, string(raw))
}

func (s *SettingService) IPRiskStore() iprisk.Store { return ipRiskStore{settings: s} }

func (s *SettingService) IPRiskTrustedProxiesConfigured() bool {
	if s.cfg == nil {
		return false
	}
	proxies := s.cfg.Server.TrustedProxies
	if len(proxies) == 0 {
		return false
	}
	for _, proxy := range proxies {
		// Match Gin's strict parsing, including rejection of whitespace. Risk
		// rules accept normalized inputs, but proxy configuration does not.
		if net.ParseIP(proxy) != nil {
			continue
		}
		if _, _, err := net.ParseCIDR(proxy); err != nil {
			return false
		}
	}
	return true
}
