package service

import "context"

type accountOwnerScopeKey struct{}

type accountOwnerScope struct {
	AdminID     int64
	FullPool    bool
	PoolOwnerID int64
}

// WithAccountOwnerScope records a verified admin and the server-configured pool
// owner. Only that positive user ID may access all uploads, including legacy
// accounts. Email addresses and request parameters never grant this permission.
func WithAccountOwnerScope(ctx context.Context, adminID, poolOwnerUserID int64) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if adminID <= 0 {
		return ctx
	}
	return context.WithValue(ctx, accountOwnerScopeKey{}, accountOwnerScope{
		AdminID:     adminID,
		FullPool:    poolOwnerUserID > 0 && adminID == poolOwnerUserID,
		PoolOwnerID: poolOwnerUserID,
	})
}

// EnsureAccountOwnerScope preserves authentication's scope for the same admin.
// Callers without a matching authenticated scope receive only their own uploads.
func EnsureAccountOwnerScope(ctx context.Context, adminID int64) context.Context {
	if scopedID, _, ok := AccountOwnerScopeDetail(ctx); ok && scopedID == adminID {
		return ctx
	}
	return WithAccountOwnerScope(ctx, adminID, 0)
}

func AccountOwnerScopeFromContext(ctx context.Context) (int64, bool) {
	id, _, ok := AccountOwnerScopeDetail(ctx)
	return id, ok
}

// AccountOwnerScopeDetail returns the verified admin's ID and pool-wide access.
func AccountOwnerScopeDetail(ctx context.Context) (adminID int64, fullPool bool, ok bool) {
	if ctx == nil {
		return 0, false, false
	}
	scope, isScope := ctx.Value(accountOwnerScopeKey{}).(accountOwnerScope)
	if !isScope || scope.AdminID <= 0 {
		return 0, false, false
	}
	return scope.AdminID, scope.FullPool, true
}

// AccountPoolOwnerScope returns the authenticated administrator and the
// server-configured pool owner. The configured owner ID is carried in the
// authenticated request context; it is never read from request parameters.
func AccountPoolOwnerScope(ctx context.Context) (adminID, poolOwnerID int64, configured, ok bool) {
	if ctx == nil {
		return 0, 0, false, false
	}
	scope, isScope := ctx.Value(accountOwnerScopeKey{}).(accountOwnerScope)
	if !isScope || scope.AdminID <= 0 {
		return 0, 0, false, false
	}
	return scope.AdminID, scope.PoolOwnerID, scope.PoolOwnerID > 0, true
}

// AccountVisibleToOwner applies the same visibility policy to individual reads
// and batch operations. Unscoped background/gateway calls retain their behavior.
func AccountVisibleToOwner(createdBy *int64, adminID int64, fullPool bool) bool {
	if adminID <= 0 || fullPool {
		return true
	}
	return createdBy != nil && *createdBy == adminID
}
