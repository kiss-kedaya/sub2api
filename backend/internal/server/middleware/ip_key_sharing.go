package middleware

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ipnotice"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ipquality"
	"github.com/Wei-Shaw/sub2api/internal/service"
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

// ipSharingWindowScript is the whole detection write path in ONE round trip.
// It records the member only when it has not been written inside the current
// window (ZSCORE < cutoff), then counts the members whose score is still inside
// the window. Counting by score instead of ZCARD keeps stale members harmless:
// they expire with the key at 2*window and are filtered out in the meantime.
//
// KEYS[1] = users zset, KEYS[2] = evidence hash
// ARGV[1] = member (user ID), ARGV[2] = cutoff millis, ARGV[3] = now millis,
// ARGV[4] = key TTL seconds, ARGV[5] = evidence value
var ipSharingWindowScript = redis.NewScript(`
local score = redis.call('ZSCORE', KEYS[1], ARGV[1])
if not score or tonumber(score) < tonumber(ARGV[2]) then
  redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', ARGV[2])
  redis.call('ZADD', KEYS[1], ARGV[3], ARGV[1])
  redis.call('EXPIRE', KEYS[1], ARGV[4])
  redis.call('HSET', KEYS[2], ARGV[1], ARGV[5])
  redis.call('EXPIRE', KEYS[2], ARGV[4])
end
return redis.call('ZCOUNT', KEYS[1], ARGV[2], '+inf')
`)

// IPKeySharingHit is the evidence bundle for one detected IP.
type IPKeySharingHit struct {
	IP            string
	DistinctUsers int
	WindowMinutes int
	KeyPrefixes   []string
	// KeyIDs are the keys observed from this IP inside the window. They are the
	// keys the gateway disables; KeyPrefixes is the masked, persistable copy.
	KeyIDs      []int64
	FirstSeenAt time.Time
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
	settings *service.SettingService
	redis    *redis.Client
	now      func() time.Time
}

// NewIPKeySharingDetector builds the detector used by the gateway routes. A nil
// Redis client disables it (single-node deployments without Redis simply keep
// the manual list).
func NewIPKeySharingDetector(settings *service.SettingService, redisClient *redis.Client) *IPKeySharingDetector {
	return &IPKeySharingDetector{settings: settings, redis: redisClient, now: time.Now}
}

// Observe records that an authenticated account called the gateway from ip with
// apiKey. It returns a hit exactly once per window per IP, when at least
// SharingMinUsers distinct accounts have been seen.
//
// The returned ok=false cases are all intentional:
//   - detection disabled or no Redis;
//   - the IP is already listed (idempotent, no Redis work);
//   - the key is younger than the configured minimum age, so it cannot have
//     been shared yet and must not make the IP look shared by itself;
//   - the distinct-user count is still below the threshold;
//   - another request already reported this window.
func (d *IPKeySharingDetector) Observe(ctx context.Context, ip string, userID int64, apiKey *service.APIKey) (IPKeySharingHit, bool) {
	if d == nil || d.redis == nil || d.settings == nil || userID <= 0 || apiKey == nil {
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

	now := d.now()
	// Key age gate: a credential issued moments ago has no sharing history to
	// detect. Skipping it here (before any Redis write) keeps a freshly created
	// key from padding the distinct-user count of an otherwise single-user IP.
	minAge := time.Duration(policy.SharingKeyMinAgeDays()) * 24 * time.Hour
	if !apiKey.CreatedAt.IsZero() && now.Sub(apiKey.CreatedAt) < minAge {
		return IPKeySharingHit{}, false
	}

	ctx, cancel := context.WithTimeout(ctx, ipKeySharingOpsTimeout)
	defer cancel()

	window := policy.SharingWindow()
	threshold := policy.SharingMinUsers()
	cutoff := strconv.FormatInt(now.Add(-window).UnixMilli(), 10)
	userKey := ipKeySharingUsersPrefix + ip
	evidenceKey := ipKeySharingEvidencePrefix + ip
	member := strconv.FormatInt(userID, 10)

	// The evidence value carries the key ID in front of the masked prefix:
	// "<keyID>:<prefix>". Older rolling-deploy entries hold a bare prefix, so
	// the parser below treats a missing colon as "prefix only".
	evidence := maskAPIKeyPrefix(apiKey.Key)
	if apiKey.ID > 0 {
		evidence = strconv.FormatInt(apiKey.ID, 10) + ":" + evidence
	}

	// Hot path: one round trip, two commands. The sorted-set score is the user's
	// last write, so ZSCORE is the "has this (ip,user) already been recorded in
	// this window" check: a repeat request only counts, it never writes. That
	// keeps the write volume proportional to distinct users per window instead
	// of gateway requests, without adding a second key per (ip,user).
	ttlSeconds := int64(2 * window / time.Second)
	if ttlSeconds < 1 {
		ttlSeconds = 1
	}
	distinct, err := ipSharingWindowScript.Run(ctx, d.redis, []string{userKey, evidenceKey},
		member, cutoff, now.UnixMilli(), ttlSeconds, evidence,
	).Int()
	if err != nil {
		slog.Debug("gateway IP sharing counter unavailable", "ip", ip, "error", err)
		return IPKeySharingHit{}, false
	}
	if distinct < threshold {
		return IPKeySharingHit{}, false
	}
	// A home or office NAT can legitimately carry several users, so the count alone
	// must not stop their keys. Only a hosting/proxy address acts; the classifier is
	// fail-open, so an unknown or unreachable answer means 'do not act'.
	if !isDatacenterSourceIP(ctx, ip) {
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
	if users, err := d.redis.ZRangeByScore(ctx, userKey, &redis.ZRangeBy{Min: cutoff, Max: "+inf"}).Result(); err == nil && len(users) > 0 {
		if values, err := d.redis.HMGet(ctx, evidenceKey, users...).Result(); err == nil {
			for _, value := range values {
				raw, ok := value.(string)
				if !ok || raw == "" {
					continue
				}
				if idPart, prefix, found := strings.Cut(raw, ":"); found {
					if id, err := strconv.ParseInt(idPart, 10, 64); err == nil && id > 0 {
						hit.KeyIDs = append(hit.KeyIDs, id)
					}
					raw = prefix
				}
				if raw != "" {
					hit.KeyPrefixes = append(hit.KeyPrefixes, raw)
				}
			}
		}
	}
	if len(hit.KeyIDs) == 0 && apiKey.ID > 0 {
		hit.KeyIDs = []int64{apiKey.ID}
	}
	if len(hit.KeyPrefixes) == 0 && apiKey.Key != "" {
		hit.KeyPrefixes = []string{maskAPIKeyPrefix(apiKey.Key)}
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

// isDatacenterSourceIP reports whether the source address classifies as hosting or
// proxy infrastructure. It is deliberately fail-open: an error, timeout or empty
// answer returns false so that shared residential egress is never punished.
func isDatacenterSourceIP(ctx context.Context, ip string) bool {
	if datacenterClassifier != nil {
		return datacenterClassifier(ctx, ip)
	}
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return ipquality.ClassifyDatacenterIPs(cctx, []string{ip})[ip]
}

// datacenterClassifier is replaced in tests so the gate can be exercised without
// calling the external classifier.
var datacenterClassifier func(context.Context, string) bool
