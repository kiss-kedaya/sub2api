package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

const (
	GroupWatchEventPriceChanged = "price_changed"
	GroupWatchEventStatusBlack  = "status_black"
	GroupWatchEventStatusGreen  = "status_green"

	GroupWatchStatusBlack = "black"
	GroupWatchStatusGreen = "green"
	GroupWatchStatusOther = "other"

	// Visual parity thresholds with frontend monitorFormat.ts (monitorBarClass):
	//   - black: cache_rate < 30% OR availability < 30%
	//   - green: cache_rate >= 30% AND availability >= 80%
	GroupWatchCacheBlackThresholdPct = 30.0
	GroupWatchAvailBlackThresholdPct = 30.0
	GroupWatchAvailGreenThresholdPct = 80.0

	groupWatchFreshBucketMaxAge     = 20 * time.Minute
	groupWatchNotificationLimit     = 30
	groupWatchNotificationRetention = 30 * 24 * time.Hour
	groupWatchPruneBatchSize        = 2000
	groupWatchDeliveryTimeout       = 45 * time.Second
	groupWatchDeliveryConcurrency   = 8
	groupWatchBarkGroup             = "channel-watch"
)

var (
	ErrGroupWatchInvalidGroupID        = infraerrors.BadRequest("INVALID_GROUP_ID", "invalid group id")
	ErrGroupWatchGroupNotAvailable     = infraerrors.Forbidden("GROUP_NOT_AVAILABLE", "group not available")
	ErrGroupWatchNotificationNotFound  = infraerrors.NotFound("GROUP_WATCH_NOTIFICATION_NOT_FOUND", "notification not found")
	ErrGroupWatchAuthorizationRequired = infraerrors.InternalServer("GROUP_WATCH_AUTH_UNAVAILABLE", "group watch authorization unavailable")
)

// GroupWatchSubscriber is one active subscribed user resolved for fan-out.
type GroupWatchSubscriber struct {
	UserID              int64
	Email               string
	Username            string
	Role                string
	PrimaryEmailBlocked bool
	ExtraNotifyEmails   []NotifyEmailEntry
}

// GroupMonitorWatchState stores the last confirmed visual band of one monitored group.
type GroupMonitorWatchState struct {
	GroupID         int64
	Status          string
	BlackNotified   bool
	LastBucketStart time.Time
	CacheRate       float64
	AvailabilityPct float64
	UpdatedAt       time.Time
}

// UserGroupWatchNotification is one durable in-app notification row for a subscribed user.
type UserGroupWatchNotification struct {
	ID        int64             `json:"id"`
	UserID    int64             `json:"-"`
	GroupID   int64             `json:"group_id"`
	EventType string            `json:"event_type"`
	DedupeKey string            `json:"-"`
	Title     string            `json:"title"`
	Content   string            `json:"content"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	ReadAt    *time.Time        `json:"read_at,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
}

// GroupWatchRepository persists subscriptions, state transitions, and in-app notifications.
type GroupWatchRepository interface {
	ListSubscribedGroupIDs(ctx context.Context, userID int64) ([]int64, error)
	Subscribe(ctx context.Context, userID, groupID int64) error
	Unsubscribe(ctx context.Context, userID, groupID int64) error
	ListWatchedGroupIDs(ctx context.Context) ([]int64, error)
	ListSubscriberUsersByGroupID(ctx context.Context, groupID int64) ([]GroupWatchSubscriber, error)
	GetGroupWatchStates(ctx context.Context, groupIDs []int64) (map[int64]GroupMonitorWatchState, error)
	UpsertGroupWatchState(ctx context.Context, state GroupMonitorWatchState) error
	InsertNotificationIfNew(ctx context.Context, notif UserGroupWatchNotification) (inserted bool, id int64, err error)
	SummaryNotifications(ctx context.Context, userID int64) (total int, unread int, err error)
	ListNotifications(ctx context.Context, userID int64, unreadOnly bool, limit int) ([]UserGroupWatchNotification, error)
	MarkNotificationRead(ctx context.Context, userID, notificationID int64, readAt time.Time) (bool, error)
	MarkAllNotificationsRead(ctx context.Context, userID int64, readAt time.Time) error
	PruneOldNotifications(ctx context.Context, before time.Time, maxRows int) (int64, error)
}

// GroupWatchGroupAuthorizer checks which groups a non-admin viewer may access.
type GroupWatchGroupAuthorizer interface {
	GetAvailableGroups(ctx context.Context, userID int64) ([]Group, error)
}

// GroupPriceChangeNotifier notifies subscribed users after a group pricing update commits.
type GroupPriceChangeNotifier interface {
	NotifyGroupPriceChanged(ctx context.Context, before, after GroupPricingSnapshot)
}

// GroupMonitorWatchEvaluator evaluates channel-monitor status transitions after aggregation.
type GroupMonitorWatchEvaluator interface {
	EvaluateMonitorTransitions(ctx context.Context, now time.Time)
	PruneOldNotifications(ctx context.Context, now time.Time)
}

// GroupPricingSnapshot captures all user-visible pricing fields of a group by value
// so UpdateGroup can diff before/after cleanly without pointer aliasing.
type GroupPricingSnapshot struct {
	ID                           int64
	Name                         string
	Platform                     string
	Status                       string
	RateMultiplier               float64
	PeakRateEnabled              bool
	PeakStart                    string
	PeakEnd                      string
	PeakRateMultiplier           float64
	ImageRateIndependent         bool
	ImageRateMultiplier          float64
	ImagePrice1K                 *float64
	ImagePrice2K                 *float64
	ImagePrice4K                 *float64
	BatchImageDiscountMultiplier float64
	VideoRateIndependent         bool
	VideoRateMultiplier          float64
	VideoPrice480P               *float64
	VideoPrice720P               *float64
	VideoPrice1080P              *float64
	VideoModelPrices             map[string]map[string]float64
	WebSearchPricePerCall        *float64
	ToolRateIndependent          bool
	ToolRateMultiplier           float64
	SearchPricePer1k             *float64
	FetchPricePer1k              *float64
	OCRPricePer1kPages           *float64
	GrokModerationRefusalPrice   *float64
	AudioRealtimePricePerMin     *float64
	AudioTTSPricePerMillionChars *float64
	AudioSTTPricePerHour         *float64
	FreeOpenAIFast               bool
	OpenAIFastMultiplier         *float64
	OpenAIUltrafastMultiplier    *float64
	LongContextPricingEnabled    bool
	ModelPricing                 []ChannelModelPricing
}

