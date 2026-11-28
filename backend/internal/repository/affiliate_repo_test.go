package repository

import (
	"context"
	"os"
	"strings"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/stretchr/testify/require"
)

func newMockAffiliateRepo(t *testing.T) (*affiliateRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	return &affiliateRepository{client: client}, mock
}

func TestAffiliateUserOverviewSQLIncludesMaturedFrozenQuota(t *testing.T) {
	query := strings.Join(strings.Fields(affiliateUserOverviewSQL), " ")

	require.Contains(t, query, "ua.aff_quota + COALESCE(matured.matured_frozen_quota, 0)")
	require.Contains(t, query, "frozen_until <= NOW()")
	require.Contains(t, query, "action = 'refund_reversal'")
	require.Contains(t, query, "ua_cap.aff_frozen_quota")
}

func TestAffiliateRecordQueriesUseLedgerAuditFields(t *testing.T) {
	source, err := os.ReadFile("affiliate_repo.go")
	require.NoError(t, err)
	content := string(source)

	require.Contains(t, content, "JOIN payment_orders po ON po.id = ual.source_order_id")
	require.Contains(t, content, "ual.amount::double precision")
	require.Contains(t, content, "ual.balance_after::double precision")
	require.NotContains(t, content, "parseAffiliateRebateAmount")
	require.NotContains(t, content, `"current_balance": "u.balance"`)
}

// TestAffiliateRepository_ReverseQuotaForOrder_FullFrozenRefundClearsFrozenUntilAndNeverResurrects
// verifies that refunding a frozen accrual deducts from aff_frozen_quota, clears
// frozen_until on the accrue row, and a subsequent ThawFrozenQuota after maturity
// adds 0 resurrected quota.
func TestAffiliateRepository_ReverseQuotaForOrder_FullFrozenRefundClearsFrozenUntilAndNeverResurrects(t *testing.T) {
	repo, mock := newMockAffiliateRepo(t)
	ctx := context.Background()

	// 1. ReverseQuotaForOrder(orderID=100, ratio=1.0) on a 10.0 frozen accrual.
	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)SELECT user_id, COALESCE\(source_user_id, 0\).*FROM user_affiliate_ledger.*source_order_id = \$1 AND action = 'accrue'`).
		WithArgs(int64(100)).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "source_user_id"}).AddRow(int64(7), int64(8)))
	mock.ExpectQuery(`SELECT aff_quota::double precision, aff_frozen_quota::double precision FROM user_affiliates WHERE user_id = \$1 FOR UPDATE`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"aff_quota", "aff_frozen_quota"}).AddRow(0.0, 10.0))
	// Pre-reversal thaw of already-matured rows returns 0.
	mock.ExpectQuery(`(?s)WITH matured_rows AS.*action = 'accrue'.*frozen_until <= NOW\(\)`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"net_matured"}).AddRow(0.0))
	// Lock this order's accrue rows (10.0, still frozen).
	mock.ExpectQuery(`(?s)SELECT amount::double precision, \(frozen_until IS NOT NULL\).*FROM user_affiliate_ledger.*FOR UPDATE`).
		WithArgs(int64(100), int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"amount", "is_frozen"}).AddRow(10.0, true))
	// Prior reversals = 0.
	mock.ExpectQuery(`(?s)SELECT COALESCE\(SUM\(ABS\(amount\)\), 0\)::double precision.*action = 'refund_reversal'`).
		WithArgs(int64(100), int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"already_reversed"}).AddRow(0.0))
	// Deduct 10.0 from aff_frozen_quota (available stays 0, frozen becomes 0, history -10).
	mock.ExpectExec(`(?s)UPDATE user_affiliates.*SET aff_quota = \$2, aff_frozen_quota = \$3`).
		WithArgs(int64(7), 0.0, 0.0, 10.0).
		WillReturnResult(sqlmock.NewResult(0, 1))
	// Insert refund_reversal ledger entry (-10.0).
	mock.ExpectExec(`(?s)INSERT INTO user_affiliate_ledger.*'refund_reversal'`).
		WithArgs(int64(7), -10.0, int64(8), int64(100)).
		WillReturnResult(sqlmock.NewResult(1, 1))
	// Full frozen accrual reversed -> clear frozen_until immediately.
	mock.ExpectExec(`(?s)UPDATE user_affiliate_ledger.*SET frozen_until = NULL.*source_order_id = \$1 AND user_id = \$2 AND action = 'accrue' AND frozen_until IS NOT NULL`).
		WithArgs(int64(100), int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	reversed, err := repo.ReverseQuotaForOrder(ctx, 100, 1.0)
	require.NoError(t, err)
	require.InDelta(t, 10.0, reversed, 1e-9)

	// 2. Fast-forward past frozen_until: ThawFrozenQuota runs. Even if a legacy
	// row still had frozen_until set, netMatured subtracts refund_reversal and
	// currentFrozenQuota is 0 -> 0 resurrected quota, NO UPDATE user_affiliates.
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT aff_frozen_quota::double precision FROM user_affiliates WHERE user_id = \$1 FOR UPDATE`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"aff_frozen_quota"}).AddRow(0.0))
	mock.ExpectQuery(`(?s)WITH matured_rows AS.*action = 'accrue'.*refund_reversal`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"net_matured"}).AddRow(0.0))
	mock.ExpectCommit()

	thawed, err := repo.ThawFrozenQuota(ctx, 7)
	require.NoError(t, err)
	require.Zero(t, thawed)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestAffiliateRepository_ReverseQuotaForOrder_TwoPartialRefundsAndIdempotentRetry
