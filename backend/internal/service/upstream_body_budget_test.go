package service

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// upstreamBodyBudgetBody is the exact 503 payload a sub2api supplier instance
// (v0.2.20+ request-body memory budget middleware) returns, with the
// Retry-After: 1 header it sends alongside.
const upstreamBodyBudgetBody = `{"error":{"message":"Request body buffering capacity is exhausted; retry later","type":"invalid_request_error","code":"model_not_found"}}`

func TestIsUpstreamRequestBodyBudgetExhausted(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{name: "openai envelope from supplier instance", body: upstreamBodyBudgetBody, want: true},
		{name: "anthropic envelope", body: `{"type":"error","error":{"type":"overloaded_error","message":"Request body buffering capacity is exhausted"}}`, want: true},
		{name: "plain text", body: "Request body buffering capacity is exhausted; retry later", want: true},
		{name: "case insensitive", body: "REQUEST BODY BUFFERING CAPACITY IS EXHAUSTED", want: true},
		{name: "ordinary 503", body: `{"error":{"message":"upstream temporarily unavailable"}}`, want: false},
		{name: "generic capacity wording", body: `{"error":{"message":"all servers are overloaded, capacity exhausted"}}`, want: false},
		{name: "similar but different phrase", body: `{"error":{"message":"Request body buffering capacity is full"}}`, want: false},
		{name: "echoed request text is not the phrase", body: `{"error":{"message":"invalid request body"}}`, want: false},
		{name: "empty", body: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, IsUpstreamRequestBodyBudgetExhausted([]byte(tt.body)))
		})
	}
}

func TestClassifyUpstreamFailure_RequestBodyBudgetIsCapacityNotSameAccountRetry(t *testing.T) {
	class := ClassifyUpstreamFailure(http.StatusServiceUnavailable, nil, []byte(upstreamBodyBudgetBody), nil)
	require.Equal(t, UpstreamFailureBodyBudget, class.Kind)
	require.True(t, class.Failover)
	require.False(t, class.SameAccountRetry, "a full supplier instance cannot accept an immediate same-account retry")
	require.False(t, class.PunishAccount, "instance capacity must not be charged to the account")
	require.True(t, ShouldFailoverUpstreamResponse(http.StatusServiceUnavailable, nil, []byte(upstreamBodyBudgetBody), false))
}

func TestClassifyUpstreamFailure_Ordinary503Unchanged(t *testing.T) {
	body := []byte(`{"error":{"message":"upstream temporarily unavailable"}}`)
	class := ClassifyUpstreamFailure(http.StatusServiceUnavailable, nil, body, nil)
	require.Equal(t, UpstreamFailureServer, class.Kind)
	require.True(t, class.Failover)
	require.True(t, class.SameAccountRetry)
	require.False(t, IsUpstreamRequestBodyBudgetExhausted(body))
}

func TestPoolModeSameAccountRetry_SkipsRequestBodyBudget(t *testing.T) {
	account := &Account{
		Type: AccountTypeAPIKey,
		Credentials: map[string]any{
			"pool_mode":                    true,
			"pool_mode_retry_status_codes": []any{float64(http.StatusServiceUnavailable)},
		},
	}
	require.False(t, PoolModeSameAccountRetry(account, http.StatusServiceUnavailable, nil, []byte(upstreamBodyBudgetBody)))
	// An ordinary 503 keeps the existing same-account retry behaviour.
	require.True(t, PoolModeSameAccountRetry(account, http.StatusServiceUnavailable, nil, []byte(`{"error":{"message":"upstream temporarily unavailable"}}`)))
}

func TestGatewaySkipsLegacySameAccountRetryOnRequestBodyBudget(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"custom_error_codes_enabled": true,
			"custom_error_codes":         []any{float64(http.StatusTooManyRequests)},
		},
	}
	svc := &GatewayService{}

	budgetResp := &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Header:     http.Header{"Retry-After": []string{"1"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBodyBudgetBody)),
	}
	require.False(t, svc.shouldRetryUpstreamErrorForResponse(account, budgetResp))

	ordinaryResp := &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"upstream temporarily unavailable"}}`)),
	}
	require.True(t, svc.shouldRetryUpstreamErrorForResponse(account, ordinaryResp))
	// The decision reads the body but must leave it readable for failover.
	remaining, err := io.ReadAll(ordinaryResp.Body)
	require.NoError(t, err)
	require.Contains(t, string(remaining), "upstream temporarily unavailable")
}

func TestNewOpenAIUpstreamFailoverError_RequestBodyBudgetRotatesWithoutPunishing(t *testing.T) {
	headers := http.Header{"Retry-After": []string{"1"}}
	failoverErr := newOpenAIUpstreamFailoverError(
		http.StatusServiceUnavailable,
		headers,
		[]byte(upstreamBodyBudgetBody),
		"Request body buffering capacity is exhausted; retry later",
		true, // even when the caller thought this was same-account retryable
	)
	require.False(t, failoverErr.RetryableOnSameAccount)
	require.True(t, failoverErr.RequestScopedTransient)
	require.Equal(t, GatewayFailureScopeRequest, failoverErr.Scope)
	require.Equal(t, http.StatusServiceUnavailable, failoverErr.ClientStatusCode)
	require.Equal(t, UpstreamRequestBodyBudgetClientMessage, failoverErr.ClientMessage)
	require.Equal(t, "1", failoverErr.ResponseHeaders.Get("Retry-After"))
	require.True(t, failoverErr.ShouldRetryNextAccount())
	require.False(t, failoverErr.IsOpenAICapacityShed())
}

func TestWrapUpstreamErrorForClient_RequestBodyBudgetKeeps503AndHidesInternalBudget(t *testing.T) {
	status, errType, code, message := WrapUpstreamErrorForClient(http.StatusServiceUnavailable, []byte(upstreamBodyBudgetBody))
	require.Equal(t, http.StatusServiceUnavailable, status)
	require.Equal(t, "overloaded_error", errType)
	require.Empty(t, code)
	require.Equal(t, UpstreamRequestBodyBudgetClientMessage, message)

	lower := strings.ToLower(message)
	for _, leak := range []string{
		"buffering",
		"memory budget",
		"gateway_request_body_memory_budget_bytes",
		"model_not_found",
		"invalid_request_error",
	} {
		require.NotContains(t, lower, leak)
	}
}

func TestShouldFailoverOpenAIPassthroughResponse_RequestBodyBudgetRotatesEvenForNonAPIKey(t *testing.T) {
	// Non-API-key accounts normally keep a bare 503 on the same account; the
	// request-body budget rejection is instance capacity, so it must rotate.
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	require.True(t, shouldFailoverOpenAIPassthroughResponse(account, http.StatusServiceUnavailable, []byte(upstreamBodyBudgetBody)))
	require.False(t, shouldFailoverOpenAIPassthroughResponse(account, http.StatusServiceUnavailable, []byte(`{"error":{"message":"upstream temporarily unavailable"}}`)))
}
