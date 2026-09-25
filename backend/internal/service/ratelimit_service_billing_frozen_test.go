//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

const billingAccountFrozenBody = `{"code":"billing","message":"\u8ba1\u8d39\u8d26\u6237\u5df2\u88ab\u51bb\u7ed3","ref_code":400901,"ref_scope":"common"}`

func TestIsUpstreamBillingAccountFrozen(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{name: "senseaudio payload", body: billingAccountFrozenBody, want: true},
		{name: "string ref_code", body: `{"code":"billing","message":"frozen","ref_code":"400901"}`, want: true},
		{name: "nested error.ref_code", body: `{"error":{"code":"billing","message":"\u8ba1\u8d39\u8d26\u6237\u5df2\u88ab\u51bb\u7ed3","ref_code":400901}}`, want: true},
		{name: "message json blob", body: `{"error":{"message":"{\"code\":\"billing\",\"message\":\"\u8ba1\u8d39\u8d26\u6237\u5df2\u88ab\u51bb\u7ed3\",\"ref_code\":400901}"}}`, want: true},
		{name: "billing without freeze", body: `{"code":"billing","message":"insufficient balance","ref_code":400101}`, want: false},
		{name: "generic 400", body: `{"error":{"message":"invalid request"}}`, want: false},
		{name: "empty", body: "", want: false},
		{name: "organization disabled stays separate", body: `{"error":{"message":"organization has been disabled"}}`, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isUpstreamBillingAccountFrozen([]byte(tt.body)))
		})
	}
}

func TestHandleUpstreamError_BillingAccountFrozenDisablesAndStopsSameAccountRetry(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	account := &Account{
		ID:          4401,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Name:        "senseaudio",
	}

	shouldDisable := svc.HandleUpstreamError(context.Background(), account, http.StatusBadRequest, http.Header{}, []byte(billingAccountFrozenBody), "deepseek-v4.1-flash")
	require.True(t, shouldDisable)
	require.Equal(t, 1, repo.setErrorCalls)
	require.Equal(t, int64(4401), repo.lastErrorID)
	require.Contains(t, repo.lastErrorMsg, "Billing account frozen")
	require.Contains(t, repo.lastErrorMsg, "\u8ba1\u8d39\u8d26\u6237\u5df2\u88ab\u51bb\u7ed3")
}

func TestHandleUpstreamError_Generic400DoesNotDisable(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	account := &Account{ID: 4402, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true}

	shouldDisable := svc.HandleUpstreamError(context.Background(), account, http.StatusBadRequest, http.Header{}, []byte(`{"error":{"message":"invalid request"}}`))
	require.False(t, shouldDisable)
	require.Zero(t, repo.setErrorCalls)
}

func TestGatewayChatCompletionsBillingFrozenFailoversToAnotherAccount(t *testing.T) {
	body := []byte(billingAccountFrozenBody)
	gw := &GatewayService{}
	require.True(t, gw.shouldFailoverUpstreamResponse(http.StatusBadRequest, nil, body))
	require.False(t, gw.shouldFailoverUpstreamResponse(http.StatusBadRequest, nil, []byte(`{"error":{"message":"invalid request"}}`)))

	openai := &OpenAIGatewayService{}
	account := newOpenAIUpstreamErrorTestAccount()
	require.True(t, openai.shouldFailoverOpenAIUpstreamResponse(account, http.StatusBadRequest, "", body))
	require.True(t, shouldFailoverOpenAIPassthroughResponse(&Account{Type: AccountTypeAPIKey}, http.StatusBadRequest, body))
	require.False(t, PoolModeSameAccountRetry(&Account{
		Type:        AccountTypeAPIKey,
		Credentials: map[string]any{"pool_mode": true},
	}, http.StatusBadRequest, nil, body))
}

func TestHandleUpstreamError_PoolModeBillingAccountFrozenStillDisables(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	account := &Account{
		ID:          4403,
		Platform:    PlatformDeepseek,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Name:        "senseaudio-pool",
		Credentials: map[string]any{"pool_mode": true},
	}

	shouldDisable := svc.HandleUpstreamError(context.Background(), account, http.StatusBadRequest, http.Header{}, []byte(billingAccountFrozenBody), "deepseek-v4.1-flash")
	require.True(t, shouldDisable)
	require.Equal(t, 1, repo.setErrorCalls)
	require.Equal(t, int64(4403), repo.lastErrorID)
	require.Contains(t, repo.lastErrorMsg, "Billing account frozen")
	require.Contains(t, repo.lastErrorMsg, "\u8ba1\u8d39\u8d26\u6237\u5df2\u88ab\u51bb\u7ed3")
}

func TestCheckErrorPolicy_PoolModeBillingAccountFrozenIsMatched(t *testing.T) {
	svc := NewRateLimitService(&rateLimitAccountRepoStub{}, nil, &config.Config{}, nil, nil)
	account := &Account{
		ID:          4404,
		Platform:    PlatformDeepseek,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Credentials: map[string]any{"pool_mode": true},
	}
	require.Equal(t, ErrorPolicyMatched, svc.CheckErrorPolicy(context.Background(), account, http.StatusBadRequest, []byte(billingAccountFrozenBody)))
}
