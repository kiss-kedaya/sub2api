package iprisk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	ippkg "github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type memoryStore struct {
	mu  sync.Mutex
	c   Config
	err error
}

func (s *memoryStore) Load(context.Context) (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.c, s.err
}
func (s *memoryStore) Save(_ context.Context, c Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.c = c
	return nil
}

type countryStub string

func (s countryStub) Country(context.Context, string) string { return string(s) }

func testManager(t *testing.T, modify func(*Config)) (*Manager, *miniredis.Miniredis, *redis.Client) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	c := DefaultConfig()
	c.Enabled = true
	c.ObserveOnly = false
	if modify != nil {
		modify(&c)
	}
	c, err := NormalizeConfig(c)
	require.NoError(t, err)
	return New(&memoryStore{c: c}, client, countryStub("US"), false), server, client
}

func riskRouter(t *testing.T, m *Manager) *gin.Engine {
	t.Helper()
	r := gin.New()
	require.NoError(t, r.SetTrustedProxies(nil))
	r.Use(m.Middleware())
	r.GET("/health", func(c *gin.Context) { c.Status(200) })
	r.GET("/", func(c *gin.Context) { c.Status(200) })
	r.POST("/v1/responses", func(c *gin.Context) { c.Status(200) })
	for _, path := range []string{"/api/v1/auth/login", "/api/v1/auth/login/2fa", "/api/v1/auth/register", "/api/v1/auth/oauth/pending/create-account", "/api/v1/auth/send-verify-code", "/api/v1/auth/forgot-password", "/api/v1/auth/refresh"} {
		r.POST(path, func(c *gin.Context) {
			switch c.GetHeader("Test-Result") {
			case "bad_credentials":
				response.ErrorWithDetails(c, 401, "Invalid credentials", "INVALID_CREDENTIALS", nil)
			case "bad_totp":
				response.ErrorWithDetails(c, 400, "Invalid code", "TOTP_INVALID_CODE", nil)
			case "bad_registration":
				response.ErrorWithDetails(c, 400, "Invalid registration", "BAD_REQUEST", nil)
			case "captcha":
				response.ErrorWithDetails(c, 400, "Captcha required", "CAPTCHA_REQUIRED", nil)
			case "rate_limit":
				c.Status(429)
			case "unavailable":
				c.Status(503)
			default:
				c.Status(200)
			}
		})
	}
	r.GET("/api/v1/auth/oauth/github/callback", func(c *gin.Context) { c.Status(302) })
	r.GET("/api/v1/auth/oauth/wechat/payment/callback", func(c *gin.Context) { c.Status(302) })
	return r
}

func riskRequest(r http.Handler, method, path, ip, ua, result string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	address := netip.MustParseAddr(ip)
	req.RemoteAddr = netip.AddrPortFrom(address, 1234).String()
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Test-Result", result)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestIPRiskNetworkValidation(t *testing.T) {
	for _, test := range []struct{ raw, want string }{
		{"198.51.100.1", "198.51.100.1/32"}, {"::ffff:198.51.100.1", "198.51.100.1/32"},
		{"::ffff:198.51.100.1/120", "198.51.100.0/24"}, {"2001:db8:abcd::1/64", "2001:db8:abcd::/64"},
	} {
		value, err := ParseNetwork(test.raw)
		require.NoError(t, err)
		require.Equal(t, test.want, value.String())
	}
	for _, raw := range []string{"invalid", "1.2.3.4/33", "fe80::1%eth0", "::ffff:192.0.2.1/80"} {
		_, err := ParseNetwork(raw)
		require.Error(t, err, raw)
	}
	c := DefaultConfig()
	c.BlockedCountries = []string{"us", "JP"}
	normalized, err := NormalizeConfig(c)
	require.NoError(t, err)
	require.Equal(t, []string{"US", "JP"}, normalized.BlockedCountries)
	for _, change := range []func(*Config){
		func(c *Config) { c.IPv6Prefix = 63 }, func(c *Config) { c.BanSeconds = 0 },
		func(c *Config) { c.LoginFailures.WindowSeconds = 0 }, func(c *Config) { c.LoginFailures.Limit = -1 },
		func(c *Config) { c.Whitelist = []string{"bad"} }, func(c *Config) { c.Blacklist = []Rule{{Network: "bad"}} },
		func(c *Config) { c.BlockedCountries = []string{"ZZ"} }, func(c *Config) { c.DenyUserAgents = []string{""} },
		func(c *Config) { c.DenyUserAgents = []string{"bad\nagent"} },
	} {
		c := DefaultConfig()
		change(&c)
		_, err := NormalizeConfig(c)
		require.Error(t, err)
	}
}

