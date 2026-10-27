// Package iprisk implements shared, trusted-origin IP admission and authentication risk controls.
package iprisk

import (
	"fmt"
	"net/netip"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/language"
)

const SettingKey = "ip_risk_control_v1"

type Rule struct {
	Network   string `json:"network"`
	Note      string `json:"note"`
	Enabled   bool   `json:"enabled"`
	ExpiresAt int64  `json:"expires_at"` // Unix seconds; zero means permanent.
}

type Threshold struct {
	Limit         int `json:"limit"` // Zero disables this counter.
	WindowSeconds int `json:"window_seconds"`
}

type AnonymousAPIConfig struct {
	Enabled        bool `json:"enabled"`
	Threshold      int  `json:"threshold"`
	WindowSeconds  int  `json:"window_seconds"`
	BanSeconds     int  `json:"ban_seconds"`
	MaxTrackedIPs  int  `json:"max_tracked_ips"`
	ResetOnSuccess bool `json:"reset_on_success"`
}

func defaultAnonymousAPIConfig() AnonymousAPIConfig {
	return AnonymousAPIConfig{Threshold: 30, WindowSeconds: 60, BanSeconds: 300, MaxTrackedIPs: 10000}
}

type Config struct {
	AnonymousAPI         AnonymousAPIConfig `json:"anonymous_api"`
	Enabled              bool               `json:"enabled"`
	ObserveOnly          bool               `json:"observe_only"`
	Whitelist            []string           `json:"whitelist"`
	Blacklist            []Rule             `json:"blacklist"`
	AllowedCountries     []string           `json:"allowed_countries"`
	BlockedCountries     []string           `json:"blocked_countries"`
	BlockUnknownCountry  bool               `json:"block_unknown_country"`
	DenyUserAgents       []string           `json:"deny_user_agents"`
	BlockEmptyUA         bool               `json:"block_empty_ua"`
	IPv6Prefix           int                `json:"ipv6_prefix"`
	BanSeconds           int                `json:"ban_seconds"`
	LoginAttempts        Threshold          `json:"login_attempts"`
	LoginFailures        Threshold          `json:"login_failures"`
	RegisterAttempts     Threshold          `json:"register_attempts"`
	RegisterFailures     Threshold          `json:"register_failures"`
	VerificationAttempts Threshold          `json:"verification_attempts"`
	EventRetentionHours  int                `json:"event_retention_hours"`
}

func DefaultConfig() Config {
	return Config{
		AnonymousAPI: defaultAnonymousAPIConfig(),
		ObserveOnly:  true, Whitelist: []string{}, Blacklist: []Rule{}, BlockedCountries: []string{}, AllowedCountries: []string{},
		DenyUserAgents: []string{"python-requests", "python-urllib", "aiohttp", "httpx", "curl/", "wget/"},
		IPv6Prefix:     64, BanSeconds: 3600, LoginAttempts: Threshold{60, 60}, LoginFailures: Threshold{10, 900},
		RegisterAttempts: Threshold{10, 600}, RegisterFailures: Threshold{5, 600},
		VerificationAttempts: Threshold{10, 600}, EventRetentionHours: 168,
	}
}

