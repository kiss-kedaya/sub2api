//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

const upstreamUsageLimitExhaustedBody = `{"code":"invalid","message":"\u5df2\u8fbe\u5230\u4f7f\u7528\u9650\u5236\u6216\u4f59\u989d\u4e0d\u8db3","ref_code":400001,"ref_scope":"common"}`

func TestIsUpstreamUsageLimitExhausted(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{name: "deepseek payload", body: upstreamUsageLimitExhaustedBody, want: true},
		{name: "string ref_code", body: `{"code":"invalid","message":"\u5df2\u8fbe\u5230\u4f7f\u7528\u9650\u5236\u6216\u4f59\u989d\u4e0d\u8db3","ref_code":"400001"}`, want: true},
		{name: "nested error", body: `{"error":{"code":"invalid","message":"\u5df2\u8fbe\u5230\u4f7f\u7528\u9650\u5236\u6216\u4f59\u989d\u4e0d\u8db3","ref_code":400001}}`, want: true},
		{name: "message json blob", body: `{"error":{"message":"{\"code\":\"invalid\",\"message\":\"\u5df2\u8fbe\u5230\u4f7f\u7528\u9650\u5236\u6216\u4f59\u989d\u4e0d\u8db3\",\"ref_code\":400001}"}}`, want: true},
		{name: "ref code without message", body: `{"code":"invalid","message":"invalid request","ref_code":400001}`, want: false},
		{name: "message without ref code", body: `{"code":"invalid","message":"\u5df2\u8fbe\u5230\u4f7f\u7528\u9650\u5236\u6216\u4f59\u989d\u4e0d\u8db3"}`, want: false},
		{name: "other ref code", body: `{"code":"invalid","message":"\u5df2\u8fbe\u5230\u4f7f\u7528\u9650\u5236\u6216\u4f59\u989d\u4e0d\u8db3","ref_code":400101}`, want: false},
		{name: "frozen stays separate", body: billingAccountFrozenBody, want: false},
		{name: "generic 400", body: `{"error":{"message":"invalid request"}}`, want: false},
		{name: "empty", body: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isUpstreamUsageLimitExhausted([]byte(tt.body)))
		})
	}
}

func TestHandleUpstreamError_UsageLimitExhaustedDisablesPoolAccountAndFailovers(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	account := &Account{
		ID:          29421,
		Platform:    PlatformDeepseek,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Name:        "16291003446",
		Credentials: map[string]any{"pool_mode": true},
	}
	body := []byte(upstreamUsageLimitExhaustedBody)

	shouldDisable := svc.HandleUpstreamError(context.Background(), account, http.StatusBadRequest, http.Header{}, body, "deepseek-v4-flash")
	require.True(t, shouldDisable)
	require.Equal(t, 1, repo.setErrorCalls)
	require.Equal(t, int64(29421), repo.lastErrorID)
	require.Contains(t, repo.lastErrorMsg, "400001")
	require.Contains(t, repo.lastErrorMsg, "\u5df2\u8fbe\u5230\u4f7f\u7528\u9650\u5236\u6216\u4f59\u989d\u4e0d\u8db3")
	require.Equal(t, ErrorPolicyMatched, svc.CheckErrorPolicy(context.Background(), account, http.StatusBadRequest, body))

	class := ClassifyUpstreamFailure(http.StatusBadRequest, nil, body, nil)
	require.Equal(t, UpstreamFailureAuth, class.Kind)
	require.True(t, class.Failover)
	require.True(t, class.PunishAccount)
	require.False(t, class.SameAccountRetry)
	require.True(t, ShouldFailoverUpstreamResponse(http.StatusBadRequest, nil, body, false))
	require.True(t, (&GatewayService{}).shouldFailoverUpstreamResponse(http.StatusBadRequest, nil, body))
	require.True(t, shouldFailoverOpenAIPassthroughResponse(&Account{Type: AccountTypeAPIKey}, http.StatusBadRequest, body))
	require.True(t, (&OpenAIGatewayService{}).shouldFailoverOpenAIUpstreamResponse(&Account{Type: AccountTypeAPIKey}, http.StatusBadRequest, "", body))
	require.False(t, PoolModeSameAccountRetry(&Account{
		Type: AccountTypeAPIKey,
		Credentials: map[string]any{
			"pool_mode":                    true,
			"pool_mode_retry_status_codes": []any{float64(400)},
		},
	}, http.StatusBadRequest, nil, body))
}

func TestHandleUpstreamError_UsageLimitLookalikesDoNotDisable(t *testing.T) {
	bodies := []string{
		`{"code":"invalid","message":"invalid request","ref_code":400001}`,
		`{"code":"invalid","message":"\u5df2\u8fbe\u5230\u4f7f\u7528\u9650\u5236\u6216\u4f59\u989d\u4e0d\u8db3"}`,
		`{"error":{"message":"invalid request"}}`,
	}
	for _, body := range bodies {
		repo := &rateLimitAccountRepoStub{}
		svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
		account := &Account{
			ID:          29421,
			Platform:    PlatformDeepseek,
			Type:        AccountTypeAPIKey,
			Status:      StatusActive,
			Schedulable: true,
			Credentials: map[string]any{"pool_mode": true},
		}
		shouldDisable := svc.HandleUpstreamError(context.Background(), account, http.StatusBadRequest, http.Header{}, []byte(body))
		require.False(t, shouldDisable)
		require.Zero(t, repo.setErrorCalls)
		require.False(t, ShouldFailoverUpstreamResponse(http.StatusBadRequest, nil, []byte(body), false))
	}
}

func TestShouldDisableAccountAfterTestUpstream_UsageLimit(t *testing.T) {
	svc := &AccountTestService{accountRepo: &openAIAccountTestRepo{}}
	require.True(t, svc.shouldDisableAccountAfterTestUpstream(http.StatusBadRequest, []byte(upstreamUsageLimitExhaustedBody)))
	require.False(t, svc.shouldDisableAccountAfterTestUpstream(http.StatusBadRequest, []byte(`{"error":{"message":"bad request"}}`)))
}
