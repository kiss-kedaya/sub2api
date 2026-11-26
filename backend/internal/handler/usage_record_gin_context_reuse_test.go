//go:build unit

package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// A usage task runs on a worker after the handler has returned, and gin hands the finished request's *gin.Context to
// the next request. A task that reads the Context there (eight usage closures used to call
// clientRequestedUsageFields(c, ...) inside the worker) records the NEXT request's public model name as this request's
// RequestedModel / ModelMappingChain, and, for a channel billed by the requested model, as its billing model.
//
// Every flow below runs through the real handler and service code with the usage worker blocked. For the "recycled"
// variant the Context is then returned to the pool by hand (Request replaced, Keys reset: what gin does for the next
// request) before the worker is released. The usage-log and billing repositories must receive exactly what the request
// itself carried, with or without the reuse.

const usageFlowPublicModel = "my-public-alias"
const usageFlowOtherRequestModel = "OTHER-REQUEST-MODEL"

type usageRecordWant struct {
	requestedModel   string
	upstreamModel    string
	mappingChain     string
	reasoningEffort  string
	serviceTier      string
	inboundEndpoint  string
	upstreamEndpoint string
}

type usageRecordDump struct {
	log map[string]any
	cmd map[string]any
}

type usageRecordFlow struct {
	name string
	want usageRecordWant
	// run sends one request through the handler; recycled returns the gin.Context to its pool before the usage worker
	// is released.
	run func(t *testing.T, recycled bool) usageRecordDump
}

// usageRecordMap turns a usage log / billing command into a comparable map without the fields that differ between any
// two runs (ids, timestamps, durations, shared objects, generated request ids).
func usageRecordMap(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	out := map[string]any{}
	require.NoError(t, json.Unmarshal(raw, &out))
	for _, k := range []string{"ID", "CreatedAt", "DurationMs", "FirstTokenMs", "User", "APIKey", "Account", "Group", "Subscription", "RequestedAt", "OccurredAt"} {
		delete(out, k)
	}
	if id, _ := out["RequestID"].(string); strings.HasPrefix(id, "generated:") {
		delete(out, "RequestID")
	}
	return out
}

