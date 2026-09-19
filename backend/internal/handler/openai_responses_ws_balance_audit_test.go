//go:build unit

package handler

import (
	"context"
	"errors"
	"fmt"
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
	"github.com/stretchr/testify/require"
)

type wsBillingAuditCache struct {
	service.BillingCache
	mu            sync.Mutex
	balance       float64
	balanceReads  int
	subscription  *service.SubscriptionCacheData
	subReads      int
	invalidations int
}

func (c *wsBillingAuditCache) GetUserBalance(context.Context, int64) (float64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.balanceReads++
	return c.balance, nil
}

func (c *wsBillingAuditCache) SetUserBalance(_ context.Context, _ int64, balance float64) error {
	c.mu.Lock()
	c.balance = balance
	c.mu.Unlock()
	return nil
}

func (c *wsBillingAuditCache) SetUserBalanceIfLower(_ context.Context, _ int64, balance float64) error {
	c.mu.Lock()
	if balance < c.balance {
		c.balance = balance
	}
	c.mu.Unlock()
	return nil
}

func (c *wsBillingAuditCache) GetSubscriptionCache(context.Context, int64, int64) (*service.SubscriptionCacheData, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.subReads++
	if c.subscription == nil {
		return nil, errors.New("subscription cache miss")
	}
	copy := *c.subscription
	return &copy, nil
}

func (c *wsBillingAuditCache) SetSubscriptionCache(_ context.Context, _, _ int64, data *service.SubscriptionCacheData) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if data == nil {
		c.subscription = nil
		return nil
	}
	copy := *data
	c.subscription = &copy
	return nil
}

func (c *wsBillingAuditCache) UpdateSubscriptionUsage(_ context.Context, _, _ int64, cost float64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.subscription != nil {
		c.subscription.DailyUsage += cost
		c.subscription.WeeklyUsage += cost
		c.subscription.MonthlyUsage += cost
	}
	return nil
}

func (c *wsBillingAuditCache) InvalidateSubscriptionCache(context.Context, int64, int64) error {
	c.mu.Lock()
	c.subscription = nil
	c.invalidations++
	c.mu.Unlock()
	return nil
}

func (c *wsBillingAuditCache) snapshot() (balance float64, balanceReads, subReads, invalidations int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.balance, c.balanceReads, c.subReads, c.invalidations
}

type wsBillingAuditSubscriptionRepo struct {
	service.UserSubscriptionRepository
	mu  sync.Mutex
	sub service.UserSubscription
}

func (r *wsBillingAuditSubscriptionRepo) GetActiveByUserIDAndGroupID(context.Context, int64, int64) (*service.UserSubscription, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	copy := r.sub
	return &copy, nil
}

func (r *wsBillingAuditSubscriptionRepo) exhaust(limit float64) {
	r.mu.Lock()
	r.sub.DailyUsageUSD = limit
	r.sub.WeeklyUsageUSD = limit
	r.sub.MonthlyUsageUSD = limit
	r.mu.Unlock()
}

func (r *wsBillingAuditSubscriptionRepo) expire() {
	r.mu.Lock()
	r.sub.ExpiresAt = time.Now().Add(-time.Minute)
	r.mu.Unlock()
}

type wsBillingAuditLedger struct {
	service.UsageBillingRepository
	cache            *wsBillingAuditCache
	firstApplyErr    error
	balanceAfter     *float64
	subscriptionStep func()
	firstSettled     chan struct{}
	once             sync.Once
	applyCalls       atomic.Int32
}

func (l *wsBillingAuditLedger) Apply(_ context.Context, _ *service.UsageBillingCommand) (*service.UsageBillingApplyResult, error) {
	call := l.applyCalls.Add(1)
	defer l.once.Do(func() { close(l.firstSettled) })
	if call == 1 && l.firstApplyErr != nil {
		return nil, l.firstApplyErr
	}
	if call == 1 && l.subscriptionStep != nil {
		l.subscriptionStep()
	}
	result := &service.UsageBillingApplyResult{Applied: true}
	if l.balanceAfter != nil {
		balance := *l.balanceAfter
		_ = l.cache.SetUserBalance(context.Background(), 0, balance)
		result.NewBalance = &balance
	}
	return result, nil
}

type wsBillingAdmissionCase struct {
	subscriptionMode bool
	mutateFirstBill  string
	settlementErr    error
	wantCloseStatus  coderws.StatusCode
	wantCloseReason  string
	wantSecondTurn   bool
}

type wsBillingAdmissionResult struct {
	upstreamTurns int32
	balance       float64
	balanceReads  int
	subReads      int
	invalidations int
	applyCalls    int32
}

