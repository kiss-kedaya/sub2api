//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// unifiedLiveLedgerHarness models the real Redis live_wallet + PostgreSQL
// users.balance / frozen_balance / billing_balance_settlements state machine in
// memory so tests can verify cross-path concurrency, outbox skipping, crash
// recovery, and zero double-debit without external services.
type unifiedLiveLedgerHarness struct {
	mu sync.Mutex

	pgBalance      map[int64]float64
	pgFrozen       map[int64]float64
	outboxEvents   []float64
	outboxSkipped  int
	dedup          map[string]string
	settlements    map[string]*BalancePreauthorizationRecord
	mediaJobs      map[string]*MediaBillingJob
	failNextPGHold error
	failNextJob    error

	walletBalance  map[int64]float64
	walletAttempts map[string]*unifiedWalletAttempt
}

type unifiedWalletAttempt struct {
	userID   int64
	reserved float64
	actual   float64
	state    LiveBalanceAttemptState
}

func newUnifiedLiveLedgerHarness(userID int64, initialBalance float64) *unifiedLiveLedgerHarness {
	return &unifiedLiveLedgerHarness{
		pgBalance:      map[int64]float64{userID: initialBalance},
		pgFrozen:       map[int64]float64{userID: 0},
		dedup:          map[string]string{},
		settlements:    map[string]*BalancePreauthorizationRecord{},
		mediaJobs:      map[string]*MediaBillingJob{},
		walletBalance:  map[int64]float64{userID: initialBalance},
		walletAttempts: map[string]*unifiedWalletAttempt{},
	}
}

func unifiedRowKey(requestID string, apiKeyID int64) string {
	return strings.TrimSpace(requestID) + "|" + strings.TrimSpace(BalancePreauthorizationAttemptID(requestID, apiKeyID))
}

// --- Wallet methods ---

func (h *unifiedLiveLedgerHarness) AuthorizeLiveBalance(ctx context.Context, userID int64, attemptID string, fallbackBalance, holdAmount float64) (LiveBalanceResult, error) {
	return h.AuthorizeLiveBalanceAtWatermarkIfSafe(ctx, userID, attemptID, fallbackBalance, 0, holdAmount, true)
}

func (h *unifiedLiveLedgerHarness) AuthorizeExistingLiveBalance(ctx context.Context, userID int64, attemptID string, holdAmount float64) (LiveBalanceResult, error) {
	return h.AuthorizeLiveBalanceAtWatermarkIfSafe(ctx, userID, attemptID, 0, 0, holdAmount, false)
}

func (h *unifiedLiveLedgerHarness) AuthorizeLiveBalanceAtWatermark(ctx context.Context, userID int64, attemptID string, fallbackBalance float64, fallbackWatermark int64, holdAmount float64) (LiveBalanceResult, error) {
	return h.AuthorizeLiveBalanceAtWatermarkIfSafe(ctx, userID, attemptID, fallbackBalance, fallbackWatermark, holdAmount, true)
}

func (h *unifiedLiveLedgerHarness) AuthorizeLiveBalanceAtWatermarkIfSafe(_ context.Context, userID int64, attemptID string, fallbackBalance float64, _ int64, holdAmount float64, allowInit bool) (LiveBalanceResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if existing, ok := h.walletAttempts[attemptID]; ok {
		return LiveBalanceResult{Outcome: LiveBalanceOutcomeIdempotent, State: existing.state, ReservedAmount: existing.reserved, ActualAmount: existing.actual}, nil
	}
	bal, ok := h.walletBalance[userID]
	if !ok {
		if !allowInit {
			return LiveBalanceResult{Outcome: LiveBalanceOutcomeNotFound}, nil
		}
		bal = fallbackBalance
		h.walletBalance[userID] = bal
	}
	hold := QuantizeUsageBillingAmount(holdAmount)
	if bal+1e-12 < hold {
		return LiveBalanceResult{Outcome: LiveBalanceOutcomeInsufficient, AvailableBalance: bal}, nil
	}
	h.walletBalance[userID] = QuantizeUsageBillingAmount(bal - hold)
	h.walletAttempts[attemptID] = &unifiedWalletAttempt{userID: userID, reserved: hold, state: LiveBalanceAttemptAuthorized}
	return LiveBalanceResult{Outcome: LiveBalanceOutcomeApplied, State: LiveBalanceAttemptAuthorized, ReservedAmount: hold, AvailableBalance: h.walletBalance[userID]}, nil
}

func (h *unifiedLiveLedgerHarness) TopUpLiveBalance(_ context.Context, userID int64, attemptID string, targetHoldAmount float64) (LiveBalanceResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	att, ok := h.walletAttempts[attemptID]
	if !ok {
		return LiveBalanceResult{Outcome: LiveBalanceOutcomeNotFound}, nil
	}
	if att.state != LiveBalanceAttemptAuthorized {
		return LiveBalanceResult{Outcome: LiveBalanceOutcomeConflict, State: att.state, ReservedAmount: att.reserved}, nil
	}
	target := QuantizeUsageBillingAmount(targetHoldAmount)
	if target <= att.reserved {
		return LiveBalanceResult{Outcome: LiveBalanceOutcomeIdempotent, State: att.state, ReservedAmount: att.reserved, AvailableBalance: h.walletBalance[userID]}, nil
	}
	delta := QuantizeUsageBillingAmount(target - att.reserved)
	if h.walletBalance[userID]+1e-12 < delta {
		return LiveBalanceResult{Outcome: LiveBalanceOutcomeInsufficient, AvailableBalance: h.walletBalance[userID], ReservedAmount: att.reserved}, nil
	}
	h.walletBalance[userID] = QuantizeUsageBillingAmount(h.walletBalance[userID] - delta)
	att.reserved = target
	return LiveBalanceResult{Outcome: LiveBalanceOutcomeApplied, State: LiveBalanceAttemptAuthorized, ReservedAmount: att.reserved, AvailableBalance: h.walletBalance[userID]}, nil
}

