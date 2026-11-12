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
	// one source IP is shared by several user accounts. Five minutes is short
	// enough that ordinary NAT churn does not accumulate, long enough to catch
	// a key-scraping client that rotates accounts within one burst.
	DefaultSharingWindowMinutes = 5
	// DefaultSharingThreshold is the number of DISTINCT USER accounts (not API
	// keys) that must appear behind one IP inside the window before the IP is
	// appended to the list. The operator asked for "3 or more"; production sets
	// 4 through the settings policy.
	DefaultSharingThreshold = 3
	// DefaultKeyMinAgeDays is the minimum age of an API key before it may
	// contribute to sharing detection. A key created minutes ago cannot have
	// been shared yet, and counting it would let one freshly issued credential
	// drag a whole IP into the list.
	DefaultKeyMinAgeDays = 30
	// DefaultRPM is the per-IP request budget applied to listed addresses.
	DefaultRPM = 100
	// DefaultTotalKeysTTLHours is how long the per-IP "keys seen" set lives.
	// Distinct keys accumulate over this period, independently of the short
	// decision window: the five-minute window decides WHETHER an IP looks
	// shared, this period answers "how many keys did it call with overall".
	// 24 hours is long enough to catch a slow rotation across one day and short
	// enough that the Redis set stays bounded and eventually forgets.
	DefaultTotalKeysTTLHours = 24

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

	// FirstSeenUsers is the distinct-user count at the moment the threshold was
	// crossed. It is written once and never changes afterwards: it is the answer
	// to "how many accounts were already there when we noticed".
	FirstSeenUsers int `json:"first_seen_users"`
	// PeakUsers is the largest distinct-user count seen in this entry's current
	// burst. It grows while the same window keeps producing hits and restarts
	// from the new burst once a full window passes without one.
	PeakUsers int `json:"peak_users"`
	// TotalKeys is how many DISTINCT API keys this IP has called with inside the
	// total-keys period (24h by default). It is not limited to the decision
	// window and it never shrinks.
	TotalKeys int `json:"total_keys"`
	// LastSeenAt refreshes on every hit that updates the entry. Zero on a legacy
	// entry; Normalize backfills it from FirstSeenAt.
	LastSeenAt time.Time `json:"last_seen_at"`
}

// backfillLegacyCounters makes an entry written by a build that only knew
// distinct_users and key_prefixes read correctly. Every fill is monotone, so
// running it repeatedly is safe and it can never shrink a counter that a newer
// build already advanced.
func (e *AutoEntry) backfillLegacyCounters() {
	if e.DistinctUsers < 0 {
		e.DistinctUsers = 0
	}
	if e.WindowMinutes <= 0 {
		e.WindowMinutes = DefaultSharingWindowMinutes
	}
	if e.FirstSeenUsers <= 0 {
		e.FirstSeenUsers = e.DistinctUsers
	}
	if e.PeakUsers < e.DistinctUsers {
		e.PeakUsers = e.DistinctUsers
	}
	if e.PeakUsers < e.FirstSeenUsers {
		e.PeakUsers = e.FirstSeenUsers
	}
	// key_prefixes is capped at maxAutoEntryKeyPrefixes, so it is a lower bound
	// for the true total. Never let the cap pull an already-larger total down.
	if e.TotalKeys < len(e.KeyPrefixes) {
		e.TotalKeys = len(e.KeyPrefixes)
	}
	if e.TotalKeys < 0 {
		e.TotalKeys = 0
	}
	if e.LastSeenAt.IsZero() {
		e.LastSeenAt = e.FirstSeenAt
	}
}

// EffectiveFirstSeenUsers is the first-seen count with the legacy fallback
// applied, for readers that did not go through Normalize.
func (e AutoEntry) EffectiveFirstSeenUsers() int {
	if e.FirstSeenUsers > 0 {
		return e.FirstSeenUsers
	}
	return e.DistinctUsers
}

// EffectivePeakUsers is the peak count with the legacy fallback applied.
func (e AutoEntry) EffectivePeakUsers() int {
	if e.PeakUsers > 0 {
		return e.PeakUsers
	}
	return e.DistinctUsers
}