// GroupPriceChangeItem describes one concrete pricing field change.
type GroupPriceChangeItem struct {
	Field   string
	Summary string
}

// SnapshotGroupPricing takes a deep value snapshot of pricing fields from a Group.
func SnapshotGroupPricing(g *Group) GroupPricingSnapshot {
	if g == nil {
		return GroupPricingSnapshot{}
	}
	snap := GroupPricingSnapshot{
		ID:                           g.ID,
		Name:                         g.Name,
		Platform:                     g.Platform,
		Status:                       g.Status,
		RateMultiplier:               g.RateMultiplier,
		PeakRateEnabled:              g.PeakRateEnabled,
		PeakStart:                    g.PeakStart,
		PeakEnd:                      g.PeakEnd,
		PeakRateMultiplier:           g.PeakRateMultiplier,
		ImageRateIndependent:         g.ImageRateIndependent,
		ImageRateMultiplier:          g.ImageRateMultiplier,
		ImagePrice1K:                 cloneFloat64Ptr(g.ImagePrice1K),
		ImagePrice2K:                 cloneFloat64Ptr(g.ImagePrice2K),
		ImagePrice4K:                 cloneFloat64Ptr(g.ImagePrice4K),
		BatchImageDiscountMultiplier: g.BatchImageDiscountMultiplier,
		VideoRateIndependent:         g.VideoRateIndependent,
		VideoRateMultiplier:          g.VideoRateMultiplier,
		VideoPrice480P:               cloneFloat64Ptr(g.VideoPrice480P),
		VideoPrice720P:               cloneFloat64Ptr(g.VideoPrice720P),
		VideoPrice1080P:              cloneFloat64Ptr(g.VideoPrice1080P),
		WebSearchPricePerCall:        cloneFloat64Ptr(g.WebSearchPricePerCall),
		ToolRateIndependent:          g.ToolRateIndependent,
		ToolRateMultiplier:           g.ToolRateMultiplier,
		SearchPricePer1k:             cloneFloat64Ptr(g.SearchPricePer1k),
		FetchPricePer1k:              cloneFloat64Ptr(g.FetchPricePer1k),
		OCRPricePer1kPages:           cloneFloat64Ptr(g.OCRPricePer1kPages),
		GrokModerationRefusalPrice:   cloneFloat64Ptr(g.GrokModerationRefusalPrice),
		AudioRealtimePricePerMin:     cloneFloat64Ptr(g.AudioRealtimePricePerMin),
		AudioTTSPricePerMillionChars: cloneFloat64Ptr(g.AudioTTSPricePerMillionChars),
		AudioSTTPricePerHour:         cloneFloat64Ptr(g.AudioSTTPricePerHour),
		FreeOpenAIFast:               g.FreeOpenAIFast,
		OpenAIFastMultiplier:         cloneFloat64Ptr(g.OpenAIFastMultiplier),
		OpenAIUltrafastMultiplier:    cloneFloat64Ptr(g.OpenAIUltrafastMultiplier),
		LongContextPricingEnabled:    g.LongContextPricingEnabled,
	}
	if len(g.VideoModelPrices) > 0 {
		snap.VideoModelPrices = make(map[string]map[string]float64, len(g.VideoModelPrices))
		for family, resMap := range g.VideoModelPrices {
			copied := make(map[string]float64, len(resMap))
			for res, price := range resMap {
				copied[res] = price
			}
			snap.VideoModelPrices[family] = copied
		}
	}
	if len(g.ModelPricing) > 0 {
		// Deep copy via JSON round-trip so nested interval pointers/slices never alias.
		if raw, err := json.Marshal(g.ModelPricing); err == nil {
			var cloned []ChannelModelPricing
			if json.Unmarshal(raw, &cloned) == nil {
				snap.ModelPricing = cloned
			}
		}
	}
	return snap
}

func cloneFloat64Ptr(v *float64) *float64 {
	if v == nil {
		return nil
	}
	c := *v
	return &c
}

func floatDiffers(a, b float64) bool {
	return math.Abs(a-b) > groupRateEpsilon
}

func floatPtrDiffers(a, b *float64) bool {
	if a == nil && b == nil {
		return false
	}
	if a == nil || b == nil {
		return true
	}
	return math.Abs(*a-*b) > groupRateEpsilon
}

func formatOptPrice(v *float64, unit string) string {
	if v == nil {
		return "默认"
	}
	return fmt.Sprintf("$%.4g%s", *v, unit)
}

func formatOptMultiplier(v *float64) string {
	if v == nil {
		return "默认"
	}
	return fmt.Sprintf("%.2fx", *v)
}

