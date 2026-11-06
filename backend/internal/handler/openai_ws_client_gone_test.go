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
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// A client that leaves while the upstream is still generating ends the upstream
// request at once. What the upstream had produced so far is billed from an estimate
// (output from the streamed content, input from the request, 95% of it as cache
// read), and the account is not blamed for the client leaving. Every test drives the
// real ResponsesWebSocket handler against an upstream the test controls.
const (
	wsGoneLongMarker  = "long-turn"
	wsGoneQuickMarker = "quick-turn"
	// 40 ASCII characters are estimated as exactly 10 tokens.
	wsGoneDeltaText     = "0123456789abcdefghijklmnopqrstuvwxyzABCD"
	wsGoneDeltaTokens   = 10
	wsGoneDeltaInterval = 20 * time.Millisecond
	// 250 deltas keep a long turn generating for 5 seconds unless somebody hangs up.
	wsGoneMaxDeltas     = 250
	wsGoneHangUpBudget  = 700 * time.Millisecond
	wsGoneHandlerBudget = 2500 * time.Millisecond
	// The real usage a long turn would report on its terminal event.
	wsGoneRealInput  = 100000
	wsGoneRealOutput = 70000
)

var wsGoneInputPrefix = strings.Repeat("Please write a long story about a fox who learns to code. ", 8)

type wsGoneMode string

const (
	wsGoneCtxPool     wsGoneMode = "ctx_pool"
	wsGoneHTTPBridge  wsGoneMode = "http_bridge"
	wsGonePassthrough wsGoneMode = "passthrough"
)

var wsGoneModes = []wsGoneMode{wsGoneCtxPool, wsGoneHTTPBridge, wsGonePassthrough}

func wsGoneCreatedEvent(id string) string {
	return `{"type":"response.created","response":{"id":"` + id + `","model":"gpt-5.4","status":"in_progress"}}`
}

func wsGoneDeltaEvent() string {
	return `{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"` + wsGoneDeltaText + `"}`
}

func wsGoneCompletedEvent(id string, input, output int) string {
	return fmt.Sprintf(`{"type":"response.completed","response":{"id":"%s","model":"gpt-5.4","status":"completed","usage":{"input_tokens":%d,"output_tokens":%d}}}`, id, input, output)
}

// wsGoneUpstream plays both upstream protocols (Responses WebSocket and SSE). A turn
// whose request carries wsGoneLongMarker keeps streaming deltas until the gateway
// hangs up; any other turn finishes at once with a small real usage.
type wsGoneUpstream struct {
	server *httptest.Server

	mu     sync.Mutex
	bodies [][]byte

	ids        atomic.Int32
	sentDeltas atomic.Int32
	hungUp     chan struct{}
	hungUpAt   atomic.Int64
	hungUpOnce sync.Once
}

func newWSGoneUpstream(t *testing.T) *wsGoneUpstream {
	t.Helper()
	u := &wsGoneUpstream{hungUp: make(chan struct{})}
	u.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			u.serveWS(w, r)
			return
		}
		u.serveSSE(w, r)
	}))
	t.Cleanup(u.server.Close)
	return u
}

func (u *wsGoneUpstream) record(body []byte) {
	u.mu.Lock()
	u.bodies = append(u.bodies, append([]byte(nil), body...))
	u.mu.Unlock()
}

func (u *wsGoneUpstream) request(i int) []byte {
	u.mu.Lock()
	defer u.mu.Unlock()
	if i >= len(u.bodies) {
		return nil
	}
	return append([]byte(nil), u.bodies[i]...)
}

func (u *wsGoneUpstream) requestCount() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.bodies)
}

func (u *wsGoneUpstream) nextID() string {
	return fmt.Sprintf("resp_gone_%d", u.ids.Add(1))
}

func (u *wsGoneUpstream) markHungUp() {
	u.hungUpOnce.Do(func() {
		u.hungUpAt.Store(time.Now().UnixNano())
		close(u.hungUp)
	})
}

