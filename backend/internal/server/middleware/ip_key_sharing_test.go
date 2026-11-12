//go:build unit

package middleware

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ipnotice"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// TestIPKeySharingAutoBan covers the acceptance rules for automatically adding
// a source IP to "指定 IP 提示与限流" when several DIFFERENT user accounts call
// the gateway from it.
func TestIPKeySharingAutoBan(t *testing.T) {
	ctx := context.Background()

	newFixture := func(t *testing.T) (*IPKeySharingDetector, *service.SettingService, *time.Time) {
		t.Helper()
		server := miniredis.RunT(t)
		client := redis.NewClient(&redis.Options{Addr: server.Addr()})
		t.Cleanup(func() { _ = client.Close() })
		repo := &panelRateLimitStubRepo{}
		settings := service.NewSettingService(repo, nil)
		require.NoError(t, settings.SetGatewayIPNotice(ctx, ipnotice.Settings{
			Enabled: true,
			IPs:     []string{"198.51.100.7"}, // manual entry, must never be duplicated
			Message: "operator notice",
			RPM:     100,
		}))
		clock := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
		detector := NewIPKeySharingDetector(settings, client)
		detector.now = func() time.Time { return clock }
		return detector, settings, &clock
	}

	t.Run("three distinct users from one IP are added once", func(t *testing.T) {
		detector, settings, _ := newFixture(t)
		ip := "203.0.113.10"
		for _, userID := range []int64{101, 102} {
			_, hit := detector.Observe(ctx, ip, userID, fmt.Sprintf("sk-user-%d-abcdef", userID))
			require.False(t, hit, "two distinct users must stay below the default threshold")
		}
		result, hit := detector.Observe(ctx, ip, 103, "sk-user-103-abcdef")
		require.True(t, hit)
		require.Equal(t, ip, result.IP)
		require.Equal(t, 3, result.DistinctUsers)
		require.Equal(t, 10, result.WindowMinutes)
		require.Len(t, result.KeyPrefixes, 3)
		for _, prefix := range result.KeyPrefixes {
			require.LessOrEqual(t, len(prefix), 8, "evidence must store masked key prefixes only")
		}

		added, err := settings.AppendGatewayIPNoticeAutoEntry(ctx, result.AutoEntry())
		require.NoError(t, err)
		require.True(t, added)

		policy := settings.GatewayIPNoticeSnapshot()
		require.Len(t, policy.AutoEntries, 1)
		require.Equal(t, ip, policy.AutoEntries[0].IP)
		require.Equal(t, ipnotice.AutoEntrySourceAuto, policy.AutoEntries[0].Source)
		require.Equal(t, 3, policy.AutoEntries[0].DistinctUsers)
		require.Equal(t, 10, policy.AutoEntries[0].WindowMinutes)
		require.False(t, policy.AutoEntries[0].FirstSeenAt.IsZero())
		require.True(t, policy.HasListedIP(ip))
		_, matched := policy.Match(ip)
		require.True(t, matched, "an auto-added IP must be enforced by the existing notice policy")
		require.Equal(t, []string{"198.51.100.7", ip}, policy.ListedIPs())

		// Repeated detections must not duplicate the entry.
		addedAgain, err := settings.AppendGatewayIPNoticeAutoEntry(ctx, result.AutoEntry())
		require.NoError(t, err)
		require.False(t, addedAgain)
		require.Len(t, settings.GatewayIPNoticeSnapshot().AutoEntries, 1)
	})

	t.Run("three keys of the same user are not sharing", func(t *testing.T) {
		detector, settings, _ := newFixture(t)
		ip := "203.0.113.20"
		for i := range 5 {
			_, hit := detector.Observe(ctx, ip, 777, fmt.Sprintf("sk-same-user-%d", i))
			require.False(t, hit, "one account rotating keys is not key sharing")
		}
		require.Empty(t, settings.GatewayIPNoticeSnapshot().AutoEntries)
	})

	t.Run("two distinct users are below the threshold", func(t *testing.T) {
		detector, settings, _ := newFixture(t)
		ip := "203.0.113.30"
		for _, userID := range []int64{201, 202} {
			_, hit := detector.Observe(ctx, ip, userID, "sk-two-users")
			require.False(t, hit)
		}
		require.Empty(t, settings.GatewayIPNoticeSnapshot().AutoEntries)
	})

	t.Run("requests outside the window do not count", func(t *testing.T) {
		detector, settings, clock := newFixture(t)
		ip := "203.0.113.40"
		for _, userID := range []int64{301, 302} {
			_, hit := detector.Observe(ctx, ip, userID, fmt.Sprintf("sk-window-%d", userID))
			require.False(t, hit)
		}
		*clock = clock.Add(11 * time.Minute)
		_, hit := detector.Observe(ctx, ip, 303, "sk-window-303")
		require.False(t, hit, "users last seen 11 minutes ago are outside the 10 minute window")
		_, hit = detector.Observe(ctx, ip, 304, "sk-window-304")
		require.False(t, hit)
		result, hit := detector.Observe(ctx, ip, 305, "sk-window-305")
		require.True(t, hit)
		require.Equal(t, 3, result.DistinctUsers, "only the three users inside the window count")

		added, err := settings.AppendGatewayIPNoticeAutoEntry(ctx, result.AutoEntry())
		require.NoError(t, err)
		require.True(t, added)
	})

	t.Run("an already listed IP is not appended again", func(t *testing.T) {
		detector, settings, _ := newFixture(t)
		// The manual entry is already in the list; detection must stay silent.
		for _, userID := range []int64{401, 402, 403} {
			_, hit := detector.Observe(ctx, "198.51.100.7", userID, "sk-manual")
			require.False(t, hit)
		}
		added, err := settings.AppendGatewayIPNoticeAutoEntry(ctx, ipnotice.AutoEntry{
			IP: "198.51.100.7", DistinctUsers: 3, WindowMinutes: 10, FirstSeenAt: time.Now(),
		})
		require.NoError(t, err)
		require.False(t, added)
		policy := settings.GatewayIPNoticeSnapshot()
		require.Empty(t, policy.AutoEntries)
		require.Len(t, policy.IPs, 1)
	})

	t.Run("configured threshold and window are honored", func(t *testing.T) {
		detector, settings, _ := newFixture(t)
		enabled := true
		require.NoError(t, settings.SetGatewayIPNotice(ctx, ipnotice.Settings{
			Enabled:              true,
			IPs:                  []string{"198.51.100.7"},
			Message:              "operator notice",
			RPM:                  100,
			AutoAddEnabled:       &enabled,
			SharingThreshold:     2,
			SharingWindowMinutes: 1,
		}))
		ip := "203.0.113.50"
		_, hit := detector.Observe(ctx, ip, 501, "sk-threshold-501")
		require.False(t, hit)
		result, hit := detector.Observe(ctx, ip, 502, "sk-threshold-502")
		require.True(t, hit)
		require.Equal(t, 2, result.DistinctUsers)
		require.Equal(t, 1, result.WindowMinutes)
	})

	t.Run("auto detection can be switched off", func(t *testing.T) {
		detector, settings, _ := newFixture(t)
		disabled := false
		require.NoError(t, settings.SetGatewayIPNotice(ctx, ipnotice.Settings{
			Enabled:        true,
			IPs:            []string{"198.51.100.7"},
			Message:        "operator notice",
			RPM:            100,
			AutoAddEnabled: &disabled,
		}))
		for _, userID := range []int64{601, 602, 603} {
			_, hit := detector.Observe(ctx, "203.0.113.60", userID, "sk-disabled")
			require.False(t, hit)
		}
	})

	t.Run("non business paths never count", func(t *testing.T) {
		for _, path := range []string{"/health", "/healthz", "/readyz", "/metrics", "/", "/static/app.js", "/admin/settings"} {
			require.False(t, ipnotice.IsGatewayBusinessPath(path), path)
		}
		for _, path := range []string{"/v1/messages", "/v1/models", "/v1/sub2api/billing", "/v1beta/models", "/api/v3/contents/generations/tasks", "/antigravity/v1/usage", "/backend-api/codex/responses"} {
			require.True(t, ipnotice.IsGatewayBusinessPath(path), path)
		}
	})
}