// NormalizeConfig rejects invalid rules rather than silently losing protection.
func NormalizeConfig(c Config) (Config, error) {
	// Old saved policies have no anonymous_api object; preserve disabled defaults.
	if c.AnonymousAPI == (AnonymousAPIConfig{}) {
		c.AnonymousAPI = defaultAnonymousAPIConfig()
	}
	a := c.AnonymousAPI
	if a.Threshold < 2 || a.Threshold > 1000 || a.WindowSeconds < 1 || a.WindowSeconds > 3600 || a.BanSeconds < 1 || a.BanSeconds > 86400 || a.MaxTrackedIPs < 100 || a.MaxTrackedIPs > 50000 {
		return c, fmt.Errorf("anonymous API threshold must be 2–1000, window 1–3600 seconds, ban 1–86400 seconds, capacity 100–50000")
	}

	if c.IPv6Prefix < 64 || c.IPv6Prefix > 128 || c.BanSeconds < 60 || c.BanSeconds > 604800 || c.EventRetentionHours < 1 || c.EventRetentionHours > 720 {
		return c, fmt.Errorf("IPv6 prefix must be 64–128, ban duration 60–604800 seconds, retention 1–720 hours")
	}
	for _, t := range []Threshold{c.LoginAttempts, c.LoginFailures, c.RegisterAttempts, c.RegisterFailures, c.VerificationAttempts} {
		if t.Limit < 0 || t.Limit > 100000 || t.WindowSeconds < 1 || t.WindowSeconds > 86400 {
			return c, fmt.Errorf("counter limit must be 0–100000 and window 1–86400 seconds")
		}
	}
	if len(c.Whitelist) > 2048 || len(c.Blacklist) > 2048 || len(c.BlockedCountries) > 250 || len(c.AllowedCountries) > 250 || len(c.DenyUserAgents) > 64 {
		return c, fmt.Errorf("too many IP, country or UA rules")
	}
	c.Whitelist = append([]string{}, c.Whitelist...)
	c.Blacklist = append([]Rule{}, c.Blacklist...)
	c.BlockedCountries = append([]string{}, c.BlockedCountries...)
	c.AllowedCountries = append([]string{}, c.AllowedCountries...)
	c.DenyUserAgents = append([]string{}, c.DenyUserAgents...)
	for i, raw := range c.Whitelist {
		network, err := ParseNetwork(raw)
		if err != nil {
			return c, err
		}
		c.Whitelist[i] = network.String()
	}
	for i, r := range c.Blacklist {
		network, err := ParseNetwork(r.Network)
		if err != nil {
			return c, err
		}
		if utf8.RuneCountInString(r.Note) > 256 || r.ExpiresAt < 0 {
			return c, fmt.Errorf("invalid blacklist note or expiration")
		}
		c.Blacklist[i].Network = network.String()
		c.Blacklist[i].Note = cleanText(r.Note, 1024)
	}
	for _, countries := range [][]string{c.AllowedCountries, c.BlockedCountries} {
		for i, raw := range countries {
			code := strings.ToUpper(strings.TrimSpace(raw))
			region, err := language.ParseRegion(code)
			if len(code) != 2 || code == "ZZ" || err != nil || !region.IsCountry() {
				return c, fmt.Errorf("invalid ISO country code: %s", cleanText(raw, 16))
			}
			countries[i] = code
		}
	}
	for _, allowed := range c.AllowedCountries {
		for _, blocked := range c.BlockedCountries {
			if allowed == blocked {
				return c, fmt.Errorf("country cannot be both allowed and blocked: %s", allowed)
			}
		}
	}
	for i, raw := range c.DenyUserAgents {
		value := strings.ToLower(strings.TrimSpace(raw))
		if len(value) < 2 || len(value) > 128 || cleanText(value, 128) != value {
			return c, fmt.Errorf("UA patterns must be 2–128 bytes without control characters")
		}
		c.DenyUserAgents[i] = value
	}
	return c, nil
}

func ParseNetwork(raw string) (netip.Prefix, error) {
	raw = strings.TrimSpace(raw)
	if addr, err := netip.ParseAddr(raw); err == nil && addr.Zone() == "" {
		addr = addr.Unmap()
		return netip.PrefixFrom(addr, addr.BitLen()), nil
	}
	prefix, err := netip.ParsePrefix(raw)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("invalid IP/CIDR: %s", cleanText(raw, 80))
	}
	if prefix.Addr().Is4In6() {
		if prefix.Bits() < 96 {
			return netip.Prefix{}, fmt.Errorf("mapped IPv4 CIDR prefix must be at least 96")
		}
		prefix = netip.PrefixFrom(prefix.Addr().Unmap(), prefix.Bits()-96)
	}
	return prefix.Masked(), nil
}

type policy struct {
	config    Config
	whitelist []netip.Prefix
	blacklist []netip.Prefix
}

func compile(c Config) *policy {
	p := &policy{config: c}
	for _, raw := range c.Whitelist {
		network, _ := ParseNetwork(raw)
		p.whitelist = append(p.whitelist, network)
	}
	for _, rule := range c.Blacklist {
		network, _ := ParseNetwork(rule.Network)
		p.blacklist = append(p.blacklist, network)
	}
	return p
}

func (p *policy) allowed(ip netip.Addr) bool {
	for _, network := range p.whitelist {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func (p *policy) denied(ip netip.Addr, now time.Time) bool {
	for i, network := range p.blacklist {
		rule := p.config.Blacklist[i]
		if rule.Enabled && (rule.ExpiresAt == 0 || rule.ExpiresAt > now.Unix()) && network.Contains(ip) {
			return true
		}
	}
	return false
}

func cleanText(raw string, maxBytes int) string {
	raw = strings.ToValidUTF8(raw, "")
	raw = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, raw)
	if len(raw) > maxBytes {
		raw = raw[:maxBytes]
		for !utf8.ValidString(raw) {
			raw = raw[:len(raw)-1]
		}
	}
	return raw
}
