package service

// Preserve the existing strict owner and route identity contract when ordinary
// Key/user/account slots are transferred to a long-lived Live lease.
func validateLiveCallIdentity(identity LiveCallIdentity) error {
	key := identity.APIKey
	if key == nil || identity.APIKeyID <= 0 || identity.UserID <= 0 ||
		key.ID != identity.APIKeyID || key.UserID != identity.UserID ||
		key.User == nil || key.User.ID != identity.UserID ||
		!sameLiveOptionalID(key.GroupID, identity.GroupID) ||
		key.GroupID == nil || key.Group == nil || key.Group.ID != *key.GroupID ||
		!sameLiveRouteGroupIDs(key.RouteGroupIDs, identity.RouteGroupIDs) {
		return ErrLiveIdentityMismatch
	}
	if !liveRouteGroupAllowed(key) {
		return ErrLiveUnavailable
	}
	return nil
}

func cloneLiveAPIKey(key *APIKey) *APIKey {
	if key == nil {
		return nil
	}
	cloned := *key
	cloned.RouteGroupIDs = append([]int64(nil), key.RouteGroupIDs...)
	return &cloned
}

func liveRouteGroupAllowed(key *APIKey) bool {
	return key != nil && key.GroupID != nil && key.Group != nil &&
		key.Group.ID == *key.GroupID && IsGroupContextValid(key.Group) &&
		key.Group.IsActive() && key.Group.AllowLive &&
		(key.Group.Platform == PlatformOpenAI || key.Group.Platform == PlatformComposite)
}

func sameLiveOptionalID(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func sameLiveRouteGroupIDs(left, right []int64) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
