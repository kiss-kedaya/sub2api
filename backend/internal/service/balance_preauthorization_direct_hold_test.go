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
	expiresAt := cmd.ExpiresAt
	if expiresAt.IsZero() {
		expiresAt = time.Now().Add(6 * time.Hour)
	}
	rec := &BalancePreauthorizationRecord{
		RequestID:                strings.TrimSpace(cmd.RequestID),
		APIKeyID:                 cmd.APIKeyID,
		UserID:                   cmd.UserID,
		AuthorizationFingerprint: strings.TrimSpace(cmd.AuthorizationFingerprint),
		HoldAmount:               QuantizeUsageBillingAmount(cmd.HoldAmount),
		Status:                   BalanceSettlementPrepared,
		ExpiresAt:                expiresAt,
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
		res := &UsageBillingApplyResult{Applied: false}
		if cmd.BalancePreauthorized {
			if rec, ok := h.settlements[k]; ok && rec.Status == BalanceSettlementFinalizationPending {
				res.BalanceFinalizationPending = true
			}
		}
		return res, nil
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
		sentinelErr := errors.New("pg deadlocked")
		h.failNextPGHold = sentinelErr
		preauth := newPreauthServiceForHarness(h)
		svc := newBatchService(h, preauth)

		_, err := svc.Submit(ctx, owner, validBatchImageSubmitRequest(), "")
		require.ErrorIs(t, err, sentinelErr)
		// Redis hold must be refunded completely back to $1.00 and PG row must be Refunded!
		require.InDelta(t, 1.0, h.walletBalance[userID], 1e-9)
		require.InDelta(t, 1.0, h.pgBalance[userID], 1e-9)
		require.Len(t, h.settlements, 1)
		for _, rec := range h.settlements {
			require.Equal(t, BalanceSettlementRefunded, rec.Status)
		}
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
	mu        sync.Mutex
	created   []*UsageLog
	failFirst error
}

func (s *cachedChargeUsageLogStub) Create(_ context.Context, log *UsageLog) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failFirst != nil {
		err := s.failFirst
		s.failFirst = nil
		return false, err
	}
	cp := *log
	s.created = append(s.created, &cp)
	return true, nil
}

type p11PlatformQuotaRepoStub struct {
	UserPlatformQuotaRepository
}

type p11CachedChargeCacheStub struct {
	*liveP0BillingCacheStub
	h              *unifiedLiveLedgerHarness
	failNextFinal  error
	platformMu     sync.Mutex
	platformUsage  map[string]float64
	finalizeCalled int
}

func (s *p11CachedChargeCacheStub) AuthorizeLiveBalance(ctx context.Context, userID int64, attemptID string, fallbackBalance, holdAmount float64) (LiveBalanceResult, error) {
	return s.h.AuthorizeLiveBalance(ctx, userID, attemptID, fallbackBalance, holdAmount)
}
func (s *p11CachedChargeCacheStub) AuthorizeExistingLiveBalance(ctx context.Context, userID int64, attemptID string, holdAmount float64) (LiveBalanceResult, error) {
	return s.h.AuthorizeExistingLiveBalance(ctx, userID, attemptID, holdAmount)
}
func (s *p11CachedChargeCacheStub) AuthorizeLiveBalanceAtWatermark(ctx context.Context, userID int64, attemptID string, fallbackBalance float64, fallbackWatermark int64, holdAmount float64) (LiveBalanceResult, error) {
	return s.h.AuthorizeLiveBalanceAtWatermark(ctx, userID, attemptID, fallbackBalance, fallbackWatermark, holdAmount)
}
func (s *p11CachedChargeCacheStub) AuthorizeLiveBalanceAtWatermarkIfSafe(ctx context.Context, userID int64, attemptID string, fallbackBalance float64, fallbackWatermark int64, holdAmount float64, allowInit bool) (LiveBalanceResult, error) {
	return s.h.AuthorizeLiveBalanceAtWatermarkIfSafe(ctx, userID, attemptID, fallbackBalance, fallbackWatermark, holdAmount, allowInit)
}
func (s *p11CachedChargeCacheStub) TopUpLiveBalance(ctx context.Context, userID int64, attemptID string, target float64) (LiveBalanceResult, error) {
	return s.h.TopUpLiveBalance(ctx, userID, attemptID, target)
}
func (s *p11CachedChargeCacheStub) FinalizeLiveBalance(ctx context.Context, userID int64, attemptID string, actual float64) (LiveBalanceResult, error) {
	s.platformMu.Lock()
	s.finalizeCalled++
	failErr := s.failNextFinal
	s.failNextFinal = nil
	s.platformMu.Unlock()
	if failErr != nil {
		return LiveBalanceResult{}, failErr
	}
	return s.h.FinalizeLiveBalance(ctx, userID, attemptID, actual)
}
func (s *p11CachedChargeCacheStub) RefundLiveBalance(ctx context.Context, userID int64, attemptID string) (LiveBalanceResult, error) {
	return s.h.RefundLiveBalance(ctx, userID, attemptID)
}
func (s *p11CachedChargeCacheStub) ReadLiveBalanceAttempt(ctx context.Context, userID int64, attemptID string) (LiveBalanceResult, error) {
	return s.h.ReadLiveBalanceAttempt(ctx, userID, attemptID)
}
func (s *p11CachedChargeCacheStub) GetUserPlatformQuotaCache(context.Context, int64, string) (*UserPlatformQuotaCacheEntry, bool, error) {
	return nil, false, errors.New("cache miss")
}
func (s *p11CachedChargeCacheStub) IncrUserPlatformQuotaUsageCache(_ context.Context, _ int64, platform string, cost float64, _ time.Duration, _ bool) error {
	s.platformMu.Lock()
	defer s.platformMu.Unlock()
	if s.platformUsage == nil {
		s.platformUsage = map[string]float64{}
	}
	s.platformUsage[platform] += cost
	return nil
}