// verifies cumulative partial refund reversal:
//   - Refund 1 (40% of 10.0 -> 4.0) deducts 4.0, keeps frozen_until for remaining 6.0.
//   - Retry of Refund 1 (40%) is idempotent and deducts 0.
//   - Refund 2 (cumulative 70% -> target 7.0, delta 3.0) deducts exactly 3.0 (total 7.0).
//   - ThawFrozenQuota after maturity moves only the net remaining 3.0 to aff_quota.
func TestAffiliateRepository_ReverseQuotaForOrder_TwoPartialRefundsAndIdempotentRetry(t *testing.T) {
	repo, mock := newMockAffiliateRepo(t)
	ctx := context.Background()

	// Partial refund 1: ratio=0.4 (target 4.0, prior 0 -> delta 4.0).
	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)SELECT user_id, COALESCE\(source_user_id, 0\)`).
		WithArgs(int64(200)).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "source_user_id"}).AddRow(int64(7), int64(8)))
	mock.ExpectQuery(`SELECT aff_quota::double precision, aff_frozen_quota::double precision FROM user_affiliates WHERE user_id = \$1 FOR UPDATE`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"aff_quota", "aff_frozen_quota"}).AddRow(0.0, 10.0))
	mock.ExpectQuery(`(?s)WITH matured_rows AS`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"net_matured"}).AddRow(0.0))
	mock.ExpectQuery(`(?s)SELECT amount::double precision, \(frozen_until IS NOT NULL\)`).
		WithArgs(int64(200), int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"amount", "is_frozen"}).AddRow(10.0, true))
	mock.ExpectQuery(`(?s)SELECT COALESCE\(SUM\(ABS\(amount\)\), 0\)::double precision`).
		WithArgs(int64(200), int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"already_reversed"}).AddRow(0.0))
	mock.ExpectExec(`(?s)UPDATE user_affiliates`).
		WithArgs(int64(7), 0.0, 6.0, 4.0).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`(?s)INSERT INTO user_affiliate_ledger.*'refund_reversal'`).
		WithArgs(int64(7), -4.0, int64(8), int64(200)).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	rev1, err := repo.ReverseQuotaForOrder(ctx, 200, 0.4)
	require.NoError(t, err)
	require.InDelta(t, 4.0, rev1, 1e-9)

	// Idempotent retry of ratio=0.4 (target 4.0, prior 4.0 -> delta 0).
	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)SELECT user_id, COALESCE\(source_user_id, 0\)`).
		WithArgs(int64(200)).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "source_user_id"}).AddRow(int64(7), int64(8)))
	mock.ExpectQuery(`SELECT aff_quota::double precision, aff_frozen_quota::double precision FROM user_affiliates WHERE user_id = \$1 FOR UPDATE`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"aff_quota", "aff_frozen_quota"}).AddRow(0.0, 6.0))
	mock.ExpectQuery(`(?s)WITH matured_rows AS`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"net_matured"}).AddRow(0.0))
	mock.ExpectQuery(`(?s)SELECT amount::double precision, \(frozen_until IS NOT NULL\)`).
		WithArgs(int64(200), int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"amount", "is_frozen"}).AddRow(10.0, true))
	mock.ExpectQuery(`(?s)SELECT COALESCE\(SUM\(ABS\(amount\)\), 0\)::double precision`).
		WithArgs(int64(200), int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"already_reversed"}).AddRow(4.0))
	mock.ExpectCommit()

	retry1, err := repo.ReverseQuotaForOrder(ctx, 200, 0.4)
	require.NoError(t, err)
	require.Zero(t, retry1)

	// Partial refund 2: cumulative ratio=0.7 (target 7.0, prior 4.0 -> delta 3.0).
	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)SELECT user_id, COALESCE\(source_user_id, 0\)`).
		WithArgs(int64(200)).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "source_user_id"}).AddRow(int64(7), int64(8)))
	mock.ExpectQuery(`SELECT aff_quota::double precision, aff_frozen_quota::double precision FROM user_affiliates WHERE user_id = \$1 FOR UPDATE`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"aff_quota", "aff_frozen_quota"}).AddRow(0.0, 6.0))
	mock.ExpectQuery(`(?s)WITH matured_rows AS`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"net_matured"}).AddRow(0.0))
	mock.ExpectQuery(`(?s)SELECT amount::double precision, \(frozen_until IS NOT NULL\)`).
		WithArgs(int64(200), int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"amount", "is_frozen"}).AddRow(10.0, true))
	mock.ExpectQuery(`(?s)SELECT COALESCE\(SUM\(ABS\(amount\)\), 0\)::double precision`).
		WithArgs(int64(200), int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"already_reversed"}).AddRow(4.0))
	mock.ExpectExec(`(?s)UPDATE user_affiliates`).
		WithArgs(int64(7), 0.0, 3.0, 3.0).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`(?s)INSERT INTO user_affiliate_ledger.*'refund_reversal'`).
		WithArgs(int64(7), -3.0, int64(8), int64(200)).
		WillReturnResult(sqlmock.NewResult(2, 1))
	mock.ExpectCommit()

	rev2, err := repo.ReverseQuotaForOrder(ctx, 200, 0.7)
	require.NoError(t, err)
	require.InDelta(t, 3.0, rev2, 1e-9)

	// After maturity, only the remaining net 3.0 thaws into aff_quota.
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT aff_frozen_quota::double precision FROM user_affiliates WHERE user_id = \$1 FOR UPDATE`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"aff_frozen_quota"}).AddRow(3.0))
	mock.ExpectQuery(`(?s)WITH matured_rows AS`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"net_matured"}).AddRow(3.0))
	mock.ExpectQuery(`(?s)WITH locked_by_order AS`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"still_locked"}).AddRow(0.0))
	mock.ExpectExec(`(?s)UPDATE user_affiliates.*SET aff_quota = aff_quota \+ \$1`).
		WithArgs(3.0, int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	thawed, err := repo.ThawFrozenQuota(ctx, 7)
	require.NoError(t, err)
	require.InDelta(t, 3.0, thawed, 1e-9)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestAffiliateRepository_ThawFrozenQuota_CrossOrderClawbackCapsAtUnencumberedFrozen
// verifies that when a prior refund on Order A confiscated Order B's frozen quota
// while Order C is still locked, maturing Order B thaws 0 instead of resurrecting
// money or releasing Order C early.
func TestAffiliateRepository_ThawFrozenQuota_CrossOrderClawbackCapsAtUnencumberedFrozen(t *testing.T) {
	repo, mock := newMockAffiliateRepo(t)
	ctx := context.Background()

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT aff_frozen_quota::double precision FROM user_affiliates WHERE user_id = \$1 FOR UPDATE`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"aff_frozen_quota"}).AddRow(10.0))
	mock.ExpectQuery(`(?s)WITH matured_rows AS`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"net_matured"}).AddRow(10.0))
	mock.ExpectQuery(`(?s)WITH locked_by_order AS`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"still_locked"}).AddRow(10.0))
	mock.ExpectCommit()

	thawed, err := repo.ThawFrozenQuota(ctx, 7)
	require.NoError(t, err)
	require.Zero(t, thawed)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestAffiliateRepository_AccrueQuotaWithCap_LocksInviterAndClampsInsideTx verifies
// that perInviteeCap enforcement runs inside the accrual transaction under FOR UPDATE.
func TestAffiliateRepository_AccrueQuotaWithCap_LocksInviterAndClampsInsideTx(t *testing.T) {
	repo, mock := newMockAffiliateRepo(t)
	ctx := context.Background()
	orderID := int64(300)

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT user_id FROM user_affiliates WHERE user_id = \$1 FOR UPDATE`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"user_id"}).AddRow(int64(7)))
	mock.ExpectQuery(`(?s)SELECT COALESCE\(SUM\(amount\), 0\)::double precision FROM user_affiliate_ledger WHERE user_id = \$1 AND source_user_id = \$2 AND action = 'accrue'`).
		WithArgs(int64(7), int64(8)).
		WillReturnRows(sqlmock.NewRows([]string{"existing"}).AddRow(8.5))
	// Cap is 10.0, existing is 8.5, requested is 5.0 -> clamped to 1.5.
	mock.ExpectExec(`UPDATE user_affiliates SET aff_frozen_quota = aff_frozen_quota \+ \$1, aff_history_quota = aff_history_quota \+ \$1, updated_at = NOW\(\) WHERE user_id = \$2`).
		WithArgs(1.5, int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`(?s)INSERT INTO user_affiliate_ledger.*'accrue'.*make_interval`).
		WithArgs(int64(7), 1.5, int64(8), int64(300), 24).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	applied, err := repo.AccrueQuotaWithCap(ctx, 7, 8, 5.0, 24, &orderID, 10.0)
	require.NoError(t, err)
	require.InDelta(t, 1.5, applied, 1e-9)

	// Already at cap -> no UPDATE or INSERT executed.
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT user_id FROM user_affiliates WHERE user_id = \$1 FOR UPDATE`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"user_id"}).AddRow(int64(7)))
	mock.ExpectQuery(`(?s)SELECT COALESCE\(SUM\(amount\), 0\)::double precision FROM user_affiliate_ledger WHERE user_id = \$1 AND source_user_id = \$2 AND action = 'accrue'`).
		WithArgs(int64(7), int64(8)).
		WillReturnRows(sqlmock.NewRows([]string{"existing"}).AddRow(10.0))
	mock.ExpectCommit()

	applied, err = repo.AccrueQuotaWithCap(ctx, 7, 8, 5.0, 24, &orderID, 10.0)
	require.NoError(t, err)
	require.Zero(t, applied)
	require.NoError(t, mock.ExpectationsWereMet())
}
