//go:build unit

package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func requireDeepSeekHostedBilling(t *testing.T, result *OpenAIForwardResult, free bool) {
	t.Helper()
	require.NotNil(t, result)
	require.Equal(t, free, result.NonBillableUpstreamError)
	logs := &openAIRecordUsageLogRepoStub{inserted: true}
	settlement := &openAIRecordUsageBillingRepoStub{}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(logs, settlement, nil, nil, nil)
	price := 5.0
	group := &Group{ID: 81, Platform: PlatformDeepseek, RateMultiplier: 1, SearchPricePer1k: &price}
	// Use a synthetic catalog-priced model to prove both token and tool costs.
	priced := *result
	priced.Model, priced.BillingModel = "gpt-5.1", "gpt-5.1"
	require.NoError(t, svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &priced, APIKey: &APIKey{ID: 82, GroupID: &group.ID, Group: group},
		User: &User{ID: 83}, Account: &Account{ID: 84, Platform: PlatformDeepseek, Type: AccountTypeAPIKey},
	}))
	require.Equal(t, 1, logs.calls)
	require.Equal(t, 1, settlement.calls)
	require.NotNil(t, logs.lastLog)
	require.NotNil(t, settlement.lastCmd)
	require.Equal(t, max(result.Usage.InputTokens-result.Usage.CacheReadInputTokens-result.Usage.CacheCreationInputTokens, 0), logs.lastLog.InputTokens)
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

func TestDeepSeekHostedSearchContinuationExplicitHTTPErrorIsFree(t *testing.T) {
	for _, status := range []int{400, 401, 403, 422, 429, 500, 502, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			base := &hostedSearchUpstream{modelResponses: [][]byte{hostedSearchResponse("resp_synthetic_turn", 10, 2, hostedSearchCall())},
				searchResponse: `{"results":[{"title":"Synthetic result","url":"https://example.com/source"}]}`}
			svc, c, recorder, account, body := hostedSearchFixture(t, base, true, "auto", false)
			svc.httpUpstream = &deepSeekHostedHTTPErrorUpstream{base: base, failAt: 2, status: status,
				errorBody: `{"error":{"type":"invalid_request_error","message":"synthetic upstream rejection"}}`}
			result, err := svc.Forward(c.Request.Context(), c, account, body)
			require.Error(t, err)
			require.NotNil(t, result)
			require.Equal(t, OpenAIUsage{InputTokens: 10, OutputTokens: 2}, result.Usage)
			require.Equal(t, 1, result.SearchCount)
			require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: response.failed\n"))
			requireDeepSeekHostedBilling(t, result, true)
		})
	}
}

func TestDeepSeekHostedSearchContinuationWaitLimitIsFree(t *testing.T) {
	base := &hostedSearchUpstream{modelResponses: [][]byte{hostedSearchResponse("resp_synthetic_wait", 10, 2, hostedSearchCall())},
		searchResponse: `{"results":[{"title":"Synthetic result","url":"https://example.com/source"}]}`}
	svc, c, recorder, account, body := hostedSearchFixture(t, base, true, "auto", false)
	svc.httpUpstream = &hostedSearchDialFailUpstream{inner: base, failModel: map[int]error{1: fmt.Errorf("synthetic wait: %w", errOpenAIUpstreamWaitLimit)}}
	result, err := svc.Forward(c.Request.Context(), c, account, body)
	require.Error(t, err)
	require.Equal(t, 1, result.SearchCount)
	require.Equal(t, OpenAIUsage{InputTokens: 10, OutputTokens: 2}, result.Usage)
	require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: response.failed\n"))
	requireDeepSeekHostedBilling(t, result, true)
}

func TestDeepSeekHostedSearchExplicitStreamFailureBilling(t *testing.T) {
	for _, failure := range []string{"response.failed", "error", "truncated"} {
		t.Run(failure, func(t *testing.T) {
			base := &hostedSearchUpstream{modelResponses: [][]byte{
				hostedSearchResponse("resp_synthetic_tool", 10, 2, hostedSearchCall()),
				hostedSearchResponse("resp_synthetic_answer", 20, 3, hostedSearchAnswer()),
			}, searchResponse: `{"results":[{"title":"Synthetic result","url":"https://example.com/source"}]}`}
			base.modelBody = func(index int, response string) io.ReadCloser {
				if index == 1 {
					terminal := strings.Index(response, "event: response.completed\n")
					response = response[:terminal]
					if failure == "response.failed" {
						response += "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_synthetic_failure\",\"status\":\"failed\",\"output\":[],\"usage\":{\"input_tokens\":20,\"output_tokens\":3},\"error\":{\"code\":\"server_error\",\"message\":\"synthetic failure\"}}}\n\n"
					} else if failure == "error" {
						response += "event: error\ndata: {\"type\":\"error\",\"error\":{\"code\":\"server_error\",\"message\":\"synthetic failure\"}}\n\n"
					}
				}
				return io.NopCloser(strings.NewReader(response))
			}
			svc, c, _, account, body := hostedSearchFixture(t, base, true, "auto", false)
			result, err := svc.Forward(c.Request.Context(), c, account, body)
			require.Error(t, err)
			require.NotNil(t, result)
			require.Equal(t, 1, result.SearchCount)
			requireDeepSeekHostedBilling(t, result, failure != "truncated")
		})
	}
}

func TestDeepSeekHostedRoundErrorPreludeSuccessfulTerminalIsBillable(t *testing.T) {
	_, writer, _ := newDeepSeekHostedTerminalFixture(t, true)
	ctx := context.WithValue(context.Background(), deepSeekHostedRoundContextKey{}, writer)
	observeDeepSeekHostedUpstreamEvent(ctx, "error")
	require.True(t, writer.upstreamStreamError)
	observeDeepSeekHostedUpstreamEvent(ctx, "response.completed")
	require.False(t, writer.upstreamStreamError)
	// A gateway-generated idle timeout traverses the writer, not the raw-source hook.
	require.NoError(t, writer.frame([]byte(`data: {"type":"error","error":{"code":"stream_timeout"}}`)))
	require.False(t, writer.upstreamStreamError)
}

func TestDeepSeekHostedHTTPObservationIsAttemptScoped(t *testing.T) {
	_, writer, _ := newDeepSeekHostedTerminalFixture(t, true)
	req, err := http.NewRequestWithContext(context.WithValue(context.Background(), deepSeekHostedRoundContextKey{}, writer), http.MethodPost, "https://example.invalid/v1/responses", nil)
	require.NoError(t, err)
	observeDeepSeekHostedUpstreamResponse(req, &http.Response{StatusCode: http.StatusBadGateway})
	require.True(t, writer.upstreamHTTPError)
	observeDeepSeekHostedUpstreamResponse(req, &http.Response{StatusCode: http.StatusOK})
	require.False(t, writer.upstreamHTTPError)
	observeDeepSeekHostedUpstreamResponse(req, nil)
	require.False(t, writer.upstreamHTTPError)
}