func TestP1_3_PreauthorizeDirectPGHold_SetsShortExpiresAt(t *testing.T) {
	ctx := context.Background()
	const userID int64 = 42
	const keyID int64 = 7

	h := newUnifiedLiveLedgerHarness(userID, 10.0)
	preauth := newPreauthServiceForHarness(h)

	before := time.Now().UTC()
	pgGuard, err := preauth.PreauthorizeDirectPGHold(ctx, "batch-short-ttl", keyID, userID, 1.0)
	require.NoError(t, err)
	require.NotNil(t, pgGuard)

	fixedGuard, err := preauth.PreauthorizeFixedAmount(ctx, "rpc-default-ttl", keyID, userID, "fp-rpc", 1.0)
	require.NoError(t, err)
	require.NotNil(t, fixedGuard)

	pgRec := h.settlements[unifiedRowKey(pgGuard.RequestID(), keyID)]
	require.NotNil(t, pgRec)
	require.WithinDuration(t, before.Add(balancePreauthorizationDirectPGHoldTTL), pgRec.ExpiresAt, 15*time.Second,
		"direct PG holds must expire in ~2m instead of the 6h default")

	fixedRec := h.settlements[unifiedRowKey(fixedGuard.RequestID(), keyID)]
	require.NotNil(t, fixedRec)
	require.True(t, fixedRec.ExpiresAt.Sub(before) > 5*time.Hour,
		"normal PreauthorizeFixedAmount holds must keep the default 6h TTL")
}

