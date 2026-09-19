package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
)

// RecoverBalancePreauthorization resumes one durable nonterminal record. It is
// safe to call concurrently across instances because every Redis operation and
// PG transition is idempotent. Callers should source records from
// ListRecoverableBalancePreauthorizations so active requests are not raced.
func (s *BalancePreauthorizationService) RecoverBalancePreauthorization(
	ctx context.Context,
	record BalancePreauthorizationRecord,
) error {
	if s == nil || s.wallet == nil || s.repo == nil {
		return balancePreauthorizationUnavailable(errors.New("balance preauthorization recovery dependency is unavailable"))
	}
	if strings.TrimSpace(record.RequestID) == "" || record.APIKeyID <= 0 || record.UserID <= 0 ||
		record.HoldAmount < 0 || record.Amount < 0 ||
		math.IsNaN(record.HoldAmount) || math.IsInf(record.HoldAmount, 0) ||
		math.IsNaN(record.Amount) || math.IsInf(record.Amount, 0) {
		return balancePreauthorizationUnavailable(ErrInvalidBillingPreauthorizationEstimate)
	}
	ctx = nonNilContext(ctx)
	switch record.Status {
	case BalanceSettlementPrepared:
		if err := s.repo.BeginBalancePreauthorizationRefund(ctx, record.RequestID, record.APIKeyID); err != nil {
			return balancePreauthorizationUnavailable(err)
		}
		return s.recoverBalancePreauthorizationRefund(ctx, record)
	case BalanceSettlementAuthorized:
		var err error
		record, err = s.reconcileAuthorizedBalancePreauthorizationHold(ctx, record)
		if err != nil {
			return err
		}
		// Redis authorization succeeded, but the process may have crashed after
		// returning a successful provider response and before its in-memory usage
		// task reached repo.Apply. The exact spend is unknowable here; refunding
		// would turn that crash window into a free call. Conservatively settle the
		// durable hold. Prepared remains the only state eligible for an abandoned
		// authorization refund.
		record.Amount = QuantizeUsageBillingAmount(record.HoldAmount)
		record.RequestFingerprint = balancePreauthorizationRecoveryHoldFingerprint(record)
		if err := s.repo.BeginBalancePreauthorizationFinalization(
			ctx, record.RequestID, record.APIKeyID, record.Amount, record.RequestFingerprint,
		); err != nil {
			return balancePreauthorizationUnavailable(err)
		}
		if record.Amount == 0 {
			return s.recoverBalancePreauthorizationRefund(ctx, record)
		}
		return s.recoverBalancePreauthorizationSettlement(ctx, record)
	case BalanceSettlementFinalizationPending:
		if QuantizeUsageBillingAmount(record.Amount) == 0 {
			return s.recoverBalancePreauthorizationRefund(ctx, record)
		}
		return s.recoverBalancePreauthorizationSettlement(ctx, record)
	default:
		return balancePreauthorizationUnavailable(fmt.Errorf("unsupported recoverable balance preauthorization status %d", record.Status))
	}
}

