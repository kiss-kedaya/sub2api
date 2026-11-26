//go:build unit

package handler

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"
	"weak"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// A billing task waits in a bounded queue for as long as the database is slow, and the usage-log batch queue
// does the same. Whatever those hold must not keep the finished request's body (megabytes) alive.
//
// Every flow below runs one full request through the real handler and service code with a stub upstream, so the
// body is tracked through the same strings, results and closures production uses.
type bodyReleaseFlow struct {
	name string
	// run sends one request whose body is pad bytes of padding.
	run func(t *testing.T, pool *service.UsageRecordWorkerPool, pad int) bodyReleaseRun
}

type bodyReleaseRun struct {
	// w tracks the request body slice
	w weak.Pointer[byte]
	// keep is everything long-lived the request touched (handler, services, the repositories that retain usage
	// records): the caller keeps it alive while it checks the body.
	keep any
	// c is the request's gin.Context. gin returns it to a pool without resetting it; only the tests that want to look
	// at that situation hold on to it.
	c *gin.Context
}

func requireRequestBodyCollected(t *testing.T, w weak.Pointer[byte], msg string) {
	t.Helper()
	require.Eventually(t, func() bool {
		runtime.GC()
		return w.Value() == nil
	}, 5*time.Second, 10*time.Millisecond, msg)
}

func newBodyReleasePreread(body []byte, path string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(nil))
	// the gateway middleware hands handlers the body as an already-read PrereadBody
	req.Body = pkghttputil.NewPrereadBody(body)
	req.ContentLength = int64(len(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func claudeBodyReleaseFlow(passthrough bool) bodyReleaseFlow {
	name := "claude_native"
	if passthrough {
		name = "claude_passthrough"
	}
	return bodyReleaseFlow{name: name, run: func(t *testing.T, pool *service.UsageRecordWorkerPool, pad int) bodyReleaseRun {
		const model = "claude-3-7-sonnet-20250219"
		group := &service.Group{ID: 9700, Hydrated: true, Platform: service.PlatformAnthropic, Status: service.StatusActive, RateMultiplier: 1}
		account := &service.Account{
			ID: 9701, Name: "body-release", Platform: service.PlatformAnthropic,
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
		w := func() weak.Pointer[byte] {
			body := []byte(`{"model":"` + model + `","stream":true,"max_tokens":64,"metadata":{"user_id":"user_` + strings.Repeat("a", 64) + `_account__session_123e4567-e89b-12d3-a456-426614174000"},"messages":[{"role":"user","content":"` + strings.Repeat("p", pad) + `"}]}`)
			c.Request = newBodyReleasePreread(body, EndpointMessages).WithContext(context.WithValue(context.Background(), ctxkey.Group, group))
			return weak.Make(&body[0])
		}()
		// production's OpsErrorLoggerMiddleware hangs the gin.Context on the request context; a queued task that keeps
		// the request context therefore keeps the context, and through it the body
		c.Request = c.Request.WithContext(service.WithOpsGinContext(c.Request.Context(), c))
		c.Set(string(middleware.ContextKeyAPIKey), key)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: key.UserID, Concurrency: 10})
		h.Messages(c)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.Contains(t, rec.Body.String(), "answer")
		return bodyReleaseRun{w: w, keep: []any{h, gateway, usage, billing, cache, snapshot}, c: c}
	}}
}

func openAIBodyReleaseFlow(protocol, route string) bodyReleaseFlow {
	return bodyReleaseFlow{name: fmt.Sprintf("openai_%s_%s", protocol, route), run: func(t *testing.T, pool *service.UsageRecordWorkerPool, pad int) bodyReleaseRun {
		groupID := int64(4001)
		account := service.Account{ID: 9910, Name: "body-release", Platform: service.PlatformDeepseek, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, GroupIDs: []int64{groupID}, Credentials: map[string]any{"api_key": "synthetic", "base_url": "https://example.invalid", "api_protocol": protocol}}
		cfg := &config.Config{RunMode: config.RunModeStandard}
		cfg.Default.RateMultiplier = 1
		cfg.Gateway.MaxAccountSwitches = 1
		admission := *cfg
		admission.RunMode = config.RunModeSimple
		billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, &admission, nil)
		t.Cleanup(billing.Stop)
		usage := &openAIWSFirstRefusalUsageRepo{}
		ledger := &openAIWSFirstRefusalBillingRepo{}
		gateway := service.NewOpenAIGatewayService(&openAIWSFailoverHandlerAccountRepoStub{accounts: []service.Account{account}}, usage, ledger, nil, nil, nil, nil, cfg, nil, nil, service.NewBillingService(cfg, nil), nil, billing, &gaps3StreamUpstream{wire: gaps3HandlerWire(protocol, "answer_success")}, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
		h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), billing, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), pool, nil, nil, nil, cfg)
		key := &service.APIKey{ID: 55, UserID: 77, GroupID: &groupID, User: &service.User{ID: 77, Balance: 100, Status: service.StatusActive}, Group: &service.Group{ID: groupID, Platform: service.PlatformDeepseek, Status: service.StatusActive, Hydrated: true, CrossProtocolConversionEnabled: true, RateMultiplier: 1}}
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		w := func() weak.Pointer[byte] {
			padding := strings.Repeat("p", pad)
			body := []byte(`{"model":"deepseek-v4-flash","stream":true,"reasoning":{"effort":"high","summary":"auto"},"input":"` + padding + `"}`)
			if route != "responses" {
				body = []byte(`{"model":"deepseek-v4-flash","stream":true,"max_tokens":128,"messages":[{"role":"user","content":"` + padding + `"}]}`)
			}
			c.Request = newBodyReleasePreread(body, "/v1/"+route)
			return weak.Make(&body[0])
		}()
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
		return bodyReleaseRun{w: w, keep: []any{h, gateway, usage, ledger}, c: c}
	}}
}

