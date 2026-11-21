package cryptopay

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/scanner"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestDepositNetworkStatusContinuousEvidence(t *testing.T) {
	start := time.Date(2026, 11, 21, 2, 0, 0, 0, time.UTC)
	obs := func(round uint64, seconds int, before, after, head uint64, failed, nearHead bool) scanner.DepositScanObservation {
		return scanner.DepositScanObservation{Chain: ChainBSC, Round: round, At: start.Add(time.Duration(seconds) * time.Second),
			ConfirmedHead: head, CursorBefore: before, Cursor: after, CanMeasure: true, ForwardFailed: failed, NearHeadScanned: nearHead, Failed: failed}
	}
	t.Run("fresh consecutive progress and single failure", func(t *testing.T) {
		var s networkScanEvidence
		state, _ := s.observe(obs(1, 0, 100, 105, 105, false, true))
		require.Equal(t, depositStatusUnknown, state)
		state, _ = s.observe(obs(2, 5, 105, 106, 106, false, true))
		require.Equal(t, depositStatusAvailable, state)
		state, _ = s.observe(obs(3, 10, 106, 106, 107, true, false))
		require.Equal(t, depositStatusDelayed, state)
		state, _ = s.observe(obs(4, 15, 106, 107, 108, false, false))
		require.Equal(t, depositStatusDelayed, state)
		state, _ = s.observe(obs(5, 20, 107, 108, 108, false, true))
		require.Equal(t, depositStatusAvailable, state)
		state, _ = s.observe(obs(7, 25, 108, 109, 109, false, true))
		require.Equal(t, depositStatusUnknown, state, "a skipped periodic attempt breaks continuity")
		state, _ = s.observe(obs(8, 30, 88, 99, 111, false, false))
		require.Equal(t, depositStatusUnknown, state, "rewind could be a manual job")
	})
	t.Run("sustained failed coverage with advancing head and no cursor progress", func(t *testing.T) {
		var s networkScanEvidence
		for i, sec := range []int{0, 60, 120, 179, 180} {
			state, proof := s.observe(obs(uint64(i+1), sec, 100, 100, 300+uint64(sec), true, false))
			want := depositStatusDelayed
			if sec == 180 {
				want = depositStatusStalled
				require.Equal(t, start, proof)
			}
			require.Equal(t, want, state, sec)
		}
		state, _ := s.observe(obs(6, 185, 100, 100, 485, true, true))
		require.Equal(t, depositStatusDelayed, state, "near-head scan covers new deposits")
		state, _ = s.observe(obs(7, 190, 100, 110, 490, true, false))
		require.Equal(t, depositStatusDelayed, state, "committed progress ends a stall")
		state, _ = s.observe(obs(8, 310, 110, 111, 491, false, false))
		require.Equal(t, depositStatusUnknown, state, "freshness gap ends evidence")
	})
}

type networkSnapshotMemory struct {
	values map[string]networkStatusSnapshot
	err    error
	writes int
}

func (m *networkSnapshotMemory) load(_ context.Context, _ []string) (map[string]networkStatusSnapshot, error) {
	return m.values, m.err
}

func (m *networkSnapshotMemory) save(_ context.Context, _ string, _ networkStatusSnapshot, _ time.Duration) error {
	m.writes++
	return m.err
}

