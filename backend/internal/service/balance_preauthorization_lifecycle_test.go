package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

type preauthorizationCallRecorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *preauthorizationCallRecorder) add(call string) {
	r.mu.Lock()
	r.calls = append(r.calls, call)
	r.mu.Unlock()
}

func (r *preauthorizationCallRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

type preauthorizationCostCalculatorStub struct {
	recorder *preauthorizationCallRecorder
	inputs   []CostInput
	err      error
	zero     bool
	// perUnitPrice, when > 0, switches the stub to per-request pricing:
	// cost = perUnitPrice * max(UsageUnits, RequestCount) * sizeTierMultiplier.
	perUnitPrice float64
	// outputUnitPrice, when > 0, adds a linear per-output-token term so tests can
	// exercise the streaming top-up tracker via the difference-method price.
	outputUnitPrice float64
}

func (s *preauthorizationCostCalculatorStub) CalculateCostUnified(input CostInput) (*CostBreakdown, error) {
	s.recorder.add("price")
	s.inputs = append(s.inputs, input)
	if s.err != nil {
		return nil, s.err
	}
	if s.zero {
		return &CostBreakdown{ActualCost: 0}, nil
	}
	if s.perUnitPrice > 0 {
		units := input.UsageUnits
		if units <= 0 {
			units = float64(input.RequestCount)
		}
		if units <= 0 {
			units = 1
		}
		multiplier := 1.0
		switch input.SizeTier {
		case "2K":
			multiplier = 1.5
		case "4K":
			multiplier = 2
		}
		cost := s.perUnitPrice * units * multiplier * input.RateMultiplier
		return &CostBreakdown{ActualCost: cost, BillingMode: string(BillingModeImage)}, nil
	}
	cost := 0.003
	switch {
	case input.Tokens.InputTokens > 0:
		cost += 0.010
	case input.Tokens.CacheReadTokens > 0:
		cost += 0.005
	case input.Tokens.CacheCreationTokens > 0:
		if input.Tokens.CacheCreation1hTokens > 0 {
			cost += 0.025
		} else {
			cost += 0.020
		}
	}
	// Output pricing is linear in output tokens so the difference method can
	// recover a per-output-token price. Enabled only when outputUnitPrice is set
	// to avoid perturbing the many tests that assert exact input-only holds.
	cost += float64(input.Tokens.OutputTokens) * s.outputUnitPrice
	return &CostBreakdown{ActualCost: cost}, nil
}

type preauthorizationWalletStub struct {
	recorder        *preauthorizationCallRecorder
	authorize       LiveBalanceResult
	authorizeErr    error
	existing        *LiveBalanceResult
	existingErr     error
	finalize        []LiveBalanceResult
	finalizeErr     error
	refund          []LiveBalanceResult
	refundErr       error
	topUp           []LiveBalanceResult
	topUpErr        error
	lastAttemptID   string
	lastFallback    float64
	lastWatermark   int64
	lastAllowInit   bool
	lastHold        float64
	lastActual      float64
	lastTopUpTarget float64
	finalizeCalls   int
	refundCalls     int
	topUpCalls      int
}

func (s *preauthorizationWalletStub) AuthorizeExistingLiveBalance(
	_ context.Context,
	_ int64,
	attemptID string,
	holdAmount float64,
) (LiveBalanceResult, error) {
	s.recorder.add("wallet_authorize_existing")
	s.lastAttemptID = attemptID
	s.lastHold = holdAmount
	if s.existingErr != nil {
		return LiveBalanceResult{}, s.existingErr
	}
	if s.existing == nil {
		return LiveBalanceResult{Outcome: LiveBalanceOutcomeNotFound}, nil
	}
	result := *s.existing
	if result.ReservedAmount == 0 && holdAmount > 0 && result.State == LiveBalanceAttemptAuthorized {
		result.ReservedAmount = holdAmount
	}
	return result, nil
}

func (s *preauthorizationWalletStub) AuthorizeLiveBalance(_ context.Context, _ int64, attemptID string, fallbackBalance, holdAmount float64) (LiveBalanceResult, error) {
	return s.AuthorizeLiveBalanceAtWatermark(context.Background(), 0, attemptID, fallbackBalance, 0, holdAmount)
}

func (s *preauthorizationWalletStub) AuthorizeLiveBalanceAtWatermark(
	_ context.Context,
	_ int64,
	attemptID string,
	fallbackBalance float64,
	fallbackWatermark int64,
	holdAmount float64,
) (LiveBalanceResult, error) {
	return s.AuthorizeLiveBalanceAtWatermarkIfSafe(
		context.Background(), 0, attemptID, fallbackBalance, fallbackWatermark, holdAmount, true,
	)
}

func (s *preauthorizationWalletStub) AuthorizeLiveBalanceAtWatermarkIfSafe(
	_ context.Context,
	_ int64,
	attemptID string,
	fallbackBalance float64,
	fallbackWatermark int64,
	holdAmount float64,
	allowInitialize bool,
) (LiveBalanceResult, error) {
	s.recorder.add("wallet_authorize")
	s.lastAttemptID = attemptID
	s.lastFallback = fallbackBalance
	s.lastWatermark = fallbackWatermark
	s.lastAllowInit = allowInitialize
	s.lastHold = holdAmount
	result := s.authorize
	if result.ReservedAmount == 0 && holdAmount > 0 && result.State == LiveBalanceAttemptAuthorized {
		result.ReservedAmount = holdAmount
	}
	return result, s.authorizeErr
}

func (s *preauthorizationWalletStub) TopUpLiveBalance(_ context.Context, _ int64, attemptID string, targetHoldAmount float64) (LiveBalanceResult, error) {
	s.recorder.add("wallet_topup")
	s.lastAttemptID = attemptID
	s.lastTopUpTarget = targetHoldAmount
	s.topUpCalls++
	if s.topUpErr != nil {
		return LiveBalanceResult{}, s.topUpErr
	}
	if len(s.topUp) == 0 {
		return LiveBalanceResult{Outcome: LiveBalanceOutcomeApplied, State: LiveBalanceAttemptAuthorized, ReservedAmount: targetHoldAmount}, nil
	}
	index := s.topUpCalls - 1
	if index >= len(s.topUp) {
		index = len(s.topUp) - 1
	}
	result := s.topUp[index]
	if result.ReservedAmount == 0 && targetHoldAmount > 0 && result.State == LiveBalanceAttemptAuthorized {
		result.ReservedAmount = targetHoldAmount
	}
	return result, nil
}

func (s *preauthorizationWalletStub) FinalizeLiveBalance(_ context.Context, _ int64, _ string, actualAmount float64) (LiveBalanceResult, error) {
	s.recorder.add("wallet_finalize")
	s.lastActual = actualAmount
	index := s.finalizeCalls
	s.finalizeCalls++
	if s.finalizeErr != nil {
		return LiveBalanceResult{}, s.finalizeErr
	}
	if index >= len(s.finalize) {
		index = len(s.finalize) - 1
	}
	result := s.finalize[index]
	if result.ActualAmount == 0 && actualAmount > 0 && result.State == LiveBalanceAttemptFinalized {
		result.ActualAmount = actualAmount
	}
	return result, nil
}

func (s *preauthorizationWalletStub) RefundLiveBalance(_ context.Context, _ int64, _ string) (LiveBalanceResult, error) {
	s.recorder.add("wallet_refund")
	index := s.refundCalls
	s.refundCalls++
	if s.refundErr != nil {
		return LiveBalanceResult{}, s.refundErr
	}
	if index >= len(s.refund) {
		index = len(s.refund) - 1
	}
	return s.refund[index], nil
}

type preauthorizationRepositoryStub struct {
	recorder                 *preauthorizationCallRecorder
	prepareRecord            *BalancePreauthorizationRecord
	prepareErr               error
	authorizedErr            error
	beginFinalizationErr     error
	completeSettlementErrors []error
	beginRefundErr           error
	completeRefundErr        error
	prepared                 *BalancePreauthorizationCommand
	finalizedAmount          float64
	finalizedFingerprint     string
	completeSettlementCalls  int
	snapshot                 LiveBalanceInitializationSnapshot
	snapshotErr              error
}

func (s *preauthorizationRepositoryStub) LoadLiveBalanceInitializationSnapshot(
	_ context.Context,
	_ int64,
) (LiveBalanceInitializationSnapshot, error) {
	s.recorder.add("balance_snapshot")
	return s.snapshot, s.snapshotErr
}

func (s *preauthorizationRepositoryStub) PrepareBalancePreauthorization(_ context.Context, cmd *BalancePreauthorizationCommand) (*BalancePreauthorizationRecord, error) {
	s.recorder.add("repo_prepare")
	copy := *cmd
	s.prepared = &copy
	if s.prepareErr != nil {
		return nil, s.prepareErr
	}
	if s.prepareRecord != nil {
		return s.prepareRecord, nil
	}
	return &BalancePreauthorizationRecord{
		RequestID:  cmd.RequestID,
		APIKeyID:   cmd.APIKeyID,
		UserID:     cmd.UserID,
		HoldAmount: cmd.HoldAmount,
		Status:     BalanceSettlementPrepared,
	}, nil
}

func (s *preauthorizationRepositoryStub) MarkBalancePreauthorizationAuthorized(context.Context, string, int64) error {
	s.recorder.add("repo_authorized")
	return s.authorizedErr
}

func (s *preauthorizationRepositoryStub) BeginBalancePreauthorizationFinalization(_ context.Context, _ string, _ int64, amount float64, fingerprint string) error {
	s.recorder.add("repo_begin_finalize")
	s.finalizedAmount = amount
	s.finalizedFingerprint = fingerprint
	return s.beginFinalizationErr
}

func (s *preauthorizationRepositoryStub) CompleteBalancePreauthorizationSettlement(context.Context, string, int64) error {
	s.recorder.add("repo_complete_settlement")
	index := s.completeSettlementCalls
	s.completeSettlementCalls++
	if len(s.completeSettlementErrors) == 0 {
		return nil
	}
	if index >= len(s.completeSettlementErrors) {
		index = len(s.completeSettlementErrors) - 1
	}
	return s.completeSettlementErrors[index]
}

func (s *preauthorizationRepositoryStub) BeginBalancePreauthorizationRefund(context.Context, string, int64) error {
	s.recorder.add("repo_begin_refund")
	return s.beginRefundErr
}

func (s *preauthorizationRepositoryStub) CompleteBalancePreauthorizationRefund(context.Context, string, int64) error {
	s.recorder.add("repo_complete_refund")
	return s.completeRefundErr
}

type preauthorizationFixture struct {
	service    *BalancePreauthorizationService
	recorder   *preauthorizationCallRecorder
	calculator *preauthorizationCostCalculatorStub
	wallet     *preauthorizationWalletStub
	repo       *preauthorizationRepositoryStub
}

func newPreauthorizationFixture() *preauthorizationFixture {
	recorder := &preauthorizationCallRecorder{}
	calculator := &preauthorizationCostCalculatorStub{recorder: recorder}
	wallet := &preauthorizationWalletStub{
		recorder:  recorder,
		authorize: LiveBalanceResult{Outcome: LiveBalanceOutcomeApplied, State: LiveBalanceAttemptAuthorized},
		finalize:  []LiveBalanceResult{{Outcome: LiveBalanceOutcomeApplied, State: LiveBalanceAttemptFinalized}},
		refund:    []LiveBalanceResult{{Outcome: LiveBalanceOutcomeApplied, State: LiveBalanceAttemptRefunded}},
	}
	repo := &preauthorizationRepositoryStub{
		recorder: recorder,
		snapshot: LiveBalanceInitializationSnapshot{Balance: 10, Watermark: 17},
	}
	return &preauthorizationFixture{
		service: &BalancePreauthorizationService{
			cfg:             &config.Config{RunMode: config.RunModeStandard},
			costCalculator:  calculator,
			snapshotReader:  repo,
			wallet:          wallet,
			watermarkWallet: wallet,
			repo:            repo,
		},
		recorder:   recorder,
		calculator: calculator,
		wallet:     wallet,
		repo:       repo,
	}
}

func balancePreauthorizationTestRequest() BalancePreauthorizationRequest {
	groupID := int64(9)
	return BalancePreauthorizationRequest{
		RequestID:                " request-1 ",
		APIKeyID:                 7,
		UserID:                   42,
		AuthorizationFingerprint: " auth-fingerprint ",
		BillingType:              BillingTypeBalance,
		BillableInputBytes:       100,
		CostInput: CostInput{
			Model:          "gpt-test",
			GroupID:        &groupID,
			RateMultiplier: 1.25,
			PricingAt:      time.Unix(123, 0),
			ServiceTier:    "priority",
		},
	}
}

// TestBalancePreauthorizationLifecycleUsesRequestLocalPlainInput proves the
// hold prices the current request once and never reads historical usage.
func TestBalancePreauthorizationLifecycleUsesRequestLocalPlainInput(t *testing.T) {
	fixture := newPreauthorizationFixture()
	guard, err := fixture.service.Preauthorize(context.Background(), balancePreauthorizationTestRequest())
	require.NoError(t, err)
	require.NotNil(t, guard)
	// Plain input: base 0.003 + input 0.010 = 0.013 (no output term; stub
	// outputUnitPrice unset), rounded up by hold quantization. Old behavior held
	// 0.028 (cache_creation+1h max).
	require.InDelta(t, 0.013, guard.HoldAmount(), 1e-4)
	require.Equal(t, DefaultBalancePreauthorizationOutputWindow, guard.ReservedOutputTokens())
	// Three pricing calls: plain-input hold, then output-unit-price windowed +
	// baseline (both plain-input, decoupled from the hold's input disposition).
	require.Len(t, fixture.calculator.inputs, 3)
	require.Equal(t, 100, fixture.calculator.inputs[0].Tokens.InputTokens)
	require.Equal(t, 0, fixture.calculator.inputs[0].Tokens.CacheReadTokens)
	require.Equal(t, DefaultBalancePreauthorizationOutputWindow, fixture.calculator.inputs[0].Tokens.OutputTokens)
	require.Equal(t, "gpt-test", fixture.calculator.inputs[0].Model)
	require.Equal(t, 1.25, fixture.calculator.inputs[0].RateMultiplier)
	// Output-unit-price probes: windowed has the output window, baseline is
	// output-free; both plain-input.
	require.Equal(t, DefaultBalancePreauthorizationOutputWindow, fixture.calculator.inputs[1].Tokens.OutputTokens)
	require.Equal(t, 0, fixture.calculator.inputs[2].Tokens.OutputTokens)
	require.InDelta(t, 0.013, fixture.repo.prepared.HoldAmount, 1e-4)
	require.Equal(t, "request-1:7", fixture.wallet.lastAttemptID)
	require.Equal(t, 10.0, fixture.wallet.lastFallback)
	require.Equal(t, int64(17), fixture.wallet.lastWatermark)
	require.Equal(t, []string{
		"price", "price", "price", "repo_prepare", "wallet_authorize_existing", "balance_snapshot", "wallet_authorize", "repo_authorized",
	}, fixture.recorder.snapshot())

	err = guard.Finalize(context.Background(), 0.019999999, " actual-fingerprint ")
	require.NoError(t, err)
	require.Equal(t, 0.02, fixture.repo.finalizedAmount)
	require.Equal(t, "actual-fingerprint", fixture.repo.finalizedFingerprint)
	require.Equal(t, 0.02, fixture.wallet.lastActual)

	// A retry after all three finalization steps is a local idempotent no-op.
	require.NoError(t, guard.Finalize(context.Background(), 0.02, "actual-fingerprint"))
	require.Equal(t, 1, fixture.wallet.finalizeCalls)
}

func TestBalancePreauthorizationLifecycleHotWalletSkipsPostgreSQLSnapshot(t *testing.T) {
	fixture := newPreauthorizationFixture()
	existing := LiveBalanceResult{Outcome: LiveBalanceOutcomeApplied, State: LiveBalanceAttemptAuthorized}
	fixture.wallet.existing = &existing

	guard, err := fixture.service.Preauthorize(context.Background(), balancePreauthorizationTestRequest())
	require.NoError(t, err)
	require.NotNil(t, guard)
	require.NotContains(t, fixture.recorder.snapshot(), "balance_snapshot")
	require.NotContains(t, fixture.recorder.snapshot(), "wallet_authorize")
	require.Equal(t, []string{
		"price", "price", "price", "repo_prepare", "wallet_authorize_existing", "repo_authorized",
	}, fixture.recorder.snapshot())
}

// perRequestPreauthorizationRequest builds a count/size-metered request that
// exercises the PreauthorizationEstimatePerRequest path (e.g. image endpoints).
func perRequestPreauthorizationRequest(count int, sizeTier string) BalancePreauthorizationRequest {
	groupID := int64(9)
	return BalancePreauthorizationRequest{
		RequestID:                " image-req-1 ",
		APIKeyID:                 7,
		UserID:                   42,
		AuthorizationFingerprint: " auth-fingerprint ",
		BillingType:              BillingTypeBalance,
		EstimateKind:             PreauthorizationEstimatePerRequest,
		PerRequestEstimate: PerRequestPreauthorizationEstimate{
			RequestCount: count,
			SizeTier:     sizeTier,
		},
		CostInput: CostInput{
			Model:          "gpt-image-1",
			GroupID:        &groupID,
			RateMultiplier: 1.0,
			PricingAt:      time.Unix(123, 0),
		},
	}
}

// TestBalancePreauthorizationPricesPerRequestEndpointsByParameters proves the
// per-request estimate reserves the exact request price once (not a token
// upper-bound), scales with count and size tier, and reports a zero output
// window. This is the P0-1 fix for image/video/search endpoints.
func TestBalancePreauthorizationPricesPerRequestEndpointsByParameters(t *testing.T) {
	fixture := newPreauthorizationFixture()
	fixture.calculator.perUnitPrice = 0.04

	guard, err := fixture.service.Preauthorize(context.Background(), perRequestPreauthorizationRequest(3, "2K"))
	require.NoError(t, err)
	require.NotNil(t, guard)

	// One pricing call only (no 4-scenario token upper bound), priced as
	// 0.04 * 3 images * 1.5 (2K) * 1.0 multiplier = 0.18.
	require.Len(t, fixture.calculator.inputs, 1)
	require.InDelta(t, 0.18, guard.HoldAmount(), 1e-12)
	require.Equal(t, 0, guard.ReservedOutputTokens())
	require.Equal(t, 3, fixture.calculator.inputs[0].RequestCount)
	require.Equal(t, "2K", fixture.calculator.inputs[0].SizeTier)
	require.Equal(t, 0, fixture.calculator.inputs[0].Tokens.OutputTokens)
	require.InDelta(t, 0.18, fixture.repo.prepared.HoldAmount, 1e-12)

	// Settlement refunds the positive difference: actual 0.12 < reserved 0.18.
	require.NoError(t, guard.Finalize(context.Background(), 0.12, "actual-fingerprint"))
	require.InDelta(t, 0.12, fixture.wallet.lastActual, 1e-12)
}

// TestBalancePreauthorizationPerRequestFailsClosedOnUnknownPricing proves an
// unpriced paid model is rejected before any wallet mutation rather than
// silently reserving zero (the §6.9 silent-zero-charge guard for the
// per-request path).
func TestBalancePreauthorizationPerRequestFailsClosedOnUnknownPricing(t *testing.T) {
	fixture := newPreauthorizationFixture()
	fixture.calculator.err = ErrModelPricingUnavailable

	guard, err := fixture.service.Preauthorize(context.Background(), perRequestPreauthorizationRequest(1, "1K"))
	require.Error(t, err)
	require.Nil(t, guard)
	require.NotContains(t, fixture.recorder.snapshot(), "repo_prepare")
	require.NotContains(t, fixture.recorder.snapshot(), "wallet_authorize_existing")
}

func TestBalancePreauthorizationLifecycleColdWalletWithUnsettledLedgerFailsClosed(t *testing.T) {
	fixture := newPreauthorizationFixture()
	fixture.repo.snapshot.HasUnsettled = true
	fixture.wallet.authorize = LiveBalanceResult{Outcome: LiveBalanceOutcomeNotFound, State: LiveBalanceAttemptNone}
	fixture.wallet.refund = []LiveBalanceResult{{Outcome: LiveBalanceOutcomeNotFound, State: LiveBalanceAttemptNone}}

	guard, err := fixture.service.Preauthorize(context.Background(), balancePreauthorizationTestRequest())
	require.Nil(t, guard)
	require.ErrorIs(t, err, ErrBillingServiceUnavailable)
	require.False(t, fixture.wallet.lastAllowInit)
	require.Contains(t, fixture.recorder.snapshot(), "repo_begin_refund")
}

func TestBalancePreauthorizationLifecycleInsufficientReturnsRequired403AndCompensates(t *testing.T) {
	fixture := newPreauthorizationFixture()
	fixture.wallet.authorize = LiveBalanceResult{Outcome: LiveBalanceOutcomeInsufficient, State: LiveBalanceAttemptNone}
	fixture.wallet.refund = []LiveBalanceResult{{Outcome: LiveBalanceOutcomeNotFound, State: LiveBalanceAttemptNone}}

	guard, err := fixture.service.Preauthorize(context.Background(), balancePreauthorizationTestRequest())
	require.Nil(t, guard)
	require.ErrorIs(t, err, ErrBalanceWithholdingFailed)
	require.Equal(t, 403, infraerrors.Code(err))
	require.Equal(t, "Insufficient balance, withholding failed", infraerrors.Message(err))
	require.Equal(t, []string{
		"price", "price", "price", "repo_prepare", "wallet_authorize_existing", "balance_snapshot", "wallet_authorize",
		"repo_begin_refund", "wallet_refund", "repo_complete_refund",
	}, fixture.recorder.snapshot())
}

func TestBalancePreauthorizationLifecycleDependencyFailureIsFailClosed(t *testing.T) {
	t.Run("prepare", func(t *testing.T) {
		fixture := newPreauthorizationFixture()
		fixture.repo.prepareErr = errors.New("postgres unavailable")
		guard, err := fixture.service.Preauthorize(context.Background(), balancePreauthorizationTestRequest())
		require.Nil(t, guard)
		require.ErrorIs(t, err, ErrBillingServiceUnavailable)
		require.Equal(t, 503, infraerrors.Code(err))
		require.NotContains(t, fixture.recorder.snapshot(), "wallet_authorize")
	})

	t.Run("redis authorize", func(t *testing.T) {
		fixture := newPreauthorizationFixture()
		fixture.wallet.authorizeErr = errors.New("redis unavailable")
		fixture.wallet.refundErr = errors.New("redis unavailable")
		guard, err := fixture.service.Preauthorize(context.Background(), balancePreauthorizationTestRequest())
		require.Nil(t, guard)
		require.ErrorIs(t, err, ErrBillingServiceUnavailable)
		require.Equal(t, 503, infraerrors.Code(err))
		require.Contains(t, fixture.recorder.snapshot(), "repo_begin_refund")
		require.Contains(t, fixture.recorder.snapshot(), "wallet_refund")
		require.NotContains(t, fixture.recorder.snapshot(), "repo_complete_refund")
	})
}

func TestBalancePreauthorizationLifecycleSkipsSimpleAndSubscription(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*preauthorizationFixture, *BalancePreauthorizationRequest)
	}{
		{
			name: "simple",
			mutate: func(fixture *preauthorizationFixture, _ *BalancePreauthorizationRequest) {
				fixture.service.cfg.RunMode = config.RunModeSimple
			},
		},
		{
			name: "subscription",
			mutate: func(_ *preauthorizationFixture, request *BalancePreauthorizationRequest) {
				request.BillingType = BillingTypeSubscription
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPreauthorizationFixture()
			request := balancePreauthorizationTestRequest()
			test.mutate(fixture, &request)
			guard, err := fixture.service.Preauthorize(context.Background(), request)
			require.NoError(t, err)
			require.Nil(t, guard)
			require.Empty(t, fixture.recorder.snapshot())
		})
	}
}

