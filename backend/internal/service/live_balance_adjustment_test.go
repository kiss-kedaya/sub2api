package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type liveBalanceAdjustmentCacheStub struct {
	billingCacheWorkerStub
	result              LiveBalanceResult
	watermarkResults    []LiveBalanceResult
	adjustErr           error
	initializeResult    LiveBalanceResult
	initializeErr       error
	invalidateErr       error
	invalidateCalls     int
	adjustCalls         int
	userID              int64
	eventID             string
	delta               float64
	deltaUnits          int64
	watermark           int64
	predecessor         int64
	watermarkCalls      int
	initializeCalls     int
	initializeBalance   float64
	initializeWatermark int64

	watermarkState      LiveBalanceWatermarkState
	watermarkStateErr   error
	watermarkStateCalls int
	rebuildResult       LiveBalanceResult
	rebuildErr          error
	rebuildCalls        int
	rebuildBalance      float64
	rebuildWatermark    int64
	rebuildExpected     LiveBalanceWatermarkState
}

type liveBalanceSnapshotReaderStub struct {
	snapshot LiveBalanceInitializationSnapshot
	err      error
	calls    int
	userID   int64
}

func (s *liveBalanceSnapshotReaderStub) LoadLiveBalanceInitializationSnapshot(
	_ context.Context,
	userID int64,
) (LiveBalanceInitializationSnapshot, error) {
	s.calls++
	s.userID = userID
	return s.snapshot, s.err
}

type liveBalanceReadCacheStub struct {
	billingCacheWorkerStub
	liveBalance float64
	liveExists  bool
	liveErr     error
	liveCalls   int
	legacyCalls int
}

func (s *liveBalanceReadCacheStub) GetLiveBalance(context.Context, int64) (float64, bool, error) {
	s.liveCalls++
	return s.liveBalance, s.liveExists, s.liveErr
}

func (s *liveBalanceReadCacheStub) GetUserBalance(context.Context, int64) (float64, error) {
	s.legacyCalls++
	return 999, nil
}

func (s *liveBalanceAdjustmentCacheStub) InvalidateUserBalance(context.Context, int64) error {
	s.invalidateCalls++
	return s.invalidateErr
}

func (s *liveBalanceAdjustmentCacheStub) AdjustLiveBalance(_ context.Context, userID int64, eventID string, delta float64) (LiveBalanceResult, error) {
	s.adjustCalls++
	s.userID = userID
	s.eventID = eventID
	s.delta = delta
	return s.result, s.adjustErr
}

func (s *liveBalanceAdjustmentCacheStub) AdjustLiveBalanceAtWatermark(
	_ context.Context,
	userID int64,
	eventID string,
	watermark int64,
	predecessor int64,
	deltaUnits int64,
) (LiveBalanceResult, error) {
	s.watermarkCalls++
	s.userID = userID
	s.eventID = eventID
	s.watermark = watermark
	s.predecessor = predecessor
	s.deltaUnits = deltaUnits
	if len(s.watermarkResults) > 0 {
		result := s.watermarkResults[0]
		s.watermarkResults = s.watermarkResults[1:]
		return result, s.adjustErr
	}
	return s.result, s.adjustErr
}

func (s *liveBalanceAdjustmentCacheStub) AdjustLiveBalanceAtWatermarkBatch(
	_ context.Context, userID int64, events []LiveBalanceAdjustmentEvent,
) (LiveBalanceResult, error) {
	s.watermarkCalls++
	s.userID = userID
	if len(events) > 0 {
		s.watermark = events[len(events)-1].ID
		s.predecessor = events[0].PredecessorID
	}
	if len(s.watermarkResults) > 0 {
		result := s.watermarkResults[0]
		s.watermarkResults = s.watermarkResults[1:]
		return result, s.adjustErr
	}
	return s.result, s.adjustErr
}

func (s *liveBalanceAdjustmentCacheStub) InitializeLiveBalanceAtWatermark(
	_ context.Context,
	_ int64,
	balance float64,
	watermark int64,
) (LiveBalanceResult, error) {
	s.initializeCalls++
	s.initializeBalance = balance
	s.initializeWatermark = watermark
	return s.initializeResult, s.initializeErr
}

