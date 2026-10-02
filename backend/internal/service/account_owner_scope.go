package service

import "context"

type accountOwnerScopeKey struct{}

// WithAccountOwnerScope marks the current admin. Account-pool queries then
// return only accounts uploaded by that admin.
func WithAccountOwnerScope(ctx context.Context, adminID int64) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if adminID <= 0 {
		return ctx
	}
	return context.WithValue(ctx, accountOwnerScopeKey{}, adminID)
}

func AccountOwnerScopeFromContext(ctx context.Context) (int64, bool) {
	if ctx == nil {
		return 0, false
	}
	id, ok := ctx.Value(accountOwnerScopeKey{}).(int64)
	return id, ok && id > 0
}

// AccountVisibleToOwner reports whether an admin may see this pool account.
// Accounts with no uploader stay visible to every admin.
func AccountVisibleToOwner(createdBy *int64, adminID int64) bool {
	if adminID <= 0 || createdBy == nil || *createdBy <= 0 {
		return true
	}
	return *createdBy == adminID
}
