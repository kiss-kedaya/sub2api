package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// ── stubs ───────────────────────────────────────────────────────────────────

type riskAckSettingsStub struct {
	settings KeyRotationRiskAckSettings
	err      error
}

func (s *riskAckSettingsStub) GetKeyRotationRiskAckSettings(context.Context) (KeyRotationRiskAckSettings, error) {
	if s.err != nil {
		return KeyRotationRiskAckSettings{}, s.err
	}
	return s.settings, nil
}

type riskAckRepoStub struct {
	ack       *KeyRotationRiskAck
	findErr   error
	created   *KeyRotationRiskAck
	createErr error
	createArg *KeyRotationRiskAck
	findCalls int
}

func (r *riskAckRepoStub) Create(_ context.Context, ack *KeyRotationRiskAck) (*KeyRotationRiskAck, error) {
	r.createArg = ack
	if r.createErr != nil {
		return nil, r.createErr
	}
	if r.created != nil {
		return r.created, nil
	}
	copied := *ack
	copied.ID = 1
	return &copied, nil
}

func (r *riskAckRepoStub) FindLatestValid(_ context.Context, _ int64, _ time.Time, _ time.Time) (*KeyRotationRiskAck, error) {
	r.findCalls++
	return r.ack, r.findErr
}

// fixedRiskAck builds a risk-ack service with a frozen clock.
func fixedRiskAck(t *testing.T, repo KeyRotationRiskAckRepository, settings KeyRotationRiskAckSettings, now time.Time) *KeyRotationRiskAckService {
	t.Helper()
	svc := NewKeyRotationRiskAckService(repo, &riskAckSettingsStub{settings: settings})
	svc.now = func() time.Time { return now }
	return svc
}

// ── table-driven gate evaluation ─────────────────────────────────────────────

func TestKeyRotationGateEvaluateWithRiskAck(t *testing.T) {
	cutoff := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	now := cutoff.Add(30 * 24 * time.Hour)
	oldKey := APIKey{ID: 1, CreatedAt: cutoff.Add(-48 * time.Hour)}

	tests := []struct {
		name            string
		riskAckSettings KeyRotationRiskAckSettings
		ack             *KeyRotationRiskAck
		findErr         error
		riskAcksNil     bool
		wantRequired    bool
		wantReason      string
		wantExpiresAt   bool
		wantError       string
		wantFindCalls   int
	}{
		{
			name:            "valid acknowledgement lets the user recharge",
			riskAckSettings: KeyRotationRiskAckSettings{Enabled: true, Hours: 24},
			ack: &KeyRotationRiskAck{
				UserID:         7,
				ExpiresAt:      now.Add(12 * time.Hour),
				CutoffSnapshot: cutoff,
			},
			wantRequired:  false,
			wantReason:    KeyRotationReasonRiskAcknowledged,
			wantExpiresAt: true,
			wantFindCalls: 1,
		},
		{
			name:            "expired acknowledgement requires remediation again",
			riskAckSettings: KeyRotationRiskAckSettings{Enabled: true, Hours: 24},
			ack: &KeyRotationRiskAck{
				UserID:         7,
				ExpiresAt:      now.Add(-time.Minute),
				CutoffSnapshot: cutoff,
			},
			wantRequired:  true,
			wantReason:    KeyRotationReasonRotationRequired,
			wantFindCalls: 1,
		},
		{
			name:            "acknowledgement from a previous cutoff is stale",
			riskAckSettings: KeyRotationRiskAckSettings{Enabled: true, Hours: 24},
			ack: &KeyRotationRiskAck{
				UserID:         7,
				ExpiresAt:      now.Add(12 * time.Hour),
				CutoffSnapshot: cutoff.Add(-time.Hour),
			},
			wantRequired:  true,
			wantReason:    KeyRotationReasonRotationRequired,
			wantFindCalls: 1,
		},
		{
			name:            "feature disabled ignores an otherwise valid acknowledgement",
			riskAckSettings: KeyRotationRiskAckSettings{Enabled: false, Hours: 24},
			ack: &KeyRotationRiskAck{
				UserID:         7,
				ExpiresAt:      now.Add(12 * time.Hour),
				CutoffSnapshot: cutoff,
			},
			wantRequired:  true,
			wantReason:    KeyRotationReasonRotationRequired,
			wantFindCalls: 0,
		},
		{
			name:            "no acknowledgement keeps the gate closed",
			riskAckSettings: KeyRotationRiskAckSettings{Enabled: true, Hours: 24},
			ack:             nil,
			wantRequired:    true,
			wantReason:      KeyRotationReasonRotationRequired,
			wantFindCalls:   1,
		},
		{
			name:            "unwired acknowledgement service keeps the gate closed",
			riskAckSettings: KeyRotationRiskAckSettings{Enabled: true, Hours: 24},
			ack: &KeyRotationRiskAck{
				UserID:         7,
				ExpiresAt:      now.Add(12 * time.Hour),
				CutoffSnapshot: cutoff,
			},
			riskAcksNil:   true,
			wantRequired:  true,
			wantReason:    KeyRotationReasonRotationRequired,
			wantFindCalls: 0,
		},
		{
			name:            "acknowledgement lookup failure is reported instead of silently allowing",
			riskAckSettings: KeyRotationRiskAckSettings{Enabled: true, Hours: 24},
			findErr:         errors.New("db is down"),
			wantError:       "db is down",
			wantFindCalls:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lister := &keyRotationListerStub{keys: []APIKey{oldKey}}
			gate := NewKeyRotationGateService(lister, keyRotationTestConfig(t, true, cutoff), nil)
			if !tt.riskAcksNil {
				repo := &riskAckRepoStub{ack: tt.ack, findErr: tt.findErr}
				gate.riskAcks = fixedRiskAck(t, repo, tt.riskAckSettings, now)
				defer func() {
					require.Equal(t, tt.wantFindCalls, repo.findCalls)
				}()
			}

			status, err := gate.Evaluate(context.Background(), 7)
			if tt.wantError != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.wantError)
				require.Nil(t, status)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, status)
			require.Equal(t, tt.wantRequired, status.Required)
			require.Equal(t, tt.wantReason, status.Reason)
			require.Equal(t, tt.riskAckSettings.Enabled, status.RiskAckEnabled)
			require.Equal(t, tt.riskAckSettings.Hours, status.RiskAckHours)
			if tt.wantExpiresAt {
				require.NotNil(t, status.RiskAckExpiresAt)
				require.True(t, status.RiskAckExpiresAt.Equal(tt.ack.ExpiresAt))
				require.Empty(t, status.ErrorCode)
				require.Empty(t, status.Message)
			} else {
				require.Nil(t, status.RiskAckExpiresAt)
			}
			if tt.wantRequired {
				require.Equal(t, KeyRotationRequiredCode, status.ErrorCode)
				require.Equal(t, KeyRotationRequiredMessage, status.Message)
			}
		})
	}
}

