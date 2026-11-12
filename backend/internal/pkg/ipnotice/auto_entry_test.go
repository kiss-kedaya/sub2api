package ipnotice

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestCloudflareRangesSnapshot pins the embedded fallback: the two acceptance
// addresses must be excluded even when the live fetch never runs, while a
// Cloudflare-owned address that is NOT in the published edge list must not be
// blanket-excluded.
func TestCloudflareRangesSnapshot(t *testing.T) {
	require.Equal(t, 15, len(parsePrefixSet(defaultCloudflareRanges).v4), "the embedded snapshot keeps all 15 published IPv4 ranges")
	require.Equal(t, 7, len(parsePrefixSet(defaultCloudflareRanges).v6), "the embedded snapshot keeps all 7 published IPv6 ranges")

	require.True(t, IsCloudflareIP("104.16.0.1"), "104.16.0.0/13 is a Cloudflare edge range")
	require.True(t, IsCloudflareIP("172.64.0.1"))
	require.True(t, IsCloudflareIP("2606:4700::1"), "2606:4700::/32 is the Cloudflare IPv6 edge range")
	require.True(t, IsCloudflareIP("::ffff:104.16.0.1"), "an IPv4-mapped source must match the IPv4 ranges")

	require.False(t, IsCloudflareIP("104.15.255.255"), "one address below 104.16.0.0/13 is outside the range")
	require.False(t, IsCloudflareIP("1.1.1.1"), "Cloudflare's public DNS is not a published edge range")
	require.False(t, IsCloudflareIP("203.0.113.10"))
	require.False(t, IsCloudflareIP("not-an-ip"))
}

// TestExcludedSourceIP covers the exclusion gate that runs before any Redis
// write. Every excluded case must be excluded for the documented reason.
func TestExcludedSourceIP(t *testing.T) {
	cases := []struct {
		ip     string
		reason string
		ok     bool
	}{
		{ip: "104.16.0.1", reason: SourceExclusionCloudflare, ok: true},
		{ip: "2606:4700::1", reason: SourceExclusionCloudflare, ok: true},
		{ip: "10.1.2.3", reason: SourceExclusionPrivate, ok: true},
		{ip: "172.16.5.5", reason: SourceExclusionPrivate, ok: true},
		{ip: "172.31.255.254", reason: SourceExclusionPrivate, ok: true},
		{ip: "192.168.1.1", reason: SourceExclusionPrivate, ok: true},
		{ip: "127.0.0.1", reason: SourceExclusionPrivate, ok: true},
		{ip: "::1", reason: SourceExclusionPrivate, ok: true},
		{ip: "169.254.1.1", reason: SourceExclusionPrivate, ok: true},
		{ip: "100.64.0.1", reason: SourceExclusionPrivate, ok: true},
		{ip: "fc00::1", reason: SourceExclusionPrivate, ok: true},
		{ip: "fe80::1", reason: SourceExclusionPrivate, ok: true},
		{ip: "", reason: SourceExclusionInvalid, ok: true},
		{ip: "nope", reason: SourceExclusionInvalid, ok: true},
		// A public address that is neither Cloudflare nor private still counts.
		{ip: "203.0.113.10"},
		{ip: "172.32.0.1"},
		{ip: "2001:4860:4860::8888"},
	}
	for _, tc := range cases {
		reason, excluded := ExcludedSourceIP(tc.ip)
		require.Equal(t, tc.ok, excluded, tc.ip)
		require.Equal(t, tc.reason, reason, tc.ip)
	}
}

// TestRefreshCloudflareRangesKeepsSnapshotOnFailure pins the fallback contract:
// a failed or nonsensical response must never empty the exclusion set, because an
// empty set would make Cloudflare egress auto-listable.
func TestRefreshCloudflareRangesKeepsSnapshotOnFailure(t *testing.T) {
	before := cloudflareRanges.Load()
	require.NotNil(t, before)
	t.Cleanup(func() { cloudflareRanges.Store(before) })

	failing := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("network unreachable")
	})}
	require.Error(t, RefreshCloudflareRanges(context.Background(), failing))
	require.Equal(t, before.count, CloudflareRangeCount(), "a failed refresh must keep the existing ranges")

	empty := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Header: make(http.Header)}, nil
	})}
	require.Error(t, RefreshCloudflareRanges(context.Background(), empty), "an empty body is not a valid range list")
	require.Equal(t, before.count, CloudflareRangeCount())
}