func (h *unifiedLiveLedgerHarness) ReadLiveBalanceAttempt(_ context.Context, _ int64, attemptID string) (LiveBalanceResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	att, ok := h.walletAttempts[attemptID]
	if !ok {
		return LiveBalanceResult{Outcome: LiveBalanceOutcomeNotFound}, nil
	}
	return LiveBalanceResult{Outcome: LiveBalanceOutcomeApplied, State: att.state, ReservedAmount: att.reserved, ActualAmount: att.actual}, nil
}

func (h *unifiedLiveLedgerHarness) FinalizeLiveBalance(_ context.Context, userID int64, attemptID string, actualAmount float64) (LiveBalanceResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	att, ok := h.walletAttempts[attemptID]
	if !ok {
		return LiveBalanceResult{Outcome: LiveBalanceOutcomeNotFound}, nil
	}
	actual := QuantizeUsageBillingAmount(actualAmount)
	if att.state == LiveBalanceAttemptFinalized {
		return LiveBalanceResult{Outcome: LiveBalanceOutcomeIdempotent, State: att.state, ReservedAmount: att.reserved, ActualAmount: att.actual, AvailableBalance: h.walletBalance[userID]}, nil
	}
	if att.state != LiveBalanceAttemptAuthorized {
		return LiveBalanceResult{Outcome: LiveBalanceOutcomeConflict, State: att.state}, nil
	}
	diff := QuantizeUsageBillingAmount(att.reserved - actual)
	h.walletBalance[userID] = QuantizeUsageBillingAmount(h.walletBalance[userID] + diff)
	att.actual = actual
	att.state = LiveBalanceAttemptFinalized
	return LiveBalanceResult{Outcome: LiveBalanceOutcomeApplied, State: LiveBalanceAttemptFinalized, ReservedAmount: att.reserved, ActualAmount: actual, AvailableBalance: h.walletBalance[userID]}, nil
}

func (h *unifiedLiveLedgerHarness) RefundLiveBalance(_ context.Context, userID int64, attemptID string) (LiveBalanceResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	att, ok := h.walletAttempts[attemptID]
	if !ok {
		return LiveBalanceResult{Outcome: LiveBalanceOutcomeNotFound}, nil
	}
	if att.state == LiveBalanceAttemptRefunded {
		return LiveBalanceResult{Outcome: LiveBalanceOutcomeIdempotent, State: LiveBalanceAttemptRefunded, ReservedAmount: att.reserved, ActualAmount: 0, AvailableBalance: h.walletBalance[userID]}, nil
	}
	if att.state != LiveBalanceAttemptAuthorized {
		return LiveBalanceResult{Outcome: LiveBalanceOutcomeConflict, State: att.state}, nil
	}
	h.walletBalance[userID] = QuantizeUsageBillingAmount(h.walletBalance[userID] + att.reserved)
	att.actual = 0
	att.state = LiveBalanceAttemptRefunded
	return LiveBalanceResult{Outcome: LiveBalanceOutcomeApplied, State: LiveBalanceAttemptRefunded, ReservedAmount: att.reserved, ActualAmount: 0, AvailableBalance: h.walletBalance[userID]}, nil
}

// --- UsageBillingRepository + Snapshot + Hold + MediaBillingJobRepository ---

func (h *unifiedLiveLedgerHarness) LoadLiveBalanceInitializationSnapshot(_ context.Context, userID int64) (LiveBalanceInitializationSnapshot, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	hasUnsettled := false
	for _, rec := range h.settlements {
		if rec.UserID == userID && (rec.Status == BalanceSettlementAuthorized || rec.Status == BalanceSettlementFinalizationPending || rec.Status == BalanceSettlementPending) {
			hasUnsettled = true
			break
		}
	}
	return LiveBalanceInitializationSnapshot{Balance: h.pgBalance[userID], Watermark: 0, HasUnsettled: hasUnsettled}, nil
}

func (h *unifiedLiveLedgerHarness) PrepareBalancePreauthorization(_ context.Context, cmd *BalancePreauthorizationCommand) (*BalancePreauthorizationRecord, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	k := unifiedRowKey(cmd.RequestID, cmd.APIKeyID)
	if existing, ok := h.settlements[k]; ok {
		cp := *existing
		return &cp, nil
	}
	rec := &BalancePreauthorizationRecord{
		RequestID:                strings.TrimSpace(cmd.RequestID),
		APIKeyID:                 cmd.APIKeyID,
		UserID:                   cmd.UserID,
		AuthorizationFingerprint: strings.TrimSpace(cmd.AuthorizationFingerprint),
		HoldAmount:               QuantizeUsageBillingAmount(cmd.HoldAmount),
		Status:                   BalanceSettlementPrepared,
		ExpiresAt:                time.Now().Add(time.Hour),
		UpdatedAt:                time.Now(),
	}
	h.settlements[k] = rec
	cp := *rec
	return &cp, nil
}

func (h *unifiedLiveLedgerHarness) MarkBalancePreauthorizationAuthorized(_ context.Context, requestID string, apiKeyID int64) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	rec, ok := h.settlements[unifiedRowKey(requestID, apiKeyID)]
	if !ok || (rec.Status != BalanceSettlementPrepared && rec.Status != BalanceSettlementAuthorized) {
		return ErrUsageBillingRequestConflict
	}
	rec.Status = BalanceSettlementAuthorized
	return nil
}

func (h *unifiedLiveLedgerHarness) AdvanceBalancePreauthorizationHold(_ context.Context, requestID string, apiKeyID int64, holdAmount float64) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	rec, ok := h.settlements[unifiedRowKey(requestID, apiKeyID)]
	if !ok || rec.Status != BalanceSettlementAuthorized {
		return ErrUsageBillingRequestConflict
	}
	rec.HoldAmount = math.Max(rec.HoldAmount, QuantizeUsageBillingAmount(holdAmount))
	return nil
}

func (h *unifiedLiveLedgerHarness) BeginBalancePreauthorizationFinalization(_ context.Context, requestID string, apiKeyID int64, amount float64, fp string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	rec, ok := h.settlements[unifiedRowKey(requestID, apiKeyID)]
	if !ok {
		return ErrUsageBillingRequestConflict
	}
	q := QuantizeUsageBillingAmount(amount)
	if rec.Status == BalanceSettlementAuthorized {
		rec.Status = BalanceSettlementFinalizationPending
		rec.Amount = q
		rec.RequestFingerprint = fp
		return nil
	}
	if (rec.Status == BalanceSettlementFinalizationPending || rec.Status == BalanceSettlementPending || rec.Status == BalanceSettlementApplied || rec.Status == BalanceSettlementRefunded) &&
		rec.Amount == q && rec.RequestFingerprint == fp {
		return nil
	}
	return ErrUsageBillingRequestConflict
}