func TestIPRiskBlacklistWhitelistAndExpiration(t *testing.T) {
	m, _, _ := testManager(t, func(c *Config) {
		c.Blacklist = []Rule{{Network: "198.51.100.0/24", Enabled: true}, {Network: "2001:db8::/32", Enabled: true}}
		c.Whitelist = []string{"198.51.100.7", "2001:db8:1::/48"}
		c.BlockedCountries = []string{"US"}
		c.BlockEmptyUA = true
	})
	r := riskRouter(t, m)
	for _, ip := range []string{"198.51.100.8", "2001:db8:2::1", "::ffff:198.51.100.8"} {
		require.Equal(t, 403, riskRequest(r, "GET", "/", ip, "Mozilla/5.0", "").Code)
	}
	for _, ip := range []string{"198.51.100.7", "2001:db8:1::1234", "::ffff:198.51.100.7"} {
		require.Equal(t, 200, riskRequest(r, "POST", "/api/v1/auth/login", ip, "", "").Code)
	}
	cfg, err := m.Config(context.Background())
	require.NoError(t, err)
	cfg.BlockedCountries = nil
	cfg.Blacklist[0].ExpiresAt = time.Now().Add(-time.Second).Unix()
	cfg.Blacklist[1].Enabled = false
	require.NoError(t, m.Save(context.Background(), cfg))
	for _, ip := range []string{"198.51.100.8", "2001:db8:2::1"} {
		require.Equal(t, 200, riskRequest(r, "GET", "/", ip, "Mozilla/5.0", "").Code)
	}
}

func TestIPRiskUserAgentOnlyAuthentication(t *testing.T) {
	m, _, _ := testManager(t, func(c *Config) { c.BlockEmptyUA = true })
	r := riskRouter(t, m)
	for _, test := range []struct {
		method, path, ua string
		status           int
	}{
		{"POST", "/api/v1/auth/login", "Python-Requests/2.32", 403},
		{"POST", "/api/v1/auth/register", "", 403},
		{"POST", "/api/v1/auth/oauth/pending/create-account", "httpx/0.1", 403},
		{"GET", "/api/v1/auth/oauth/github/callback", "curl/8.0", 403},
		{"POST", "/v1/responses", "python-requests/2.32", 200},
		{"POST", "/api/v1/auth/refresh", "", 200},
		{"GET", "/api/v1/auth/oauth/wechat/payment/callback", "python-requests", 302},
		{"POST", "/api/v1/auth/login", "Mozilla/5.0", 200},
	} {
		require.Equal(t, test.status, riskRequest(r, test.method, test.path, "198.51.100.1", test.ua, "").Code, test.path)
	}
}

func TestIPRiskUsesTrustedChainEvenWhenLegacyHeadersAreEnabled(t *testing.T) {
	m, _, _ := testManager(t, func(c *Config) { c.Blacklist = []Rule{{Network: "198.51.100.1", Enabled: true}} })
	r := gin.New()
	require.NoError(t, r.SetTrustedProxies(nil))
	r.Use(func(c *gin.Context) { ippkg.SetLegacyForwardedIPTrust(c, true); c.Next() })
	r.Use(m.Middleware())
	r.GET("/", func(c *gin.Context) { c.Status(200) })
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "198.51.100.1:1234"
	for _, header := range []string{"X-Forwarded-For", "X-Real-IP", "CF-Connecting-IP"} {
		req.Header.Set(header, "203.0.113.2")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, 403, w.Code)
	require.NoError(t, r.SetTrustedProxies([]string{"127.0.0.1"}))
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "198.51.100.1")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, 403, w.Code)

}