// TestRefreshCloudflareRangesReplacesWithLiveList pins the success path: the
// fetched list wins, so a new Cloudflare range does not need a release.
func TestRefreshCloudflareRangesReplacesWithLiveList(t *testing.T) {
	before := cloudflareRanges.Load()
	t.Cleanup(func() { cloudflareRanges.Store(before) })

	live := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		rec := httptest.NewRecorder()
		if strings.HasSuffix(req.URL.Path, "/ips-v6") {
			_, _ = rec.WriteString("2001:db8::/32\n")
		} else {
			_, _ = rec.WriteString("203.0.113.0/24\n198.51.100.0/24\n")
		}
		return rec.Result(), nil
	})}

	require.NoError(t, RefreshCloudflareRanges(context.Background(), live))
	require.Equal(t, 3, CloudflareRangeCount())
	require.True(t, IsCloudflareIP("203.0.113.7"))
	require.False(t, IsCloudflareIP("104.16.0.1"), "the live list replaces the snapshot entirely")
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// TestAutoEntryLegacyCompatibility is the acceptance case for entries written by
// a build that only knew distinct_users and key_prefixes: reading them must fill
// first_seen_users, peak_users, total_keys and last_seen_at without error.
func TestAutoEntryLegacyCompatibility(t *testing.T) {
	legacy := AutoEntry{
		IP:            "203.0.113.7",
		Source:        AutoEntrySourceAuto,
		FirstSeenAt:   time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC),
		DistinctUsers: 4,
		WindowMinutes: 5,
		KeyPrefixes:   []string{"sk-aaaa1", "sk-aaaa2"},
	}

	// The accessors answer correctly even without going through Normalize.
	require.Equal(t, 4, legacy.EffectiveFirstSeenUsers())
	require.Equal(t, 4, legacy.EffectivePeakUsers())
	require.Equal(t, 2, legacy.EffectiveTotalKeys())
	require.Equal(t, legacy.FirstSeenAt, legacy.EffectiveLastSeenAt())

	normalized, err := (Settings{
		Enabled:     true,
		IPs:         []string{"203.0.113.1"},
		Message:     "operator notice",
		RPM:         100,
		AutoEntries: []AutoEntry{legacy},
	}).Normalize()
	require.NoError(t, err, "a legacy entry must not fail normalization")
	require.Len(t, normalized.AutoEntries, 1)
	entry := normalized.AutoEntries[0]
	require.Equal(t, 4, entry.FirstSeenUsers, "first_seen_users falls back to distinct_users")
	require.Equal(t, 4, entry.PeakUsers, "peak_users falls back to distinct_users")
	require.Equal(t, 2, entry.TotalKeys, "total_keys falls back to the masked prefix count")
	require.Equal(t, entry.FirstSeenAt, entry.LastSeenAt, "last_seen_at falls back to first_seen_at")

	// Backfill is idempotent and never shrinks an advanced counter.
	advanced := entry
	advanced.PeakUsers = 9
	advanced.TotalKeys = 11
	advanced.backfillLegacyCounters()
	require.Equal(t, 9, advanced.PeakUsers)
	require.Equal(t, 11, advanced.TotalKeys)
}

// TestUpsertAutoEntryPeakAndTotal covers the stored-entry rules: the first hit
// freezes first_seen_users, later hits in the same burst raise the peak, and a
// burst that starts after a full quiet window restarts the peak.
func TestUpsertAutoEntryPeakAndTotal(t *testing.T) {
	base := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	settings := Settings{
		Enabled:              true,
		IPs:                  []string{"203.0.113.1"},
		Message:              "notice",
		RPM:                  100,
		SharingWindowMinutes: 5,
		SharingThreshold:     4,
	}

	hit := func(users, total int, at time.Time) AutoEntry {
		return AutoEntry{
			IP: "203.0.113.7", DistinctUsers: users, FirstSeenUsers: users,
			PeakUsers: users, TotalKeys: total, WindowMinutes: 5,
			FirstSeenAt: at, LastSeenAt: at, KeyPrefixes: []string{"sk-aaaa1"},
		}
	}

	settings, added := settings.UpsertAutoEntry(hit(4, 4, base))
	require.True(t, added)
	require.Len(t, settings.AutoEntries, 1)
	require.Equal(t, 4, settings.AutoEntries[0].FirstSeenUsers)
	require.Equal(t, 4, settings.AutoEntries[0].PeakUsers)

	// Same burst, 5th then 6th user: the peak grows, the first-seen stays.
	settings, added = settings.UpsertAutoEntry(hit(5, 7, base.Add(31*time.Second)))
	require.False(t, added, "a later hit must merge instead of duplicating")
	require.Equal(t, 4, settings.AutoEntries[0].FirstSeenUsers)
	require.Equal(t, 5, settings.AutoEntries[0].PeakUsers)
	require.Equal(t, 7, settings.AutoEntries[0].TotalKeys)

	settings, _ = settings.UpsertAutoEntry(hit(6, 10, base.Add(62*time.Second)))
	require.Equal(t, 4, settings.AutoEntries[0].FirstSeenUsers, "first_seen_users is frozen")
	require.Equal(t, 6, settings.AutoEntries[0].PeakUsers)
	require.Equal(t, 10, settings.AutoEntries[0].TotalKeys)
	require.Equal(t, base.Add(62*time.Second), settings.AutoEntries[0].LastSeenAt)

	// A burst that starts after a full window of silence restarts the peak, but
	// the distinct-key total still only moves forward.
	settings, _ = settings.UpsertAutoEntry(hit(4, 10, base.Add(62*time.Second+6*time.Minute)))
	require.Equal(t, 4, settings.AutoEntries[0].PeakUsers, "a new burst restarts the peak")
	require.Equal(t, 4, settings.AutoEntries[0].FirstSeenUsers)
	require.Equal(t, 10, settings.AutoEntries[0].TotalKeys)
}