func (h *unifiedLiveLedgerHarness) CompleteBalancePreauthorizationSettlement(_ context.Context, requestID string, apiKeyID int64) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	rec, ok := h.settlements[unifiedRowKey(requestID, apiKeyID)]
	if !ok {
		return ErrUsageBillingRequestConflict
	}
	if rec.Status == BalanceSettlementPending || rec.Status == BalanceSettlementApplied {
		return nil
	}
	if rec.Status != BalanceSettlementFinalizationPending || rec.Amount <= 0 || strings.TrimSpace(rec.RequestFingerprint) == "" {
		return ErrUsageBillingRequestConflict
	}
	if strings.HasPrefix(rec.RequestFingerprint, BalancePreauthorizationDirectPGHoldFingerprintPrefix) {
		rec.Status = BalanceSettlementApplied
	} else {
		rec.Status = BalanceSettlementPending
	}
	return nil
}

func (h *unifiedLiveLedgerHarness) BeginBalancePreauthorizationRefund(_ context.Context, requestID string, apiKeyID int64) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	rec, ok := h.settlements[unifiedRowKey(requestID, apiKeyID)]
	if !ok {
		return ErrUsageBillingRequestConflict
	}
	if rec.Status == BalanceSettlementRefunded || (rec.Status == BalanceSettlementFinalizationPending && rec.Amount == 0) {
		return nil
	}
	if rec.Status != BalanceSettlementPrepared && rec.Status != BalanceSettlementAuthorized {
		return ErrUsageBillingRequestConflict
	}
	rec.Status = BalanceSettlementFinalizationPending
	rec.Amount = 0
	return nil
}

func (h *unifiedLiveLedgerHarness) CompleteBalancePreauthorizationRefund(_ context.Context, requestID string, apiKeyID int64) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	rec, ok := h.settlements[unifiedRowKey(requestID, apiKeyID)]
	if !ok {
		return ErrUsageBillingRequestConflict
	}
	if rec.Status == BalanceSettlementRefunded {
		return nil
	}
	if rec.Status != BalanceSettlementFinalizationPending || rec.Amount != 0 {
		return ErrUsageBillingRequestConflict
	}
	rec.Status = BalanceSettlementRefunded
	return nil
}

func (h *unifiedLiveLedgerHarness) ListRecoverableBalancePreauthorizations(context.Context, time.Time, time.Time, int) ([]BalancePreauthorizationRecord, error) {
	return nil, nil
}

func (h *unifiedLiveLedgerHarness) Apply(_ context.Context, cmd *UsageBillingCommand) (*UsageBillingApplyResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	cmd.Normalize()
	k := unifiedRowKey(cmd.RequestID, cmd.APIKeyID)
	if fp, dup := h.dedup[k]; dup {
		if fp != cmd.RequestFingerprint {
			return nil, ErrUsageBillingRequestConflict
		}
		return &UsageBillingApplyResult{Applied: false}, nil
	}
	res := &UsageBillingApplyResult{Applied: true}
	if cmd.BalancePreauthorized {
		rec, ok := h.settlements[k]
		if !ok || (rec.Status != BalanceSettlementAuthorized && !(rec.Status == BalanceSettlementFinalizationPending && rec.Amount == cmd.BalanceCost && rec.RequestFingerprint == cmd.RequestFingerprint)) {
			return nil, ErrUsageBillingRequestConflict
		}
		rec.Status = BalanceSettlementFinalizationPending
		rec.Amount = cmd.BalanceCost
		rec.RequestFingerprint = cmd.RequestFingerprint
		res.BalanceFinalizationPending = true
	} else if cmd.BalanceCost > 0 {
		h.pgBalance[cmd.UserID] = QuantizeUsageBillingAmount(h.pgBalance[cmd.UserID] - cmd.BalanceCost)
		nb := h.pgBalance[cmd.UserID]
		res.NewBalance = &nb
	}
	h.dedup[k] = cmd.RequestFingerprint
	return res, nil
}

func (h *unifiedLiveLedgerHarness) reserveDirectPGLocked(cmd *BatchImageBalanceHoldCommand) (*BatchImageBalanceHoldResult, error) {
	if h.failNextPGHold != nil {
		err := h.failNextPGHold
		h.failNextPGHold = nil
		return nil, err
	}
	hold := QuantizeUsageBillingAmount(cmd.HoldAmount)
	if hold <= 0 {
		return &BatchImageBalanceHoldResult{Applied: true}, nil
	}
	if h.pgBalance[cmd.UserID]+1e-12 < hold {
		return nil, ErrBatchImageInsufficientBalance
	}
	if preauthID := strings.TrimSpace(cmd.PreauthRequestID); preauthID != "" {
		rec, ok := h.settlements[unifiedRowKey(preauthID, cmd.APIKeyID)]
		if !ok || rec.Status != BalanceSettlementAuthorized {
			return nil, ErrUsageBillingRequestConflict
		}
		rec.Status = BalanceSettlementFinalizationPending
		rec.Amount = hold
		rec.RequestFingerprint = BalancePreauthorizationDirectPGHoldFingerprintPrefix + preauthID
		h.outboxSkipped++
	} else {
		h.outboxEvents = append(h.outboxEvents, -hold)
	}
	h.pgBalance[cmd.UserID] = QuantizeUsageBillingAmount(h.pgBalance[cmd.UserID] - hold)
	h.pgFrozen[cmd.UserID] = QuantizeUsageBillingAmount(h.pgFrozen[cmd.UserID] + hold)
	nb := h.pgBalance[cmd.UserID]
	nf := h.pgFrozen[cmd.UserID]
	return &BatchImageBalanceHoldResult{Applied: true, NewBalance: &nb, FrozenBalance: &nf}, nil
}

