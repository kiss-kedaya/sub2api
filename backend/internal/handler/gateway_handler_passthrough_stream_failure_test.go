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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/testutil"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGatewayBridgePreservesFixedToolBilling(t *testing.T) {
	converted := openAIForwardResultAsGateway(&service.OpenAIForwardResult{SearchCount: 2, FixedToolBilling: true})
	require.NotNil(t, converted)
	require.True(t, converted.FixedToolBilling, "Messages compatibility must retain fixed hosted tool pricing")
	require.Equal(t, 2, converted.SearchCount)
}

type passthroughFailureHandlerUsage struct {
	service.UsageLogRepository
	mu   sync.Mutex
	logs []*service.UsageLog
}

func (r *passthroughFailureHandlerUsage) Create(_ context.Context, usage *service.UsageLog) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	copy := *usage
	r.logs = append(r.logs, &copy)
	return true, nil
}

type passthroughFailureHandlerBilling struct {
	service.UsageBillingRepository
	mu       sync.Mutex
	commands []*service.UsageBillingCommand
}

func (r *passthroughFailureHandlerBilling) Apply(_ context.Context, command *service.UsageBillingCommand) (*service.UsageBillingApplyResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	copy := *command
	r.commands = append(r.commands, &copy)
	return &service.UsageBillingApplyResult{Applied: true}, nil
}

type passthroughFailureHandlerCache struct {
	testutil.StubGatewayCache
	mu                  sync.Mutex
	bindings            map[string]int64
	upstreamCalls       *atomic.Int64
	preForwardBindings  int
	postForwardBindings int
}

func (s *passthroughFailureHandlerCache) GetSessionAccountID(_ context.Context, groupID int64, key string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bindings[fmt.Sprintf("%d:%s", groupID, key)], nil
}

func (s *passthroughFailureHandlerCache) SetSessionAccountID(_ context.Context, groupID int64, key string, accountID int64, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.upstreamCalls.Load() == 0 {
		s.preForwardBindings++
	} else {
		s.postForwardBindings++
	}
	s.bindings[fmt.Sprintf("%d:%s", groupID, key)] = accountID
	return nil
}

func (s *passthroughFailureHandlerCache) DeleteSessionAccountID(_ context.Context, groupID int64, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.bindings, fmt.Sprintf("%d:%s", groupID, key))
	return nil
}

type passthroughFailureHandlerUpstream struct {
	calls atomic.Int64
	wire  string
}

func (u *passthroughFailureHandlerUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.calls.Add(1)
	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": {"text/event-stream"},
			"X-Request-Id": {"synthetic-anthropic-stream-failure"},
		},
		Body:    io.NopCloser(strings.NewReader(u.wire)),
		Request: req,
	}, nil
}

func (u *passthroughFailureHandlerUpstream) DoWithTLS(req *http.Request, proxy string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, accountID, concurrency)
}