func TestIPRiskCountriesAndUnknownPolicy(t *testing.T) {
	m, _, _ := testManager(t, func(c *Config) { c.BlockedCountries = []string{"US", "JP"} })
	r := riskRouter(t, m)
	require.Equal(t, 403, riskRequest(r, "GET", "/", "198.51.100.1", "Mozilla/5.0", "").Code)
	m.geo = countryStub("JP")
	require.Equal(t, 403, riskRequest(r, "POST", "/v1/responses", "2001:db8::1", "Python", "").Code)
	m.geo = countryStub("DE")
	require.Equal(t, 200, riskRequest(r, "GET", "/", "198.51.100.1", "Mozilla/5.0", "").Code)
	m.geo = countryStub("")
	require.Equal(t, 200, riskRequest(r, "GET", "/", "198.51.100.1", "Mozilla/5.0", "").Code)
	cfg, err := m.Config(context.Background())
	require.NoError(t, err)
	cfg.BlockUnknownCountry = true
	require.NoError(t, m.Save(context.Background(), cfg))
	require.Equal(t, 403, riskRequest(r, "GET", "/", "198.51.100.1", "Mozilla/5.0", "").Code)
	require.Equal(t, 200, riskRequest(r, "GET", "/", "10.1.2.3", "Mozilla/5.0", "").Code)
}

func TestIPRiskAutomaticFailureBanSharedAndFixedExpiry(t *testing.T) {
	m, server, client := testManager(t, func(c *Config) { c.LoginFailures.Limit = 2; c.BanSeconds = 60 })
	r := riskRouter(t, m)
	for _, result := range []string{"captcha", "unavailable", "rate_limit", ""} {
		riskRequest(r, "POST", "/api/v1/auth/login", "198.51.100.1", "Mozilla/5.0", result)
	}
	bans, err := m.Bans(context.Background())
	require.NoError(t, err)
	require.Empty(t, bans)
	require.Equal(t, 401, riskRequest(r, "POST", "/api/v1/auth/login", "198.51.100.1", "Mozilla/5.0", "bad_credentials").Code)
	require.Equal(t, 200, riskRequest(r, "POST", "/api/v1/auth/login", "198.51.100.1", "Mozilla/5.0", "").Code)
	require.Equal(t, 400, riskRequest(r, "POST", "/api/v1/auth/login/2fa", "198.51.100.1", "Mozilla/5.0", "bad_totp").Code)
	key := modePrefix(false) + hashNetwork("198.51.100.1/32") + ":ban"
	rawBan, err := server.Get(key)
	require.NoError(t, err)
	var ban Ban
	require.NoError(t, json.Unmarshal([]byte(rawBan), &ban))
	require.Equal(t, int64(60), ban.ExpiresAt-ban.CreatedAt)
	require.Equal(t, 60*time.Second, server.TTL(key))
	// Another process with an independent config cache must see the same Redis ban.
	other := New(m.store, client, countryStub("US"), false)
	r2 := riskRouter(t, other)
	beforeRequest := time.Now().Unix()
	blocked := riskRequest(r2, "POST", "/v1/responses", "198.51.100.1", "Python", "")
	afterRequest := time.Now().Unix()
	require.Equal(t, 429, blocked.Code)
	retryAfter, err := strconv.ParseInt(blocked.Header().Get("Retry-After"), 10, 64)
	require.NoError(t, err)
	// The stored expiry is fixed, but Retry-After uses wall-clock Unix seconds.
	// Creating the second manager may cross a second boundary; validate against
	// the request's actual time window rather than assuming it is still creation time.
	require.GreaterOrEqual(t, retryAfter, max(int64(1), ban.ExpiresAt-afterRequest))
	require.LessOrEqual(t, retryAfter, max(int64(1), ban.ExpiresAt-beforeRequest))
	server.FastForward(30 * time.Second)
	require.Equal(t, 429, riskRequest(r, "GET", "/", "198.51.100.1", "Mozilla/5.0", "").Code)
	require.Equal(t, 30*time.Second, server.TTL(key))
	unchangedBan, err := server.Get(key)
	require.NoError(t, err)
	require.Equal(t, rawBan, unchangedBan, "blocked requests must not rewrite the fixed expiry")
	require.Equal(t, 200, riskRequest(r, "GET", "/health", "198.51.100.1", "", "").Code)
	require.NoError(t, other.Unban(context.Background(), "198.51.100.1"))
	require.Equal(t, 200, riskRequest(r, "GET", "/", "198.51.100.1", "Mozilla/5.0", "").Code)
	// Failure history is cleared too, so a single new failure does not immediately reban.
	riskRequest(r, "POST", "/api/v1/auth/login", "198.51.100.1", "Mozilla/5.0", "bad_credentials")
	require.Equal(t, 200, riskRequest(r, "GET", "/", "198.51.100.1", "Mozilla/5.0", "").Code)
}