func (s *liveBalanceAdjustmentCacheStub) LoadLiveBalanceWatermarkState(
	_ context.Context,
	_ int64,
) (LiveBalanceWatermarkState, error) {
	s.watermarkStateCalls++
	return s.watermarkState, s.watermarkStateErr
}

func (s *liveBalanceAdjustmentCacheStub) RebuildLiveBalanceAtWatermark(
	_ context.Context,
	_ int64,
	expected LiveBalanceWatermarkState,
	balance float64,
	watermark int64,
) (LiveBalanceResult, error) {
	s.rebuildCalls++
	s.rebuildExpected = expected
	s.rebuildBalance = balance
	s.rebuildWatermark = watermark
	return s.rebuildResult, s.rebuildErr
}

func TestApplyExternalBalanceAdjustmentInvalidatesLegacyCacheThenAdjusts(t *testing.T) {
	cache := &liveBalanceAdjustmentCacheStub{result: LiveBalanceResult{Outcome: LiveBalanceOutcomeApplied}}
	service := &BillingCacheService{cache: cache}

	err := service.ApplyExternalBalanceAdjustment(context.Background(), 42, "redeem:7", 3.5)

	require.NoError(t, err)
	require.Equal(t, 1, cache.invalidateCalls)
	require.Equal(t, 1, cache.adjustCalls)
	require.Equal(t, int64(42), cache.userID)
	require.Equal(t, "redeem:7", cache.eventID)
	require.Equal(t, 3.5, cache.delta)
}

func TestApplyExternalBalanceAdjustmentRejectsConflictAndStopsAfterInvalidationFailure(t *testing.T) {
	cache := &liveBalanceAdjustmentCacheStub{result: LiveBalanceResult{Outcome: LiveBalanceOutcomeConflict}}
	service := &BillingCacheService{cache: cache}
	require.ErrorContains(t, service.ApplyExternalBalanceAdjustment(context.Background(), 42, "admin:7", -1), "event conflict")

	cache.invalidateErr = errors.New("redis unavailable")
	cache.adjustCalls = 0
	err := service.ApplyExternalBalanceAdjustment(context.Background(), 42, "admin:8", -1)
	require.ErrorContains(t, err, "invalidate legacy balance cache")
	require.Zero(t, cache.adjustCalls)
}

func TestApplyExternalBalanceAdjustmentTreatsMissingWalletAsSuccess(t *testing.T) {
	cache := &liveBalanceAdjustmentCacheStub{result: LiveBalanceResult{Outcome: LiveBalanceOutcomeNotFound}}
	service := &BillingCacheService{cache: cache}

	require.NoError(t, service.ApplyExternalBalanceAdjustment(context.Background(), 9, "promo:2:9", 1))
}

func TestApplyExternalBalanceOutboxAdjustmentUsesDatabaseWatermark(t *testing.T) {
	cache := &liveBalanceAdjustmentCacheStub{result: LiveBalanceResult{Outcome: LiveBalanceOutcomeApplied}}
	service := &BillingCacheService{cache: cache}
	event := LiveBalanceAdjustmentEvent{ID: 17, UserID: 42, PredecessorID: 11, DeltaUnits: -125000000}

	err := service.ApplyExternalBalanceOutboxAdjustment(context.Background(), event)

	require.NoError(t, err)
	require.Equal(t, 1, cache.invalidateCalls)
	require.Equal(t, 1, cache.watermarkCalls)
	require.Equal(t, "live-balance-outbox:17", cache.eventID)
	require.Equal(t, int64(17), cache.watermark)
	require.Equal(t, int64(11), cache.predecessor)
	require.Equal(t, int64(-125000000), cache.deltaUnits)
}

func TestApplyExternalBalanceOutboxAdjustmentFailsClosedOnWatermarkConflict(t *testing.T) {
	cache := &liveBalanceAdjustmentCacheStub{result: LiveBalanceResult{Outcome: LiveBalanceOutcomeConflict}}
	service := &BillingCacheService{cache: cache}

	err := service.ApplyExternalBalanceOutboxAdjustment(context.Background(), LiveBalanceAdjustmentEvent{
		ID: 17, UserID: 42, PredecessorID: 11, DeltaUnits: 100000000,
	})

	require.ErrorContains(t, err, "watermark conflict")
}

