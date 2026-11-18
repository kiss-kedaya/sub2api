//go:build unit

package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/testutil"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGeminiR4NativeHandlerRegressionsSettleOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const usage = `"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":2}`
	const prelude = "data: {\"error\":{\"code\":503,\"message\":\"synthetic prelude\"}}\n\n"
	cases := []struct {
		name, protocol, body, marker string
		stream, oauth, free          bool
	}{
		{name: "chat-placeholder-buffered", protocol: "chat_completions", body: `{"choices":[{"message":{"role":"assistant","content":""},"finish_reason":null}],"error":{"message":"synthetic failure"},"usage":{"prompt_tokens":10,"completion_tokens":2}}`, free: true},
		{name: "chat-placeholder-stream", protocol: "chat_completions", stream: true, body: "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2}}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"}}],\"error\":{\"message\":\"synthetic failure\"}}\n\ndata: [DONE]\n\n", free: true},
	}
	for _, stream := range []bool{false, true} {
		for _, part := range []struct{ name, data, marker string }{
			{"audio", `{"inlineData":{"mimeType":"audio/pcm","data":"c3ludGhldGlj"}}`, "c3ludGhldGlj"},
			{"file", `{"fileData":{"mimeType":"image/png","fileUri":"https://example.invalid/generated.png"}}`, "https://example.invalid/generated.png"},
			{"second-candidate", `{"text":"synthetic second answer"}`, "synthetic second answer"},
		} {
			candidates := `[{"content":{"parts":[` + part.data + `]},"finishReason":"STOP"}]`
			if part.name == "second-candidate" {
				candidates = `[{"index":0,"finishReason":"STOP"},{"index":1,"content":{"parts":[` + part.data + `]},"finishReason":"STOP"}]`
			}
			frame := `{"candidates":` + candidates + `,` + usage + `}`
			if !stream {
				frame = `{"response":` + frame + `}`
			}
			cases = append(cases, struct {
				name, protocol, body, marker string
				stream, oauth, free          bool
			}{fmt.Sprintf("%s/stream=%t", part.name, stream), "gemini", prelude + "data: " + frame + "\n\n", part.marker, stream, !stream, false})
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			group := &service.Group{ID: 9800, Hydrated: true, Platform: service.PlatformGemini, Status: service.StatusActive, RateMultiplier: 1}
			account := &service.Account{ID: 9801, Platform: service.PlatformGemini, Type: service.AccountTypeAPIKey,
				Status: service.StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{group.ID},
				AccountGroups: []service.AccountGroup{{AccountID: 9801, GroupID: group.ID}},
				Credentials:   map[string]any{"api_key": "synthetic-key", "base_url": "https://example.invalid", "api_protocol": tc.protocol}}
			if tc.oauth {
				account.Type = service.AccountTypeOAuth
				account.Credentials = map[string]any{"access_token": "synthetic-token", "project_id": "synthetic-project"}
			}
			cfg := &config.Config{RunMode: config.RunModeStandard}
			cfg.Default.RateMultiplier = 1
			cfg.Gateway.MaxLineSize = 1024 * 1024
			cache := &testutil.StubGatewayCache{}
			snapshot := service.NewSchedulerSnapshotService(&fakeSchedulerCache{accounts: []*service.Account{account}}, nil, nil, nil, nil)
			logs, billing := &passthroughFailureHandlerUsage{}, &passthroughFailureHandlerBilling{}
			upstream := &geminiGaps3HandlerUpstream{body: tc.body}
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
			action := "generateContent"
			if tc.stream {
				action = "streamGenerateContent"
			}
			requestBody := `{"contents":[{"role":"user","parts":[{"text":"synthetic check"}]}]}`
			switch {
			case strings.Contains(tc.name, "audio"):
				requestBody = `{"contents":[{"role":"user","parts":[{"text":"synthetic check"}]}],"generationConfig":{"responseModalities":["AUDIO"]}}`
			case strings.Contains(tc.name, "second-candidate"):
				requestBody = `{"contents":[{"role":"user","parts":[{"text":"synthetic check"}]}],"generationConfig":{"candidateCount":2}}`
			}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-3.7-flash:"+action, bytes.NewBufferString(requestBody)).WithContext(context.WithValue(context.Background(), ctxkey.Group, group))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Set(string(middleware.ContextKeyAPIKey), key)
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: key.UserID, Concurrency: 10})
			c.Params = gin.Params{{Key: "modelAction", Value: "gemini-3.7-flash:" + action}}
			h.GeminiV1BetaModels(c)
			require.EqualValues(t, 1, upstream.calls.Load(), rec.Body.String())
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
			if tc.free {
				require.Zero(t, entries[0].ActualCost)
				require.Contains(t, rec.Body.String(), `"error"`)
				require.NotContains(t, rec.Body.String(), `"finishReason"`)
			} else {
				require.Equal(t, http.StatusOK, rec.Code)
				require.Positive(t, entries[0].ActualCost)
				require.Contains(t, rec.Body.String(), tc.marker)
			}
			require.Equal(t, entries[0].ActualCost, commands[0].BalanceCost)
			require.LessOrEqual(t, strings.Count(rec.Body.String(), "event: error\n"), 1)
			clientSnapshot, err := json.Marshal([]any{rec.Code, entries[0].InputTokens, entries[0].OutputTokens, entries[0].ActualCost, commands[0].BalanceCost, rec.Body.String()})
			require.NoError(t, err)
			t.Logf("R4-HANDLER-SNAPSHOT %s", clientSnapshot)
		})
	}
}
