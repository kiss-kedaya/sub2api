package iprisk

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestIPAuditSourceReleaseAndReadback(t *testing.T) {
	m, clock, _ := testManager(t, func(c *Config) {
		c.AnonymousAPI.Enabled = true
		c.AnonymousAPI.Threshold = 2
		c.Blacklist = []Rule{{Network: "8.8.8.8", Enabled: true}}
	})
	// miniredis advances TTL only through FastForward. Freeze TIME on the same
	// logical clock so an unchanged login ban cannot acquire a different expiry
	// fingerprint merely because wall-clock time passed between read and release.
	clock.SetTime(time.Now())
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		_, err := m.AnonymousFailure(ctx, "8.8.8.8", "unknown")
		require.NoError(t, err)
	}
	page, err := m.AuditPage(ctx, AuditQuery{Source: "anonymous_api", IP: "8.8.8.8", Limit: 1})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	row := page.Items[0]
	require.Equal(t, "anonymous_api", row.Source)
	// Unrelated source and a future generation must never be cleared.
	require.Error(t, m.ReleaseAuditEntry(ctx, "manual_deny", row.ID, row.Network, row.Version))
	require.Error(t, m.ReleaseAuditEntry(ctx, row.Source, row.ID, row.Network, "stale"))
	require.NoError(t, m.ReleaseAuditEntry(ctx, row.Source, row.ID, row.Network, row.Version))
	page, err = m.AuditPage(ctx, AuditQuery{Source: "anonymous_api", IP: "8.8.8.8", Limit: 20})
	require.NoError(t, err)
	require.Empty(t, page.Items)
	rules, err := m.AuditPage(ctx, AuditQuery{Source: "manual_deny", IP: "::ffff:8.8.8.8", Limit: 20})
	require.NoError(t, err)
	require.Len(t, rules.Items, 1)
	require.True(t, rules.Items[0].Manual)
	require.False(t, rules.Items[0].CanRelease)

}

func TestIPAuditPaginationExpiryAndValidation(t *testing.T) {
	m, clock, _ := testManager(t, func(c *Config) { c.AnonymousAPI.Enabled = true; c.AnonymousAPI.Threshold = 2 })
	ctx := context.Background()
	for _, ip := range []string{"8.8.8.8", "1.1.1.1", "9.9.9.9"} {
		for i := 0; i < 2; i++ {
			_, err := m.AnonymousFailure(ctx, ip, "missing")
			require.NoError(t, err)
		}
	}
	first, err := m.AuditPage(ctx, AuditQuery{Source: "anonymous_api", Limit: 1})
	require.NoError(t, err)
	require.Len(t, first.Items, 1)
	require.True(t, first.HasMore)
	next, err := m.AuditPage(ctx, AuditQuery{Source: "anonymous_api", Limit: 1, Cursor: first.NextCursor})
	require.NoError(t, err)
	require.Len(t, next.Items, 1)
	require.NotEqual(t, first.Items[0].ID, next.Items[0].ID)
	for _, query := range []AuditQuery{{Source: "bad", Limit: 20}, {Source: "anonymous_api", Limit: 101}, {Source: "anonymous_api", Limit: 20, IP: "invalid"}, {Source: "anonymous_api", Limit: 20, Cursor: -1}} {
		_, err = m.AuditPage(ctx, query)
		require.Error(t, err)
	}
	clock.FastForward(301 * time.Second)
	clock.SetTime(time.Now().Add(301 * time.Second))
	page, err := m.AuditPage(ctx, AuditQuery{Source: "anonymous_api", Limit: 20})
	require.NoError(t, err)
	require.Empty(t, page.Items)
}