func TestApplyExternalBalanceOutboxAdjustmentsRequiresOneContiguousUserChain(t *testing.T) {
	cache := &liveBalanceAdjustmentCacheStub{result: LiveBalanceResult{Outcome: LiveBalanceOutcomeApplied}}
	service := &BillingCacheService{cache: cache}
	events := []LiveBalanceAdjustmentEvent{
		{ID: 17, UserID: 42, PredecessorID: 11, DeltaUnits: 100},
		{ID: 19, UserID: 42, PredecessorID: 17, DeltaUnits: -25},
	}
	require.NoError(t, service.ApplyExternalBalanceOutboxAdjustments(context.Background(), events))
	require.Equal(t, 1, cache.invalidateCalls)
	require.Equal(t, 1, cache.watermarkCalls)
	require.Equal(t, int64(42), cache.userID)
	require.Equal(t, int64(19), cache.watermark)
	require.ErrorContains(t, service.ApplyExternalBalanceOutboxAdjustments(context.Background(), []LiveBalanceAdjustmentEvent{events[0], {ID: 20, UserID: 42, PredecessorID: 18, DeltaUnits: 1}}), "non-contiguous")
	require.ErrorContains(t, service.ApplyExternalBalanceOutboxAdjustments(context.Background(), []LiveBalanceAdjustmentEvent{events[0], {ID: 21, UserID: 7, PredecessorID: 17, DeltaUnits: 1}}), "invalid")
}

func TestRecoveringLiveBalanceAdjustmentInitializesFromSnapshotAndReplays(t *testing.T) {
	cache := &liveBalanceAdjustmentCacheStub{
		watermarkResults: []LiveBalanceResult{
			{Outcome: LiveBalanceOutcomeNotFound},
			{Outcome: LiveBalanceOutcomeIdempotent},
		},
		initializeResult: LiveBalanceResult{Outcome: LiveBalanceOutcomeApplied},
	}
	reader := &liveBalanceSnapshotReaderStub{
		snapshot: LiveBalanceInitializationSnapshot{Balance: 12.5, Watermark: 17},
	}
	applier := newRecoveringLiveBalanceAdjustmentApplier(&BillingCacheService{cache: cache}, reader)
	event := LiveBalanceAdjustmentEvent{ID: 17, UserID: 42, PredecessorID: 11, DeltaUnits: 100000000}

	err := applier.ApplyExternalBalanceOutboxAdjustment(context.Background(), event)

	require.NoError(t, err)
	require.Equal(t, 2, cache.watermarkCalls)
	require.Equal(t, 1, cache.initializeCalls)
	require.Equal(t, 12.5, cache.initializeBalance)
	require.Equal(t, int64(17), cache.initializeWatermark)
	require.Equal(t, 1, reader.calls)
	require.Equal(t, int64(42), reader.userID)
}

func TestRecoveringLiveBalanceAdjustmentWaitsForUnsettledBilling(t *testing.T) {
	cache := &liveBalanceAdjustmentCacheStub{
		watermarkResults: []LiveBalanceResult{{Outcome: LiveBalanceOutcomeNotFound}},
	}
	reader := &liveBalanceSnapshotReaderStub{
		snapshot: LiveBalanceInitializationSnapshot{Balance: 12.5, Watermark: 17, HasUnsettled: true},
	}
	applier := newRecoveringLiveBalanceAdjustmentApplier(&BillingCacheService{cache: cache}, reader)

	err := applier.ApplyExternalBalanceOutboxAdjustment(context.Background(), LiveBalanceAdjustmentEvent{
		ID: 17, UserID: 42, PredecessorID: 11, DeltaUnits: 100000000,
	})

	require.ErrorContains(t, err, "has unsettled billing")
	require.Equal(t, 1, cache.watermarkCalls)
	require.Zero(t, cache.initializeCalls)
}

