package iprisk

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAnonymousSlidingWindowBanExpiryAndUnban(t *testing.T) {
	m, clock, client := testManager(t, func(c *Config) { c.Enabled = false; c.AnonymousAPI.Enabled = true; c.AnonymousAPI.Threshold = 3 })
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	advance := func(d time.Duration) { now = now.Add(d); clock.FastForward(d); clock.SetTime(now) }
	advance(0)
	fail := func(ip string) *Ban { b, e := m.AnonymousFailure(ctx, ip, "unknown"); require.NoError(t, e); return b }
	require.Nil(t, fail("8.8.8.8"))
	advance(40 * time.Second)
	require.Nil(t, fail("::ffff:8.8.8.8"))
	advance(21 * time.Second)
	require.Nil(t, fail("8.8.8.8"), "first event left the rolling window")
	require.NotNil(t, fail("8.8.8.8"), "remaining three events span a fixed-window boundary")
	bans, e := m.AnonymousBans(ctx)
	require.NoError(t, e)
	require.Len(t, bans, 1)
	expiry := bans[0].ExpiresAt
	advance(time.Second)
	require.Equal(t, expiry, fail("8.8.8.8").ExpiresAt, "blocked traffic must not prolong a ban")
	require.NoError(t, m.UnbanAnonymous(ctx, "::ffff:8.8.8.8"))
	require.Nil(t, fail("8.8.8.8"))
	require.Nil(t, fail("8.8.8.8"))
	require.NotNil(t, fail("8.8.8.8"))
	advance(301 * time.Second)
	require.Nil(t, fail("8.8.8.8"))
	require.LessOrEqual(t, len(clock.Keys()), 4)
	require.NoError(t, client.Ping(ctx).Err())
}

func TestAnonymousAtomicCapacityWhitelistAndFailure(t *testing.T) {
	m, server, client := testManager(t, func(c *Config) {
		c.AnonymousAPI.Enabled = true
		c.AnonymousAPI.Threshold = 10
		c.AnonymousAPI.MaxTrackedIPs = 100
		c.Whitelist = []string{"9.9.9.0/24"}
	})
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = m.AnonymousFailure(ctx, "2001:4860:4860::8888", "missing") }()
	}
	wg.Wait()
	bans, e := m.AnonymousBans(ctx)
	require.NoError(t, e)
	require.Len(t, bans, 1)
	for _, ip := range []string{"9.9.9.9", "bad", "::", "::1", "10.0.0.1", "fc00::1", "fe80::1", "224.0.0.1", "100.64.0.1", "192.0.2.1", "2001:db8::1", "240.0.0.1"} {
		for i := 0; i < 11; i++ {
			b, e := m.AnonymousFailure(ctx, ip, "unknown")
			require.NoError(t, e)
			require.Nil(t, b, ip)
		}
	}
	for i := 0; i < 150; i++ {
		_, e := m.AnonymousFailure(ctx, fmt.Sprintf("11.0.0.%d", i), "malformed")
		require.NoError(t, e)
	}
	require.LessOrEqual(t, len(server.Keys()), 202, "hard cap includes failure/ban keys and registry")
	require.EqualValues(t, 100, client.ZCard(ctx, anonymousPrefix+"tracked").Val())
	// Capacity recovers by expiry on the next operation, with no key scanning.
	server.FastForward(61 * time.Second)
	server.SetTime(time.Now().Add(61 * time.Second))
	b, e := m.AnonymousFailure(ctx, "8.8.8.8", "unknown")
	require.NoError(t, e)
	require.Nil(t, b)
	require.EqualValues(t, 2, client.ZCard(ctx, anonymousPrefix+"tracked").Val())

	require.NoError(t, client.Close())
	b, e = m.AnonymousFailure(ctx, "8.8.4.4", "missing")
	require.Error(t, e)
	require.Nil(t, b)
}

