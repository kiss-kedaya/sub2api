//go:build unit

package service

import (
	"context"
	"fmt"
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

func TestDeepSeekStandaloneToolFeesIgnoreGroupAndUserMultipliers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, endpoint := range []string{DeepSeekToolsPathSearch, DeepSeekToolsPathFetch} {
		for _, multiplier := range []float64{0, 0.1248, 3} {
			t.Run(fmt.Sprintf("%s/rate=%g", endpoint, multiplier), func(t *testing.T) {
				for _, tc := range []struct {
					name, response string
					status         int
					billable       bool
				}{
					{name: "success", response: `{"results":[{"title":"Example","url":"https://example.com"}],"title":"Example","content":"Page body"}`, status: 200, billable: true},
					{name: "empty", response: `{"results":[],"title":"Empty","content":"  "}`, status: 200},
					{name: "failure", response: `{"error":{"type":"invalid_request_error","message":"synthetic provider failure"}}`, status: 400},
				} {
					t.Run(tc.name, func(t *testing.T) {
						recorder := httptest.NewRecorder()
						c, _ := gin.CreateTestContext(recorder)
						c.Request = httptest.NewRequest(http.MethodPost, endpoint, nil)
						upstream := &httpUpstreamRecorder{resp: &http.Response{
							StatusCode: tc.status, Header: http.Header{"Content-Type": {"application/json"}, "X-Request-Id": {"synthetic-fixed-tool"}},
							Body: io.NopCloser(strings.NewReader(tc.response)),
						}}
						forwarder := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
						account := &Account{ID: 9, Type: AccountTypeAPIKey, Platform: PlatformDeepseek,
							Credentials: map[string]any{"api_key": "synthetic-key", "base_url": "https://api.deepseek.com"}}
						result, err := forwarder.ForwardDeepSeekTools(context.Background(), c, account, nil, endpoint, "", []byte(`{"query":"example","url":"https://example.com"}`))
						require.NoError(t, err)
						require.Equal(t, tc.status, recorder.Code)
						if !tc.billable {
							require.Nil(t, result, "failed/empty tool responses must never reach settlement")
							return
						}
						require.NotNil(t, result)
						require.True(t, result.FixedToolBilling)
						require.Equal(t, 1, result.WebSearchCalls)
						logs, settlement := &openAIRecordUsageLogRepoStub{inserted: true}, &openAIRecordUsageBillingRepoStub{}
						rates := &openAIUserGroupRateRepoStub{rate: &multiplier}
						svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(logs, settlement, nil, nil, rates)
						price := 5.0
						group := &Group{ID: 36, Platform: PlatformDeepseek, RateMultiplier: 2, SearchPricePer1k: &price}
						key := &APIKey{ID: 7, GroupID: &group.ID, Group: group}
						require.NoError(t, svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
							Result: result, APIKey: key, User: &User{ID: 8}, Account: account,
							InboundEndpoint: endpoint, UpstreamEndpoint: endpoint,
						}))
						require.Equal(t, 1, settlement.calls)
						require.Equal(t, 1, logs.calls)
						require.Positive(t, rates.calls, "the test must exercise the user-specific rate override")
						require.InDelta(t, 0.005, settlement.lastCmd.BalanceCost, 1e-12)
						require.InDelta(t, 0.005, logs.lastLog.TotalCost, 1e-12)
						require.InDelta(t, 0.005, logs.lastLog.ActualCost, 1e-12)
						require.Equal(t, 1.0, logs.lastLog.RateMultiplier)
						require.Zero(t, logs.lastLog.TotalTokens())
					})
				}
			})
		}
	}
}

func TestDeepSeekFixedHostedSurchargePreservesTokenMultiplier(t *testing.T) {
	for _, multiplier := range []float64{0, 0.1248, 3} {
		for _, price := range []float64{0, 5} {
			t.Run(fmt.Sprintf("rate=%g/price=%g", multiplier, price), func(t *testing.T) {
				billing := newTestBillingService()
				group := &Group{ID: 36, Platform: PlatformDeepseek, SearchPricePer1k: &price}
				key := &APIKey{ID: 7, Group: group, GroupID: &group.ID}
				tokens := UsageTokens{InputTokens: 1000, OutputTokens: 500, CacheReadTokens: 200}
				baseline, err := billing.CalculateCost("claude-sonnet-4", tokens, multiplier)
				require.NoError(t, err)
				fee := 2 * price / 1000
				openAI := &OpenAIGatewayService{billingService: billing}
				cost, err := openAI.calculateOpenAIRecordUsageCost(context.Background(),
					&OpenAIForwardResult{SearchCount: 2, FixedToolBilling: true}, key, []string{"claude-sonnet-4"},
					multiplier, 1, 1, multiplier, tokens, "", boolPtr(false), time.Time{})
				require.NoError(t, err)
				require.InDelta(t, baseline.TotalCost+fee, cost.TotalCost, 1e-12)
				require.InDelta(t, baseline.ActualCost+fee, cost.ActualCost, 1e-12)
				gateway := &GatewayService{billingService: billing}
				anthropic := gateway.calculateRecordUsageCost(context.Background(), &ForwardResult{
					Model: "claude-sonnet-4", FixedToolBilling: true, SearchCount: 2,
					Usage: ClaudeUsage{InputTokens: 1000, OutputTokens: 500, CacheReadInputTokens: 200},
				}, key, "claude-sonnet-4", multiplier, 1, time.Time{})
				require.InDelta(t, baseline.TotalCost+fee, anthropic.TotalCost, 1e-12)
				require.InDelta(t, baseline.ActualCost+fee, anthropic.ActualCost, 1e-12)
			})
		}
	}
}