func TestRecoveringLiveBalanceAdjustmentHealsLaggingWatermarkAndReplays(t *testing.T) {
	cache := &liveBalanceAdjustmentCacheStub{
		// The Redis watermark lags the database chain, so the first batch is a
		// conflict. After the snapshot rebuild the replay is included by the new
		// watermark and must be an idempotent no-op.
		watermarkResults: []LiveBalanceResult{
			{Outcome: LiveBalanceOutcomeConflict},
			{Outcome: LiveBalanceOutcomeIdempotent},
		},
		watermarkState: LiveBalanceWatermarkState{
			WalletValue: "163832846020", WalletExists: true,
			WatermarkValue: "286884590", WatermarkExists: true,
		},
		rebuildResult: LiveBalanceResult{Outcome: LiveBalanceOutcomeApplied},
	}
	reader := &liveBalanceSnapshotReaderStub{
		snapshot: LiveBalanceInitializationSnapshot{Balance: 2584.85424897, Watermark: 291000347},
	}
	applier := newRecoveringLiveBalanceAdjustmentApplier(&BillingCacheService{cache: cache}, reader)
	events := []LiveBalanceAdjustmentEvent{
		{ID: 287036667, UserID: 9250, PredecessorID: 287034410, DeltaUnits: -51800},
		{ID: 287036709, UserID: 9250, PredecessorID: 287036667, DeltaUnits: -38920},
	}

	require.NoError(t, applier.ApplyExternalBalanceOutboxAdjustments(context.Background(), events))

	require.Equal(t, 2, cache.watermarkCalls)
	require.Equal(t, 1, cache.watermarkStateCalls)
	require.Equal(t, 1, cache.rebuildCalls)
	require.Equal(t, cache.watermarkState, cache.rebuildExpected)
	require.Equal(t, 2584.85424897, cache.rebuildBalance)
	require.Equal(t, int64(291000347), cache.rebuildWatermark)
	require.Equal(t, uint64(1), applier.RecoveryStats().Healed)
	require.Zero(t, applier.RecoveryStats().HealFailures)
}

func TestRecoveringLiveBalanceAdjustmentRetriesWhenRebuildLosesCompareAndSwap(t *testing.T) {
	cache := &liveBalanceAdjustmentCacheStub{
		watermarkResults: []LiveBalanceResult{{Outcome: LiveBalanceOutcomeConflict}},
		rebuildResult:    LiveBalanceResult{Outcome: LiveBalanceOutcomeConflict},
	}
	reader := &liveBalanceSnapshotReaderStub{
		snapshot: LiveBalanceInitializationSnapshot{Balance: 12.5, Watermark: 40},
	}
	applier := newRecoveringLiveBalanceAdjustmentApplier(&BillingCacheService{cache: cache}, reader)

	err := applier.ApplyExternalBalanceOutboxAdjustment(context.Background(), LiveBalanceAdjustmentEvent{
		ID: 41, UserID: 42, PredecessorID: 39, DeltaUnits: 100000000,
	})

	require.ErrorIs(t, err, errLiveBalanceWatermarkConflict)
	require.Equal(t, 1, cache.watermarkCalls, "a lost compare-and-swap must not replay the stale chain")
	require.Equal(t, uint64(1), applier.RecoveryStats().HealFailures)
	require.Zero(t, applier.RecoveryStats().Healed)
}

func TestRecoveringLiveBalanceAdjustmentWaitsForUnsettledBillingOnConflict(t *testing.T) {
	cache := &liveBalanceAdjustmentCacheStub{
		watermarkResults: []LiveBalanceResult{{Outcome: LiveBalanceOutcomeConflict}},
	}
	reader := &liveBalanceSnapshotReaderStub{
		snapshot: LiveBalanceInitializationSnapshot{Balance: 12.5, Watermark: 40, HasUnsettled: true},
	}
	applier := newRecoveringLiveBalanceAdjustmentApplier(&BillingCacheService{cache: cache}, reader)

	err := applier.ApplyExternalBalanceOutboxAdjustment(context.Background(), LiveBalanceAdjustmentEvent{
		ID: 41, UserID: 42, PredecessorID: 39, DeltaUnits: 100000000,
	})

	require.ErrorContains(t, err, "has unsettled billing")
	require.Zero(t, cache.rebuildCalls, "an outstanding hold is not yet reflected in users.balance")
	require.Equal(t, uint64(1), applier.RecoveryStats().HealFailures)
}

