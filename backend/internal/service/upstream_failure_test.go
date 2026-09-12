package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestClassifyUpstreamFailure_WAFIsNotCooling(t *testing.T) {
	class := ClassifyUpstreamFailure(http.StatusForbidden, nil, []byte("error code: 1010"), nil)
	require.Equal(t, UpstreamFailureWAF, class.Kind)
	require.True(t, class.Failover)
	require.False(t, class.SameAccountRetry)
	require.False(t, class.PunishAccount)
	require.False(t, IsUpstreamCapacityCoolingBody([]byte("error code: 1010")))
	require.True(t, IsUpstreamWAFBody([]byte("error code: 1010")))
}

func TestClassifyUpstreamFailure_ProviderCooling(t *testing.T) {
	body := []byte(`{"code":"FORBIDDEN","message":"当前分组内支持该模型的货源均在冷却中。请稍后重试"}`)
	class := ClassifyUpstreamFailure(http.StatusForbidden, nil, body, nil)
	require.Equal(t, UpstreamFailureCapacity, class.Kind)
	require.True(t, class.Failover)
	require.True(t, class.SameAccountRetry)
	require.False(t, class.PunishAccount)
}

func TestClassifyUpstreamFailure_CredentialForbidden(t *testing.T) {
	body := []byte(`{"error":{"code":"FORBIDDEN","message":"invalid api key"}}`)
	class := ClassifyUpstreamFailure(http.StatusForbidden, nil, body, nil)
	require.Equal(t, UpstreamFailureAuth, class.Kind)
	require.True(t, class.PunishAccount)
}

func TestClassifyUpstreamFailure_ModelNotFound(t *testing.T) {
	body := []byte(`{"error":{"code":"model_not_found","message":"model not found"}}`)
	class := ClassifyUpstreamFailure(http.StatusBadRequest, nil, body, nil)
	require.Equal(t, UpstreamFailureModelMissing, class.Kind)
	require.True(t, class.Failover)
}

func TestClassifyUpstreamFailure_Canceled(t *testing.T) {
	class := ClassifyUpstreamFailure(0, nil, nil, context.Canceled)
	require.Equal(t, UpstreamFailureCanceled, class.Kind)
	require.False(t, class.Failover)
}

func TestClassifyUpstreamFailure_WrappedJSONObjectIsClient(t *testing.T) {
	body := []byte(`{"error":{"message":"Response input messages must contain the word 'json' in some form to use 'text.format' of type 'json_object'."}}`)
	class := ClassifyUpstreamFailure(http.StatusBadGateway, nil, body, nil)
	require.Equal(t, UpstreamFailureClient, class.Kind)
	require.False(t, class.Failover)
}

func TestShouldFailoverUpstreamResponse_ModelNotFoundWithoutFlag(t *testing.T) {
	body := []byte(`{"error":{"code":"model_not_found","message":"model not found"}}`)
	require.True(t, ShouldFailoverUpstreamResponse(http.StatusBadRequest, nil, body, false))
	require.True(t, ShouldFailoverUpstreamResponse(http.StatusNotFound, nil, body, false))
}

func TestShouldFailoverUpstreamResponse_CompatRequiresFlag(t *testing.T) {
	body := []byte(`{"error":{"message":"thinking is not supported"}}`)
	require.False(t, ShouldFailoverUpstreamResponse(http.StatusBadRequest, nil, body, false))
	require.True(t, ShouldFailoverUpstreamResponse(http.StatusBadRequest, nil, body, true))
}

func TestShouldFailoverUpstreamResponse_StatusOnly400StaysFalse(t *testing.T) {
	require.False(t, ShouldFailoverUpstreamResponse(http.StatusBadRequest, nil, nil, false))
	require.False(t, ShouldFailoverUpstreamResponse(http.StatusBadRequest, nil, nil, true))
}

func TestGatewayShouldFailoverUpstreamResponse_CompatGated(t *testing.T) {
	body := []byte(`{"error":{"message":"thinking is not supported"}}`)
	modelMissing := []byte(`{"error":{"code":"model_not_found","message":"model not found"}}`)

	svc := &GatewayService{}
	require.False(t, svc.shouldFailoverUpstreamResponse(http.StatusBadRequest, nil, body))
	require.True(t, svc.shouldFailoverUpstreamResponse(http.StatusBadRequest, nil, modelMissing))
	require.False(t, svc.shouldFailoverUpstreamError(http.StatusBadRequest))
	require.True(t, svc.shouldFailoverUpstreamError(http.StatusMethodNotAllowed))

	svc.cfg = &config.Config{}
	svc.cfg.Gateway.FailoverOn400 = true
	require.True(t, svc.shouldFailoverUpstreamResponse(http.StatusBadRequest, nil, body))
}

func TestPoolModeSameAccountRetry_SkipsWAF(t *testing.T) {
	account := &Account{
		Type:        AccountTypeAPIKey,
		Credentials: map[string]any{"pool_mode": true},
	}
	require.False(t, PoolModeSameAccountRetry(account, http.StatusForbidden, nil, []byte("error code: 1010")))
	require.True(t, PoolModeSameAccountRetry(account, http.StatusTooManyRequests, nil, []byte(`{"error":{"message":"rate limit"}}`)))
}

func TestIsScriptUserAgent(t *testing.T) {
	require.True(t, IsScriptUserAgent("Python-urllib/3.12"))
	require.True(t, IsScriptUserAgent("Go-http-client/2.0"))
	require.True(t, IsScriptUserAgent("curl/8.5.0"))
	require.False(t, IsScriptUserAgent("okhttp/4.12.0"))
	require.False(t, IsScriptUserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/131.0.0.0"))
	require.False(t, IsScriptUserAgent("codex_cli_rs/0.1.0"))
	require.Equal(t, "", SanitizeForwardedUserAgent("Python-urllib/3.12"))
}

func TestEnsureNonScriptUserAgentKeepsAccountOverride(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"user_agent": "curl/8.5.0",
		},
	}
	h := make(http.Header)
	h.Set("User-Agent", "curl/8.5.0")
	EnsureNonScriptUserAgent(h, account)
	require.Equal(t, "curl/8.5.0", h.Get("User-Agent"))

	unconfigured := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	h.Set("User-Agent", "Python-urllib/3.12")
	EnsureNonScriptUserAgent(h, unconfigured)
	require.Equal(t, gatewayBrowserUserAgent, h.Get("User-Agent"))
}
