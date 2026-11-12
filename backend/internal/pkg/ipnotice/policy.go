// Package ipnotice adds operator notices to explicitly selected client IPs.
package ipnotice

import (
	"fmt"
	"net/netip"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// MaxListedIPs bounds the effective list so a runaway detector or a bulk
	// paste cannot grow the request-path snapshot without limit.
	MaxListedIPs = 256

	// DefaultSharingWindowMinutes is the sliding window used to decide whether
	// one source IP is shared by several user accounts. Ten minutes is short
	// enough that ordinary NAT churn does not accumulate, long enough to catch
	// a key-scraping client that rotates accounts.
	DefaultSharingWindowMinutes = 10
	// DefaultSharingThreshold is the number of DISTINCT USER accounts (not API
	// keys) that must appear behind one IP inside the window before the IP is
	// appended to the list. The operator asked for "3 or more".
	DefaultSharingThreshold = 3
	// DefaultRPM is the per-IP request budget applied to listed addresses.
	DefaultRPM = 100

	// AutoEntrySourceAuto marks an entry that the gateway appended by itself.
	// Manual entries live in Settings.IPs and never carry this source.
	AutoEntrySourceAuto = "auto"

	maxAutoEntryKeyPrefixes = 8
	maxAutoEntryPrefixLen   = 64
)

// AutoEntry is one gateway-detected IP plus the evidence that justified it.
// Keeping it separate from Settings.IPs is what makes "auto" and "manual"
// distinguishable in the admin UI and lets an operator remove either one
// without rewriting the other.
type AutoEntry struct {
	IP string `json:"ip"`
	// Source is always AutoEntrySourceAuto; Normalize rewrites it so a client
	// cannot label a hand-typed entry as automatic.
	Source string `json:"source"`
	// FirstSeenAt is the moment the distinct-user threshold was crossed.
	FirstSeenAt time.Time `json:"first_seen_at"`
	// DistinctUsers is the number of distinct user accounts observed in window.
	DistinctUsers int `json:"distinct_users"`
	// WindowMinutes is the window that was in force when the entry was added.
	WindowMinutes int `json:"window_minutes"`
	// KeyPrefixes holds masked API-key prefixes (never the full key).
	KeyPrefixes []string `json:"key_prefixes"`
}

// Settings is an IP-only policy. It deliberately contains no user or key IDs.
type Settings struct {
	Enabled bool     `json:"enabled"`
	IPs     []string `json:"ips"`
	Message string   `json:"message"`
	RPM     int      `json:"rpm"`

	// AutoAddEnabled turns the distinct-user detection on. Nil means "not
	// configured yet" and normalizes to true so a fresh deployment gets the
	// protection without an extra switch flip.
	AutoAddEnabled *bool `json:"auto_add_enabled,omitempty"`
	// SharingWindowMinutes and SharingThreshold are operator-tunable; zero
	// means "use the default" so older stored policies stay valid.
	SharingWindowMinutes int `json:"sharing_window_minutes,omitempty"`
	SharingThreshold     int `json:"sharing_threshold,omitempty"`
	// AutoEntries are appended by the gateway, never by the admin form.
	AutoEntries []AutoEntry `json:"auto_entries"`
}

// DefaultSettings leaves the policy disabled until the operator supplies IPs.
func DefaultSettings() Settings {
	enabled := true
	return Settings{IPs: []string{}, RPM: DefaultRPM, AutoAddEnabled: &enabled,
		SharingWindowMinutes: DefaultSharingWindowMinutes, SharingThreshold: DefaultSharingThreshold}
}

// AutoAdd reports whether distinct-user detection is on.
func (s Settings) AutoAdd() bool { return s.AutoAddEnabled == nil || *s.AutoAddEnabled }

// SharingWindow returns the configured window, falling back to the default.
func (s Settings) SharingWindow() time.Duration {
	minutes := s.SharingWindowMinutes
	if minutes <= 0 {
		minutes = DefaultSharingWindowMinutes
	}
	return time.Duration(minutes) * time.Minute
}

