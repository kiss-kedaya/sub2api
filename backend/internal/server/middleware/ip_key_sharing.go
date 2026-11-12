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
	// gateway_ip_sharing:throttle:<ip> bounds settings writes per IP to one per
	// write throttle period. Without it a busy IP would rewrite the settings row
	// on every gateway request.
	//
	// It replaces the older gateway_ip_sharing:reported:<ip> "one report per
	// window" claim. That key made the stored entry a snapshot taken the first
	// time the threshold was crossed, so a later, larger burst was invisible.
	// Leftover reported:* keys from a rolling deploy simply expire with their
	// window and are never read again.
	ipKeySharingThrottlePrefix = "gateway_ip_sharing:throttle:"
	// gateway_ip_sharing:keys:<ip> is the set of key IDs this IP has been seen
	// calling with. Its TTL is the policy's total-keys retention (24h by
	// default) and is deliberately decoupled from the five-minute decision
	// window: the window answers "does this IP look shared right now", the set
	// answers "how many distinct keys has it used overall".
	ipKeySharingKeysPrefix = "gateway_ip_sharing:keys:"

	ipKeySharingOpsTimeout = 2 * time.Second
	ipKeySharingKeyPrefix  = 8
	// ipKeySharingWriteThrottle is how often one IP may update its stored
	// evidence. Thirty seconds keeps a hot IP from rewriting the settings row on
	// every request while still catching a burst that keeps growing.
	ipKeySharingWriteThrottle = 30 * time.Second
)

