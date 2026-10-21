package handler

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository" //nolint:depguard // End-to-end fixture needs the real Redis block persistence implementation.
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9" //nolint:depguard // Verify the stored block's Redis TTL, not only the gateway interface result.
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Keep this harness independent of the HTTP refusal fixtures. Both transports
// enter the real handler, run BeforeUpstreamFailure and settle through AfterTurn.
type openAIWSFirstRefusalUsageRepo struct {
	service.UsageLogRepository
	mu   sync.Mutex
	logs []*service.UsageLog
}

func (r *openAIWSFirstRefusalUsageRepo) Create(_ context.Context, log *service.UsageLog) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = append(r.logs, log)
	return true, nil
}

func (r *openAIWSFirstRefusalUsageRepo) snapshot() []*service.UsageLog {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*service.UsageLog(nil), r.logs...)
}

type openAIWSFirstRefusalBillingRepo struct {
	service.UsageBillingRepository
	mu       sync.Mutex
	commands []*service.UsageBillingCommand
}

func (r *openAIWSFirstRefusalBillingRepo) Apply(_ context.Context, command *service.UsageBillingCommand) (*service.UsageBillingApplyResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commands = append(r.commands, command)
	return &service.UsageBillingApplyResult{Applied: true}, nil
}

func (r *openAIWSFirstRefusalBillingRepo) snapshot() []*service.UsageBillingCommand {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*service.UsageBillingCommand(nil), r.commands...)
}

type openAIWSFirstRefusalHTTPUpstream struct {
	service.HTTPUpstream
	client *http.Client
}

func (u *openAIWSFirstRefusalHTTPUpstream) Do(request *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return u.client.Do(request)
}

type openAIWSFirstRefusalHarness struct {
	url         string
	headers     http.Header
	handlerDone <-chan struct{}
	apiKey      *service.APIKey
	redisServer *miniredis.Miniredis
	redisClient *redis.Client
	usage       *openAIWSFirstRefusalUsageRepo
	billing     *openAIWSFirstRefusalBillingRepo
}

// Follow the passthrough cyber harness, with standard billing and an observable
// Redis instance so a temporary ban cannot satisfy the permanent-block check.
func newOpenAIWSFirstRefusalHarness(t *testing.T, platform, upstreamURL string, refusalPrice float64) *openAIWSFirstRefusalHarness {
	t.Helper()
	redisServer := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { require.NoError(t, redisClient.Close()) })
	groupID := int64(4301)
	account := service.Account{
		ID: 9951, Name: "ws-first-refusal-fixture", Platform: platform,
		Type: service.AccountTypeAPIKey, Status: service.StatusActive,
		Schedulable: true, Concurrency: 1, GroupIDs: []int64{groupID},
		Credentials: map[string]any{"api_key": "sk-test", "base_url": upstreamURL},
		Extra: map[string]any{
			"openai_apikey_responses_websockets_v2_enabled": true,
			"openai_apikey_responses_websockets_v2_mode":    service.OpenAIWSIngressModePassthrough,
		},
	}
	cfg := &config.Config{RunMode: config.RunModeStandard}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.MaxAccountSwitches = 1
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.APIKeyEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
	cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.IngressInterTurnIdleTimeoutSeconds = 3

	// Isolate admission from external balance stores; settlement stays standard.
	admissionCfg := *cfg
	admissionCfg.RunMode = config.RunModeSimple
	billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, &admissionCfg, nil)
	t.Cleanup(billingCache.Stop)
	usage := &openAIWSFirstRefusalUsageRepo{}
	billing := &openAIWSFirstRefusalBillingRepo{}
	upstream := &openAIWSFirstRefusalHTTPUpstream{client: &http.Client{Timeout: 3 * time.Second}}
	gateway := service.NewOpenAIGatewayService(
		&openAIWSUsageHandlerAccountRepoStub{account: account}, usage, billing,
		nil, nil, nil, repository.NewGatewayCache(redisClient), cfg, nil, nil,
		service.NewBillingService(cfg, nil), nil, billingCache, upstream, &service.DeferredService{},
		nil, nil, nil, nil, nil, nil, nil,
	)
	concurrencyCache := &concurrencyCacheMock{
		acquireUserSlotFn:    func(context.Context, int64, int, string) (bool, error) { return true, nil },
		acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil },
	}
	handler := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(concurrencyCache), billingCache,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
	apiKey := &service.APIKey{
		ID: 1851, UserID: 1751, Key: "sk-ws-first-refusal-test", GroupID: &groupID,
		User: &service.User{ID: 1751, Status: service.StatusActive, Balance: 100},
		Group: &service.Group{
			ID: groupID, Platform: platform, Status: service.StatusActive, Hydrated: true,
			CrossProtocolConversionEnabled: true, RateMultiplier: 0.07, GrokModerationRefusalPrice: &refusalPrice,
		},
	}
	handlerDone := make(chan struct{}, 2)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyAPIKey), apiKey)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: apiKey.User.ID, Concurrency: 1})
		c.Next()
	})
	router.GET("/openai/v1/responses", func(c *gin.Context) {
		defer func() { handlerDone <- struct{}{} }()
		handler.ResponsesWebSocket(c)
	})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return &openAIWSFirstRefusalHarness{
		url:         "ws" + strings.TrimPrefix(server.URL, "http") + "/openai/v1/responses",
		headers:     http.Header{"Session_id": {"ws-first-refusal-conversation"}},
		handlerDone: handlerDone, apiKey: apiKey, redisServer: redisServer, redisClient: redisClient,
		usage: usage, billing: billing,
	}
}

