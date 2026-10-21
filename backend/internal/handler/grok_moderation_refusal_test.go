//go:build unit

package handler

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/testutil"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type grokRefusalUpstream struct {
	service.HTTPUpstream
	body  string
	calls int
}

func TestGrokModerationBlockedSessionRejectsWebSocketBeforeUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	harness := newOpenAIWSPassthroughHandlerHarness(t, "http://127.0.0.1:1")
	payload := []byte(`{"type":"response.create","model":"gpt-5.1","prompt_cache_key":"refused-conversation","input":"hello"}`)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	hash := service.GrokModerationSessionHash(c, harness.apiKey, payload)
	require.NotEmpty(t, hash)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.NoError(t, harness.gatewayCache.BlockSession(ctx, 0, hash, 0))
	require.NoError(t, harness.clientConn.Write(ctx, coderws.MessageText, payload))
	_, _, err := harness.clientConn.Read(ctx)
	require.Equal(t, coderws.StatusPolicyViolation, coderws.CloseStatus(err), "must block before dialing the deliberately unreachable upstream")
	select {
	case <-harness.handlerDone:
	case <-ctx.Done():
		t.Fatal("blocked websocket handler did not finish")
	}
}

func TestGrokModerationTranscriptBlockPersistsInRedis(t *testing.T) {
	cache := testutil.NewRedisGatewayCache(t)
	svc := service.NewOpenAIGatewayService(nil, nil, nil, nil, nil, nil, cache, nil, nil, nil,
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	key := &service.APIKey{UserID: 77}
	body := []byte(`{"model":"grok-4.7","messages":[{"role":"user","content":"hello"},{"role":"assistant","content":"history A"},{"role":"user","content":"refused turn"}]}`)
	next := []byte(`{"model":"grok-4.6","messages":[{"role":"user","content":"hello"},{"role":"assistant","content":"history A"},{"role":"user","content":"refused turn"},{"role":"user","content":"continue"}]}`)
	other := []byte(`{"model":"grok-4.6","messages":[{"role":"user","content":"hello"},{"role":"assistant","content":"history B"},{"role":"user","content":"ordinary request"}]}`)
	ctx := context.Background()
	require.False(t, svc.GrokModerationRefusedRequestBlocked(ctx, c, key, body))
	svc.BlockGrokModerationRefusedRequest(ctx, c, key, body)
	require.True(t, svc.GrokModerationRefusedRequestBlocked(ctx, c, key, body))
	require.True(t, svc.GrokModerationRefusedRequestBlocked(ctx, c, key, next))
	require.False(t, svc.GrokModerationRefusedRequestBlocked(ctx, c, key, other))
	require.False(t, svc.GrokModerationRefusedRequestBlocked(ctx, c, &service.APIKey{UserID: 78}, next))
}

func (s *grokRefusalUpstream) Do(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	s.calls++
	return &http.Response{StatusCode: http.StatusForbidden, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(s.body))}, nil
}

type grokRefusalCache struct {
	testutil.StubGatewayCache
	blocked map[string]bool
}

func (s *grokRefusalCache) BlockSession(_ context.Context, groupID int64, hash string, ttl time.Duration) error {
	s.blocked[strconv.FormatInt(groupID, 10)+":"+hash] = ttl >= 0
	return nil
}

func (s *grokRefusalCache) IsSessionBlocked(_ context.Context, groupID int64, hash string) (bool, error) {
	return s.blocked[strconv.FormatInt(groupID, 10)+":"+hash], nil
}

