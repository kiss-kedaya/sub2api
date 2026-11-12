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
// window (default: 3 distinct users / 10 minutes).
//
// It sits after API-key authentication and before GatewayIPNotice, so the IP it
// adds takes effect immediately for the triggering request as well. It is
// observation-only: it never rejects, blocks or mutates an account or key.
func GatewayIPSharing(settings *service.SettingService, detector *IPKeySharingDetector) gin.HandlerFunc {
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
		hit, detected := detector.Observe(c.Request.Context(), ip, userID, apiKey.Key)
		if !detected {
			c.Next()
			return
		}

		// The settings write is bounded and off the request's own context: a
		// cancelled downstream call must not hide the evidence.
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		added, err := settings.AppendGatewayIPNoticeAutoEntry(ctx, hit.AutoEntry())
		attrs := []any{
			"ip", hit.IP,
			"distinct_users", hit.DistinctUsers,
			"window_minutes", hit.WindowMinutes,
			"key_prefixes", hit.KeyPrefixes,
			"auto_added", added,
		}
		if err != nil {
			attrs = append(attrs, "error", err)
			slog.Error("gateway ip key sharing detected but auto add failed", attrs...)
			c.Next()
			return
		}
		slog.Warn("gateway ip key sharing detected, ip auto added to notice policy", attrs...)
		c.Next()
	}
}
