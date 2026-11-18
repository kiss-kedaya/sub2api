//go:build unit

package handler

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/testutil"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type geminiGaps3HandlerUpstream struct {
	service.HTTPUpstream
	calls atomic.Int64
	body  string
}

func (u *geminiGaps3HandlerUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.calls.Add(1)
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(u.body)), Request: req}, nil
}

func TestGeminiGaps3HandlersSettleOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, native := range []bool{false, true} {
		for _, mode := range []string{"stream", "buffered", "oauth-collected"} {
			for _, recovered := range []bool{false, true} {
				t.Run(fmt.Sprintf("native=%t/%s/recovered=%t", native, mode, recovered), func(t *testing.T) {
					group := &service.Group{ID: 9800, Hydrated: true, Platform: service.PlatformGemini, Status: service.StatusActive, RateMultiplier: 1}
					account := &service.Account{ID: 9801, Platform: service.PlatformGemini, Type: service.AccountTypeAPIKey,
						Status: service.StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{group.ID},
						AccountGroups: []service.AccountGroup{{AccountID: 9801, GroupID: group.ID}},
						Credentials:   map[string]any{"api_key": "synthetic-key", "base_url": "https://example.invalid", "api_protocol": "gemini"}}
					if mode == "oauth-collected" {
						account.Type = service.AccountTypeOAuth
						account.Credentials = map[string]any{"access_token": "synthetic-token", "project_id": "synthetic-project"}
					}
					cfg := &config.Config{RunMode: config.RunModeStandard}
					cfg.Default.RateMultiplier = 1
					cfg.Gateway.MaxLineSize = 1024 * 1024
					cache := &testutil.StubGatewayCache{}
					snapshot := service.NewSchedulerSnapshotService(&fakeSchedulerCache{accounts: []*service.Account{account}}, nil, nil, nil, nil)
					logs, billing := &passthroughFailureHandlerUsage{}, &passthroughFailureHandlerBilling{}
					upstream := &geminiGaps3HandlerUpstream{}
					usage := `"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":2}`
					answer := `{"candidates":[{"content":{"parts":[{"text":"complete answer"}]},"finishReason":"STOP"}],` + usage + `}`
					failure := `{"error":{"code":503,"status":"UNAVAILABLE","message":"synthetic failure"},` + usage + `}`
					if mode == "buffered" {
						upstream.body = failure
						if recovered {
							upstream.body = answer
						}
					} else {
						upstream.body = "event: error\ndata: {\"code\":503,\"status\":\"UNAVAILABLE\",\"message\":\"synthetic failure\"," + usage + "}\n\n"
						if recovered {
							if mode == "oauth-collected" {
								answer = `{"response":` + answer + `}`
							}
							upstream.body += "data: " + answer + "\n\n"
						}
					}
					billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, &config.Config{RunMode: config.RunModeSimple}, nil)
					t.Cleanup(billingCache.Stop)
					gateway := service.NewGatewayService(nil, &fakeGroupRepo{group: group}, logs, billing, nil, nil, nil, cache, cfg,
						snapshot, nil, service.NewBillingService(cfg, nil), nil, billingCache, nil, upstream, &service.DeferredService{},
						nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
					compat := service.NewGeminiMessagesCompatService(nil, nil, cache, snapshot, &service.GeminiTokenProvider{}, nil, upstream, nil, cfg)
					h := &GatewayHandler{gatewayService: gateway, geminiCompatService: compat, billingCacheService: billingCache, cfg: cfg,
						concurrencyHelper: NewConcurrencyHelper(service.NewConcurrencyService(&fakeConcurrencyCache{}), SSEPingFormatClaude, 0), maxAccountSwitches: 1, maxAccountSwitchesGemini: 1}
					key := &service.APIKey{ID: 9802, UserID: 9803, GroupID: &group.ID, Group: group, Status: service.StatusActive,
						User: &service.User{ID: 9803, Status: service.StatusActive, Balance: 100, Concurrency: 10}}
					stream := mode == "stream"
					requestBody := fmt.Sprintf(`{"model":"gemini-3.7-flash","stream":%t,"max_tokens":64,"messages":[{"role":"user","content":"synthetic check"}]}`, stream)
					path := "/v1/messages"
					action := "generateContent"
					if stream {
						action = "streamGenerateContent"
					}
					if native {
						requestBody = `{"contents":[{"role":"user","parts":[{"text":"synthetic check"}]}]}`
						path = "/v1beta/models/gemini-3.7-flash:" + action
					}
					recorder := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(recorder)
					c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(requestBody)).WithContext(context.WithValue(context.Background(), ctxkey.Group, group))
					c.Request.Header.Set("Content-Type", "application/json")
					c.Set(string(middleware.ContextKeyAPIKey), key)
					c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: key.UserID, Concurrency: 10})
					if native {
						c.Params = gin.Params{{Key: "modelAction", Value: "gemini-3.7-flash:" + action}}
						h.GeminiV1BetaModels(c)
					} else {
						h.Messages(c)
					}
					require.EqualValues(t, 1, upstream.calls.Load(), recorder.Body.String())
					logs.mu.Lock()
					entries := append([]*service.UsageLog(nil), logs.logs...)
					logs.mu.Unlock()
					billing.mu.Lock()
					commands := append([]*service.UsageBillingCommand(nil), billing.commands...)
					billing.mu.Unlock()
					require.Len(t, entries, 1)
					require.Len(t, commands, 1)
					require.Equal(t, 10, entries[0].InputTokens)
					require.Equal(t, 2, entries[0].OutputTokens)
					if recovered {
						require.Contains(t, recorder.Body.String(), "complete answer")
						require.Positive(t, entries[0].ActualCost)
						require.Equal(t, entries[0].ActualCost, commands[0].BalanceCost)
					} else {
						require.Zero(t, entries[0].ActualCost)
						require.Zero(t, commands[0].BalanceCost)
						if mode == "stream" {
							require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: error\n"))
							require.NotContains(t, recorder.Body.String(), "event: message_stop\n")
						} else {
							require.Contains(t, recorder.Body.String(), `"error"`)
						}
					}
				})
			}
		}
	}
}
