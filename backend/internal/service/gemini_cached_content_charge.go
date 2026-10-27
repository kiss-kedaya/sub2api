package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

type preparedCachedContentChargeKey struct{}
type preparedCachedContentCharge struct{ intent *MediaBillingIntent }

func capturePreparedCachedContentCharge(ctx context.Context, usage *UsageLog, input *recordUsageCoreInput, cost *CostBreakdown, accountRate float64, subscriptionBill bool) bool {
	capture, ok := ctx.Value(preparedCachedContentChargeKey{}).(*preparedCachedContentCharge)
	if !ok {
		return false
	}
	cmd := buildUsageBillingCommand(usage.RequestID, usage, &postUsageBillingParams{
		Cost: cost, User: input.User, APIKey: input.APIKey, Account: input.Account, Subscription: input.Subscription,
		IsSubscriptionBill: subscriptionBill, AccountRateMultiplier: accountRate, APIKeyService: input.APIKeyService,
		RequestPayloadHash: input.RequestPayloadHash,
	})
	if cmd == nil {
		return true
	}
	if input.Result == nil || !strings.HasPrefix(cmd.RequestID, GeminiCachedContentUsageRequestIDPrefix) {
		return true
	}
	if input.APIKey == nil || usage.UserID != cmd.UserID {
		return true
	}
	if capture != nil {
		capture.intent = &MediaBillingIntent{Command: *cmd, Usage: *usage, QuotaPlatform: input.QuotaPlatform}
		capture.intent.Usage.User, capture.intent.Usage.APIKey, capture.intent.Usage.Account, capture.intent.Usage.Group, capture.intent.Usage.Subscription = nil, nil, nil, nil, nil
	}
	return true
}

// PrepareCachedContentCharge freezes model prices and the existing multiplier
// rules. It executes no ledger effects and persists no usage row.
func (s *GatewayService) PrepareCachedContentCharge(ctx context.Context, input *RecordUsageInput) (*MediaBillingIntent, error) {
	if input == nil || input.APIKey == nil || input.Account == nil {
		return nil, errors.New("cached content billing identity is missing")
	}
	copyInput := *input
	if copyInput.User == nil {
		copyInput.User = copyInput.APIKey.User
	}
	if copyInput.User == nil {
		return nil, errors.New("cached content billing user is missing")
	}
	capture := &preparedCachedContentCharge{}
	if err := s.RecordUsage(context.WithValue(ctx, preparedCachedContentChargeKey{}, capture), &copyInput); err != nil {
		return nil, err
	}
	if capture.intent == nil {
		return nil, errors.New("cached content charge could not be prepared")
	}
	if s.cfg != nil && s.cfg.RunMode == config.RunModeSimple {
		capture.intent.Command.BalanceCost, capture.intent.Command.SubscriptionCost, capture.intent.Command.APIKeyQuotaCost, capture.intent.Command.APIKeyRateLimitCost, capture.intent.Command.AccountQuotaCost = 0, 0, 0, 0, 0
		capture.intent.Command.RequestFingerprint = ""
		capture.intent.Command.Normalize()
	}
	return capture.intent, nil
}

// ApplyCachedContentCharge replays only frozen, credential-free ledger data.
// A ledger commit followed by a process crash is safe: Apply's durable dedup key
// and UsageLogRepository's unique request key make recovery idempotent.
func (s *GatewayService) ApplyCachedContentCharge(ctx context.Context, intent *MediaBillingIntent) error {
	if s == nil || intent == nil || s.usageBillingRepo == nil || s.usageLogRepo == nil {
		return errors.New("cached content settlement is unavailable")
	}
	cmd := &intent.Command
	if cmd.BalancePreauthorized {
		return errors.New("cached content charges cannot use an unstored balance hold")
	}
	if !strings.HasPrefix(cmd.RequestID, GeminiCachedContentUsageRequestIDPrefix) || cmd.RequestID != intent.Usage.RequestID || cmd.UserID != intent.Usage.UserID || cmd.APIKeyID != intent.Usage.APIKeyID || cmd.AccountID != intent.Usage.AccountID {
		return errors.New("cached content charge identity conflict")
	}
	result, err := s.usageBillingRepo.Apply(ctx, cmd)
	if err != nil {
		return err
	}
	if s.billingCacheService != nil {
		_ = s.billingCacheService.InvalidateUserBalance(ctx, cmd.UserID)
		if cmd.SubscriptionID != nil && intent.Usage.GroupID != nil {
			_ = s.billingCacheService.InvalidateSubscription(ctx, cmd.UserID, *intent.Usage.GroupID)
		}
		if result != nil && result.Applied && cmd.APIKeyRateLimitCost > 0 {
			s.billingCacheService.QueueUpdateAPIKeyRateLimitUsage(cmd.APIKeyID, cmd.APIKeyRateLimitCost)
		}
	}
	if result != nil && result.Applied && s.billingCacheService != nil && s.deferredService != nil {
		// Reuse existing quota/notification updates after the authoritative
		// ledger commit. Subscription counters above were invalidated rather
		// than incremented, so suppress that one optimistic update here.
		key := &APIKey{ID: cmd.APIKeyID, UserID: cmd.UserID, GroupID: intent.Usage.GroupID}
		if cmd.APIKeyRateLimitCost > 0 {
			key.RateLimit5h = 1
		}
		cost := &CostBreakdown{ActualCost: intent.Usage.ActualCost, TotalCost: intent.Usage.TotalCost}
		params := &postUsageBillingParams{Cost: cost, User: &User{ID: cmd.UserID}, APIKey: key, Account: &Account{ID: cmd.AccountID, Type: cmd.AccountType}, AccountRateMultiplier: 1, Platform: intent.QuotaPlatform}
		if intent.Usage.AccountRateMultiplier != nil {
			params.AccountRateMultiplier = *intent.Usage.AccountRateMultiplier
		}
		if cmd.SubscriptionID != nil {
			cost.ActualCost = 0
			params.Platform = ""
		}
		// API-key rate counters already updated immediately above.
		key.RateLimit5h = 0
		finalizePostUsageBilling(ctx, params, s.billingDeps(), result, true)
	}
	usage := intent.Usage
	if _, err := s.usageLogRepo.Create(ctx, &usage); err != nil {
		return err
	}
	return nil
}