func allBodyReleaseFlows() []bodyReleaseFlow {
	flows := []bodyReleaseFlow{claudeBodyReleaseFlow(false), claudeBodyReleaseFlow(true)}
	for _, protocol := range []string{service.APIProtocolResponses, service.APIProtocolChatCompletions, service.APIProtocolAnthropic} {
		for _, route := range []string{"responses", "chat", "messages"} {
			flows = append(flows, openAIBodyReleaseFlow(protocol, route))
		}
	}
	return flows
}

// Billing is slow: the single worker is stuck, so every usage task of a finished request waits in the queue.
func TestQueuedBillingTasksDoNotPinTheFinishedRequestBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, flow := range allBodyReleaseFlows() {
		t.Run(flow.name, func(t *testing.T) {
			pool := service.NewUsageRecordWorkerPoolWithOptions(service.UsageRecordWorkerPoolOptions{
				WorkerCount: 1, QueueSize: 64, TaskTimeout: time.Second, OverflowPolicy: config.UsageRecordOverflowPolicySync,
			})
			block, started := make(chan struct{}), make(chan struct{})
			pool.Submit(func(context.Context) { close(started); <-block })
			<-started
			t.Cleanup(func() { close(block); pool.Stop() })

			warm := flow.run(t, pool, 64<<10) // warm-up: lazily built singletons may capture the first request
			queuedBefore := pool.Stats().WaitingTasks
			got := flow.run(t, pool, 1<<20)
			require.Greater(t, pool.Stats().WaitingTasks, queuedBefore, "the request's usage task must actually be waiting in the queue, otherwise this test proves nothing")
			requireRequestBodyCollected(t, got.w, "a queued billing task keeps the finished request's body alive")
			runtime.KeepAlive(warm.keep)
			runtime.KeepAlive(got.keep)
		})
	}
}

// The test repositories keep every UsageLog / billing command they are handed, like the production batch queue does
// while it waits for a flush. Those records must not point into the request body either.
func TestRetainedUsageRecordsDoNotPinTheFinishedRequestBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, flow := range allBodyReleaseFlows() {
		t.Run(flow.name, func(t *testing.T) {
			warm := flow.run(t, nil, 64<<10)
			got := flow.run(t, nil, 1<<20)
			requireRequestBodyCollected(t, got.w, "a retained usage record keeps the finished request's body alive")
			runtime.KeepAlive(warm.keep)
			runtime.KeepAlive(got.keep)
		})
	}
}

// gin gives the Context back to its pool without resetting it, and a task may carry it, so the Context outlives the
// request. RejectConflictingRequestFields releases the body, the BodyTop index and the cached copies when the chain
// returns (that middleware is tested in its own package); here the same cleanup is applied to the Context each flow
// left behind, and nothing else on it or on its request context may still hold the body.
func TestFinishedRequestContextDoesNotPinTheBodyOnceReleased(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, flow := range allBodyReleaseFlows() {
		t.Run(flow.name, func(t *testing.T) {
			warm := flow.run(t, nil, 64<<10)
			got := flow.run(t, nil, 1<<20)
			if p, ok := got.c.Request.Body.(*pkghttputil.PrereadBody); ok {
				p.Release()
			}
			service.ReleaseRequestBodyReferences(got.c)
			requireRequestBodyCollected(t, got.w, "something else on the finished request's gin.Context still holds the body")
			runtime.KeepAlive(warm.keep)
			runtime.KeepAlive(got.keep)
			runtime.KeepAlive(got.c)
		})
	}
}

// submitOpenAIUsageRecordTask is where every OpenAI-family usage task passes, so it also covers strings that reach a
// result by a path no flow test exercises.
func TestSubmitOpenAIUsageRecordTaskDetachesResultStrings(t *testing.T) {
	pool := service.NewUsageRecordWorkerPoolWithOptions(service.UsageRecordWorkerPoolOptions{
		WorkerCount: 1, QueueSize: 8, TaskTimeout: time.Second, OverflowPolicy: config.UsageRecordOverflowPolicySync,
	})
	block, started := make(chan struct{}), make(chan struct{})
	pool.Submit(func(context.Context) { close(started); <-block })
	<-started
	t.Cleanup(func() { close(block); pool.Stop() })
	h := &OpenAIGatewayHandler{usageRecordWorkerPool: pool}

	w := func() weak.Pointer[byte] {
		buf := []byte(`{"model":"gpt-5.1"` + strings.Repeat(" ", 1<<20) + `}`)
		tier := unsafe.String(&buf[10], 7)
		result := &service.OpenAIForwardResult{Model: unsafe.String(&buf[10], 7), UpstreamModel: unsafe.String(&buf[10], 7), ServiceTier: &tier}
		h.submitOpenAIUsageRecordTask(context.Background(), result, func(context.Context) { _ = result.Model })
		return weak.Make(&buf[0])
	}()
	require.Equal(t, uint64(1), pool.Stats().WaitingTasks, "the usage task must be waiting in the queue")
	requireRequestBodyCollected(t, w, "a queued usage task keeps the request body alive through its result's strings")
}
