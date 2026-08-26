package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type guardedUsageBillingApplyRepoStub struct {
	UsageBillingRepository
	result  *UsageBillingApplyResult
	err     error
	lastCmd *UsageBillingCommand
}

func (s *guardedUsageBillingApplyRepoStub) Apply(_ context.Context, cmd *UsageBillingCommand) (*UsageBillingApplyResult, error) {
	s.lastCmd = cmd
	return s.result, s.err
}

func guardedUsageBillingParams(actual float64) (*UsageLog, *postUsageBillingParams, *billingDeps) {
	apiKey := &APIKey{ID: 7, UserID: 42, User: &User{ID: 42}}
	account := &Account{ID: 11}
	usageLog := &UsageLog{
		RequestID:    "request-1",
		UserID:       42,
		APIKeyID:     7,
		AccountID:    11,
		Model:        "gpt-test",
		BillingType:  BillingTypeBalance,
		InputTokens:  100,
		OutputTokens: 20,
		ActualCost:   actual,
	}
	params := &postUsageBillingParams{
		Cost:    &CostBreakdown{ActualCost: actual, TotalCost: actual},
		User:    apiKey.User,
		APIKey:  apiKey,
		Account: account,
	}
	deps := &billingDeps{deferredService: &DeferredService{}}
	return usageLog, params, deps
}

func TestApplyUsageBillingGuardedDuplicateStillFinalizesReservation(t *testing.T) {
	fixture := newPreauthorizationFixture()
	handlerGuard, err := fixture.service.Preauthorize(context.Background(), balancePreauthorizationTestRequest())
	require.NoError(t, err)
	workerGuard, ok := handlerGuard.TransferToWorker()
	require.True(t, ok)

	usageLog, params, deps := guardedUsageBillingParams(0.02)
	repo := &guardedUsageBillingApplyRepoStub{result: &UsageBillingApplyResult{Applied: false}}
	ctx := ContextWithBalancePreauthorizationGuard(context.Background(), workerGuard)
	applied, err := applyUsageBilling(ctx, "request-1", usageLog, params, deps, repo)

	require.NoError(t, err)
	require.False(t, applied)
	require.NotNil(t, repo.lastCmd)
	require.True(t, repo.lastCmd.BalancePreauthorized)
	require.InDelta(t, 0.02, fixture.repo.finalizedAmount, 1e-12)
	require.Equal(t, repo.lastCmd.RequestFingerprint, fixture.repo.finalizedFingerprint)
	require.InDelta(t, 0.02, fixture.wallet.lastActual, 1e-12)
	require.False(t, workerGuard.IsCurrentOwner())
	// The handler's stale defer must not refund a worker-owned settlement.
	require.NoError(t, handlerGuard.Refund(context.Background()))
	require.NotContains(t, fixture.recorder.snapshot(), "repo_begin_refund")
}

func TestApplyUsageBillingGuardedApplyErrorStillPersistsActualFinalization(t *testing.T) {
	fixture := newPreauthorizationFixture()
	handlerGuard, err := fixture.service.Preauthorize(context.Background(), balancePreauthorizationTestRequest())
	require.NoError(t, err)
	workerGuard, ok := handlerGuard.TransferToWorker()
	require.True(t, ok)

	usageLog, params, deps := guardedUsageBillingParams(0.02)
	applyErr := errors.New("database transaction failed")
	repo := &guardedUsageBillingApplyRepoStub{err: applyErr}
	ctx := ContextWithBalancePreauthorizationGuard(context.Background(), workerGuard)
	applied, err := applyUsageBilling(ctx, "request-1", usageLog, params, deps, repo)

	require.False(t, applied)
	require.ErrorIs(t, err, applyErr)
	require.NotNil(t, repo.lastCmd)
	require.True(t, repo.lastCmd.BalancePreauthorized)
	require.InDelta(t, 0.02, fixture.repo.finalizedAmount, 1e-12)
	require.InDelta(t, 0.02, fixture.wallet.lastActual, 1e-12)
	require.NotContains(t, fixture.recorder.snapshot(), "repo_begin_refund")
}

func TestTransferBalancePreauthorizationToUsageTaskAttachesOnlyWorkerOwner(t *testing.T) {
	fixture := newPreauthorizationFixture()
	handlerGuard, err := fixture.service.Preauthorize(context.Background(), balancePreauthorizationTestRequest())
	require.NoError(t, err)

	var taskGuard *BalancePreauthorizationGuard
	wrapped, ok := TransferBalancePreauthorizationToUsageTask(handlerGuard, func(ctx context.Context) {
		taskGuard, _ = BalancePreauthorizationGuardFromContext(ctx)
	})
	require.True(t, ok)
	wrapped(context.Background())

	require.NotNil(t, taskGuard)
	require.True(t, taskGuard.IsCurrentOwner())
	require.True(t, handlerGuard.IsTransferred())
	require.NoError(t, handlerGuard.Refund(context.Background()))
	require.NotContains(t, fixture.recorder.snapshot(), "repo_begin_refund")
}

