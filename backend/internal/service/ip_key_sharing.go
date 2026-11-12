package service

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ipnotice"
	"github.com/redis/go-redis/v9"
)

// Redis key layout for distinct-user detection. Everything is per source IP and
// expires on its own, so a burst of scraping cannot leave permanent state
// behind even when nobody reads the settings table.
const (
	// gateway_ip_sharing:users:<ip> is a sorted set of DISTINCT user IDs whose
	// most recent authenticated gateway call happened inside the window. The
	// member is the user ID — never the key — so two keys of one account count
	// as one user.
	ipKeySharingUsersPrefix = "gateway_ip_sharing:users:"
	// gateway_ip_sharing:evidence:<ip> maps user ID -> masked key prefix. It is
	// only read when the threshold is crossed and is what the admin sees as
	// "why was this IP added".
	ipKeySharingEvidencePrefix = "gateway_ip_sharing:evidence:"
	// gateway_ip_sharing:reported:<ip> throttles settings writes/log spam to one
	// report per window per IP.
	ipKeySharingReportedPrefix = "gateway_ip_sharing:reported:"

	ipKeySharingOpsTimeout = 2 * time.Second
	ipKeySharingKeyPrefix  = 8
)

// IPKeySharingHit is the evidence bundle for one detected IP.
type IPKeySharingHit struct {
	IP            string
	DistinctUsers int
	WindowMinutes int
	KeyPrefixes   []string
	FirstSeenAt   time.Time
}

// AutoEntry converts a hit into the persisted policy entry.
func (h IPKeySharingHit) AutoEntry() ipnotice.AutoEntry {
	return ipnotice.AutoEntry{
		IP:            h.IP,
		Source:        ipnotice.AutoEntrySourceAuto,
		FirstSeenAt:   h.FirstSeenAt,
		DistinctUsers: h.DistinctUsers,
		WindowMinutes: h.WindowMinutes,
		KeyPrefixes:   append([]string(nil), h.KeyPrefixes...),
	}
}

// IPKeySharingDetector counts distinct user accounts per source IP in a sliding
// window and reports the moment the configured threshold is crossed.
//
// It only observes: it never blocks a request, never touches users, keys or
// accounts, and any Redis failure degrades to "not detected".
type IPKeySharingDetector struct {
	settings *SettingService
	redis    *redis.Client
	now      func() time.Time
}

// NewIPKeySharingDetector builds the detector used by the gateway routes. A nil
// Redis client disables it (single-node deployments without Redis simply keep
// the manual list).
func NewIPKeySharingDetector(settings *SettingService, redisClient *redis.Client) *IPKeySharingDetector {
	return &IPKeySharingDetector{settings: settings, redis: redisClient, now: time.Now}
}

// Observe records that userID (an authenticated account) called the gateway
// from ip. It returns a hit exactly once per window per IP, when at least
// SharingMinUsers distinct accounts have been seen.
//
// The returned ok=false cases are all intentional:
//   - detection disabled or no Redis;
//   - the IP is already listed (idempotent, no Redis work);
//   - the distinct-user count is still below the threshold;
//   - another request already reported this window.
func (d *IPKeySharingDetector) Observe(ctx context.Context, ip string, userID int64, apiKey string) (IPKeySharingHit, bool) {
	if d == nil || d.redis == nil || d.settings == nil || userID <= 0 {
		return IPKeySharingHit{}, false
	}
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return IPKeySharingHit{}, false
	}
	policy := d.settings.GatewayIPNoticeSnapshot()
	if !policy.AutoAdd() || policy.HasListedIP(ip) {
		return IPKeySharingHit{}, false
	}

	ctx, cancel := context.WithTimeout(ctx, ipKeySharingOpsTimeout)
	defer cancel()

	window := policy.SharingWindow()
	threshold := policy.SharingMinUsers()
	now := d.now()
	cutoff := strconv.FormatInt(now.Add(-window).UnixMilli(), 10)
	userKey := ipKeySharingUsersPrefix + ip
	evidenceKey := ipKeySharingEvidencePrefix + ip
	member := strconv.FormatInt(userID, 10)

	pipe := d.redis.Pipeline()
	pipe.ZRemRangeByScore(ctx, userKey, "-inf", cutoff)
	pipe.ZAdd(ctx, userKey, redis.Z{Score: float64(now.UnixMilli()), Member: member})
	pipe.Expire(ctx, userKey, 2*window)
	pipe.HSet(ctx, evidenceKey, member, maskAPIKeyPrefix(apiKey))
	pipe.Expire(ctx, evidenceKey, 2*window)
	distinctCmd := pipe.ZCard(ctx, userKey)
	if _, err := pipe.Exec(ctx); err != nil {
		slog.Debug("gateway IP sharing counter unavailable", "ip", ip, "error", err)
		return IPKeySharingHit{}, false
	}
	distinct := int(distinctCmd.Val())
	if distinct < threshold {
		return IPKeySharingHit{}, false
	}

	// One report per IP per window: concurrent replicas and the rest of the
	// burst collapse into a single settings write.
	claimed, err := d.redis.SetNX(ctx, ipKeySharingReportedPrefix+ip, "1", window).Result()
	if err != nil || !claimed {
		return IPKeySharingHit{}, false
	}

	hit := IPKeySharingHit{
		IP:            ip,
		DistinctUsers: distinct,
		WindowMinutes: int(window / time.Minute),
		FirstSeenAt:   now,
	}
	if users, err := d.redis.ZRange(ctx, userKey, 0, -1).Result(); err == nil && len(users) > 0 {
		if values, err := d.redis.HMGet(ctx, evidenceKey, users...).Result(); err == nil {
			for _, value := range values {
				if prefix, ok := value.(string); ok && prefix != "" {
					hit.KeyPrefixes = append(hit.KeyPrefixes, prefix)
				}
			}
		}
	}
	if len(hit.KeyPrefixes) == 0 && apiKey != "" {
		hit.KeyPrefixes = []string{maskAPIKeyPrefix(apiKey)}
	}
	return hit, true
}

// maskAPIKeyPrefix keeps only the leading characters of a key. The full key is
// never logged or persisted in policy evidence.
func maskAPIKeyPrefix(key string) string {
	key = strings.TrimSpace(key)
	if len(key) > ipKeySharingKeyPrefix {
		key = key[:ipKeySharingKeyPrefix]
	}
	return key
}
