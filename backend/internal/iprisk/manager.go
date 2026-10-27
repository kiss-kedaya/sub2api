package iprisk

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const redisPrefix = "ip_risk:{ip-risk}:"
const maxEvents = 2000

type Store interface {
	Load(context.Context) (Config, error)
	Save(context.Context, Config) error
}

type Manager struct {
	store             Store
	redis             *redis.Client
	geo               CountryResolver
	emergencyDisabled bool
	saveMu            sync.Mutex
	mu                sync.Mutex
	refreshing        bool
	revision          uint64
	cached            *policy
	expires           time.Time
	lastLoadError     string
	redisRetryAfter   time.Time
	ctx               context.Context
	cancel            context.CancelFunc
	startOnce         sync.Once
	done              chan struct{}
}

func New(store Store, client *redis.Client, geo CountryResolver, emergencyDisabled bool) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{ctx: ctx, cancel: cancel, done: make(chan struct{}), store: store, redis: client, geo: geo, emergencyDisabled: emergencyDisabled,
		cached: compile(DefaultConfig())}
	// One bounded startup read; request handling never waits for configuration IO.
	m.refresh(m.ctx, 0)
	return m
}

// Start refreshes every node even when it receives no requests.
func (m *Manager) Start() { m.startPolling(5 * time.Second) }

func (m *Manager) startPolling(interval time.Duration) {
	m.startOnce.Do(func() {
		go func() {
			defer close(m.done)
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-m.ctx.Done():
					return
				case <-ticker.C:
					m.mu.Lock()
					if m.refreshing {
						m.mu.Unlock()
						continue
					}
					m.refreshing = true
					revision := m.revision
					m.mu.Unlock()
					m.refresh(m.ctx, revision)
				}
			}
		}()
	})
}

func (m *Manager) Stop() {
	m.cancel()
	m.Start() // Also makes Stop safe when polling has not yet started.
	<-m.done
}