func (h *unifiedLiveLedgerHarness) ReserveBatchImageBalance(_ context.Context, cmd *BatchImageBalanceHoldCommand) (*BatchImageBalanceHoldResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	cmd.Normalize()
	k := unifiedRowKey(cmd.RequestID, cmd.APIKeyID)
	if fp, dup := h.dedup[k]; dup {
		if fp != cmd.RequestFingerprint {
			return nil, ErrUsageBillingRequestConflict
		}
		return &BatchImageBalanceHoldResult{Applied: false}, nil
	}
	res, err := h.reserveDirectPGLocked(cmd)
	if err != nil {
		return nil, err
	}
	h.dedup[k] = cmd.RequestFingerprint
	return res, nil
}

func (h *unifiedLiveLedgerHarness) CaptureBatchImageBalance(context.Context, *BatchImageBalanceHoldCommand) (*BatchImageBalanceHoldResult, error) {
	return &BatchImageBalanceHoldResult{Applied: true}, nil
}

func (h *unifiedLiveLedgerHarness) ReleaseBatchImageBalance(_ context.Context, cmd *BatchImageBalanceHoldCommand) (*BatchImageBalanceHoldResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	hold := QuantizeUsageBillingAmount(cmd.HoldAmount)
	h.pgBalance[cmd.UserID] = QuantizeUsageBillingAmount(h.pgBalance[cmd.UserID] + hold)
	h.pgFrozen[cmd.UserID] = QuantizeUsageBillingAmount(h.pgFrozen[cmd.UserID] - hold)
	h.outboxEvents = append(h.outboxEvents, +hold)
	nb := h.pgBalance[cmd.UserID]
	nf := h.pgFrozen[cmd.UserID]
	return &BatchImageBalanceHoldResult{Applied: true, NewBalance: &nb, FrozenBalance: &nf}, nil
}

func (h *unifiedLiveLedgerHarness) flushPendingSettlements() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	flushed := 0
	for _, rec := range h.settlements {
		if rec.Status == BalanceSettlementPending {
			h.pgBalance[rec.UserID] = QuantizeUsageBillingAmount(h.pgBalance[rec.UserID] - rec.Amount)
			rec.Status = BalanceSettlementApplied
			flushed++
		}
	}
	return flushed
}

func (h *unifiedLiveLedgerHarness) CreateMediaBillingJob(_ context.Context, input *MediaBillingJob) (*MediaBillingJob, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.failNextJob != nil {
		err := h.failNextJob
		h.failNextJob = nil
		return nil, err
	}
	if existing, ok := h.mediaJobs[input.ID]; ok {
		cp := *existing
		cp.PreauthRequestID = ""
		return &cp, nil
	}
	cp := *input
	if cp.Kind == MediaBillingKindGrokVideo && cp.SubscriptionID == nil && cp.ReservedAmount > 0 {
		if _, err := h.reserveDirectPGLocked(&BatchImageBalanceHoldCommand{
			UserID:           cp.UserID,
			APIKeyID:         cp.APIKeyID,
			BatchID:          cp.ID,
			HoldAmount:       cp.ReservedAmount,
			PreauthRequestID: cp.PreauthRequestID,
		}); err != nil {
			if errors.Is(err, ErrBatchImageInsufficientBalance) {
				return nil, ErrBalanceWithholdingFailed.WithCause(err)
			}
			return nil, err
		}
	}
	stored := cp
	stored.PreauthRequestID = ""
	h.mediaJobs[cp.ID] = &stored
	return &cp, nil
}

func (h *unifiedLiveLedgerHarness) SubmitMediaBillingJob(context.Context, string, string, json.RawMessage) error {
	return nil
}
func (h *unifiedLiveLedgerHarness) GetMediaBillingJob(context.Context, int64, int64, string) (*MediaBillingJob, error) {
	return nil, ErrMediaBillingJobNotFound
}
func (h *unifiedLiveLedgerHarness) GetMediaBillingJobByID(context.Context, string) (*MediaBillingJob, error) {
	return nil, ErrMediaBillingJobNotFound
}
func (h *unifiedLiveLedgerHarness) PrepareMediaBillingIntent(context.Context, string, *MediaBillingIntent) (*MediaBillingIntent, error) {
	return nil, nil
}
func (h *unifiedLiveLedgerHarness) ApplyMediaBillingJob(context.Context, string) (*UsageBillingApplyResult, error) {
	return &UsageBillingApplyResult{}, nil
}
func (h *unifiedLiveLedgerHarness) FailMediaBillingJob(context.Context, string) (*UsageBillingApplyResult, error) {
	return &UsageBillingApplyResult{}, nil
}
func (h *unifiedLiveLedgerHarness) ReconcileMediaBillingJob(context.Context, string) error {
	return nil
}
func (h *unifiedLiveLedgerHarness) AbandonMediaBillingJob(context.Context, string) (bool, error) {
	return false, nil
}
func (h *unifiedLiveLedgerHarness) CompleteMediaBillingJob(context.Context, string) error { return nil }
func (h *unifiedLiveLedgerHarness) ListMediaBillingJobs(context.Context, int) ([]*MediaBillingJob, error) {
	return nil, nil
}
func (h *unifiedLiveLedgerHarness) RetryMediaBillingJob(context.Context, string, time.Duration) error {
	return nil
}

func newPreauthServiceForHarness(h *unifiedLiveLedgerHarness) *BalancePreauthorizationService {
	cfg := &config.Config{}
	cfg.Billing.BalancePreauthorizationEnabled = true
	return &BalancePreauthorizationService{
		cfg:             cfg,
		costCalculator:  NewBillingService(cfg, nil),
		snapshotReader:  h,
		wallet:          h,
		watermarkWallet: h,
		repo:            h,
	}
}

