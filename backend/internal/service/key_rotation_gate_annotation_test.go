package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAssessKeyRotationMatrix(t *testing.T) {
	cutoff := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	old := cutoff.Add(-24 * time.Hour)
	rotatedBefore := cutoff.Add(-time.Hour)
	rotatedAfter := cutoff.Add(time.Hour)
	pendingReasons := []string{KeyRotationKeyReasonCreatedBeforeCutoff, KeyRotationKeyReasonNoIPAllowlist}

	cases := []struct {
		name        string
		key         APIKey
		enabled     bool
		wantPending bool
		wantReasons []string
	}{
		{
			name:        "old active key without an allowlist is pending",
			key:         APIKey{ID: 1, Status: StatusAPIKeyActive, CreatedAt: old},
			enabled:     true,
			wantPending: true,
			wantReasons: pendingReasons,
		},
		{
			name:    "key created exactly at the cutoff passes",
			key:     APIKey{ID: 2, Status: StatusAPIKeyActive, CreatedAt: cutoff},
			enabled: true,
		},
		{
			name:    "new key passes",
			key:     APIKey{ID: 3, Status: StatusAPIKeyActive, CreatedAt: cutoff.Add(time.Hour)},
			enabled: true,
		},
		{
			name:    "old key with a non-empty allowlist passes",
			key:     APIKey{ID: 4, Status: StatusAPIKeyActive, CreatedAt: old, IPWhitelist: []string{"203.0.113.7"}},
			enabled: true,
		},
		{
			name:        "blank allowlist entries do not count",
			key:         APIKey{ID: 5, Status: StatusAPIKeyActive, CreatedAt: old, IPWhitelist: []string{"   "}},
			enabled:     true,
			wantPending: true,
			wantReasons: pendingReasons,
		},
		{
			name:    "rotation after the cutoff passes",
			key:     APIKey{ID: 6, Status: StatusAPIKeyActive, CreatedAt: old, KeyRotatedAt: &rotatedAfter},
			enabled: true,
		},
		{
			name:        "rotation before the cutoff is still pending",
			key:         APIKey{ID: 7, Status: StatusAPIKeyActive, CreatedAt: old, KeyRotatedAt: &rotatedBefore},
			enabled:     true,
			wantPending: true,
			wantReasons: pendingReasons,
		},
		{
			name:    "disabled key does not participate",
			key:     APIKey{ID: 8, Status: StatusAPIKeyInactive, CreatedAt: old},
			enabled: true,
		},
		{
			name:    "expired/other non-active keys do not participate",
			key:     APIKey{ID: 9, Status: StatusAPIKeyExpired, CreatedAt: old},
			enabled: true,
		},
		{
			name:    "gate disabled turns every key compliant",
			key:     APIKey{ID: 10, Status: StatusAPIKeyActive, CreatedAt: old},
			enabled: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := AssessKeyRotation(tc.key, tc.enabled, cutoff)
			require.Equal(t, tc.wantPending, got.NeedsRotation)
			require.Equal(t, tc.wantReasons, got.Reasons)
		})
	}
}

// The per-key verdict and the gate counter must never drift apart: they share
// IsKeyRotationCompliant, and this pins the two entry points together.
func TestAssessKeyRotationAgreesWithGate(t *testing.T) {
	cutoff := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	old := cutoff.Add(-time.Hour)
	keys := []APIKey{
		{ID: 1, Status: StatusAPIKeyActive, CreatedAt: old},
		{ID: 2, Status: StatusAPIKeyActive, CreatedAt: cutoff.Add(time.Hour)},
		{ID: 3, Status: StatusAPIKeyActive, CreatedAt: old, IPWhitelist: []string{"198.51.100.9"}},
		{ID: 4, Status: StatusAPIKeyActive, CreatedAt: old, IPWhitelist: []string{}},
	}
	gate := NewKeyRotationGateService(&keyRotationListerStub{keys: keys}, keyRotationTestConfig(t, true, cutoff), nil)
	status, err := gate.Evaluate(context.Background(), 7)
	require.NoError(t, err)
	require.True(t, status.Required)

	pending := 0
	for i := range keys {
		if AssessKeyRotation(keys[i], true, cutoff).NeedsRotation {
			pending++
		}
	}
	require.Equal(t, status.PendingKeyCount, pending)
	require.Equal(t, 2, pending)
}

func TestAPIKeyServiceAnnotateKeyRotations(t *testing.T) {
	cutoff := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	old := cutoff.Add(-24 * time.Hour)
	svc := &APIKeyService{cfg: keyRotationTestConfig(t, true, cutoff)}

	keys := []APIKey{
		{ID: 1, Status: StatusAPIKeyActive, CreatedAt: old},
		{ID: 2, Status: StatusAPIKeyActive, CreatedAt: cutoff.Add(time.Hour)},
		{ID: 3, Status: StatusAPIKeyActive, CreatedAt: old, IPWhitelist: []string{"203.0.113.7"}},
		{ID: 4, Status: StatusAPIKeyInactive, CreatedAt: old},
	}
	svc.AnnotateKeyRotations(keys)

	require.True(t, keys[0].NeedsRotation)
	require.Equal(t, []string{KeyRotationKeyReasonCreatedBeforeCutoff, KeyRotationKeyReasonNoIPAllowlist}, keys[0].RotationReasons)
	require.False(t, keys[1].NeedsRotation)
	require.Empty(t, keys[1].RotationReasons)
	require.False(t, keys[2].NeedsRotation)
	require.False(t, keys[3].NeedsRotation, "a disabled key must not be badged as pending")

	// A single-key annotation path backs GET /keys/:id.
	single := APIKey{ID: 5, Status: StatusAPIKeyActive, CreatedAt: old}
	svc.AnnotateKeyRotation(&single)
	require.True(t, single.NeedsRotation)

	// The gate switch is honoured here too.
	disabled := &APIKeyService{cfg: keyRotationTestConfig(t, false, cutoff)}
	disabledKeys := []APIKey{{ID: 6, Status: StatusAPIKeyActive, CreatedAt: old}}
	disabled.AnnotateKeyRotations(disabledKeys)
	require.False(t, disabledKeys[0].NeedsRotation)

	// Services built without config (lightweight embedders and tests) leave the
	// read-time fields at their zero value instead of panicking.
	var nilCfg APIKeyService
	nilCfgKeys := []APIKey{{ID: 7, Status: StatusAPIKeyActive, CreatedAt: old}}
	nilCfg.AnnotateKeyRotations(nilCfgKeys)
	require.False(t, nilCfgKeys[0].NeedsRotation)
}