// TestUpsertAutoEntryKeyPrefixesStayBounded keeps the persisted evidence small:
// a burst that keeps rotating keys must not grow the stored prefix list without
// limit.
func TestUpsertAutoEntryKeyPrefixesStayBounded(t *testing.T) {
	settings := Settings{Enabled: true, IPs: []string{"203.0.113.1"}, Message: "n", RPM: 100}
	at := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	for i := range 12 {
		prefixes := []string{"sk-" + strings.Repeat("a", i+1)}
		settings, _ = settings.UpsertAutoEntry(AutoEntry{
			IP: "203.0.113.7", DistinctUsers: 4, FirstSeenUsers: 4, PeakUsers: 4,
			TotalKeys: i + 1, WindowMinutes: 5, FirstSeenAt: at,
			LastSeenAt: at.Add(time.Duration(i) * 31 * time.Second), KeyPrefixes: prefixes,
		})
	}
	require.LessOrEqual(t, len(settings.AutoEntries[0].KeyPrefixes), maxAutoEntryKeyPrefixes)
	require.Equal(t, 12, settings.AutoEntries[0].TotalKeys, "the total is a number, so it is not capped by the prefix list")
}

// TestTotalKeysRetentionDefaults pins the 24h default and its accepted range.
func TestTotalKeysRetentionDefaults(t *testing.T) {
	require.Equal(t, 24*time.Hour, Settings{}.TotalKeysTTL())
	require.Equal(t, 48*time.Hour, Settings{TotalKeysTTLHours: 48}.TotalKeysTTL())

	normalized, err := (Settings{Enabled: true, IPs: []string{"203.0.113.1"}, Message: "n", RPM: 100}).Normalize()
	require.NoError(t, err)
	require.Equal(t, DefaultTotalKeysTTLHours, normalized.TotalKeysTTLHours)

	for _, invalid := range []int{-1, 169, 1000} {
		_, err := (Settings{
			Enabled: true, IPs: []string{"203.0.113.1"}, Message: "n", RPM: 100,
			TotalKeysTTLHours: invalid,
		}).Normalize()
		require.Error(t, err, "retention %d must be rejected", invalid)
	}
}

// TestUpsertAutoEntryDoesNotMutateReceiver pins the aliasing bug that silently
// swallowed every merge: a value receiver that writes through its own slice is
// indistinguishable from the caller's slice, so a "did anything change?" check
// compares a value with itself and always concludes "no".
func TestUpsertAutoEntryDoesNotMutateReceiver(t *testing.T) {
	at := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	original := Settings{
		Enabled: true,
		IPs:     []string{"203.0.113.1"},
		Message: "n",
		RPM:     100,
		AutoEntries: []AutoEntry{{
			IP: "203.0.113.7", DistinctUsers: 4, FirstSeenUsers: 4,
			PeakUsers: 4, TotalKeys: 4, WindowMinutes: 5,
			FirstSeenAt: at, LastSeenAt: at,
		}},
	}
	updated, added := original.UpsertAutoEntry(AutoEntry{
		IP: "203.0.113.7", DistinctUsers: 9, FirstSeenUsers: 9,
		PeakUsers: 9, TotalKeys: 9, WindowMinutes: 5,
		FirstSeenAt: at, LastSeenAt: at.Add(31 * time.Second),
	})
	require.False(t, added)
	require.Equal(t, 9, updated.AutoEntries[0].PeakUsers)
	require.Equal(t, 9, updated.AutoEntries[0].TotalKeys)
	require.Equal(t, 4, original.AutoEntries[0].PeakUsers, "the receiver must not be mutated")
	require.Equal(t, 4, original.AutoEntries[0].TotalKeys)
}
