package service

import (
	"context"
	"strings"
)

type accountOwnerScopeKey struct{}

type accountOwnerScope struct {
	AdminID   int64
	SeeLegacy bool
}

const legacyPoolOwnerEmail = "admin@sub2api.local"

// WithAccountOwnerScope marks the current admin. Account-pool queries then
// return only accounts uploaded by that admin. Accounts with no uploader are
// visible only to admin@sub2api.local.
func WithAccountOwnerScope(ctx context.Context, adminID int64, email string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if adminID <= 0 {
		return ctx
	}
	return context.WithValue(ctx, accountOwnerScopeKey{}, accountOwnerScope{
		AdminID:   adminID,
		SeeLegacy: strings.EqualFold(strings.TrimSpace(email), legacyPoolOwnerEmail),
	})
}

func AccountOwnerScopeFromContext(ctx context.Context) (int64, bool) {
	id, _, ok := AccountOwnerScopeDetail(ctx)
	return id, ok
}

func AccountOwnerScopeDetail(ctx context.Context) (adminID int64, seeLegacy bool, ok bool) {
	if ctx == nil {
		return 0, false, false
	}
	scope, isScope := ctx.Value(accountOwnerScopeKey{}).(accountOwnerScope)
	if !isScope || scope.AdminID <= 0 {
		return 0, false, false
	}
	return scope.AdminID, scope.SeeLegacy, true
}

// AccountVisibleToOwner reports whether an admin may see this pool account.
// A nil or non-positive uploader is a legacy account. seeLegacy is true only
// for admin@sub2api.local. adminID <= 0 means the caller is outside the
// admin pool scope, so nothing is hidden.
func AccountVisibleToOwner(createdBy *int64, adminID int64, seeLegacy bool) bool {
	if adminID <= 0 {
		return true
	}
	if createdBy == nil || *createdBy <= 0 {
		return seeLegacy
	}
	return *createdBy == adminID
}
