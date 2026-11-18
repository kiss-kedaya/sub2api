//go:build unit

package service

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func requireGeminiSignalBilling(t *testing.T, result *ForwardResult, free bool) {
	t.Helper()
	require.Equal(t, free, result.NonBillableUpstreamError)
	logs, settlement := &openAIRecordUsageLogRepoStub{inserted: true}, &openAIRecordUsageBillingRepoStub{}
	billing := NewBillingService(&config.Config{}, &PricingService{pricingData: map[string]*LiteLLMModelPricing{
		result.Model: {InputCostPerToken: 1e-6, OutputCostPerToken: 2e-6},
	}})
	gateway := &GatewayService{usageLogRepo: logs, usageBillingRepo: settlement, billingService: billing,
		billingCacheService: &BillingCacheService{}, deferredService: &DeferredService{}}
	require.NoError(t, gateway.RecordUsage(context.Background(), &RecordUsageInput{
		Result: result, APIKey: &APIKey{ID: 81}, User: &User{ID: 82}, Account: geminiSignalTestAccount(),
	}))
	require.Equal(t, 1, logs.calls)
	require.Equal(t, 1, settlement.calls)
	require.NotNil(t, logs.lastLog)
	require.NotNil(t, settlement.lastCmd)
	require.Equal(t, result.Usage.InputTokens, logs.lastLog.InputTokens)
	require.Equal(t, result.Usage.OutputTokens, logs.lastLog.OutputTokens)
	if free {
		require.Zero(t, logs.lastLog.TotalCost)
		require.Zero(t, logs.lastLog.ActualCost)
		require.Zero(t, settlement.lastCmd.BalanceCost)
	} else {
		require.Positive(t, logs.lastLog.ActualCost)
		require.Equal(t, logs.lastLog.ActualCost, settlement.lastCmd.BalanceCost)
	}
}

func TestGeminiForwardNative_CollectedStreamErrorIsFree(t *testing.T) {
	account := &Account{ID: 83, Platform: PlatformGemini, Type: AccountTypeOAuth, Credentials: map[string]any{
		"access_token": "synthetic-access-token", "project_id": "synthetic-project",
	}}
	body := "data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"partial\"}]}}],\"usageMetadata\":{\"promptTokenCount\":10,\"candidatesTokenCount\":2}}}\n\n" +
		"data: {\"response\":{\"error\":{\"code\":503,\"message\":\"synthetic failure\",\"status\":\"UNAVAILABLE\"}}}\n\n"
	svc := newGeminiSignalService("text/event-stream", body)
	svc.tokenProvider = &GeminiTokenProvider{}
	c, _ := newGeminiNativeTestContext(t)
	result, err := svc.ForwardNative(context.Background(), c, account, "gemini-3.7-flash", "generateContent", false, geminiSignalTestRequest())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, ClaudeUsage{InputTokens: 10, OutputTokens: 2}, result.Usage)
	requireGeminiSignalBilling(t, result, true)
}

func TestGeminiForwardNative_ErrorPreludeSuccessfulTerminalKeepsBilling(t *testing.T) {
	body := "data: {\"error\":{\"code\":503,\"message\":\"synthetic prelude\"}}\n\n" + geminiSignalTestStopSSE
	svc := newGeminiSignalService("text/event-stream", body)
	c, _ := newGeminiNativeTestContext(t)
	result, err := svc.ForwardNative(context.Background(), c, geminiSignalTestAccount(), "gemini-3.7-flash", "streamGenerateContent", true, geminiSignalTestRequest())
	require.NoError(t, err)
	require.NotNil(t, result)
	requireGeminiSignalBilling(t, result, false)
}

func TestGeminiForwardNative_ErrorThenClientDisconnectKeepsWaiver(t *testing.T) {
	svc := newGeminiSignalService("text/event-stream", "data: {\"error\":{\"code\":503,\"message\":\"synthetic failure\"},\"usageMetadata\":{\"promptTokenCount\":10,\"candidatesTokenCount\":2}}\n\n")
	c, _ := gin.CreateTestContext(&geminiGoneRecorder{ResponseRecorder: httptest.NewRecorder(), failWrites: true})
	c.Request = httptest.NewRequest("POST", "/v1beta/models/synthetic:streamGenerateContent", nil)
	result, err := svc.ForwardNative(context.Background(), c, geminiSignalTestAccount(), "gemini-3.7-flash", "streamGenerateContent", true, geminiSignalTestRequest())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.ClientDisconnect)
	requireGeminiSignalBilling(t, result, true)
}
