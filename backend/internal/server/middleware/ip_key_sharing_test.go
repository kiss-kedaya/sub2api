//go:build unit

package middleware

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ipnotice"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

var (
	// sharingTestWindow is the frozen clock every detector test uses.
	sharingTestWindow = time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	// sharingTestKeyCreatedAt is older than the 30-day key-age gate, so these
	// keys are eligible for detection unless a test overrides it.
	sharingTestKeyCreatedAt = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
)

// sharingTestKey builds the authenticated key the middleware would pass in.
func sharingTestKey(userID int64) *service.APIKey {
	return &service.APIKey{
		ID:        userID,
		UserID:    userID,
		Key:       fmt.Sprintf("sk-%d-secret", userID),
		CreatedAt: sharingTestKeyCreatedAt,
	}
}

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
		clock := sharingTestWindow
		detector := NewIPKeySharingDetector(settings, client)
		detector.now = func() time.Time { return clock }
		return detector, settings, &clock
	}

	t.Run("three distinct users from one IP are added once", func(t *testing.T) {
		detector, settings, _ := newFixture(t)
		ip := "203.0.113.10"
		for _, userID := range []int64{101, 102} {
			_, hit := detector.Observe(ctx, ip, userID, sharingTestKey(userID))
			require.False(t, hit, "two distinct users must stay below the default threshold")
		}
		result, hit := detector.Observe(ctx, ip, 103, sharingTestKey(103))
		require.True(t, hit)
		require.Equal(t, ip, result.IP)
		require.Equal(t, 3, result.DistinctUsers)
		require.Equal(t, 5, result.WindowMinutes, "the default window is five minutes")
		require.ElementsMatch(t, []int64{101, 102, 103}, result.KeyIDs)
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
		require.Equal(t, 5, policy.AutoEntries[0].WindowMinutes)
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
			_, hit := detector.Observe(ctx, ip, 777, &service.APIKey{
				ID: int64(700 + i), UserID: 777, Key: fmt.Sprintf("sk-same-user-%d", i), CreatedAt: sharingTestKeyCreatedAt,
			})
			require.False(t, hit, "one account rotating keys is not key sharing")
		}
		require.Empty(t, settings.GatewayIPNoticeSnapshot().AutoEntries)
	})

	t.Run("two distinct users are below the threshold", func(t *testing.T) {
		detector, settings, _ := newFixture(t)
		ip := "203.0.113.30"
		for _, userID := range []int64{201, 202} {
			_, hit := detector.Observe(ctx, ip, userID, sharingTestKey(userID))
			require.False(t, hit)
		}
		require.Empty(t, settings.GatewayIPNoticeSnapshot().AutoEntries)
	})

	t.Run("requests outside the window do not count", func(t *testing.T) {
		detector, settings, clock := newFixture(t)
		ip := "203.0.113.40"
		for _, userID := range []int64{301, 302} {
			_, hit := detector.Observe(ctx, ip, userID, sharingTestKey(userID))
			require.False(t, hit)
		}
		*clock = clock.Add(11 * time.Minute)
		_, hit := detector.Observe(ctx, ip, 303, sharingTestKey(303))
		require.False(t, hit, "users last seen 11 minutes ago are outside the five minute window")
		_, hit = detector.Observe(ctx, ip, 304, sharingTestKey(304))
		require.False(t, hit)
		result, hit := detector.Observe(ctx, ip, 305, sharingTestKey(305))
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
			_, hit := detector.Observe(ctx, "198.51.100.7", userID, sharingTestKey(userID))
			require.False(t, hit)
		}
		added, err := settings.AppendGatewayIPNoticeAutoEntry(ctx, ipnotice.AutoEntry{
			IP: "198.51.100.7", DistinctUsers: 3, WindowMinutes: 5, FirstSeenAt: time.Now(),
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
		_, hit := detector.Observe(ctx, ip, 501, sharingTestKey(501))
		require.False(t, hit)
		result, hit := detector.Observe(ctx, ip, 502, sharingTestKey(502))
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
			_, hit := detector.Observe(ctx, "203.0.113.60", userID, sharingTestKey(userID))
			require.False(t, hit)
		}
	})

	// Regression: a key younger than the 30-day gate must not contribute, so a
	// fresh credential cannot drag a single-user IP into the list by itself.
	t.Run("keys younger than the minimum age never count", func(t *testing.T) {
		detector, settings, clock := newFixture(t)
		ip := "203.0.113.70"
		young := func(userID int64) *service.APIKey {
			key := sharingTestKey(userID)
			key.CreatedAt = clock.Add(-24 * time.Hour) // one day old
			return key
		}
		for _, userID := range []int64{701, 702, 703, 704} {
			_, hit := detector.Observe(ctx, ip, userID, young(userID))
			require.False(t, hit, "a one-day-old key must not count")
		}
		require.Empty(t, settings.GatewayIPNoticeSnapshot().AutoEntries)

		// The gate skips before the Redis write, so the young keys never entered
		// the window: three old keys are still required to cross the threshold.
		for _, userID := range []int64{705, 706} {
			_, hit := detector.Observe(ctx, ip, userID, sharingTestKey(userID))
			require.False(t, hit)
		}
		result, hit := detector.Observe(ctx, ip, 707, sharingTestKey(707))
		require.True(t, hit)
		require.Equal(t, 3, result.DistinctUsers)
		require.ElementsMatch(t, []int64{705, 706, 707}, result.KeyIDs)
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

// TestIPKeySharingWritesOncePerWindow pins the Redis traffic shape: a repeat
// request from an (ip,user) pair that is already recorded in the window must not
// rewrite the sorted set (its score stays at the first write), while the window
// still refreshes the pair once the old record falls out.
func TestIPKeySharingWritesOncePerWindow(t *testing.T) {
	ctx := context.Background()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	settings := service.NewSettingService(&panelRateLimitStubRepo{}, nil)
	require.NoError(t, settings.SetGatewayIPNotice(ctx, ipnotice.Settings{
		Enabled: true, IPs: []string{"198.51.100.7"}, Message: "operator notice", RPM: 100,
	}))
	clock := sharingTestWindow
	detector := NewIPKeySharingDetector(settings, client)
	detector.now = func() time.Time { return clock }
	ip := "203.0.113.80"
	userKey := ipKeySharingUsersPrefix + ip

	_, hit := detector.Observe(ctx, ip, 801, sharingTestKey(801))
	require.False(t, hit)
	firstScore, err := client.ZScore(ctx, userKey, "801").Result()
	require.NoError(t, err)

	// Twenty repeats inside the same window: the score must not move and the set
	// must not grow, so these requests cost one ZSCORE + one ZCOUNT each.
	for range 20 {
		_, hit = detector.Observe(ctx, ip, 801, sharingTestKey(801))
		require.False(t, hit)
	}
	repeatScore, err := client.ZScore(ctx, userKey, "801").Result()
	require.NoError(t, err)
	require.Equal(t, firstScore, repeatScore, "a repeat request must not rewrite the member")
	require.EqualValues(t, 1, client.ZCard(ctx, userKey).Val())

	// Once the recorded score falls outside the window, the next request for the
	// same pair refreshes it so an active sharer keeps counting.
	clock = clock.Add(6 * time.Minute)
	_, hit = detector.Observe(ctx, ip, 801, sharingTestKey(801))
	require.False(t, hit)
	refreshedScore, err := client.ZScore(ctx, userKey, "801").Result()
	require.NoError(t, err)
	require.Greater(t, refreshedScore, repeatScore, "an expired record is refreshed on the next request")
}

// TestIPKeySharingEvidenceExcludesStaleUsers guards the window boundary after
// writes are coalesced: members older than the window stay in the sorted set
// until the key expires, so both the count and the evidence read must filter by
// score. Otherwise an out-of-window key would be disabled by mistake.
func TestIPKeySharingEvidenceExcludesStaleUsers(t *testing.T) {
	ctx := context.Background()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	settings := service.NewSettingService(&panelRateLimitStubRepo{}, nil)
	require.NoError(t, settings.SetGatewayIPNotice(ctx, ipnotice.Settings{
		Enabled: true, IPs: []string{"198.51.100.7"}, Message: "operator notice", RPM: 100,
	}))
	clock := sharingTestWindow
	detector := NewIPKeySharingDetector(settings, client)
	detector.now = func() time.Time { return clock }
	ip := "203.0.113.81"

	for _, userID := range []int64{811, 812} {
		_, hit := detector.Observe(ctx, ip, userID, sharingTestKey(userID))
		require.False(t, hit)
	}
	clock = clock.Add(11 * time.Minute)
	// The two old users are still physically present in the sorted set; only the
	// three users inside the window may be reported.
	for _, userID := range []int64{813, 814} {
		_, hit := detector.Observe(ctx, ip, userID, sharingTestKey(userID))
		require.False(t, hit)
	}
	result, hit := detector.Observe(ctx, ip, 815, sharingTestKey(815))
	require.True(t, hit)
	require.Equal(t, 3, result.DistinctUsers)
	require.ElementsMatch(t, []int64{813, 814, 815}, result.KeyIDs,
		"users outside the five-minute window must not be disabled")
	require.Len(t, result.KeyPrefixes, 3)
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
			WindowMinutes: 5,
			KeyPrefixes:   []string{"sk-aaaaa", "sk-aaaaa", ""},
		}},
	}).Normalize()
	require.NoError(t, err)
	require.Len(t, normalized.AutoEntries, 1)
	require.Equal(t, ipnotice.AutoEntrySourceAuto, normalized.AutoEntries[0].Source)
	require.Equal(t, []string{"sk-aaaaa"}, normalized.AutoEntries[0].KeyPrefixes)
	require.Equal(t, 5, normalized.SharingWindowMinutes, "the default window is five minutes")
	require.Equal(t, 3, normalized.SharingThreshold)
	require.Equal(t, 30, normalized.SharingKeyMinAgeDays(), "unset key age falls back to 30 days")
	require.True(t, normalized.AutoAdd())

	_, err = (ipnotice.Settings{
		RPM: 100,
		AutoEntries: []ipnotice.AutoEntry{{
			IP: "10.0.0.9", // private address: never auto-added
		}},
	}).Normalize()
	require.Error(t, err)
}

