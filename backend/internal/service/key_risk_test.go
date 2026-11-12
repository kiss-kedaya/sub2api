//go:build unit

package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ipnotice"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// riskKeyRepoStub is a two-key-aware APIKeyRepository stub: the delete-test
// stub only ever returns one key, while the risk control has to look up and
// disable several.
type riskKeyRepoStub struct {
	*apiKeyRepoStub
	keys         map[int64]*APIKey
	updates      int
	updateFields []APIKeyUpdateFields
	updateErr    error
}

func newRiskKeyRepo(keys ...*APIKey) *riskKeyRepoStub {
	repo := &riskKeyRepoStub{apiKeyRepoStub: &apiKeyRepoStub{}, keys: map[int64]*APIKey{}}
	for _, key := range keys {
		clone := *key
		repo.keys[key.ID] = &clone
	}
	return repo
}

func (r *riskKeyRepoStub) GetByID(_ context.Context, id int64) (*APIKey, error) {
	key, ok := r.keys[id]
	if !ok {
		return nil, ErrAPIKeyNotFound
	}
	clone := *key
	return &clone, nil
}

func (r *riskKeyRepoStub) Update(_ context.Context, key *APIKey, fields APIKeyUpdateFields) error {
	if r.updateErr != nil {
		return r.updateErr
	}
	r.updates++
	r.updateFields = append(r.updateFields, fields)
	clone := *key
	r.keys[key.ID] = &clone
	return nil
}

func (r *riskKeyRepoStub) ExistsByKey(_ context.Context, key string) (bool, error) {
	for _, item := range r.keys {
		if item.Key == key {
			return true, nil
		}
	}
	return false, nil
}

type keyRiskFixture struct {
	detector *IPKeySharingDetector
	settings *SettingService
	keys     *APIKeyService
	repo     *riskKeyRepoStub
}

func newKeyRiskFixture(t *testing.T, keys ...*APIKey) *keyRiskFixture {
	t.Helper()
	resetAPIKeyRiskMarkers()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	repo := newRiskKeyRepo(keys...)
	cfg := &config.Config{}
	cfg.Default.APIKeyPrefix = "sk-"
	svc := NewAPIKeyService(repo, nil, nil, nil, nil, nil, cfg)

	settings := NewSettingService(&panelRateLimitSettingRepo{values: map[string]string{}}, nil)
	require.NoError(t, settings.SetGatewayIPNotice(context.Background(), ipnotice.Settings{
		Enabled: true,
		IPs:     []string{"198.51.100.7"},
		Message: "operator notice",
		RPM:     100,
	}))
	detector := NewIPKeySharingDetector(settings, client)
	detector.now = func() time.Time { return time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC) }
	return &keyRiskFixture{detector: detector, settings: settings, keys: svc, repo: repo}
}

// observe funnels one authenticated gateway call through the detector, mirroring
// the middleware (userID + keyID + plaintext key).
func (f *keyRiskFixture) observe(ip string, userID, keyID int64) (IPKeySharingHit, bool) {
	return f.detector.Observe(context.Background(), ip, userID, keyID, fmt.Sprintf("sk-%d-%d-abcdef", userID, keyID))
}

// enforce applies what the middleware does with a hit: append the IP, then
// disable the involved keys.
func (f *keyRiskFixture) enforce(t *testing.T, hit IPKeySharingHit) []int64 {
	t.Helper()
	added, err := f.settings.AppendGatewayIPNoticeAutoEntry(context.Background(), hit.AutoEntry())
	require.NoError(t, err)
	require.True(t, added)
	disabled, err := f.keys.DisableKeysForRisk(context.Background(), hit.KeyIDs, APIKeyRiskInfo{
		Reason:        KeyRiskReasonAutoIPSharing,
		IP:            hit.IP,
		DistinctUsers: hit.DistinctUsers,
		WindowMinutes: hit.WindowMinutes,
		HitAt:         hit.FirstSeenAt,
	})
	require.NoError(t, err)
	return disabled
}

func sharedEgressKeys(ip string) []*APIKey {
	return []*APIKey{
		{ID: 11, UserID: 101, Key: "sk-user-101-abcdef", Name: "k1", Status: StatusActive},
		{ID: 12, UserID: 102, Key: "sk-user-102-abcdef", Name: "k2", Status: StatusActive},
		{ID: 13, UserID: 103, Key: "sk-user-103-abcdef", Name: "k3", Status: StatusActive},
	}
}

