package repository

import (
	"context"

	dbaccount "github.com/Wei-Shaw/sub2api/ent/account"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// ListAccountOpsSnapshots reads the fields used by monitor grouping and threshold
// checks, without loading routing groups or proxies. Nil ids means all accounts;
// an empty, non-nil slice means no accounts. Ent retains soft-delete and observer
// visibility filtering, just as the full account repository does.
func (r *accountRepository) ListAccountOpsSnapshots(ctx context.Context, ids []int64) ([]service.Account, error) {
	if ids != nil && len(ids) == 0 {
		return []service.Account{}, nil
	}
	query := r.applyAccountOwnerScope(ctx, r.client.Account.Query().Where(dbaccount.DeletedAtIsNil()))
	if ids != nil {
		query = query.Where(dbaccount.IDIn(ids...))
	}
	rows, err := query.Select(dbaccount.FieldID, dbaccount.FieldName,
		dbaccount.FieldPlatform, dbaccount.FieldType, dbaccount.FieldCredentials,
		dbaccount.FieldExtra, dbaccount.FieldSessionWindowEnd).All(ctx)
	if err != nil {
		return nil, err
	}
	accounts := make([]service.Account, 0, len(rows))
	for _, row := range rows {
		accounts = append(accounts, *accountEntityToService(row))
	}
	return accounts, nil
}
