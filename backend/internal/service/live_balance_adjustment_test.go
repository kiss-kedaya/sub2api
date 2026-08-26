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
	result          LiveBalanceResult
	adjustErr       error
	invalidateErr   error
	invalidateCalls int
	adjustCalls     int
	userID          int64
	eventID         string
	delta           float64
	deltaUnits      int64
	watermark       int64
	predecessor     int64
	watermarkCalls  int
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
	return s.result, s.adjustErr
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
