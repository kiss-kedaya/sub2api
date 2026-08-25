package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/shopspring/decimal"
)

const usageBalanceSettlementAdvisoryLock int64 = 0x5355423242414c

type usageBalanceSettlementRepository struct {
	db *sql.DB
}

func NewUsageBalanceSettlementRepository(db *sql.DB) service.UsageBalanceSettlementRepository {
	return &usageBalanceSettlementRepository{db: db}
}

type pendingBalanceSettlement struct {
	id               int64
	userID           int64
	amount           decimal.Decimal
	walletPreapplied bool
}

func (r *usageBalanceSettlementRepository) FlushPendingBalanceSettlements(
	ctx context.Context,
	limit int,
) (_ []service.UsageBalanceSettlementResult, err error) {
	if r == nil || r.db == nil {
		return nil, errors.New("balance settlement database is unavailable")
	}
	if limit <= 0 {
		limit = 1000
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	var retryIDs []int64
	defer func() {
		_ = tx.Rollback()
		if err == nil || len(retryIDs) == 0 {
			return
		}
		retryCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = r.recordBalanceSettlementFailure(retryCtx, retryIDs, err)
	}()
	var leader bool
	if err := tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock($1)`, usageBalanceSettlementAdvisoryLock).Scan(&leader); err != nil {
		return nil, err
	}
	if !leader {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return []service.UsageBalanceSettlementResult{}, nil
	}
	if _, err := tx.ExecContext(ctx, `SET LOCAL lock_timeout = '2s'`); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `SET LOCAL statement_timeout = '12s'`); err != nil {
		return nil, err
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT id, user_id, amount_usd::text, wallet_preapplied
		FROM billing_balance_settlements
		WHERE status = $2 AND available_at <= NOW()
		ORDER BY available_at, id
		LIMIT $1
		FOR UPDATE SKIP LOCKED
	`, limit, service.BalanceSettlementPending)
	if err != nil {
		return nil, err
	}

	pending := make([]pendingBalanceSettlement, 0, limit)
	for rows.Next() {
		var item pendingBalanceSettlement
		var amountText string
		if err := rows.Scan(&item.id, &item.userID, &amountText, &item.walletPreapplied); err != nil {
			_ = rows.Close()
			return nil, err
		}
		item.amount, err = decimal.NewFromString(amountText)
		if err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("parse settlement %d amount: %w", item.id, err)
		}
		pending = append(pending, item)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(pending) == 0 {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return []service.UsageBalanceSettlementResult{}, nil
	}
	retryIDs = make([]int64, 0, len(pending))
	for _, item := range pending {
		retryIDs = append(retryIDs, item.id)
	}

	totals := make(map[int64]decimal.Decimal)
	preappliedTotals := make(map[int64]decimal.Decimal)
	unappliedTotals := make(map[int64]decimal.Decimal)
	counts := make(map[int64]int)
	for _, item := range pending {
		totals[item.userID] = totals[item.userID].Add(item.amount)
		counts[item.userID]++
		if item.walletPreapplied {
			preappliedTotals[item.userID] = preappliedTotals[item.userID].Add(item.amount)
		} else {
			unappliedTotals[item.userID] = unappliedTotals[item.userID].Add(item.amount)
		}
	}

	newBalances, err := applyUsageBalanceSettlementTotals(ctx, tx, unappliedTotals)
	if err != nil {
		return nil, err
	}
	if len(preappliedTotals) > 0 {
		if err := skipLiveBalanceAdjustmentOutbox(ctx, tx); err != nil {
			return nil, err
		}
		preappliedBalances, err := applyUsageBalanceSettlementTotals(ctx, tx, preappliedTotals)
		if err != nil {
			return nil, err
		}
		for userID, balance := range preappliedBalances {
			newBalances[userID] = balance
		}
	}
	validIDs := make([]int64, 0, len(pending))
	invalidIDs := make([]int64, 0)
	for _, item := range pending {
		if _, ok := newBalances[item.userID]; ok {
			validIDs = append(validIDs, item.id)
		} else {
			invalidIDs = append(invalidIDs, item.id)
		}
	}

	if len(validIDs) > 0 {
		markResult, err := tx.ExecContext(ctx, `
			UPDATE billing_balance_settlements
			SET status = $2,
				attempts = attempts + 1,
				applied_at = NOW(),
				last_error = NULL,
				updated_at = NOW()
			WHERE id = ANY($1::bigint[]) AND status = $3
		`, pq.Array(validIDs), service.BalanceSettlementApplied, service.BalanceSettlementPending)
		if err != nil {
			return nil, err
		}
		marked, err := markResult.RowsAffected()
		if err != nil {
			return nil, err
		}
		if marked != int64(len(validIDs)) {
			return nil, fmt.Errorf("balance settlement marked %d of %d valid events", marked, len(validIDs))
		}
	}
	if len(invalidIDs) > 0 {
		terminalResult, err := tx.ExecContext(ctx, `
			UPDATE billing_balance_settlements
			SET status = $2,
				attempts = attempts + 1,
				last_error = 'user is missing or deleted',
				updated_at = NOW()
			WHERE id = ANY($1::bigint[]) AND status = $3
		`, pq.Array(invalidIDs), service.BalanceSettlementTerminal, service.BalanceSettlementPending)
		if err != nil {
			return nil, err
		}
		marked, err := terminalResult.RowsAffected()
		if err != nil {
			return nil, err
		}
		if marked != int64(len(invalidIDs)) {
			return nil, fmt.Errorf("balance settlement marked %d of %d terminal events", marked, len(invalidIDs))
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	retryIDs = nil

	userIDs := sortedBalanceSettlementUserIDs(totals)
	results := make([]service.UsageBalanceSettlementResult, 0, len(newBalances))
	for _, userID := range userIDs {
		newBalance, ok := newBalances[userID]
		if !ok {
			continue
		}
		amount, _ := totals[userID].Float64()
		results = append(results, service.UsageBalanceSettlementResult{
			UserID:     userID,
			EventCount: counts[userID],
			Amount:     amount,
			NewBalance: newBalance,
		})
	}
	return results, nil
}

func applyUsageBalanceSettlementTotals(
	ctx context.Context,
	tx *sql.Tx,
	totals map[int64]decimal.Decimal,
) (map[int64]float64, error) {
	balances := make(map[int64]float64, len(totals))
	if len(totals) == 0 {
		return balances, nil
	}
	userIDs := sortedBalanceSettlementUserIDs(totals)
	amounts := make([]string, len(userIDs))
	for index, userID := range userIDs {
		amounts[index] = totals[userID].StringFixed(service.UsageBillingMonetaryScale)
	}

	rows, err := tx.QueryContext(ctx, `
		WITH deltas(user_id, amount_usd) AS (
			SELECT * FROM unnest($1::bigint[], $2::numeric[])
		)
		UPDATE users AS u
		SET balance = u.balance - d.amount_usd,
			updated_at = NOW()
		FROM deltas AS d
		WHERE u.id = d.user_id AND u.deleted_at IS NULL
		RETURNING u.id, u.balance
	`, pq.Array(userIDs), pq.Array(amounts))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var userID int64
		var balance float64
		if err := rows.Scan(&userID, &balance); err != nil {
			return nil, err
		}
		balances[userID] = balance
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return balances, nil
}

func sortedBalanceSettlementUserIDs(totals map[int64]decimal.Decimal) []int64 {
	userIDs := make([]int64, 0, len(totals))
	for userID := range totals {
		userIDs = append(userIDs, userID)
	}
	sort.Slice(userIDs, func(i, j int) bool { return userIDs[i] < userIDs[j] })
	return userIDs
}

func (r *usageBalanceSettlementRepository) recordBalanceSettlementFailure(
	ctx context.Context,
	ids []int64,
	cause error,
) error {
	if len(ids) == 0 {
		return nil
	}
	message := cause.Error()
	if len(message) > 2000 {
		message = message[:2000]
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE billing_balance_settlements
		SET attempts = attempts + 1,
			available_at = NOW() + (
				LEAST(30::double precision, POWER(2::double precision, LEAST(attempts, 7)) * 0.25)
				* INTERVAL '1 second'
			),
			last_error = $2,
			updated_at = NOW()
		WHERE id = ANY($1::bigint[]) AND status = $3
	`, pq.Array(ids), message, service.BalanceSettlementPending)
	return err
}

func (r *usageBalanceSettlementRepository) DeleteAppliedBalanceSettlements(
	ctx context.Context,
	before time.Time,
	limit int,
) (int64, error) {
	if r == nil || r.db == nil {
		return 0, errors.New("balance settlement database is unavailable")
	}
	if limit <= 0 {
		limit = 5000
	}
	result, err := r.db.ExecContext(ctx, `
		WITH doomed AS (
			SELECT id
			FROM billing_balance_settlements
			WHERE status IN ($3, $4) AND applied_at < $1
			ORDER BY applied_at, id
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		DELETE FROM billing_balance_settlements AS s
		USING doomed
		WHERE s.id = doomed.id
	`, before, limit, service.BalanceSettlementApplied, service.BalanceSettlementRefunded)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