func (m *Manager) refresh(ctx context.Context, revision uint64) {
	loadCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	c, err := m.store.Load(loadCtx)
	if err == nil {
		c, err = NormalizeConfig(c)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refreshing = false
	// A save that finished while this read was in flight always wins.
	if revision != m.revision {
		return
	}
	m.expires = time.Now().Add(5 * time.Second)
	if err != nil {
		m.lastLoadError = "configuration_refresh_failed"
		return // Retain the last valid policy, or the disabled startup default.
	}
	m.lastLoadError = ""
	m.cached = compile(c)
}

func (m *Manager) snapshot(_ context.Context) (*policy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ctx.Err() == nil && !m.refreshing && !time.Now().Before(m.expires) {
		m.refreshing = true
		go m.refresh(m.ctx, m.revision)
	}
	return m.cached, nil
}

func (m *Manager) Config(ctx context.Context) (Config, error) {
	p, err := m.snapshot(ctx)
	if err != nil {
		return Config{}, err
	}
	// Normalize creates independent slices; callers cannot mutate the active policy.
	return NormalizeConfig(p.config)
}

func (m *Manager) Save(ctx context.Context, c Config) error {
	normalized, err := NormalizeConfig(c)
	if err != nil {
		return err
	}
	if (normalized.Enabled || normalized.AnonymousAPI.Enabled) && !m.emergencyDisabled {
		if m.redis == nil {
			return fmt.Errorf("redis is required for IP risk control")
		}
		if err := m.redis.Ping(ctx).Err(); err != nil {
			return fmt.Errorf("redis is unavailable")
		}
	}
	m.saveMu.Lock()
	defer m.saveMu.Unlock()
	if err := m.store.Save(ctx, normalized); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.revision++
	m.cached = compile(normalized)
	m.lastLoadError = ""
	m.expires = time.Now().Add(5 * time.Second)
	return nil
}

// CheckAdministrator prevents direct self-lockout without granting admins a runtime bypass.
func (m *Manager) CheckAdministrator(ctx context.Context, c Config, rawIP, ua string) error {
	if !c.Enabled || c.ObserveOnly || m.emergencyDisabled {
		return nil
	}
	ip, err := netip.ParseAddr(rawIP)
	if err != nil {
		return fmt.Errorf("cannot verify administrator IP; configure trusted proxies first")
	}
	ip = ip.Unmap()
	p := compile(c)
	if p.allowed(ip) {
		return nil
	}
	if uaDenied(c, ua) != "" {
		return fmt.Errorf("UA policy would block your next login; whitelist your current IP or adjust UA rules")
	}
	if p.denied(ip, time.Now()) {
		return fmt.Errorf("blacklist would block your current IP; add it to the whitelist first")
	}
	if hasCountryRules(c) {
		country := m.country(ctx, ip)
		if countryDenied(c, ip, country) {
			return fmt.Errorf("country policy would block your current IP; add it to the whitelist first")
		}
	}
	// An existing automatic ban is possible when switching from observation to enforcement.
	ban, err := m.activeBan(ctx, riskNetwork(ip, c.IPv6Prefix), false)
	if err != nil {
		return fmt.Errorf("cannot verify current automatic ban")
	}
	if ban != nil {
		return fmt.Errorf("your current network is banned; whitelist it before enabling enforcement")
	}
	return nil
}

func riskNetwork(ip netip.Addr, bits int) string {
	ip = ip.Unmap()
	if ip.Is6() {
		return netip.PrefixFrom(ip, bits).Masked().String()
	}
	return netip.PrefixFrom(ip, 32).String()
}

func hashNetwork(network string) string {
	sum := sha256.Sum256([]byte(network))
	return hex.EncodeToString(sum[:])
}

func modePrefix(observe bool) string {
	if observe {
		return redisPrefix + "observe:"
	}
	return redisPrefix + "enforce:"
}

type Ban struct {
	RetryAfter  time.Duration `json:"-"`
	Network     string        `json:"network"`
	Reason      string        `json:"reason"`
	CreatedAt   int64         `json:"created_at"`
	ExpiresAt   int64         `json:"expires_at"`
	ObserveOnly bool          `json:"observe_only"`
}

// One fixed expiry shared by all nodes. A blocked request never prolongs the ban.
var counterScript = redis.NewScript(`
local active = redis.call('GET', KEYS[2])
if active then return active end
local count = redis.call('INCR', KEYS[1])
if count == 1 or redis.call('PTTL', KEYS[1]) == -1 then
  redis.call('EXPIRE', KEYS[1], ARGV[1])
end
if count < tonumber(ARGV[2]) then return '' end
redis.call('SET', KEYS[2], ARGV[3], 'EX', ARGV[4])
redis.call('DEL', KEYS[1])
redis.call('ZREMRANGEBYSCORE', KEYS[3], '-inf', ARGV[5])
redis.call('ZADD', KEYS[3], ARGV[6], ARGV[7])
redis.call('EXPIRE', KEYS[3], 604800)
return ARGV[3]
`)

// Bound dynamic checks and briefly back off after failures so a cache outage
// cannot stall all business traffic. Static policies are checked separately.
func (m *Manager) riskRedis(ctx context.Context, run func(context.Context) error) error {
	m.mu.Lock()
	backoff := time.Now().Before(m.redisRetryAfter)
	m.mu.Unlock()
	if m.redis == nil || backoff {
		return errors.New("risk Redis unavailable")
	}
	budget, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel()
	err := run(budget)
	if err != nil && !errors.Is(err, redis.Nil) && ctx.Err() == nil {
		m.mu.Lock()
		m.redisRetryAfter = time.Now().Add(time.Second)
		m.mu.Unlock()
	}
	return err
}

func (m *Manager) count(ctx context.Context, c Config, network, reason string, t Threshold, failure bool) (*Ban, error) {
	if t.Limit == 0 {
		return nil, nil
	}
	if m.redis == nil {
		return nil, errors.New("redis unavailable")
	}
	prefix := modePrefix(c.ObserveOnly)
	hash := hashNetwork(network)
	now := time.Now()
	ban := Ban{Network: network, Reason: reason, CreatedAt: now.Unix(), ExpiresAt: now.Unix() + int64(c.BanSeconds), ObserveOnly: c.ObserveOnly}
	// A fresh internal generation prevents a stale administrative confirmation
	// from matching a replacement ban created with identical second timestamps.
	// It is retained only in Redis; public Ban metadata remains unchanged.
	encoded, _ := json.Marshal(struct {
		Ban
		Generation string `json:"generation"`
	}{Ban: ban, Generation: uuid.NewString()})
	limit := t.Limit + 1 // Allow exactly Limit requests; failures ban on the Limit-th failure.
	if failure {
		limit = t.Limit
	}
	var value string
	err := m.riskRedis(ctx, func(budget context.Context) error {
		var runErr error
		value, runErr = counterScript.Run(budget, m.redis, []string{prefix + hash + ":" + reason, prefix + hash + ":ban", prefix + "bans"},
			t.WindowSeconds, limit, string(encoded), c.BanSeconds, now.Unix(), ban.ExpiresAt, network).Text()
		return runErr
	})
	if err != nil {
		return nil, err
	}
	if value == "" {
		return nil, nil
	}
	if err := json.Unmarshal([]byte(value), &ban); err != nil {
		return nil, err
	}
	return &ban, nil
}

func (m *Manager) activeBan(ctx context.Context, network string, observe bool) (*Ban, error) {
	if m.redis == nil {
		return nil, errors.New("redis unavailable")
	}
	var value string
	err := m.riskRedis(ctx, func(budget context.Context) error {
		var runErr error
		value, runErr = m.redis.Get(budget, modePrefix(observe)+hashNetwork(network)+":ban").Result()
		return runErr
	})
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ban Ban
	if err := json.Unmarshal([]byte(value), &ban); err != nil {
		return nil, err
	}
	return &ban, nil
}

func (m *Manager) Bans(ctx context.Context) ([]Ban, error) {
	if m.redis == nil {
		return nil, errors.New("redis unavailable")
	}
	bans := []Ban{}
	for _, observe := range []bool{false, true} {
		prefix := modePrefix(observe)
		if err := m.redis.ZRemRangeByScore(ctx, prefix+"bans", "-inf", fmt.Sprint(time.Now().Unix())).Err(); err != nil {
			return nil, err
		}
		networks, err := m.redis.ZRangeArgs(ctx, redis.ZRangeArgs{Key: prefix + "bans", Start: 0, Stop: 499, Rev: true}).Result()
		if err != nil {
			return nil, err
		}
		if len(networks) == 0 {
			continue
		}
		keys := make([]string, len(networks))
		for i, network := range networks {
			keys[i] = prefix + hashNetwork(network) + ":ban"
		}
		values, err := m.redis.MGet(ctx, keys...).Result()
		if err != nil {
			return nil, err
		}
		for _, value := range values {
			if raw, ok := value.(string); ok {
				var ban Ban
				if json.Unmarshal([]byte(raw), &ban) == nil {
					bans = append(bans, ban)
				}
			}
		}
	}
	return bans, nil
}

func (m *Manager) Unban(ctx context.Context, raw string) error {
	network, err := ParseNetwork(raw)
	if err != nil {
		return err
	}
	if m.redis == nil {
		return errors.New("redis unavailable")
	}
	normalized := network.String()
	hash := hashNetwork(normalized)
	_, err = m.redis.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		for _, observe := range []bool{false, true} {
			prefix := modePrefix(observe)
			keys := []string{prefix + hash + ":ban"}
			for _, reason := range []string{"login_attempts", "login_failures", "register_attempts", "register_failures", "verification_attempts"} {
				keys = append(keys, prefix+hash+":"+reason)
			}
			pipe.Del(ctx, keys...)
			pipe.ZRem(ctx, prefix+"bans", normalized)
		}
		return nil
	})
	return err
}