// The detector only acts on hosting/proxy addresses; the count alone must never
// stop a shared home or office egress. The counting tests above pin the
// distinct-user rules, so the classifier is stubbed to "yes" for the whole
// package and exercised explicitly below.
func TestMain(m *testing.M) {
	datacenterClassifier = func(context.Context, string) bool { return true }
	os.Exit(m.Run())
}

// TestIPKeySharingDatacenterGate is the anti-false-positive regression: a
// residential or corporate egress shared by four different users is never
// added to the IP list.
func TestIPKeySharingDatacenterGate(t *testing.T) {
	previous := datacenterClassifier
	t.Cleanup(func() { datacenterClassifier = previous })

	datacenterClassifier = func(context.Context, string) bool { return false }

	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	settings := service.NewSettingService(&panelRateLimitStubRepo{}, nil)
	require.NoError(t, settings.SetGatewayIPNotice(context.Background(), ipnotice.Settings{
		Enabled: true, IPs: []string{"198.51.100.7"}, Message: "operator notice", RPM: 100,
	}))
	detector := NewIPKeySharingDetector(settings, client)
	detector.now = func() time.Time { return sharingTestWindow }

	for _, id := range []int64{401, 402, 403, 404} {
		_, hit := detector.Observe(context.Background(), "203.0.113.9", id, sharingTestKey(id))
		require.False(t, hit, "a shared home/office egress must never be reported")
	}
	require.Empty(t, settings.GatewayIPNoticeSnapshot().AutoEntries)
}

