package iprisk

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const anonymousManagerKey = "sub2api.anonymous_api_guard"
const anonymousPrefix = redisPrefix + "anonymous:"

// AnonymousGuard is bound before any authentication executes. It never applies
// a pre-authentication IP gate: only explicit credential failure paths call it.
func AnonymousGuard(c *gin.Context) *Manager {
	value, _ := c.Get(anonymousManagerKey)
	m, _ := value.(*Manager)
	return m
}

// Exclude non-routable identities so proxy/private infrastructure cannot be
// merged into an automatically punished public client. Documentation ranges are
// deliberately excluded too; tests use synthetic public addresses without IO.
var anonymousReserved = func() []netip.Prefix {
	var result []netip.Prefix
	for _, raw := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001::/23", "2001:db8::/32", "2002::/16", "3fff::/20"} {
		result = append(result, netip.MustParsePrefix(raw))
	}
	return result
}()

func AnonymousNetwork(raw string) string {
	ip, err := netip.ParseAddr(strings.TrimSpace(raw))
	if err != nil || ip.Zone() != "" {
		return ""
	}
	ip = ip.Unmap()
	if !publicIP(ip) {
		return ""
	}
	// IPv6 global unicast allocation; exclude unspecified/reserved/mapped ranges.
	if ip.Is6() && !netip.MustParsePrefix("2000::/3").Contains(ip) {
		return ""
	}
	for _, p := range anonymousReserved {
		if p.Contains(ip) {
			return ""
		}
	}
	return netip.PrefixFrom(ip, ip.BitLen()).String()
}

// AnonymousEnabled lets the shared policy own punishment instead of stacking
// an inconsistent local IP ban on top of Redis or bypassing the whitelist.
func (m *Manager) AnonymousEnabled(ctx context.Context) bool {
	if m == nil || m.emergencyDisabled {
		return false
	}
	p, _ := m.snapshot(ctx)
	return p.config.AnonymousAPI.Enabled
}

func (m *Manager) anonymousPolicy(ctx context.Context, raw string) (*policy, string) {
	if m == nil || m.emergencyDisabled {
		return nil, ""
	}
	p, _ := m.snapshot(ctx)
	if !p.config.AnonymousAPI.Enabled {
		return nil, ""
	}
	network := AnonymousNetwork(raw)
	if network == "" {
		return nil, ""
	}
	ip := netip.MustParsePrefix(network).Addr()
	if p.allowed(ip) {
		return nil, ""
	}
	return p, network
}

// Redis TIME keeps the sliding window and ban expiry consistent across nodes.
// Registry admission, exact-window eviction, threshold and ban creation are one
// transaction. Both indexes and every per-IP key are bounded and expire.
var anonymousFailureScript = redis.NewScript(`
local time = redis.call('TIME')
local now = tonumber(time[1])*1000 + math.floor(tonumber(time[2])/1000)
local active = redis.call('GET',KEYS[3])
if active then return {active,redis.call('PTTL',KEYS[3])} end
redis.call('ZREMRANGEBYSCORE',KEYS[1],'-inf',now)
if not redis.call('ZSCORE',KEYS[1],ARGV[1]) and redis.call('ZCARD',KEYS[1]) >= tonumber(ARGV[5]) then return {'',0} end
local window = tonumber(ARGV[2])*1000
redis.call('ZREMRANGEBYSCORE',KEYS[2],'-inf',now-window)
redis.call('ZADD',KEYS[2],now,ARGV[6])
redis.call('PEXPIRE',KEYS[2],window)
redis.call('ZADD',KEYS[1],now+window,ARGV[1])
redis.call('EXPIRE',KEYS[1],90000)
if redis.call('ZCARD',KEYS[2]) < tonumber(ARGV[3]) then return {'',0} end
local expires = math.floor(now/1000)+tonumber(ARGV[4])
local ban = cjson.encode({generation=ARGV[6],network=ARGV[1],reason='anonymous_api_'..ARGV[7],created_at=math.floor(now/1000),expires_at=expires,observe_only=false})
redis.call('SET',KEYS[3],ban,'EX',ARGV[4])
redis.call('DEL',KEYS[2])
redis.call('ZADD',KEYS[1],now+tonumber(ARGV[4])*1000,ARGV[1])
redis.call('ZREMRANGEBYSCORE',KEYS[4],'-inf',now)
redis.call('ZADD',KEYS[4],now+tonumber(ARGV[4])*1000,ARGV[1])
redis.call('EXPIRE',KEYS[4],90000)
return {ban,tonumber(ARGV[4])*1000}
`)