// serveWS keeps one reader on the connection for its whole life: it answers the
// gateway's close handshake at once and tells a hang-up apart from a live turn.
func (u *wsGoneUpstream) serveWS(w http.ResponseWriter, r *http.Request) {
	conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{CompressionMode: coderws.CompressionContextTakeover})
	if err != nil {
		return
	}
	defer func() { _ = conn.CloseNow() }()
	gone := make(chan struct{})
	frames := make(chan []byte, 8)
	go func() {
		defer close(gone)
		for {
			_, frame, readErr := conn.Read(context.Background())
			if readErr != nil {
				return
			}
			frames <- frame
		}
	}()
	for {
		select {
		case frame := <-frames:
			u.record(frame)
			if !u.playWSTurn(conn, frame, gone) {
				return
			}
		case <-gone:
			return
		}
	}
}

func (u *wsGoneUpstream) playWSTurn(conn *coderws.Conn, frame []byte, gone <-chan struct{}) bool {
	id := u.nextID()
	send := func(payload string) bool {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		return conn.Write(ctx, coderws.MessageText, []byte(payload)) == nil
	}
	if !send(wsGoneCreatedEvent(id)) {
		return false
	}
	if !bytes.Contains(frame, []byte(wsGoneLongMarker)) {
		return send(wsGoneDeltaEvent()) && send(wsGoneCompletedEvent(id, 10, 5))
	}
	for i := 0; i < wsGoneMaxDeltas; i++ {
		select {
		case <-gone:
			u.markHungUp()
			return false
		case <-time.After(wsGoneDeltaInterval):
		}
		if !send(wsGoneDeltaEvent()) {
			u.markHungUp()
			return false
		}
		u.sentDeltas.Add(1)
	}
	return send(wsGoneCompletedEvent(id, wsGoneRealInput, wsGoneRealOutput))
}

func (u *wsGoneUpstream) serveSSE(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	u.record(body)
	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	send := func(event, payload string) bool {
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload); err != nil {
			return false
		}
		if flusher != nil {
			flusher.Flush()
		}
		return true
	}
	id := u.nextID()
	send("response.created", wsGoneCreatedEvent(id))
	if !bytes.Contains(body, []byte(wsGoneLongMarker)) {
		send("response.output_text.delta", wsGoneDeltaEvent())
		send("response.completed", wsGoneCompletedEvent(id, 10, 5))
		return
	}
	for i := 0; i < wsGoneMaxDeltas; i++ {
		select {
		case <-r.Context().Done():
			u.markHungUp()
			return
		case <-time.After(wsGoneDeltaInterval):
		}
		if !send("response.output_text.delta", wsGoneDeltaEvent()) {
			u.markHungUp()
			return
		}
		u.sentDeltas.Add(1)
	}
	send("response.completed", wsGoneCompletedEvent(id, wsGoneRealInput, wsGoneRealOutput))
}

type wsGoneUsageRepo struct {
	service.UsageLogRepository
	mu   sync.Mutex
	logs []*service.UsageLog
}

func (r *wsGoneUsageRepo) Create(_ context.Context, log *service.UsageLog) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = append(r.logs, log)
	return true, nil
}

func (r *wsGoneUsageRepo) snapshot() []*service.UsageLog {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*service.UsageLog(nil), r.logs...)
}

type wsGoneBillingRepo struct {
	service.UsageBillingRepository
	mu       sync.Mutex
	commands []*service.UsageBillingCommand
}

func (r *wsGoneBillingRepo) Apply(_ context.Context, command *service.UsageBillingCommand) (*service.UsageBillingApplyResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commands = append(r.commands, command)
	return &service.UsageBillingApplyResult{Applied: true}, nil
}

func (r *wsGoneBillingRepo) snapshot() []*service.UsageBillingCommand {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*service.UsageBillingCommand(nil), r.commands...)
}

type wsGoneHTTPUpstream struct {
	service.HTTPUpstream
	client *http.Client
}

func (u *wsGoneHTTPUpstream) Do(request *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return u.client.Do(request)
}

type wsGoneHarness struct {
	url               string
	upstream          *wsGoneUpstream
	usage             *wsGoneUsageRepo
	billing           *wsGoneBillingRepo
	concurrency       *concurrencyCacheMock
	handlerDone       chan struct{}
	handlerReturnedAt atomic.Int64
}

