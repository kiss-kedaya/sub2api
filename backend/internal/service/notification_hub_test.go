package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNotificationHubDedupesWithinCooldown(t *testing.T) {
	hub := NewNotificationHub()
	sent := 0
	hub.Register(NotificationChannelFunc{
		ChannelName: "test",
		Send: func(context.Context, NotificationMessage) error {
			sent++
			return nil
		},
	})

	first := hub.Publish(context.Background(), NotificationMessage{DedupeKey: "k1", Title: "t"})
	require.False(t, first.Suppressed)
	require.Equal(t, []string{"test"}, first.Delivered)

	second := hub.Publish(context.Background(), NotificationMessage{DedupeKey: "k1", Title: "t"})
	require.True(t, second.Suppressed)
	require.Equal(t, 1, sent)

	hub.ResetDedupeKey("k1")
	third := hub.Publish(context.Background(), NotificationMessage{DedupeKey: "k1", Title: "t"})
	require.False(t, third.Suppressed)
	require.Equal(t, 2, sent)
}

func TestNotificationHubCooldownExpires(t *testing.T) {
	hub := NewNotificationHub()
	now := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	hub.now = func() time.Time { return now }
	sent := 0
	hub.Register(NotificationChannelFunc{
		ChannelName: "test",
		Send: func(context.Context, NotificationMessage) error {
			sent++
			return nil
		},
	})

	require.False(t, hub.Publish(context.Background(), NotificationMessage{DedupeKey: "k", Title: "t"}).Suppressed)
	now = now.Add(3 * time.Hour)
	require.True(t, hub.Publish(context.Background(), NotificationMessage{DedupeKey: "k", Title: "t"}).Suppressed)
	now = now.Add(4 * time.Hour)
	require.False(t, hub.Publish(context.Background(), NotificationMessage{DedupeKey: "k", Title: "t"}).Suppressed)
	require.Equal(t, 2, sent)
}

func TestNotificationHubSkipsDisabledAndReportsFailures(t *testing.T) {
	hub := NewNotificationHub()
	var recorded []NotificationDelivery
	hub.SetDeliveryRecorder(recorderFunc(func(_ context.Context, d NotificationDelivery) error {
		recorded = append(recorded, d)
		return nil
	}))
	hub.Register(
		NotificationChannelFunc{ChannelName: "off", Send: func(context.Context, NotificationMessage) error {
			return ErrNotificationChannelDisabled
		}},
		NotificationChannelFunc{ChannelName: "broken", Send: func(context.Context, NotificationMessage) error {
			return errors.New("boom")
		}},
		NotificationChannelFunc{ChannelName: "ok", Send: func(context.Context, NotificationMessage) error {
			return nil
		}},
	)

	result := hub.Publish(context.Background(), NotificationMessage{Title: "t", Target: NotificationUserTarget(42)})
	require.Equal(t, []string{"ok"}, result.Delivered)
	require.Equal(t, map[string]string{"broken": "boom"}, result.Failed)
	require.Len(t, recorded, 2, "disabled channels are not part of the delivery log")
	require.Equal(t, "user:42", recorded[0].Target)
}

func TestNotificationHubWithoutChannelsIsANoop(t *testing.T) {
	hub := NewNotificationHub()
	result := hub.Publish(context.Background(), NotificationMessage{DedupeKey: "x", Title: "t"})
	require.False(t, result.Suppressed)
	require.False(t, result.HasFailure())
	require.Empty(t, hub.Channels())
}

func TestNotificationTargetRoundTrip(t *testing.T) {
	target := NotificationUserTarget(9)
	id, ok := NotificationUserID(target)
	require.True(t, ok)
	require.Equal(t, int64(9), id)
	_, ok = NotificationUserID("admins")
	require.False(t, ok)
}

type recorderFunc func(context.Context, NotificationDelivery) error

func (f recorderFunc) RecordNotificationDelivery(ctx context.Context, d NotificationDelivery) error {
	return f(ctx, d)
}