func (s *BalancePreauthorizationService) reconcileAuthorizedBalancePreauthorizationHold(
	ctx context.Context,
	record BalancePreauthorizationRecord,
) (BalancePreauthorizationRecord, error) {
	reader, ok := s.wallet.(balancePreauthorizationAttemptReader)
	if !ok {
		return record, balancePreauthorizationUnavailable(errors.New("balance preauthorization attempt reader is unavailable"))
	}
	holdRepo, ok := s.repo.(balancePreauthorizationHoldRepository)
	if !ok {
		return record, balancePreauthorizationUnavailable(errors.New("balance preauthorization hold repository is unavailable"))
	}

	result, err := reader.ReadLiveBalanceAttempt(
		ctx,
		record.UserID,
		BalancePreauthorizationAttemptID(record.RequestID, record.APIKeyID),
	)
	if err != nil {
		return record, balancePreauthorizationUnavailable(err)
	}
	if result.Outcome == LiveBalanceOutcomeNotFound {
		// Preserve the existing fail-closed path. Finalize will also return
		// NotFound, leaving the durable row recoverable instead of inventing state.
		return record, nil
	}
	if !liveBalanceAuthorizationSucceeded(result, record.HoldAmount) {
		// A terminal Redis attempt means finalize/refund won the race. A fresh PG
		// read will expose its durable transition; this stale recovery must stop.
		return record, balancePreauthorizationUnavailable(fmt.Errorf(
			"recover hold returned outcome=%d state=%d",
			result.Outcome,
			result.State,
		))
	}

	cumulativeHold := QuantizeUsageBillingAmount(result.ReservedAmount)
	if err := holdRepo.AdvanceBalancePreauthorizationHold(
		ctx,
		record.RequestID,
		record.APIKeyID,
		cumulativeHold,
	); err != nil {
		return record, balancePreauthorizationUnavailable(err)
	}
	record.HoldAmount = math.Max(record.HoldAmount, cumulativeHold)
	return record, nil
}

func balancePreauthorizationRecoveryHoldFingerprint(record BalancePreauthorizationRecord) string {
	cmd := &UsageBillingCommand{
		UserID:             record.UserID,
		APIKeyID:           record.APIKeyID,
		BillingType:        BillingTypeBalance,
		BalanceCost:        QuantizeUsageBillingAmount(record.HoldAmount),
		RequestPayloadHash: "authorized-hold-recovery:v1:" + strings.TrimSpace(record.RequestID) + ":" + strings.TrimSpace(record.AuthorizationFingerprint),
	}
	cmd.Normalize()
	return cmd.RequestFingerprint
}

func (s *BalancePreauthorizationService) recoverBalancePreauthorizationRefund(
	ctx context.Context,
	record BalancePreauthorizationRecord,
) error {
	result, err := s.wallet.RefundLiveBalance(
		ctx,
		record.UserID,
		BalancePreauthorizationAttemptID(record.RequestID, record.APIKeyID),
	)
	if err != nil {
		return balancePreauthorizationUnavailable(err)
	}
	// NotFound is safe here: live attempts no longer expire. It means no hold was
	// ever created (prepared crash) or Redis lost the whole wallet, while PG has
	// not deducted this request's amount.
	if result.Outcome != LiveBalanceOutcomeNotFound && !liveBalanceRefundSucceeded(result) {
		return balancePreauthorizationUnavailable(fmt.Errorf("recover refund returned outcome=%d state=%d", result.Outcome, result.State))
	}
	if err := s.repo.CompleteBalancePreauthorizationRefund(ctx, record.RequestID, record.APIKeyID); err != nil {
		return balancePreauthorizationUnavailable(err)
	}
	return nil
}

func (s *BalancePreauthorizationService) recoverBalancePreauthorizationSettlement(
	ctx context.Context,
	record BalancePreauthorizationRecord,
) error {
	actual := QuantizeUsageBillingAmount(record.Amount)
	if strings.TrimSpace(record.RequestFingerprint) == "" {
		return balancePreauthorizationUnavailable(ErrInvalidBillingPreauthorizationEstimate)
	}
	result, err := s.wallet.FinalizeLiveBalance(
		ctx,
		record.UserID,
		BalancePreauthorizationAttemptID(record.RequestID, record.APIKeyID),
		actual,
	)
	if err != nil {
		return balancePreauthorizationUnavailable(err)
	}
	if !liveBalanceFinalizationSucceeded(result, actual) {
		return balancePreauthorizationUnavailable(fmt.Errorf("recover settlement returned outcome=%d state=%d", result.Outcome, result.State))
	}
	if err := s.repo.CompleteBalancePreauthorizationSettlement(ctx, record.RequestID, record.APIKeyID); err != nil {
		return balancePreauthorizationUnavailable(err)
	}
	return nil
}
