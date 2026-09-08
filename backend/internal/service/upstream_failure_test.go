package service

import (
	"context"
	"net/http"
	"testing"

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
	require.False(t, IsScriptUserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/131.0.0.0"))
	require.False(t, IsScriptUserAgent("codex_cli_rs/0.1.0"))
	require.Equal(t, "", SanitizeForwardedUserAgent("Python-urllib/3.12"))
}
