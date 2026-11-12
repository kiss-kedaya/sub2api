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
// adds takes effect immediately for the triggering request as well. On a hit it
// also disables the API keys observed from that IP (status=inactive, never
// deleted, upstream accounts untouched). Otherwise it is observation-only: it
// never rejects or blocks a request.
func GatewayIPSharing(settings *service.SettingService, detector *IPKeySharingDetector, keys *service.APIKeyService) gin.HandlerFunc {
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

		// Record the IP first: the notice policy is the reversible half of the
		// action. The key write is attempted regardless, so one failing half
		// never silently leaves the other undone.
		added, appendErr := settings.AppendGatewayIPNoticeAutoEntry(ctx, hit.AutoEntry())
		disabled, disableErr := disableSharedKeys(ctx, keys, hit)
		attrs := []any{
			"ip", hit.IP,
			"distinct_users", hit.DistinctUsers,
			"window_minutes", hit.WindowMinutes,
			"key_prefixes", hit.KeyPrefixes,
			"key_ids", hit.KeyIDs,
			"auto_added", added,
			"disabled_keys", disabled,
		}
		if appendErr != nil {
			attrs = append(attrs, "append_error", appendErr)
		}
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
