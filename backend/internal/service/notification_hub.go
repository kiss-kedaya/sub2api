package service

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// NotificationHub is the single delivery primitive shared by the notification
// paths that used to carry their own channel plumbing: balance alerts, upstream
// monitor alerts, announcement sync and anything else that wants to push.
//
// It intentionally stays transport agnostic. A caller registers the channels it
// can reach (Bark, email, webhook) once, then publishes events. Dedupe keys and
// the cooldown window live here so the same event can never be delivered twice
// through two different code paths.
const (
	NotificationLevelInfo     = "info"
	NotificationLevelWarning  = "warning"
	NotificationLevelCritical = "critical"

	NotificationTargetAdmins = "admins"

	defaultNotificationCooldown = 6 * time.Hour
	maxNotificationSeenEntries  = 8192
)

// ErrNotificationChannelDisabled is returned by adapters that are configured
// but currently switched off. The hub treats it as a normal skip.
var ErrNotificationChannelDisabled = errors.New("notification channel disabled")

// NotificationMessage is one event to deliver.
type NotificationMessage struct {
	EventKind string
	DedupeKey string
	Title     string
	Body      string
	Level     string
	// Group is the channel grouping label some providers use (Bark group).
	Group string
	// Target identifies the recipient: NotificationTargetAdmins or a value
	// produced by NotificationUserTarget.
	Target   string
	Metadata map[string]string
}

// NotificationChannel is one delivery backend.
type NotificationChannel interface {
	Name() string
	SendNotification(ctx context.Context, msg NotificationMessage) error
}

// NotificationChannelFunc adapts a function to NotificationChannel so callers
// do not have to declare a type per transport.
type NotificationChannelFunc struct {
	ChannelName string
	Send        func(ctx context.Context, msg NotificationMessage) error
}

func (c NotificationChannelFunc) Name() string { return c.ChannelName }

func (c NotificationChannelFunc) SendNotification(ctx context.Context, msg NotificationMessage) error {
	if c.Send == nil {
		return ErrNotificationChannelDisabled
	}
	return c.Send(ctx, msg)
}

// NotificationUserTarget builds the user target value used by push channels.
func NotificationUserTarget(userID int64) string {
	return "user:" + strconv.FormatInt(userID, 10)
}

// NotificationUserID extracts the user id from a user target.
func NotificationUserID(target string) (int64, bool) {
	raw := strings.TrimPrefix(strings.TrimSpace(target), "user:")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// NotificationDelivery is one recorded delivery attempt.
type NotificationDelivery struct {
	Channel   string
	EventKind string
	DedupeKey string
	Target    string
	Status    string
	Error     string
	CreatedAt time.Time
}

// NotificationDeliveryRecorder persists delivery attempts. Optional: without a
// recorder the hub still dedupes in memory.
type NotificationDeliveryRecorder interface {
	RecordNotificationDelivery(ctx context.Context, d NotificationDelivery) error
}

// NotificationPublishResult reports what one publish did.
type NotificationPublishResult struct {
	Suppressed bool
	Delivered  []string
	Failed     map[string]string
}

// HasFailure reports whether any channel rejected the message.
func (r NotificationPublishResult) HasFailure() bool { return len(r.Failed) > 0 }

// NotificationHub is safe for concurrent use.
type NotificationHub struct {
	mu       sync.Mutex
	channels map[string]NotificationChannel
	order    []string
	seen     map[string]time.Time
	cooldown time.Duration
	recorder NotificationDeliveryRecorder
	now      func() time.Time
}

// NewNotificationHub creates an empty hub with the default cooldown.
func NewNotificationHub() *NotificationHub {
	return &NotificationHub{
		channels: map[string]NotificationChannel{},
		seen:     map[string]time.Time{},
		cooldown: defaultNotificationCooldown,
		now:      func() time.Time { return time.Now().UTC() },
	}
}

// SetCooldown overrides the dedupe window. Zero disables dedupe.
func (h *NotificationHub) SetCooldown(d time.Duration) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cooldown = d
}

