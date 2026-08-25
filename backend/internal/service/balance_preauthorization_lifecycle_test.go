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
	return &CostBreakdown{ActualCost: cost}, nil
}

type preauthorizationWalletStub struct {
	recorder      *preauthorizationCallRecorder
	authorize     LiveBalanceResult
	authorizeErr  error
	existing      *LiveBalanceResult
	existingErr   error
	finalize      []LiveBalanceResult
	finalizeErr   error
	refund        []LiveBalanceResult
	refundErr     error
	lastAttemptID string
	lastFallback  float64
	lastWatermark int64
	lastAllowInit bool
	lastHold      float64
	lastActual    float64
	finalizeCalls int
	refundCalls   int
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

func TestBalancePreauthorizationLifecycleUsesLargestUnifiedPricingScenario(t *testing.T) {
	fixture := newPreauthorizationFixture()
	guard, err := fixture.service.Preauthorize(context.Background(), balancePreauthorizationTestRequest())
	require.NoError(t, err)
	require.NotNil(t, guard)
	require.InDelta(t, 0.028, guard.HoldAmount(), 1e-12)
	require.Equal(t, DefaultBalancePreauthorizationOutputWindow, guard.ReservedOutputTokens())
	require.Len(t, fixture.calculator.inputs, 4)
	require.Equal(t, 100, fixture.calculator.inputs[0].Tokens.InputTokens)
	require.Equal(t, 100, fixture.calculator.inputs[1].Tokens.CacheReadTokens)
	require.Equal(t, 100, fixture.calculator.inputs[2].Tokens.CacheCreationTokens)
	require.Equal(t, 100, fixture.calculator.inputs[3].Tokens.CacheCreationTokens)
	require.Equal(t, 100, fixture.calculator.inputs[3].Tokens.CacheCreation1hTokens)
	for _, input := range fixture.calculator.inputs {
		require.Equal(t, DefaultBalancePreauthorizationOutputWindow, input.Tokens.OutputTokens)
		require.Equal(t, "gpt-test", input.Model)
		require.Equal(t, 1.25, input.RateMultiplier)
	}
	require.InDelta(t, 0.028, fixture.repo.prepared.HoldAmount, 1e-12)
	require.Equal(t, "request-1:7", fixture.wallet.lastAttemptID)
	require.Equal(t, 10.0, fixture.wallet.lastFallback)
	require.Equal(t, int64(17), fixture.wallet.lastWatermark)
	require.Equal(t, []string{
		"price", "price", "price", "price", "repo_prepare", "wallet_authorize_existing", "balance_snapshot", "wallet_authorize", "repo_authorized",
	}, fixture.recorder.snapshot())

	err = guard.Finalize(context.Background(), 0.019999999, " actual-fingerprint ")
	require.NoError(t, err)
	require.Equal(t, 0.02, fixture.repo.finalizedAmount)
	require.Equal(t, "actual-fingerprint", fixture.repo.finalizedFingerprint)
	require.Equal(t, 0.02, fixture.wallet.lastActual)
	require.Equal(t, []string{
		"price", "price", "price", "price", "repo_prepare", "wallet_authorize_existing", "balance_snapshot", "wallet_authorize", "repo_authorized",
		"repo_begin_finalize", "wallet_finalize", "repo_complete_settlement",
	}, fixture.recorder.snapshot())

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
		"price", "price", "price", "price", "repo_prepare", "wallet_authorize_existing", "repo_authorized",
	}, fixture.recorder.snapshot())
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
		"price", "price", "price", "price", "repo_prepare", "wallet_authorize_existing", "balance_snapshot", "wallet_authorize",
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
