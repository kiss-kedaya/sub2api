package notify

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/notify/bark"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// PushAdapter implements the service layer's PushNotifier. It no longer owns a
// send loop of its own: messages are published to the shared notification hub,
// which holds the single Bark transport (and, later, any other channel).
type PushAdapter struct {
	svc *UserBarkService
}

func NewPushAdapter(svc *UserBarkService) *PushAdapter {
	// Registering here keeps the wiring in one place and makes every caller of
	// NewPushAdapter share the same transport.
	RegisterDefaultChannels(svc)
	return &PushAdapter{svc: svc}
}

func (a *PushAdapter) PushToUser(ctx context.Context, userID int64, title, body, group string) error {
	return a.publish(ctx, service.NotificationUserTarget(userID), title, body, group)
}

func (a *PushAdapter) PushToAdmins(ctx context.Context, title, body, group string) error {
	return a.publish(ctx, service.NotificationTargetAdmins, title, body, group)
}

// publish keeps the historical contract: no dedupe key, so every call is
// delivered exactly like before. Errors from any channel are folded into one
// error so callers still log a failed delivery.
func (a *PushAdapter) publish(ctx context.Context, target, title, body, group string) error {
	if a == nil || a.svc == nil {
		return nil
	}
	result := service.DefaultNotificationHub().Publish(ctx, service.NotificationMessage{
		EventKind: "ops.push",
		Title:     title,
		Body:      body,
		Group:     group,
		Level:     service.NotificationLevelWarning,
		Target:    target,
	})
	if !result.HasFailure() {
		return nil
	}
	names := make([]string, 0, len(result.Failed))
	for name := range result.Failed {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, name+": "+result.Failed[name])
	}
	return fmt.Errorf("push delivery failed (%s)", strings.Join(parts, "; "))
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