func TestBalancePreauthorizationRequirementMatchesLifecycleModes(t *testing.T) {
	fixture := newPreauthorizationFixture()
	require.True(t, fixture.service.RequiresPreauthorization(BillingTypeBalance))
	require.False(t, fixture.service.RequiresPreauthorization(BillingTypeSubscription))

	fixture.service.cfg.RunMode = config.RunModeSimple
	require.False(t, fixture.service.RequiresPreauthorization(BillingTypeBalance))
}

func TestBalancePreauthorizationGuardTransferInvalidatesHandlerOwnership(t *testing.T) {
	fixture := newPreauthorizationFixture()
	handlerGuard, err := fixture.service.Preauthorize(context.Background(), balancePreauthorizationTestRequest())
	require.NoError(t, err)

	var successes atomic.Int32
	owners := make(chan *BalancePreauthorizationGuard, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if owner, ok := handlerGuard.TransferToWorker(); ok {
				successes.Add(1)
				owners <- owner
			}
		}()
	}
	wg.Wait()
	close(owners)
	require.Equal(t, int32(1), successes.Load())
	workerGuard := <-owners
	require.True(t, handlerGuard.IsTransferred())
	require.False(t, handlerGuard.IsCurrentOwner())
	require.True(t, workerGuard.IsCurrentOwner())

	callCount := len(fixture.recorder.snapshot())
	require.NoError(t, handlerGuard.Refund(context.Background()))
	require.Len(t, fixture.recorder.snapshot(), callCount)
	require.ErrorIs(t, handlerGuard.Finalize(context.Background(), 0.01, "fingerprint"), ErrBalancePreauthorizationOwnershipTransferred)
	require.NoError(t, workerGuard.Finalize(context.Background(), 0.01, "fingerprint"))
}

