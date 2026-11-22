package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type keyRotationListerStub struct {
	keys  []APIKey
	err   error
	calls int
}

func (s *keyRotationListerStub) ListActiveKeysByUser(context.Context, int64) ([]APIKey, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.keys, nil
}

func keyRotationTestConfig(t *testing.T, enabled bool, cutoff time.Time) *config.Config {
	t.Helper()
	return &config.Config{Security: config.SecurityConfig{
		KeyRotationGateEnabled: enabled,
		KeyRotationCutoff:      cutoff.Format(time.RFC3339),
	}}
}

func TestKeyRotationGateEvaluateDisabled(t *testing.T) {
	cutoff := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	lister := &keyRotationListerStub{keys: []APIKey{{ID: 1, CreatedAt: cutoff.Add(-30 * 24 * time.Hour)}}}
	gate := NewKeyRotationGateService(lister, keyRotationTestConfig(t, false, cutoff), nil)

	status, err := gate.Evaluate(context.Background(), 7)
	require.NoError(t, err)
	require.False(t, status.Enabled)
	require.False(t, status.Required)
	require.Equal(t, KeyRotationReasonDisabled, status.Reason)
	require.Equal(t, 0, lister.calls, "disabled gate must not query API keys")
}

func TestKeyRotationGateEvaluateNoActiveKeys(t *testing.T) {
	cutoff := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	gate := NewKeyRotationGateService(&keyRotationListerStub{}, keyRotationTestConfig(t, true, cutoff), nil)

	status, err := gate.Evaluate(context.Background(), 7)
	require.NoError(t, err)
	require.False(t, status.Required)
	require.Equal(t, KeyRotationReasonNoActiveKeys, status.Reason)
	require.Equal(t, 0, status.ActiveKeyCount)
	require.Equal(t, KeyRotationRemediationPath, status.RemediationPath)
}

func TestKeyRotationGateEvaluateAllKeysRotated(t *testing.T) {
	cutoff := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	gate := NewKeyRotationGateService(&keyRotationListerStub{keys: []APIKey{
		{ID: 1, CreatedAt: cutoff}, // exactly at the cutoff counts as rotated
		{ID: 2, CreatedAt: cutoff.Add(time.Hour)},
	}}, keyRotationTestConfig(t, true, cutoff), nil)

	status, err := gate.Evaluate(context.Background(), 7)
	require.NoError(t, err)
	require.False(t, status.Required)
	require.Equal(t, KeyRotationReasonAllKeysRotated, status.Reason)
	require.Equal(t, 2, status.ActiveKeyCount)
	require.Equal(t, 2, status.CompliantKeyCount)
	require.Equal(t, 0, status.PendingKeyCount)
}

func TestKeyRotationGateEvaluateAllKeysIPAllowlisted(t *testing.T) {
	cutoff := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	old := cutoff.Add(-30 * 24 * time.Hour)
	gate := NewKeyRotationGateService(&keyRotationListerStub{keys: []APIKey{
		{ID: 1, CreatedAt: old, IPWhitelist: []string{"203.0.113.7"}},
		{ID: 2, CreatedAt: old, IPWhitelist: []string{"10.0.0.0/8"}},
	}}, keyRotationTestConfig(t, true, cutoff), nil)

	status, err := gate.Evaluate(context.Background(), 7)
	require.NoError(t, err)
	require.False(t, status.Required)
	require.Equal(t, KeyRotationReasonAllKeysAllowlist, status.Reason)
	require.Equal(t, 2, status.CompliantKeyCount)
}

func TestKeyRotationGateEvaluateRequiresRotation(t *testing.T) {
	cutoff := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	old := cutoff.Add(-time.Minute)
	gate := NewKeyRotationGateService(&keyRotationListerStub{keys: []APIKey{
		{ID: 1, CreatedAt: old},
		{ID: 2, CreatedAt: old, IPWhitelist: []string{"   "}}, // blank entries are not an allowlist
	}}, keyRotationTestConfig(t, true, cutoff), nil)

	status, err := gate.Evaluate(context.Background(), 7)
	require.NoError(t, err)
	require.True(t, status.Required)
	require.Equal(t, KeyRotationReasonRotationRequired, status.Reason)
	require.Equal(t, KeyRotationRequiredCode, status.ErrorCode)
	require.Equal(t, KeyRotationRequiredMessage, status.Message)
	require.Equal(t, 2, status.ActiveKeyCount)
	require.Equal(t, 0, status.CompliantKeyCount)
	require.Equal(t, 2, status.PendingKeyCount)
}

func TestKeyRotationGateEvaluatePartiallyRotated(t *testing.T) {
	cutoff := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	gate := NewKeyRotationGateService(&keyRotationListerStub{keys: []APIKey{
		{ID: 1, CreatedAt: cutoff.Add(-time.Hour)}, // still old, no allowlist
		{ID: 2, CreatedAt: cutoff.Add(time.Hour)},  // rotated
	}}, keyRotationTestConfig(t, true, cutoff), nil)

	status, err := gate.Evaluate(context.Background(), 7)
	require.NoError(t, err)
	require.True(t, status.Required, "a single old key without an allowlist must keep the gate closed")
	require.Equal(t, 2, status.ActiveKeyCount)
	require.Equal(t, 1, status.CompliantKeyCount)
	require.Equal(t, 1, status.PendingKeyCount)
}