func runWSBillingAdmissionCase(t *testing.T, tc wsBillingAdmissionCase) wsBillingAdmissionResult {
	t.Helper()
	gin.SetMode(gin.TestMode)

	var upstreamTurns atomic.Int32
	upstreamErr := make(chan error, 1)
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{CompressionMode: coderws.CompressionContextTakeover})
		if err != nil {
			upstreamErr <- err
			return
		}
		defer func() { _ = conn.CloseNow() }()
		for turn := 1; turn <= 2; turn++ {
			readCtx, cancelRead := context.WithTimeout(r.Context(), 3*time.Second)
			_, _, readErr := conn.Read(readCtx)
			cancelRead()
			if readErr != nil {
				if turn == 2 {
					upstreamErr <- nil
					return
				}
				upstreamErr <- readErr
				return
			}
			upstreamTurns.Add(1)
			response := fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp_billing_audit_%d","model":"gpt-5.1","usage":{"input_tokens":2,"output_tokens":1}}}`, turn)
			writeCtx, cancelWrite := context.WithTimeout(r.Context(), 3*time.Second)
			writeErr := conn.Write(writeCtx, coderws.MessageText, []byte(response))
			cancelWrite()
			if writeErr != nil {
				upstreamErr <- writeErr
				return
			}
		}
		upstreamErr <- nil
	}))
	defer upstreamServer.Close()

	groupID := int64(4209)
	userID := int64(1709)
	group := &service.Group{ID: groupID, Name: "ws-billing-audit", Platform: service.PlatformOpenAI, Status: service.StatusActive, Hydrated: true}
	cache := &wsBillingAuditCache{balance: 1}
	var subscription *service.UserSubscription
	var subscriptionRepo *wsBillingAuditSubscriptionRepo
	if tc.subscriptionMode {
		limit := 1.0
		group.SubscriptionType = service.SubscriptionTypeSubscription
		group.DailyLimitUSD = &limit
		subscription = &service.UserSubscription{
			ID: 8109, UserID: userID, GroupID: groupID, Status: service.SubscriptionStatusActive,
			StartsAt: time.Now().Add(-time.Hour), ExpiresAt: time.Now().Add(time.Hour),
		}
		subscriptionRepo = &wsBillingAuditSubscriptionRepo{sub: *subscription}
		cache.subscription = &service.SubscriptionCacheData{Status: service.SubscriptionStatusActive, ExpiresAt: subscription.ExpiresAt}
	}

	zero := 0.0
	positive := 1.0
	ledger := &wsBillingAuditLedger{cache: cache, firstApplyErr: tc.settlementErr, firstSettled: make(chan struct{})}
	switch tc.mutateFirstBill {
	case "balance_exhausted":
		ledger.balanceAfter = &zero
	case "balance_positive":
		ledger.balanceAfter = &positive
	case "subscription_exhausted":
		ledger.subscriptionStep = func() { subscriptionRepo.exhaust(*group.DailyLimitUSD) }
	case "subscription_expired":
		ledger.subscriptionStep = subscriptionRepo.expire
	}

	accountRepo := &openAIWSUsageHandlerAccountRepoStub{account: service.Account{
		ID: 9909, Name: "openai-ws-billing-audit", Platform: service.PlatformOpenAI,
		Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{
			"api_key": "sk-test", "base_url": upstreamServer.URL,
			"model_mapping": map[string]any{"gpt-5.1": "gpt-5.1"},
		},
		Extra: map[string]any{
			"openai_apikey_responses_websockets_v2_enabled": true,
			"openai_apikey_responses_websockets_v2_mode":    service.OpenAIWSIngressModePassthrough,
		},
	}}
	cfg := &config.Config{RunMode: config.RunModeStandard}
	cfg.Default.RateMultiplier = 1
	cfg.Billing.BalancePreauthorizationEnabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.APIKeyEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
	cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3

	billingCache := service.NewBillingCacheService(cache, nil, subscriptionRepo, nil, nil, nil, cfg, nil)
	defer billingCache.Stop()
	usageRepo := &openAIWSUsageHandlerUsageLogRepoStub{created: make(chan *service.UsageLog, 2)}
	gateway := service.NewOpenAIGatewayService(
		accountRepo, usageRepo, ledger, nil, subscriptionRepo, nil, nil, cfg, nil, nil,
		service.NewBillingService(cfg, nil), nil, billingCache, nil, &service.DeferredService{},
		nil, nil, nil, nil, nil, nil, nil,
	)
	concurrency := &concurrencyCacheMock{
		acquireUserSlotFn:    func(context.Context, int64, int, string) (bool, error) { return true, nil },
		acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil },
	}
	h := &OpenAIGatewayHandler{
		gatewayService: gateway, billingCacheService: billingCache, apiKeyService: &service.APIKeyService{},
		concurrencyHelper: NewConcurrencyHelper(service.NewConcurrencyService(concurrency), SSEPingFormatNone, time.Second),
	}
	apiKey := &service.APIKey{
		ID: 1809, UserID: userID, GroupID: &groupID, Group: group,
		User: &service.User{ID: userID, Status: service.StatusActive, Balance: 1},
	}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyAPIKey), apiKey)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: userID, Concurrency: 1})
		if subscription != nil {
			c.Set(string(middleware.ContextKeySubscription), subscription)
		}
		c.Next()
	})
	router.GET("/openai/v1/responses", h.ResponsesWebSocket)
	handlerServer := httptest.NewServer(router)
	defer handlerServer.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	client, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(handlerServer.URL, "http")+"/openai/v1/responses", nil)
	cancelDial()
	require.NoError(t, err)
	defer func() { _ = client.CloseNow() }()

	writeTurn := func(payload string) {
		writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancelWrite()
		require.NoError(t, client.Write(writeCtx, coderws.MessageText, []byte(payload)))
	}
	readTurn := func() error {
		readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancelRead()
		_, _, readErr := client.Read(readCtx)
		return readErr
	}

	writeTurn(`{"type":"response.create","model":"gpt-5.1","input":"first"}`)
	require.NoError(t, readTurn())
	select {
	case <-ledger.firstSettled:
	case <-time.After(3 * time.Second):
		t.Fatal("first WebSocket turn did not finish billing")
	}

	writeTurn(`{"type":"response.create","model":"gpt-5.1","input":"second"}`)
	secondErr := readTurn()
	if tc.wantSecondTurn {
		require.NoError(t, secondErr)
	} else {
		require.Error(t, secondErr)
		require.NotErrorIs(t, secondErr, context.DeadlineExceeded)
		if status := coderws.CloseStatus(secondErr); status != -1 {
			require.Equal(t, tc.wantCloseStatus, status)
			require.Contains(t, strings.ToLower(secondErr.Error()), strings.ToLower(tc.wantCloseReason))
		}
	}

	if tc.wantSecondTurn {
		_ = client.Close(coderws.StatusNormalClosure, "done")
	}
	select {
	case serverErr := <-upstreamErr:
		require.NoError(t, serverErr)
	case <-time.After(3 * time.Second):
		t.Fatal("upstream websocket did not finish")
	}
	balance, balanceReads, subReads, invalidations := cache.snapshot()
	return wsBillingAdmissionResult{
		upstreamTurns: upstreamTurns.Load(), balance: balance, balanceReads: balanceReads,
		subReads: subReads, invalidations: invalidations, applyCalls: ledger.applyCalls.Load(),
	}
}