func TestIPRiskRegistrationVerificationAndIPv6Aggregation(t *testing.T) {
	m, server, _ := testManager(t, func(c *Config) { c.RegisterAttempts.Limit = 2; c.VerificationAttempts.Limit = 1; c.BanSeconds = 60 })
	r := riskRouter(t, m)
	for _, path := range []string{"/api/v1/auth/register", "/api/v1/auth/oauth/pending/create-account"} {
		require.Equal(t, 200, riskRequest(r, "POST", path, "2001:db8:1::1", "Mozilla/5.0", "").Code)
	}
	require.Equal(t, 429, riskRequest(r, "GET", "/api/v1/auth/oauth/github/callback", "2001:db8:1::ffff", "Mozilla/5.0", "").Code)
	require.Equal(t, 200, riskRequest(r, "GET", "/", "2001:db8:2::1", "Mozilla/5.0", "").Code)
	require.Equal(t, 200, riskRequest(r, "POST", "/api/v1/auth/send-verify-code", "198.51.100.9", "Mozilla/5.0", "").Code)
	require.Equal(t, 429, riskRequest(r, "POST", "/api/v1/auth/forgot-password", "198.51.100.9", "Mozilla/5.0", "").Code)
	server.FastForward(61 * time.Second)
	require.Equal(t, 200, riskRequest(r, "GET", "/", "198.51.100.9", "Mozilla/5.0", "").Code)
}

func TestIPRiskRegistrationFailuresExcludeServiceErrorsAndRateLimits(t *testing.T) {
	m, _, _ := testManager(t, func(c *Config) { c.RegisterAttempts.Limit = 0; c.RegisterFailures.Limit = 2 })
	r := riskRouter(t, m)
	for _, result := range []string{"unavailable", "rate_limit", "bad_registration"} {
		riskRequest(r, "POST", "/api/v1/auth/register", "198.51.100.1", "Mozilla/5.0", result)
	}
	require.Equal(t, 200, riskRequest(r, "GET", "/", "198.51.100.1", "", "").Code)
	riskRequest(r, "POST", "/api/v1/auth/oauth/pending/create-account", "198.51.100.1", "Mozilla/5.0", "bad_registration")
	require.Equal(t, 429, riskRequest(r, "GET", "/", "198.51.100.1", "", "").Code)
}

func TestIPRiskObservationAndWhitelistedClients(t *testing.T) {
	m, _, _ := testManager(t, func(c *Config) { c.ObserveOnly = true; c.LoginFailures.Limit = 1 })
	r := riskRouter(t, m)
	require.Equal(t, 200, riskRequest(r, "POST", "/api/v1/auth/login", "198.51.100.1", "python-requests", "").Code)
	riskRequest(r, "POST", "/api/v1/auth/login", "198.51.100.1", "Mozilla/5.0", "bad_credentials")
	require.Equal(t, 200, riskRequest(r, "GET", "/", "198.51.100.1", "Mozilla/5.0", "").Code)
	cfg, err := m.Config(context.Background())
	require.NoError(t, err)
	cfg.ObserveOnly = false
	require.NoError(t, m.Save(context.Background(), cfg))
	require.Equal(t, 200, riskRequest(r, "GET", "/", "198.51.100.1", "Mozilla/5.0", "").Code, "observation bans must not become enforcement bans")
	riskRequest(r, "POST", "/api/v1/auth/login", "198.51.100.1", "Mozilla/5.0", "bad_credentials")
	require.Equal(t, 429, riskRequest(r, "GET", "/", "198.51.100.1", "Mozilla/5.0", "").Code)
	cfg.Whitelist = []string{"198.51.100.1"}
	require.NoError(t, m.Save(context.Background(), cfg))
	require.Equal(t, 200, riskRequest(r, "POST", "/api/v1/auth/login", "198.51.100.1", "python-requests", "").Code)
}

