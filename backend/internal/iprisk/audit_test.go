package iprisk

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestIPRiskAllowedCountriesEnforcement(t *testing.T) {
	for _, test := range []struct {
		name, country, address, reason string
		want                           int
	}{
		{"china", "CN", "8.8.8.8", "", 200},
		{"outside", "US", "8.8.8.8", "country_not_allowed", 403},
		{"unknown", "", "8.8.8.8", "country_unknown", 403},
		{"ipv6 outside", "JP", "2606:4700:4700::1111", "country_not_allowed", 403},
		{"private", "", "10.1.2.3", "", 200},
		{"whitelist", "US", "1.1.1.1", "", 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			m, _, _ := testManager(t, func(c *Config) { c.AllowedCountries = []string{"CN"}; c.Whitelist = []string{"1.1.1.1"} })
			m.geo = countryStub(test.country)
			defer m.Stop()
			r := riskRouter(t, m)
			require.Equal(t, test.want, riskRequest(r, "POST", "/v1/responses", test.address, "python-requests", "").Code)
			event, err := m.Check(context.Background(), test.address, "", false)
			require.NoError(t, err)
			require.Equal(t, test.reason, event.Reason)
		})
	}
	c := DefaultConfig()
	c.AllowedCountries = []string{" cn "}
	normalized, err := NormalizeConfig(c)
	require.NoError(t, err)
	require.Equal(t, []string{"CN"}, normalized.AllowedCountries)
	normalized.AllowedCountries[0] = "JP"
	require.Equal(t, []string{" cn "}, c.AllowedCountries)
	c.BlockedCountries = []string{"CN"}
	_, err = NormalizeConfig(c)
	require.Error(t, err)
	c.BlockedCountries = nil
	c.AllowedCountries = []string{"ZZ"}
	_, err = NormalizeConfig(c)
	require.Error(t, err)
}

func TestIPRiskAllowedCountriesObservationAndAdministrator(t *testing.T) {
	m, _, _ := testManager(t, func(c *Config) { c.AllowedCountries = []string{"CN"}; c.ObserveOnly = true })
	defer m.Stop()
	require.Equal(t, 200, riskRequest(riskRouter(t, m), "GET", "/", "8.8.8.8", "Mozilla", "").Code)
	events, err := m.Events(context.Background(), "8.8.8.8", "country_not_allowed", "")
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "would_block", events[0].Decision)
	cfg, err := m.Config(context.Background())
	require.NoError(t, err)
	cfg.ObserveOnly = false
	require.Error(t, m.CheckAdministrator(context.Background(), cfg, "8.8.8.8", "Mozilla"))
	m.geo = countryStub("")
	require.Error(t, m.CheckAdministrator(context.Background(), cfg, "8.8.8.8", "Mozilla"))
	cfg.Whitelist = []string{"8.8.8.8"}
	require.NoError(t, m.CheckAdministrator(context.Background(), cfg, "8.8.8.8", "Mozilla"))
}

func TestIPRiskRedisOutageRetainsStaticPolicies(t *testing.T) {
	m, server, _ := testManager(t, func(c *Config) {
		c.AllowedCountries = []string{"CN"}
		c.Blacklist = []Rule{{Network: "1.1.1.1", Enabled: true}}
	})
	defer m.Stop()
	server.Close()
	r := riskRouter(t, m)
	require.Equal(t, 403, riskRequest(r, "GET", "/", "1.1.1.1", "", "").Code)
	require.Equal(t, 403, riskRequest(r, "POST", "/v1/responses", "8.8.8.8", "", "").Code)
	m.geo = countryStub("CN")
	require.Equal(t, 200, riskRequest(r, "POST", "/v1/responses", "8.8.8.8", "", "").Code)
	require.Equal(t, 403, riskRequest(r, "POST", "/api/v1/auth/login", "8.8.8.8", "python-requests", "").Code)
	require.Equal(t, 200, riskRequest(r, "POST", "/api/v1/auth/login", "8.8.8.8", "Mozilla", "").Code)
	cfg, err := m.Config(context.Background())
	require.NoError(t, err)
	cfg.Enabled = false
	require.NoError(t, m.Save(context.Background(), cfg)) // Disabling never requires Redis.
	require.Equal(t, 200, riskRequest(r, "POST", "/api/v1/auth/login", "1.1.1.1", "python-requests", "").Code)
}