func TestBILLH2_ConcurrentTextVsDirectPGHold_ZeroOutboxGapAndNoDoubleDebit(t *testing.T) {
	ctx := context.Background()
	const userID int64 = 42
	const keyID int64 = 7

	t.Run("text hold immediately blocks batch image / video direct PG hold", func(t *testing.T) {
		h := newUnifiedLiveLedgerHarness(userID, 10.0)
		preauth := newPreauthServiceForHarness(h)

		// Active text request holds $8.00 in live_wallet (PG still shows $10.00).
		textGuard, err := preauth.PreauthorizeFixedAmount(ctx, "local:text-1", keyID, userID, "fp-text-1", 8.0)
		require.NoError(t, err)
		require.NotNil(t, textGuard)
		require.InDelta(t, 2.0, h.walletBalance[userID], 1e-9)
		require.InDelta(t, 10.0, h.pgBalance[userID], 1e-9)

		// Batch image / video tries to reserve $5.00 -> blocked immediately by live_wallet!
		_, err = preauth.PreauthorizeDirectPGHold(ctx, "batch-image:b1", keyID, userID, 5.0)
		require.ErrorIs(t, err, ErrBalanceWithholdingFailed)
		require.InDelta(t, 2.0, h.walletBalance[userID], 1e-9)
		require.InDelta(t, 10.0, h.pgBalance[userID], 1e-9)
		require.Zero(t, h.pgFrozen[userID])
	})

	t.Run("batch image direct PG hold immediately blocks text with zero outbox gap and zero double debit", func(t *testing.T) {
		h := newUnifiedLiveLedgerHarness(userID, 10.0)
		preauth := newPreauthServiceForHarness(h)

		// Step 1: PreauthorizeDirectPGHold debits live_wallet $10 -> $2 BEFORE PG tx.
		pgGuard, err := preauth.PreauthorizeDirectPGHold(ctx, "batch-image:b2", keyID, userID, 8.0)
		require.NoError(t, err)
		require.NotNil(t, pgGuard)
		require.Equal(t, "pg-hold:batch-image:b2", pgGuard.RequestID())
		require.InDelta(t, 2.0, h.walletBalance[userID], 1e-9)

		// Concurrent text request for $5.00 arrives 0ms later -> rejected immediately!
		_, err = preauth.PreauthorizeFixedAmount(ctx, "local:text-2", keyID, userID, "fp-text-2", 5.0)
		require.ErrorIs(t, err, ErrBalanceWithholdingFailed)

		// Step 2: PG hold tx skips outbox, advances settlement row Authorized->FinalizationPending,
		// and moves $8.00 users.balance -> frozen_balance.
		res, err := h.ReserveBatchImageBalance(ctx, &BatchImageBalanceHoldCommand{
			RequestID:        BatchImageHoldRequestID("b2"),
			APIKeyID:         keyID,
			UserID:           userID,
			BatchID:          "b2",
			HoldAmount:       8.0,
			PreauthRequestID: pgGuard.RequestID(),
		})
		require.NoError(t, err)
		require.True(t, res.Applied)
		require.Equal(t, 1, h.outboxSkipped)
		require.Empty(t, h.outboxEvents, "PG hold must skip outbox so live_wallet is not debited twice")
		require.InDelta(t, 2.0, h.pgBalance[userID], 1e-9)
		require.InDelta(t, 8.0, h.pgFrozen[userID], 1e-9)

		// Step 3: FinalizeDirectPGHold finalizes Redis and transitions row directly to Applied (NOT Pending).
		require.NoError(t, pgGuard.FinalizeDirectPGHold(ctx))
		rec := h.settlements[unifiedRowKey("pg-hold:batch-image:b2", keyID)]
		require.Equal(t, BalanceSettlementApplied, rec.Status)

		// FlushPendingBalanceSettlements must NOT deduct users.balance a second time!
		require.Zero(t, h.flushPendingSettlements())
		require.InDelta(t, 2.0, h.pgBalance[userID], 1e-9)
		require.InDelta(t, 2.0, h.walletBalance[userID], 1e-9)

		// Snapshot must report HasUnsettled == false (no stuck unsettled row).
		snap, err := h.LoadLiveBalanceInitializationSnapshot(ctx, userID)
		require.NoError(t, err)
		require.False(t, snap.HasUnsettled)
	})
}

func TestBILLH2_DirectPGHoldCrashRecovery(t *testing.T) {
	ctx := context.Background()
	const userID int64 = 42
	const keyID int64 = 7

	t.Run("crash before PG commit (Authorized on pg-hold:*) refunds Redis instead of charging", func(t *testing.T) {
		h := newUnifiedLiveLedgerHarness(userID, 10.0)
		preauth := newPreauthServiceForHarness(h)

		guard, err := preauth.PreauthorizeDirectPGHold(ctx, "batch-image:crash-before", keyID, userID, 6.0)
		require.NoError(t, err)
		require.InDelta(t, 4.0, h.walletBalance[userID], 1e-9)
		require.InDelta(t, 10.0, h.pgBalance[userID], 1e-9)

		// Process crashes before PG hold tx commits; recovery sees Authorized row.
		rec := *h.settlements[unifiedRowKey(guard.RequestID(), keyID)]
		require.Equal(t, BalanceSettlementAuthorized, rec.Status)
		require.NoError(t, preauth.RecoverBalancePreauthorization(ctx, rec))

		// Redis balance fully restored, PG row Refunded, user lost $0.
		require.InDelta(t, 10.0, h.walletBalance[userID], 1e-9)
		require.InDelta(t, 10.0, h.pgBalance[userID], 1e-9)
		require.Equal(t, BalanceSettlementRefunded, h.settlements[unifiedRowKey(guard.RequestID(), keyID)].Status)
	})

	t.Run("crash after PG commit (FinalizationPending on pg-direct-hold:*) finalizes Redis directly to Applied", func(t *testing.T) {
		h := newUnifiedLiveLedgerHarness(userID, 10.0)
		preauth := newPreauthServiceForHarness(h)

		guard, err := preauth.PreauthorizeDirectPGHold(ctx, "media-video:crash-after", keyID, userID, 6.0)
		require.NoError(t, err)

		// PG hold commits (moves row to FinalizationPending with pg-direct-hold:* fingerprint).
		_, err = h.ReserveBatchImageBalance(ctx, &BatchImageBalanceHoldCommand{
			RequestID:        "batch_image_hold:crash-after",
			APIKeyID:         keyID,
			UserID:           userID,
			BatchID:          "crash-after",
			HoldAmount:       6.0,
			PreauthRequestID: guard.RequestID(),
		})
		require.NoError(t, err)

		// Process crashes before guard.FinalizeDirectPGHold runs; recovery finishes it!
		rec := *h.settlements[unifiedRowKey(guard.RequestID(), keyID)]
		require.Equal(t, BalanceSettlementFinalizationPending, rec.Status)
		require.True(t, strings.HasPrefix(rec.RequestFingerprint, BalancePreauthorizationDirectPGHoldFingerprintPrefix))
		require.NoError(t, preauth.RecoverBalancePreauthorization(ctx, rec))

		// Row moves directly to Applied; FlushPendingBalanceSettlements never double-debits PG.
		require.Equal(t, BalanceSettlementApplied, h.settlements[unifiedRowKey(guard.RequestID(), keyID)].Status)
		require.Zero(t, h.flushPendingSettlements())
		require.InDelta(t, 4.0, h.walletBalance[userID], 1e-9)
		require.InDelta(t, 4.0, h.pgBalance[userID], 1e-9)
		require.InDelta(t, 6.0, h.pgFrozen[userID], 1e-9)
	})
}