func usageRecordString(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

// newUsageFlowRequest builds the request the way the gateway middleware hands it to a handler: JSON body already read
// into a PrereadBody.
func newUsageFlowRequest(body []byte, path string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(nil))
	req.Body = pkghttputil.NewPrereadBody(body)
	req.ContentLength = int64(len(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// recycleGinContext does to the finished request's Context what gin does when it serves the next request with it.
func recycleGinContext(c *gin.Context) {
	next := httptest.NewRequest(http.MethodPost, "/v1/other", nil).
		WithContext(context.WithValue(context.Background(), ctxkey.RequestedPublicModel, usageFlowOtherRequestModel))
	c.Request = next
	c.Keys = nil
}

func newBlockedUsagePool(t *testing.T) (pool *service.UsageRecordWorkerPool, release func()) {
	t.Helper()
	pool = service.NewUsageRecordWorkerPoolWithOptions(service.UsageRecordWorkerPoolOptions{
		WorkerCount: 1, QueueSize: 64, TaskTimeout: 5 * time.Second, OverflowPolicy: config.UsageRecordOverflowPolicySync,
	})
	block, started := make(chan struct{}), make(chan struct{})
	pool.Submit(func(context.Context) { close(started); <-block })
	<-started
	release = sync.OnceFunc(func() { close(block) })
	t.Cleanup(func() { release(); pool.Stop() })
	return pool, release
}

func claudeUsageRecordFlow(name string, passthrough bool, publicModel, extraBody string, want usageRecordWant) usageRecordFlow {
	return usageRecordFlow{name: name, want: want, run: func(t *testing.T, recycled bool) usageRecordDump {
		const model = "claude-3-7-sonnet-20250219"
		group := &service.Group{ID: 9700, Hydrated: true, Platform: service.PlatformAnthropic, Status: service.StatusActive, RateMultiplier: 1}
		account := &service.Account{
			ID: 9701, Name: "usage-record", Platform: service.PlatformAnthropic,
			Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
			Concurrency: 1, GroupIDs: []int64{group.ID},
			AccountGroups: []service.AccountGroup{{AccountID: 9701, GroupID: group.ID}},
			Credentials:   map[string]any{"api_key": "synthetic-test-key", "base_url": "https://api.anthropic.com"},
			Extra:         map[string]any{"anthropic_passthrough": passthrough},
		}
		cfg := &config.Config{RunMode: config.RunModeStandard}
		cfg.Default.RateMultiplier = 1
		cfg.Gateway.MaxLineSize = 1024 * 1024
		billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, &config.Config{RunMode: config.RunModeSimple}, nil)
		t.Cleanup(billingCache.Stop)
		usage := &passthroughFailureHandlerUsage{}
		billing := &passthroughFailureHandlerBilling{}
		upstream := &passthroughFailureHandlerUpstream{wire: "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_p\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"" + model + "\",\"content\":[],\"usage\":{\"input_tokens\":11,\"cache_read_input_tokens\":7}}}\n\n" +
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"answer\"}}\n\n" +
			"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":3}}\n\n" +
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"}
		cache := &passthroughFailureHandlerCache{bindings: make(map[string]int64), upstreamCalls: &upstream.calls}
		snapshot := service.NewSchedulerSnapshotService(&fakeSchedulerCache{accounts: []*service.Account{account}}, nil, nil, nil, nil)
		gateway := service.NewGatewayService(
			nil, &fakeGroupRepo{group: group}, usage, billing, nil, nil, nil, cache, cfg,
			snapshot, nil, service.NewBillingService(cfg, nil), nil, billingCache, nil,
			upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
		)
		pool, release := newBlockedUsagePool(t)
		h := &GatewayHandler{
			gatewayService: gateway, billingCacheService: billingCache, cfg: cfg,
			concurrencyHelper:     NewConcurrencyHelper(service.NewConcurrencyService(&fakeConcurrencyCache{}), SSEPingFormatClaude, 0),
			maxAccountSwitches:    2,
			usageRecordWorkerPool: pool,
		}
		key := &service.APIKey{
			ID: 9702, UserID: 9703, GroupID: &group.ID, Group: group, Status: service.StatusActive,
			User: &service.User{ID: 9703, Status: service.StatusActive, Balance: 100, Concurrency: 10},
		}

		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		body := []byte(`{"model":"` + model + `","stream":true,"max_tokens":64,` + extraBody + `"metadata":{"user_id":"user_` + strings.Repeat("a", 64) + `_account__session_123e4567-e89b-12d3-a456-426614174000"},"messages":[{"role":"user","content":"hello"}]}`)
		ctx := context.WithValue(context.Background(), ctxkey.Group, group)
		if publicModel != "" {
			ctx = context.WithValue(ctx, ctxkey.RequestedPublicModel, publicModel)
		}
		c.Request = newUsageFlowRequest(body, EndpointMessages).WithContext(ctx)
		c.Request = c.Request.WithContext(service.WithOpsGinContext(c.Request.Context(), c))
		c.Set(string(middleware.ContextKeyAPIKey), key)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: key.UserID, Concurrency: 10})
		h.Messages(c)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.Contains(t, rec.Body.String(), "answer")

		if recycled {
			recycleGinContext(c)
		}
		release()
		require.Eventually(t, func() bool {
			usage.mu.Lock()
			defer usage.mu.Unlock()
			billing.mu.Lock()
			defer billing.mu.Unlock()
			return len(usage.logs) >= 1 && len(billing.commands) >= 1
		}, 8*time.Second, 10*time.Millisecond, "the usage task did not run after the worker was released")
		usage.mu.Lock()
		defer usage.mu.Unlock()
		billing.mu.Lock()
		defer billing.mu.Unlock()
		require.Len(t, usage.logs, 1)
		require.Len(t, billing.commands, 1)
		return usageRecordDump{log: usageRecordMap(t, usage.logs[0]), cmd: usageRecordMap(t, billing.commands[0])}
	}}
}

