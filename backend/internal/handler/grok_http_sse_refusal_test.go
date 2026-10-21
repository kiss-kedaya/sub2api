//go:build unit

package handler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type grokHTTPProtocolRefusalUpstream struct {
	service.HTTPUpstream
	body  string
	calls int
}

type grokHTTPProtocolBillingRepo struct {
	geminiBridgeBillingRepo
	calls int
}

func (r *grokHTTPProtocolBillingRepo) Apply(ctx context.Context, cmd *service.UsageBillingCommand) (*service.UsageBillingApplyResult, error) {
	r.calls++
	return r.geminiBridgeBillingRepo.Apply(ctx, cmd)
}

func (s *grokHTTPProtocolRefusalUpstream) Do(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	s.calls++
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(s.body))}, nil
}

func TestGrokHTTPProtocolRefusalBillsOnceAndBlocksConversation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const contentRefusal = `{"type":"response.failed","response":{"id":"resp_refused","status":"failed","error":{"status_code":403,"type":"upstream_error","message":"I'm sorry, I can't help with that request."},"usage":{"input_tokens":8,"output_tokens":1}}}`
	for _, fixture := range []struct {
		name, refusal, eventType string
		rawChat                  bool
	}{
		{"bridge_content", contentRefusal, "response.failed", false},
		{"raw_content", contentRefusal, "response.failed", true},
		{"raw_ip_blacklist", `{"type":"error","error":{"status_code":403,"type":"upstream_error","message":"blocked by global ip blacklist"}}`, "error", true},
	} {
		refusal := fixture.refusal
		for _, endpoint := range []string{"/v1/responses", "/v1/chat/completions", "/v1/messages"} {
			if fixture.rawChat && endpoint != "/v1/chat/completions" {
				continue
			}
			for _, stream := range []bool{false, true} {
				for _, afterOutput := range []bool{false, true} {
					if afterOutput && (!stream || endpoint == "/v1/responses") {
						continue
					}
					t.Run(fmt.Sprintf("%s/%s/stream=%t/afterOutput=%t", fixture.name, endpoint, stream, afterOutput), func(t *testing.T) {
						groupID := int64(1234)
						price := 0.25
						cfg := &config.Config{RunMode: config.RunModeStandard}
						cfg.Default.RateMultiplier = 1
						cfg.Gateway.MaxAccountSwitches = 1
						account := service.Account{ID: 9910, Name: "fixture", Platform: service.PlatformGrok, Type: service.AccountTypeOAuth,
							Status: service.StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{groupID},
							Credentials: map[string]any{"access_token": "fixture", "refresh_token": "fixture-refresh", "expires_at": time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339), "base_url": "https://api.example.test"},
							Extra:       map[string]any{service.GrokMediaEligibleExtraKey: true}}
						accounts := &openAIWSFailoverHandlerAccountRepoStub{accounts: []service.Account{account}}
						logs, ledger := &geminiBridgeUsageRepo{}, &grokHTTPProtocolBillingRepo{}
						body := "event: " + fixture.eventType + "\ndata: " + refusal + "\n\n"
						if afterOutput {
							if fixture.rawChat {
								body = "data: {\"id\":\"chatcmpl_partial\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hello\"},\"finish_reason\":null}]}\n\n" + body
							} else {
								body = "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_refused\",\"status\":\"in_progress\"}}\n\n" +
									"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n" + body
							}
						}
						upstream := &grokHTTPProtocolRefusalUpstream{body: body}
						cache := &grokRefusalCache{blocked: map[string]bool{}}
						admissionCfg := *cfg
						admissionCfg.RunMode = config.RunModeSimple
						billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, &admissionCfg, nil)
						t.Cleanup(billing.Stop)
						provider := service.NewGrokTokenProvider(accounts, nil)
						_, credentialErr := provider.GetAccessToken(context.Background(), &account)
						require.NoError(t, credentialErr)
						svc := service.NewOpenAIGatewayService(accounts, logs, ledger, nil, nil, nil, cache, cfg, nil, nil,
							service.NewBillingService(cfg, nil), nil, billing, upstream, &service.DeferredService{}, nil, provider, nil, nil, nil, nil, nil)
						h := NewOpenAIGatewayHandler(svc, service.NewConcurrencyService(nil), billing,
							service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
						key := &service.APIKey{ID: 55, UserID: 77, GroupID: &groupID, User: &service.User{ID: 77, Balance: 100, Status: service.StatusActive},
							Group: &service.Group{ID: groupID, Platform: service.PlatformGrok, Status: service.StatusActive, Hydrated: true,
								CrossProtocolConversionEnabled: true, AllowMessagesDispatch: true, RateMultiplier: 0.07, GrokModerationRefusalPrice: &price}}
						call := func() *httptest.ResponseRecorder {
							model := "grok-4.6"
							if fixture.rawChat {
								model = "grok-4.7"
							}
							payload := fmt.Sprintf(`{"model":"%s","stream":%t,"messages":[{"role":"user","content":"hello"}],"max_tokens":128,"prompt_cache_key":"fixture-cache"}`, model, stream)
							if endpoint == "/v1/responses" {
								payload = fmt.Sprintf(`{"model":"grok-4.6","stream":%t,"input":"hello"}`, stream)
							}
							rec := httptest.NewRecorder()
							c, _ := gin.CreateTestContext(rec)
							c.Request = httptest.NewRequest(http.MethodPost, endpoint, strings.NewReader(payload))
							c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.ClientRequestID, "sse-refusal-fixture"))
							c.Request.Header.Set("Content-Type", "application/json")
							c.Request.Header.Set("Session_id", "refused-conversation")
							c.Set(string(middleware.ContextKeyAPIKey), key)
							c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 77})
							switch endpoint {
							case "/v1/chat/completions":
								h.ChatCompletions(c)
							case "/v1/messages":
								h.Messages(c)
							default:
								h.Responses(c)
							}
							return rec
						}
						first := call()
						if afterOutput {
							require.Equal(t, http.StatusOK, first.Code, first.Body.String())
							require.Contains(t, first.Body.String(), "hello")
							require.True(t, strings.HasSuffix(first.Body.String(), "event: "+fixture.eventType+"\ndata: "+refusal+"\n\n"), first.Body.String())
						} else if stream && (endpoint == "/v1/responses" || fixture.rawChat) {
							require.Equal(t, http.StatusOK, first.Code, first.Body.String())
							require.Equal(t, body, first.Body.String())
						} else {
							require.Equal(t, http.StatusForbidden, first.Code, first.Body.String())
							require.Equal(t, refusal, first.Body.String())
						}
						require.Equal(t, 1, upstream.calls)
						require.NotNil(t, logs.last)
						require.NotNil(t, ledger.last)
						require.Equal(t, 1, ledger.calls, "the actual refusal must settle exactly once")
						require.InDelta(t, price, ledger.last.BalanceCost, 1e-8, "the configured refusal price must ignore the group multiplier")
						require.InDelta(t, price, logs.last.ActualCost, 1e-8)
						require.Equal(t, "per_request", *logs.last.BillingMode)
						lastUsage, lastCommand := logs.last, ledger.last
						second := call()
						require.Equal(t, http.StatusForbidden, second.Code)
						require.Equal(t, 1, upstream.calls, "the permanent session block must reject reconnects before the upstream")
						require.Same(t, lastUsage, logs.last)
						require.Same(t, lastCommand, ledger.last)
						require.Equal(t, 1, ledger.calls, "local reconnect blocks must never settle again")
					})
				}
			}
		}
	}
}