// sharingAcceptanceFixture builds a detector plus the settings service and Redis
// client the acceptance helpers below need. The window and threshold are
// operator-tunable, so each case declares exactly what it depends on.
func sharingAcceptanceFixture(t *testing.T, threshold, windowMinutes int) (*IPKeySharingDetector, *service.SettingService, *redis.Client, func(time.Duration)) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	settings := service.NewSettingService(&panelRateLimitStubRepo{}, nil)
	require.NoError(t, settings.SetGatewayIPNotice(context.Background(), ipnotice.Settings{
		Enabled:              true,
		IPs:                  []string{"198.51.100.7"},
		Message:              "operator notice",
		RPM:                  100,
		SharingThreshold:     threshold,
		SharingWindowMinutes: windowMinutes,
	}))
	clock := sharingTestWindow
	detector := NewIPKeySharingDetector(settings, client)
	detector.now = func() time.Time { return clock }
	// advance moves BOTH clocks. miniredis expires keys from its own internal
	// clock, so moving only the detector's clock would leave the 30-second write
	// throttle key alive forever and silently suppress every later hit.
	advance := func(d time.Duration) {
		clock = clock.Add(d)
		server.FastForward(d)
	}
	return detector, settings, client, advance
}

// sharingAcceptanceKey builds an eligible key whose ID and prefix are distinct
// per keyID, so "how many different keys" is unambiguous.
func sharingAcceptanceKey(userID, keyID int64, ageDays int) *service.APIKey {
	return &service.APIKey{
		ID:        keyID,
		UserID:    userID,
		Key:       fmt.Sprintf("sk-%04d-secret", keyID),
		CreatedAt: sharingTestWindow.Add(-time.Duration(ageDays) * 24 * time.Hour),
	}
}

