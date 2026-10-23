package notify

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/notify/bark"
)

// PushAdapter implements the service layer's PushNotifier over the per-user
// Bark keys, so balance and upstream alerts need no separate configuration.
type PushAdapter struct {
	svc *UserBarkService
}

func NewPushAdapter(svc *UserBarkService) *PushAdapter {
	return &PushAdapter{svc: svc}
}

func (a *PushAdapter) PushToUser(ctx context.Context, userID int64, title, body, group string) error {
	if a == nil || a.svc == nil {
		return nil
	}
	return a.svc.SendToUser(ctx, userID, bark.Notification{
		Title: title, Body: body, Group: group, Level: "timeSensitive",
	})
}

func (a *PushAdapter) PushToAdmins(ctx context.Context, title, body, group string) error {
	if a == nil || a.svc == nil {
		return nil
	}
	return a.svc.SendToAdmins(ctx, bark.Notification{
		Title: title, Body: body, Group: group, Level: "timeSensitive",
	})
}

// OwnerPushAdapter routes private wallet alerts exclusively to the primary
// administrator and requires a real delivery attempt to succeed.
type OwnerPushAdapter struct {
	svc     *UserBarkService
	ownerID int64
}

func NewOwnerPushAdapter(svc *UserBarkService, ownerID int64) *OwnerPushAdapter {
	return &OwnerPushAdapter{svc: svc, ownerID: ownerID}
}

func (a *OwnerPushAdapter) PushToAdmins(ctx context.Context, title, body, group string) error {
	if a == nil || a.svc == nil || a.ownerID <= 0 {
		return ErrBarkNotConfigured
	}
	return a.svc.SendToUserRequired(ctx, a.ownerID, bark.Notification{
		Title: title, Body: body, Group: group, Level: "timeSensitive",
	})
}

func (a *OwnerPushAdapter) WalletOperationNotificationsEnabled(ctx context.Context) (bool, error) {
	if a == nil || a.svc == nil || a.ownerID <= 0 {
		return false, ErrBarkNotConfigured
	}
	return a.svc.WalletOperationNotificationsEnabled(ctx, a.ownerID)
}
