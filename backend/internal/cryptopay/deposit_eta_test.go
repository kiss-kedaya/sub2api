package cryptopay

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/scanner"
	"github.com/stretchr/testify/require"
)

// The published ETA has exactly one definition; these cases pin the formula and
// the clamp boundaries so a UI change cannot silently invent a different one.
func TestDepositETAFormula(t *testing.T) {
	int64Ptr := func(v int64) *int64 { return &v }
	for _, tc := range []struct {
		name          string
		chain         string
		confirmations int64
		instant       bool
		backlog       *int64
		want          *int64
	}{
		{"instant, caught up pays one scan period", ChainBSC, 3, true, int64Ptr(0), int64Ptr(5)},
		{"instant, backlog adds chain time", ChainBSC, 3, true, int64Ptr(10), int64Ptr(15)},
		{"final BSC needs depth × 3s", ChainBSC, 3, false, int64Ptr(0), int64Ptr(14)},
		{"final Ethereum needs depth × 12s", ChainEthereum, 64, false, int64Ptr(0), int64Ptr(773)},
		{"instant Ethereum ignores depth", ChainEthereum, 64, true, int64Ptr(0), int64Ptr(5)},
		{"floor is five seconds", "unknown-chain", 0, true, int64Ptr(0), int64Ptr(5)},
		{"ceiling is thirty minutes", ChainEthereum, 1000, false, int64Ptr(0), int64Ptr(1800)},
		{"no backlog measurement means null", ChainBSC, 3, true, nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := depositETA(tc.chain, tc.confirmations, tc.instant, tc.backlog)
			if tc.want == nil {
				require.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			require.Equal(t, *tc.want, *got)
		})
	}
}

// Backlog is measured only from evidence this pass can prove; a missing cursor,
// an unmeasurable chain or a rewind must not produce a fabricated number.
func TestDepositBacklogSecondsRequiresEvidence(t *testing.T) {
	cursor := uint64(10_000)
	base := scanner.DepositScanObservation{Chain: ChainBSC, ConfirmedHead: cursor + 100, CursorBefore: cursor, Cursor: cursor, CanMeasure: true}
	seconds, ok := depositBacklogSeconds(base)
	require.True(t, ok)
	require.Equal(t, int64(300), seconds, "100 BSC blocks at 3s")

	for name, observation := range map[string]scanner.DepositScanObservation{
		"unmeasurable": {Chain: ChainBSC, ConfirmedHead: cursor + 100, Cursor: cursor},
		"no cursor":    {Chain: ChainBSC, ConfirmedHead: cursor + 100, CanMeasure: true},
		"no head":      {Chain: ChainBSC, Cursor: cursor, CanMeasure: true},
		"rewind":       {Chain: ChainBSC, ConfirmedHead: cursor, Cursor: cursor + 100, CanMeasure: true},
	} {
		_, ok := depositBacklogSeconds(observation)
		require.False(t, ok, name)
	}
}

// The network-status row carries instant_credit, required_confirmations and the
// ETA derived from the same fresh snapshot the status uses.
func TestDepositNetworkStatusPublishesETA(t *testing.T) {
	int64Ptr := func(v int64) *int64 { return &v }
	observed := time.Date(2026, 11, 22, 3, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name         string
		raw          string
		backlogKnown bool
		backlogSec   int64
		wantEta      *int64
		instant      bool
		wantRequired int64
	}{
		{
			name:         "instant network pays backlog plus one pass",
			raw:          `{"enabled":true,"chains":["bsc"]}`,
			backlogKnown: true, backlogSec: 10, instant: true, wantEta: int64Ptr(15), wantRequired: 3,
		},
		{
			name:         "stored switch off falls back to confirmation depth",
			raw:          `{"enabled":true,"chains":["bsc"],"instant_credit_enabled":false}`,
			backlogKnown: true, backlogSec: 10, instant: false, wantEta: int64Ptr(24), wantRequired: 3,
		},
		{
			name:         "missing backlog measurement publishes null ETA",
			raw:          `{"enabled":true,"chains":["bsc"]}`,
			backlogKnown: false, instant: true, wantEta: nil, wantRequired: 3,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policy, ok := decodeNetworkStatusPolicy(tc.raw, observed.Add(-time.Minute))
			require.True(t, ok)
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			mock.ExpectQuery("SELECT value, updated_at FROM settings WHERE key").
				WithArgs(SettingKeyCryptoConfig).
				WillReturnRows(sqlmock.NewRows([]string{"value", "updated_at"}).AddRow(tc.raw, observed.Add(-time.Minute)))
			mock.ExpectClose()
			t.Cleanup(func() {
				require.NoError(t, db.Close())
				require.NoError(t, mock.ExpectationsWereMet())
			})
			store := &networkSnapshotMemory{values: map[string]networkStatusSnapshot{
				ChainBSC: {
					Status: depositStatusAvailable, ObservedMS: observed.UnixMilli(),
					EvidenceMS: observed.Add(-time.Second).UnixMilli(), Fingerprint: policy.fingerprint,
					BacklogSec: tc.backlogSec, BacklogKnown: tc.backlogKnown,
				},
			}}
			s := &Service{db: db, depositNetworkStatus: newDepositNetworkMonitor(store, func() time.Time { return observed.Add(time.Second) })}
			report := s.DepositNetworkStatus(context.Background())
			var row DepositNetworkStatusRow
			for _, candidate := range report.Networks {
				if candidate.Chain == ChainBSC {
					row = candidate
				}
			}
			require.Equal(t, tc.instant, row.InstantCredit)
			require.NotNil(t, row.RequiredConfirmations)
			require.Equal(t, tc.wantRequired, *row.RequiredConfirmations)
			if tc.backlogKnown {
				require.NotNil(t, row.BacklogSeconds)
				require.Equal(t, tc.backlogSec, *row.BacklogSeconds)
			} else {
				require.Nil(t, row.BacklogSeconds)
			}
			if tc.wantEta == nil {
				require.Nil(t, row.EtaSeconds)
			} else {
				require.NotNil(t, row.EtaSeconds)
				require.Equal(t, *tc.wantEta, *row.EtaSeconds)
			}
		})
	}
}

// A paused network keeps its configuration visible but publishes no measurement.
func TestDepositNetworkStatusPausedRowsHaveNoETA(t *testing.T) {
	observed := time.Date(2026, 11, 22, 3, 0, 0, 0, time.UTC)
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	mock.ExpectQuery("SELECT value, updated_at FROM settings WHERE key").
		WithArgs(SettingKeyCryptoConfig).
		WillReturnRows(sqlmock.NewRows([]string{"value", "updated_at"}).AddRow(`{"enabled":false}`, observed.Add(-time.Minute)))
	mock.ExpectClose()
	t.Cleanup(func() {
		require.NoError(t, db.Close())
		require.NoError(t, mock.ExpectationsWereMet())
	})
	s := &Service{db: db, depositNetworkStatus: newDepositNetworkMonitor(&networkSnapshotMemory{}, func() time.Time { return observed })}
	report := s.DepositNetworkStatus(context.Background())
	for _, row := range report.Networks {
		require.Equal(t, depositStatusPaused, row.Status)
		require.Nil(t, row.BacklogSeconds)
		require.Nil(t, row.EtaSeconds)
		require.NotNil(t, row.RequiredConfirmations, "configured depth stays visible while paused")
	}
}
