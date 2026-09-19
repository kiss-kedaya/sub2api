package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAuditRecoveryMustNotDiscardStreamingHoldTopUps(t *testing.T) {
	fixture := newPreauthorizationFixture()
	guard := streamingPreauthorizationGuard(t, fixture)
	initial := fixture.repo.prepared.HoldAmount
	for i := 0; i < 40; i++ {
		require.NoError(t, guard.ObserveStreamingOutput(context.Background(), 32))
	}
	reserved := guard.HoldAmount()
	require.Greater(t, reserved, initial)
	prepared := fixture.repo.prepared
	// After process loss, recovery receives only the durable authorization row.
	record := BalancePreauthorizationRecord{
		RequestID: prepared.RequestID, APIKeyID: prepared.APIKeyID, UserID: prepared.UserID,
		AuthorizationFingerprint: prepared.AuthorizationFingerprint,
		HoldAmount:               prepared.HoldAmount, Status: BalanceSettlementAuthorized,
	}
	fixture.wallet.finalize = []LiveBalanceResult{{
		Outcome: LiveBalanceOutcomeApplied, State: LiveBalanceAttemptFinalized, ReservedAmount: reserved,
	}}
	require.NoError(t, fixture.service.RecoverBalancePreauthorization(context.Background(), record))
	t.Logf("initial=%v reserved_after_output=%v durable_hold=%v recovery_settlement=%v released=%v",
		initial, reserved, record.HoldAmount, fixture.wallet.lastActual, reserved-fixture.wallet.lastActual)
	require.InDelta(t, reserved, fixture.wallet.lastActual, 1e-9,
		"crash recovery must account for cumulative live reservation before finalizing")
}

func TestStreamingTopUpRedisSuccessPGFailureRecoversCumulativeHold(t *testing.T) {
	fixture := newPreauthorizationFixture()
	guard := streamingPreauthorizationGuard(t, fixture)
	initial := guard.HoldAmount()
	fixture.repo.advanceHoldErr = errors.New("postgres unavailable")

	var topUpErr error
	for i := 0; i < 40 && topUpErr == nil; i++ {
		topUpErr = guard.ObserveStreamingOutput(context.Background(), 64)
	}
	require.ErrorIs(t, topUpErr, ErrBillingServiceUnavailable)
	redisReserved := fixture.wallet.lastTopUpTarget
	require.Greater(t, redisReserved, initial)
	require.InDelta(t, initial, guard.HoldAmount(), 1e-12,
		"memory must not acknowledge a hold that PG failed to persist")

	fixture.repo.advanceHoldErr = nil
	fixture.wallet.finalize = []LiveBalanceResult{{
		Outcome: LiveBalanceOutcomeApplied, State: LiveBalanceAttemptFinalized,
		ReservedAmount: redisReserved,
	}}
	record := BalancePreauthorizationRecord{
		RequestID: "request-1", APIKeyID: 7, UserID: 42,
		AuthorizationFingerprint: "authorization-fingerprint",
		HoldAmount:               initial, Status: BalanceSettlementAuthorized,
	}
	require.NoError(t, fixture.service.RecoverBalancePreauthorization(context.Background(), record))
	require.InDelta(t, redisReserved, fixture.repo.advancedHolds[len(fixture.repo.advancedHolds)-1], 1e-12)
	require.InDelta(t, redisReserved, fixture.wallet.lastActual, 1e-12)
}

func TestRecoverAuthorizedDoesNotOverwriteConcurrentTerminalAttempt(t *testing.T) {
	fixture := newPreauthorizationFixture()
	terminal := LiveBalanceResult{
		Outcome:        LiveBalanceOutcomeIdempotent,
		State:          LiveBalanceAttemptFinalized,
		ReservedAmount: 0.50,
		ActualAmount:   0.20,
	}
	fixture.wallet.attemptRead = &terminal

	err := fixture.service.RecoverBalancePreauthorization(context.Background(), BalancePreauthorizationRecord{
		RequestID: "stale-authorized", APIKeyID: 7, UserID: 42,
		AuthorizationFingerprint: "authorization-fingerprint",
		HoldAmount:               0.10, Status: BalanceSettlementAuthorized,
	})

	require.ErrorIs(t, err, ErrBillingServiceUnavailable)
	require.Empty(t, fixture.repo.advancedHolds)
	require.Zero(t, fixture.wallet.finalizeCalls)
	require.NotContains(t, fixture.recorder.snapshot(), "repo_begin_finalize")
}
