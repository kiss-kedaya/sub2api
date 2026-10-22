package repository

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/enttest"
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
	db.SetMaxOpenConns(1)

	drv := entsql.OpenDB(dialect.SQLite, db)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(drv)))
	t.Cleanup(func() { _ = client.Close() })

	return NewRedeemCodeRepository(client).(*redeemCodeRepository), client
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