// TestIPKeySharingPeakUsers is the acceptance case for the boss report that
// distinct_users: 4 read like "only 4 keys were scanned": the first-seen count
// must stay frozen while the peak follows the real burst.
func TestIPKeySharingPeakUsers(t *testing.T) {
	ctx := context.Background()

	t.Run("peak grows from 4 to 6 while first seen stays 4", func(t *testing.T) {
		detector, settings, _, advance := sharingAcceptanceFixture(t, 4, 5)
		ip := "203.0.113.7"

		var hit IPKeySharingHit
		var ok bool
		for userID := int64(101); userID <= 104; userID++ {
			hit, ok = detector.Observe(ctx, ip, userID, sharingAcceptanceKey(userID, userID, 40))
			require.Equal(t, userID == 104, ok, "the fourth distinct user crosses the threshold")
		}
		require.Equal(t, 4, hit.DistinctUsers)
		added, err := settings.UpsertGatewayIPNoticeAutoEntry(ctx, hit.AutoEntry())
		require.NoError(t, err)
		require.True(t, added)

		stored := settings.GatewayIPNoticeSnapshot().AutoEntries
		require.Len(t, stored, 1)
		require.Equal(t, 4, stored[0].FirstSeenUsers)
		require.Equal(t, 4, stored[0].PeakUsers)

		// The 5th user: the peak moves, the first-seen snapshot does not. The
		// write throttle is 30s, so the clock advances just past it while staying
		// inside the five-minute window.
		advance(31 * time.Second)
		hit, ok = detector.Observe(ctx, ip, 105, sharingAcceptanceKey(105, 105, 40))
		require.True(t, ok)
		require.Equal(t, 5, hit.PeakUsers)
		added, err = settings.UpsertGatewayIPNoticeAutoEntry(ctx, hit.AutoEntry())
		require.NoError(t, err)
		require.False(t, added, "an existing entry is merged, not duplicated")

		advance(31 * time.Second)
		hit, ok = detector.Observe(ctx, ip, 106, sharingAcceptanceKey(106, 106, 40))
		require.True(t, ok)
		require.Equal(t, 6, hit.PeakUsers)
		_, err = settings.UpsertGatewayIPNoticeAutoEntry(ctx, hit.AutoEntry())
		require.NoError(t, err)

		stored = settings.GatewayIPNoticeSnapshot().AutoEntries
		require.Len(t, stored, 1)
		require.Equal(t, 4, stored[0].FirstSeenUsers, "first_seen_users must stay at the crossing value")
		require.Equal(t, 6, stored[0].PeakUsers, "peak_users must reach the real burst size")
		require.Equal(t, 6, stored[0].DistinctUsers, "distinct_users keeps the largest observed count")
	})

	t.Run("a new window restarts the peak", func(t *testing.T) {
		detector, settings, _, advance := sharingAcceptanceFixture(t, 4, 5)
		ip := "207.32.218.140"

		for userID := int64(201); userID <= 204; userID++ {
			hit, ok := detector.Observe(ctx, ip, userID, sharingAcceptanceKey(userID, userID, 40))
			if ok {
				_, err := settings.UpsertGatewayIPNoticeAutoEntry(ctx, hit.AutoEntry())
				require.NoError(t, err)
			}
		}
		advance(31 * time.Second)
		hit, ok := detector.Observe(ctx, ip, 205, sharingAcceptanceKey(205, 205, 40))
		require.True(t, ok)
		_, err := settings.UpsertGatewayIPNoticeAutoEntry(ctx, hit.AutoEntry())
		require.NoError(t, err)
		require.Equal(t, 5, settings.GatewayIPNoticeSnapshot().AutoEntries[0].PeakUsers)

		// More than one full window later the old users are outside the window,
		// so the count restarts from the new arrivals only.
		advance(6 * time.Minute)
		for userID := int64(301); userID <= 304; userID++ {
			hit, ok = detector.Observe(ctx, ip, userID, sharingAcceptanceKey(userID, userID, 40))
			if ok {
				_, err := settings.UpsertGatewayIPNoticeAutoEntry(ctx, hit.AutoEntry())
				require.NoError(t, err)
			}
		}
		stored := settings.GatewayIPNoticeSnapshot().AutoEntries
		require.Len(t, stored, 1)
		require.Equal(t, 4, stored[0].PeakUsers, "the peak restarts with the new burst")
		require.Equal(t, 4, stored[0].FirstSeenUsers, "the very first snapshot is still the original one")
	})
}