// DiffGroupPricing computes human-readable diffs for any user-visible pricing change.
func DiffGroupPricing(before, after GroupPricingSnapshot) []GroupPriceChangeItem {
	var changes []GroupPriceChangeItem

	if floatDiffers(before.RateMultiplier, after.RateMultiplier) {
		delta := roundGroupRate(after.RateMultiplier - before.RateMultiplier)
		dir := "上涨"
		sign := "+"
		if delta < 0 {
			dir = "下调"
			sign = ""
		}
		changes = append(changes, GroupPriceChangeItem{
			Field:   "rate_multiplier",
			Summary: fmt.Sprintf("基础倍率 %.2fx → %.2fx（%s %s%.2fx）", before.RateMultiplier, after.RateMultiplier, dir, sign, delta),
		})
	}
	if before.PeakRateEnabled != after.PeakRateEnabled ||
		(after.PeakRateEnabled && (before.PeakStart != after.PeakStart || before.PeakEnd != after.PeakEnd || floatDiffers(before.PeakRateMultiplier, after.PeakRateMultiplier))) {
		if after.PeakRateEnabled {
			changes = append(changes, GroupPriceChangeItem{
				Field:   "peak_rate",
				Summary: fmt.Sprintf("高峰倍率调整为 %.2fx（%s–%s）", after.PeakRateMultiplier, after.PeakStart, after.PeakEnd),
			})
		} else {
			changes = append(changes, GroupPriceChangeItem{
				Field:   "peak_rate",
				Summary: "已关闭高峰时段倍率",
			})
		}
	}
	if before.ImageRateIndependent != after.ImageRateIndependent ||
		(after.ImageRateIndependent && floatDiffers(before.ImageRateMultiplier, after.ImageRateMultiplier)) {
		if after.ImageRateIndependent {
			changes = append(changes, GroupPriceChangeItem{
				Field:   "image_rate_multiplier",
				Summary: fmt.Sprintf("图片独立倍率调整为 %.2fx", after.ImageRateMultiplier),
			})
		} else {
			changes = append(changes, GroupPriceChangeItem{
				Field:   "image_rate_multiplier",
				Summary: "图片倍率恢复跟随分组倍率",
			})
		}
	}
	if floatPtrDiffers(before.ImagePrice1K, after.ImagePrice1K) ||
		floatPtrDiffers(before.ImagePrice2K, after.ImagePrice2K) ||
		floatPtrDiffers(before.ImagePrice4K, after.ImagePrice4K) {
		changes = append(changes, GroupPriceChangeItem{
			Field:   "image_prices",
			Summary: fmt.Sprintf("图片单价调整（1K %s / 2K %s / 4K %s）", formatOptPrice(after.ImagePrice1K, ""), formatOptPrice(after.ImagePrice2K, ""), formatOptPrice(after.ImagePrice4K, "")),
		})
	}
	if floatDiffers(before.BatchImageDiscountMultiplier, after.BatchImageDiscountMultiplier) {
		changes = append(changes, GroupPriceChangeItem{
			Field:   "batch_image_discount",
			Summary: fmt.Sprintf("批量出图折扣倍率 %.2fx → %.2fx", before.BatchImageDiscountMultiplier, after.BatchImageDiscountMultiplier),
		})
	}
	if before.VideoRateIndependent != after.VideoRateIndependent ||
		(after.VideoRateIndependent && floatDiffers(before.VideoRateMultiplier, after.VideoRateMultiplier)) {
		if after.VideoRateIndependent {
			changes = append(changes, GroupPriceChangeItem{
				Field:   "video_rate_multiplier",
				Summary: fmt.Sprintf("视频独立倍率调整为 %.2fx", after.VideoRateMultiplier),
			})
		} else {
			changes = append(changes, GroupPriceChangeItem{
				Field:   "video_rate_multiplier",
				Summary: "视频倍率恢复跟随分组倍率",
			})
		}
	}
	if floatPtrDiffers(before.VideoPrice480P, after.VideoPrice480P) ||
		floatPtrDiffers(before.VideoPrice720P, after.VideoPrice720P) ||
		floatPtrDiffers(before.VideoPrice1080P, after.VideoPrice1080P) ||
		!reflect.DeepEqual(before.VideoModelPrices, after.VideoModelPrices) {
		changes = append(changes, GroupPriceChangeItem{
			Field:   "video_prices",
			Summary: "视频计费价格已调整",
		})
	}
	if before.ToolRateIndependent != after.ToolRateIndependent ||
		(after.ToolRateIndependent && floatDiffers(before.ToolRateMultiplier, after.ToolRateMultiplier)) {
		if after.ToolRateIndependent {
			changes = append(changes, GroupPriceChangeItem{
				Field:   "tool_rate_multiplier",
				Summary: fmt.Sprintf("工具独立倍率调整为 %.2fx", after.ToolRateMultiplier),
			})
		} else {
			changes = append(changes, GroupPriceChangeItem{
				Field:   "tool_rate_multiplier",
				Summary: "工具倍率恢复跟随分组倍率",
			})
		}
	}
	if floatPtrDiffers(before.SearchPricePer1k, after.SearchPricePer1k) ||
		floatPtrDiffers(before.FetchPricePer1k, after.FetchPricePer1k) ||
		floatPtrDiffers(before.OCRPricePer1kPages, after.OCRPricePer1kPages) ||
		floatPtrDiffers(before.WebSearchPricePerCall, after.WebSearchPricePerCall) ||
		floatPtrDiffers(before.GrokModerationRefusalPrice, after.GrokModerationRefusalPrice) ||
		floatPtrDiffers(before.AudioRealtimePricePerMin, after.AudioRealtimePricePerMin) ||
		floatPtrDiffers(before.AudioTTSPricePerMillionChars, after.AudioTTSPricePerMillionChars) ||
		floatPtrDiffers(before.AudioSTTPricePerHour, after.AudioSTTPricePerHour) {
		changes = append(changes, GroupPriceChangeItem{
			Field:   "tool_and_media_prices",
			Summary: "工具/搜索/语音等按次计费单价已调整",
		})
	}
	if before.FreeOpenAIFast != after.FreeOpenAIFast ||
		floatPtrDiffers(before.OpenAIFastMultiplier, after.OpenAIFastMultiplier) ||
		floatPtrDiffers(before.OpenAIUltrafastMultiplier, after.OpenAIUltrafastMultiplier) {
		changes = append(changes, GroupPriceChangeItem{
			Field:   "openai_tier_multipliers",
			Summary: fmt.Sprintf("OpenAI 档位倍率调整（Fast %s / UltraFast %s）", formatOptMultiplier(after.OpenAIFastMultiplier), formatOptMultiplier(after.OpenAIUltrafastMultiplier)),
		})
	}
	if before.LongContextPricingEnabled != after.LongContextPricingEnabled ||
		!reflect.DeepEqual(before.ModelPricing, after.ModelPricing) {
		changes = append(changes, GroupPriceChangeItem{
			Field:   "model_pricing",
			Summary: "分组模型自定义定价已更新",
		})
	}
	return changes
}