func TestBalancePreauthorizationGuardRefundAndZeroCostFinalize(t *testing.T) {
	t.Run("refund", func(t *testing.T) {
		fixture := newPreauthorizationFixture()
		guard, err := fixture.service.Preauthorize(context.Background(), balancePreauthorizationTestRequest())
		require.NoError(t, err)
		require.NoError(t, guard.Refund(context.Background()))
		require.NoError(t, guard.Refund(context.Background()))
		require.Equal(t, 1, fixture.wallet.refundCalls)
		require.ErrorIs(t, guard.Finalize(context.Background(), 0.01, "fingerprint"), ErrBalancePreauthorizationAlreadyRefunded)
	})

	t.Run("zero actual", func(t *testing.T) {
		fixture := newPreauthorizationFixture()
		fixture.calculator.zero = true
		guard, err := fixture.service.Preauthorize(context.Background(), balancePreauthorizationTestRequest())
		require.NoError(t, err)
		require.Zero(t, guard.HoldAmount())
		require.NoError(t, guard.Finalize(context.Background(), 0, "zero-cost"))
		require.Equal(t, 1, fixture.wallet.refundCalls)
		require.Zero(t, fixture.wallet.finalizeCalls)
		require.Contains(t, fixture.recorder.snapshot(), "repo_begin_finalize")
		require.Contains(t, fixture.recorder.snapshot(), "repo_complete_refund")
	})
}