func TestBILLH2_BatchImagePublicService_SubmitPreauthAndPGRollbackRefund(t *testing.T) {
	ctx := context.Background()
	owner := testBatchImageOwner()
	userID := owner.UserID
	keyID := owner.APIKeyID

	newBatchService := func(h *unifiedLiveLedgerHarness, preauth *BalancePreauthorizationService) *BatchImagePublicService {
		svc, _, _, _, _ := newTestBatchImagePublicService(true)
		svc.BillingRepo = h
		svc.BalancePreauthorization = preauth
		return svc
	}

	t.Run("text hold blocks BatchImagePublicService.Submit with ErrBatchImageInsufficientBalance", func(t *testing.T) {
		// validBatchImageSubmitRequest() holds $0.30 (2 items * $0.25 * 0.6).
		h := newUnifiedLiveLedgerHarness(userID, 0.50)
		preauth := newPreauthServiceForHarness(h)
		svc := newBatchService(h, preauth)

		// Active text hold drains live_wallet $0.50 -> $0.15 (PG still $0.50).
		_, err := preauth.PreauthorizeFixedAmount(ctx, "local:text-block", keyID, userID, "fp", 0.35)
		require.NoError(t, err)

		// Batch submit needs $0.30 hold -> must fail with ErrBatchImageInsufficientBalance.
		_, err = svc.Submit(ctx, owner, validBatchImageSubmitRequest(), "")
		require.ErrorIs(t, err, ErrBatchImageInsufficientBalance)
		require.InDelta(t, 0.15, h.walletBalance[userID], 1e-9)
		require.InDelta(t, 0.50, h.pgBalance[userID], 1e-9)
	})

	t.Run("PG hold failure refunds Redis live_wallet hold cleanly", func(t *testing.T) {
		h := newUnifiedLiveLedgerHarness(userID, 1.0)
		h.failNextPGHold = errors.New("pg deadlocked")
		preauth := newPreauthServiceForHarness(h)
		svc := newBatchService(h, preauth)

		_, err := svc.Submit(ctx, owner, validBatchImageSubmitRequest(), "")
		require.Error(t, err)
		// Redis hold must be refunded completely back to $1.00!
		require.InDelta(t, 1.0, h.walletBalance[userID], 1e-9)
		require.InDelta(t, 1.0, h.pgBalance[userID], 1e-9)
	})

	t.Run("successful BatchImagePublicService.Submit blocks concurrent text immediately", func(t *testing.T) {
		h := newUnifiedLiveLedgerHarness(userID, 0.50)
		preauth := newPreauthServiceForHarness(h)
		svc := newBatchService(h, preauth)

		batch, err := svc.Submit(ctx, owner, validBatchImageSubmitRequest(), "")
		require.NoError(t, err)
		require.NotNil(t, batch)
		require.InDelta(t, 0.20, h.walletBalance[userID], 1e-9)
		require.InDelta(t, 0.20, h.pgBalance[userID], 1e-9)
		require.InDelta(t, 0.30, h.pgFrozen[userID], 1e-9)
		require.Equal(t, 1, h.outboxSkipped)

		// Concurrent text for $0.30 rejected immediately (0ms outbox gap).
		_, err = preauth.PreauthorizeFixedAmount(ctx, "local:text-after-batch", keyID, userID, "fp", 0.30)
		require.ErrorIs(t, err, ErrBalanceWithholdingFailed)
	})
}

