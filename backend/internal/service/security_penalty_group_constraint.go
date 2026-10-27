package service

import (
	"context"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// This is an admission error, not an upstream 403 or a billing failure.
var ErrSecurityPenaltyGroupRestricted = infraerrors.Forbidden("security_penalty_group_restricted", "This conversation is restricted to the configured group; review your security records")

type securityPenaltyGroupConstraintKey struct{}
type securityPenaltyGroupConstraint struct {
	userID, apiKeyID, groupID int64
}

// ContextWithSecurityPenaltyGroupConstraint binds a server-owned audit decision
// to the authenticated key. It never grants a group or changes billing identity.
// A fresh WebSocket turn may clear a prior turn's restriction with a nil target.
func ContextWithSecurityPenaltyGroupConstraint(ctx context.Context, key *APIKey, target *int64) (context.Context, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if target == nil {
		if securityPenaltyConstraint(ctx) == nil {
			return ctx, nil
		}
		return context.WithValue(ctx, securityPenaltyGroupConstraintKey{}, (*securityPenaltyGroupConstraint)(nil)), nil
	}
	if key == nil || key.ID <= 0 || key.UserID <= 0 || key.User == nil || key.User.ID != key.UserID || *target <= 0 {
		return ctx, ErrSecurityPenaltyGroupRestricted
	}
	for _, id := range key.CandidateGroupIDs() {
		if id == *target {
			return context.WithValue(ctx, securityPenaltyGroupConstraintKey{}, &securityPenaltyGroupConstraint{
				userID: key.UserID, apiKeyID: key.ID, groupID: id,
			}), nil
		}
	}
	return ctx, ErrSecurityPenaltyGroupRestricted
}

func securityPenaltyConstraint(ctx context.Context) *securityPenaltyGroupConstraint {
	if ctx == nil {
		return nil
	}
	constraint, _ := ctx.Value(securityPenaltyGroupConstraintKey{}).(*securityPenaltyGroupConstraint)
	return constraint
}

// CheckSecurityPenaltySelectedGroup is the final admission check before a
// selected key becomes the forwarding/billing identity. Current user/group ACL
// and normal subscription checks remain in force.
func CheckSecurityPenaltySelectedGroup(ctx context.Context, key *APIKey) error {
	constraint := securityPenaltyConstraint(ctx)
	if constraint == nil {
		return nil
	}
	if key == nil || key.ID != constraint.apiKeyID || key.UserID != constraint.userID ||
		key.User == nil || key.User.ID != constraint.userID || !apiKeyRouteGroupAllowed(key) {
		return ErrSecurityPenaltyGroupRestricted
	}
	return CheckSecurityPenaltySchedulingGroup(ctx, key.GroupID)
}

// CheckSecurityPenaltySchedulingGroup runs before every sticky/previous-response
// shortcut and again after fallback resolution. Missing groups never widen scope.
func CheckSecurityPenaltySchedulingGroup(ctx context.Context, groupID *int64) error {
	constraint := securityPenaltyConstraint(ctx)
	if constraint != nil && (groupID == nil || *groupID != constraint.groupID) {
		return ErrSecurityPenaltyGroupRestricted
	}
	return nil
}

func securityPenaltyRouteCandidateIDs(ctx context.Context, key *APIKey, ids []int64) ([]int64, error) {
	constraint := securityPenaltyConstraint(ctx)
	if constraint == nil {
		return ids, nil
	}
	if key == nil || key.ID != constraint.apiKeyID || key.UserID != constraint.userID || key.User == nil || key.User.ID != constraint.userID {
		return nil, ErrSecurityPenaltyGroupRestricted
	}
	for _, id := range ids {
		if id == constraint.groupID {
			return []int64{id}, nil
		}
	}
	return nil, ErrSecurityPenaltyGroupRestricted
}
