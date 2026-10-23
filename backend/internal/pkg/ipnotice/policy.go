// Package ipnotice adds operator notices to explicitly selected client IPs.
package ipnotice

import (
	"fmt"
	"net/netip"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Settings is an IP-only policy. It deliberately contains no user or key IDs.
type Settings struct {
	Enabled bool     `json:"enabled"`
	IPs     []string `json:"ips"`
	Message string   `json:"message"`
	RPM     int      `json:"rpm"`
}

// DefaultSettings leaves the policy disabled until the operator supplies IPs.
func DefaultSettings() Settings { return Settings{IPs: []string{}, RPM: 100} }

// Normalize validates an exact-address list and returns an independent copy.
func (s Settings) Normalize() (Settings, error) {
	if len(s.IPs) > 256 {
		return Settings{}, fmt.Errorf("at most 256 IP addresses are allowed")
	}
	if s.RPM < 1 || s.RPM > 100000 {
		return Settings{}, fmt.Errorf("RPM must be between 1 and 100000")
	}
	if !utf8.ValidString(s.Message) || utf8.RuneCountInString(s.Message) > 2048 {
		return Settings{}, fmt.Errorf("notice must contain at most 2048 valid characters")
	}
	for _, r := range s.Message {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return Settings{}, fmt.Errorf("notice contains a control character")
		}
	}
	s.Message = strings.TrimSpace(s.Message)
	if strings.Contains(s.Message, noticeFence) {
		return Settings{}, fmt.Errorf("notice contains a reserved separator")
	}
	if s.Enabled && (s.Message == "" || len(s.IPs) == 0) {
		return Settings{}, fmt.Errorf("an enabled policy requires a notice and at least one IP")
	}
	ips := make([]string, 0, len(s.IPs))
	seen := make(map[string]bool, len(s.IPs))
	for _, raw := range s.IPs {
		addr, err := netip.ParseAddr(strings.TrimSpace(raw))
		if err != nil || addr.Zone() != "" || !addr.IsGlobalUnicast() || addr.IsPrivate() {
			return Settings{}, fmt.Errorf("each entry must be a public IPv4 or IPv6 address")
		}
		value := addr.Unmap().String()
		if !seen[value] {
			seen[value] = true
			ips = append(ips, value)
		}
	}
	s.IPs = ips
	return s, nil
}

// Match returns the canonical listed address; forwarded headers are resolved by
// the gateway's existing trusted client-IP policy before this function is called.
func (s Settings) Match(raw string) (string, bool) {
	if !s.Enabled {
		return "", false
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(raw))
	if err != nil {
		return "", false
	}
	canonical := addr.Unmap().String()
	for _, allowed := range s.IPs {
		if allowed == canonical {
			return canonical, true
		}
	}
	return "", false
}