func datacenterClassifier(ips ...string) datacenterIPClassifier {
	return func(_ context.Context, candidates []string) map[string]bool {
		out := map[string]bool{}
		for _, candidate := range candidates {
			for _, marked := range ips {
				if candidate == marked {
					out[candidate] = true
				}
			}
		}
		return out
	}
}

// TestKeyRiskDatacenterSharedEgressDisablesKeys is the positive acceptance case:
// datacenter IP + 3 distinct users -> IP listed AND the involved keys disabled.
func TestKeyRiskDatacenterSharedEgressDisablesKeys(t *testing.T) {
	ip := "203.0.113.10"
	f := newKeyRiskFixture(t, sharedEgressKeys(ip)...)
	f.detector.classify = datacenterClassifier(ip)

	for _, userID := range []int64{101, 102} {
		_, hit := f.observe(ip, userID, userID-90)
		require.False(t, hit, "below the threshold nothing may happen")
	}
	result, hit := f.observe(ip, 103, 13)
	require.True(t, hit)
	require.Equal(t, 3, result.DistinctUsers)
	require.ElementsMatch(t, []int64{11, 12, 13}, result.KeyIDs)

	disabled := f.enforce(t, result)
	require.ElementsMatch(t, []int64{11, 12, 13}, disabled)
	for id, key := range f.repo.keys {
		require.Equal(t, StatusAPIKeyInactive, key.Status, "key %d must be disabled", id)
	}
	policy := f.settings.GatewayIPNoticeSnapshot()
	require.Len(t, policy.AutoEntries, 1)
	require.Equal(t, ip, policy.AutoEntries[0].IP)
	_, matched := policy.Match(ip)
	require.True(t, matched, "the detected IP must be enforced by the notice policy")
	for _, id := range []int64{11, 12, 13} {
		info, ok := APIKeyRiskInfoFor(id)
		require.True(t, ok)
		require.Equal(t, KeyRiskReasonAutoIPSharing, info.Reason)
		require.Equal(t, ip, info.IP)
		require.Equal(t, 3, info.DistinctUsers)
	}
}

// TestKeyRiskResidentialSharedEgressDoesNothing is the anti-false-positive core:
// an office/home NAT with 3 distinct users must change nothing at all.
func TestKeyRiskResidentialSharedEgressDoesNothing(t *testing.T) {
	ip := "203.0.113.20"
	f := newKeyRiskFixture(t, sharedEgressKeys(ip)...)
	f.detector.classify = func(context.Context, []string) map[string]bool {
		// Residential: the classifier returns an empty set.
		return map[string]bool{}
	}

	for _, userID := range []int64{101, 102, 103} {
		_, hit := f.observe(ip, userID, userID-90)
		require.False(t, hit, "a residential/keyed NAT must never be reported")
	}
	require.Empty(t, f.settings.GatewayIPNoticeSnapshot().AutoEntries)
	require.Zero(t, f.repo.updates, "no key may be touched")
	for _, key := range f.repo.keys {
		require.Equal(t, StatusActive, key.Status)
	}
}

// TestKeyRiskSameUserMultipleKeysDoesNothing: one account rotating its own keys
// is not sharing, even from a datacenter.
func TestKeyRiskSameUserMultipleKeysDoesNothing(t *testing.T) {
	ip := "203.0.113.30"
	keys := []*APIKey{
		{ID: 21, UserID: 777, Key: "sk-777-a", Name: "a", Status: StatusActive},
		{ID: 22, UserID: 777, Key: "sk-777-b", Name: "b", Status: StatusActive},
		{ID: 23, UserID: 777, Key: "sk-777-c", Name: "c", Status: StatusActive},
	}
	f := newKeyRiskFixture(t, keys...)
	f.detector.classify = datacenterClassifier(ip)

	for _, keyID := range []int64{21, 22, 23} {
		_, hit := f.observe(ip, 777, keyID)
		require.False(t, hit)
	}
	require.Empty(t, f.settings.GatewayIPNoticeSnapshot().AutoEntries)
	require.Zero(t, f.repo.updates)
}

