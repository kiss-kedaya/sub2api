//go:build unit

package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// The conversation lock is switched off in production (see
// service.SetGrokModerationSessionBlockEnabled). The handler tests written for
// the lock keep running with it on so that code stays covered until the
// matching is fixed; the test below pins what production does now.
func init() { service.SetGrokModerationSessionBlockEnabled(true) }

func TestGrokRefusalDoesNotLockTheConversationWhileTheLockIsOff(t *testing.T) {
	defer service.SetGrokModerationSessionBlockEnabled(false)()
	gin.SetMode(gin.TestMode)
	const refusal = `{"error":{"message":"I'm sorry, I can't help with that request.","type":"upstream_error"}}`
	groupID := int64(1234)
	price := 0.1
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
	admissionCfg.RunMode = config.RunModeSimple
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, &admissionCfg, nil)
	t.Cleanup(billing.Stop)
	svc := service.NewOpenAIGatewayService(accounts, logs, ledger, nil, nil, nil, cache, cfg, nil, nil,
		service.NewBillingService(cfg, nil), nil, billing, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
	h := NewOpenAIGatewayHandler(svc, service.NewConcurrencyService(nil), billing,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
	key := &service.APIKey{ID: 55, UserID: 77, GroupID: &groupID, User: &service.User{ID: 77, Balance: 100, Status: service.StatusActive},
		Group: &service.Group{ID: groupID, Platform: service.PlatformGrok, Status: service.StatusActive, Hydrated: true,
			CrossProtocolConversionEnabled: true, RateMultiplier: 0.07, GrokModerationRefusalPrice: &price}}
	call := func() *httptest.ResponseRecorder {
		body := `{"model":"grok-4.7","stream":false,"messages":[{"role":"user","content":"hello"}],"max_tokens":128}`
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.ClientRequestID, "refusal-fixture"))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Request.Header.Set("Session_id", "refused-conversation")
		c.Set(string(middleware.ContextKeyAPIKey), key)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 77})
		h.ChatCompletions(c)
		return rec
	}

	// A lock written by an earlier version must be ignored.
	probe, _ := gin.CreateTestContext(httptest.NewRecorder())
	probe.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	probe.Request.Header.Set("Session_id", "refused-conversation")
	staleHash := service.GrokModerationSessionHash(probe, key, nil)
	require.NotEmpty(t, staleHash)
	require.NoError(t, cache.BlockSession(context.Background(), 0, staleHash, 0))

	first := call()
	require.Equal(t, http.StatusForbidden, first.Code, first.Body.String())
	require.Equal(t, refusal, first.Body.String(), "the request must reach the provider and return its refusal")
	require.Equal(t, 1, upstream.calls, "a lock from an earlier version must not stop the request locally")
	require.NotNil(t, ledger.last)
	require.InDelta(t, price, ledger.last.BalanceCost, 1e-8, "the refusal fee itself is unchanged")

	second := call()
	require.Equal(t, http.StatusForbidden, second.Code, second.Body.String())
	require.Equal(t, 2, upstream.calls, "the same conversation is asked again instead of being rejected locally")
	require.Len(t, cache.blocked, 1, "no new lock is written")
}