func TestP1_1_ApplyCachedContentCharge_RetryAfterExpiredTerminalAttemptAndQuotaOrdering(t *testing.T) {
	ctx := context.Background()
	const userID int64 = 42
	const keyID int64 = 7
	const accountID int64 = 9
	groupID := int64(11)

	t.Run("retry_after_terminal_attempt_ttl_expiry_skips_recovery_and_succeeds", func(t *testing.T) {
		h := newUnifiedLiveLedgerHarness(userID, 10.0)
		cacheStub := &p11CachedChargeCacheStub{
			liveP0BillingCacheStub: newLiveP0BillingCacheStub(10.0),
			h:                      h,
		}
		cfg := &config.Config{RunMode: config.RunModeStandard}
		cfg.Billing.BalancePreauthorizationEnabled = true
		cfg.Database.UserPlatformQuotaFlusherEnabled = true
		bcs := NewBillingCacheService(cacheStub, nil, nil, nil, nil, nil, cfg, nil)
		defer bcs.Stop()

		preauth := NewBalancePreauthorizationService(cfg, NewBillingService(cfg, nil), bcs, h)
		reqID := GeminiCachedContentUsageRequestIDPrefix + "retry-ttl-expired"
		guard, err := preauth.PreauthorizeFixedAmount(ctx, reqID, keyID, userID, "fp-cached-1", 2.0)
		require.NoError(t, err)
		workerGuard, ok := guard.TransferToWorker()
		require.True(t, ok)

		usageLogs := &cachedChargeUsageLogStub{failFirst: errors.New("temporary usage_log write failure")}
		gw := &GatewayService{
			cfg:                   cfg,
			billingService:        NewBillingService(cfg, nil),
			billingCacheService:   bcs,
			usageBillingRepo:      h,
			usageLogRepo:          usageLogs,
			deferredService:       &DeferredService{},
			userPlatformQuotaRepo: &p11PlatformQuotaRepoStub{},
		}
		intent := &MediaBillingIntent{
			QuotaPlatform: PlatformGemini,
			Command: UsageBillingCommand{
				RequestID:            reqID,
				UserID:               userID,
				APIKeyID:             keyID,
				AccountID:            accountID,
				BalanceCost:          1.25,
				APIKeyQuotaCost:      1.25,
				APIKeyRateLimitCost:  1.25,
				BalancePreauthorized: true,
				RequestPayloadHash:   "hash-cached-1",
			},
			Usage: UsageLog{
				RequestID:  reqID,
				UserID:     userID,
				APIKeyID:   keyID,
				AccountID:  accountID,
				GroupID:    &groupID,
				ActualCost: 1.25,
				TotalCost:  1.25,
			},
		}
		intent.Command.Normalize()

		// Attempt 1: Apply + Finalize succeed (PG moves to BalanceSettlementPending),
		// then usageLogRepo.Create fails transiently.
		err = gw.ApplyCachedContentCharge(ContextWithBalancePreauthorizationGuard(ctx, workerGuard), intent)
		require.ErrorContains(t, err, "temporary usage_log write failure")
		require.Equal(t, BalanceSettlementPending, h.settlements[unifiedRowKey(reqID, keyID)].Status)
		require.InDelta(t, 8.75, h.walletBalance[userID], 1e-9)

		// Simulate >15m passing so Redis expires the terminal attempt key!
		h.mu.Lock()
		delete(h.walletAttempts, BalancePreauthorizationAttemptID(reqID, keyID))
		h.mu.Unlock()
		cacheStub.platformMu.Lock()
		finalizeCallsBefore := cacheStub.finalizeCalled
		cacheStub.platformMu.Unlock()

		// Attempt 2: background retry with NO guard (guard == nil). Because PG row
		// is already Pending (BalanceFinalizationPending == false), recovery must
		// NOT call RecoverBalancePreauthorization against the expired Redis key.
		err = gw.ApplyCachedContentCharge(ctx, intent)
		require.NoError(t, err)
		cacheStub.platformMu.Lock()
		finalizeCallsAfter := cacheStub.finalizeCalled
		cacheStub.platformMu.Unlock()
		require.Equal(t, finalizeCallsBefore, finalizeCallsAfter, "must not re-invoke FinalizeLiveBalance after settlement already left FinalizationPending")
		require.Len(t, usageLogs.created, 1)
		require.InDelta(t, 8.75, h.walletBalance[userID], 1e-9)
	})

	t.Run("applied_side_effects_run_before_transient_finalize_failure", func(t *testing.T) {
		h := newUnifiedLiveLedgerHarness(userID, 10.0)
		cacheStub := &p11CachedChargeCacheStub{
			liveP0BillingCacheStub: newLiveP0BillingCacheStub(10.0),
			h:                      h,
			failNextFinal:          errors.New("transient redis timeout"),
		}
		cfg := &config.Config{RunMode: config.RunModeStandard}
		cfg.Billing.BalancePreauthorizationEnabled = true
		cfg.Database.UserPlatformQuotaFlusherEnabled = true
		bcs := NewBillingCacheService(cacheStub, nil, nil, nil, nil, nil, cfg, nil)
		defer bcs.Stop()

		preauth := NewBalancePreauthorizationService(cfg, NewBillingService(cfg, nil), bcs, h)
		reqID := GeminiCachedContentUsageRequestIDPrefix + "quota-order-1"
		guard, err := preauth.PreauthorizeFixedAmount(ctx, reqID, keyID, userID, "fp-cached-2", 2.0)
		require.NoError(t, err)
		workerGuard, ok := guard.TransferToWorker()
		require.True(t, ok)

		usageLogs := &cachedChargeUsageLogStub{}
		gw := &GatewayService{
			cfg:                   cfg,
			billingService:        NewBillingService(cfg, nil),
			billingCacheService:   bcs,
			usageBillingRepo:      h,
			usageLogRepo:          usageLogs,
			deferredService:       &DeferredService{},
			userPlatformQuotaRepo: &p11PlatformQuotaRepoStub{},
		}
		intent := &MediaBillingIntent{
			QuotaPlatform: PlatformGemini,
			Command: UsageBillingCommand{
				RequestID:            reqID,
				UserID:               userID,
				APIKeyID:             keyID,
				AccountID:            accountID,
				BalanceCost:          1.50,
				APIKeyQuotaCost:      1.50,
				APIKeyRateLimitCost:  1.50,
				BalancePreauthorized: true,
				RequestPayloadHash:   "hash-cached-2",
			},
			Usage: UsageLog{
				RequestID:  reqID,
				UserID:     userID,
				APIKeyID:   keyID,
				AccountID:  accountID,
				GroupID:    &groupID,
				ActualCost: 1.50,
				TotalCost:  1.50,
			},
		}
		intent.Command.Normalize()

		// Attempt 1: Apply succeeds (Applied=true), then FinalizeLiveBalance fails transiently.
		err = gw.ApplyCachedContentCharge(ContextWithBalancePreauthorizationGuard(ctx, workerGuard), intent)
		require.ErrorContains(t, err, "transient redis timeout")
		require.Equal(t, BalanceSettlementFinalizationPending, h.settlements[unifiedRowKey(reqID, keyID)].Status)

		// All Applied-only accounting side effects MUST already have been recorded!
		cacheStub.mu.Lock()
		quotaIncBefore := cacheStub.quotaIncrements[keyID]
		cacheStub.mu.Unlock()
		require.InDelta(t, 1.50, quotaIncBefore, 1e-9, "API key quota must update before Finalize fails")
		require.Eventually(t, func() bool {
			cacheStub.mu.Lock()
			defer cacheStub.mu.Unlock()
			return math.Abs(cacheStub.rateLimitUpdates[keyID]-1.50) < 1e-9
		}, 2*time.Second, 10*time.Millisecond, "API key rate limit must update before Finalize fails")
		cacheStub.platformMu.Lock()
		platUsed := cacheStub.platformUsage[PlatformGemini]
		cacheStub.platformMu.Unlock()
		require.InDelta(t, 1.50, platUsed, 1e-9, "user-platform quota must update before Finalize fails")

		// Attempt 2: background retry (guard == nil). Row is STILL FinalizationPending,
		// so RecoverBalancePreauthorization runs and succeeds without double-counting quotas!
		err = gw.ApplyCachedContentCharge(ctx, intent)
		require.NoError(t, err)
		require.Equal(t, BalanceSettlementPending, h.settlements[unifiedRowKey(reqID, keyID)].Status)
		require.InDelta(t, 8.50, h.walletBalance[userID], 1e-9)
		require.Len(t, usageLogs.created, 1)
		cacheStub.mu.Lock()
		quotaIncAfter := cacheStub.quotaIncrements[keyID]
		cacheStub.mu.Unlock()
		require.InDelta(t, 1.50, quotaIncAfter, 1e-9, "retry must not double-count API key quota")
		cacheStub.platformMu.Lock()
		platUsedAfter := cacheStub.platformUsage[PlatformGemini]
		cacheStub.platformMu.Unlock()
		require.InDelta(t, 1.50, platUsedAfter, 1e-9, "retry must not double-count user-platform quota")
	})
}