// TestKeyRiskClassifierFailureFailsOpen: a failing/timed-out classification is
// "unknown" and must not disable or list anything.
func TestKeyRiskClassifierFailureFailsOpen(t *testing.T) {
	ip := "203.0.113.40"
	f := newKeyRiskFixture(t, sharedEgressKeys(ip)...)
	f.detector.classify = func(context.Context, []string) map[string]bool { return nil }

	for _, userID := range []int64{101, 102, 103} {
		_, hit := f.observe(ip, userID, userID-90)
		require.False(t, hit)
	}
	require.Empty(t, f.settings.GatewayIPNoticeSnapshot().AutoEntries)
	require.Zero(t, f.repo.updates)
}

// TestKeyRiskMarkerSurvivesProcessCacheMiss proves the marker is shared through
// Redis, so another replica can still show the "compromised key" hint.
func TestKeyRiskMarkerSurvivesProcessCacheMiss(t *testing.T) {
	resetAPIKeyRiskMarkers()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() {
		ConfigureAPIKeyRiskStore(nil)
		resetAPIKeyRiskMarkers()
		_ = client.Close()
	})
	ConfigureAPIKeyRiskStore(client)

	info := APIKeyRiskInfo{
		Reason:        KeyRiskReasonAutoIPSharing,
		IP:            "203.0.113.90",
		DistinctUsers: 4,
		WindowMinutes: 10,
		HitAt:         time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC),
	}
	markAPIKeyRiskDisabled(4242, info)
	require.True(t, server.Exists("gateway_key_risk:4242"), "marker must be persisted for other replicas")

	// Drop the local cache: the answer must come from Redis.
	resetAPIKeyRiskMarkers()
	got, ok := APIKeyRiskInfoFor(4242)
	require.True(t, ok)
	require.Equal(t, info.IP, got.IP)
	require.Equal(t, info.DistinctUsers, got.DistinctUsers)

	clearAPIKeyRiskDisabled(4242)
	require.False(t, server.Exists("gateway_key_risk:4242"))
}

// TestKeyRiskDisableIsIdempotent: a repeated detection never writes the status
// twice, and an already-inactive key is left alone.
func TestKeyRiskDisableIsIdempotent(t *testing.T) {
	ip := "203.0.113.50"
	f := newKeyRiskFixture(t, sharedEgressKeys(ip)...)
	f.detector.classify = datacenterClassifier(ip)

	var result IPKeySharingHit
	hit := false
	for _, userID := range []int64{101, 102, 103} {
		result, hit = f.observe(ip, userID, userID-90)
		if userID != 103 {
			require.False(t, hit)
		}
	}
	require.True(t, hit)
	require.Len(t, f.enforce(t, result), 3)
	require.Equal(t, 3, f.repo.updates, "only the three distinct observed keys are disabled")
	require.Len(t, f.settings.GatewayIPNoticeSnapshot().AutoEntries, 1)

	_, repeated := f.observe(ip, 104, 14)
	require.False(t, repeated, "the window is already reported exactly once")

	// Re-running the enforcement (e.g. two replicas, retried request) is a no-op.
	again, err := f.keys.DisableKeysForRisk(context.Background(), []int64{11, 12, 13}, APIKeyRiskInfo{IP: ip})
	require.NoError(t, err)
	require.Empty(t, again)
	require.Equal(t, 3, f.repo.updates)
}

// TestKeyRiskRotateRestoresKey covers the user-side recovery path: rotating the
// key generates a new credential and reactivates the auto-disabled key.
func TestKeyRiskRotateRestoresKey(t *testing.T) {
	ip := "203.0.113.60"
	keys := sharedEgressKeys(ip)
	f := newKeyRiskFixture(t, keys...)
	f.detector.classify = datacenterClassifier(ip)

	var result IPKeySharingHit
	hit := false
	for _, userID := range []int64{101, 102, 103} {
		result, hit = f.observe(ip, userID, userID-90)
	}
	require.True(t, hit)
	f.enforce(t, result)
	require.Equal(t, StatusAPIKeyInactive, f.repo.keys[11].Status)
	_, marked := APIKeyRiskInfoFor(11)
	require.True(t, marked)

	rotated, err := f.keys.Update(context.Background(), 11, 101, UpdateAPIKeyRequest{RegenerateKey: true})
	require.NoError(t, err)
	require.Equal(t, StatusActive, rotated.Status)
	require.NotEqual(t, "sk-user-101-abcdef", rotated.Key)
	require.NotEmpty(t, rotated.Key)
	require.Equal(t, rotated.Key, f.repo.keys[11].Key, "the new credential must be persisted")
	_, marked = APIKeyRiskInfoFor(11)
	require.False(t, marked, "rotation clears the risk marker")
}
