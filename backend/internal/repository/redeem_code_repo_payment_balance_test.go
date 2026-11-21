package repository

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/enttest"
	"github.com/Wei-Shaw/sub2api/ent/redeemcode"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func newRedeemCodePaymentBalanceRepo(t *testing.T) (*redeemCodeRepository, *dbent.Client) {
	t.Helper()

	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1&_pragma=foreign_keys(1)", t.Name()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(4)

	drv := entsql.OpenDB(dialect.SQLite, db)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(drv)))
	t.Cleanup(func() { _ = client.Close() })

	// redeem_code_hashes 是迁移 328 新增的原生 DDL 表，enttest 只建 schema 里的表，
	// 这里用 SQLite 兼容的列类型补一份，保证创建/删除的双写路径可测。
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS redeem_code_hashes (
		redeem_code_id INTEGER PRIMARY KEY REFERENCES redeem_codes(id) ON DELETE CASCADE,
		code_hash      TEXT NOT NULL UNIQUE,
		code_prefix    TEXT NOT NULL,
		created_at     TIMESTAMP NOT NULL,
		updated_at     TIMESTAMP NOT NULL
	)`)
	require.NoError(t, err)

	repo, ok := NewRedeemCodeRepository(client).(*redeemCodeRepository)
	require.True(t, ok, "repository constructor must return concrete test repository")
	return repo, client
}

func TestRedeemCodeRepositoryCreateUsesTransactionFromContext(t *testing.T) {
	repo, client := newRedeemCodePaymentBalanceRepo(t)
	ctx := context.Background()
	u, err := client.User.Create().SetEmail("transactional-source@example.com").SetPasswordHash("test-hash").Save(ctx)
	require.NoError(t, err)

	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	txCtx := dbent.NewTxContext(ctx, tx)
	usedAt := time.Now()
	code := &service.RedeemCode{
		Code:   "transactional-source",
		Type:   "admin_balance",
		Value:  5,
		Status: service.StatusUsed,
		UsedBy: &u.ID,
		UsedAt: &usedAt,
	}
	require.NoError(t, repo.Create(txCtx, code))
	require.NotEmpty(t, code.CodeHash, "create must derive the credential hash in memory")
	require.NoError(t, tx.Rollback())

	// 凭据本体是 code_hash；code 明文列必须保持 NULL（只落哈希）。
	count, err := client.RedeemCode.Query().Where(redeemcode.CodeHashEQ(code.CodeHash)).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, count, "source record must roll back with its balance transaction")

	ledgerCount, err := client.QueryContext(ctx, `SELECT COUNT(*) FROM redeem_code_hashes WHERE code_hash = $1`, code.CodeHash)
	require.NoError(t, err)
	require.True(t, ledgerCount.Next())
	var ledgerRows int
	require.NoError(t, ledgerCount.Scan(&ledgerRows))
	require.NoError(t, ledgerCount.Close())
	require.Zero(t, ledgerRows, "hash ledger row must roll back with the source record")
}

func TestRedeemCodeRepositorySumPositiveBalanceByUserIncludesPaymentBalance(t *testing.T) {
	repo, client := newRedeemCodePaymentBalanceRepo(t)
	ctx := context.Background()

	u, err := client.User.Create().SetEmail("payment-balance@example.com").SetPasswordHash("test-hash").Save(ctx)
	require.NoError(t, err)
	other, err := client.User.Create().SetEmail("other-payment-balance@example.com").SetPasswordHash("test-hash").Save(ctx)
	require.NoError(t, err)

	for _, code := range []struct {
		name   string
		typ    string
		value  float64
		userID int64
	}{
		{name: "legacy-bal", typ: service.RedeemTypeBalance, value: 10, userID: u.ID},
		{name: "legacy-admin-bal", typ: "admin_balance", value: 20, userID: u.ID},
		{name: "payment-bal", typ: service.RedeemTypePaymentBalance, value: 30, userID: u.ID},
		{name: "negative-pay", typ: service.RedeemTypePaymentBalance, value: -5, userID: u.ID},
		{name: "other-type", typ: service.RedeemTypeConcurrency, value: 100, userID: u.ID},
		{name: "other-user-pay", typ: service.RedeemTypePaymentBalance, value: 200, userID: other.ID},
	} {
		_, err := client.RedeemCode.Create().
			SetCode(code.name).
			SetType(code.typ).
			SetStatus(service.StatusUsed).
			SetValue(code.value).
			SetUsedBy(code.userID).
			Save(ctx)
		require.NoError(t, err)
	}

	got, err := repo.SumPositiveBalanceByUser(ctx, u.ID)
	require.NoError(t, err)
	require.Equal(t, float64(60), got)
}

func TestRedeemCodeRepositoryListByUserPaginatedPaymentBalanceFilter(t *testing.T) {
	repo, client := newRedeemCodePaymentBalanceRepo(t)
	ctx := context.Background()

	u, err := client.User.Create().SetEmail("payment-filter@example.com").SetPasswordHash("test-hash").Save(ctx)
	require.NoError(t, err)

	for _, code := range []struct {
		name string
		typ  string
	}{
		{name: "payment", typ: service.RedeemTypePaymentBalance},
		{name: "legacy-balance", typ: service.RedeemTypeBalance},
		{name: "legacy-admin", typ: "admin_balance"},
	} {
		_, err := client.RedeemCode.Create().
			SetCode(code.name).
			SetType(code.typ).
			SetStatus(service.StatusUsed).
			SetValue(1).
			SetUsedBy(u.ID).
			Save(ctx)
		require.NoError(t, err)
	}

	for _, tc := range []struct {
		codeType  string
		wantTypes []string
	}{
		{codeType: service.RedeemTypeBalance, wantTypes: []string{service.RedeemTypeBalance, service.RedeemTypePaymentBalance}},
		{codeType: service.RedeemTypePaymentBalance, wantTypes: []string{service.RedeemTypePaymentBalance}},
		{codeType: "admin_balance", wantTypes: []string{"admin_balance"}},
	} {
		t.Run(tc.codeType, func(t *testing.T) {
			codes, result, err := repo.ListByUserPaginated(ctx, u.ID, pagination.PaginationParams{Page: 1, PageSize: 10}, tc.codeType)
			require.NoError(t, err)
			require.Equal(t, int64(len(tc.wantTypes)), result.Total, "filter %q", tc.codeType)
			require.Len(t, codes, len(tc.wantTypes), "filter %q", tc.codeType)
			gotTypes := make(map[string]int, len(codes))
			for _, code := range codes {
				gotTypes[code.Type]++
			}
			for _, wantType := range tc.wantTypes {
				require.Equal(t, 1, gotTypes[wantType], "filter %q must return %q once", tc.codeType, wantType)
			}
		})
	}
}
