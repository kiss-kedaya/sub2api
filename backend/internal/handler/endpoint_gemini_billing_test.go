package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type geminiBridgeUsageRepo struct {
	service.UsageLogRepository
	last *service.UsageLog
}

func TestGeminiBridgeUpstreamEndpointUsesActualProtocol(t *testing.T) {
	for _, endpoint := range []string{EndpointChatCompletions, EndpointResponses} {
		c, _ := gin.CreateTestContext(nil)
		service.SetActualOpenAIUpstreamEndpoint(c, endpoint)
		require.Equal(t, endpoint, GetUpstreamEndpoint(c, service.PlatformGemini))
	}
}

func (r *geminiBridgeUsageRepo) Create(_ context.Context, log *service.UsageLog) (bool, error) {
	r.last = log
	return true, nil
}

type geminiBridgeBillingRepo struct {
	service.UsageBillingRepository
	last *service.UsageBillingCommand
}

func (r *geminiBridgeBillingRepo) Apply(_ context.Context, cmd *service.UsageBillingCommand) (*service.UsageBillingApplyResult, error) {
	r.last = cmd
	return &service.UsageBillingApplyResult{Applied: true}, nil
}

func TestGeminiBridgeGatewayBillingUsesExclusiveInputBuckets(t *testing.T) {
	cfg := &config.Config{RunMode: config.RunModeStandard}
	cfg.Default.RateMultiplier = 1
	billing := service.NewBillingService(cfg, nil)
	logs, repo := &geminiBridgeUsageRepo{}, &geminiBridgeBillingRepo{}
	gateway := service.NewGatewayService(nil, nil, logs, repo, nil, nil, nil, nil, cfg,
		nil, nil, billing, nil, &service.BillingCacheService{}, nil, nil, &service.DeferredService{},
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	upstream := &service.OpenAIForwardResult{RequestID: "gemini-bridge-billing", Model: "gpt-5.1",
		Usage: service.OpenAIUsage{InputTokens: 1000, CacheReadInputTokens: 600, CacheCreationInputTokens: 100, OutputTokens: 50}}
	original := *upstream
	converted := openAIForwardResultAsGateway(upstream)
	require.NoError(t, gateway.RecordUsage(context.Background(), &service.RecordUsageInput{
		Result: converted, APIKey: &service.APIKey{ID: 7}, User: &service.User{ID: 42},
		Account: &service.Account{ID: 9, Platform: service.PlatformGemini},
	}))
	expected, err := billing.CalculateCost("gpt-5.1", service.UsageTokens{
		InputTokens: 300, CacheReadTokens: 600, CacheCreationTokens: 100, OutputTokens: 50,
	}, 1)
	require.NoError(t, err)
	require.NotNil(t, logs.last)
	require.NotNil(t, repo.last)
	require.Equal(t, 300, logs.last.InputTokens)
	require.Equal(t, 600, logs.last.CacheReadTokens)
	require.Equal(t, 100, logs.last.CacheCreationTokens)
	require.InDelta(t, expected.ActualCost, logs.last.ActualCost, 1e-12)
	require.InDelta(t, expected.ActualCost, repo.last.BalanceCost, 1e-12)
	require.Equal(t, original, *upstream, "dedicated OpenAI billing must still receive total input")
}

func TestGeminiBridgeGatewayConversionPreservesMetadataAndClampsInput(t *testing.T) {
	require.Nil(t, openAIForwardResultAsGateway(nil))
	headers := http.Header{"X-Request-Id": {"upstream-id"}}
	effort := "high"
	result := openAIForwardResultAsGateway(&service.OpenAIForwardResult{
		Usage:           service.OpenAIUsage{InputTokens: 5, CacheReadInputTokens: 6, CacheCreationInputTokens: 2, OutputTokens: 8, ImageOutputTokens: 3},
		UpstreamHeaders: headers, RequestedReasoningEffort: &effort, SearchCount: 2,
		AudioUsage: &service.AudioUsage{Mode: "tts", DurationOrUnits: 1},
	})
	require.Equal(t, service.ClaudeUsage{CacheReadInputTokens: 6, CacheCreationInputTokens: 2, OutputTokens: 8, ImageOutputTokens: 3}, result.Usage)
	require.Equal(t, headers, result.UpstreamHeaders)
	require.Equal(t, &effort, result.RequestedReasoningEffort)
	require.Equal(t, 2, result.SearchCount)
	require.Equal(t, 1.0, result.AudioUsage.DurationOrUnits)
}

func TestGeminiBridgeGatewayNonBillableFlagPreservesUsage(t *testing.T) {
	cfg := &config.Config{RunMode: config.RunModeStandard}
	cfg.Default.RateMultiplier = 1
	logs, repo := &geminiBridgeUsageRepo{}, &geminiBridgeBillingRepo{}
	gateway := service.NewGatewayService(nil, nil, logs, repo, nil, nil, nil, nil, cfg,
		nil, nil, service.NewBillingService(cfg, nil), nil, &service.BillingCacheService{}, nil, nil, &service.DeferredService{},
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	upstream := &service.OpenAIForwardResult{RequestID: "gemini-rejected-request", Model: "gpt-5.1",
		Usage: service.OpenAIUsage{InputTokens: 1000}, NonBillableUpstreamError: true}
	converted := openAIForwardResultAsGateway(upstream)
	require.True(t, converted.NonBillableUpstreamError)
	require.NoError(t, gateway.RecordUsage(context.Background(), &service.RecordUsageInput{
		Result: converted, APIKey: &service.APIKey{ID: 7}, User: &service.User{ID: 42},
		Account: &service.Account{ID: 9, Platform: service.PlatformGemini},
	}))
	require.NotNil(t, logs.last)
	require.NotNil(t, repo.last)
	require.Equal(t, 1000, logs.last.InputTokens)
	require.Zero(t, logs.last.OutputTokens)
	require.Zero(t, logs.last.InputCost)
	require.Zero(t, logs.last.TotalCost)
	require.Zero(t, logs.last.ActualCost)
	require.Zero(t, repo.last.BalanceCost)
	require.Equal(t, 1000, converted.Usage.InputTokens)
	require.Equal(t, 1000, upstream.Usage.InputTokens)
}
