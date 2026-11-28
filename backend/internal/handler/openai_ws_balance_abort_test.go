//go:build unit

package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestShouldReportOpenAIWSProxyAccountFailure_BalanceErrorsExcluded(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "direct ErrBalanceWithholdingFailed",
			err:  service.ErrBalanceWithholdingFailed,
			want: false,
		},
		{
			name: "wrapped stream output hold top-up ErrBalanceWithholdingFailed",
			err:  fmt.Errorf("stream output hold top-up failed: %w", service.ErrBalanceWithholdingFailed),
			want: false,
		},
		{
			name: "wrapped stream output hold top-up ErrBillingServiceUnavailable",
			err:  fmt.Errorf("stream output hold top-up failed: %w", service.ErrBillingServiceUnavailable),
			want: false,
		},
		{
			name: "client close error wrapping ErrBalanceWithholdingFailed (Turn 2 rejection)",
			err:  service.NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "insufficient balance", service.ErrBalanceWithholdingFailed),
			want: false,
		},
		{
			name: "client close error wrapping ErrInsufficientBalance",
			err:  service.NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "billing check failed", service.ErrInsufficientBalance),
			want: false,
		},
		{
			name: "real upstream failure still attributed to account",
			err:  errors.New("upstream websocket read failed"),
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, shouldReportOpenAIWSProxyAccountFailure(tc.err))
		})
	}
}

func TestOpenAIWSBalanceAbortCloseStatusAndReason(t *testing.T) {
	status, reason := openAIWSBalanceAbortCloseStatusAndReason(
		fmt.Errorf("stream output hold top-up failed: %w", service.ErrBalanceWithholdingFailed),
	)
	require.Equal(t, coderws.StatusPolicyViolation, status)
	require.Equal(t, "insufficient balance", reason)

	status, reason = openAIWSBalanceAbortCloseStatusAndReason(
		fmt.Errorf("stream output hold top-up failed: %w", service.ErrBillingServiceUnavailable),
	)
	require.Equal(t, coderws.StatusTryAgainLater, status)
	require.NotEmpty(t, reason)
}

type wsBalanceAbortAffinitySpyCache struct {
	service.GatewayCache
	deleteCalls atomic.Int32
}

func (c *wsBalanceAbortAffinitySpyCache) GetSessionAccountID(context.Context, int64, string) (int64, error) {
	return 0, errors.New("cache miss")
}

func (c *wsBalanceAbortAffinitySpyCache) SetSessionAccountID(context.Context, int64, string, int64, time.Duration) error {
	return nil
}

func (c *wsBalanceAbortAffinitySpyCache) RefreshSessionTTL(context.Context, int64, string, time.Duration) error {
	return nil
}

func (c *wsBalanceAbortAffinitySpyCache) DeleteSessionAccountID(context.Context, int64, string) error {
	c.deleteCalls.Add(1)
	return nil
}