func openAIUsageRecordFlow(name, protocol, route string, want usageRecordWant) usageRecordFlow {
	return usageRecordFlow{name: name, want: want, run: func(t *testing.T, recycled bool) usageRecordDump {
		groupID := int64(4001)
		account := service.Account{ID: 9910, Name: "usage-record", Platform: service.PlatformDeepseek, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, GroupIDs: []int64{groupID}, Credentials: map[string]any{"api_key": "synthetic", "base_url": "https://example.invalid", "api_protocol": protocol}}
		cfg := &config.Config{RunMode: config.RunModeStandard}
		cfg.Default.RateMultiplier = 1
		cfg.Gateway.MaxAccountSwitches = 1
		admission := *cfg
		admission.RunMode = config.RunModeSimple
		billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, &admission, nil)
		t.Cleanup(billingCache.Stop)
		usage := &openAIWSFirstRefusalUsageRepo{}
		ledger := &openAIWSFirstRefusalBillingRepo{}
		gateway := service.NewOpenAIGatewayService(&openAIWSFailoverHandlerAccountRepoStub{accounts: []service.Account{account}}, usage, ledger, nil, nil, nil, nil, cfg, nil, nil, service.NewBillingService(cfg, nil), nil, billingCache, &gaps3StreamUpstream{wire: gaps3HandlerWire(protocol, "answer_success")}, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
		pool, release := newBlockedUsagePool(t)
		h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), billingCache, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), pool, nil, nil, nil, cfg)
		key := &service.APIKey{ID: 55, UserID: 77, GroupID: &groupID, User: &service.User{ID: 77, Balance: 100, Status: service.StatusActive}, Group: &service.Group{ID: groupID, Platform: service.PlatformDeepseek, Status: service.StatusActive, Hydrated: true, CrossProtocolConversionEnabled: true, RateMultiplier: 1}}

		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		body := []byte(`{"model":"deepseek-v4-flash","stream":true,"service_tier":"priority","prompt_cache_key":"pck-probe","reasoning":{"effort":"high","summary":"auto"},"input":"hello"}`)
		if route != "responses" {
			body = []byte(`{"model":"deepseek-v4-flash","stream":true,"max_tokens":128,"messages":[{"role":"user","content":"hello"}]}`)
		}
		ctx := context.WithValue(context.Background(), ctxkey.RequestedPublicModel, usageFlowPublicModel)
		c.Request = newUsageFlowRequest(body, "/v1/"+route).WithContext(ctx)
		c.Request = c.Request.WithContext(service.WithOpsGinContext(c.Request.Context(), c))
		c.Set(string(middleware.ContextKeyAPIKey), key)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 77})
		switch route {
		case "responses":
			h.Responses(c)
		case "chat":
			h.ChatCompletions(c)
		default:
			h.Messages(c)
		}
		require.Contains(t, rec.Body.String(), "synthetic answer")

		if recycled {
			recycleGinContext(c)
		}
		release()
		require.Eventually(t, func() bool { return len(usage.snapshot()) >= 1 && len(ledger.snapshot()) >= 1 }, 8*time.Second, 10*time.Millisecond, "the usage task did not run after the worker was released")
		logs, commands := usage.snapshot(), ledger.snapshot()
		require.Len(t, logs, 1)
		require.Len(t, commands, 1)
		return usageRecordDump{log: usageRecordMap(t, logs[0]), cmd: usageRecordMap(t, commands[0])}
	}}
}

func claudeUsageWant(requested, chain, effort string) usageRecordWant {
	const model = "claude-3-7-sonnet-20250219"
	return usageRecordWant{
		requestedModel: requested, upstreamModel: model, mappingChain: chain, reasoningEffort: effort,
		inboundEndpoint: "/v1/messages", upstreamEndpoint: "/v1/messages",
	}
}

func openAIUsageWant(inbound, upstream, effort, tier string) usageRecordWant {
	return usageRecordWant{
		requestedModel: usageFlowPublicModel, upstreamModel: "deepseek-v4-flash", mappingChain: usageFlowPublicModel + "→deepseek-v4-flash",
		reasoningEffort: effort, serviceTier: tier, inboundEndpoint: inbound, upstreamEndpoint: upstream,
	}
}

func claudeUsageRecordFlows() []usageRecordFlow {
	const chain = usageFlowPublicModel + "→claude-3-7-sonnet-20250219"
	publicWant := claudeUsageWant(usageFlowPublicModel, chain, "")
	return []usageRecordFlow{
		claudeUsageRecordFlow("claude_native_plain", false, "", "", claudeUsageWant("claude-3-7-sonnet-20250219", "", "")),
		claudeUsageRecordFlow("claude_native_public", false, usageFlowPublicModel, "", publicWant),
		claudeUsageRecordFlow("claude_passthrough_public", true, usageFlowPublicModel, "", publicWant),
		claudeUsageRecordFlow("claude_native_thinking_effort", false, usageFlowPublicModel, `"thinking":{"type":"enabled","budget_tokens":1024},"output_config":{"effort":"high"},`, claudeUsageWant(usageFlowPublicModel, chain, "high")),
	}
}