func newWSGoneHarness(t *testing.T, mode wsGoneMode) *wsGoneHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)
	upstream := newWSGoneUpstream(t)
	groupID := int64(4401)

	extra := map[string]any{"openai_apikey_responses_websockets_v2_enabled": true}
	cfg := &config.Config{RunMode: config.RunModeStandard}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.MaxAccountSwitches = 1
	ws := &cfg.Gateway.OpenAIWS
	ws.Enabled = true
	ws.APIKeyEnabled = true
	ws.OAuthEnabled = true
	ws.ResponsesWebsocketsV2 = true
	ws.DialTimeoutSeconds = 3
	ws.ReadTimeoutSeconds = 10
	ws.WriteTimeoutSeconds = 3
	ws.IngressInterTurnIdleTimeoutSeconds = 5
	ws.MaxConnsPerAccount = 1
	ws.MinIdlePerAccount = 0
	ws.MaxIdlePerAccount = 1
	ws.QueueLimitPerConn = 8
	switch mode {
	case wsGonePassthrough:
		ws.ModeRouterV2Enabled = true
		extra["openai_apikey_responses_websockets_v2_mode"] = service.OpenAIWSIngressModePassthrough
	case wsGoneHTTPBridge:
		ws.ModeRouterV2Enabled = true
		extra["openai_apikey_responses_websockets_v2_mode"] = service.OpenAIWSIngressModeHTTPBridge
	}
	account := service.Account{
		ID: 9961, Name: "ws-client-gone-fixture", Platform: service.PlatformOpenAI,
		Type: service.AccountTypeAPIKey, Status: service.StatusActive,
		Schedulable: true, Concurrency: 1, GroupIDs: []int64{groupID},
		Credentials: map[string]any{"api_key": "sk-test", "base_url": upstream.server.URL},
		Extra:       extra,
	}

	// Admission stays off the external balance stores; settlement is the standard path.
	admissionCfg := *cfg
	admissionCfg.RunMode = config.RunModeSimple
	billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, &admissionCfg, nil)
	t.Cleanup(billingCache.Stop)
	usage := &wsGoneUsageRepo{}
	billing := &wsGoneBillingRepo{}
	concurrency := &concurrencyCacheMock{
		acquireUserSlotFn:    func(context.Context, int64, int, string) (bool, error) { return true, nil },
		acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil },
	}
	concurrencyService := service.NewConcurrencyService(concurrency)
	gateway := service.NewOpenAIGatewayService(
		&openAIWSUsageHandlerAccountRepoStub{account: account}, usage, billing,
		nil, nil, nil, nil, cfg, nil, concurrencyService,
		service.NewBillingService(cfg, nil), nil, billingCache,
		&wsGoneHTTPUpstream{client: &http.Client{}}, &service.DeferredService{},
		nil, nil, nil, nil, nil, nil, nil,
	)
	handler := NewOpenAIGatewayHandler(gateway, concurrencyService, billingCache,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
	apiKey := &service.APIKey{
		ID: 1861, UserID: 1761, Key: "fixture-ws-client-gone", GroupID: &groupID,
		User: &service.User{ID: 1761, Status: service.StatusActive, Balance: 100},
		Group: &service.Group{
			ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive,
			Hydrated: true, RateMultiplier: 1,
		},
	}

	h := &wsGoneHarness{upstream: upstream, usage: usage, billing: billing, concurrency: concurrency, handlerDone: make(chan struct{}, 4)}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyAPIKey), apiKey)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: apiKey.User.ID, Concurrency: 1})
		c.Next()
	})
	router.GET("/openai/v1/responses", func(c *gin.Context) {
		defer func() {
			h.handlerReturnedAt.Store(time.Now().UnixNano())
			select {
			case h.handlerDone <- struct{}{}:
			default:
			}
		}()
		handler.ResponsesWebSocket(c)
	})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	h.url = "ws" + strings.TrimPrefix(server.URL, "http") + "/openai/v1/responses"
	return h
}