func TestIPRiskMachineCallbacksKeepOriginalAuthentication(t *testing.T) {
	m, server, _ := testManager(t, func(c *Config) {
		c.AllowedCountries = []string{"CN"}
		c.Blacklist = []Rule{{Network: "0.0.0.0/0", Enabled: true}}
	})
	defer m.Stop()
	server.Close()
	r := riskRouter(t, m)
	// Stub the downstream verification guard: risk exemption must still call it.
	guard := func(c *gin.Context) {
		if c.GetHeader("Test-Result") != "verified" {
			c.AbortWithStatus(401)
			return
		}
		c.Status(204)
	}
	for _, route := range []struct{ method, path, url string }{
		{"POST", "/api/v1/payment/webhook/stripe", "/api/v1/payment/webhook/stripe"},
		{"GET", "/api/v1/payment/webhook/easypay", "/api/v1/payment/webhook/easypay"},
		{"GET", "/api/bps-images/:token", "/api/bps-images/capability"},
		{"HEAD", "/api/bps-images/:token", "/api/bps-images/capability"},
		{"POST", "/api/v1/internal/openai-reauth/:task_id/callback", "/api/v1/internal/openai-reauth/task/callback"},
	} {
		r.Handle(route.method, route.path, guard)
		require.Equal(t, 401, riskRequest(r, route.method, route.url, "8.8.8.8", "python-requests", "").Code)
		require.Equal(t, 204, riskRequest(r, route.method, route.url, "8.8.8.8", "python-requests", "verified").Code)
	}
	for _, route := range []struct{ method, path string }{
		{"POST", "/api/v1/payment/webhook/stripe/extra"},
		{"GET", "/api/v1/payment/webhook/stripe"},
		{"POST", "/api/bps-images/:token"},
		{"POST", "/api/v1/internal/openai-reauth/not-a-worker-route"},
	} {
		require.False(t, machineCallback(route.method, route.path))
	}
}

// Captures a stale load before a save; channels make ordering deterministic.
type delayedStore struct {
	memoryStore
	loadGate, saveGate <-chan struct{}
	started            chan struct{}
}

func (s *delayedStore) Load(ctx context.Context) (Config, error) {
	s.mu.Lock()
	c, gate, started := s.c, s.loadGate, s.started
	s.mu.Unlock()
	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return Config{}, ctx.Err()
		}
	}
	return c, nil
}
func (s *delayedStore) Save(ctx context.Context, c Config) error {
	s.mu.Lock()
	gate := s.saveGate
	s.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return s.memoryStore.Save(ctx, c)
}

func TestIPRiskColdConfigurationFailureDoesNotBlockSystem(t *testing.T) {
	m := New(&memoryStore{err: errors.New("database unavailable")}, nil, nil, false)
	defer m.Stop()
	r := riskRouter(t, m)
	require.Equal(t, 200, riskRequest(r, "POST", "/api/v1/auth/login", "8.8.8.8", "python-requests", "").Code)
	require.Equal(t, 200, riskRequest(r, "POST", "/v1/responses", "8.8.8.8", "python-requests", "").Code)
	require.Equal(t, "configuration_refresh_failed", m.Health(context.Background())["config_error"])
}

func TestIPRiskRefreshDoesNotBlockAndCannotOverwriteSave(t *testing.T) {
	store := &delayedStore{memoryStore: memoryStore{c: DefaultConfig()}}
	m := New(store, nil, nil, false)
	defer m.Stop()
	gate := make(chan struct{})
	defer func() {
		select {
		case <-gate:
		default:
			close(gate)
		}
	}()
	started := make(chan struct{}, 1)
	store.mu.Lock()
	store.loadGate, store.started = gate, started
	store.mu.Unlock()
	m.mu.Lock()
	m.expires = time.Time{}
	m.mu.Unlock()
	r := riskRouter(t, m)
	require.Equal(t, 200, riskRequest(r, "GET", "/", "8.8.8.8", "", "").Code)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("refresh did not start")
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		var wg sync.WaitGroup
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				require.Equal(t, 200, riskRequest(r, "POST", "/api/v1/auth/login", "8.8.8.8", "python-requests", "").Code)
			}()
		}
		wg.Wait()
	}()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("requests blocked behind database IO")
	}
	c := DefaultConfig()
	c.Whitelist = []string{"8.8.8.8"}
	require.NoError(t, m.Save(context.Background(), c))
	// Complete the stale successful read; it must not overwrite Save.
	close(gate)
	require.Eventually(t, func() bool { m.mu.Lock(); defer m.mu.Unlock(); return !m.refreshing }, time.Second, time.Millisecond)
	current, err := m.Config(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"8.8.8.8/32"}, current.Whitelist)
}

func TestIPRiskSlowSaveDoesNotHoldRequestLock(t *testing.T) {
	gate := make(chan struct{})
	store := &delayedStore{memoryStore: memoryStore{c: DefaultConfig()}, saveGate: gate}
	m := New(store, nil, nil, false)
	defer m.Stop()
	saveDone := make(chan error, 1)
	go func() { saveDone <- m.Save(context.Background(), DefaultConfig()) }()
	reqDone := make(chan int, 1)
	go func() { reqDone <- riskRequest(riskRouter(t, m), "GET", "/", "8.8.8.8", "", "").Code }()
	select {
	case status := <-reqDone:
		require.Equal(t, 200, status)
	case <-time.After(500 * time.Millisecond):
		t.Error("request waits for config save")
	}
	close(gate)
	require.NoError(t, <-saveDone)
}