func TestGrokModerationRefusalBillsOnceAndBlocksBeforeUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, endpoint := range []string{"/v1/chat/completions", "/v1/responses", "/v1/messages"} {
		for _, refusal := range []string{
			`{"error":{"message":"I'm sorry, I can't help with that request. Failed check: SAFETY_CHECK_TYPE"}}`,
			`{"error":{"message":"I'm sorry. Failed check: SAFETY_CHECK_TYPE"}}`,
		} {
			for _, price := range []float64{0.1, 0.25, 0} {
				t.Run(endpoint+"/"+refusal+"/"+strconv.FormatFloat(price, 'f', 2, 64), func(t *testing.T) {
					groupID := int64(1234)
					cfg := &config.Config{RunMode: config.RunModeStandard}
					cfg.Default.RateMultiplier = 1
					cfg.Gateway.MaxAccountSwitches = 1
					account := service.Account{ID: 9910, Name: "fixture", Platform: service.PlatformGrok, Type: service.AccountTypeAPIKey,
						Status: service.StatusActive, Schedulable: true, GroupIDs: []int64{groupID},
						Credentials: map[string]any{"api_key": "fixture", "base_url": "https://api.example.test"}}
					accounts := &openAIWSFailoverHandlerAccountRepoStub{accounts: []service.Account{account}}
					logs, ledger := &geminiBridgeUsageRepo{}, &geminiBridgeBillingRepo{}
					upstream := &grokRefusalUpstream{body: refusal}
					cache := &grokRefusalCache{blocked: map[string]bool{}}
					admissionCfg := *cfg
					admissionCfg.RunMode = config.RunModeSimple // isolate admission; gateway billing remains standard
					billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, &admissionCfg, nil)
					t.Cleanup(billing.Stop)
					svc := service.NewOpenAIGatewayService(accounts, logs, ledger, nil, nil, nil, cache, cfg, nil, nil,
						service.NewBillingService(cfg, nil), nil, billing, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
					h := NewOpenAIGatewayHandler(svc, service.NewConcurrencyService(nil), billing,
						service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
					key := &service.APIKey{ID: 55, UserID: 77, GroupID: &groupID, User: &service.User{ID: 77, Balance: 100, Status: service.StatusActive},
						Group: &service.Group{ID: groupID, Platform: service.PlatformGrok, Status: service.StatusActive, Hydrated: true,
							CrossProtocolConversionEnabled: true, RateMultiplier: 0.07, GrokModerationRefusalPrice: &price}}
					call := func(endpoint, model string) *httptest.ResponseRecorder {
						body := `{"model":"` + model + `","stream":false,"input":"hello","messages":[{"role":"user","content":"hello"}],"max_tokens":128}`
						rec := httptest.NewRecorder()
						c, _ := gin.CreateTestContext(rec)
						c.Request = httptest.NewRequest(http.MethodPost, endpoint, strings.NewReader(body))
						c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.ClientRequestID, "refusal-fixture"))
						c.Request.Header.Set("Content-Type", "application/json")
						c.Request.Header.Set("Session_id", "refused-conversation")
						c.Set(string(middleware.ContextKeyAPIKey), key)
						c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 77})
						switch endpoint {
						case "/v1/chat/completions":
							h.ChatCompletions(c)
						case "/v1/responses":
							h.Responses(c)
						case "/v1/messages":
							h.Messages(c)
						}
						return rec
					}
					first := call(endpoint, "grok-4.7")
					require.Equal(t, 403, first.Code, first.Body.String())
					require.Equal(t, 1, upstream.calls, "refusal must not retry or switch accounts")
					require.NotNil(t, logs.last, "ordinary error must still write a usage record")
					require.NotNil(t, ledger.last)
					require.InDelta(t, price, ledger.last.BalanceCost, 1e-8, "fixed price must not use the group multiplier")
					if price > 0 {
						require.Equal(t, "per_request", *logs.last.BillingMode)
					}
					lastUsage, lastCommand := logs.last, ledger.last
					otherGroup := int64(1235)
					key.GroupID = &otherGroup
					for _, nextEndpoint := range []string{"/v1/chat/completions", "/v1/responses", "/v1/messages"} {
						second := call(nextEndpoint, "grok-4.6")
						require.Equal(t, 403, second.Code, second.Body.String())
						require.Equal(t, 1, upstream.calls, "blocked session must stay local across model, protocol and group changes")
						require.Same(t, lastUsage, logs.last, "local block must not create another bill")
						require.Same(t, lastCommand, ledger.last)
					}
				})
			}
		}
	}
}