func anonymousKeys(network string) []string {
	hash := hashNetwork(network)
	return []string{anonymousPrefix + "tracked", anonymousPrefix + hash + ":failures", anonymousPrefix + hash + ":ban", anonymousPrefix + "bans"}
}

// AnonymousFailure accepts a fixed category, never request/error text. An error
// means fail-open for this protection only; authentication must still reject.
func (m *Manager) AnonymousFailure(ctx context.Context, raw, reason string) (*Ban, error) {
	switch reason {
	case "missing", "malformed", "unknown", "disabled":
	default:
		return nil, nil
	}
	p, network := m.anonymousPolicy(ctx, raw)
	if p == nil {
		return nil, nil
	}
	a := p.config.AnonymousAPI
	var result []any
	err := m.riskRedis(ctx, func(budget context.Context) error {
		var e error
		result, e = anonymousFailureScript.Run(budget, m.redis, anonymousKeys(network), network, a.WindowSeconds, a.Threshold, a.BanSeconds, a.MaxTrackedIPs, uuid.NewString(), reason).Slice()
		return e
	})
	if err != nil {
		return nil, err
	}
	if len(result) != 2 {
		return nil, fmt.Errorf("invalid anonymous guard result")
	}
	value, _ := result[0].(string)
	if value == "" {
		return nil, nil
	}
	var ban Ban
	if err := json.Unmarshal([]byte(value), &ban); err != nil {
		return nil, err
	}
	if milliseconds, ok := result[1].(int64); ok {
		ban.RetryAfter = time.Duration(milliseconds) * time.Millisecond
	}
	return &ban, nil
}

var anonymousUnbanScript = redis.NewScript(`
redis.call('DEL',KEYS[2],KEYS[3])
redis.call('ZREM',KEYS[1],ARGV[1])
redis.call('ZREM',KEYS[4],ARGV[1])
return 1
`)

// UnbanAnonymous clears only an exact IP's automatic state. Manual deny rules,
// login bans and other policy state are deliberately outside this transaction.
func (m *Manager) UnbanAnonymous(ctx context.Context, raw string) error {
	prefix, err := ParseNetwork(raw)
	if err != nil || prefix.Bits() != prefix.Addr().BitLen() {
		return fmt.Errorf("an exact IP address is required")
	}
	network := prefix.String()
	return m.riskRedis(ctx, func(budget context.Context) error {
		return anonymousUnbanScript.Run(budget, m.redis, anonymousKeys(network), network).Err()
	})
}

func (m *Manager) AnonymousSuccess(ctx context.Context, raw string) error {
	p, network := m.anonymousPolicy(ctx, raw)
	if p == nil || !p.config.AnonymousAPI.ResetOnSuccess {
		return nil
	}
	return m.UnbanAnonymous(ctx, network)
}

// Return at most 500 bans for the management UI without scanning Redis keys.
var anonymousBansScript = redis.NewScript(`
local time=redis.call('TIME')
local now=tonumber(time[1])*1000+math.floor(tonumber(time[2])/1000)
redis.call('ZREMRANGEBYSCORE',KEYS[1],'-inf',now)
return redis.call('ZREVRANGE',KEYS[1],0,499)
`)

func (m *Manager) AnonymousBans(ctx context.Context) ([]Ban, error) {
	bans := []Ban{}
	err := m.riskRedis(ctx, func(budget context.Context) error {
		networks, e := anonymousBansScript.Run(budget, m.redis, []string{anonymousPrefix + "bans"}).StringSlice()
		if e != nil {
			return e
		}
		if len(networks) == 0 {
			return nil
		}
		keys := make([]string, len(networks))
		for i, n := range networks {
			keys[i] = anonymousKeys(n)[2]
		}
		values, e := m.redis.MGet(budget, keys...).Result()
		if e != nil {
			return e
		}
		for _, value := range values {
			if raw, ok := value.(string); ok {
				var ban Ban
				if json.Unmarshal([]byte(raw), &ban) == nil {
					bans = append(bans, ban)
				}
			}
		}
		return nil
	})
	return bans, err
}