// SetDeliveryRecorder wires optional persistence.
func (h *NotificationHub) SetDeliveryRecorder(r NotificationDeliveryRecorder) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recorder = r
}

// Register adds or replaces channels by name. Nil channels are ignored.
func (h *NotificationHub) Register(channels ...NotificationChannel) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ch := range channels {
		if ch == nil {
			continue
		}
		name := strings.TrimSpace(ch.Name())
		if name == "" {
			continue
		}
		if _, ok := h.channels[name]; !ok {
			h.order = append(h.order, name)
		}
		h.channels[name] = ch
	}
	sort.Strings(h.order)
}

// Channels lists the registered channel names.
func (h *NotificationHub) Channels() []string {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.order...)
}

// Publish delivers one message through every registered channel. A message
// whose dedupe key was seen inside the cooldown window is suppressed whole.
func (h *NotificationHub) Publish(ctx context.Context, msg NotificationMessage) NotificationPublishResult {
	result := NotificationPublishResult{Failed: map[string]string{}}
	if h == nil {
		result.Suppressed = true
		return result
	}
	if strings.TrimSpace(msg.Level) == "" {
		msg.Level = NotificationLevelInfo
	}
	if strings.TrimSpace(msg.Target) == "" {
		msg.Target = NotificationTargetAdmins
	}

	h.mu.Lock()
	now := h.now()
	if key := strings.TrimSpace(msg.DedupeKey); key != "" && h.cooldown > 0 {
		if last, ok := h.seen[key]; ok && now.Sub(last) < h.cooldown {
			h.mu.Unlock()
			result.Suppressed = true
			return result
		}
		h.seen[key] = now
		h.pruneSeenLocked(now)
	}
	channels := make([]NotificationChannel, 0, len(h.order))
	for _, name := range h.order {
		channels = append(channels, h.channels[name])
	}
	recorder := h.recorder
	h.mu.Unlock()

	if len(channels) == 0 {
		return result
	}
	for _, ch := range channels {
		err := ch.SendNotification(ctx, msg)
		if errors.Is(err, ErrNotificationChannelDisabled) {
			continue
		}
		delivery := NotificationDelivery{
			Channel:   ch.Name(),
			EventKind: msg.EventKind,
			DedupeKey: msg.DedupeKey,
			Target:    msg.Target,
			Status:    "sent",
			CreatedAt: now,
		}
		if err != nil {
			delivery.Status = "failed"
			delivery.Error = truncateNotificationError(err)
			if result.Failed == nil {
				result.Failed = map[string]string{}
			}
			result.Failed[ch.Name()] = delivery.Error
			slog.Warn("notification_delivery_failed", "channel", ch.Name(), "event", msg.EventKind, "error", err)
		} else {
			result.Delivered = append(result.Delivered, ch.Name())
		}
		if recorder != nil {
			if recordErr := recorder.RecordNotificationDelivery(ctx, delivery); recordErr != nil {
				slog.Warn("notification_delivery_record_failed", "channel", ch.Name(), "error", recordErr)
			}
		}
	}
	return result
}

// ResetDedupeKey forgets one key so the next publish is not suppressed. Used
// when an operator acknowledges an alert or re-arms a threshold.
func (h *NotificationHub) ResetDedupeKey(key string) {
	if h == nil || strings.TrimSpace(key) == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.seen, key)
}

func (h *NotificationHub) pruneSeenLocked(now time.Time) {
	if len(h.seen) <= maxNotificationSeenEntries {
		return
	}
	for key, at := range h.seen {
		if now.Sub(at) >= h.cooldown {
			delete(h.seen, key)
		}
	}
}

func truncateNotificationError(err error) string {
	if err == nil {
		return ""
	}
	text := strings.TrimSpace(err.Error())
	if len(text) > 512 {
		text = text[:512]
	}
	return text
}

var defaultNotificationHub = NewNotificationHub()

// DefaultNotificationHub is the process-wide hub. Services register the
// transports they own and publish events here instead of keeping private
// dedupe/cooldown/send loops.
func DefaultNotificationHub() *NotificationHub { return defaultNotificationHub }