// ClassifyMonitorBucketStatus maps one channel-monitor V2 5m bucket to the exact
// visual status band rendered on the V3 card timeline (monitorBarClass):
//   - invalid/false: health.overall == "unknown" (insufficient samples -> grey bar)
//   - "black": cache_rate < 30% OR availability < 30%
//   - "green": cache_rate >= 30% AND availability >= 80%
//   - "other": intermediate red/amber/yellow band (30% <= avail < 80%, cache >= 30%)
func ClassifyMonitorBucketStatus(bucket ChannelMonitorV2TrendPoint) (status string, cachePct float64, availPct float64, valid bool) {
	overall := strings.TrimSpace(bucket.Health.Overall)
	if overall == "" || overall == "unknown" {
		return "", 0, 0, false
	}
	availPct = (1.0 - bucket.Metrics.ErrorRate) * 100.0
	if availPct < 0 {
		availPct = 0
	} else if availPct > 100 {
		availPct = 100
	}
	cachePct = bucket.Metrics.CacheRate * 100.0
	if cachePct < 0 {
		cachePct = 0
	} else if cachePct > 100 {
		cachePct = 100
	}
	if cachePct < GroupWatchCacheBlackThresholdPct || availPct < GroupWatchAvailBlackThresholdPct {
		return GroupWatchStatusBlack, cachePct, availPct, true
	}
	if availPct >= GroupWatchAvailGreenThresholdPct {
		return GroupWatchStatusGreen, cachePct, availPct, true
	}
	return GroupWatchStatusOther, cachePct, availPct, true
}

// GroupWatchService manages user subscriptions to channel-monitor groups and
// dispatches in-app, Bark push, and email alerts on price/status transitions.
type GroupWatchService struct {
	repo                     GroupWatchRepository
	monitorRepo              ChannelMonitorV2Repository
	groupAuthorizer          GroupWatchGroupAuthorizer
	settingRepo              SettingRepository
	notificationEmailService *NotificationEmailService

	pushMu       sync.RWMutex
	pushNotifier PushNotifier

	// asyncDispatch allows tests to toggle synchronous delivery deterministically.
	syncDispatch bool
	now          func() time.Time
}

func NewGroupWatchService(
	repo GroupWatchRepository,
	monitorRepo ChannelMonitorV2Repository,
	groupAuthorizer GroupWatchGroupAuthorizer,
	settingRepo SettingRepository,
	notificationEmailService *NotificationEmailService,
) *GroupWatchService {
	return &GroupWatchService{
		repo:                     repo,
		monitorRepo:              monitorRepo,
		groupAuthorizer:          groupAuthorizer,
		settingRepo:              settingRepo,
		notificationEmailService: notificationEmailService,
		now:                      func() time.Time { return time.Now().UTC() },
	}
}

func (s *GroupWatchService) SetPushNotifier(n PushNotifier) {
	if s == nil {
		return
	}
	s.pushMu.Lock()
	s.pushNotifier = n
	s.pushMu.Unlock()
}

func (s *GroupWatchService) getPushNotifier() PushNotifier {
	if s == nil {
		return nil
	}
	s.pushMu.RLock()
	n := s.pushNotifier
	s.pushMu.RUnlock()
	return n
}

// ListSubscribedGroupIDs returns the sorted group IDs subscribed by userID.
func (s *GroupWatchService) ListSubscribedGroupIDs(ctx context.Context, userID int64) ([]int64, error) {
	if s == nil || s.repo == nil || userID <= 0 {
		return []int64{}, nil
	}
	ids, err := s.repo.ListSubscribedGroupIDs(ctx, userID)
	if err != nil {
		return nil, err
	}
	if ids == nil {
		return []int64{}, nil
	}
	return ids, nil
}

// Subscribe subscribes userID to groupID after verifying viewer access.
func (s *GroupWatchService) Subscribe(ctx context.Context, userID, groupID int64, isAdmin bool) error {
	if s == nil || s.repo == nil {
		return ErrGroupWatchAuthorizationRequired
	}
	if userID <= 0 || groupID <= 0 {
		return ErrGroupWatchInvalidGroupID
	}
	if !isAdmin {
		allowed, err := s.canUserAccessGroup(ctx, userID, "user", groupID)
		if err != nil {
			return err
		}
		if !allowed {
			return ErrGroupWatchGroupNotAvailable
		}
	}
	if err := s.repo.Subscribe(ctx, userID, groupID); err != nil {
		return err
	}
	// Seed current visual state if missing so a user subscribing while a group is
	// already black immediately receives the green recovery alert on the next tick.
	s.seedGroupStateBestEffort(groupID)
	return nil
}

// Unsubscribe removes userID's subscription to groupID.
func (s *GroupWatchService) Unsubscribe(ctx context.Context, userID, groupID int64) error {
	if s == nil || s.repo == nil {
		return nil
	}
	if userID <= 0 || groupID <= 0 {
		return ErrGroupWatchInvalidGroupID
	}
	return s.repo.Unsubscribe(ctx, userID, groupID)
}

func (s *GroupWatchService) SummaryNotifications(ctx context.Context, userID int64) (int, int, error) {
	if s == nil || s.repo == nil || userID <= 0 {
		return 0, 0, nil
	}
	return s.repo.SummaryNotifications(ctx, userID)
}