func TestOpenAIWSResponsesWebSocket_BalanceTopUpAbortSettlesPartialAndSkipsAffinityInvalidation(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const pricingJSON = `{
		"gpt-5.1": {
			"litellm_provider": "openai",
			"mode": "chat",
			"input_cost_per_token": 0.00001,
			"output_cost_per_token": 0.00002
		}
	}`

	groupID := int64(4211)
	userID := int64(1711)

	cfg, _, billingSvc := newPreauthTestPricingService(t, pricingJSON)
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.APIKeyEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
	cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3

	ledger := newPreauthEntrypointsLedger(userID, 5.0)
	preauth := newPreauthTestService(t, cfg, billingSvc, ledger)
	usageLogs := &preauthEntrypointsUsageLogRepo{}
	affinityCache := &wsBalanceAbortAffinitySpyCache{}

	upstreamDone := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(upstreamDone)
		conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{CompressionMode: coderws.CompressionContextTakeover})
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()

		readCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		_, _, err = conn.Read(readCtx)
		cancel()
		if err != nil {
			return
		}

		// Drain the user's live_wallet right after Turn 1 admission so the first
		// streaming hold top-up fails with ErrBalanceWithholdingFailed mid-turn.
		ledger.setBalance(userID, 0.000001)

		bigDelta := strings.Repeat("word ", 260) // ~1300 bytes (~325 tokens > 100 initial window)
		frames := []string{
			`{"type":"response.created","response":{"id":"resp_ws_abort_1","model":"gpt-5.1","status":"in_progress"}}`,
			fmt.Sprintf(`{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":%q}`, bigDelta),
			fmt.Sprintf(`{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":%q}`, bigDelta),
			`{"type":"response.completed","response":{"id":"resp_ws_abort_1","model":"gpt-5.1","status":"completed","usage":{"input_tokens":10,"output_tokens":700}}}`,
		}
		for _, frame := range frames {
			wCtx, wCancel := context.WithTimeout(r.Context(), 3*time.Second)
			err = conn.Write(wCtx, coderws.MessageText, []byte(frame))
			wCancel()
			if err != nil {
				return
			}
		}
	}))
	defer upstream.Close()

	group := &service.Group{
		ID: groupID, Name: "ws-preauth-abort", Platform: service.PlatformOpenAI,
		Status: service.StatusActive, Hydrated: true, RateMultiplier: 1.0,
	}
	accountRepo := &openAIWSUsageHandlerAccountRepoStub{account: service.Account{
		ID: 9911, Name: "openai-ws-preauth-abort", Platform: service.PlatformOpenAI,
		Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 2,
		Credentials: map[string]any{
			"api_key":       "test-key-must-not-leak",
			"base_url":      upstream.URL,
			"model_mapping": map[string]any{"gpt-5.1": "gpt-5.1"},
		},
		Extra: map[string]any{
			"openai_apikey_responses_websockets_v2_enabled": true,
			"openai_apikey_responses_websockets_v2_mode":    service.OpenAIWSIngressModeCtxPool,
		},
	}}
	bcs := service.NewBillingCacheService(&preauthEntrypointsBillingCache{l: ledger}, nil, nil, nil, nil, nil, cfg, nil)
	defer bcs.Stop()

	gateway := service.NewOpenAIGatewayService(
		accountRepo, usageLogs, ledger, nil, nil, nil, affinityCache, cfg, nil, nil,
		billingSvc, nil, bcs, nil, &service.DeferredService{},
		nil, nil, nil, nil, nil, nil, nil,
	)
	defer gateway.CloseOpenAIWSPool()

	concurrency := &concurrencyCacheMock{
		acquireUserSlotFn:    func(context.Context, int64, int, string) (bool, error) { return true, nil },
		acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil },
	}
	h := &OpenAIGatewayHandler{
		gatewayService:      gateway,
		billingCacheService: bcs,
		apiKeyService:       &service.APIKeyService{},
		concurrencyHelper:   NewConcurrencyHelper(service.NewConcurrencyService(concurrency), SSEPingFormatNone, time.Second),
	}
	h.balancePreauthorizer = preauth
	apiKey := &service.APIKey{
		ID: 1811, UserID: userID, GroupID: &groupID, Group: group, Status: service.StatusAPIKeyActive,
		User: &service.User{ID: userID, Status: service.StatusActive, Balance: 1000.0},
	}

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware2.ContextKeyAPIKey), apiKey)
		c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: userID, Concurrency: 2})
		c.Next()
	})
	router.GET("/openai/v1/responses", h.ResponsesWebSocket)
	srv := httptest.NewServer(router)
	defer srv.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	client, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/openai/v1/responses", nil)
	cancelDial()
	require.NoError(t, err)
	defer func() { _ = client.CloseNow() }()

	turn1Frame := []byte(`{"type":"response.create","model":"gpt-5.1","input":"Write a long explanation.","max_output_tokens":100}`)
	wCtx, wCancel := context.WithTimeout(context.Background(), 3*time.Second)
	require.NoError(t, client.Write(wCtx, coderws.MessageText, turn1Frame))
	wCancel()

	var closeStatus coderws.StatusCode
	var closeReason string
	var receivedDelta bool
	for {
		rCtx, rCancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, msg, rErr := client.Read(rCtx)
		rCancel()
		if rErr != nil {
			var cerr coderws.CloseError
			if errors.As(rErr, &cerr) {
				closeStatus, closeReason = cerr.Code, cerr.Reason
			}
			break
		}
		if strings.Contains(string(msg), "response.output_text.delta") {
			receivedDelta = true
		}
	}
	select {
	case <-upstreamDone:
	case <-time.After(2 * time.Second):
	}

	require.True(t, receivedDelta, "client must have received at least one output delta before abort")
	require.Equal(t, coderws.StatusPolicyViolation, closeStatus, "balance abort must close WS with 1008 PolicyViolation, not 1011")
	require.Equal(t, "insufficient balance", closeReason)

	require.Eventually(t, func() bool {
		return len(usageLogs.snapshot()) >= 1 && len(ledger.finalizedSnapshot()) >= 1
	}, 2*time.Second, 10*time.Millisecond, "AfterTurn must settle delivered partial output rather than dropping or refunding it")

	logs := usageLogs.snapshot()
	require.Len(t, logs, 1)
	require.Greater(t, logs[0].OutputTokens, 100, "partial output delivered before abort must be billed")
	require.Greater(t, logs[0].ActualCost, 0.0)
	require.Empty(t, ledger.refundedSnapshot(), "delivered output must be finalized, never 100%% refunded")
	require.Zero(t, ledger.activeHoldCount())
	require.Zero(t, affinityCache.deleteCalls.Load(), "client balance exhaustion must not invalidate healthy upstream affinity")
}
