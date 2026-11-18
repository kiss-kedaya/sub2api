//go:build unit

package service

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func requireDeepSeekR4NormalSettlement(t *testing.T, result *OpenAIForwardResult) {
	t.Helper()
	require.NotNil(t, result)
	require.False(t, result.NonBillableUpstreamError)
	require.Equal(t, 1000, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.OutputTokens)
	rows := &openAIRecordUsageLogRepoStub{inserted: true}
	ledger := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
	billing := newOpenAIRecordUsageServiceWithBillingRepoForTest(rows, ledger, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
	copyResult := *result
	copyResult.Model, copyResult.BillingModel, copyResult.UpstreamModel = "gpt-5.1", "gpt-5.1", "gpt-5.1"
	require.NoError(t, billing.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &copyResult,
		APIKey: &APIKey{ID: 1000, Group: &Group{RateMultiplier: 1}},
		User:   &User{ID: 2000}, Account: &Account{ID: 3000, Type: AccountTypeAPIKey},
	}))
	require.Equal(t, 1, rows.calls)
	require.Equal(t, 1, ledger.calls)
	require.Positive(t, ledger.lastCmd.BalanceCost)
}

func TestDeepSeekStreamSilenceR4ReasoningFinishKeepsBilling(t *testing.T) {
	shortenDeepSeekStreamSilence(t, 80*time.Millisecond)
	for _, route := range []string{"responses", "messages"} {
		for _, finish := range []string{"stop", "length", "content_filter"} {
			t.Run(route+"/"+finish, func(t *testing.T) {
				wire := "data: {\"id\":\"chat_synthetic\",\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"synthetic reasoning\"}}],\"usage\":{\"prompt_tokens\":1000,\"completion_tokens\":2}}\n\n" +
					fmt.Sprintf("data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":%q}]}\n\n", finish)
				svc, c, rec, _, resp := streamSilenceFixture(t, "/v1/"+route, wire)
				var result *OpenAIForwardResult
				var err error
				switch route {
				case "responses":
					result, err = svc.streamChatCompletionsAsResponses(c, resp, "synthetic", nil, nil, false, nil, nil, "synthetic", "synthetic", nil, nil, time.Now())
				case "messages":
					result, err = svc.streamChatCompletionsAsAnthropic(c, resp, "synthetic", "synthetic", "synthetic", nil, nil, time.Now())
				}
				require.NoError(t, err)
				require.Contains(t, rec.Body.String(), "synthetic reasoning")
				require.NotContains(t, rec.Body.String(), "Upstream did not respond in time")
				if route == "responses" {
					require.Contains(t, rec.Body.String(), `"type":"response.completed"`)
				} else {
					require.Contains(t, rec.Body.String(), "event: message_stop")
				}
				requireDeepSeekR4NormalSettlement(t, result)
				_, hasLimit := c.Get(OpsUpstreamErrorsKey)
				require.False(t, hasLimit)
			})
		}
	}
}

func TestDeepSeekStreamSilenceR4ReasoningDoesNotClearPendingError(t *testing.T) {
	shortenDeepSeekStreamSilence(t, 80*time.Millisecond)
	wire := "data: {\"error\":{\"message\":\"synthetic provider rejection\"}}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"synthetic reasoning\"}}],\"usage\":{\"prompt_tokens\":1000,\"completion_tokens\":2}}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"
	svc, c, rec, account, resp := streamSilenceFixture(t, "/v1/responses", wire)
	result, err := svc.streamChatCompletionsAsResponses(c, resp, "synthetic", nil, nil, false, nil, nil, "synthetic", "synthetic", nil, nil, time.Now())
	requireStreamSilenceError(t, err, account)
	assertExplicitUpstreamErrorFree(t, result)
	require.NotContains(t, rec.Body.String(), `"type":"response.completed"`)
	require.Equal(t, 1, strings.Count(rec.Body.String(), `"type":"response.failed"`))
}