// TestIPKeySharingTotalKeys is the acceptance case for "record how many keys this
// IP called with in total": ten distinct keys inside the retention period, with
// repeats of the same key counted once.
func TestIPKeySharingTotalKeys(t *testing.T) {
	ctx := context.Background()
	// A one-hour decision window keeps every observation inside it while the
	// clock advances past the write throttle; the distinct keys are what this
	// test is about.
	detector, settings, client, advance := sharingAcceptanceFixture(t, 2, 60)
	ip := "207.32.218.141"

	// Users alternate so the two-user threshold stays crossed for the whole run.
	// The first observation only brings one distinct user, so it is below the
	// threshold; from the second on every new key raises the total.
	for i := range 10 {
		userID := int64(1000 + i%2)
		keyID := int64(2000 + i)
		hit, ok := detector.Observe(ctx, ip, userID, sharingAcceptanceKey(userID, keyID, 40))
		require.Equal(t, i >= 1, ok, "hit %d", i)
		if i >= 1 {
			require.Equal(t, i+1, hit.TotalKeys, "each new key raises the distinct-key total")
			_, err := settings.UpsertGatewayIPNoticeAutoEntry(ctx, hit.AutoEntry())
			require.NoError(t, err)
		}
		advance(31 * time.Second)
	}

	stored := settings.GatewayIPNoticeSnapshot().AutoEntries
	require.Len(t, stored, 1)
	require.Equal(t, 10, stored[0].TotalKeys, "ten distinct keys are ten, not four")
	require.EqualValues(t, 10, client.SCard(ctx, ipKeySharingKeysPrefix+ip).Val())
	require.Greater(t, client.TTL(ctx, ipKeySharingKeysPrefix+ip).Val(), 23*time.Hour,
		"the distinct-key set lives far longer than the five-minute window")

	// Replaying the same key must not inflate the total.
	for range 5 {
		hit, ok := detector.Observe(ctx, ip, 1000, sharingAcceptanceKey(1000, 2009, 40))
		require.True(t, ok)
		require.Equal(t, 10, hit.TotalKeys, "the same key is only counted once")
		_, err := settings.UpsertGatewayIPNoticeAutoEntry(ctx, hit.AutoEntry())
		require.NoError(t, err)
		advance(31 * time.Second)
	}
	require.Equal(t, 10, settings.GatewayIPNoticeSnapshot().AutoEntries[0].TotalKeys)
}

