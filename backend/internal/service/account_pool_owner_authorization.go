package service

import (
	"context"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// ErrAccountPoolOwnerRequired is returned when a protected pool-owner action
// is attempted by another administrator. A zero pool-owner setting preserves
// the existing deployment behavior.
var ErrAccountPoolOwnerRequired = infraerrors.Forbidden("ACCOUNT_POOL_OWNER_REQUIRED", "configured account pool owner authorization required")

// RequireConfiguredPoolOwner protects either a global pool-owner operation
// (targetUserID <= 0) or changes to the configured owner user. It relies only
// on the authenticated scope installed by admin middleware.
func RequireConfiguredPoolOwner(ctx context.Context, targetUserID int64) error {
	adminID, poolOwnerID, configured, scoped := AccountPoolOwnerScope(ctx)
	if !scoped || !configured || adminID == poolOwnerID {
		return nil
	}
	if targetUserID <= 0 || targetUserID == poolOwnerID {
		return ErrAccountPoolOwnerRequired
	}
	return nil
}