func TestBalancePreauthorizationGuardRetriesAfterPGCompletionFailure(t *testing.T) {
	fixture := newPreauthorizationFixture()
	fixture.wallet.finalize = []LiveBalanceResult{
		{Outcome: LiveBalanceOutcomeApplied, State: LiveBalanceAttemptFinalized},
		{Outcome: LiveBalanceOutcomeIdempotent, State: LiveBalanceAttemptFinalized},
	}
	fixture.repo.completeSettlementErrors = []error{errors.New("commit response lost"), nil}
	guard, err := fixture.service.Preauthorize(context.Background(), balancePreauthorizationTestRequest())
	require.NoError(t, err)

	err = guard.Finalize(context.Background(), 0.02, "fingerprint")
	require.ErrorIs(t, err, ErrBillingServiceUnavailable)
	require.NoError(t, guard.Finalize(context.Background(), 0.02, "fingerprint"))
	require.Equal(t, 2, fixture.wallet.finalizeCalls)
	require.Equal(t, 2, fixture.repo.completeSettlementCalls)
}

func TestBalancePreauthorizationGuardContextRoundTrip(t *testing.T) {
	fixture := newPreauthorizationFixture()
	guard, err := fixture.service.Preauthorize(context.Background(), balancePreauthorizationTestRequest())
	require.NoError(t, err)

	ctx := ContextWithBalancePreauthorizationGuard(nil, guard)
	got, ok := BalancePreauthorizationGuardFromContext(ctx)
	require.True(t, ok)
	require.Same(t, guard, got)
	_, ok = BalancePreauthorizationGuardFromContext(context.Background())
	require.False(t, ok)
}