// TestIPKeySharingExcludedSources is the acceptance case for "make sure this IP is
// not a shared Cloudflare range", plus the private-network guard: an excluded
// source must not count, must not be added and must not reach the key-disable
// path, so it must leave no Redis state at all.
func TestIPKeySharingExcludedSources(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		ip   string
	}{
		{name: "cloudflare ipv4 edge", ip: "104.16.0.1"},
		{name: "cloudflare ipv6 edge", ip: "2606:4700::1"},
		{name: "rfc1918 ten", ip: "10.1.2.3"},
		{name: "rfc1918 172.16", ip: "172.16.5.5"},
		{name: "rfc1918 192.168", ip: "192.168.1.9"},
		{name: "loopback", ip: "127.0.0.1"},
		{name: "loopback ipv6", ip: "::1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			detector, settings, client, _ := sharingAcceptanceFixture(t, 3, 5)
			for userID := int64(401); userID <= 404; userID++ {
				hit, ok := detector.Observe(ctx, tc.ip, userID, sharingAcceptanceKey(userID, userID, 40))
				require.False(t, ok, "%s must never produce a sharing hit", tc.ip)
				require.Zero(t, hit.DistinctUsers)
			}
			require.Empty(t, settings.GatewayIPNoticeSnapshot().AutoEntries, "%s must never be added", tc.ip)
			require.Empty(t, client.Keys(ctx, "*").Val(), "%s must not write any Redis state", tc.ip)
		})
	}
}

// TestIPKeySharingKeyAgeGate is the acceptance case for "make sure these keys were
// leaked a month ago": 29 days is too young to count, 31 days counts.
func TestIPKeySharingKeyAgeGate(t *testing.T) {
	ctx := context.Background()

	t.Run("29 day old keys do not count", func(t *testing.T) {
		detector, settings, client, _ := sharingAcceptanceFixture(t, 2, 5)
		ip := "207.32.218.142"
		for userID := int64(501); userID <= 503; userID++ {
			_, ok := detector.Observe(ctx, ip, userID, sharingAcceptanceKey(userID, userID, 29))
			require.False(t, ok, "a 29-day-old key is younger than the 30-day gate")
		}
		require.Empty(t, settings.GatewayIPNoticeSnapshot().AutoEntries)
		require.Empty(t, client.Keys(ctx, "*").Val(), "a too-young key must be filtered before any Redis write")
	})

	t.Run("31 day old keys do count", func(t *testing.T) {
		detector, settings, _, _ := sharingAcceptanceFixture(t, 2, 5)
		ip := "207.32.218.143"
		_, ok := detector.Observe(ctx, ip, 511, sharingAcceptanceKey(511, 511, 31))
		require.False(t, ok)
		hit, ok := detector.Observe(ctx, ip, 512, sharingAcceptanceKey(512, 512, 31))
		require.True(t, ok, "a 31-day-old key is past the gate")
		require.Equal(t, 2, hit.DistinctUsers)
		added, err := settings.UpsertGatewayIPNoticeAutoEntry(ctx, hit.AutoEntry())
		require.NoError(t, err)
		require.True(t, added)
	})

	t.Run("the age gate is configurable", func(t *testing.T) {
		detector, settings, client, _ := sharingAcceptanceFixture(t, 2, 5)
		require.NoError(t, settings.SetGatewayIPNotice(ctx, ipnotice.Settings{
			Enabled: true, IPs: []string{"198.51.100.7"}, Message: "operator notice", RPM: 100,
			SharingThreshold: 2, SharingWindowMinutes: 5, KeyMinAgeDays: 60,
		}))
		ip := "207.32.218.144"
		for userID := int64(521); userID <= 523; userID++ {
			_, ok := detector.Observe(ctx, ip, userID, sharingAcceptanceKey(userID, userID, 31))
			require.False(t, ok, "with a 60-day gate a 31-day-old key is still too young")
		}
		require.Empty(t, client.Keys(ctx, "*").Val())
	})
}

