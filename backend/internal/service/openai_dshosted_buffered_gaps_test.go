//go:build unit

package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Each model turn uses the configured protocol; search never leaves the stub.
type dsHostedBufferedGapsUpstream struct {
	HTTPUpstream
	protocol, continuation, contentType string
	turns, searches                     int
}

func (u *dsHostedBufferedGapsUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	kind, wire := "application/json", ""
	if strings.HasSuffix(req.URL.Path, "/tools/search") {
		u.searches++
		wire = `{"results":[{"title":"Synthetic source","url":"https://example.invalid/source"}]}`
	} else {
		u.turns++
		if u.turns > 2 {
			return nil, fmt.Errorf("unexpected synthetic model turn %d", u.turns)
		}
		if u.turns == 2 {
			kind, wire = u.contentType, u.continuation
		} else {
			switch u.protocol {
			case APIProtocolChatCompletions:
				wire = hostedChatSearchJSON(1)
			case APIProtocolAnthropic:
				kind = "text/event-stream"
				wire = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_synthetic_search\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"deepseek-v4.1-flash\",\"content\":[],\"usage\":{\"input_tokens\":10,\"output_tokens\":2}}}\n\n" +
					"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"call_synthetic_search\",\"name\":\"web_search\",\"input\":{\"query\":\"synthetic\"}}}\n\n" +
					"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":2}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
			default:
				wire = string(hostedSearchResponse("resp_synthetic_search", 10, 2, hostedSearchCall()))
			}
		}
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {kind}}, Body: io.NopCloser(strings.NewReader(wire))}, nil
}

func TestDSHostedBufferedGapsJSONErrorWithoutUsage(t *testing.T) {
	for _, protocol := range []string{APIProtocolResponses, APIProtocolAnthropic, APIProtocolChatCompletions} {
		for _, kind := range []string{"application/json", "text/event-stream"} {
			t.Run(protocol+"/"+kind, func(t *testing.T) {
				upstream := &dsHostedBufferedGapsUpstream{protocol: protocol, contentType: kind,
					continuation: "{\n  \"error\": {\"type\":\"invalid_request_error\",\"message\":\"unknown parameter synthetic\"}\n}"}
				svc, c, _, account, body := hostedSearchFixture(t, &hostedSearchUpstream{}, false, "auto", false)
				account.Credentials["api_protocol"] = protocol
				account.Credentials["base_url"] = "https://example.invalid"
				apiKey, ok := c.MustGet("api_key").(*APIKey)
				require.True(t, ok)
				apiKey.Group.CrossProtocolConversionEnabled = true
				svc.httpUpstream = upstream
				result, err := svc.Forward(c.Request.Context(), c, account, body)
				require.Error(t, err)
				require.NotNil(t, result, "turns=%d searches=%d error=%v", upstream.turns, upstream.searches, err)
				require.Equal(t, 2, upstream.turns)
				require.Equal(t, 1, upstream.searches)
				require.Equal(t, OpenAIUsage{InputTokens: 10, OutputTokens: 2}, result.Usage)
				require.Equal(t, 1, result.SearchCount)
				requireDeepSeekHostedBilling(t, result, true)
			})
		}
	}
}

const dsHostedBufferedGapsError = "event: error\ndata: {\"type\":\"error\",\"error\":{\"code\":\"invalid_request\",\"message\":\"unknown parameter synthetic\"}}\n\n"
const dsHostedBufferedGapsUntypedError = "data: {\"error\":\"unknown parameter synthetic\"}\n\n"
const dsHostedBufferedGapsUsage = "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_synthetic_partial\",\"usage\":{\"input_tokens\":20,\"output_tokens\":3}}}\n\n"