func TestKeyRotationGateEvaluateMixedButSafeKeys(t *testing.T) {
	cutoff := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	// One rotated key and one old-but-IP-restricted key: no key is both old and
	// unprotected, so the user has remediated (the requirement's "either" choice
	// is evaluated per key).
	gate := NewKeyRotationGateService(&keyRotationListerStub{keys: []APIKey{
		{ID: 1, CreatedAt: cutoff.Add(time.Hour)},
		{ID: 2, CreatedAt: cutoff.Add(-time.Hour), IPWhitelist: []string{"192.0.2.10"}},
	}}, keyRotationTestConfig(t, true, cutoff), nil)

	status, err := gate.Evaluate(context.Background(), 7)
	require.NoError(t, err)
	require.False(t, status.Required)
	require.Equal(t, 2, status.CompliantKeyCount)
}

// keyRotationRepoStub satisfies the whole APIKeyRepository through embedding and
// adds the unpaginated listing the gate needs; any unexpected method call panics.
type keyRotationRepoStub struct {
	APIKeyRepository
	keys []APIKey
}

func (s *keyRotationRepoStub) ListAllByUserID(context.Context, int64, APIKeyListFilters) ([]APIKey, error) {
	return s.keys, nil
}

// keyRotationRepoWithoutLister implements APIKeyRepository but not the
// unpaginated listing, exercising the "repository does not support it" branch.
type keyRotationRepoWithoutLister struct {
	APIKeyRepository
}

func TestAPIKeyServiceListActiveKeysByUser(t *testing.T) {
	repo := &keyRotationRepoStub{keys: []APIKey{
		{ID: 1, UserID: 7, Status: StatusAPIKeyActive},
		{ID: 2, UserID: 7, Status: StatusAPIKeyActive},
	}}
	service := &APIKeyService{apiKeyRepo: repo}

	keys, err := service.ListActiveKeysByUser(context.Background(), 7)
	require.NoError(t, err)
	require.Len(t, keys, 2)
}

func TestAPIKeyServiceListActiveKeysByUserRejectsUnsupportedRepository(t *testing.T) {
	service := &APIKeyService{apiKeyRepo: &keyRotationRepoWithoutLister{}}
	_, err := service.ListActiveKeysByUser(context.Background(), 7)
	require.Error(t, err)
}

func TestAPIKeyServiceListActiveKeysByUserWithoutRepository(t *testing.T) {
	service := &APIKeyService{}
	keys, err := service.ListActiveKeysByUser(context.Background(), 7)
	require.NoError(t, err)
	require.Empty(t, keys)
}

func TestKeyRotationGateEvaluateRequireUser(t *testing.T) {
	cutoff := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	gate := NewKeyRotationGateService(&keyRotationListerStub{keys: []APIKey{
		{ID: 1, CreatedAt: cutoff.Add(-time.Hour)},
	}}, keyRotationTestConfig(t, true, cutoff), nil)

	status, blocked, err := gate.RequireUser(context.Background(), 7)
	require.NoError(t, err)
	require.True(t, blocked)
	require.NotNil(t, status)
	require.True(t, status.Required)
}

func TestKeyRotationGateEvaluateListerError(t *testing.T) {
	cutoff := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	lister := &keyRotationListerStub{err: errors.New("db is down")}
	gate := NewKeyRotationGateService(lister, keyRotationTestConfig(t, true, cutoff), nil)

	status, err := gate.Evaluate(context.Background(), 7)
	require.Error(t, err)
	require.Nil(t, status)
	require.Contains(t, err.Error(), "db is down")
}

func TestKeyRotationGateEvaluateNilConfigDefaultsToEnabled(t *testing.T) {
	// nil *config.Config must resolve to the documented defaults: gate on, cutoff
	// 2026-11-21T00:00:00+08:00.
	gate := NewKeyRotationGateService(nil, nil, nil)
	enabled, cutoff := gate.Settings()
	require.True(t, enabled)
	require.True(t, cutoff.Equal(config.DefaultKeyRotationCutoffTime()))

	status, err := gate.Evaluate(context.Background(), 7)
	require.NoError(t, err)
	// No lister wired -> treated as disabled instead of locking every user out.
	require.False(t, status.Required)
	require.Equal(t, KeyRotationReasonDisabled, status.Reason)
}

func TestIsKeyRotationCompliant(t *testing.T) {
	cutoff := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	require.True(t, IsKeyRotationCompliant(APIKey{CreatedAt: cutoff}, cutoff))
	require.True(t, IsKeyRotationCompliant(APIKey{CreatedAt: cutoff.Add(-time.Second), IPWhitelist: []string{"1.2.3.4"}}, cutoff))
	require.False(t, IsKeyRotationCompliant(APIKey{CreatedAt: cutoff.Add(-time.Second)}, cutoff))
	require.False(t, IsKeyRotationCompliant(APIKey{CreatedAt: cutoff.Add(-time.Second), IPWhitelist: []string{}}, cutoff))
}

func TestKeyRotationGateUsesSuccessfulRotationNotUpdateTime(t *testing.T) {
	cutoff := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	old, newTime := cutoff.Add(-time.Hour), cutoff.Add(time.Hour)
	key := APIKey{CreatedAt: old, UpdatedAt: newTime}
	require.False(t, IsKeyRotationCompliant(key, cutoff), "ordinary metadata or usage updates do not rotate a key")
	key.KeyRotatedAt = &old
	require.False(t, IsKeyRotationCompliant(key, cutoff), "rotation before the threshold is not enough")
	key.KeyRotatedAt = &cutoff
	require.True(t, IsKeyRotationCompliant(key, cutoff))
	key.KeyRotatedAt = &newTime
	require.True(t, IsKeyRotationCompliant(key, cutoff))
}