func (s *GroupWatchService) ListNotifications(ctx context.Context, userID int64, unreadOnly bool, limit int) ([]UserGroupWatchNotification, error) {
	if s == nil || s.repo == nil || userID <= 0 {
		return []UserGroupWatchNotification{}, nil
	}
	if limit <= 0 || limit > 100 {
		limit = groupWatchNotificationLimit
	}
	items, err := s.repo.ListNotifications(ctx, userID, unreadOnly, limit)
	if err != nil {
		return nil, err
	}
	if items == nil {
		return []UserGroupWatchNotification{}, nil
	}
	return items, nil
}

func (s *GroupWatchService) MarkNotificationRead(ctx context.Context, userID, notificationID int64) error {
	if s == nil || s.repo == nil || userID <= 0 || notificationID <= 0 {
		return ErrGroupWatchNotificationNotFound
	}
	found, err := s.repo.MarkNotificationRead(ctx, userID, notificationID, s.now())
	if err != nil {
		return err
	}
	if !found {
		return ErrGroupWatchNotificationNotFound
	}
	return nil
}

func (s *GroupWatchService) MarkAllNotificationsRead(ctx context.Context, userID int64) error {
	if s == nil || s.repo == nil || userID <= 0 {
		return nil
	}
	return s.repo.MarkAllNotificationsRead(ctx, userID, s.now())
}

func (s *GroupWatchService) PruneOldNotifications(ctx context.Context, now time.Time) {
	if s == nil || s.repo == nil {
		return
	}
	if now.IsZero() {
		now = s.now()
	}
	deleted, err := s.repo.PruneOldNotifications(ctx, now.Add(-groupWatchNotificationRetention), groupWatchPruneBatchSize)
	if err != nil {
		logger.LegacyPrintf("service.group_watch", "[GroupWatch] prune notifications failed: %v", err)
		return
	}
	if deleted > 0 {
		logger.LegacyPrintf("service.group_watch", "[GroupWatch] pruned %d old notifications", deleted)
	}
}

func (s *GroupWatchService) canUserAccessGroup(ctx context.Context, userID int64, role string, groupID int64) (bool, error) {
	if strings.EqualFold(strings.TrimSpace(role), "admin") {
		return true, nil
	}
	if s.groupAuthorizer == nil {
		return false, ErrGroupWatchAuthorizationRequired
	}
	groups, err := s.groupAuthorizer.GetAvailableGroups(ctx, userID)
	if err != nil {
		return false, err
	}
	for i := range groups {
		if groups[i].ID == groupID {
			return true, nil
		}
	}
	return false, nil
}

func (s *GroupWatchService) seedGroupStateBestEffort(groupID int64) {
	if s == nil || s.repo == nil || s.monitorRepo == nil || groupID <= 0 {
		return
	}
	run := func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("panic seeding group watch state", "group_id", groupID, "recover", r)
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		states, err := s.repo.GetGroupWatchStates(ctx, []int64{groupID})
		if err != nil {
			return
		}
		if _, exists := states[groupID]; exists {
			return
		}
		cfg, err := s.monitorRepo.GetConfig(ctx)
		if err != nil || cfg == nil || !cfg.Enabled {
			return
		}
		now := s.now().UTC()
		filter := buildGroupWatchMatrixFilter(now, []int64{groupID})
		matrix, err := s.monitorRepo.GetMatrix(ctx, filter, *cfg, ChannelMonitorV2GroupByPlatformGroup, true)
		if err != nil || matrix == nil {
			return
		}
		for i := range matrix.Items {
			row := &matrix.Items[i]
			if row.GroupID == nil || *row.GroupID != groupID {
				continue
			}
			bucket, status, cachePct, availPct, ok := latestFreshClassifiedBucket(row.Buckets, now)
			if !ok {
				return
			}
			_ = s.repo.UpsertGroupWatchState(ctx, GroupMonitorWatchState{
				GroupID:         groupID,
				Status:          status,
				BlackNotified:   status == GroupWatchStatusBlack,
				LastBucketStart: bucket.BucketStart.UTC(),
				CacheRate:       cachePct / 100.0,
				AvailabilityPct: availPct,
				UpdatedAt:       now,
			})
			return
		}
	}
	if s.syncDispatch {
		run()
		return
	}
	go run()
}

func buildGroupWatchMatrixFilter(now time.Time, groupIDs []int64) ChannelMonitorV2Filter {
	bucket := 5 * time.Minute
	window := 90 * time.Minute
	end := now.UTC().Truncate(bucket).Add(bucket)
	start := end.Add(-window)
	return ChannelMonitorV2Filter{
		Range:    "90m",
		GroupIDs: normalizeInt64Set(groupIDs),
		Start:    start,
		End:      end,
		Bucket:   bucket,
	}
}

func latestFreshClassifiedBucket(buckets []ChannelMonitorV2TrendPoint, now time.Time) (ChannelMonitorV2TrendPoint, string, float64, float64, bool) {
	if len(buckets) == 0 {
		return ChannelMonitorV2TrendPoint{}, "", 0, 0, false
	}
	sorted := append([]ChannelMonitorV2TrendPoint(nil), buckets...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].BucketStart.Before(sorted[j].BucketStart)
	})
	for i := len(sorted) - 1; i >= 0; i-- {
		b := sorted[i]
		if b.BucketStart.IsZero() {
			continue
		}
		if now.Sub(b.BucketStart.UTC()) > groupWatchFreshBucketMaxAge {
			// Remaining earlier buckets are older still.
			break
		}
		status, cachePct, availPct, valid := ClassifyMonitorBucketStatus(b)
		if !valid {
			continue
		}
		return b, status, cachePct, availPct, true
	}
	return ChannelMonitorV2TrendPoint{}, "", 0, 0, false
}