func TestDeepSeekStreamSilenceR4NativeTerminalWithoutBlank(t *testing.T) {
	shortenDeepSeekStreamSilence(t, 80*time.Millisecond)
	for _, kind := range []string{"response.completed", "response.failed"} {
		t.Run(kind, func(t *testing.T) {
			terminal := `{"type":"response.completed","response":{"id":"resp_synthetic","status":"completed","output":[],"usage":{"input_tokens":1000,"output_tokens":2}}}`
			if kind == "response.failed" {
				terminal = `{"type":"response.failed","response":{"id":"resp_synthetic","status":"failed","error":{"code":"invalid_request_error","message":"synthetic provider rejection"},"usage":{"input_tokens":1000,"output_tokens":2}}}`
			}
			svc, c, rec, account, resp := streamSilenceFixture(t, "/v1/responses", silenceResponsesPrelude+"event: "+kind+"\ndata: "+terminal+"\n")
			value, err := svc.handleStreamingResponse(c.Request.Context(), resp, c, account, time.Now(), "synthetic", "synthetic")
			require.NotNil(t, value)
			result := &OpenAIForwardResult{Usage: *value.usage, NonBillableUpstreamError: value.nonBillableUpstreamError}
			if kind == "response.completed" {
				require.NoError(t, err)
				requireDeepSeekR4NormalSettlement(t, result)
			} else {
				require.Error(t, err)
				require.NotErrorIs(t, err, errOpenAIUpstreamWaitLimit)
				assertExplicitUpstreamErrorFree(t, result)
			}
			require.NotContains(t, rec.Body.String(), "Upstream did not respond in time")
			require.True(t, strings.HasSuffix(rec.Body.String(), "\n\n"), "the final SSE event must be dispatched")
			count := 0
			for _, event := range strings.Split(rec.Body.String(), "\n\n") {
				for _, line := range strings.Split(event, "\n") {
					if strings.HasPrefix(line, "data: ") {
						payload := strings.TrimPrefix(line, "data: ")
						require.True(t, gjson.Valid(payload))
						if gjson.Get(payload, "type").String() == kind {
							count++
						}
					}
				}
			}
			require.Equal(t, 1, count)
			if raw, exists := c.Get(OpsUpstreamErrorsKey); exists {
				events, ok := raw.([]*OpsUpstreamErrorEvent)
				require.True(t, ok)
				for _, event := range events {
					require.NotEqual(t, "stream_silence_limit", event.Kind)
				}
			}
		})
	}
}

func TestDeepSeekStreamSilenceR4RawChatDeliveredErrorOnce(t *testing.T) {
	shortenDeepSeekStreamSilence(t, 80*time.Millisecond)
	wire := silenceChatPrelude + "data: {\"error\":{\"type\":\"invalid_request_error\",\"message\":\"synthetic provider rejection\"}}\n\n"
	svc, c, rec, account, resp := streamSilenceFixture(t, "/v1/chat/completions", wire)
	result, err := svc.streamRawChatCompletions(c, resp, account, "synthetic", "synthetic", "synthetic", nil, nil, time.Now(), int(deepSeekLargeRequestBytes)+1)
	requireStreamSilenceError(t, err, account)
	assertExplicitUpstreamErrorFree(t, result)
	require.Contains(t, rec.Body.String(), "synthetic provider rejection")
	require.NotContains(t, rec.Body.String(), "Upstream did not respond in time")
	require.Equal(t, 1, strings.Count(rec.Body.String(), `"error":`))
	require.True(t, IsResponseCommitted(c))
	requireOneStreamSilenceEvent(t, c)
}