// SharingMinUsers returns the configured threshold, falling back to the default.
func (s Settings) SharingMinUsers() int {
	if s.SharingThreshold <= 0 {
		return DefaultSharingThreshold
	}
	return s.SharingThreshold
}

// ListedIPs returns the effective exact-address list: manual entries first,
// then gateway-appended ones. Callers that only need membership should use
// HasListedIP.
func (s Settings) ListedIPs() []string {
	out := make([]string, 0, len(s.IPs)+len(s.AutoEntries))
	out = append(out, s.IPs...)
	for _, entry := range s.AutoEntries {
		out = append(out, entry.IP)
	}
	return out
}

// HasListedIP reports whether raw is already on the list, independent of the
// Enabled switch. The detector uses it to stay idempotent.
func (s Settings) HasListedIP(raw string) bool {
	addr, err := netip.ParseAddr(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	canonical := addr.Unmap().String()
	for _, allowed := range s.IPs {
		if allowed == canonical {
			return true
		}
	}
	for _, entry := range s.AutoEntries {
		if entry.IP == canonical {
			return true
		}
	}
	return false
}

// Normalize validates an exact-address list and returns an independent copy.
func (s Settings) Normalize() (Settings, error) {
	if len(s.IPs) > MaxListedIPs {
		return Settings{}, fmt.Errorf("at most %d IP addresses are allowed", MaxListedIPs)
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

	if s.AutoAddEnabled == nil {
		enabled := true
		s.AutoAddEnabled = &enabled
	}
	if s.SharingWindowMinutes == 0 {
		s.SharingWindowMinutes = DefaultSharingWindowMinutes
	}
	if s.SharingThreshold == 0 {
		s.SharingThreshold = DefaultSharingThreshold
	}
	if s.SharingWindowMinutes < 1 || s.SharingWindowMinutes > 1440 {
		return Settings{}, fmt.Errorf("sharing window must be between 1 and 1440 minutes")
	}
	if s.SharingThreshold < 2 || s.SharingThreshold > 100 {
		return Settings{}, fmt.Errorf("sharing threshold must be between 2 and 100 users")
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

	auto := make([]AutoEntry, 0, len(s.AutoEntries))
	seenAuto := make(map[string]bool, len(s.AutoEntries))
	for _, entry := range s.AutoEntries {
		addr, err := netip.ParseAddr(strings.TrimSpace(entry.IP))
		if err != nil || addr.Zone() != "" || !addr.IsGlobalUnicast() || addr.IsPrivate() {
			return Settings{}, fmt.Errorf("each automatic entry must be a public IPv4 or IPv6 address")
		}
		value := addr.Unmap().String()
		// A manual entry for the same address wins: one address must never be
		// stored twice with two different provenances.
		if seen[value] || seenAuto[value] {
			continue
		}
		seenAuto[value] = true
		entry.IP = value
		entry.Source = AutoEntrySourceAuto
		entry.KeyPrefixes = normalizeKeyPrefixes(entry.KeyPrefixes)
		auto = append(auto, entry)
	}
	s.AutoEntries = auto

	if s.Enabled && (s.Message == "" || len(s.IPs)+len(s.AutoEntries) == 0) {
		return Settings{}, fmt.Errorf("an enabled policy requires a notice and at least one IP")
	}
	if total := len(s.IPs) + len(s.AutoEntries); total > MaxListedIPs {
		return Settings{}, fmt.Errorf("at most %d IP addresses are allowed", MaxListedIPs)
	}
	return s, nil
}

func normalizeKeyPrefixes(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		value = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return -1
			}
			return r
		}, value)
		if len(value) > maxAutoEntryPrefixLen {
			value = value[:maxAutoEntryPrefixLen]
		}
		seen[value] = true
		out = append(out, value)
		if len(out) == maxAutoEntryKeyPrefixes {
			break
		}
	}
	return out
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
	if s.HasListedIP(canonical) {
		return canonical, true
	}
	return "", false
}