func (h *openAIWSFirstRefusalHarness) request(t *testing.T, payload []byte, bridgeErrorMessage string) coderws.CloseError {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := coderws.Dial(ctx, h.url, &coderws.DialOptions{HTTPHeader: h.headers})
	require.NoError(t, err)
	defer func() { _ = conn.CloseNow() }()
	require.NoError(t, conn.Write(ctx, coderws.MessageText, payload))
	// The HTTP bridge reports its upstream 403 before returning to the hook;
	// passthrough intercepts the refusal frame before writing to the client.
	_, frame, err := conn.Read(ctx)
	if bridgeErrorMessage != "" {
		require.NoError(t, err)
		require.Equal(t, "error", gjson.GetBytes(frame, "type").String())
		require.Equal(t, int64(http.StatusForbidden), gjson.GetBytes(frame, "status").Int())
		require.Equal(t, bridgeErrorMessage, gjson.GetBytes(frame, "error.message").String())
		_, frame, err = conn.Read(ctx)
	}
	require.Error(t, err, "refusal must close the client, got frame %s", frame)
	var closeErr coderws.CloseError
	require.ErrorAs(t, err, &closeErr)
	require.Equal(t, coderws.StatusPolicyViolation, closeErr.Code)
	select {
	case <-h.handlerDone:
	case <-ctx.Done():
		t.Fatal("first-refusal websocket handler did not finish")
	}
	return closeErr
}