type Event struct {
	ID          string `json:"id"`
	At          int64  `json:"at"`
	IP          string `json:"ip"`
	Network     string `json:"network"`
	Peer        string `json:"peer"`
	IPSource    string `json:"ip_source"`
	Country     string `json:"country"`
	Method      string `json:"method"`
	Category    string `json:"category"`
	Path        string `json:"path"`
	UserAgent   string `json:"user_agent"`
	Status      int    `json:"status"`
	Reason      string `json:"reason"`
	ErrorReason string `json:"error_reason"`
	Decision    string `json:"decision"`
}

func (m *Manager) record(ctx context.Context, c Config, event Event) {
	if m.redis == nil {
		return
	}
	event.ID = uuid.NewString()
	raw, err := json.Marshal(event)
	if err != nil {
		return
	}
	_, err = m.redis.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.ZAdd(ctx, redisPrefix+"events", redis.Z{Score: float64(time.Now().UnixMicro()), Member: string(raw)})
		pipe.ZRemRangeByScore(ctx, redisPrefix+"events", "-inf", fmt.Sprint(time.Now().Add(-time.Duration(c.EventRetentionHours)*time.Hour).UnixMicro()))
		pipe.ZRemRangeByRank(ctx, redisPrefix+"events", 0, -maxEvents-1)
		pipe.Expire(ctx, redisPrefix+"events", time.Duration(c.EventRetentionHours)*time.Hour)
		return nil
	})
	if err != nil {
		slog.Warn("IP risk event storage unavailable")
	}
}

func (m *Manager) Events(ctx context.Context, ipFilter, reason, category string) ([]Event, error) {
	result, err := m.EventsPage(ctx, ipFilter, reason, category, 1, 200)
	return result.Items, err
}

type EventPage struct {
	Items    []Event `json:"items"`
	Total    int     `json:"total"`
	Page     int     `json:"page"`
	PageSize int     `json:"page_size"`
	Pages    int     `json:"pages"`
}