// EffectiveTotalKeys is the distinct-key total with the legacy fallback applied.
// A legacy entry has no total, so the masked prefix count is the best available
// lower bound.
func (e AutoEntry) EffectiveTotalKeys() int {
	if e.TotalKeys > 0 {
		return e.TotalKeys
	}
	return len(e.KeyPrefixes)
}

// EffectiveLastSeenAt is the last-seen time with the legacy fallback applied.
func (e AutoEntry) EffectiveLastSeenAt() time.Time {
	if !e.LastSeenAt.IsZero() {
		return e.LastSeenAt
	}
	return e.FirstSeenAt
}

// mergeAutoEntry folds a later hit into the stored entry for the same IP.
//
// The rules are deliberately asymmetric:
//   - first_seen_users is frozen — it records the burst's opening state;
//   - peak_users is the max inside one burst, and a gap of more than one window
//     since the previous hit means a new burst, so the peak restarts instead of
//     remembering a week-old maximum;
//   - total_keys only ever grows, because the Redis key set it comes from is
//     additive over its own (much longer) period;
//   - last_seen_at moves forward;
//   - window_minutes keeps the value that was in force when the IP was added, so
//     the displayed evidence never silently rewrites history.
func mergeAutoEntry(existing, incoming AutoEntry) AutoEntry {
	existing.backfillLegacyCounters()
	incoming.backfillLegacyCounters()

	window := time.Duration(existing.WindowMinutes) * time.Minute
	if window <= 0 {
		window = time.Duration(DefaultSharingWindowMinutes) * time.Minute
	}
	newBurst := existing.LastSeenAt.IsZero() ||
		(!incoming.LastSeenAt.IsZero() && incoming.LastSeenAt.Sub(existing.LastSeenAt) > window)

	if newBurst {
		existing.PeakUsers = incoming.PeakUsers
	} else if incoming.PeakUsers > existing.PeakUsers {
		existing.PeakUsers = incoming.PeakUsers
	}
	// distinct_users is the field every existing consumer already reads, so it
	// must not stay frozen at the crossing value: that snapshot is exactly what
	// made a six-user burst read as "4". It tracks the peak, while
	// first_seen_users keeps the frozen opening state.
	existing.DistinctUsers = existing.PeakUsers
	if incoming.TotalKeys > existing.TotalKeys {
		existing.TotalKeys = incoming.TotalKeys
	}
	// A new burst means the window that noticed the sharing is the new one; keep
	// the recorded window consistent with the peak it now describes.
	if newBurst {
		existing.WindowMinutes = incoming.WindowMinutes
	}
	if incoming.LastSeenAt.After(existing.LastSeenAt) {
		existing.LastSeenAt = incoming.LastSeenAt
	}
	if incoming.FirstSeenUsers > 0 && existing.FirstSeenUsers <= 0 {
		existing.FirstSeenUsers = incoming.FirstSeenUsers
	}
	existing.KeyPrefixes = normalizeKeyPrefixes(append(append([]string{}, existing.KeyPrefixes...), incoming.KeyPrefixes...))
	return existing
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
	// KeyMinAgeDays ignores keys younger than this many days. Zero means "use
	// the default" so older stored policies stay valid.
	KeyMinAgeDays int `json:"key_min_age_days,omitempty"`
	// TotalKeysTTLHours is how long one source IP's "distinct keys seen" set is
	// remembered. It is independent of SharingWindowMinutes on purpose: the
	// window decides whether an IP looks shared, this period decides how far
	// back "total keys" counts. Zero means "use the default" (24h) so older
	// stored policies stay valid.
	TotalKeysTTLHours int `json:"total_keys_ttl_hours,omitempty"`
	// KeyDisableEnabled is the opt-in escalation switch. When it is true a
	// sharing hit also flips the involved API keys to inactive; when it is
	// false (the product default, and what an older stored policy means) the
	// gateway only appends the IP and logs the hit. Nil therefore means "off",
	// not "unset protection": detection itself stays governed by AutoAddEnabled.
	KeyDisableEnabled *bool `json:"key_disable_enabled,omitempty"`
	// AutoEntries are appended by the gateway, never by the admin form.
	AutoEntries []AutoEntry `json:"auto_entries"`
}

// DefaultSettings leaves the policy disabled until the operator supplies IPs.
func DefaultSettings() Settings {
	enabled := true
	keyDisable := false
	return Settings{IPs: []string{}, RPM: DefaultRPM, AutoAddEnabled: &enabled,
		KeyDisableEnabled:    &keyDisable,
		SharingWindowMinutes: DefaultSharingWindowMinutes, SharingThreshold: DefaultSharingThreshold}
}

