//go:build unit

package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/repository" //nolint:depguard // End-to-end fixture needs the real HTTPUpstream to verify 400 body peeking + circuit breaker wiring.
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/testutil"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type responsesCircuitRepo struct {
	*stormRepo
}

func (r *responsesCircuitRepo) ListSchedulableByGroupIDAndPlatforms(_ context.Context, _ int64, platforms []string) ([]service.Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []service.Account
	for _, account := range r.accounts {
		if !account.IsSchedulable() {
			continue
		}
		for _, p := range platforms {
			if account.Platform == p {
				out = append(out, account)
				break
			}
		}
	}
	return out, nil
}

func TestGatewayHandlerResponsesPropagatesAccountIdentityAndPlatformToCircuitBreaker(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgDisabledBody := `{"type":"error","error":{"type":"invalid_request_error","message":"This organization has been disabled."}}`
	normal400Body := `{"type":"error","error":{"type":"invalid_request_error","message":"messages: roles must alternate"}}`

	newFixture := func(
		t *testing.T,
		groupPlatform string,
		account *service.Account,
		upstreamHandler http.HandlerFunc,
	) (*GatewayHandler, *service.APIKey, *service.Group, *service.AccountCircuitBreaker, *[]service.AccountCircuitTrip, *sync.Mutex) {
		t.Helper()
		srv := httptest.NewServer(upstreamHandler)
		t.Cleanup(srv.Close)
		account.Credentials["base_url"] = srv.URL

		groupID := int64(9200)
		group := &service.Group{
			ID:                             groupID,
			Hydrated:                       true,
			Platform:                       groupPlatform,
			Status:                         service.StatusActive,
			CrossProtocolConversionEnabled: true,
			AllowMessagesDispatch:          true,
		}
		account.AccountGroups = []service.AccountGroup{{AccountID: account.ID, GroupID: groupID}}

		cfg := &config.Config{RunMode: config.RunModeSimple}
		cfg.Security.URLAllowlist.AllowInsecureHTTP = true
		cfg.Security.URLAllowlist.AllowPrivateHosts = true
		cfg.Gateway.MaxAccountSwitches = 1
		cfg.Gateway.AccountCircuitBreaker = config.AccountCircuitBreakerConfig{
			Enabled:          true,
			WindowSeconds:    300,
			ErrorRate:        0.95,
			MinRequests:      20,
			CooldownSeconds:  60,
			ValveMinAccounts: 3,
		}

		up := repository.NewHTTPUpstream(nil)
		var tripsMu sync.Mutex
		var trips []service.AccountCircuitTrip
		breaker := service.NewAccountCircuitBreaker(service.AccountCircuitBreakerSettingsFromConfig(cfg), func(_ context.Context, trip service.AccountCircuitTrip) error {
			tripsMu.Lock()
			trips = append(trips, trip)
			tripsMu.Unlock()
			return nil
		})
		breaker.RunPersistSynchronouslyForTest()
		observable, ok := up.(service.UpstreamAttemptObservable)
		require.True(t, ok)
		observable.SetUpstreamAttemptObserver(breaker)

		repo := &responsesCircuitRepo{&stormRepo{&grokCredentialHandlerRepo{
			accounts:     []service.Account{*account},
			missingOnGet: map[int64]bool{},
		}}}
		rateLimit := service.NewRateLimitService(repo, nil, cfg, nil, nil)
		rateLimit.SetAccountCircuitBreaker(breaker)

		billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
		t.Cleanup(billingCache.Stop)
		billingSvc := service.NewBillingService(cfg, nil)
		concurrencySvc := service.NewConcurrencyService(&fakeConcurrencyCache{})

		gatewaySvc := service.NewGatewayService(
			repo, &fakeGroupRepo{group: group}, nil, nil, nil, nil, nil, testutil.StubGatewayCache{}, cfg,
			nil, concurrencySvc, billingSvc, rateLimit, billingCache, nil, up,
			&service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
		)
		openAISvc := service.NewOpenAIGatewayService(
			repo, nil, nil, nil, nil, nil, testutil.StubGatewayCache{}, cfg, nil, concurrencySvc,
			billingSvc, rateLimit, billingCache, up, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil,
		)

		h := &GatewayHandler{
			gatewayService:       gatewaySvc,
			openAIGatewayService: openAISvc,
			billingCacheService:  billingCache,
			concurrencyHelper:    NewConcurrencyHelper(concurrencySvc, SSEPingFormatClaude, 0),
			maxAccountSwitches:   1,
			cfg:                  cfg,
		}
		apiKey := &service.APIKey{
			ID: 9202, UserID: 9203, GroupID: &groupID, Group: group, Status: service.StatusActive,
			User: &service.User{ID: 9203, Concurrency: 100, Balance: 1000, Status: service.StatusActive},
		}
		return h, apiKey, group, breaker, &trips, &tripsMu
	}

	send := func(call func(*gin.Context), apiKey *service.APIKey, group *service.Group, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		ctx := context.WithValue(context.Background(), ctxkey.UserID, apiKey.UserID)
		ctx = context.WithValue(ctx, ctxkey.Group, group)
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body)).WithContext(ctx)
		req.Header.Set("Content-Type", "application/json")
		c.Request = req
		c.Set(string(middleware.ContextKeyAPIKey), apiKey)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: apiKey.UserID, Concurrency: 100})
		call(c)
		return rec
	}

	t.Run("anthropic /v1/responses 503 + 400 org disabled trips with anthropic platform and account name", func(t *testing.T) {
		var seq atomic.Int64
		account := &service.Account{
			ID: 9211, Name: "claude-relay-1", Platform: service.PlatformAnthropic, Type: service.AccountTypeAPIKey,
			Status: service.StatusActive, Schedulable: true, Concurrency: 50,
			Credentials: map[string]any{
				"api_key":   "sk-ant-test",
				"pool_mode": true,
			},
		}
		h, apiKey, group, breaker, trips, tripsMu := newFixture(t, service.PlatformAnthropic, account, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if seq.Add(1)%2 == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"type":"error","error":{"type":"api_error","message":"overloaded"}}`))
				return
			}
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(orgDisabledBody))
		})

		var saw400Body string
		for i := 0; i < 20 && !breaker.IsOpen(account.ID); i++ {
			rec := send(h.Responses, apiKey, group, "/v1/responses", `{"model":"claude-sonnet-4-5","input":"hello","stream":false}`)
			if rec.Code == http.StatusBadRequest {
				saw400Body = rec.Body.String()
			}
		}

		require.True(t, breaker.IsOpen(account.ID), "mixed 503 + 400 org-disabled through /v1/responses must trip breaker")
		require.Contains(t, saw400Body, "organization has been disabled", "peeked 400 body must remain intact for downstream error handling")
		tripsMu.Lock()
		defer tripsMu.Unlock()
		require.Len(t, *trips, 1)
		require.Equal(t, service.PlatformAnthropic, (*trips)[0].Platform, "selected account platform must propagate into ForwardAsResponses context")
		require.Equal(t, "claude-relay-1", (*trips)[0].AccountName, "selected account name must propagate into ForwardAsResponses context")
		require.Equal(t, 20, (*trips)[0].Attempts)
		require.Equal(t, 20, (*trips)[0].Failures)
	})

	t.Run("non-anthropic gemini-openai account on /v1/responses 400 with org-disabled text stays attemptOK and does not trip", func(t *testing.T) {
		account := &service.Account{
			ID: 9212, Name: "gemini-openai-1", Platform: service.PlatformGemini, Type: service.AccountTypeAPIKey,
			Status: service.StatusActive, Schedulable: true, Concurrency: 50,
			Credentials: map[string]any{
				"api_key":      "sk-gem-test",
				"api_protocol": service.APIProtocolResponses,
				"pool_mode":    true,
			},
		}
		h, apiKey, group, breaker, trips, tripsMu := newFixture(t, service.PlatformGemini, account, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(orgDisabledBody))
		})

		for i := 0; i < 25; i++ {
			rec := send(h.Responses, apiKey, group, "/v1/responses", `{"model":"gemini-2.5-pro","input":"hello","stream":false}`)
			require.Equal(t, http.StatusBadRequest, rec.Code)
		}

		require.False(t, breaker.IsOpen(account.ID), "non-Anthropic 400 must not be refined as Anthropic org-disabled via stale requestCtx")
		stats := breaker.Stats()
		require.Equal(t, int64(25), stats.Observed)
		require.Zero(t, stats.Failures)
		tripsMu.Lock()
		defer tripsMu.Unlock()
		require.Empty(t, *trips)
	})

	t.Run("anthropic /v1/messages normal 400 stays attemptOK while 400 org disabled trips", func(t *testing.T) {
		normalAcct := &service.Account{
			ID: 9213, Name: "claude-normal-400", Platform: service.PlatformAnthropic, Type: service.AccountTypeAPIKey,
			Status: service.StatusActive, Schedulable: true, Concurrency: 50,
			Credentials: map[string]any{"api_key": "sk-ant-test", "pool_mode": true},
		}
		hNormal, keyNormal, grpNormal, breakerNormal, _, _ := newFixture(t, service.PlatformAnthropic, normalAcct, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(normal400Body))
		})
		for i := 0; i < 25; i++ {
			rec := send(hNormal.Messages, keyNormal, grpNormal, "/v1/messages", `{"model":"claude-sonnet-4-5","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`)
			require.Equal(t, http.StatusBadRequest, rec.Code)
		}
		require.False(t, breakerNormal.IsOpen(normalAcct.ID))
		require.Equal(t, int64(25), breakerNormal.Stats().Observed)
		require.Zero(t, breakerNormal.Stats().Failures)

		disabledAcct := &service.Account{
			ID: 9214, Name: "claude-disabled-400", Platform: service.PlatformAnthropic, Type: service.AccountTypeAPIKey,
			Status: service.StatusActive, Schedulable: true, Concurrency: 50,
			Credentials: map[string]any{"api_key": "sk-ant-test", "pool_mode": true},
		}
		hDisabled, keyDisabled, grpDisabled, breakerDisabled, tripsDisabled, tripsMu := newFixture(t, service.PlatformAnthropic, disabledAcct, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(orgDisabledBody))
		})
		for i := 0; i < 20; i++ {
			rec := send(hDisabled.Messages, keyDisabled, grpDisabled, "/v1/messages", `{"model":"claude-sonnet-4-5","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`)
			require.Equal(t, http.StatusBadRequest, rec.Code)
		}
		require.True(t, breakerDisabled.IsOpen(disabledAcct.ID))
		tripsMu.Lock()
		defer tripsMu.Unlock()
		require.Len(t, *tripsDisabled, 1)
		require.Equal(t, service.PlatformAnthropic, (*tripsDisabled)[0].Platform)
		require.Equal(t, "claude-disabled-400", (*tripsDisabled)[0].AccountName)
	})
}