func TestDepositNetworkStatusFreshSharedPolicy(t *testing.T) {
	const raw = `{"enabled":true,"chains":["bsc","ethereum"]}`
	observed := time.Date(2026, 11, 21, 2, 0, 0, 0, time.UTC)
	policy, ok := decodeNetworkStatusPolicy(raw, observed.Add(-time.Minute))
	require.True(t, ok)
	for _, tc := range []struct {
		name           string
		raw            string
		updatedAt, now time.Time
		fingerprint    string
		state          string
		storeErr       error
		want           string
	}{
		{"fresh available", raw, observed.Add(-time.Minute), observed.Add(time.Second), policy.fingerprint, depositStatusAvailable, nil, depositStatusAvailable},
		{"fresh delayed", raw, observed.Add(-time.Minute), observed, policy.fingerprint, depositStatusDelayed, nil, depositStatusDelayed},
		{"fresh confirmed stalled", raw, observed.Add(-4 * time.Minute), observed, policy.fingerprint, depositStatusStalled, nil, depositStatusStalled},
		{"unproven cached stalled", raw, observed.Add(-time.Minute), observed, policy.fingerprint, depositStatusStalled, nil, depositStatusUnknown},
		{"stale at TTL", raw, observed.Add(-time.Minute), observed.Add(depositNetworkTTL), policy.fingerprint, depositStatusStalled, nil, depositStatusUnknown},
		{"future", raw, observed.Add(-time.Minute), observed.Add(-time.Second), policy.fingerprint, depositStatusAvailable, nil, depositStatusUnknown},
		{"previous config fingerprint", raw, observed.Add(-time.Minute), observed, "old", depositStatusStalled, nil, depositStatusUnknown},
		{"same config after edit", raw, observed.Add(-time.Second), observed, policy.fingerprint, depositStatusStalled, nil, depositStatusUnknown},
		{"cache read fails", raw, observed.Add(-time.Minute), observed, policy.fingerprint, depositStatusAvailable, errors.New("cache unavailable"), depositStatusUnknown},
		{"missing config row", "", time.Time{}, observed, policy.fingerprint, depositStatusAvailable, nil, depositStatusUnknown},
		{"missing explicit channel switch", `{"chains":["bsc"]}`, observed.Add(-time.Minute), observed, policy.fingerprint, depositStatusAvailable, nil, depositStatusUnknown},
		{"old cache cannot pause", raw, observed.Add(-time.Minute), observed, policy.fingerprint, depositStatusPaused, nil, depositStatusUnknown},
		{"explicit site pause despite cache failure", `{"enabled":false}`, observed.Add(-time.Minute), observed, "old", depositStatusAvailable, errors.New("cache unavailable"), depositStatusPaused},
		{"explicit chain removed", `{"enabled":true,"chains":["ethereum"]}`, observed.Add(-time.Minute), observed, "old", depositStatusAvailable, nil, depositStatusPaused},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			q := mock.ExpectQuery("SELECT value, updated_at FROM settings WHERE key").WithArgs(SettingKeyCryptoConfig)
			if tc.raw == "" {
				q.WillReturnError(sql.ErrNoRows)
			} else {
				q.WillReturnRows(sqlmock.NewRows([]string{"value", "updated_at"}).AddRow(tc.raw, tc.updatedAt))
			}
			evidenceAt := observed.Add(-5 * time.Second)
			if tc.name == "fresh confirmed stalled" || tc.name == "stale at TTL" || tc.name == "same config after edit" || tc.name == "previous config fingerprint" {
				evidenceAt = observed.Add(-chainStalledAfter)
			}
			store := &networkSnapshotMemory{err: tc.storeErr, values: map[string]networkStatusSnapshot{
				ChainBSC: {Status: tc.state, ObservedMS: observed.UnixMilli(), EvidenceMS: evidenceAt.UnixMilli(), Fingerprint: tc.fingerprint},
			}}
			s := &Service{db: db, depositNetworkStatus: newDepositNetworkMonitor(store, func() time.Time { return tc.now })}
			report := s.DepositNetworkStatus(t.Context())
			for _, n := range report.Networks {
				if n.Chain == ChainBSC {
					require.Equal(t, tc.want, n.Status)
					if tc.want == depositStatusUnknown {
						require.Nil(t, n.ObservedAt)
						require.Nil(t, n.ExpiresAt)
					} else {
						require.NotNil(t, n.ObservedAt)
						require.NotNil(t, n.ExpiresAt)
						wantObservation := observed
						if tc.want == depositStatusPaused {
							wantObservation = tc.now
						}
						require.Equal(t, wantObservation, *n.ObservedAt)
						require.Equal(t, wantObservation.Add(depositNetworkTTL), *n.ExpiresAt)
					}
				}
			}
			require.Zero(t, store.writes, "a user GET never writes the health cache")
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

type slowNetworkStatusStore struct {
	entered chan string
	release chan struct{}
}

func (s slowNetworkStatusStore) load(context.Context, []string) (map[string]networkStatusSnapshot, error) {
	return nil, nil
}

func (s slowNetworkStatusStore) save(_ context.Context, chain string, _ networkStatusSnapshot, _ time.Duration) error {
	s.entered <- chain
	<-s.release // A slow backend can ignore cancellation; the worker must still move.
	return errors.New("simulated write failure")
}

func TestDepositNetworkStatusSlowCacheDoesNotBlockScanner(t *testing.T) {
	store := slowNetworkStatusStore{entered: make(chan string, 4), release: make(chan struct{})}
	defer close(store.release)
	now := time.Now()
	observer := newDepositNetworkMonitor(store, func() time.Time { return now }).observer("config")
	observed := func(chain string, round uint64) {
		observer(scanner.DepositScanObservation{Chain: chain, At: now, Round: round})
	}
	writeStarted := func() string {
		t.Helper()
		select {
		case chain := <-store.entered:
			return chain
		case <-time.After(time.Second):
			t.Fatal("independent cache write did not start")
			return ""
		}
	}
	initial := make(chan struct{})
	go func() {
		observed(ChainBSC, 1)
		close(initial)
	}()
	require.Equal(t, ChainBSC, writeStarted())
	select {
	case <-initial:
	case <-time.After(time.Second):
		t.Fatal("scan waited for Redis")
	}
	observed(ChainBSC, 2)
	observed(ChainEthereum, 2)
	require.Equal(t, ChainEthereum, writeStarted(), "slow chain must not queue another write or block other chains")
}

func TestDepositNetworkStatusRedisRejectsLateWriterAndExpires(t *testing.T) {
	redisServer := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	store := redisNetworkStatusStore{rdb: rdb}
	newer := networkStatusSnapshot{Status: depositStatusDelayed, ObservedMS: 200}
	older := networkStatusSnapshot{Status: depositStatusStalled, ObservedMS: 100}
	require.NoError(t, store.save(t.Context(), ChainBSC, newer, depositNetworkTTL))
	require.NoError(t, store.save(t.Context(), ChainBSC, older, depositNetworkTTL))
	got, err := store.load(t.Context(), []string{ChainBSC, ChainEthereum})
	require.NoError(t, err)
	require.Equal(t, map[string]networkStatusSnapshot{ChainBSC: newer}, got)
	redisServer.FastForward(depositNetworkTTL)
	got, err = store.load(t.Context(), []string{ChainBSC})
	require.NoError(t, err)
	require.Empty(t, got)
}