func TestOpenAIResponsesWebSocketFirstUpstreamRefusal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, fixture := range []struct {
		name, platform, model, refusal, closeReason string
		price                                       float64
		wantBills                                   int
	}{
		{
			name: "grok_http_bridge_full_refusal", platform: service.PlatformGrok, model: "grok-4.7",
			refusal:     `{"error":{"message":"I'm sorry, I can't help with that request. Failed check: SAFETY_CHECK_TYPE"}}`,
			closeReason: service.GrokModerationRefusalMessage(), price: 0.1, wantBills: 1,
		},
		{
			name: "grok_http_bridge_short_refusal_custom_price", platform: service.PlatformGrok, model: "grok-4.7",
			refusal:     `{"error":{"message":"I'm sorry. Failed check: SAFETY_CHECK_TYPE"}}`,
			closeReason: service.GrokModerationRefusalMessage(), price: 0.25, wantBills: 1,
		},
		{
			name: "openai_passthrough_blacklist_error", platform: service.PlatformOpenAI, model: "gpt-5.1",
			refusal:     `{"type":"error","error":{"message":"blocked by global ip blacklist"}}`,
			closeReason: service.GlobalIPBlacklistSessionBlockedMessage(), price: 0.25,
		},
		{
			name: "openai_passthrough_blacklist_response_failed", platform: service.PlatformOpenAI, model: "gpt-5.1",
			refusal:     `{"type":"response.failed","response":{"id":"resp_blacklisted","model":"gpt-5.1","error":{"message":"blocked by global ip blacklist"},"usage":{"input_tokens":1100,"output_tokens":30}}}`,
			closeReason: service.GlobalIPBlacklistSessionBlockedMessage(), price: 0.25,
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			var upstreamCalls atomic.Int32
			upstreamBodies := make(chan []byte, 2)
			upstreamErrors := make(chan error, 4)
			upstreamDone := make(chan struct{}, 2)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				upstreamCalls.Add(1)
				defer func() { upstreamDone <- struct{}{} }()
				if fixture.platform == service.PlatformGrok {
					body, err := io.ReadAll(r.Body)
					upstreamErrors <- err
					upstreamBodies <- body
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusForbidden)
					_, err = io.WriteString(w, fixture.refusal)
					upstreamErrors <- err
					return
				}
				conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{CompressionMode: coderws.CompressionContextTakeover})
				upstreamErrors <- err
				if err != nil {
					return
				}
				defer func() { _ = conn.CloseNow() }()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_, body, err := conn.Read(ctx)
				upstreamErrors <- err
				if err != nil {
					return
				}
				upstreamBodies <- body
				upstreamErrors <- conn.Write(ctx, coderws.MessageText, []byte(fixture.refusal))
				// Keep the upstream alive until the gateway closes it, as in the
				// passthrough cyber harness; an EOF must not masquerade as refusal.
				_, followup, err := conn.Read(ctx)
				if err == nil {
					upstreamBodies <- followup
				}
			}))
			t.Cleanup(upstream.Close)
			harness := newOpenAIWSFirstRefusalHarness(t, fixture.platform, upstream.URL, fixture.price)
			payload := []byte(`{"type":"response.create","model":"` + fixture.model + `","input":"hello"}`)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodGet, "/openai/v1/responses", nil)
			c.Request.Header = harness.headers.Clone()
			hash := service.GrokModerationSessionHash(c, harness.apiKey, payload)
			require.NotEmpty(t, hash)
			blockKey := "blocked:sess:0:" + hash
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			require.False(t, harness.redisServer.Exists(blockKey), "first request must not start pre-blocked")

			bridgeErrorMessage := ""
			if fixture.platform == service.PlatformGrok {
				bridgeErrorMessage = gjson.Get(fixture.refusal, "error.message").String()
			}
			closeErr := harness.request(t, payload, bridgeErrorMessage)
			require.Equal(t, fixture.closeReason, closeErr.Reason)
			require.EqualValues(t, 1, upstreamCalls.Load(), "the first rejection must reach upstream once without retry/failover")
			select {
			case <-upstreamDone:
			case <-ctx.Done():
				t.Fatal("refused upstream did not finish")
			}
			for len(upstreamErrors) > 0 {
				require.NoError(t, <-upstreamErrors)
			}
			require.Len(t, upstreamBodies, 1, "only the first response.create may reach upstream")
			body := <-upstreamBodies
			require.Equal(t, fixture.model, gjson.GetBytes(body, "model").String())
			require.Contains(t, string(body), "hello")

			assertBilling := func() {
				t.Helper()
				logs, commands := harness.usage.snapshot(), harness.billing.snapshot()
				require.Len(t, logs, fixture.wantBills, "only a Grok content refusal may create a usage record")
				require.Len(t, commands, fixture.wantBills, "blacklist and blocked reconnects must never debit the balance")
				if fixture.wantBills == 0 {
					return
				}
				require.InDelta(t, fixture.price, commands[0].BalanceCost, 1e-8, "refusal price must not use the 0.07 group multiplier")
				require.InDelta(t, fixture.price, logs[0].ActualCost, 1e-8)
				require.InDelta(t, fixture.price, logs[0].TotalCost, 1e-8)
				require.NotNil(t, logs[0].BillingMode)
				require.Equal(t, "per_request", *logs[0].BillingMode)
				require.True(t, logs[0].OpenAIWSMode)
				require.True(t, logs[0].Stream)
			}
			assertBilling()
			require.True(t, harness.redisServer.Exists(blockKey), "first refusal must persist its block through BeforeUpstreamFailure")
			ttl, err := harness.redisClient.TTL(ctx, blockKey).Result()
			require.NoError(t, err)
			require.Equal(t, time.Duration(-1), ttl, "Redis block must have no expiration")
			harness.redisServer.FastForward(365 * 24 * time.Hour)
			require.True(t, harness.redisServer.Exists(blockKey), "block must survive long after ordinary session TTLs")

			// A fresh socket must consult Redis before selecting/dialing upstream.
			blockedClose := harness.request(t, payload, "")
			require.Equal(t, service.GrokModerationRefusalMessage(), blockedClose.Reason)
			require.EqualValues(t, 1, upstreamCalls.Load(), "blocked reconnect must not leave the gateway")
			assertBilling()
		})
	}
}
