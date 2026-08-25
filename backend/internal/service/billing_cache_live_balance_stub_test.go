package service

import "context"

// noopLiveBalanceCacheStub keeps older focused BillingCache test doubles small.
// Tests for live-balance behavior use the repository implementation directly.
type noopLiveBalanceCacheStub struct{}

func (noopLiveBalanceCacheStub) GetLiveBalance(context.Context, int64) (float64, bool, error) {
	return 0, false, nil
}

func (noopLiveBalanceCacheStub) AuthorizeLiveBalance(context.Context, int64, string, float64, float64) (LiveBalanceResult, error) {
	return LiveBalanceResult{}, nil
}

func (noopLiveBalanceCacheStub) TopUpLiveBalance(context.Context, int64, string, float64) (LiveBalanceResult, error) {
	return LiveBalanceResult{}, nil
}

func (noopLiveBalanceCacheStub) FinalizeLiveBalance(context.Context, int64, string, float64) (LiveBalanceResult, error) {
	return LiveBalanceResult{}, nil
}

func (noopLiveBalanceCacheStub) RefundLiveBalance(context.Context, int64, string) (LiveBalanceResult, error) {
	return LiveBalanceResult{}, nil
}

func (noopLiveBalanceCacheStub) AdjustLiveBalance(context.Context, int64, string, float64) (LiveBalanceResult, error) {
	return LiveBalanceResult{Outcome: LiveBalanceOutcomeNotFound}, nil
}