func openAIUsageRecordFlows() []usageRecordFlow {
	var flows []usageRecordFlow
	for _, tc := range []struct {
		protocol, route string
		want            usageRecordWant
	}{
		{service.APIProtocolAnthropic, "chat", openAIUsageWant("/v1/chat", "/v1/messages", "", "")},
		{service.APIProtocolAnthropic, "messages", openAIUsageWant("/v1/messages", "/v1/messages", "", "")},
		{service.APIProtocolAnthropic, "responses", openAIUsageWant("/v1/responses", "/v1/messages", "high", "")},
		{service.APIProtocolChatCompletions, "chat", openAIUsageWant("/v1/chat", "/v1/chat/completions", "", "")},
		{service.APIProtocolChatCompletions, "messages", openAIUsageWant("/v1/messages", "/v1/chat/completions", "medium", "")},
		{service.APIProtocolChatCompletions, "responses", openAIUsageWant("/v1/responses", "/v1/chat/completions", "high", "priority")},
		{service.APIProtocolResponses, "chat", openAIUsageWant("/v1/chat", "/v1/chat", "", "")},
		{service.APIProtocolResponses, "messages", openAIUsageWant("/v1/messages", "/v1/messages", "medium", "")},
		{service.APIProtocolResponses, "responses", openAIUsageWant("/v1/responses", "/v1/responses", "high", "priority")},
	} {
		flows = append(flows, openAIUsageRecordFlow("openai_"+tc.protocol+"_"+tc.route, tc.protocol, tc.route, tc.want))
	}
	return flows
}

func requireUsageRecords(t *testing.T, got usageRecordDump, want usageRecordWant) {
	t.Helper()
	for _, rec := range []struct {
		kind string
		m    map[string]any
	}{{"usage log", got.log}, {"billing command", got.cmd}} {
		require.Equal(t, want.upstreamModel, usageRecordString(rec.m, "Model"), rec.kind+" Model")
		require.Equal(t, want.reasoningEffort, usageRecordString(rec.m, "ReasoningEffort"), rec.kind+" ReasoningEffort")
		require.Equal(t, want.serviceTier, usageRecordString(rec.m, "ServiceTier"), rec.kind+" ServiceTier")
	}
	require.Equal(t, want.requestedModel, usageRecordString(got.log, "RequestedModel"), "usage log RequestedModel must be the request's own public model")
	require.Equal(t, want.upstreamModel, usageRecordString(got.log, "UpstreamModel"), "usage log UpstreamModel")
	require.Equal(t, want.mappingChain, usageRecordString(got.log, "ModelMappingChain"), "usage log ModelMappingChain")
	require.Equal(t, want.inboundEndpoint, usageRecordString(got.log, "InboundEndpoint"), "usage log InboundEndpoint")
	require.Equal(t, want.upstreamEndpoint, usageRecordString(got.log, "UpstreamEndpoint"), "usage log UpstreamEndpoint")
}

// The values the usage-log and billing repositories receive for a request whose Context is still intact: this pins
// the fields billing and the usage page read (models, tier, effort, endpoints) for every handler flow.
func TestUsageRecordsCarryTheRequestsOwnValues(t *testing.T) {
	gin.SetMode(gin.TestMode)
	flows := append(claudeUsageRecordFlows(), openAIUsageRecordFlows()...)
	require.Len(t, flows, 13)
	for _, flow := range flows {
		t.Run(flow.name, func(t *testing.T) {
			requireUsageRecords(t, flow.run(t, false), flow.want)
		})
	}
}

// The same flows when the Context has already been given to the next request by the time the worker runs: nothing a
// record carries may change. Before the usage closures stopped reading the Context, RequestedModel and
// ModelMappingChain came out as the next request's model in every one of these flows.
func TestUsageRecordsDoNotFollowARecycledGinContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var flows []usageRecordFlow
	for _, flow := range append(claudeUsageRecordFlows(), openAIUsageRecordFlows()...) {
		if flow.want.requestedModel == usageFlowPublicModel && flow.name != "claude_native_thinking_effort" {
			flows = append(flows, flow)
		}
	}
	require.Len(t, flows, 11)
	for _, flow := range flows {
		t.Run(flow.name, func(t *testing.T) {
			intact := flow.run(t, false)
			recycled := flow.run(t, true)
			requireUsageRecords(t, recycled, flow.want)
			// every other field too, not only the ones named above
			require.Equal(t, intact.log, recycled.log, "usage log differs once the Context was reused")
			require.Equal(t, intact.cmd, recycled.cmd, "billing command differs once the Context was reused")
		})
	}
}