func TestDeepSeekStreamSilenceR4RawChatBufferedErrorReleasedOnce(t *testing.T) {
	shortenDeepSeekStreamSilence(t, 80*time.Millisecond)
	wire := "data: {\"status\":\"failed\",\"message\":\"synthetic provider rejection\"}\n\n" + strings.TrimSuffix(silenceChatPrelude, "\n")
	svc, c, rec, account, resp := streamSilenceFixture(t, "/v1/chat/completions", wire)
	result, err := svc.streamRawChatCompletions(c, resp, account, "synthetic", "synthetic", "synthetic", nil, nil, time.Now(), int(deepSeekLargeRequestBytes)+1)
	requireStreamSilenceError(t, err, account)
	assertExplicitUpstreamErrorFree(t, result)
	require.Contains(t, rec.Body.String(), "synthetic provider rejection")
	require.NotContains(t, rec.Body.String(), "Upstream did not respond in time")
	require.True(t, IsResponseCommitted(c))
}

func TestDeepSeekStreamSilenceR4RawChatBufferedErrorStillTimesOut(t *testing.T) {
	shortenDeepSeekStreamSilence(t, 80*time.Millisecond)
	svc, c, rec, account, resp := streamSilenceFixture(t, "/v1/chat/completions", "data: {\"status\":\"failed\"}\n\n")
	result, err := svc.streamRawChatCompletions(c, resp, account, "synthetic", "synthetic", "synthetic", nil, nil, time.Now(), int(deepSeekLargeRequestBytes)+1)
	requireStreamSilenceError(t, err, account)
	require.NotNil(t, result)
	require.True(t, result.NonBillableUpstreamError)
	require.NotContains(t, rec.Body.String(), `"status":"failed"`)
	require.Contains(t, rec.Body.String(), "Upstream did not respond in time")
	require.Equal(t, 1, strings.Count(rec.Body.String(), `"error":`))
	require.True(t, IsResponseCommitted(c))
}

func TestDeepSeekStreamSilenceR4BufferedJSONCompleted(t *testing.T) {
	shortenDeepSeekStreamSilence(t, 150*time.Millisecond)
	body := []byte(fmt.Sprintf(`{"model":"deepseek-synthetic","input":"%s","stream":false}`, strings.Repeat("x", int(deepSeekLargeRequestBytes)+1)))
	completed := `{"id":"resp_synthetic","object":"response","status":"completed","output":[{"id":"msg_synthetic","type":"message","role":"assistant","content":[{"type":"output_text","text":"complete synthetic answer"}]}],"usage":{"input_tokens":1000,"output_tokens":2}}`
	for _, passthrough := range []bool{false, true} {
		for _, complete := range []bool{false, true} {
			t.Run(fmt.Sprintf("passthrough=%t/complete=%t", passthrough, complete), func(t *testing.T) {
				wire := completed
				if !complete {
					wire = strings.TrimSuffix(wire, "}")
				}
				c, rec := newOpenAIPartialUsageContext(t, body)
				account := newOpenAIPartialUsageAccount(passthrough)
				account.Platform, account.Schedulable = PlatformDeepseek, true
				account.Credentials["base_url"], account.Credentials["api_protocol"] = "https://example.invalid", APIProtocolResponses
				u := &streamSilenceUpstream{wire: wire, contentType: "application/json"}
				svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: u}
				result, err := svc.Forward(WithOpsGinContext(c.Request.Context(), c), c, account, body)
				require.Equal(t, 1, u.calls)
				if complete {
					require.NoError(t, err)
					require.Equal(t, http.StatusOK, rec.Code)
					require.JSONEq(t, completed, rec.Body.String())
					requireDeepSeekR4NormalSettlement(t, result)
					_, hasLimit := c.Get(OpsUpstreamErrorsKey)
					require.False(t, hasLimit)
				} else {
					requireStreamSilenceError(t, err, account)
					require.Equal(t, http.StatusGatewayTimeout, rec.Code)
					require.True(t, gjson.ValidBytes(rec.Body.Bytes()))
					require.Contains(t, rec.Body.String(), "Upstream did not respond in time")
					require.NotNil(t, result)
					require.True(t, result.NonBillableUpstreamError)
				}
			})
		}
	}
}