// 已整改（全部 Key 轮换或白名单）时不应再查风险确认。
func TestKeyRotationGateEvaluateCompliantSkipsRiskAckLookup(t *testing.T) {
	cutoff := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	now := cutoff.Add(30 * 24 * time.Hour)
	repo := &riskAckRepoStub{ack: &KeyRotationRiskAck{
		UserID:         7,
		ExpiresAt:      now.Add(12 * time.Hour),
		CutoffSnapshot: cutoff,
	}}
	lister := &keyRotationListerStub{keys: []APIKey{{ID: 1, CreatedAt: cutoff.Add(time.Hour)}}}
	gate := NewKeyRotationGateService(lister, keyRotationTestConfig(t, true, cutoff), nil)
	gate.riskAcks = fixedRiskAck(t, repo, KeyRotationRiskAckSettings{Enabled: true, Hours: 24}, now)

	status, err := gate.Evaluate(context.Background(), 7)
	require.NoError(t, err)
	require.False(t, status.Required)
	require.Equal(t, KeyRotationReasonAllKeysRotated, status.Reason)
	require.Nil(t, status.RiskAckExpiresAt)
	require.Equal(t, 0, repo.findCalls)
}

// 门禁本身关闭时，状态接口仍应返回风险确认功能位（前端据此决定是否展示按钮），
// 但不再读 Key 列表。
func TestKeyRotationGateEvaluateDisabledStillReportsRiskAckSettings(t *testing.T) {
	cutoff := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	lister := &keyRotationListerStub{}
	gate := NewKeyRotationGateService(lister, keyRotationTestConfig(t, false, cutoff), nil)
	gate.riskAcks = fixedRiskAck(t, &riskAckRepoStub{}, KeyRotationRiskAckSettings{Enabled: true, Hours: 8}, cutoff)

	status, err := gate.Evaluate(context.Background(), 7)
	require.NoError(t, err)
	require.Equal(t, KeyRotationReasonDisabled, status.Reason)
	require.True(t, status.RiskAckEnabled)
	require.Equal(t, 8, status.RiskAckHours)
	require.Equal(t, 0, lister.calls)
}