func TestDeepSeekHostedUsageRecordKeepsTokenRateAndFixedToolFee(t *testing.T) {
	logs, settlement := &openAIRecordUsageLogRepoStub{inserted: true}, &openAIRecordUsageBillingRepoStub{}
	multiplier, price := 0.1248, 5.0
	rates := &openAIUserGroupRateRepoStub{rate: &multiplier}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(logs, settlement, nil, nil, rates)
	group := &Group{ID: 36, Platform: PlatformDeepseek, RateMultiplier: 3, SearchPricePer1k: &price}
	usage := OpenAIUsage{InputTokens: 1000, OutputTokens: 500, CacheReadInputTokens: 200}
	baseline := expectedOpenAICost(t, svc, "gpt-5.1", usage, multiplier)
	require.NoError(t, svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{RequestID: "synthetic-fixed-hosted", Model: "gpt-5.1", Usage: usage,
			SearchCount: 2, FixedToolBilling: true},
		APIKey: &APIKey{ID: 7, GroupID: &group.ID, Group: group}, User: &User{ID: 8},
		Account: &Account{ID: 9, Platform: PlatformDeepseek, Type: AccountTypeAPIKey},
	}))
	require.Equal(t, 1, logs.calls)
	require.Equal(t, 1, settlement.calls)
	require.NotNil(t, logs.lastLog)
	require.Equal(t, multiplier, logs.lastLog.RateMultiplier, "the stored multiplier still describes model tokens")
	require.InDelta(t, baseline.TotalCost+0.01, logs.lastLog.TotalCost, 1e-12)
	require.InDelta(t, QuantizeUsageBillingAmount(baseline.ActualCost+0.01), logs.lastLog.ActualCost, 1e-12)
	require.InDelta(t, logs.lastLog.ActualCost, settlement.lastCmd.BalanceCost, 1e-12)
}

func TestDeepSeekHostedSearchAndFetchMarkFixedFeeAtExecution(t *testing.T) {
	for _, tool := range []string{"search", "fetch"} {
		for _, successful := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/success=%t", tool, successful), func(t *testing.T) {
				call := hostedSearchCall()
				if tool == "fetch" {
					call = hostedFetchCall("open_fixed", "https://example.com")
				}
				upstream := &hostedSearchUpstream{modelResponses: [][]byte{
					hostedSearchResponse("resp_tool", 10, 2, call),
					hostedSearchResponse("resp_answer", 20, 3, hostedSearchAnswer()),
				}, searchResponse: `{"results":[]}`, fetchResponse: `{"title":"Example","content":""}`}
				if successful {
					upstream.searchResponse = `{"results":[{"title":"Example","url":"https://example.com"}]}`
					upstream.fetchResponse = `{"title":"Example","content":"Fixed fee page"}`
				}
				svc, c, _, account, body := hostedSearchFixture(t, upstream, false, "auto", false)
				result, err := svc.Forward(context.Background(), c, account, body)
				require.NoError(t, err)
				require.NotNil(t, result)
				require.True(t, result.FixedToolBilling)
				require.Equal(t, 30, result.Usage.InputTokens)
				require.Equal(t, 5, result.Usage.OutputTokens)
				if successful {
					require.Equal(t, 1, result.SearchCount)
				} else {
					require.Zero(t, result.SearchCount, "empty tool results do not create a fixed surcharge")
				}
			})
		}
	}
}

type fixedToolIdempotentSettlement struct {
	UsageBillingRepository
	seen         map[string]string
	appliedCost  float64
	applications int
}

func (r *fixedToolIdempotentSettlement) Apply(_ context.Context, command *UsageBillingCommand) (*UsageBillingApplyResult, error) {
	key := fmt.Sprintf("%s:%d", command.RequestID, command.APIKeyID)
	if fingerprint, exists := r.seen[key]; exists {
		if fingerprint != command.RequestFingerprint {
			return nil, fmt.Errorf("fixed fee changed its settlement fingerprint")
		}
		return &UsageBillingApplyResult{Applied: false}, nil
	}
	r.seen[key] = command.RequestFingerprint
	r.applications++
	r.appliedCost += command.BalanceCost
	return &UsageBillingApplyResult{Applied: true}, nil
}

func TestDeepSeekFixedToolBillingRetainsStableSettlementIdentity(t *testing.T) {
	logs := &openAIRecordUsageLogRepoStub{inserted: true}
	settlement := &fixedToolIdempotentSettlement{seen: make(map[string]string)}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(logs, settlement, nil, nil, nil)
	price := 5.0
	group := &Group{ID: 36, Platform: PlatformDeepseek, RateMultiplier: 0.1248, SearchPricePer1k: &price}
	for _, label := range []string{DeepSeekToolsLabelSearch, DeepSeekToolsLabelFetch} {
		for range 2 {
			require.NoError(t, svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
				Result: &OpenAIForwardResult{RequestID: "fixed-" + label, Model: label, WebSearchCalls: 1, FixedToolBilling: true},
				APIKey: &APIKey{ID: 7, GroupID: &group.ID, Group: group}, User: &User{ID: 8},
				Account: &Account{ID: 9, Type: AccountTypeAPIKey, Platform: PlatformDeepseek},
			}))
		}
	}
	require.Equal(t, 2, settlement.applications, "two distinct tool requests settle once each")
	require.InDelta(t, 0.01, settlement.appliedCost, 1e-12)
}