func TestIPRiskIdleInstanceRefreshesAndLocalSwitchIsImmediate(t *testing.T) {
	m, _, client := testManager(t, nil)
	defer m.Stop()
	other := New(m.store, client, countryStub("US"), false)
	other.startPolling(10 * time.Millisecond)
	defer other.Stop()
	cfg, err := m.Config(context.Background())
	require.NoError(t, err)
	cfg.AllowedCountries = []string{"CN"}
	require.NoError(t, m.Save(context.Background(), cfg))
	require.Equal(t, 403, riskRequest(riskRouter(t, m), "POST", "/v1/responses", "8.8.8.8", "", "").Code)
	// No requests to the idle instance while waiting for its background refresh.
	require.Eventually(t, func() bool {
		other.mu.Lock()
		defer other.mu.Unlock()
		return len(other.cached.config.AllowedCountries) == 1
	}, time.Second, time.Millisecond)
	require.Equal(t, 403, riskRequest(riskRouter(t, other), "POST", "/v1/responses", "8.8.8.8", "", "").Code)
	cfg.Enabled = false
	require.NoError(t, m.Save(context.Background(), cfg))
	require.Equal(t, 200, riskRequest(riskRouter(t, m), "POST", "/api/v1/auth/login", "8.8.8.8", "python-requests", "").Code)
	require.Eventually(t, func() bool { other.mu.Lock(); defer other.mu.Unlock(); return !other.cached.config.Enabled }, time.Second, time.Millisecond)
	require.Equal(t, 200, riskRequest(riskRouter(t, other), "POST", "/v1/responses", "8.8.8.8", "", "").Code)
	other.Stop() // Repeated stops are safe.
}

func TestIPRiskNaturalBanExpiryResetsTriggerCounter(t *testing.T) {
	m, server, _ := testManager(t, func(c *Config) { c.LoginFailures = Threshold{2, 900}; c.BanSeconds = 60 })
	defer m.Stop()
	r := riskRouter(t, m)
	for i := 0; i < 2; i++ {
		require.Equal(t, 401, riskRequest(r, "POST", "/api/v1/auth/login", "8.8.8.8", "Mozilla", "bad_credentials").Code)
	}
	require.Equal(t, 429, riskRequest(r, "GET", "/", "8.8.8.8", "", "").Code)
	server.FastForward(61 * time.Second)
	require.Equal(t, 401, riskRequest(r, "POST", "/api/v1/auth/login", "8.8.8.8", "Mozilla", "bad_credentials").Code)
	require.Equal(t, 200, riskRequest(r, "GET", "/", "8.8.8.8", "", "").Code)
}

func TestIPRiskRedisBudgetAndCancellationIsolation(t *testing.T) {
	m, _, _ := testManager(t, nil)
	defer m.Stop()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := m.activeBan(cancelled, "8.8.8.8/32", false)
	require.Error(t, err)
	require.True(t, m.redisRetryAfter.IsZero())
	require.Equal(t, 200, riskRequest(riskRouter(t, m), "POST", "/api/v1/auth/login", "8.8.8.8", "Mozilla", "").Code)
	// A local socket consumes commands without responding, simulating a black hole.
	blackhole := redis.NewClient(&redis.Options{Addr: "isolated", Protocol: 2, DisableIdentity: true, MaxRetries: -1, ContextTimeoutEnabled: true,
		Dialer: func(context.Context, string, string) (net.Conn, error) {
			a, b := net.Pipe()
			t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
			go func() { _, _ = io.Copy(io.Discard, b) }()
			return a, nil
		},
	})
	defer func() { _ = blackhole.Close() }()
	m.redis = blackhole
	started := time.Now()
	_, err = m.activeBan(context.Background(), "8.8.8.8/32", false)
	require.Error(t, err)
	require.Less(t, time.Since(started), time.Second)
	require.True(t, time.Now().Before(m.redisRetryAfter))
	require.Error(t, m.riskRedis(context.Background(), func(context.Context) error { t.Error("backoff must skip Redis IO"); return nil }))
}

func TestIPRiskEmergencyOverridePreservesSavedConfiguration(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.ObserveOnly = false
	cfg.AllowedCountries = []string{"CN"}
	cfg.Blacklist = []Rule{{Network: "8.8.8.8/32", Enabled: true}}
	m := New(&memoryStore{c: cfg}, nil, countryStub("US"), true)
	defer m.Stop()
	current, err := m.Config(context.Background())
	require.NoError(t, err)
	require.True(t, current.Enabled)
	require.Equal(t, []string{"CN"}, current.AllowedCountries)
	require.Equal(t, cfg.Blacklist, current.Blacklist)
	require.Equal(t, 200, riskRequest(riskRouter(t, m), "POST", "/api/v1/auth/login", "8.8.8.8", "python-requests", "").Code)
}
