package middleware

import (
	"context"
	"log/slog"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ipnotice"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// GatewayIPSharing auto-adds a source IP to the "指定 IP 提示与限流" list when
// several DIFFERENT user accounts call the gateway from it inside the sliding
// window (default: 3 distinct users / 5 minutes) AND the address is positively
// classified as hosting/proxy infrastructure.
//
// It sits after API-key authentication and before GatewayIPNotice, so the IP it
// adds takes effect immediately for the triggering request as well. It is
// observation-only by default: it appends the IP and logs the hit, and never
// rejects a request. Disabling the involved keys is an explicit opt-in
// (Settings.KeyDisableEnabled); only then does it flip them to inactive
// (status=inactive, never deleted, upstream accounts untouched).
func GatewayIPSharing(settings *service.SettingService, detector *IPKeySharingDetector, keys *service.APIKeyService) gin.HandlerFunc {
	// The Cloudflare range list is refreshed once per process: immediately here
	// and then daily. A failed fetch keeps the embedded snapshot, so detection
	// never loses the exclusion because of a network problem.
	ipnotice.StartCloudflareRangesRefresher()
	return func(c *gin.Context) {
		if settings == nil || detector == nil {
			c.Next()
			return
		}
		// Health checks, probes, metrics and static assets must never count
		// toward "one IP, several users".
		if !ipnotice.IsGatewayBusinessPath(c.Request.URL.Path) {
			c.Next()
			return
		}
		apiKey, ok := GetAPIKeyFromContext(c)
		if !ok || apiKey == nil {
			c.Next()
			return
		}
		userID := apiKey.UserID
		if apiKey.User != nil && apiKey.User.ID > 0 {
			userID = apiKey.User.ID
		}
		if userID <= 0 {
			c.Next()
			return
		}
		ip := SecurityClientIP(c)
		hit, detected := detector.Observe(c.Request.Context(), ip, userID, apiKey)
		if !detected {
			c.Next()
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		// The escalation switch is opt-in. Reading it off the cached policy
		// keeps the hit path free of an extra settings read; a replica adopts an
		// edit within the same five seconds as every other policy field, and an
		// unreadable policy stays at the safe default (observe only).
		keyDisable := settings.GatewayIPNoticeSnapshot().KeyDisable()

		// Record the IP first: the notice policy is the reversible half of the
		// action. In opt-in mode the key write still runs when the append fails,
		// so one failing half never silently leaves the other undone.
		//
		// Upsert rather than append: a later, larger burst from an IP that is
		// already listed must move peak_users, total_keys and last_seen_at
		// forward instead of being dropped as a duplicate.
		added, appendErr := settings.UpsertGatewayIPNoticeAutoEntry(ctx, hit.AutoEntry())
		attrs := []any{
			"ip", hit.IP,
			"distinct_users", hit.DistinctUsers,
			"peak_users", hit.PeakUsers,
			"total_keys", hit.TotalKeys,
			"window_minutes", hit.WindowMinutes,
			"key_prefixes", hit.KeyPrefixes,
			"auto_added", added,
		}
		if appendErr != nil {
			attrs = append(attrs, "append_error", appendErr)
		}
		if !keyDisable {
			// Default mode: append the IP and keep every key exactly as it is.
			// mode=observe_only is the operator-facing marker that makes this
			// line distinguishable from the opt-in action below.
			attrs = append(attrs, "mode", ipSharingModeObserveOnly)
			if appendErr != nil {
				slog.Error("gateway ip key sharing detected, observe only, ip append failed", attrs...)
				c.Next()
				return
			}
			slog.Warn("gateway ip key sharing detected, observe only: ip added, keys untouched", attrs...)
			c.Next()
			return
		}
		disabled, disableErr := disableSharedKeys(ctx, keys, hit)
		attrs = append(attrs, "mode", ipSharingModeKeyDisable, "key_ids", hit.KeyIDs, "disabled_keys", disabled)
		if disableErr != nil {
			attrs = append(attrs, "disable_error", disableErr)
		}
		if appendErr != nil || disableErr != nil {
			slog.Error("gateway ip key sharing detected, action partially failed", attrs...)
			c.Next()
			return
		}
		slog.Warn("gateway ip key sharing detected, ip auto added and shared keys disabled", attrs...)
		c.Next()
	}
}

// Log modes for one sharing hit. observe_only is the default and the only mode
// that runs without an explicit operator opt-in.
const (
	ipSharingModeObserveOnly = "observe_only"
	ipSharingModeKeyDisable  = "key_disable"
)

// disableSharedKeys flips every involved key to inactive. A nil service (tests
// and deployments without the API-key service) makes it a no-op.
func disableSharedKeys(ctx context.Context, keys *service.APIKeyService, hit IPKeySharingHit) ([]int64, error) {
	if keys == nil || len(hit.KeyIDs) == 0 {
		return nil, nil
	}
	return keys.DisableKeysForRisk(ctx, hit.KeyIDs, service.APIKeyRiskInfo{
		Reason:        service.KeyRiskReasonAutoIPSharing,
		IP:            hit.IP,
		DistinctUsers: hit.DistinctUsers,
		WindowMinutes: hit.WindowMinutes,
		HitAt:         hit.FirstSeenAt,
	})
}