func (h *wsGoneHarness) dial(t *testing.T) *coderws.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := coderws.Dial(ctx, h.url, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.CloseNow() })
	return conn
}

func wsGoneCreate(t *testing.T, conn *coderws.Conn, marker string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	payload := fmt.Sprintf(`{"type":"response.create","model":"gpt-5.4","input":"%s%s"}`, wsGoneInputPrefix, marker)
	require.NoError(t, conn.Write(ctx, coderws.MessageText, []byte(payload)))
}

// wsGoneReadEvents reads n frames from the gateway and returns their event types.
func wsGoneReadEvents(t *testing.T, conn *coderws.Conn, n int) []string {
	t.Helper()
	types := make([]string, 0, n)
	for len(types) < n {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, frame, err := conn.Read(ctx)
		cancel()
		require.NoError(t, err, "frame %d must reach the client", len(types)+1)
		types = append(types, gjson.GetBytes(frame, "type").String())
	}
	return types
}

func wsGoneReadUntilCompleted(t *testing.T, conn *coderws.Conn) {
	t.Helper()
	for i := 0; i < 8; i++ {
		if types := wsGoneReadEvents(t, conn, 1); types[0] == "response.completed" {
			return
		}
	}
	t.Fatal("the quick turn never completed")
}

// awaitHangUp reports how long after closedAt the upstream was cut loose.
func (h *wsGoneHarness) awaitHangUp(t *testing.T, closedAt time.Time) {
	t.Helper()
	select {
	case <-h.upstream.hungUp:
		elapsed := time.Duration(h.upstream.hungUpAt.Load() - closedAt.UnixNano())
		assert.LessOrEqual(t, elapsed, wsGoneHangUpBudget, "the upstream request must end right after the client left")
	case <-time.After(wsGoneHangUpBudget + 2*time.Second):
		assert.Fail(t, "the upstream request was never cut: the gateway kept draining a turn nobody was reading")
	}
}

func (h *wsGoneHarness) awaitHandler(t *testing.T, closedAt time.Time) {
	t.Helper()
	select {
	case <-h.handlerDone:
		elapsed := time.Duration(h.handlerReturnedAt.Load() - closedAt.UnixNano())
		assert.LessOrEqual(t, elapsed, wsGoneHandlerBudget, "the handler must not outlive the client by a drain window")
	case <-time.After(10 * time.Second):
		t.Fatal("websocket handler did not return")
	}
}

// requireEstimatedBill checks one usage record is the estimate for a turn cut off
// after the client had read minDeltas deltas: input from the request body with 95%
// priced as cache read, output from the streamed content, never the real usage the
// terminal frame would have reported.
func (h *wsGoneHarness) requireEstimatedBill(t *testing.T, index int, requestBody []byte, minDeltas int) {
	t.Helper()
	logs, commands := h.usage.snapshot(), h.billing.snapshot()
	require.Greater(t, len(logs), index, "the cut-off turn must produce a usage record")
	require.Greater(t, len(commands), index, "the cut-off turn must reach billing")
	log, command := logs[index], commands[index]

	total, err := service.EstimateOpenAIResponsesInputTokens(requestBody)
	require.NoError(t, err)
	require.Positive(t, total)
	cacheRead := total * 95 / 100
	assert.Equal(t, cacheRead, log.CacheReadTokens, "95%% of the estimated input is priced as cache read")
	assert.Equal(t, total-cacheRead, log.InputTokens, "the other 5%% is normal input")

	sent := int(h.upstream.sentDeltas.Load())
	assert.Zero(t, log.OutputTokens%wsGoneDeltaTokens, "output is estimated from whole deltas")
	assert.GreaterOrEqual(t, log.OutputTokens, minDeltas*wsGoneDeltaTokens, "everything the client had read is billed")
	assert.LessOrEqual(t, log.OutputTokens, (sent+1)*wsGoneDeltaTokens, "nothing beyond what the upstream produced is billed")
	assert.Less(t, log.OutputTokens, wsGoneMaxDeltas/2*wsGoneDeltaTokens, "the turn was cut well before the upstream finished")

	assert.Positive(t, command.BalanceCost, "the cut-off turn is not free")
	assert.Less(t, command.BalanceCost, 0.5, "a cut-off turn must not be charged the usage of a finished one")
	assert.True(t, log.OpenAIWSMode)
}