func TestIPAuditRiskReleaseDoesNotClearOtherSources(t *testing.T) {
	m, _, _ := testManager(t, nil)
	ctx := context.Background()
	cfg, err := m.Config(ctx)
	require.NoError(t, err)
	network := "8.8.8.8/32"
	for _, observe := range []bool{false, true} {
		cfg.ObserveOnly = observe
		_, err = m.count(ctx, cfg, network, "login_failures", Threshold{1, 60}, true)
		require.NoError(t, err)
	}
	page, err := m.AuditPage(ctx, AuditQuery{Source: "ip_risk", IP: "8.8.8.8", Limit: 20})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	entry := page.Items[0]
	require.NoError(t, m.ReleaseAuditEntry(ctx, entry.Source, entry.ID, entry.Network, entry.Version))
	present, err := m.AuditEntryPresent(ctx, entry.Source, entry.ID, entry.Network)
	require.NoError(t, err)
	require.False(t, present)
	other, err := m.AuditPage(ctx, AuditQuery{Source: "ip_risk_observe", IP: "8.8.8.8", Limit: 20})
	require.NoError(t, err)
	require.Len(t, other.Items, 1)
}

func TestIPAuditFiltersDoNotInventHistoryOrManualState(t *testing.T) {
	m, _, _ := testManager(t, func(c *Config) {
		c.Blacklist = []Rule{{Network: "8.8.8.0/24", Enabled: true}, {Network: "9.9.9.9", Enabled: true, ExpiresAt: time.Now().Add(-time.Minute).Unix()}}
		c.Whitelist = []string{"1.1.1.1"}
	})
	ctx := context.Background()
	page, err := m.AuditPage(ctx, AuditQuery{Source: "manual_deny", IP: "8.8.8.8", State: "active", Limit: 1})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.True(t, page.Items[0].Manual)
	require.Equal(t, "permanent", page.Items[0].Duration)
	require.Nil(t, page.Items[0].StartedAt)
	page, err = m.AuditPage(ctx, AuditQuery{Source: "manual_deny", State: "expired", Limit: 10})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Equal(t, "temporary", page.Items[0].Duration)
	page, err = m.AuditPage(ctx, AuditQuery{Source: "login_failures", Limit: 10})
	require.NoError(t, err)
	require.Empty(t, page.Items)
	page, err = m.AuditPage(ctx, AuditQuery{Source: "ip_history", Limit: 10})
	require.NoError(t, err)
	require.Empty(t, page.Items)
	cfg, err := m.Config(ctx)
	require.NoError(t, err)
	m.record(ctx, cfg, Event{ID: "unused", At: time.Now().Unix(), Network: "8.8.8.8/32", Reason: "ip_blacklist", Decision: "blocked"})
	page, err = m.AuditPage(ctx, AuditQuery{Source: "ip_history", Start: time.Now().Add(-time.Hour).Unix(), End: time.Now().Add(time.Hour).Unix(), Limit: 10})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.True(t, page.Items[0].Manual)
	require.False(t, page.Items[0].CanRelease)
}

func TestIPAuditDoesNotReleaseRecreatedBanWithinSameSecond(t *testing.T) {
	m, clock, _ := testManager(t, func(c *Config) { c.AnonymousAPI.Enabled = true; c.AnonymousAPI.Threshold = 2 })
	ctx := context.Background()
	clock.SetTime(time.Now().Truncate(time.Second))
	for i := 0; i < 2; i++ {
		_, err := m.AnonymousFailure(ctx, "8.8.8.8", "unknown")
		require.NoError(t, err)
	}
	page, err := m.AuditPage(ctx, AuditQuery{Source: "anonymous_api", IP: "8.8.8.8", Limit: 20})
	require.NoError(t, err)
	row := page.Items[0]
	require.NoError(t, m.UnbanAnonymous(ctx, "8.8.8.8"))
	for i := 0; i < 2; i++ {
		_, err := m.AnonymousFailure(ctx, "8.8.8.8", "unknown")
		require.NoError(t, err)
	}
	require.ErrorIs(t, m.ReleaseAuditEntry(ctx, row.Source, row.ID, row.Network, row.Version), ErrAuditConflict, "a previous confirmation must not clear a newly created ban")
}