// TestIPKeySharingPolicyNormalization locks the shape of the auto entries that
// the admin page renders and edits.
func TestIPKeySharingPolicyNormalization(t *testing.T) {
	normalized, err := (ipnotice.Settings{
		Enabled: true,
		IPs:     []string{"203.0.113.1"},
		Message: "operator notice",
		RPM:     100,
		AutoEntries: []ipnotice.AutoEntry{{
			IP:            "203.0.113.9",
			Source:        "manual", // rewritten: only the gateway may claim auto
			FirstSeenAt:   time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC),
			DistinctUsers: 4,
			WindowMinutes: 10,
			KeyPrefixes:   []string{"sk-aaaaa", "sk-aaaaa", ""},
		}},
	}).Normalize()
	require.NoError(t, err)
	require.Len(t, normalized.AutoEntries, 1)
	require.Equal(t, ipnotice.AutoEntrySourceAuto, normalized.AutoEntries[0].Source)
	require.Equal(t, []string{"sk-aaaaa"}, normalized.AutoEntries[0].KeyPrefixes)
	require.Equal(t, 10, normalized.SharingWindowMinutes)
	require.Equal(t, 3, normalized.SharingThreshold)
	require.True(t, normalized.AutoAdd())

	_, err = (ipnotice.Settings{
		RPM: 100,
		AutoEntries: []ipnotice.AutoEntry{{
			IP: "10.0.0.9", // private address: never auto-added
		}},
	}).Normalize()
	require.Error(t, err)
}