type groupWatchDispatchEvent struct {
	GroupID        int64
	GroupName      string
	Platform       string
	EventType      string
	EmailEvent     string
	DedupeKey      string
	Title          string
	Content        string
	BarkBody       string
	Metadata       map[string]string
	EmailVariables map[string]string
	ReminderKey    string
	OccurredAt     time.Time
}

// NotifyGroupPriceChanged inspects before/after pricing snapshots and notifies
// active authorized subscribers if any pricing field changed.
func (s *GroupWatchService) NotifyGroupPriceChanged(ctx context.Context, before, after GroupPricingSnapshot) {
	if s == nil || s.repo == nil || after.ID <= 0 {
		return
	}
	changes := DiffGroupPricing(before, after)
	if len(changes) == 0 {
		return
	}
	now := s.now().UTC()
	summaries := make([]string, 0, len(changes))
	fields := make([]string, 0, len(changes))
	for _, ch := range changes {
		summaries = append(summaries, ch.Summary)
		fields = append(fields, ch.Field)
	}
	summaryText := strings.Join(summaries, "；")
	groupName := strings.TrimSpace(after.Name)
	if groupName == "" {
		groupName = fmt.Sprintf("分组 #%d", after.ID)
	}
	platformLabel := groupWatchPlatformLabel(after.Platform)
	changedAtStr := now.In(time.FixedZone("CST", 8*3600)).Format("2006-01-02 15:04:05")

	hashInput := fmt.Sprintf("%d|%s|%d", after.ID, summaryText, now.Unix()/60)
	sum := sha256.Sum256([]byte(hashInput))
	shortHash := hex.EncodeToString(sum[:8])

	event := groupWatchDispatchEvent{
		GroupID:    after.ID,
		GroupName:  groupName,
		Platform:   after.Platform,
		EventType:  GroupWatchEventPriceChanged,
		EmailEvent: NotificationEmailEventGroupWatchPriceChanged,
		DedupeKey:  fmt.Sprintf("group:%d:price:%s", after.ID, shortHash),
		Title:      fmt.Sprintf("分组改价：%s", groupName),
		Content:    fmt.Sprintf("您订阅的分组「%s」（%s）发生价格调整：%s。", groupName, platformLabel, summaryText),
		BarkBody:   fmt.Sprintf("%s：%s", groupName, summaryText),
		Metadata: map[string]string{
			"group_id":       strconv.FormatInt(after.ID, 10),
			"group_name":     groupName,
			"platform":       after.Platform,
			"changed_fields": strings.Join(fields, ","),
			"summary":        summaryText,
			"old_rate":       fmt.Sprintf("%.4g", before.RateMultiplier),
			"new_rate":       fmt.Sprintf("%.4g", after.RateMultiplier),
		},
		EmailVariables: map[string]string{
			"group_name":           groupName,
			"platform":             platformLabel,
			"price_change_summary": summaryText,
			"changed_at":           changedAtStr,
		},
		ReminderKey: "price-" + shortHash,
		OccurredAt:  now,
	}

	if s.syncDispatch {
		s.deliverEventToSubscribers(ctx, event)
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("panic dispatching group price change notification", "group_id", after.ID, "recover", r)
			}
		}()
		dispatchCtx, cancel := context.WithTimeout(context.Background(), groupWatchDeliveryTimeout)
		defer cancel()
		s.deliverEventToSubscribers(dispatchCtx, event)
	}()
}