func TestRecoverBalancePreauthorizationRefundsPreparedAttemptThatNeverAuthorized(t *testing.T) {
	fixture := newPreauthorizationFixture()
	fixture.wallet.refund = []LiveBalanceResult{{Outcome: LiveBalanceOutcomeNotFound, State: LiveBalanceAttemptNone}}
	err := fixture.service.RecoverBalancePreauthorization(context.Background(), BalancePreauthorizationRecord{
		RequestID:  "prepared-crash",
		APIKeyID:   7,
		UserID:     42,
		HoldAmount: 0.10,
		Status:     BalanceSettlementPrepared,
	})
	require.NoError(t, err)
	require.Equal(t, []string{"repo_begin_refund", "wallet_refund", "repo_complete_refund"}, fixture.recorder.snapshot())
}

func TestRecoverAuthorizedAfterSuccessfulResponseBeforeUsageTaskSettlesHold(t *testing.T) {
	fixture := newPreauthorizationFixture()
	fixture.wallet.finalize = []LiveBalanceResult{{
		Outcome: LiveBalanceOutcomeApplied,
		State:   LiveBalanceAttemptFinalized,
	}}
	record := BalancePreauthorizationRecord{
		RequestID:                "response-succeeded-worker-never-ran",
		APIKeyID:                 7,
		UserID:                   42,
		AuthorizationFingerprint: "request-payload-hash",
		HoldAmount:               0.10,
		Status:                   BalanceSettlementAuthorized,
	}
	err := fixture.service.RecoverBalancePreauthorization(context.Background(), record)

	require.NoError(t, err)
	require.Equal(t, []string{"repo_begin_finalize", "wallet_finalize", "repo_complete_settlement"}, fixture.recorder.snapshot())
	require.InDelta(t, record.HoldAmount, fixture.repo.finalizedAmount, 1e-12)
	require.NotEmpty(t, fixture.repo.finalizedFingerprint)
	require.InDelta(t, record.HoldAmount, fixture.wallet.lastActual, 1e-12)
	require.Zero(t, fixture.wallet.refundCalls)
}