// EventsPage filters the bounded event stream before counting and paginating it.
func (m *Manager) EventsPage(ctx context.Context, ipFilter, reason, category string, page, pageSize int) (EventPage, error) {
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 1000 {
		pageSize = 1000
	}
	ipFilter = strings.TrimSpace(ipFilter)
	if ipFilter != "" {
		network, err := ParseNetwork(ipFilter)
		if err != nil {
			return EventPage{}, err
		}
		if strings.Contains(ipFilter, "/") {
			ipFilter = network.String()
		} else {
			ipFilter = network.Addr().String()
		}
	}
	if m.redis == nil {
		return EventPage{}, errors.New("redis unavailable")
	}
	c, err := m.Config(ctx)
	if err != nil {
		return EventPage{}, err
	}
	if err := m.redis.ZRemRangeByScore(ctx, redisPrefix+"events", "-inf", fmt.Sprint(time.Now().Add(-time.Duration(c.EventRetentionHours)*time.Hour).UnixMicro())).Err(); err != nil {
		return EventPage{}, err
	}
	values, err := m.redis.ZRangeArgs(ctx, redis.ZRangeArgs{Key: redisPrefix + "events", Start: 0, Stop: maxEvents - 1, Rev: true}).Result()
	if err != nil {
		return EventPage{}, err
	}
	events := []Event{}
	cutoff := time.Now().Add(-time.Duration(c.EventRetentionHours) * time.Hour).Unix()
	for _, value := range values {
		var event Event
		if json.Unmarshal([]byte(value), &event) != nil || event.At < cutoff {
			continue
		}
		if ipFilter != "" && event.IP != ipFilter && event.Network != ipFilter {
			continue
		}
		if reason != "" && event.Reason != reason {
			continue
		}
		if category != "" && event.Category != category {
			continue
		}
		events = append(events, event)
	}
	total := len(events)
	pages := max(1, (total+pageSize-1)/pageSize)
	// Retention or clearing can remove pages between requests. Clamp before
	// calculating the offset, which also avoids overflow from oversized pages.
	page = min(max(1, page), pages)
	start := (page - 1) * pageSize
	return EventPage{
		Items: events[start:min(start+pageSize, total)],
		Total: total, Page: page, PageSize: pageSize, Pages: pages,
	}, nil
}

func (m *Manager) ClearEvents(ctx context.Context) error {
	if m.redis == nil {
		return errors.New("redis unavailable")
	}
	return m.redis.Del(ctx, redisPrefix+"events").Err()
}

func (m *Manager) country(ctx context.Context, ip netip.Addr) string {
	if !publicIP(ip) || m.geo == nil {
		return ""
	}
	return strings.ToUpper(m.geo.Country(ctx, ip.String()))
}

func publicIP(ip netip.Addr) bool {
	return ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast()
}

func countryDenied(c Config, ip netip.Addr, country string) bool {
	if !publicIP(ip) {
		return false
	}
	if country == "" {
		return c.BlockUnknownCountry || len(c.AllowedCountries) > 0
	}
	for _, blocked := range c.BlockedCountries {
		if blocked == country {
			return true
		}
	}
	if len(c.AllowedCountries) > 0 {
		for _, allowed := range c.AllowedCountries {
			if allowed == country {
				return false
			}
		}
		return true
	}
	return false
}

func hasCountryRules(c Config) bool {
	return len(c.AllowedCountries) > 0 || len(c.BlockedCountries) > 0 || c.BlockUnknownCountry
}

func (m *Manager) Health(ctx context.Context) map[string]any {
	redisOK := m.redis != nil && m.redis.Ping(ctx).Err() == nil
	m.mu.Lock()
	defer m.mu.Unlock()
	return map[string]any{"redis_available": redisOK, "emergency_disabled": m.emergencyDisabled, "config_error": m.lastLoadError}
}

// Check previews saved policy without incrementing counters or recording a login.
func (m *Manager) Check(ctx context.Context, rawIP, ua string, auth bool) (Event, error) {
	ip, err := netip.ParseAddr(strings.TrimSpace(rawIP))
	if err != nil || ip.Zone() != "" {
		return Event{}, fmt.Errorf("invalid IP address")
	}
	ip = ip.Unmap()
	p, err := m.snapshot(ctx)
	if err != nil {
		return Event{}, err
	}
	event := Event{IP: ip.String(), Network: riskNetwork(ip, p.config.IPv6Prefix), Decision: "allowed"}
	if m.emergencyDisabled || !p.config.Enabled {
		event.Decision = "disabled"
		return event, nil
	}
	if p.allowed(ip) {
		event.Decision = "whitelisted"
		return event, nil
	}
	reason, _, err := m.assess(ctx, p, ip, "", "", &event)
	if err != nil {
		return Event{}, err
	}
	if reason == "" && auth {
		reason = uaDenied(p.config, ua)
	}
	event.Reason = reason
	if reason != "" {
		event.Decision = "blocked"
		if p.config.ObserveOnly {
			event.Decision = "would_block"
		}
	}
	return event, nil
}