func dsHostedBufferedGapsCases() []struct {
	name, wire string
	free       bool
	wantErr    bool
} {
	answer := hostedSearchSSE(hostedSearchResponse("resp_synthetic_answer", 20, 3, hostedSearchAnswer()))
	incomplete := "event: response.incomplete\ndata: {\"type\":\"response.incomplete\",\"response\":{\"id\":\"resp_synthetic_partial\",\"status\":\"incomplete\",\"output\":[],\"usage\":{\"input_tokens\":20,\"output_tokens\":3}}}\n\n"
	empty := hostedSearchSSE(hostedSearchResponse("resp_synthetic_empty", 20, 3))
	return []struct {
		name, wire string
		free       bool
		wantErr    bool
	}{
		{"error_then_incomplete", dsHostedBufferedGapsError + incomplete, true, true},
		{"error_then_cancelled", dsHostedBufferedGapsError + strings.ReplaceAll(incomplete, "incomplete", "cancelled"), true, true},
		{"error_then_canceled", dsHostedBufferedGapsError + strings.ReplaceAll(incomplete, "incomplete", "canceled"), true, true},
		{"untyped_error_then_done", dsHostedBufferedGapsUsage + dsHostedBufferedGapsUntypedError + "data: [DONE]\n\n", true, true},
		{"error_then_EOF", dsHostedBufferedGapsUsage + dsHostedBufferedGapsError, true, true},
		{"error_then_empty_completed", dsHostedBufferedGapsError + empty, false, false},
		{"delta_reconstructed_after_error", dsHostedBufferedGapsError + "data: {\"type\":\"response.output_text.delta\",\"delta\":\"The search API returns structured web results.\"}\n\n" + empty, false, false},
		{"untyped_object_error", dsHostedBufferedGapsUsage + "data: {\"error\":{\"message\":\"unknown parameter synthetic\"}}\n\n", true, true},
		{"error_then_completed_answer", dsHostedBufferedGapsError + answer, false, false},
		{"untyped_error_then_done_answer", dsHostedBufferedGapsUntypedError + strings.ReplaceAll(answer, "response.completed", "response.done"), false, false},
		{"EOF_without_error", dsHostedBufferedGapsUsage, false, false},
	}
}

func TestDSHostedBufferedGapsContinuationSettlement(t *testing.T) {
	for _, tc := range dsHostedBufferedGapsCases() {
		t.Run(tc.name, func(t *testing.T) {
			upstream := &dsHostedBufferedGapsUpstream{protocol: APIProtocolResponses, continuation: tc.wire, contentType: "text/event-stream"}
			svc, c, recorder, account, body := hostedSearchFixture(t, &hostedSearchUpstream{}, false, "auto", false)
			account.Credentials["base_url"] = "https://example.invalid"
			svc.httpUpstream = upstream
			result, err := svc.Forward(c.Request.Context(), c, account, body)
			if tc.wantErr || tc.name == "EOF_without_error" || tc.name == "error_then_empty_completed" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				if tc.name != "error_then_empty_completed" {
					require.Contains(t, recorder.Body.String(), "structured web results")
				}
			}
			require.NotNil(t, result)
			require.Equal(t, 2, upstream.turns)
			require.Equal(t, 1, upstream.searches)
			require.Equal(t, 1, result.SearchCount)
			require.Equal(t, OpenAIUsage{InputTokens: 30, OutputTokens: 5}, result.Usage)
			requireDeepSeekHostedBilling(t, result, tc.free)
		})
	}
}

func TestDSHostedBufferedGapsNativeJSONReadLimit(t *testing.T) {
	c, _ := newNonStreamingFailoverContext(t)
	svc := newNonStreamingFailoverService()
	svc.cfg.Gateway.UpstreamResponseReadMaxBytes = 8
	wire := "{\n\"error\": {\"message\": \"Synthetic rejection\"}\n}"
	result, err := svc.handleResponsesBufferedFromNativeAnthropic(explicitErrorResponse(wire, "application/json"), c, "model", "model", "model", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
	require.ErrorIs(t, err, ErrUpstreamResponseBodyTooLarge)
	require.Nil(t, result)
}

func TestDSHostedBufferedGapsReplayableFailureDropsUsage(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		t.Run(fmt.Sprint(passthrough), func(t *testing.T) {
			c, recorder := newNonStreamingFailoverContext(t)
			wire := dsHostedBufferedGapsUsage + "data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"message\":\"Selected model is at capacity. Please try a different model.\"}}}\n\n"
			svc := newNonStreamingFailoverService()
			if passthrough {
				result, err := svc.handleNonStreamingResponsePassthrough(c.Request.Context(), explicitErrorResponse(wire, "text/event-stream"), c, newNonStreamingFailoverAccount(), "model", "model")
				var failover *UpstreamFailoverError
				require.ErrorAs(t, err, &failover)
				require.Nil(t, result)
			} else {
				result, err := svc.handleNonStreamingResponse(c.Request.Context(), explicitErrorResponse(wire, "text/event-stream"), c, newNonStreamingFailoverAccount(), "model", "model")
				var failover *UpstreamFailoverError
				require.ErrorAs(t, err, &failover)
				require.Nil(t, result)
			}
			require.False(t, IsResponseCommitted(c))
			require.Empty(t, recorder.Body.String())
		})
	}
}