func TestRecoveringLiveBalanceAdjustmentSkipsVanishedUser(t *testing.T) {
	cache := &liveBalanceAdjustmentCacheStub{
		watermarkResults: []LiveBalanceResult{{Outcome: LiveBalanceOutcomeConflict}},
	}
	reader := &liveBalanceSnapshotReaderStub{err: ErrUserNotFound}
	applier := newRecoveringLiveBalanceAdjustmentApplier(&BillingCacheService{cache: cache}, reader)

	err := applier.ApplyExternalBalanceOutboxAdjustment(context.Background(), LiveBalanceAdjustmentEvent{
		ID: 41, UserID: 42, PredecessorID: 39, DeltaUnits: 100000000,
	})

	require.ErrorIs(t, err, errLiveBalanceAdjustmentUserGone)
	require.Zero(t, cache.rebuildCalls)
	require.Equal(t, uint64(1), applier.RecoveryStats().Abandoned)
}

func TestCommittedBalanceSyncOnlyInvalidatesLegacyCache(t *testing.T) {
	cache := &liveBalanceAdjustmentCacheStub{result: LiveBalanceResult{Outcome: LiveBalanceOutcomeApplied}}
	service := &BillingCacheService{cache: cache}

	syncCommittedLiveBalanceAdjustment(service, 9, "legacy-call", 1)

	require.Equal(t, 1, cache.invalidateCalls)
	require.Zero(t, cache.adjustCalls)
	require.Zero(t, cache.watermarkCalls, "durable outbox worker must be the only persistent wallet delta writer")
}

func TestGetUserBalancePrefersPersistentLiveWallet(t *testing.T) {
	cache := &liveBalanceReadCacheStub{liveBalance: 1.25, liveExists: true}
	service := &BillingCacheService{cache: cache}

	balance, err := service.GetUserBalance(context.Background(), 7)
	require.NoError(t, err)
	require.Equal(t, 1.25, balance)
	require.Zero(t, cache.legacyCalls)
}

func TestGetUserBalanceDoesNotBypassLiveWalletRedisFailure(t *testing.T) {
	cache := &liveBalanceReadCacheStub{liveErr: errors.New("redis unavailable")}
	service := &BillingCacheService{cache: cache}

	_, err := service.GetUserBalance(context.Background(), 7)
	require.ErrorContains(t, err, "get live user balance")
	require.Zero(t, cache.legacyCalls)
}

func TestGetUserBalanceSkipsStaleLiveWalletWhenPreauthorizationDisabled(t *testing.T) {
	tests := []struct {
		name  string
		cache *liveBalanceReadCacheStub
	}{
		{
			name:  "stale negative wallet",
			cache: &liveBalanceReadCacheStub{liveBalance: -25, liveExists: true},
		},
		{
			name:  "live wallet redis failure",
			cache: &liveBalanceReadCacheStub{liveErr: errors.New("redis unavailable")},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &BillingCacheService{
				cache: test.cache,
				cfg:   &config.Config{},
			}

			balance, err := service.GetUserBalance(context.Background(), 7)
			require.NoError(t, err)
			require.Equal(t, float64(999), balance)
			require.Zero(t, test.cache.liveCalls)
			require.Equal(t, 1, test.cache.legacyCalls)
		})
	}
}

func TestGetUserBalanceKeepsLiveWalletFailClosedWhenPreauthorizationEnabled(t *testing.T) {
	cache := &liveBalanceReadCacheStub{liveErr: errors.New("redis unavailable")}
	service := &BillingCacheService{
		cache: cache,
		cfg: &config.Config{Billing: config.BillingConfig{
			BalancePreauthorizationEnabled: true,
		}},
	}

	_, err := service.GetUserBalance(context.Background(), 7)
	require.ErrorContains(t, err, "get live user balance")
	require.Equal(t, 1, cache.liveCalls)
	require.Zero(t, cache.legacyCalls)
}