func TestOpenAIResponsesWebSocketRejectsSecondTurnAfterBalanceExhaustion(t *testing.T) {
	result := runWSBillingAdmissionCase(t, wsBillingAdmissionCase{
		mutateFirstBill: "balance_exhausted",
		wantCloseStatus: coderws.StatusPolicyViolation, wantCloseReason: "billing check failed",
	})
	require.Zero(t, result.balance)
	require.Equal(t, int32(1), result.upstreamTurns)
	require.Equal(t, 2, result.balanceReads, "connection admission and second-turn admission must both read balance")
}

func TestOpenAIResponsesWebSocketRejectsExhaustedOrExpiredSubscription(t *testing.T) {
	tests := []struct {
		name   string
		mutate string
	}{
		{name: "daily quota exhausted", mutate: "subscription_exhausted"},
		{name: "subscription expired", mutate: "subscription_expired"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := runWSBillingAdmissionCase(t, wsBillingAdmissionCase{
				subscriptionMode: true, mutateFirstBill: tt.mutate,
				wantCloseStatus: coderws.StatusPolicyViolation, wantCloseReason: "billing check failed",
			})
			require.Equal(t, int32(1), result.upstreamTurns)
			require.GreaterOrEqual(t, result.subReads, 2)
			require.Equal(t, 1, result.invalidations, "successful subscription settlement must invalidate the stale per-turn view")
		})
	}
}

func TestOpenAIResponsesWebSocketRejectsSecondTurnAfterSettlementFailure(t *testing.T) {
	result := runWSBillingAdmissionCase(t, wsBillingAdmissionCase{
		settlementErr:   errors.New("forced settlement failure"),
		wantCloseStatus: coderws.StatusInternalError, wantCloseReason: "previous turn billing failed",
	})
	require.Equal(t, int32(1), result.upstreamTurns)
	require.Equal(t, int32(1), result.applyCalls)
}

func TestOpenAIResponsesWebSocketAllowsNormalSecondTurn(t *testing.T) {
	result := runWSBillingAdmissionCase(t, wsBillingAdmissionCase{
		mutateFirstBill: "balance_positive", wantSecondTurn: true,
	})
	require.Equal(t, int32(2), result.upstreamTurns)
	require.Equal(t, int32(2), result.applyCalls)
	require.Equal(t, 2, result.balanceReads, "duplicate passthrough callbacks must not repeat second-turn admission")
}