func TestRecoverAuthorizedMissingWalletStaysRecoverable(t *testing.T) {
	fixture := newPreauthorizationFixture()
	fixture.wallet.finalize = []LiveBalanceResult{{Outcome: LiveBalanceOutcomeNotFound, State: LiveBalanceAttemptNone}}
	err := fixture.service.RecoverBalancePreauthorization(context.Background(), BalancePreauthorizationRecord{
		RequestID:                "authorized-wallet-missing",
		APIKeyID:                 7,
		UserID:                   42,
		AuthorizationFingerprint: "request-payload-hash",
		HoldAmount:               0.10,
		Status:                   BalanceSettlementAuthorized,
	})

	require.ErrorIs(t, err, ErrBillingServiceUnavailable)
	require.Equal(t, []string{"repo_begin_finalize", "wallet_finalize"}, fixture.recorder.snapshot())
	require.Zero(t, fixture.repo.completeSettlementCalls)
	require.Zero(t, fixture.wallet.refundCalls)
}

func TestRecoverBalancePreauthorizationCompletesStaleFinalization(t *testing.T) {
	fixture := newPreauthorizationFixture()
	fixture.wallet.finalize = []LiveBalanceResult{{
		Outcome:      LiveBalanceOutcomeIdempotent,
		State:        LiveBalanceAttemptFinalized,
		ActualAmount: 0.02,
	}}
	err := fixture.service.RecoverBalancePreauthorization(context.Background(), BalancePreauthorizationRecord{
		RequestID:          "stale-finalization",
		APIKeyID:           7,
		UserID:             42,
		RequestFingerprint: "actual-fingerprint",
		HoldAmount:         0.03,
		Amount:             0.02,
		Status:             BalanceSettlementFinalizationPending,
	})
	require.NoError(t, err)
	require.Equal(t, []string{"wallet_finalize", "repo_complete_settlement"}, fixture.recorder.snapshot())
}