// AutoAdd reports whether distinct-user detection is on.
func (s Settings) AutoAdd() bool { return s.AutoAddEnabled == nil || *s.AutoAddEnabled }

// KeyDisable reports whether a sharing hit may also disable the involved API
// keys. Nil (never configured, e.g. a policy stored before this switch
// existed) reports false: the gateway appends the IP and leaves keys alone.
func (s Settings) KeyDisable() bool { return s.KeyDisableEnabled != nil && *s.KeyDisableEnabled }

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

// SharingKeyMinAgeDays returns the minimum key age (in days) a credential must
// reach before it can count toward sharing detection. A non-positive value
// falls back to DefaultKeyMinAgeDays.
func (s Settings) SharingKeyMinAgeDays() int {
	if s.KeyMinAgeDays <= 0 {
		return DefaultKeyMinAgeDays
	}
	return s.KeyMinAgeDays
}

// TotalKeysTTL is how long the per-IP distinct-key set is remembered. A
// non-positive value falls back to DefaultTotalKeysTTLHours.
func (s Settings) TotalKeysTTL() time.Duration {
	hours := s.TotalKeysTTLHours
	if hours <= 0 {
		hours = DefaultTotalKeysTTLHours
	}
	return time.Duration(hours) * time.Hour
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

// HasManualIP reports whether raw is on the hand-written list. The detector uses
// it to stay silent for operator-curated addresses: a manual entry is never
// updated, disabled or re-logged by the gateway.
func (s Settings) HasManualIP(raw string) bool {
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
	return false
}

// UpsertAutoEntry folds one detected hit into the policy and reports whether the
// entry is new. An existing automatic entry is merged (peak, total keys and
// last-seen move forward) instead of duplicating, which is what lets the admin
// page show the real peak and the true distinct-key total rather than the
// snapshot taken the first time the threshold was crossed.
//
// The receiver is normalized first so a legacy entry is compared on backfilled
// counters; callers persist the returned value through Normalize.
func (s Settings) UpsertAutoEntry(entry AutoEntry) (Settings, bool) {
	addr, err := netip.ParseAddr(strings.TrimSpace(entry.IP))
	if err != nil || addr.Zone() != "" || !addr.IsGlobalUnicast() || addr.IsPrivate() {
		// An address that can never be stored is left out entirely; the caller
		// treats "not added" as "nothing to write".
		return s, false
	}
	canonical := addr.Unmap().String()
	entry.IP = canonical
	entry.Source = AutoEntrySourceAuto
	entry.backfillLegacyCounters()

	// Copy before merging. A value receiver shares the backing array with the
	// caller, so writing through s.AutoEntries[i] would mutate the caller's slice
	// too and make "did anything change?" comparisons alias — which silently
	// swallowed every merge before this line existed.
	out := make([]AutoEntry, len(s.AutoEntries))
	copy(out, s.AutoEntries)
	for i := range out {
		stored := out[i]
		storedAddr, err := netip.ParseAddr(strings.TrimSpace(stored.IP))
		if err != nil || storedAddr.Unmap().String() != canonical {
			continue
		}
		out[i] = mergeAutoEntry(stored, entry)
		s.AutoEntries = out
		return s, false
	}
	s.AutoEntries = append(out, entry)
	return s, true
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
	// Materialize the key-disable switch so the admin API always returns an
	// explicit boolean and the persisted policy says what it does. A stored
	// policy without the field normalizes to false (observe-only).
	if s.KeyDisableEnabled == nil {
		disabled := false
		s.KeyDisableEnabled = &disabled
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
	if s.TotalKeysTTLHours == 0 {
		s.TotalKeysTTLHours = DefaultTotalKeysTTLHours
	}
	if s.TotalKeysTTLHours < 1 || s.TotalKeysTTLHours > 168 {
		return Settings{}, fmt.Errorf("total keys retention must be between 1 and 168 hours")
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
		// Backfill after the prefix list is normalized: a legacy entry has no
		// first_seen_users/peak_users/total_keys at all, and the prefix count is
		// the only lower bound available for its distinct-key total.
		entry.backfillLegacyCounters()
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
