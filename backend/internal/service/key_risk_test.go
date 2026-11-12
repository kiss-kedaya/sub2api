//go:build unit

package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
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
	keys *APIKeyService
	repo *riskKeyRepoStub
}

func newKeyRiskFixture(t *testing.T, keys ...*APIKey) *keyRiskFixture {
	t.Helper()
	resetAPIKeyRiskMarkers()
	ConfigureAPIKeyRiskStore(nil)

	repo := newRiskKeyRepo(keys...)
	cfg := &config.Config{}
	cfg.Default.APIKeyPrefix = "sk-"
	svc := NewAPIKeyService(repo, nil, nil, nil, nil, nil, cfg)
	return &keyRiskFixture{keys: svc, repo: repo}
}

func riskInfo(ip string) APIKeyRiskInfo {
	return APIKeyRiskInfo{
		Reason:        KeyRiskReasonAutoIPSharing,
		IP:            ip,
		DistinctUsers: 3,
		WindowMinutes: 5,
		HitAt:         time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC),
	}
}

func sharedEgressKeys() []*APIKey {
	return []*APIKey{
		{ID: 11, UserID: 101, Key: "sk-user-101-abcdef", Name: "k1", Status: StatusActive},
		{ID: 12, UserID: 102, Key: "sk-user-102-abcdef", Name: "k2", Status: StatusActive},
		{ID: 13, UserID: 103, Key: "sk-user-103-abcdef", Name: "k3", Status: StatusActive},
	}
}

// TestKeyRiskDisableIsIdempotent pins the write contract: only active keys are
// flipped, every key is written at most once, and a repeated detection (or a
// second replica) is a no-op.
func TestKeyRiskDisableIsIdempotent(t *testing.T) {
	f := newKeyRiskFixture(t, sharedEgressKeys()...)
	info := riskInfo("203.0.113.10")

	disabled, err := f.keys.DisableKeysForRisk(context.Background(), []int64{11, 12, 13}, info)
	require.NoError(t, err)
	require.ElementsMatch(t, []int64{11, 12, 13}, disabled)
	require.Equal(t, 3, f.repo.updates, "only the three distinct observed keys are disabled")
	for id, key := range f.repo.keys {
		require.Equal(t, StatusAPIKeyInactive, key.Status, "key %d must be disabled", id)
	}
	for _, id := range []int64{11, 12, 13} {
		marker, ok := APIKeyRiskInfoFor(id)
		require.True(t, ok)
		require.Equal(t, KeyRiskReasonAutoIPSharing, marker.Reason)
		require.Equal(t, "203.0.113.10", marker.IP)
		require.Equal(t, 3, marker.DistinctUsers)
		require.Equal(t, 5, marker.WindowMinutes)
		require.False(t, marker.HitAt.IsZero())
	}

	// Re-running the enforcement (e.g. two replicas, retried request) is a no-op.
	again, err := f.keys.DisableKeysForRisk(context.Background(), []int64{11, 12, 13}, info)
	require.NoError(t, err)
	require.Empty(t, again)
	require.Equal(t, 3, f.repo.updates)

	// One missing key never aborts the rest of the batch.
	extra := &APIKey{ID: 14, UserID: 104, Key: "sk-user-104-abcdef", Status: StatusActive}
	f.repo.keys[extra.ID] = extra
	disabled, err = f.keys.DisableKeysForRisk(context.Background(), []int64{99, 14}, info)
	require.Error(t, err, "the missing key is reported")
	require.Equal(t, []int64{14}, disabled, "the valid key is still disabled")
	require.Equal(t, 4, f.repo.updates)
}

// TestKeyRiskRotateRestoresKey covers the user-side recovery path: rotating the
// key generates a new credential, reactivates the auto-disabled key and clears
// the risk marker.
func TestKeyRiskRotateRestoresKey(t *testing.T) {
	keys := sharedEgressKeys()
	f := newKeyRiskFixture(t, keys...)

	disabled, err := f.keys.DisableKeysForRisk(context.Background(), []int64{11}, riskInfo("203.0.113.60"))
	require.NoError(t, err)
	require.Equal(t, []int64{11}, disabled)
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

// memoryRiskStore is the in-memory APIKeyRiskStore used to prove the marker is
// shared across a process-cache miss. The Redis implementation is exercised in
// the middleware package, which may import Redis.
type memoryRiskStore struct {
	mu     sync.Mutex
	values map[int64][]byte
}

func (s *memoryRiskStore) Save(_ context.Context, keyID int64, payload []byte, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.values == nil {
		s.values = map[int64][]byte{}
	}
	s.values[keyID] = append([]byte(nil), payload...)
	return nil
}

func (s *memoryRiskStore) Load(_ context.Context, keyID int64) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	payload, ok := s.values[keyID]
	if !ok {
		return nil, errRiskMarkerNotFound
	}
	return payload, nil
}

func (s *memoryRiskStore) Delete(_ context.Context, keyID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.values, keyID)
	return nil
}

var errRiskMarkerNotFound = &riskMarkerNotFoundError{}

type riskMarkerNotFoundError struct{}

func (*riskMarkerNotFoundError) Error() string { return "risk marker not found" }

// TestKeyRiskMarkerSurvivesProcessCacheMiss proves the marker is shared through
// the store, so another replica can still show the "compromised key" hint.
func TestKeyRiskMarkerSurvivesProcessCacheMiss(t *testing.T) {
	store := &memoryRiskStore{}
	ConfigureAPIKeyRiskStore(store)
	t.Cleanup(func() {
		ConfigureAPIKeyRiskStore(nil)
		resetAPIKeyRiskMarkers()
	})

	info := riskInfo("203.0.113.90")
	markAPIKeyRiskDisabled(4242, info)
	require.Contains(t, store.values, int64(4242), "marker must be persisted for other replicas")

	// Drop the local cache: the answer must come from the store.
	resetAPIKeyRiskMarkers()
	got, ok := APIKeyRiskInfoFor(4242)
	require.True(t, ok)
	require.Equal(t, info.IP, got.IP)
	require.Equal(t, info.DistinctUsers, got.DistinctUsers)

	clearAPIKeyRiskDisabled(4242)
	require.NotContains(t, store.values, int64(4242))

	// Without a store the marker is process-local only: it still works until the
	// cache is dropped.
	ConfigureAPIKeyRiskStore(nil)
	markAPIKeyRiskDisabled(5151, info)
	_, ok = APIKeyRiskInfoFor(5151)
	require.True(t, ok)
	resetAPIKeyRiskMarkers()
	_, ok = APIKeyRiskInfoFor(5151)
	require.False(t, ok)
}