func TestRecoverBalancePreauthorizationFailsClosedOnWalletConflict(t *testing.T) {
	fixture := newPreauthorizationFixture()
	fixture.wallet.refund = []LiveBalanceResult{{Outcome: LiveBalanceOutcomeConflict, State: LiveBalanceAttemptFinalized}}
	err := fixture.service.RecoverBalancePreauthorization(context.Background(), BalancePreauthorizationRecord{
		RequestID:  "conflict",
		APIKeyID:   7,
		UserID:     42,
		HoldAmount: 0.10,
		Status:     BalanceSettlementFinalizationPending,
	})
	require.ErrorIs(t, err, ErrBillingServiceUnavailable)
	require.NotContains(t, fixture.recorder.snapshot(), "repo_complete_refund")
}

// streamingPreauthorizationGuard builds a guard whose tracker is active by
// giving the cost stub a positive per-output-token price, so the difference
// method recovers a non-zero output unit price at preauthorization.
func streamingPreauthorizationGuard(t *testing.T, fixture *preauthorizationFixture) *BalancePreauthorizationGuard {
	t.Helper()
	fixture.calculator.outputUnitPrice = 0.001
	existing := LiveBalanceResult{Outcome: LiveBalanceOutcomeApplied, State: LiveBalanceAttemptAuthorized}
	fixture.wallet.existing = &existing
	guard, err := fixture.service.Preauthorize(context.Background(), balancePreauthorizationTestRequest())
	require.NoError(t, err)
	require.NotNil(t, guard)
	return guard
}