// Exercise the complete Messages handler, the actual Anthropic passthrough,
// and real usage settlement. Helper-only error tests cannot detect a second
// submission or a success-only sticky refresh in the handler's final branch.
func TestGatewayHandlerMessagesPassthroughStreamFailureIsTerminal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name      string
		payload   string
		status    int
		errorType string
		withUsage bool
	}{
		{
			name:    "explicit status preserves partial usage once",
			payload: `{"type":"error","error":{"type":"relay_stream_error","message":"upstream interrupted","status_code":500}}`,
			status:  http.StatusInternalServerError, errorType: "relay_stream_error", withUsage: true,
		},
		{
			name:    "overload is 529 and not generic 502",
			payload: `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`,
			status:  529, errorType: "overloaded_error", withUsage: true,
		},
		{
			name:    "error without measured usage does not charge",
			payload: `{"error":{"message":"stream relay failure: scanner_error: unexpected EOF","type":"relay_stream_error"},"type":"error"}`,
			status:  http.StatusBadGateway, errorType: "relay_stream_error",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const model = "claude-3-7-sonnet-20250219"
			group := &service.Group{ID: 9700, Hydrated: true, Platform: service.PlatformAnthropic, Status: service.StatusActive, RateMultiplier: 1}
			account := &service.Account{
				ID: 9701, Name: "synthetic-anthropic-stream", Platform: service.PlatformAnthropic,
				Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
				Concurrency: 1, GroupIDs: []int64{group.ID},
				AccountGroups: []service.AccountGroup{{AccountID: 9701, GroupID: group.ID}},
				Credentials:   map[string]any{"api_key": "synthetic-test-key", "base_url": "https://api.anthropic.com"},
				Extra:         map[string]any{"anthropic_passthrough": true},
			}
			cfg := &config.Config{RunMode: config.RunModeStandard}
			cfg.Default.RateMultiplier = 1
			cfg.Gateway.MaxLineSize = 1024 * 1024
			// Only eligibility storage is bypassed. Forwarding, the handler's
			// error branch, token pricing, settlement and log creation are real.
			billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, &config.Config{RunMode: config.RunModeSimple}, nil)
			t.Cleanup(billingCache.Stop)
			usage := &passthroughFailureHandlerUsage{}
			billing := &passthroughFailureHandlerBilling{}
			upstream := &passthroughFailureHandlerUpstream{}
			cache := &passthroughFailureHandlerCache{bindings: make(map[string]int64), upstreamCalls: &upstream.calls}
			if tc.withUsage {
				upstream.wire = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_partial\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"" + model + "\",\"content\":[],\"usage\":{\"input_tokens\":11,\"cache_read_input_tokens\":7}}}\n\n" +
					"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"partial answer\"}}\n\n" +
					"event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":3}}\n\n"
			}
			upstream.wire += "event: error\ndata: " + tc.payload + "\n\n" +
				"event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":999,\"output_tokens\":999}}\n\n" +
				"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
			snapshot := service.NewSchedulerSnapshotService(&fakeSchedulerCache{accounts: []*service.Account{account}}, nil, nil, nil, nil)
			gateway := service.NewGatewayService(
				nil, &fakeGroupRepo{group: group}, usage, billing, nil, nil, nil, cache, cfg,
				snapshot, nil, service.NewBillingService(cfg, nil), nil, billingCache, nil,
				upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
			)
			h := &GatewayHandler{
				gatewayService: gateway, billingCacheService: billingCache, cfg: cfg,
				concurrencyHelper:  NewConcurrencyHelper(service.NewConcurrencyService(&fakeConcurrencyCache{}), SSEPingFormatClaude, 0),
				maxAccountSwitches: 2,
			}
			key := &service.APIKey{
				ID: 9702, UserID: 9703, GroupID: &group.ID, Group: group, Status: service.StatusActive,
				User: &service.User{ID: 9703, Status: service.StatusActive, Balance: 100, Concurrency: 10},
			}
			body := `{"model":"` + model + `","stream":true,"max_tokens":64,"metadata":{"user_id":"user_` + strings.Repeat("a", 64) + `_account__session_123e4567-e89b-12d3-a456-426614174000"},"messages":[{"role":"user","content":"Please answer this synthetic regression check."}]}`
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			ctx := context.WithValue(context.Background(), ctxkey.Group, group)
			c.Request = httptest.NewRequest(http.MethodPost, EndpointMessages, bytes.NewBufferString(body)).WithContext(ctx)
			c.Request.Header.Set("Content-Type", "application/json")
			c.Set(string(middleware.ContextKeyAPIKey), key)
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: key.UserID, Concurrency: 10})

			h.Messages(c)

			require.Equal(t, http.StatusOK, recorder.Code, "the accepted SSE transport stays 200")
			require.Contains(t, recorder.Header().Get("Content-Type"), "text/event-stream")
			require.EqualValues(t, 1, upstream.calls.Load(), "a terminal stream failure must never replay the request")
			wire := recorder.Body.String()
			require.Equal(t, 1, strings.Count(wire, "event: error\n"), wire)
			require.Equal(t, 1, strings.Count(wire, tc.payload), "the original provider error must be sent exactly once")
			require.NotContains(t, wire, "Upstream request failed")
			require.NotContains(t, wire, "event: message_stop")
			require.NotContains(t, wire, `"output_tokens":999`, "events after the terminal error cannot change usage")
			failure, ok := service.GetOpsStreamError(c)
			require.True(t, ok, "HTTP 200 must still be recorded as an application failure")
			require.Equal(t, tc.status, failure.IntendedStatus)
			require.Equal(t, tc.errorType, failure.ErrType)
			require.True(t, failure.CountTowardsSLA)

			cache.mu.Lock()
			preBindings, postBindings, remainingBindings := cache.preForwardBindings, cache.postForwardBindings, len(cache.bindings)
			cache.mu.Unlock()
			require.Positive(t, preBindings, "the fixture must exercise a real session binding before Forward")
			require.Zero(t, postBindings, "failed Forward must skip the success-only sticky refresh")
			require.Zero(t, remainingBindings, "the failed upstream affinity must be invalidated")
			// No worker pool is installed, so the mandatory usage path completes
			// synchronously before Messages returns. Both record counts are exact.
			usage.mu.Lock()
			logs := append([]*service.UsageLog(nil), usage.logs...)
			usage.mu.Unlock()
			billing.mu.Lock()
			commands := append([]*service.UsageBillingCommand(nil), billing.commands...)
			billing.mu.Unlock()
			if !tc.withUsage {
				require.Empty(t, logs, "an error-only stream has no observed usage to record")
				require.Empty(t, commands, "an error-only stream must not fabricate a charge")
				return
			}
			require.Len(t, logs, 1, "partial usage must be recorded once through the handler's error branch")
			require.Len(t, commands, 1, "partial usage must be settled once, never again as success")
			require.Equal(t, 11, logs[0].InputTokens)
			require.Equal(t, 7, logs[0].CacheReadTokens)
			require.Equal(t, 3, logs[0].OutputTokens)
			require.Positive(t, logs[0].ActualCost, "real provider-measured tokens remain billable")
			require.InDelta(t, logs[0].ActualCost, commands[0].BalanceCost, 1e-12)
			require.Equal(t, logs[0].RequestID, commands[0].RequestID)
		})
	}
}
