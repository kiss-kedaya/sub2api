package service

import (
	"context"
	"errors"
	"time"
)

// ResolveAPIKeyRouteSubscription binds billing to the selected API key group.
func (s *GatewayService) ResolveAPIKeyRouteSubscription(ctx context.Context, key *APIKey, current *UserSubscription) (*UserSubscription, error) {
	var repo UserSubscriptionRepository
	if s != nil {
		repo = s.userSubRepo
	}
	return resolveAPIKeyRouteSubscription(ctx, repo, key, current)
}

// ResolveAPIKeyRouteSubscription binds billing to the selected API key group.
func (s *OpenAIGatewayService) ResolveAPIKeyRouteSubscription(ctx context.Context, key *APIKey, current *UserSubscription) (*UserSubscription, error) {
	var repo UserSubscriptionRepository
	if s != nil {
		repo = s.userSubRepo
	}
	return resolveAPIKeyRouteSubscription(ctx, repo, key, current)
}

func resolveAPIKeyRouteSubscription(ctx context.Context, repo UserSubscriptionRepository, key *APIKey, current *UserSubscription) (*UserSubscription, error) {
	if key == nil || key.Group == nil || !key.Group.IsSubscriptionType() {
		return nil, nil
	}
	userID := balancePreauthorizationAPIKeyUserID(key)
	if userID <= 0 || key.GroupID == nil || *key.GroupID <= 0 || key.Group.ID != *key.GroupID ||
		(key.User != nil && key.User.ID != userID) {
		return nil, ErrSubscriptionInvalid
	}
	ctx = nonNilContext(ctx)
	if err := ctx.Err(); err != nil {
		return nil, ErrBillingServiceUnavailable.WithCause(err)
	}
	if current != nil && current.UserID == userID && current.GroupID == *key.GroupID {
		if !validAPIKeyRouteSubscription(current, userID, *key.GroupID) {
			return nil, ErrSubscriptionInvalid
		}
		return current, nil
	}
	if repo == nil {
		return nil, ErrBillingServiceUnavailable
	}
	sub, err := repo.GetActiveByUserIDAndGroupID(ctx, userID, *key.GroupID)
	if errors.Is(err, ErrSubscriptionNotFound) {
		return nil, ErrSubscriptionInvalid
	}
	if err != nil {
		return nil, ErrBillingServiceUnavailable.WithCause(err)
	}
	if !validAPIKeyRouteSubscription(sub, userID, *key.GroupID) {
		return nil, ErrSubscriptionInvalid
	}
	return sub, nil
}

func validAPIKeyRouteSubscription(sub *UserSubscription, userID, groupID int64) bool {
	now := time.Now()
	return sub != nil && sub.ID > 0 && sub.UserID == userID && sub.GroupID == groupID &&
		sub.Status == SubscriptionStatusActive && sub.DeletedAt == nil &&
		!sub.StartsAt.After(now) && sub.ExpiresAt.After(now) &&
		(sub.User == nil || sub.User.ID == userID) && (sub.Group == nil || sub.Group.ID == groupID)
}

// Settlement checks identity only: a subscription admitted before expiry may
// finish afterwards. Missing legacy identity fields are tolerated, but an
// explicit conflicting user/group must never reach a billing mutation.
func validateAPIKeyRouteSubscriptionIdentity(key *APIKey, user *User, sub *UserSubscription) error {
	if sub == nil {
		return nil
	}
	if key == nil || key.Group == nil || !key.Group.IsSubscriptionType() {
		return ErrSubscriptionInvalid
	}
	userID := balancePreauthorizationAPIKeyUserID(key)
	if user != nil {
		if userID != 0 && userID != user.ID {
			return ErrSubscriptionInvalid
		}
		userID = user.ID
	}
	if key.User != nil && userID != 0 && key.User.ID != userID {
		return ErrSubscriptionInvalid
	}
	groupID := key.Group.ID
	if key.GroupID != nil {
		if groupID != 0 && groupID != *key.GroupID {
			return ErrSubscriptionInvalid
		}
		groupID = *key.GroupID
	}
	if (sub.UserID != 0 && sub.UserID != userID) || (sub.GroupID != 0 && sub.GroupID != groupID) ||
		(sub.User != nil && sub.User.ID != userID) || (sub.Group != nil && sub.Group.ID != groupID) {
		return ErrSubscriptionInvalid
	}
	return nil
}