// TestObserveStreamingOutputTopsUpOncePerWindowCrossing proves streaming output
// raises the hold only when a reserved output window is crossed, calling the
// wallet top-up a bounded number of times rather than per frame.
func TestObserveStreamingOutputTopsUpOncePerWindowCrossing(t *testing.T) {
	fixture := newPreauthorizationFixture()
	guard := streamingPreauthorizationGuard(t, fixture)
	beforeTopUps := fixture.wallet.topUpCalls

	// Default window is 256 tokens; emit well beyond one window in small frames.
	for i := 0; i < 40; i++ {
		require.NoError(t, guard.ObserveStreamingOutput(context.Background(), 32))
	}
	require.Greater(t, fixture.wallet.topUpCalls, beforeTopUps, "crossing a window must top up")
	// 40*32 = 1280 observed bytes over a 256-token window: a handful of top-ups,
	// never one per frame.
	require.LessOrEqual(t, fixture.wallet.topUpCalls-beforeTopUps, 8)
	require.Greater(t, fixture.wallet.lastTopUpTarget, guard.HoldAmount()-0.0001)
}

// TestObserveStreamingOutputAbortsOnInsufficientBalance proves a failed top-up
// surfaces the 403 withholding error so the caller aborts the upstream stream.
func TestObserveStreamingOutputAbortsOnInsufficientBalance(t *testing.T) {
	fixture := newPreauthorizationFixture()
	fixture.wallet.topUp = []LiveBalanceResult{{Outcome: LiveBalanceOutcomeInsufficient, State: LiveBalanceAttemptAuthorized}}
	guard := streamingPreauthorizationGuard(t, fixture)

	var err error
	for i := 0; i < 40 && err == nil; i++ {
		err = guard.ObserveStreamingOutput(context.Background(), 64)
	}
	require.ErrorIs(t, err, ErrBalanceWithholdingFailed)
}

// TestObserveStreamingOutputNoopWithoutTracker proves per-request (image) guards
// carry no tracker and never attempt streaming top-ups.
func TestObserveStreamingOutputNoopWithoutTracker(t *testing.T) {
	fixture := newPreauthorizationFixture()
	fixture.calculator.perUnitPrice = 0.04
	existing := LiveBalanceResult{Outcome: LiveBalanceOutcomeApplied, State: LiveBalanceAttemptAuthorized}
	fixture.wallet.existing = &existing
	guard, err := fixture.service.Preauthorize(context.Background(), perRequestPreauthorizationRequest(3, "2K"))
	require.NoError(t, err)
	require.NotNil(t, guard)

	for i := 0; i < 100; i++ {
		require.NoError(t, guard.ObserveStreamingOutput(context.Background(), 1024))
	}
	require.Zero(t, fixture.wallet.topUpCalls)
}