func TestIPRiskRedisOutageEmergencyAndDisabled(t *testing.T) {
	m, server, _ := testManager(t, func(c *Config) { c.Whitelist = []string{"198.51.100.7"} })
	r := riskRouter(t, m)
	_, err := m.Config(context.Background())
	require.NoError(t, err)
	server.Close()
	require.Equal(t, 200, riskRequest(r, "GET", "/", "198.51.100.1", "", "").Code)
	require.Equal(t, 200, riskRequest(r, "POST", "/v1/responses", "198.51.100.1", "python-requests", "").Code)
	require.Equal(t, 200, riskRequest(r, "POST", "/api/v1/auth/login", "198.51.100.1", "Mozilla", "").Code)
	require.Equal(t, 200, riskRequest(r, "GET", "/", "198.51.100.7", "", "").Code)
	require.Equal(t, 200, riskRequest(r, "GET", "/health", "198.51.100.1", "", "").Code)
	emergency := New(m.store, nil, nil, true)
	require.Equal(t, 200, riskRequest(riskRouter(t, emergency), "GET", "/", "198.51.100.1", "", "").Code)
	cfg := DefaultConfig()
	disabled := New(&memoryStore{c: cfg}, nil, nil, false)
	require.Equal(t, 200, riskRequest(riskRouter(t, disabled), "POST", "/api/v1/auth/login", "198.51.100.1", "python-requests", "").Code)
}

func TestIPRiskAdministratorSelfLockAndPreview(t *testing.T) {
	m, _, _ := testManager(t, nil)
	c, err := m.Config(context.Background())
	require.NoError(t, err)
	c.Blacklist = []Rule{{Network: "198.51.100.0/24", Enabled: true}}
	require.Error(t, m.CheckAdministrator(context.Background(), c, "198.51.100.1", "Mozilla"))
	c.Whitelist = []string{"198.51.100.1"}
	require.NoError(t, m.CheckAdministrator(context.Background(), c, "198.51.100.1", "python-requests"))
	c.Blacklist = nil
	c.Whitelist = nil
	c.BlockedCountries = []string{"US"}
	require.Error(t, m.CheckAdministrator(context.Background(), c, "198.51.100.1", "Mozilla"))
	c.ObserveOnly = true
	require.NoError(t, m.CheckAdministrator(context.Background(), c, "198.51.100.1", "Mozilla"))
	require.NoError(t, m.Save(context.Background(), c))
	preview, err := m.Check(context.Background(), "198.51.100.1", "Mozilla", true)
	require.NoError(t, err)
	require.Equal(t, "would_block", preview.Decision)
	require.Equal(t, "country_blocked", preview.Reason)
	c.BlockedCountries = nil
	c.ObserveOnly = false
	require.NoError(t, m.Save(context.Background(), c))
	require.Error(t, m.CheckAdministrator(context.Background(), c, "198.51.100.1", "python-requests"))
	preview, err = m.Check(context.Background(), "198.51.100.1", "python-requests", true)
	require.NoError(t, err)
	require.Equal(t, "user_agent_blocked", preview.Reason)
	preview, err = m.Check(context.Background(), "198.51.100.1", "python-requests", false)
	require.NoError(t, err)
	require.Equal(t, "allowed", preview.Decision)
	bans, err := m.Bans(context.Background())
	require.NoError(t, err)
	require.Empty(t, bans)
}