// ── HasValidRiskAck / Acknowledge ───────────────────────────────────────────

func TestHasValidRiskAck(t *testing.T) {
	cutoff := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	now := cutoff.Add(30 * 24 * time.Hour)

	tests := []struct {
		name   string
		ack    *KeyRotationRiskAck
		err    error
		expect bool
	}{
		{
			name:   "valid",
			ack:    &KeyRotationRiskAck{ExpiresAt: now.Add(time.Hour), CutoffSnapshot: cutoff},
			expect: true,
		},
		{
			name:   "expired",
			ack:    &KeyRotationRiskAck{ExpiresAt: now, CutoffSnapshot: cutoff},
			expect: false,
		},
		{
			name:   "cutoff moved",
			ack:    &KeyRotationRiskAck{ExpiresAt: now.Add(time.Hour), CutoffSnapshot: cutoff.Add(-time.Hour)},
			expect: false,
		},
		{
			name:   "absent",
			ack:    nil,
			expect: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := fixedRiskAck(t, &riskAckRepoStub{ack: tt.ack, findErr: tt.err},
				KeyRotationRiskAckSettings{Enabled: true, Hours: 24}, now)
			ack, err := svc.HasValidRiskAck(context.Background(), 7, cutoff)
			require.NoError(t, err)
			if tt.expect {
				require.NotNil(t, ack)
			} else {
				require.Nil(t, ack)
			}
		})
	}
}

func TestAcknowledgeRisk(t *testing.T) {
	cutoff := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	now := cutoff.Add(30 * 24 * time.Hour)

	t.Run("writes an expiring acknowledgement bound to the current cutoff", func(t *testing.T) {
		repo := &riskAckRepoStub{}
		svc := fixedRiskAck(t, repo, KeyRotationRiskAckSettings{Enabled: true, Hours: 6}, now)

		ack, err := svc.Acknowledge(context.Background(), 7, cutoff, "203.0.113.9", "test-agent")
		require.NoError(t, err)
		require.NotNil(t, ack)
		require.EqualValues(t, 7, ack.UserID)
		require.True(t, ack.ExpiresAt.Equal(now.Add(6*time.Hour)))
		require.True(t, ack.CutoffSnapshot.Equal(cutoff))
		require.Equal(t, "203.0.113.9", ack.ClientIP)
		require.Equal(t, "test-agent", ack.UserAgent)
		require.NotNil(t, repo.createArg)
	})

	t.Run("rejects when the feature is disabled", func(t *testing.T) {
		svc := fixedRiskAck(t, &riskAckRepoStub{}, KeyRotationRiskAckSettings{Enabled: false, Hours: 24}, now)
		_, err := svc.Acknowledge(context.Background(), 7, cutoff, "", "")
		require.ErrorIs(t, err, ErrKeyRotationRiskAckDisabled)
	})

	t.Run("propagates repository failure", func(t *testing.T) {
		svc := fixedRiskAck(t, &riskAckRepoStub{createErr: errors.New("insert failed")},
			KeyRotationRiskAckSettings{Enabled: true, Hours: 24}, now)
		_, err := svc.Acknowledge(context.Background(), 7, cutoff, "", "")
		require.Error(t, err)
		require.Contains(t, err.Error(), "insert failed")
	})
}

// ── settings ────────────────────────────────────────────────────────────────

func TestNormalizeKeyRotationRiskAckSettings(t *testing.T) {
	tests := []struct {
		name    string
		in      KeyRotationRiskAckSettings
		want    KeyRotationRiskAckSettings
		wantErr bool
	}{
		{name: "defaults hours when omitted", in: KeyRotationRiskAckSettings{Enabled: true}, want: KeyRotationRiskAckSettings{Enabled: true, Hours: 24}},
		{name: "keeps enabled false", in: KeyRotationRiskAckSettings{Enabled: false, Hours: 1}, want: KeyRotationRiskAckSettings{Enabled: false, Hours: 1}},
		{name: "max allowed", in: KeyRotationRiskAckSettings{Enabled: true, Hours: 720}, want: KeyRotationRiskAckSettings{Enabled: true, Hours: 720}},
		{name: "negative hours", in: KeyRotationRiskAckSettings{Enabled: true, Hours: -1}, wantErr: true},
		{name: "too long", in: KeyRotationRiskAckSettings{Enabled: true, Hours: 721}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeKeyRotationRiskAckSettings(tt.in)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}