func TestBILLH2_AsyncVideoCreateGrokVideoBillingJob_UnifiesLiveWalletAndRefundsOnPGError(t *testing.T) {
	ctx := context.Background()
	const userID int64 = 42
	const keyID int64 = 7
	groupID := int64(5)

	makeVideoService := func(h *unifiedLiveLedgerHarness, preauth *BalancePreauthorizationService) *OpenAIGatewayService {
		cfg := &config.Config{}
		cfg.Billing.BalancePreauthorizationEnabled = true
		cfg.Default.RateMultiplier = 1.0
		billingSvc := NewBillingService(cfg, nil)
		svc := &OpenAIGatewayService{
			cfg:                     cfg,
			billingService:          billingSvc,
			mediaBillingJobs:        h,
			usageBillingRepo:        h,
			balancePreauthorization: preauth,
		}
		return svc
	}

	group := &Group{ID: groupID, Platform: PlatformGrok, Status: StatusActive, RateMultiplier: 1.0}
	apiKey := &APIKey{ID: keyID, UserID: userID, GroupID: &groupID, Group: group, User: &User{ID: userID}}
	account := &Account{ID: 9, Platform: PlatformGrok, Type: AccountTypeAPIKey}
	videoInput := func() (*OpenAIRecordUsageInput, GrokVideoPendingBilling) {
		// grok-imagine-video @ 720p ($0.07/s) * 10s = $0.70 hold.
		return &OpenAIRecordUsageInput{
				Result:  &OpenAIForwardResult{Model: "grok-imagine-video", BillingModel: "grok-imagine-video"},
				APIKey:  apiKey,
				User:    apiKey.User,
				Account: account,
			}, GrokVideoPendingBilling{
				GroupID:              groupID,
				Model:                "grok-imagine-video",
				BillingModel:         "grok-imagine-video",
				VideoResolution:      "720p",
				VideoDurationSeconds: 10,
			}
	}

	t.Run("text hold blocks async video creation", func(t *testing.T) {
		h := newUnifiedLiveLedgerHarness(userID, 1.0)
		preauth := newPreauthServiceForHarness(h)
		svc := makeVideoService(h, preauth)

		// Text holds $0.50 -> $0.50 left in live_wallet, video needs $0.70 -> blocked!
		_, err := preauth.PreauthorizeFixedAmount(ctx, "local:text-v1", keyID, userID, "fp", 0.50)
		require.NoError(t, err)

		in, pending := videoInput()
		_, err = svc.CreateGrokVideoBillingJob(ctx, in, pending)
		require.ErrorIs(t, err, ErrBalanceWithholdingFailed)
		require.InDelta(t, 1.0, h.pgBalance[userID], 1e-9)
	})

	t.Run("async video hold immediately blocks text and skips outbox", func(t *testing.T) {
		h := newUnifiedLiveLedgerHarness(userID, 1.0)
		preauth := newPreauthServiceForHarness(h)
		svc := makeVideoService(h, preauth)

		in, pending := videoInput()
		job, err := svc.CreateGrokVideoBillingJob(ctx, in, pending)
		require.NoError(t, err)
		require.NotNil(t, job)
		require.InDelta(t, 0.70, job.ReservedAmount, 1e-9)
		require.InDelta(t, 0.30, h.walletBalance[userID], 1e-9)
		require.InDelta(t, 0.30, h.pgBalance[userID], 1e-9)
		require.InDelta(t, 0.70, h.pgFrozen[userID], 1e-9)
		require.Equal(t, 1, h.outboxSkipped)

		// Concurrent text for $0.50 immediately rejected!
		_, err = preauth.PreauthorizeFixedAmount(ctx, "local:text-after-video", keyID, userID, "fp", 0.50)
		require.ErrorIs(t, err, ErrBalanceWithholdingFailed)

		// Flush must not double-debit PG.
		require.Zero(t, h.flushPendingSettlements())
		require.InDelta(t, 0.30, h.pgBalance[userID], 1e-9)
	})

	t.Run("PG CreateMediaBillingJob failure refunds Redis live_wallet hold", func(t *testing.T) {
		h := newUnifiedLiveLedgerHarness(userID, 1.0)
		h.failNextJob = errors.New("pg connection reset")
		preauth := newPreauthServiceForHarness(h)
		svc := makeVideoService(h, preauth)

		in, pending := videoInput()
		_, err := svc.CreateGrokVideoBillingJob(ctx, in, pending)
		require.Error(t, err)
		require.InDelta(t, 1.0, h.walletBalance[userID], 1e-9)
		require.InDelta(t, 1.0, h.pgBalance[userID], 1e-9)
	})
}

func TestBILLH2_GeminiCachedContent_PreauthorizedApplyAndCrashRecovery(t *testing.T) {
	ctx := context.Background()
	const userID int64 = 42
	const keyID int64 = 7

	h := newUnifiedLiveLedgerHarness(userID, 10.0)
	preauth := newPreauthServiceForHarness(h)
	cfg := &config.Config{}
	cfg.Billing.BalancePreauthorizationEnabled = true

	usageLogs := &cachedChargeUsageLogStub{}
	billingCache := &BillingCacheService{cache: &unifiedBillingCacheAdapter{h: h}, cfg: cfg}
	gatewaySvc := &GatewayService{
		cfg:                 cfg,
		billingService:      NewBillingService(cfg, nil),
		billingCacheService: billingCache,
		usageBillingRepo:    h,
		usageLogRepo:        usageLogs,
	}

	reqID := GeminiCachedContentCreateUsageRequestID("pub-cache-1")
	guard, err := preauth.PreauthorizeFixedAmount(ctx, reqID, keyID, userID, "fp-cache-1", 4.0)
	require.NoError(t, err)
	require.InDelta(t, 6.0, h.walletBalance[userID], 1e-9)

	intent := &MediaBillingIntent{
		Command: UsageBillingCommand{
			RequestID:            reqID,
			APIKeyID:             keyID,
			UserID:               userID,
			AccountID:            3,
			BillingType:          BillingTypeBalance,
			BalanceCost:          2.5,
			BalancePreauthorized: true,
		},
		Usage: UsageLog{
			RequestID:  reqID,
			APIKeyID:   keyID,
			UserID:     userID,
			AccountID:  3,
			ActualCost: 2.5,
			TotalCost:  2.5,
		},
	}
	intent.Command.Normalize()

	// Normal hot-path settlement with guard in context finalizes $2.50 and refunds $1.50 surplus.
	workerGuard, ok := guard.TransferToWorker()
	require.True(t, ok)
	require.NoError(t, gatewaySvc.ApplyCachedContentCharge(ContextWithBalancePreauthorizationGuard(ctx, workerGuard), intent))
	require.InDelta(t, 7.5, h.walletBalance[userID], 1e-9)
	require.Equal(t, BalanceSettlementPending, h.settlements[unifiedRowKey(reqID, keyID)].Status)
	require.Equal(t, 1, h.flushPendingSettlements())
	require.InDelta(t, 7.5, h.pgBalance[userID], 1e-9)

	// Crash-recovery path (no guard in context, e.g. RecoverPendingCharges after restart):
	reqID2 := GeminiCachedContentCreateUsageRequestID("pub-cache-2")
	_, err = preauth.PreauthorizeFixedAmount(ctx, reqID2, keyID, userID, "fp-cache-2", 3.0)
	require.NoError(t, err)
	require.InDelta(t, 4.5, h.walletBalance[userID], 1e-9)

	intent2 := &MediaBillingIntent{
		Command: UsageBillingCommand{
			RequestID:            reqID2,
			APIKeyID:             keyID,
			UserID:               userID,
			AccountID:            3,
			BillingType:          BillingTypeBalance,
			BalanceCost:          1.5,
			BalancePreauthorized: true,
		},
		Usage: UsageLog{
			RequestID:  reqID2,
			APIKeyID:   keyID,
			UserID:     userID,
			AccountID:  3,
			ActualCost: 1.5,
			TotalCost:  1.5,
		},
	}
	intent2.Command.Normalize()
	require.NoError(t, gatewaySvc.ApplyCachedContentCharge(ctx, intent2))
	require.InDelta(t, 6.0, h.walletBalance[userID], 1e-9)
	require.Equal(t, 1, h.flushPendingSettlements())
	require.InDelta(t, 6.0, h.pgBalance[userID], 1e-9)
}