// Additional repository contract is separate from legacy metadata fixtures.
// Production implementations must support this contract; no charged operation
// falls back to a non-durable save.
type GeminiCachedContentChargeRepository interface {
	CommitChargedMutation(context.Context, int64, string, time.Time, *MediaBillingIntent) error
	CompleteCharge(context.Context, int64, string) error
	ListPendingCharges(context.Context, int) ([]*GeminiCachedContent, error)
}

func (s *GeminiCachedContentService) SetBillingGateway(gateway *GatewayService) {
	s.billingGateway = gateway
}

func (s *GeminiCachedContentService) SaveCharged(ctx context.Context, record *GeminiCachedContent, intent *MediaBillingIntent) error {
	if _, ok := s.repo.(GeminiCachedContentChargeRepository); !ok {
		return errors.New("durable cached content charges are unsupported")
	}
	if intent == nil {
		return errors.New("cached content charge is missing")
	}
	if intent.Command.UserID != record.UserID || intent.Command.APIKeyID != record.APIKeyID || intent.Command.AccountID != record.AccountID {
		return errors.New("cached content charge owner conflict")
	}
	record.BillingPending, record.ChargeIntent = true, intent
	target := record.ExpireTime
	record.ChargeTargetExpire = &target
	return s.repo.Create(ctx, record)
}

func (s *GeminiCachedContentService) CommitChargedMutation(ctx context.Context, m *GeminiCachedContentMutation, target time.Time, intent *MediaBillingIntent) error {
	repo, ok := s.repo.(GeminiCachedContentChargeRepository)
	if !ok || intent == nil {
		return errors.New("durable cached content charges are unsupported")
	}
	if intent.Command.UserID != m.record.UserID || intent.Command.APIKeyID != m.record.APIKeyID || intent.Command.AccountID != m.record.AccountID {
		return errors.New("cached content charge owner conflict")
	}
	if err := repo.CommitChargedMutation(ctx, m.record.ID, m.token, target, intent); err != nil {
		return err
	}
	m.record.BillingPending, m.record.ChargeIntent, m.record.ChargeTargetExpire = true, intent, &target
	return nil
}

func (s *GeminiCachedContentService) SettleCharge(ctx context.Context, record *GeminiCachedContent) error {
	if record.ChargeIntent == nil {
		return nil
	}
	cmd := &record.ChargeIntent.Command
	if cmd.UserID != record.UserID || cmd.APIKeyID != record.APIKeyID || cmd.AccountID != record.AccountID {
		return errors.New("cached content settlement owner conflict")
	}
	if s.billingGateway == nil {
		return errors.New("cached content billing service is unavailable")
	}
	repo, ok := s.repo.(GeminiCachedContentChargeRepository)
	if !ok {
		return errors.New("durable cached content charges are unsupported")
	}
	if err := s.billingGateway.ApplyCachedContentCharge(ctx, record.ChargeIntent); err != nil {
		return err
	}
	if err := repo.CompleteCharge(ctx, record.ID, record.ChargeIntent.Command.RequestID); err != nil {
		return err
	}
	if record.ChargeTargetExpire != nil {
		record.ExpireTime = *record.ChargeTargetExpire
	}
	record.BillingPending, record.ChargeIntent, record.ChargeTargetExpire = false, nil, nil
	return nil
}

func (s *GeminiCachedContentService) RecoverPendingCharges(ctx context.Context) (int, error) {
	repo, ok := s.repo.(GeminiCachedContentChargeRepository)
	if !ok {
		return 0, nil
	}
	records, err := repo.ListPendingCharges(ctx, geminiCachedContentMutationRecoveryBatch)
	if err != nil {
		return 0, err
	}
	count := 0
	var failures []error
	for _, record := range records {
		workCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := s.SettleCharge(workCtx, record)
		cancel()
		if err != nil {
			failures = append(failures, err)
		} else {
			count++
		}
	}
	return count, errors.Join(failures...)
}