func TestDuplicateUsageTaskTransferRemainsRunnableWithStaleGuard(t *testing.T) {
	fixture := newPreauthorizationFixture()
	handlerGuard, err := fixture.service.Preauthorize(context.Background(), balancePreauthorizationTestRequest())
	require.NoError(t, err)

	first, ok := TransferBalancePreauthorizationToUsageTask(handlerGuard, func(context.Context) {})
	require.True(t, ok)
	require.NotNil(t, first)

	var duplicateGuard *BalancePreauthorizationGuard
	duplicateRan := false
	duplicate, ok := TransferBalancePreauthorizationToUsageTask(handlerGuard, func(ctx context.Context) {
		duplicateRan = true
		duplicateGuard, _ = BalancePreauthorizationGuardFromContext(ctx)
	})
	require.False(t, ok)
	require.NotNil(t, duplicate, "duplicate side effects must not be silently dropped")
	duplicate(context.Background())

	require.True(t, duplicateRan)
	require.Same(t, handlerGuard, duplicateGuard)
	require.False(t, duplicateGuard.IsCurrentOwner())
}

func TestApplyUsageBillingZeroUsageRefundsHold(t *testing.T) {
	fixture := newPreauthorizationFixture()
	handlerGuard, err := fixture.service.Preauthorize(context.Background(), balancePreauthorizationTestRequest())
	require.NoError(t, err)
	workerGuard, ok := handlerGuard.TransferToWorker()
	require.True(t, ok)

	usageLog, params, deps := guardedUsageBillingParams(0)
	usageLog.InputTokens = 0
	usageLog.OutputTokens = 0
	repo := &guardedUsageBillingApplyRepoStub{result: &UsageBillingApplyResult{Applied: false}}
	ctx := ContextWithBalancePreauthorizationGuard(context.Background(), workerGuard)
	_, err = applyUsageBilling(ctx, "request-1", usageLog, params, deps, repo)

	require.NoError(t, err)
	require.NotNil(t, repo.lastCmd)
	require.Greater(t, handlerGuard.HoldAmount(), 0.0)
	require.Zero(t, repo.lastCmd.BalanceCost)
	require.Zero(t, usageLog.ActualCost)
	require.Equal(t, 1, fixture.wallet.refundCalls)
	require.Zero(t, fixture.wallet.finalizeCalls)
}

func TestApplyUsageBillingZeroUsageRefundsStreamingTopUps(t *testing.T) {
	fixture := newPreauthorizationFixture()
	fixture.calculator.outputUnitPrice = 0.001
	handlerGuard, err := fixture.service.Preauthorize(context.Background(), balancePreauthorizationTestRequest())
	require.NoError(t, err)
	initialHold := handlerGuard.HoldAmount()

	for i := 0; i < 24; i++ {
		require.NoError(t, handlerGuard.ObserveStreamingOutput(context.Background(), 64))
	}
	require.Greater(t, handlerGuard.HoldAmount(), initialHold)
	require.Greater(t, fixture.wallet.topUpCalls, 0)

	workerGuard, ok := handlerGuard.TransferToWorker()
	require.True(t, ok)
	usageLog, params, deps := guardedUsageBillingParams(0)
	usageLog.InputTokens = 0
	usageLog.OutputTokens = 0
	repo := &guardedUsageBillingApplyRepoStub{result: &UsageBillingApplyResult{Applied: false}}
	ctx := ContextWithBalancePreauthorizationGuard(context.Background(), workerGuard)

	_, err = applyUsageBilling(ctx, "request-1", usageLog, params, deps, repo)

	require.NoError(t, err)
	require.Zero(t, repo.lastCmd.BalanceCost)
	require.Zero(t, usageLog.ActualCost)
	require.Equal(t, 1, fixture.wallet.refundCalls)
	require.Zero(t, fixture.wallet.finalizeCalls)
}

func TestApplyUsageBillingKeepsTrulyFreeZeroHoldFree(t *testing.T) {
	fixture := newPreauthorizationFixture()
	fixture.calculator.zero = true
	handlerGuard, err := fixture.service.Preauthorize(context.Background(), balancePreauthorizationTestRequest())
	require.NoError(t, err)
	require.Zero(t, handlerGuard.HoldAmount())
	workerGuard, ok := handlerGuard.TransferToWorker()
	require.True(t, ok)

	usageLog, params, deps := guardedUsageBillingParams(0)
	repo := &guardedUsageBillingApplyRepoStub{result: &UsageBillingApplyResult{Applied: false}}
	ctx := ContextWithBalancePreauthorizationGuard(context.Background(), workerGuard)
	_, err = applyUsageBilling(ctx, "request-1", usageLog, params, deps, repo)

	require.NoError(t, err)
	require.Zero(t, repo.lastCmd.BalanceCost)
	require.Zero(t, usageLog.ActualCost)
	require.Equal(t, 1, fixture.wallet.refundCalls)
	require.Zero(t, fixture.wallet.finalizeCalls)
}

func TestApplyUsageBillingStaleGuardRejectsBeforeRepositoryApply(t *testing.T) {
	fixture := newPreauthorizationFixture()
	handlerGuard, err := fixture.service.Preauthorize(context.Background(), balancePreauthorizationTestRequest())
	require.NoError(t, err)
	workerGuard, ok := handlerGuard.TransferToWorker()
	require.True(t, ok)

	usageLog, params, deps := guardedUsageBillingParams(0.02)
	repo := &guardedUsageBillingApplyRepoStub{result: &UsageBillingApplyResult{Applied: true}}
	staleCtx := ContextWithBalancePreauthorizationGuard(context.Background(), handlerGuard)
	_, err = applyUsageBilling(staleCtx, "request-1", usageLog, params, deps, repo)

	require.ErrorIs(t, err, ErrBalancePreauthorizationOwnershipTransferred)
	require.Nil(t, repo.lastCmd)
	require.True(t, workerGuard.IsCurrentOwner())
	require.Zero(t, fixture.wallet.finalizeCalls)
	require.Zero(t, fixture.wallet.refundCalls)
}