// EvaluateMonitorTransitions checks all groups that have at least one active
// subscriber and emits status_black / status_green alerts on confirmed transitions.
// Expected to run inside ChannelMonitorV2Aggregator under the singleton leader lock.
func (s *GroupWatchService) EvaluateMonitorTransitions(ctx context.Context, now time.Time) {
	if s == nil || s.repo == nil || s.monitorRepo == nil {
		return
	}
	if now.IsZero() {
		now = s.now()
	}
	now = now.UTC()

	watchedGroupIDs, err := s.repo.ListWatchedGroupIDs(ctx)
	if err != nil {
		logger.LegacyPrintf("service.group_watch", "[GroupWatch] list watched groups failed: %v", err)
		return
	}
	if len(watchedGroupIDs) == 0 {
		return
	}

	cfg, err := s.monitorRepo.GetConfig(ctx)
	if err != nil || cfg == nil || !cfg.Enabled {
		return
	}

	filter := buildGroupWatchMatrixFilter(now, watchedGroupIDs)
	matrix, err := s.monitorRepo.GetMatrix(ctx, filter, *cfg, ChannelMonitorV2GroupByPlatformGroup, true)
	if err != nil || matrix == nil || len(matrix.Items) == 0 {
		if err != nil {
			logger.LegacyPrintf("service.group_watch", "[GroupWatch] load matrix failed: %v", err)
		}
		return
	}

	prevStates, err := s.repo.GetGroupWatchStates(ctx, watchedGroupIDs)
	if err != nil {
		logger.LegacyPrintf("service.group_watch", "[GroupWatch] load watch states failed: %v", err)
		return
	}

	for i := range matrix.Items {
		row := &matrix.Items[i]
		if row.GroupID == nil || *row.GroupID <= 0 {
			continue
		}
		gid := *row.GroupID
		latestBucket, currStatus, cachePct, availPct, ok := latestFreshClassifiedBucket(row.Buckets, now)
		if !ok {
			// Insufficient samples ("unknown") or stale data never flips state.
			continue
		}
		bucketStart := latestBucket.BucketStart.UTC()

		prev, hasPrev := prevStates[gid]
		if !hasPrev {
			// First observation: seed quietly without alerting.
			seed := GroupMonitorWatchState{
				GroupID:         gid,
				Status:          currStatus,
				BlackNotified:   currStatus == GroupWatchStatusBlack,
				LastBucketStart: bucketStart,
				CacheRate:       cachePct / 100.0,
				AvailabilityPct: availPct,
				UpdatedAt:       now,
			}
			if err := s.repo.UpsertGroupWatchState(ctx, seed); err != nil {
				logger.LegacyPrintf("service.group_watch", "[GroupWatch] seed state group=%d failed: %v", gid, err)
			}
			continue
		}

		if bucketStart.Before(prev.LastBucketStart) {
			continue
		}

		nextState := GroupMonitorWatchState{
			GroupID:         gid,
			Status:          currStatus,
			BlackNotified:   prev.BlackNotified,
			LastBucketStart: bucketStart,
			CacheRate:       cachePct / 100.0,
			AvailabilityPct: availPct,
			UpdatedAt:       now,
		}

		var event *groupWatchDispatchEvent
		groupName := strings.TrimSpace(row.GroupName)
		if groupName == "" {
			groupName = fmt.Sprintf("分组 #%d", gid)
		}
		platformLabel := groupWatchPlatformLabel(row.Platform)
		changedAtStr := now.In(time.FixedZone("CST", 8*3600)).Format("2006-01-02 15:04:05")
		cacheStr := fmt.Sprintf("%.1f%%", cachePct)
		availStr := fmt.Sprintf("%.1f%%", availPct)

		switch {
		case currStatus == GroupWatchStatusBlack && prev.Status != GroupWatchStatusBlack:
			nextState.BlackNotified = true
			reason := describeBlackReason(cachePct, availPct)
			ev := groupWatchDispatchEvent{
				GroupID:    gid,
				GroupName:  groupName,
				Platform:   row.Platform,
				EventType:  GroupWatchEventStatusBlack,
				EmailEvent: NotificationEmailEventGroupWatchStatusBlack,
				DedupeKey:  fmt.Sprintf("group:%d:status_black:%d", gid, bucketStart.Unix()),
				Title:      fmt.Sprintf("分组不可用（黑）：%s", groupName),
				Content:    fmt.Sprintf("您订阅的分组「%s」（%s）已变为黑色不可用（%s；当前可用率 %s，缓存率 %s）。", groupName, platformLabel, reason, availStr, cacheStr),
				BarkBody:   fmt.Sprintf("%s 变黑不可用：可用率 %s · 缓存率 %s", groupName, availStr, cacheStr),
				Metadata: map[string]string{
					"group_id":          strconv.FormatInt(gid, 10),
					"group_name":        groupName,
					"platform":          row.Platform,
					"cache_rate":        cacheStr,
					"availability_rate": availStr,
					"reason":            reason,
					"bucket_start":      bucketStart.Format(time.RFC3339),
				},
				EmailVariables: map[string]string{
					"group_name":        groupName,
					"platform":          platformLabel,
					"cache_rate":        cacheStr,
					"availability_rate": availStr,
					"status_reason":     reason,
					"changed_at":        changedAtStr,
				},
				ReminderKey: fmt.Sprintf("black-%d", bucketStart.Unix()),
				OccurredAt:  now,
			}
			event = &ev

		case currStatus == GroupWatchStatusGreen && (prev.Status == GroupWatchStatusBlack || prev.BlackNotified):
			nextState.BlackNotified = false
			ev := groupWatchDispatchEvent{
				GroupID:    gid,
				GroupName:  groupName,
				Platform:   row.Platform,
				EventType:  GroupWatchEventStatusGreen,
				EmailEvent: NotificationEmailEventGroupWatchStatusGreen,
				DedupeKey:  fmt.Sprintf("group:%d:status_green:%d", gid, bucketStart.Unix()),
				Title:      fmt.Sprintf("分组已恢复（绿）：%s", groupName),
				Content:    fmt.Sprintf("您订阅的分组「%s」（%s）已恢复绿色可用（当前可用率 %s，缓存率 %s）。", groupName, platformLabel, availStr, cacheStr),
				BarkBody:   fmt.Sprintf("%s 已恢复绿色：可用率 %s · 缓存率 %s", groupName, availStr, cacheStr),
				Metadata: map[string]string{
					"group_id":          strconv.FormatInt(gid, 10),
					"group_name":        groupName,
					"platform":          row.Platform,
					"cache_rate":        cacheStr,
					"availability_rate": availStr,
					"bucket_start":      bucketStart.Format(time.RFC3339),
				},
				EmailVariables: map[string]string{
					"group_name":        groupName,
					"platform":          platformLabel,
					"cache_rate":        cacheStr,
					"availability_rate": availStr,
					"changed_at":        changedAtStr,
				},
				ReminderKey: fmt.Sprintf("green-%d", bucketStart.Unix()),
				OccurredAt:  now,
			}
			event = &ev

		case currStatus == GroupWatchStatusBlack:
			nextState.BlackNotified = true
		case currStatus == GroupWatchStatusGreen:
			nextState.BlackNotified = false
		}

		if err := s.repo.UpsertGroupWatchState(ctx, nextState); err != nil {
			logger.LegacyPrintf("service.group_watch", "[GroupWatch] update state group=%d failed: %v", gid, err)
			continue
		}
		if event != nil {
			s.deliverEventToSubscribers(ctx, *event)
		}
	}
}

func describeBlackReason(cachePct, availPct float64) string {
	lowCache := cachePct < GroupWatchCacheBlackThresholdPct
	lowAvail := availPct < GroupWatchAvailBlackThresholdPct
	switch {
	case lowCache && lowAvail:
		return "缓存率与可用率均低于 30%"
	case lowCache:
		return "缓存率低于 30%"
	case lowAvail:
		return "可用率低于 30%"
	default:
		return "状态异常"
	}
}

func groupWatchPlatformLabel(platform string) string {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case PlatformAnthropic:
		return "Anthropic"
	case PlatformOpenAI:
		return "OpenAI"
	case PlatformGemini:
		return "Gemini"
	case PlatformGrok:
		return "Grok"
	case PlatformAntigravity:
		return "Antigravity"
	case PlatformKimi:
		return "Kimi"
	case PlatformZhipu:
		return "Zhipu"
	case PlatformDeepseek:
		return "DeepSeek"
	case "minimax":
		return "MiniMax"
	default:
		if strings.TrimSpace(platform) == "" {
			return "Unknown"
		}
		return platform
	}
}