func TestAnonymousDefaultAndSuccessPolicy(t *testing.T) {
	m, _, _ := testManager(t, nil)
	ctx := context.Background()
	for i := 0; i < 40; i++ {
		b, e := m.AnonymousFailure(ctx, "8.8.8.8", "missing")
		require.NoError(t, e)
		require.Nil(t, b)
	}
	cfg, e := m.Config(ctx)
	require.NoError(t, e)
	cfg.AnonymousAPI.Enabled = true
	cfg.AnonymousAPI.Threshold = 2
	cfg.AnonymousAPI.ResetOnSuccess = true
	require.NoError(t, m.Save(ctx, cfg))
	b, e := m.AnonymousFailure(ctx, "8.8.8.8", "unknown")
	require.NoError(t, e)
	require.Nil(t, b)
	require.NoError(t, m.AnonymousSuccess(ctx, "8.8.8.8"))
	b, e = m.AnonymousFailure(ctx, "8.8.8.8", "unknown")
	require.NoError(t, e)
	require.Nil(t, b)
	b, e = m.AnonymousFailure(ctx, "8.8.8.8", "disabled")
	require.NoError(t, e)
	require.NotNil(t, b)
	require.NoError(t, m.AnonymousSuccess(ctx, "8.8.8.8"))
	bans, e := m.AnonymousBans(ctx)
	require.NoError(t, e)
	require.Empty(t, bans)
	b, e = m.AnonymousFailure(ctx, "8.8.8.8", "upstream_401")
	require.NoError(t, e)
	require.Nil(t, b)
}

func TestAnonymousSharedStateAndIndependentRelease(t *testing.T) {
	m, _, client := testManager(t, func(c *Config) {
		c.AnonymousAPI.Enabled = true
		c.AnonymousAPI.Threshold = 2
		c.Blacklist = []Rule{{Network: "8.8.8.8", Enabled: true}}
	})
	cfg, e := m.Config(context.Background())
	require.NoError(t, e)
	other := New(&memoryStore{c: cfg}, client, nil, false)
	t.Cleanup(other.Stop)
	ctx := context.Background()
	b, e := m.AnonymousFailure(ctx, "8.8.8.8", "unknown")
	require.NoError(t, e)
	require.Nil(t, b)
	b, e = other.AnonymousFailure(ctx, "8.8.8.8", "unknown")
	require.NoError(t, e)
	require.NotNil(t, b)
	require.Equal(t, 300*time.Second, b.RetryAfter)
	// Default success policy does not let an active key erase a stranger's budget.
	require.NoError(t, other.AnonymousSuccess(ctx, "8.8.8.8"))
	bans, e := m.AnonymousBans(ctx)
	require.NoError(t, e)
	require.Len(t, bans, 1)
	require.NoError(t, other.UnbanAnonymous(ctx, "8.8.8.8/32"))
	bans, e = m.AnonymousBans(ctx)
	require.NoError(t, e)
	require.Empty(t, bans)
	check, e := m.Check(ctx, "8.8.8.8", "", false)
	require.NoError(t, e)
	require.Equal(t, "ip_blacklist", check.Reason)
	require.Error(t, m.UnbanAnonymous(ctx, "8.8.8.0/24"))
}

func TestAnonymousConfigValidationAndOldSavedPolicy(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AnonymousAPI = AnonymousAPIConfig{}
	normalized, e := NormalizeConfig(cfg)
	require.NoError(t, e)
	require.False(t, normalized.AnonymousAPI.Enabled)
	require.Equal(t, 30, normalized.AnonymousAPI.Threshold)
	for _, modify := range []func(*AnonymousAPIConfig){func(a *AnonymousAPIConfig) { a.Threshold = 0 }, func(a *AnonymousAPIConfig) { a.WindowSeconds = 0 }, func(a *AnonymousAPIConfig) { a.BanSeconds = 86401 }, func(a *AnonymousAPIConfig) { a.MaxTrackedIPs = 50001 }} {
		cfg = DefaultConfig()
		modify(&cfg.AnonymousAPI)
		_, e = NormalizeConfig(cfg)
		require.Error(t, e)
	}
}

func TestAnonymousIPv6NeighborsRemainSeparate(t *testing.T) {
	m, _, _ := testManager(t, func(c *Config) { c.AnonymousAPI.Enabled = true; c.AnonymousAPI.Threshold = 2 })
	ctx := context.Background()
	for _, ip := range []string{"2001:4860:4860::8888", "2001:4860:4860::8844"} {
		b, e := m.AnonymousFailure(ctx, ip, "malformed")
		require.NoError(t, e)
		require.Nil(t, b)
	}
	b, e := m.AnonymousFailure(ctx, "2001:4860:4860::8888", "malformed")
	require.NoError(t, e)
	require.NotNil(t, b)
	require.Equal(t, "2001:4860:4860::8888/128", b.Network)
}