func TestP1_1_SettleMediaBillingJob_RetrySkipsRecoveryWhenFinalizationNotPending(t *testing.T) {
	ctx := context.Background()
	cache := newLiveP0BillingCacheStub(100)
	cfg := &config.Config{RunMode: config.RunModeStandard}
	billingCache := NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	defer billingCache.Stop()

	memRepo := &mediaJobMemoryRepo{jobs: map[string]*MediaBillingJob{}, balance: 100}
	wrapper := &liveP0MediaRepoErrWrapper{
		mediaJobMemoryRepo: memRepo,
		// Applied=false AND BalanceFinalizationPending=false simulates a retry after
		// Apply + CompleteBalancePreauthorizationSettlement already finished (and
		// after Redis's 15m terminal attempt TTL expired).
		applyRes: &UsageBillingApplyResult{Applied: false, BalanceFinalizationPending: false},
	}
	usageLogs := &mediaUsageMemoryRepo{}
	// Deliberately leave usageBillingRepo == nil so any accidental call to
	// RecoverBalancePreauthorization would fail immediately.
	svc := &OpenAIGatewayService{
		cfg:                 cfg,
		mediaBillingJobs:    wrapper,
		usageLogRepo:        usageLogs,
		billingCacheService: billingCache,
	}

	job := &MediaBillingJob{
		ID:        "media_retry_after_15m_ttl",
		Kind:      MediaBillingKindImage,
		UserID:    42,
		APIKeyID:  78,
		AccountID: 9,
		GroupID:   11,
		Status:    MediaBillingBilled,
		Intent: &MediaBillingIntent{
			Command: UsageBillingCommand{
				RequestID:            "image:retry-settled",
				UserID:               42,
				APIKeyID:             78,
				AccountID:            9,
				BalanceCost:          0.75,
				BalancePreauthorized: true,
			},
			Usage: UsageLog{
				RequestID:  "image:retry-settled",
				UserID:     42,
				APIKeyID:   78,
				AccountID:  9,
				ActualCost: 0.75,
				TotalCost:  0.75,
			},
		},
	}
	memRepo.jobs[job.ID] = job

	err := svc.settleMediaBillingJob(ctx, job, nil)
	require.NoError(t, err, "retry after settlement left FinalizationPending must succeed without calling RecoverBalancePreauthorization")
	require.Equal(t, MediaBillingDone, memRepo.jobs[job.ID].Status)
	require.Len(t, usageLogs.logs, 1)
}