// The client leaves in the middle of a turn while the upstream is still generating.
func TestOpenAIResponsesWebSocketClientGoneMidTurnCutsUpstreamAndBillsEstimate(t *testing.T) {
	for _, mode := range wsGoneModes {
		t.Run(string(mode), func(t *testing.T) {
			h := newWSGoneHarness(t, mode)
			conn := h.dial(t)
			wsGoneCreate(t, conn, wsGoneLongMarker)
			wsGoneReadEvents(t, conn, 3)
			closedAt := time.Now()
			require.NoError(t, conn.CloseNow())

			h.awaitHangUp(t, closedAt)
			h.awaitHandler(t, closedAt)

			require.Equal(t, 1, h.upstream.requestCount())
			h.requireEstimatedBill(t, 0, h.upstream.request(0), 2)
			require.Len(t, h.usage.snapshot(), 1, "one turn is billed exactly once")
			require.Eventually(t, func() bool {
				return atomic.LoadInt32(&h.concurrency.releaseUserCalled) >= 1 &&
					atomic.LoadInt32(&h.concurrency.releaseAccountCalled) >= 1
			}, 2*time.Second, 10*time.Millisecond, "the user and account slots are released")
		})
	}
}

// A finished first turn is settled with its real usage and a second turn cut off
// mid-way is billed an estimate: nothing lost, nothing counted twice.
func TestOpenAIResponsesWebSocketClientGoneOnLaterTurnKeepsEarlierSettlement(t *testing.T) {
	for _, mode := range wsGoneModes {
		t.Run(string(mode), func(t *testing.T) {
			h := newWSGoneHarness(t, mode)
			conn := h.dial(t)
			wsGoneCreate(t, conn, wsGoneQuickMarker)
			wsGoneReadUntilCompleted(t, conn)
			wsGoneCreate(t, conn, wsGoneLongMarker)
			wsGoneReadEvents(t, conn, 3)
			closedAt := time.Now()
			require.NoError(t, conn.CloseNow())

			h.awaitHangUp(t, closedAt)
			h.awaitHandler(t, closedAt)

			require.Equal(t, 2, h.upstream.requestCount())
			logs := h.usage.snapshot()
			require.Len(t, logs, 2, "each turn is billed exactly once")
			assert.Equal(t, 10, logs[0].InputTokens, "the finished turn keeps its real usage")
			assert.Equal(t, 5, logs[0].OutputTokens)
			assert.Zero(t, logs[0].CacheReadTokens)
			h.requireEstimatedBill(t, 1, h.upstream.request(1), 2)
		})
	}
}

// The client leaves after a turn finished, with no turn in flight: the session
// just ends and nothing is estimated.
func TestOpenAIResponsesWebSocketClientGoneBetweenTurnsIsNotEstimated(t *testing.T) {
	for _, mode := range wsGoneModes {
		t.Run(string(mode), func(t *testing.T) {
			h := newWSGoneHarness(t, mode)
			conn := h.dial(t)
			wsGoneCreate(t, conn, wsGoneQuickMarker)
			wsGoneReadUntilCompleted(t, conn)
			closedAt := time.Now()
			_ = conn.Close(coderws.StatusNormalClosure, "done")

			h.awaitHandler(t, closedAt)

			logs, commands := h.usage.snapshot(), h.billing.snapshot()
			require.Len(t, logs, 1)
			require.Len(t, commands, 1)
			assert.Equal(t, 10, logs[0].InputTokens)
			assert.Equal(t, 5, logs[0].OutputTokens)
			assert.Zero(t, logs[0].CacheReadTokens, "reported usage is never re-split")
			assert.Positive(t, commands[0].BalanceCost, "a finished turn is billed")
			time.Sleep(300 * time.Millisecond)
			assert.Len(t, h.usage.snapshot(), 1, "leaving between turns adds no charge")
			assert.Len(t, h.billing.snapshot(), 1)
		})
	}
}
