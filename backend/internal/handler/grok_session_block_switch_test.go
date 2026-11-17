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

// The identifier-based lock is switched off in production (see
// service.SetGrokModerationSessionBlockEnabled). The handler tests keep it on
// so the remaining code for it stays covered; the test below pins what
// production does.
func init() { service.SetGrokModerationSessionBlockEnabled(true) }

// A refusal locks the refused conversation by its content and nothing else:
// the same client identifier with other content is served, and a lock that an
// earlier version wrote for the identifier is ignored.
func TestGrokRefusalLocksOnlyTheRefusedConversation(t *testing.T) {
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
	// Every request carries the same client identifiers, the way a relay reuses
	// one x-grok-conv-id for most of a customer's traffic.
	call := func(messages string) *httptest.ResponseRecorder {
		body := `{"model":"grok-4.7","stream":false,"messages":` + messages + `,"max_tokens":128}`
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.ClientRequestID, "refusal-fixture"))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Request.Header.Set("Session_id", "shared-identifier")
		c.Request.Header.Set("x-grok-conv-id", "shared-identifier")
		c.Set(string(middleware.ContextKeyAPIKey), key)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 77})
		h.ChatCompletions(c)
		return rec
	}
	const refused = `[{"role":"user","content":"refused request"}]`
	const continued = `[{"role":"user","content":"refused request"},{"role":"assistant","content":"earlier answer"},{"role":"user","content":"go on"}]`
	const unrelated = `[{"role":"user","content":"an unrelated conversation"}]`

	// A lock written by an earlier version for the shared identifier.
	probe, _ := gin.CreateTestContext(httptest.NewRecorder())
	probe.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	probe.Request.Header.Set("Session_id", "shared-identifier")
	staleHash := service.GrokModerationSessionHash(probe, key, nil)
	require.NotEmpty(t, staleHash)
	require.NoError(t, cache.BlockSession(context.Background(), 0, staleHash, 0))

	first := call(refused)
	require.Equal(t, http.StatusForbidden, first.Code, first.Body.String())
	require.Equal(t, refusal, first.Body.String(), "the identifier lock of an earlier version must not stop the request locally")
	require.Equal(t, 1, upstream.calls)
	require.NotNil(t, ledger.last)
	require.InDelta(t, price, ledger.last.BalanceCost, 1e-8, "the refusal itself is charged once")
	firstCharge := ledger.last

	again := call(refused)
	require.Equal(t, http.StatusForbidden, again.Code, again.Body.String())
	require.Equal(t, 1, upstream.calls, "resending the refused conversation is stopped locally")
	require.Same(t, firstCharge, ledger.last, "and is not charged again")

	next := call(continued)
	require.Equal(t, http.StatusForbidden, next.Code, next.Body.String())
	require.Equal(t, 1, upstream.calls, "continuing the refused conversation is stopped locally too")
	require.Same(t, firstCharge, ledger.last)

	other := call(unrelated)
	require.Equal(t, 2, upstream.calls, "another conversation with the same identifiers must reach the provider: %s", other.Body.String())
}
