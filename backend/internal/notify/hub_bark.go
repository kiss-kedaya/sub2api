package notify

import (
	"context"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/notify/bark"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// BarkNotificationChannel adapts the per-user Bark service to the shared
// notification hub. Bark used to be reached from several services, each with
// its own retry and formatting path; this is now the single Bark transport.
type BarkNotificationChannel struct {
	svc *UserBarkService
}

// NewBarkNotificationChannel wraps the Bark service.
func NewBarkNotificationChannel(svc *UserBarkService) *BarkNotificationChannel {
	return &BarkNotificationChannel{svc: svc}
}

// Name is the hub channel name.
func (c *BarkNotificationChannel) Name() string { return "bark" }

// SendNotification maps a hub message onto a Bark push. Admin-targeted events
// still go through the primary-admin query, user events through the per-user
// key, so no new recipient list is introduced here.
func (c *BarkNotificationChannel) SendNotification(ctx context.Context, msg service.NotificationMessage) error {
	if c == nil || c.svc == nil {
		return service.ErrNotificationChannelDisabled
	}
	level := "active"
	if msg.Level == service.NotificationLevelWarning || msg.Level == service.NotificationLevelCritical {
		level = "timeSensitive"
	}
	notification := bark.Notification{
		Title: msg.Title,
		Body:  msg.Body,
		Group: msg.Group,
		Level: level,
	}
	target := strings.TrimSpace(msg.Target)
	if target == "" || target == service.NotificationTargetAdmins {
		return c.svc.SendToAdmins(ctx, notification)
	}
	userID, ok := service.NotificationUserID(target)
	if !ok {
		return service.ErrNotificationChannelDisabled
	}
	return c.svc.SendToUser(ctx, userID, notification)
}

// RegisterDefaultChannels installs the shared transports on the process-wide
// hub. It is safe to call more than once: channels are keyed by name.
func RegisterDefaultChannels(barkService *UserBarkService) {
	hub := service.DefaultNotificationHub()
	if barkService != nil {
		hub.Register(NewBarkNotificationChannel(barkService))
	}
}