// TestIPKeySharingManualEntryIsNeverRewritten pins the ownership rule: an
// operator-curated address is not a candidate, so the gateway never creates a
// second automatic entry beside it.
func TestIPKeySharingManualEntryIsNeverRewritten(t *testing.T) {
	ctx := context.Background()
	detector, settings, client, _ := sharingAcceptanceFixture(t, 3, 5)
	manual := "198.51.100.7"
	for userID := int64(601); userID <= 605; userID++ {
		_, ok := detector.Observe(ctx, manual, userID, sharingAcceptanceKey(userID, userID, 40))
		require.False(t, ok, "a hand-written entry is not a detection candidate")
	}
	added, err := settings.UpsertGatewayIPNoticeAutoEntry(ctx, ipnotice.AutoEntry{IP: manual, DistinctUsers: 5, FirstSeenUsers: 5, PeakUsers: 5})
	require.NoError(t, err)
	require.False(t, added)
	policy := settings.GatewayIPNoticeSnapshot()
	require.Empty(t, policy.AutoEntries)
	require.Len(t, policy.IPs, 1)
	require.Empty(t, client.Keys(ctx, "*").Val())
}

// TestIPKeySharingOneUserManyKeysIsNotSharing pins the rule down: the trigger is
// one address reaching DIFFERENT ACCOUNTS, never one account rotating its own
// keys. A single user legitimately holding many keys, or a relay that reissues
// them, must leave no entry and must never reach the disable path — otherwise
// every busy customer would ban themselves.
func TestIPKeySharingOneUserManyKeysIsNotSharing(t *testing.T) {
	ctx := context.Background()
	detector, settings, client, advance := sharingAcceptanceFixture(t, 3, 60)
	ip := "207.32.218.142"
	const soleUser = int64(4242)

	for i := range 20 {
		key := sharingAcceptanceKey(soleUser, int64(9000+i), 40)
		_, ok := detector.Observe(ctx, ip, soleUser, key)
		require.False(t, ok, "one account with %d keys is not sharing", i+1)
		advance(31 * time.Second)
	}

	require.Empty(t, settings.GatewayIPNoticeSnapshot().AutoEntries,
		"a single account must never be listed")
	require.EqualValues(t, 20, client.SCard(ctx, ipKeySharingKeysPrefix+ip).Val(),
		"the keys are still counted as context")
	require.EqualValues(t, 1, client.ZCard(ctx, ipKeySharingUsersPrefix+ip).Val(),
		"only one ACCOUNT was seen")

	// The same account's keys stay uncounted as users even after the real users
	// that would cross the threshold appear: the account set is what triggers.
	var crossing *IPKeySharingHit
	for i := range 3 {
		userID := int64(5000 + i)
		hit, ok := detector.Observe(ctx, ip, userID, sharingAcceptanceKey(userID, int64(9100+i), 40))
		if ok {
			// Whatever trips it must be an account count at or above the
			// threshold, and each hit names the account that produced it.
			require.GreaterOrEqual(t, hit.DistinctUsers, 3)
			require.Len(t, hit.Users, 1)
			require.Equal(t, userID, hit.Users[0].ID)
			if crossing == nil {
				first := hit
				crossing = &first
			}
		}
		advance(31 * time.Second)
	}
	require.NotNil(t, crossing, "different accounts must eventually trip the rule")
}
