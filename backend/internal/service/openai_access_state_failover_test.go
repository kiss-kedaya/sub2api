package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type openAIStream403AccountRepo struct {
	AccountRepository
	setErrorCalls int
}

func (r *openAIStream403AccountRepo) SetError(context.Context, int64, string) error {
	r.setErrorCalls++
	return nil
}

func TestOpenAIUpstreamAccessStateClassification(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"workspace_code", `{"detail":{"code":"deactivated_workspace"}}`},
		{"disabled_account", `{"error":{"message":"Your account is disabled"}}`},
		{"suspended_workspace", `{"response":{"error":{"message":"This workspace has been suspended"}}}`},
		{"deactivated_organization", `{"detail":{"message":"The organization is deactivated"}}`},
		{"scalar_detail", `{"detail":"This workspace has been disabled"}`},
		{"suspended_org_code", `{"error":{"code":"org_suspended"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := []byte(tt.body)
			require.True(t, isOpenAIUpstreamAccessStateError("", body))
			require.True(t, (&OpenAIGatewayService{}).shouldFailoverOpenAIUpstreamResponse(http.StatusForbidden, "", body))
			require.True(t, shouldFailoverOpenAIPassthroughResponse(&Account{Type: AccountTypeOAuth}, http.StatusForbidden, body))

			err := newOpenAIUpstreamFailoverError(http.StatusForbidden, nil, body, "", true)
			require.True(t, err.IsCredentialFailure())
			require.Equal(t, GatewayFailureScopeAccount, err.Scope)
			require.Equal(t, OpenAIUpstreamAccessStateReason, err.Reason)
			require.Equal(t, NextAccountRetry, err.NextAccountAction)
			require.False(t, err.RetryableOnSameAccount)
			require.False(t, err.RequestScopedTransient)
			require.Equal(t, http.StatusBadGateway, err.ClientStatusCode)
			require.Equal(t, openAIUpstreamAccessUnavailableClientMessage, err.ClientMessage)
		})
	}
}

func TestOpenAIUpstreamAccessStateDoesNotScanEchoedJSON(t *testing.T) {
	body := []byte(`{"error":{"code":"invalid_request_error","message":"Invalid input"},"echo":{"prompt":"my account is disabled"}}`)
	require.False(t, isOpenAIUpstreamAccessStateError("", body))
	require.False(t, shouldFailoverOpenAIPassthroughResponse(&Account{Type: AccountTypeOAuth}, http.StatusBadRequest, body))
}

func TestOpenAICyberPolicyWrapped5xxNeverFailsOver(t *testing.T) {
	body := []byte(`{"error":{"code":"cyber_policy","message":"blocked"}}`)
	svc := &OpenAIGatewayService{}
	require.False(t, svc.shouldFailoverOpenAIUpstreamResponse(http.StatusBadGateway, "wrapped upstream failure", body))
	require.False(t, shouldFailoverOpenAIPassthroughResponse(&Account{Type: AccountTypeOAuth}, http.StatusBadGateway, body))
}

func TestOpenAICapacityFailoverCarriesSafeTerminalResponse(t *testing.T) {
	message := "Our servers are currently overloaded. Please try again later."
	body := []byte(`{"error":{"code":"server_is_overloaded","message":"` + message + `"}}`)
	err := newOpenAIUpstreamFailoverError(http.StatusBadRequest, nil, body, message, false)

	require.True(t, err.IsOpenAICapacityShed())
	require.Equal(t, http.StatusServiceUnavailable, err.ClientStatusCode)
	require.Equal(t, message, err.ClientMessage)
	require.NotContains(t, err.ClientMessage, "server_is_overloaded")
}

func TestOpenAIStreamSemanticStatusesPreservedAcrossTerminalShapes(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		status       int
		wantFailover bool
	}{
		{"unauthorized", `{"type":"error","error":{"type":"authentication_error","code":"invalid_api_key","message":"unauthorized"}}`, http.StatusUnauthorized, true},
		{"forbidden", `{"type":"response.failed","response":{"error":{"type":"permission_error","message":"forbidden"}}}`, http.StatusForbidden, false},
		{"rate_limit", `{"error":{"type":"rate_limit_error","code":"rate_limit_exceeded","message":"slow down"}}`, http.StatusTooManyRequests, true},
		{"overload_529", `{"type":"error","error":{"status_code":529,"code":"overloaded","message":"overloaded"}}`, 529, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := []byte(tt.body)
			message := extractOpenAISSEErrorMessage(payload)
			require.Equal(t, tt.status, openAIStreamFailureStatus(payload, message))
			require.Equal(t, tt.wantFailover, openAIStreamErrorEventShouldFailover(payload, message))
		})
	}
}

func TestOpenAIStreamBareErrorUsesSemanticFailover(t *testing.T) {
	payload := []byte(`{"error":{"type":"rate_limit_error","code":"rate_limit_exceeded","message":"slow down"}}`)
	require.True(t, openAIStreamErrorEventShouldFailover(payload, "slow down"))
}

func TestOpenAIStream403FailoverRequiresStructuredAccountCredentialSignal(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    bool
	}{
		{
			name:    "ordinary permission error",
			payload: `{"type":"response.failed","response":{"error":{"type":"permission_error","code":"forbidden","message":"access denied for this request"}}}`,
		},
		{
			name:    "explicit 403 request status",
			payload: `{"type":"error","error":{"type":"permission_error","code":"forbidden","status_code":403,"message":"forbidden content"}}`,
		},
		{
			name:    "structured access state",
			payload: `{"type":"response.failed","response":{"error":{"code":"workspace_suspended","message":"workspace is suspended"}}}`,
			want:    true,
		},
		{
			name:    "explicit credential auth code",
			payload: `{"type":"error","error":{"type":"permission_error","code":"invalid_api_key","status_code":403,"message":"credential rejected"}}`,
			want:    true,
		},
		{
			name:    "explicit authentication type",
			payload: `{"type":"error","error":{"type":"authentication_error","status_code":403,"message":"credential rejected"}}`,
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := []byte(tt.payload)
			message := extractOpenAISSEErrorMessage(payload)
			require.Equal(t, http.StatusForbidden, openAIStreamFailureStatus(payload, message))
			require.Equal(t, tt.want, openAIStreamFailedEventShouldFailover(payload, message))
			require.Equal(t, tt.want, openAIStreamErrorEventShouldFailover(payload, message))
		})
	}
}

func TestOpenAIStream403PostOutputAccountSideEffectsIgnoreRequestPermissionErrors(t *testing.T) {
	repo := &openAIStream403AccountRepo{}
	rateLimits := &RateLimitService{accountRepo: repo}
	svc := &OpenAIGatewayService{rateLimitService: rateLimits}
	account := &Account{ID: 918, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	payload := []byte(`{"type":"error","error":{"type":"permission_error","code":"forbidden","status_code":403,"message":"access denied for this request"}}`)

	status, disabled := svc.handleOpenAIStreamTerminalAccountSideEffects(nil, account, payload, "access denied for this request", nil)

	require.Equal(t, http.StatusForbidden, status)
	require.False(t, disabled)
	require.Zero(t, repo.setErrorCalls)
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
}

func TestOpenAIStream403ExplicitCredentialAuthAppliesAccountSideEffects(t *testing.T) {
	repo := &openAIStream403AccountRepo{}
	rateLimits := &RateLimitService{accountRepo: repo}
	svc := &OpenAIGatewayService{rateLimitService: rateLimits}
	account := &Account{ID: 917, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	payload := []byte(`{"type":"error","error":{"type":"permission_error","code":"invalid_api_key","status_code":403,"message":"credential rejected"}}`)

	status, disabled := svc.handleOpenAIStreamTerminalAccountSideEffects(nil, account, payload, "credential rejected", nil)

	require.Equal(t, http.StatusForbidden, status)
	require.True(t, disabled)
	require.Equal(t, 1, repo.setErrorCalls)
	require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
}

func TestOpenAIWSStandaloneFailedStructured403AppliesAccountSideEffectsOnce(t *testing.T) {
	repo := &openAIStream403AccountRepo{}
	svc := &OpenAIGatewayService{rateLimitService: &RateLimitService{accountRepo: repo}}
	account := &Account{ID: 923, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	failed := []byte(`{"type":"response.failed","response":{"error":{"type":"permission_error","code":"invalid_api_key","status_code":403,"message":"credential rejected"}}}`)

	require.True(t, svc.handleOpenAIWSFailureAccountSideEffects(context.Background(), account, "gpt-5", nil, failed))
	require.Equal(t, 1, repo.setErrorCalls)
	require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
}

func TestOpenAIWSPairedStructured403SideEffectsCanBeDeduplicated(t *testing.T) {
	repo := &openAIStream403AccountRepo{}
	svc := &OpenAIGatewayService{rateLimitService: &RateLimitService{accountRepo: repo}}
	account := &Account{ID: 924, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	errorEvent := []byte(`{"type":"error","error":{"code":"workspace_suspended","status_code":403,"message":"workspace is suspended"}}`)
	failedEvent := []byte(`{"type":"response.failed","response":{"error":{"code":"workspace_suspended","status_code":403,"message":"workspace is suspended"}}}`)

	applied := svc.handleOpenAIWSFailureAccountSideEffects(context.Background(), account, "gpt-5", nil, errorEvent)
	if !applied {
		applied = svc.handleOpenAIWSFailureAccountSideEffects(context.Background(), account, "gpt-5", nil, failedEvent)
	}

	require.True(t, applied)
	require.Equal(t, 1, repo.setErrorCalls)
}

func TestOpenAIStreamAccessStateAppliesAccountHealthBeforeFailover(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := &Account{ID: 919, Platform: PlatformOpenAI, Type: AccountTypeSetupToken}
	payload := []byte(`{"type":"response.failed","response":{"error":{"code":"workspace_suspended","message":"workspace is suspended"}}}`)

	status, disabled := svc.handleOpenAIStreamTerminalAccountSideEffects(nil, account, payload, "workspace is suspended", nil)

	require.Equal(t, http.StatusForbidden, status)
	require.True(t, disabled)
	require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
}

func TestOpenAIStreamPairedFailureAppliesAccountSideEffectsOnce(t *testing.T) {
	const upstream = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n" +
		"data: {\"type\":\"error\",\"error\":{\"status_code\":403,\"code\":\"workspace_suspended\",\"message\":\"workspace is suspended\"}}\n\n" +
		"data: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_failed\",\"error\":{\"status_code\":403,\"code\":\"workspace_suspended\",\"message\":\"workspace is suspended\"}}}\n\n"

	t.Run("native", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		repo := &openAIStream403AccountRepo{}
		svc := &OpenAIGatewayService{
			cfg:              &config.Config{},
			toolCorrector:    NewCodexToolCorrector(),
			rateLimitService: &RateLimitService{accountRepo: repo},
		}
		account := &Account{ID: 921, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
		recorder := newOpenAIResponseFlushRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(upstream)),
		}

		result, err := svc.handleStreamingResponse(context.Background(), resp, c, account, time.Now(), "gpt-5", "gpt-5")

		require.Error(t, err)
		require.NotNil(t, result)
		require.Equal(t, 1, repo.setErrorCalls)
	})

	t.Run("passthrough", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		repo := &openAIStream403AccountRepo{}
		svc := &OpenAIGatewayService{
			cfg:              &config.Config{},
			rateLimitService: &RateLimitService{accountRepo: repo},
		}
		account := &Account{ID: 922, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		writer := &passthroughFlushTestWriter{
			ResponseWriter:  c.Writer,
			recorder:        recorder,
			failAfterWrites: -1,
		}
		c.Writer = writer
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(upstream)),
		}

		result, err := svc.handleStreamingResponsePassthrough(
			context.Background(), resp, c, account, time.Now(), "gpt-5", "gpt-5",
		)

		require.Error(t, err)
		require.NotNil(t, result)
		require.Equal(t, 1, repo.setErrorCalls)
	})
}

func TestOpenAIStreamOAuthLike429GetsDeadlineWithoutImmediateRuntimeBlock(t *testing.T) {
	for _, accountType := range []string{AccountTypeOAuth, AccountTypeSetupToken} {
		t.Run(accountType, func(t *testing.T) {
			svc := &OpenAIGatewayService{}
			account := &Account{ID: 920, Platform: PlatformOpenAI, Type: accountType}
			payload := []byte(`{"type":"error","error":{"type":"rate_limit_error","code":"rate_limit_exceeded","message":"slow down"}}`)
			status, disabled := svc.handleOpenAIStreamTerminalAccountSideEffects(nil, account, payload, "slow down", nil)
			err := svc.newOpenAIAccountFailoverError(account, status, nil, payload, "slow down", disabled, false)

			require.Equal(t, http.StatusTooManyRequests, status)
			require.False(t, disabled)
			require.True(t, err.RetryableOnSameAccount)
			require.False(t, err.SameAccountRetryDeadline.IsZero())
			require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
		})
	}
}