func (s *GroupWatchService) resolveChannelStatusURL(ctx context.Context) string {
	if s.settingRepo != nil {
		if raw, err := s.settingRepo.GetValue(ctx, SettingKeyFrontendURL); err == nil {
			if base := strings.TrimRight(strings.TrimSpace(raw), "/"); base != "" {
				return base + "/monitor"
			}
		}
	}
	return "/monitor"
}

func (s *GroupWatchService) deliverEventToSubscribers(ctx context.Context, event groupWatchDispatchEvent) {
	subs, err := s.repo.ListSubscriberUsersByGroupID(ctx, event.GroupID)
	if err != nil {
		logger.LegacyPrintf("service.group_watch", "[GroupWatch] list subscribers group=%d failed: %v", event.GroupID, err)
		return
	}
	if len(subs) == 0 {
		return
	}

	statusURL := s.resolveChannelStatusURL(ctx)
	pushNotifier := s.getPushNotifier()
	sem := make(chan struct{}, groupWatchDeliveryConcurrency)
	var wg sync.WaitGroup

	for _, sub := range subs {
		sub := sub
		allowed, accessErr := s.canUserAccessGroup(ctx, sub.UserID, sub.Role, event.GroupID)
		if accessErr != nil {
			logger.LegacyPrintf("service.group_watch", "[GroupWatch] access check failed user=%d group=%d: %v", sub.UserID, event.GroupID, accessErr)
			continue
		}
		if !allowed {
			continue
		}

		inserted, _, insertErr := s.repo.InsertNotificationIfNew(ctx, UserGroupWatchNotification{
			UserID:    sub.UserID,
			GroupID:   event.GroupID,
			EventType: event.EventType,
			DedupeKey: event.DedupeKey,
			Title:     event.Title,
			Content:   event.Content,
			Metadata:  event.Metadata,
			CreatedAt: event.OccurredAt,
		})
		if insertErr != nil {
			logger.LegacyPrintf("service.group_watch", "[GroupWatch] insert notification user=%d group=%d failed: %v", sub.UserID, event.GroupID, insertErr)
			continue
		}
		if !inserted {
			// Already delivered for this user + dedupe key.
			continue
		}

		deliverChannels := func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("panic in group watch channel delivery", "user_id", sub.UserID, "group_id", event.GroupID, "recover", r)
				}
			}()
			if pushNotifier != nil {
				pushCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				if err := pushNotifier.PushToUser(pushCtx, sub.UserID, event.Title, event.BarkBody, groupWatchBarkGroup); err != nil {
					slog.Warn("group watch bark push failed", "user_id", sub.UserID, "group_id", event.GroupID, "error", err)
				}
				cancel()
			}
			if s.notificationEmailService != nil && event.EmailEvent != "" {
				s.sendSubscriberEmails(ctx, sub, event, statusURL)
			}
		}

		if s.syncDispatch {
			deliverChannels()
			continue
		}
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			deliverChannels()
		}()
	}
	wg.Wait()
}

// collectGroupWatchEmailRecipients returns the deduplicated list of email
// addresses to notify for a subscribed group event:
//  1. The user's registered email (when valid and non-synthetic), regardless of
//     low-balance primary-email opt-out;
//  2. All verified, enabled extra notification emails configured in Profile.
func collectGroupWatchEmailRecipients(sub GroupWatchSubscriber) []string {
	recipients := make([]string, 0, len(sub.ExtraNotifyEmails)+1)
	seen := make(map[string]struct{}, len(sub.ExtraNotifyEmails)+1)
	if IsValidNotifyEmail(sub.Email) {
		primary := strings.TrimSpace(sub.Email)
		recipients = append(recipients, primary)
		seen[strings.ToLower(primary)] = struct{}{}
	}
	for _, entry := range sub.ExtraNotifyEmails {
		if entry.Disabled || !entry.Verified {
			continue
		}
		addr := strings.TrimSpace(entry.Email)
		if !IsValidNotifyEmail(addr) {
			continue
		}
		lower := strings.ToLower(addr)
		if _, dup := seen[lower]; dup {
			continue
		}
		seen[lower] = struct{}{}
		recipients = append(recipients, addr)
	}
	return recipients
}

func (s *GroupWatchService) sendSubscriberEmails(ctx context.Context, sub GroupWatchSubscriber, event groupWatchDispatchEvent, statusURL string) {
	recipients := collectGroupWatchEmailRecipients(sub)
	if len(recipients) == 0 {
		return
	}
	displayName := strings.TrimSpace(sub.Username)
	if displayName == "" {
		displayName = strings.TrimSpace(sub.Email)
	}
	vars := make(map[string]string, len(event.EmailVariables)+1)
	for k, v := range event.EmailVariables {
		vars[k] = v
	}
	vars["channel_status_url"] = statusURL

	for _, to := range recipients {
		sendCtx, cancel := context.WithTimeout(ctx, emailSendTimeout)
		err := s.notificationEmailService.Send(sendCtx, NotificationEmailSendInput{
			Event:          event.EmailEvent,
			RecipientEmail: to,
			RecipientName:  displayName,
			UserID:         sub.UserID,
			SourceType:     "group_watch",
			SourceID:       fmt.Sprintf("%d:%d", event.GroupID, sub.UserID),
			ReminderKey:    event.ReminderKey,
			Variables:      vars,
		})
		cancel()
		if err != nil {
			// Expected skip when SMTP is unconfigured or user unsubscribed; log non-fatal.
			var cfgErr notificationEmailConfigError
			if errors.As(err, &cfgErr) {
				return
			}
			slog.Warn("group watch email delivery failed", "user_id", sub.UserID, "group_id", event.GroupID, "event", event.EmailEvent, "error", err)
		}
	}
}