// ipSharingObserveScript records one observation and returns both counters in a
// single round trip, so the gateway hot path still costs exactly one Redis call.
//
// It records the member only when it has not been written inside the current
// window (ZSCORE < cutoff), then counts the members still inside the window.
// Counting by score instead of ZCARD keeps stale members harmless: they expire
// with the key at 2*window and are filtered out in the meantime.
//
// The distinct-key set is additive and gets its TTL once, on creation. The TTL
// is therefore a fixed retention window from the first key seen, not a sliding
// one, so "distinct keys in the last 24h" cannot be extended forever by
// activity.
//
// KEYS[1] = users zset, KEYS[2] = evidence hash, KEYS[3] = distinct keys set
// ARGV[1] = member (user ID), ARGV[2] = cutoff millis, ARGV[3] = now millis,
// ARGV[4] = window key TTL seconds, ARGV[5] = evidence value,
// ARGV[6] = key member (key ID), ARGV[7] = distinct-key retention seconds
var ipSharingObserveScript = redis.NewScript(`
local score = redis.call('ZSCORE', KEYS[1], ARGV[1])
if not score or tonumber(score) < tonumber(ARGV[2]) then
  redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', ARGV[2])
  redis.call('ZADD', KEYS[1], ARGV[3], ARGV[1])
  redis.call('EXPIRE', KEYS[1], ARGV[4])
  redis.call('HSET', KEYS[2], ARGV[1], ARGV[5])
  redis.call('EXPIRE', KEYS[2], ARGV[4])
end
redis.call('SADD', KEYS[3], ARGV[6])
if redis.call('TTL', KEYS[3]) < 0 then
  redis.call('EXPIRE', KEYS[3], ARGV[7])
end
return {redis.call('ZCOUNT', KEYS[1], ARGV[2], '+inf'), redis.call('SCARD', KEYS[3])}
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
	// PeakUsers is the distinct-user count of THIS observation. The store keeps
	// the larger of this and what it already has inside the same burst.
	PeakUsers int
	// TotalKeys is the number of distinct keys this IP has called with inside
	// the total-keys retention period, independent of the window above.
	TotalKeys int
	// LastSeenAt is the moment of this observation.
	LastSeenAt time.Time
}

// AutoEntry converts a hit into the persisted policy entry.
func (h IPKeySharingHit) AutoEntry() ipnotice.AutoEntry {
	lastSeen := h.LastSeenAt
	if lastSeen.IsZero() {
		lastSeen = h.FirstSeenAt
	}
	return ipnotice.AutoEntry{
		IP:             h.IP,
		Source:         ipnotice.AutoEntrySourceAuto,
		FirstSeenAt:    h.FirstSeenAt,
		DistinctUsers:  h.DistinctUsers,
		WindowMinutes:  h.WindowMinutes,
		KeyPrefixes:    append([]string(nil), h.KeyPrefixes...),
		FirstSeenUsers: h.DistinctUsers,
		PeakUsers:      h.PeakUsers,
		TotalKeys:      h.TotalKeys,
		LastSeenAt:     lastSeen,
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
// apiKey. It returns a hit whenever at least SharingMinUsers distinct accounts
// have been seen inside the window, throttled to at most one hit per IP per
// write throttle period so a growing burst can keep moving the peak, the
// distinct-key total and last-seen forward without rewriting settings on every
// request.
//
// The returned ok=false cases are all intentional:
//   - detection disabled or no Redis;
//   - the source address is a Cloudflare edge range or a private/loopback
//     address, neither of which identifies a real client;
//   - the IP is on the hand-written list (the operator owns that entry; the
//     gateway never rewrites, re-logs or disables on top of it);
//   - the key is younger than the configured minimum age, so it cannot have
//     been shared yet and must not make the IP look shared by itself;
//   - the distinct-user count is still below the threshold;
//   - another request already wrote this IP's evidence inside the throttle
//     period.
func (d *IPKeySharingDetector) Observe(ctx context.Context, ip string, userID int64, apiKey *service.APIKey) (IPKeySharingHit, bool) {
	if d == nil || d.redis == nil || d.settings == nil || userID <= 0 || apiKey == nil {
		return IPKeySharingHit{}, false
	}
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return IPKeySharingHit{}, false
	}
	policy := d.settings.GatewayIPNoticeSnapshot()
	if !policy.AutoAdd() || policy.HasManualIP(ip) {
		return IPKeySharingHit{}, false
	}

	// Cloudflare edge ranges and private/loopback networks are excluded before
	// any Redis write: a request that arrives from Cloudflare's shared egress
	// says nothing about the real client, and an internal proxy would otherwise
	// look like one IP serving many accounts. Excluding them here means they
	// never count, are never added and are never disabled.
	if reason, excluded := ipnotice.ExcludedSourceIP(ip); excluded {
		slog.Debug("gateway IP sharing skipped: source address excluded",
			"ip", ip, "reason", reason, "user_id", userID)
		return IPKeySharingHit{}, false
	}

	now := d.now()
	// Key age gate: a credential issued moments ago has no sharing history to
	// detect. Skipping it here (before any Redis write, and therefore before the
	// distinct-key set) keeps a freshly created key from padding either counter.
	// The log names the key and its age so an operator can see why a candidate
	// was not counted; one request carries exactly one key, so the number of
	// keys excluded by this rule for a request is always one.
	minAgeDays := policy.SharingKeyMinAgeDays()
	minAge := time.Duration(minAgeDays) * 24 * time.Hour
	if !apiKey.CreatedAt.IsZero() {
		if age := now.Sub(apiKey.CreatedAt); age < minAge {
			slog.Debug("gateway IP sharing skipped: key is younger than the minimum age",
				"ip", ip, "user_id", userID, "key_id", apiKey.ID,
				"key_prefix", maskAPIKeyPrefix(apiKey.Key),
				"age_days", int(age.Hours()/24), "min_age_days", minAgeDays,
				"keys_excluded", 1)
			return IPKeySharingHit{}, false
		}
	}

	ctx, cancel := context.WithTimeout(ctx, ipKeySharingOpsTimeout)
	defer cancel()

	window := policy.SharingWindow()
	threshold := policy.SharingMinUsers()
	cutoff := strconv.FormatInt(now.Add(-window).UnixMilli(), 10)
	userKey := ipKeySharingUsersPrefix + ip
	evidenceKey := ipKeySharingEvidencePrefix + ip
	keysKey := ipKeySharingKeysPrefix + ip
	member := strconv.FormatInt(userID, 10)

	// The evidence value carries the key ID in front of the masked prefix:
	// "<keyID>:<prefix>". Older rolling-deploy entries hold a bare prefix, so
	// the parser below treats a missing colon as "prefix only".
	evidence := maskAPIKeyPrefix(apiKey.Key)
	if apiKey.ID > 0 {
		evidence = strconv.FormatInt(apiKey.ID, 10) + ":" + evidence
	}
	keyMember := strconv.FormatInt(apiKey.ID, 10)
	if apiKey.ID <= 0 {
		// A key without an ID cannot be counted as distinct; use its masked
		// prefix so the total still moves and never collides across keys.
		keyMember = "p:" + evidence
	}

	// Hot path: one round trip, three key operations in one script. The
	// sorted-set score is the user's last write, so ZSCORE is the "has this
	// (ip,user) already been recorded in this window" check: a repeat request
	// only counts, it never rewrites. The distinct-key set is additive over its
	// own, much longer TTL.
	ttlSeconds := int64(2 * window / time.Second)
	if ttlSeconds < 1 {
		ttlSeconds = 1
	}
	totalKeysTTLSeconds := int64(policy.TotalKeysTTL() / time.Second)
	if totalKeysTTLSeconds < 1 {
		totalKeysTTLSeconds = 1
	}
	counters, err := ipSharingObserveScript.Run(ctx, d.redis, []string{userKey, evidenceKey, keysKey},
		member, cutoff, now.UnixMilli(), ttlSeconds, evidence, keyMember, totalKeysTTLSeconds,
	).Int64Slice()
	if err != nil || len(counters) != 2 {
		slog.Debug("gateway IP sharing counter unavailable", "ip", ip, "error", err)
		return IPKeySharingHit{}, false
	}
	distinct, totalKeys := int(counters[0]), int(counters[1])
	if distinct < threshold {
		return IPKeySharingHit{}, false
	}
	// A home or office NAT can legitimately carry several users, so the count alone
	// must not stop their keys. Only a hosting/proxy address acts; the classifier is
	// fail-open, so an unknown or unreachable answer means 'do not act'.
	if !isDatacenterSourceIP(ctx, ip) {
		return IPKeySharingHit{}, false
	}

	// Write throttle: concurrent replicas and the rest of a burst collapse into
	// one settings write per period, while a burst that keeps growing still gets
	// its peak recorded on the next period.
	claimed, err := d.redis.SetNX(ctx, ipKeySharingThrottlePrefix+ip, "1", ipKeySharingWriteThrottle).Result()
	if err != nil || !claimed {
		return IPKeySharingHit{}, false
	}

	hit := IPKeySharingHit{
		IP:            ip,
		DistinctUsers: distinct,
		WindowMinutes: int(window / time.Minute),
		FirstSeenAt:   now,
		PeakUsers:     distinct,
		TotalKeys:     totalKeys,
		LastSeenAt:    now,
	}
	if users, err := d.redis.ZRangeArgs(ctx, redis.ZRangeArgs{Key: userKey, Start: cutoff, Stop: "+inf", ByScore: true}).Result(); err == nil && len(users) > 0 {
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