func TestIPRiskBoundedEventsAndSafeMetadata(t *testing.T) {
	m, _, client := testManager(t, nil)
	r := riskRouter(t, m)
	riskRequest(r, "POST", "/api/v1/auth/login?token=SECRET_QUERY", "198.51.100.1", "Mozilla/5.0 SECRET_UA", "")
	items, err := m.Events(context.Background(), "::ffff:198.51.100.1", "", "login")
	require.NoError(t, err)
	require.Len(t, items, 1)
	encoded, err := json.Marshal(items)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "SECRET_QUERY")
	require.Contains(t, items[0].UserAgent, "SECRET_UA")
	require.Equal(t, "/api/v1/auth/login", items[0].Path)
	require.Equal(t, "198.51.100.1", items[0].Peer)
	otherCategory, err := m.Events(context.Background(), "198.51.100.1", "", "register")
	require.NoError(t, err)
	require.Empty(t, otherCategory)
	cfg, err := m.Config(context.Background())
	require.NoError(t, err)
	// Existing over-capacity data is bounded on the next event write.
	members := make([]redis.Z, maxEvents+10)
	for i := range members {
		members[i] = redis.Z{Score: float64(time.Now().UnixMicro()), Member: fmt.Sprintf("dummy-%d", i)}
	}
	require.NoError(t, client.ZAdd(context.Background(), redisPrefix+"events", members...).Err())
	m.record(context.Background(), cfg, Event{At: time.Now().Unix(), IP: "198.51.100.1", Decision: "allowed"})
	require.EqualValues(t, maxEvents, client.ZCard(context.Background(), redisPrefix+"events").Val())
	old := Event{At: time.Now().Add(-169 * time.Hour).Unix(), IP: "203.0.113.1"}
	raw, _ := json.Marshal(old)
	require.NoError(t, client.ZAdd(context.Background(), redisPrefix+"events", redis.Z{Score: float64(old.At) * 1e6, Member: string(raw)}).Err())
	items, err = m.Events(context.Background(), "203.0.113.1", "", "")
	require.NoError(t, err)
	require.Empty(t, items)
	require.NoError(t, m.ClearEvents(context.Background()))
	require.Zero(t, client.ZCard(context.Background(), redisPrefix+"events").Val())
	require.Equal(t, "a界", cleanText("a\x00界\nzz", 4))
}

func TestIPRiskAtomicCounterAndConcurrentConfig(t *testing.T) {
	m, _, _ := testManager(t, func(c *Config) { c.LoginFailures.Limit = 10 })
	cfg, err := m.Config(context.Background())
	require.NoError(t, err)
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, countErr := m.count(context.Background(), cfg, "198.51.100.1/32", "login_failures", cfg.LoginFailures, true)
			require.NoError(t, countErr)
			clone, configErr := m.Config(context.Background())
			require.NoError(t, configErr)
			clone.DenyUserAgents[0] = "changed" // Must not mutate the live snapshot.
		}()
	}
	wg.Wait()
	ban, err := m.activeBan(context.Background(), "198.51.100.1/32", false)
	require.NoError(t, err)
	require.NotNil(t, ban)
	current, err := m.Config(context.Background())
	require.NoError(t, err)
	require.Equal(t, "python-requests", current.DenyUserAgents[0])
	store, ok := m.store.(*memoryStore)
	require.True(t, ok)
	store.mu.Lock()
	store.err = errors.New("database down")
	store.mu.Unlock()
	m.mu.Lock()
	m.expires = time.Time{}
	m.mu.Unlock()
	current, err = m.Config(context.Background())
	require.NoError(t, err)
	require.True(t, current.Enabled)
	require.Eventually(t, func() bool { return m.Health(context.Background())["config_error"] == "configuration_refresh_failed" }, time.Second, time.Millisecond)
}

func TestIPRiskAuthCategoryBoundaries(t *testing.T) {
	for _, path := range []string{"/api/v1/auth/register", "/api/v1/auth/oauth/oidc/create-account", "/api/v1/auth/oauth/github/complete-registration"} {
		require.Equal(t, "register", authCategory("POST", path))
	}
	require.Empty(t, authCategory("POST", "/api/v1/other/register"))
	require.Empty(t, authCategory("POST", "/api/v1/auth/logout"))
	require.Empty(t, authCategory("GET", "/api/v1/auth/oauth/wechat/payment/callback"))
	require.Equal(t, "register", authCategory("GET", "/api/v1/auth/oauth/oidc/callback"))
	require.Equal(t, "verification", authCategory("POST", "/api/v1/auth/oauth/pending/exchange"))
	require.NotContains(t, strings.Join(DefaultConfig().DenyUserAgents, ","), "mozilla")
}