func TestDSHostedBufferedGapsForwardRetainsFinalFailureUsage(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		t.Run(fmt.Sprint(passthrough), func(t *testing.T) {
			body := []byte(`{"model":"gpt-5.1","stream":false,"input":"Synthetic request"}`)
			c, recorder := newOpenAIPartialUsageContext(t, body)
			upstream := &httpUpstreamRecorder{resp: explicitErrorResponse(dsHostedBufferedGapsUsage+dsHostedBufferedGapsUntypedError, "text/event-stream")}
			account := newOpenAIPartialUsageAccount(passthrough)
			account.Credentials["base_url"] = "https://example.invalid"
			result, err := newOpenAIPartialUsageService(upstream).Forward(c.Request.Context(), c, account, body)
			require.Error(t, err)
			require.Contains(t, err.Error(), "upstream response failed:")
			require.True(t, IsResponseCommitted(c))
			require.NotNil(t, result)
			require.Equal(t, OpenAIUsage{InputTokens: 20, OutputTokens: 3}, result.Usage)
			require.True(t, gjson.ValidBytes(recorder.Body.Bytes()), recorder.Body.String())
			assertExplicitUpstreamErrorFree(t, result)
		})
	}
}

func TestDSHostedBufferedGapsPassthroughJSONSource(t *testing.T) {
	for _, kind := range []string{"application/json", "text/event-stream"} {
		for _, withChoices := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/choices=%t", kind, withChoices), func(t *testing.T) {
				c, _ := newNonStreamingFailoverContext(t)
				_, round, _ := newDeepSeekHostedTerminalFixture(t, false)
				c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), deepSeekHostedRoundContextKey{}, round))
				wire := `{"error":{"message":"unknown parameter synthetic"}}`
				if withChoices {
					wire = `{"choices":[{"message":{"role":"assistant","content":"Synthetic answer"},"finish_reason":"stop"}],"error":{"message":"synthetic note"},"usage":{"prompt_tokens":20,"completion_tokens":3}}`
				}
				result, err := newNonStreamingFailoverService().handleNonStreamingResponsePassthrough(c.Request.Context(), explicitErrorResponse(wire, kind), c, newNonStreamingFailoverAccount(), "model", "model")
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, !withChoices, round.upstreamStreamError)
				require.Equal(t, !withChoices, result.nonBillableUpstreamError)
			})
		}
	}
}

func TestDSHostedBufferedGapsExcludedPolicySource(t *testing.T) {
	c, _ := newNonStreamingFailoverContext(t)
	_, round, _ := newDeepSeekHostedTerminalFixture(t, false)
	ctx := context.WithValue(c.Request.Context(), deepSeekHostedRoundContextKey{}, round)
	_, _, free := inspectOpenAIBufferedSSE(ctx, dsHostedBufferedGapsUsage+"data: {\"type\":\"error\",\"error\":{\"code\":\"cyber_policy\",\"message\":\"Synthetic policy\"}}\n\n")
	require.False(t, free)
	require.False(t, round.upstreamStreamError)
}

func TestDSHostedBufferedGapsOrdinaryAndPassthroughSettlement(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, tc := range dsHostedBufferedGapsCases() {
			t.Run(fmt.Sprintf("passthrough=%t/%s", passthrough, tc.name), func(t *testing.T) {
				c, recorder := newNonStreamingFailoverContext(t)
				svc := newNonStreamingFailoverService()
				state, round, _ := newDeepSeekHostedTerminalFixture(t, false)
				c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), deepSeekHostedRoundContextKey{}, round))
				resp := explicitErrorResponse(tc.wire, "text/event-stream")
				var usage *OpenAIUsage
				var free bool
				var err error
				if passthrough {
					result, handleErr := svc.handleNonStreamingResponsePassthrough(c.Request.Context(), resp, c, newNonStreamingFailoverAccount(), "model", "model")
					err = handleErr
					if result != nil {
						usage, free = result.usage, result.nonBillableUpstreamError
					}
				} else {
					result, handleErr := svc.handleNonStreamingResponse(c.Request.Context(), resp, c, newNonStreamingFailoverAccount(), "model", "model")
					err = handleErr
					if result != nil {
						usage, free = result.usage, result.nonBillableUpstreamError
					}
				}
				if tc.wantErr {
					require.Error(t, err)
					require.True(t, IsResponseCommitted(c), recorder.Body.String())
					require.Contains(t, err.Error(), "upstream response failed:")
				} else {
					require.NoError(t, err)
				}
				require.NotNil(t, usage)
				require.Equal(t, OpenAIUsage{InputTokens: 20, OutputTokens: 3}, *usage)
				require.Equal(t, tc.free, free)
				require.Equal(t, tc.free, round.upstreamStreamError)
				require.False(t, round.upstreamHTTPError)
				state.addResult(&OpenAIForwardResult{Usage: *usage, NonBillableUpstreamError: free})
				requireDeepSeekHostedBilling(t, state.finishResult(), tc.free)
			})
		}
	}
}
