package service

import (
	"context"
	"fmt"
	"strings"

	dbent "github.com/Wei-Shaw/sub2api/ent"

	entsql "entgo.io/ent/dialect/sql"
)

// ApplyProviderDefaultSettingsOnFirstBind applies provider-specific bootstrap
// settings the first time a user binds a third-party identity. The grant is
// idempotent per user/provider pair.
func (s *AuthService) ApplyProviderDefaultSettingsOnFirstBind(
	ctx context.Context,
	userID int64,
	providerType string,
) error {
	if s == nil || s.entClient == nil || s.settingService == nil || userID <= 0 {
		return nil
	}
	providerType = strings.TrimSpace(providerType)
	eventID := fmt.Sprintf("provider-default:%d:%s", userID, providerType)

	if existingTx := dbent.TxFromContext(ctx); existingTx != nil {
		balanceDelta, err := s.applyProviderDefaultSettingsOnFirstBind(ctx, userID, providerType)
		if err != nil {
			return err
		}
		if balanceDelta != 0 {
			existingTx.OnCommit(func(next dbent.Committer) dbent.Committer {
				return dbent.CommitFunc(func(commitCtx context.Context, tx *dbent.Tx) error {
					if err := next.Commit(commitCtx, tx); err != nil {
						return err
					}
					syncCommittedLiveBalanceAdjustment(s.billingCacheService, userID, eventID, balanceDelta)
					return nil
				})
			})
		}
		return nil
	}

	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin first bind defaults transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	txCtx := dbent.NewTxContext(ctx, tx)
	balanceDelta, err := s.applyProviderDefaultSettingsOnFirstBind(txCtx, userID, providerType)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if balanceDelta != 0 {
		syncCommittedLiveBalanceAdjustment(s.billingCacheService, userID, eventID, balanceDelta)
	}
	return nil
}

func (s *AuthService) applyProviderDefaultSettingsOnFirstBind(
	ctx context.Context,
	userID int64,
	providerType string,
) (float64, error) {
	providerDefaults, enabled, err := s.settingService.ResolveAuthSourceGrantSettings(ctx, providerType, true)
	if err != nil {
		return 0, fmt.Errorf("load auth source defaults: %w", err)
	}
	if !enabled {
		return 0, nil
	}

	client := s.entClient
	if tx := dbent.TxFromContext(ctx); tx != nil {
		client = tx.Client()
	}

	var result entsql.Result
	if err := client.Driver().Exec(
		ctx,
		`INSERT INTO user_provider_default_grants (user_id, provider_type, grant_reason)
VALUES ($1, $2, $3)
ON CONFLICT (user_id, provider_type, grant_reason) DO NOTHING`,
		[]any{userID, strings.TrimSpace(providerType), "first_bind"},
		&result,
	); err != nil {
		return 0, fmt.Errorf("record first bind provider grant: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read first bind provider grant result: %w", err)
	}
	if affected == 0 {
		return 0, nil
	}

	if providerDefaults.Balance != 0 {
		if err := client.User.UpdateOneID(userID).AddBalance(providerDefaults.Balance).Exec(ctx); err != nil {
			return 0, fmt.Errorf("apply first bind balance default: %w", err)
		}
	}
	if providerDefaults.Concurrency != 0 {
		if err := client.User.UpdateOneID(userID).AddConcurrency(providerDefaults.Concurrency).Exec(ctx); err != nil {
			return 0, fmt.Errorf("apply first bind concurrency default: %w", err)
		}
	}
	if s.defaultSubAssigner != nil {
		for _, item := range providerDefaults.Subscriptions {
			if _, _, err := s.defaultSubAssigner.AssignOrExtendSubscription(ctx, &AssignSubscriptionInput{
				UserID:       userID,
				GroupID:      item.GroupID,
				ValidityDays: item.ValidityDays,
				Notes:        "auto assigned by first bind defaults",
			}); err != nil {
				return 0, fmt.Errorf("apply first bind subscription default: %w", err)
			}
		}
	}

	return providerDefaults.Balance, nil
}