func TestOpenAIGatewayRecordUsage_WSGuardRequestIDUnifiedWithSettlement(t *testing.T) {
	ctx := context.Background()
	const userID int64 = 42
	const keyID int64 = 7
	groupID := int64(2)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "model_pricing.json"), []byte(`{
		"gpt-4o": {
			"litellm_provider": "openai",
			"mode": "chat",
			"input_cost_per_token": 0.00001,
			"output_cost_per_token": 0.00002
		}
	}`), 0o644))

	h := newUnifiedLiveLedgerHarness(userID, 10.0)
	preauth := newPreauthServiceForHarness(h)
	cfg := &config.Config{}
	cfg.Billing.BalancePreauthorizationEnabled = true
	cfg.Default.RateMultiplier = 1.0
	cfg.Pricing.DataDir = dir
	cfg.Pricing.UpdateIntervalHours = 100000

	pricingSvc := NewPricingService(cfg, nil)
	require.NoError(t, pricingSvc.Initialize())

	usageLogs := &cachedChargeUsageLogStub{}
	svc := &OpenAIGatewayService{
		cfg:              cfg,
		billingService:   NewBillingService(cfg, pricingSvc),
		usageBillingRepo: h,
		usageLogRepo:     usageLogs,
		deferredService:  &DeferredService{},
	}

	turnReqID := "ws:conn-1:turn:2:1"
	guard, err := preauth.PreauthorizeFixedAmount(ctx, turnReqID, keyID, userID, "fp-turn-2", 1.0)
	require.NoError(t, err)
	workerGuard, ok := guard.TransferToWorker()
	require.True(t, ok)

	group := &Group{ID: groupID, Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 1.0}
	apiKey := &APIKey{ID: keyID, UserID: userID, GroupID: &groupID, Group: group, User: &User{ID: userID}}
	account := &Account{ID: 5, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}

	err = svc.RecordUsage(ContextWithBalancePreauthorizationGuard(ctx, workerGuard), &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{
			RequestID:    "resp_upstream_xyz",
			Model:        "gpt-4o",
			OpenAIWSMode: true,
			Usage:        OpenAIUsage{InputTokens: 1000, OutputTokens: 500},
		},
		APIKey:  apiKey,
		User:    apiKey.User,
		Account: account,
	})
	require.NoError(t, err)
	require.Len(t, usageLogs.created, 1)
	require.Greater(t, usageLogs.created[0].ActualCost, 0.0)
	require.Equal(t, turnReqID, usageLogs.created[0].RequestID, "billing RequestID must match guard.RequestID()")
	require.NotNil(t, usageLogs.created[0].ResponseRequestID)
	require.Equal(t, "resp_upstream_xyz", *usageLogs.created[0].ResponseRequestID, "upstream response ID must stay preserved in ResponseRequestID")
	require.Equal(t, BalanceSettlementPending, h.settlements[unifiedRowKey(turnReqID, keyID)].Status)
	require.InDelta(t, 10.0-usageLogs.created[0].ActualCost, h.walletBalance[userID], 1e-8, "unused portion of $1.00 hold must be refunded on finalization")
}

// --- Minimal test stubs for service-level unified tests ---

type unifiedBillingCacheAdapter struct {
	BillingCache
	h *unifiedLiveLedgerHarness
}

func (a *unifiedBillingCacheAdapter) AuthorizeLiveBalance(ctx context.Context, userID int64, attemptID string, fallbackBalance, holdAmount float64) (LiveBalanceResult, error) {
	return a.h.AuthorizeLiveBalance(ctx, userID, attemptID, fallbackBalance, holdAmount)
}
func (a *unifiedBillingCacheAdapter) AuthorizeExistingLiveBalance(ctx context.Context, userID int64, attemptID string, holdAmount float64) (LiveBalanceResult, error) {
	return a.h.AuthorizeExistingLiveBalance(ctx, userID, attemptID, holdAmount)
}
func (a *unifiedBillingCacheAdapter) AuthorizeLiveBalanceAtWatermark(ctx context.Context, userID int64, attemptID string, fallbackBalance float64, fallbackWatermark int64, holdAmount float64) (LiveBalanceResult, error) {
	return a.h.AuthorizeLiveBalanceAtWatermark(ctx, userID, attemptID, fallbackBalance, fallbackWatermark, holdAmount)
}
func (a *unifiedBillingCacheAdapter) AuthorizeLiveBalanceAtWatermarkIfSafe(ctx context.Context, userID int64, attemptID string, fallbackBalance float64, fallbackWatermark int64, holdAmount float64, allowInit bool) (LiveBalanceResult, error) {
	return a.h.AuthorizeLiveBalanceAtWatermarkIfSafe(ctx, userID, attemptID, fallbackBalance, fallbackWatermark, holdAmount, allowInit)
}
func (a *unifiedBillingCacheAdapter) TopUpLiveBalance(ctx context.Context, userID int64, attemptID string, target float64) (LiveBalanceResult, error) {
	return a.h.TopUpLiveBalance(ctx, userID, attemptID, target)
}
func (a *unifiedBillingCacheAdapter) FinalizeLiveBalance(ctx context.Context, userID int64, attemptID string, actual float64) (LiveBalanceResult, error) {
	return a.h.FinalizeLiveBalance(ctx, userID, attemptID, actual)
}
func (a *unifiedBillingCacheAdapter) RefundLiveBalance(ctx context.Context, userID int64, attemptID string) (LiveBalanceResult, error) {
	return a.h.RefundLiveBalance(ctx, userID, attemptID)
}
func (a *unifiedBillingCacheAdapter) ReadLiveBalanceAttempt(ctx context.Context, userID int64, attemptID string) (LiveBalanceResult, error) {
	return a.h.ReadLiveBalanceAttempt(ctx, userID, attemptID)
}

type cachedChargeUsageLogStub struct {
	UsageLogRepository
	mu      sync.Mutex
	created []*UsageLog
}

func (s *cachedChargeUsageLogStub) Create(_ context.Context, log *UsageLog) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *log
	s.created = append(s.created, &cp)
	return true, nil
}
