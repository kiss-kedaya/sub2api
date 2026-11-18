//go:build unit

package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIResponsesFailureFollowsACompleteSSEEvent(t *testing.T) {
	const wire = "event: error\ndata: {\"error\":{\"message\":\"synthetic upstream error\"}}\n\n" +
		"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"synthetic answer\"}\n"
	for _, passthrough := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "passthrough"}[passthrough], func(t *testing.T) {
			svc, c, rec, _, _ := streamSilenceFixture(t, "/v1/responses", "")
			account := newOpenAIPartialUsageAccount(passthrough)
			resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(wire))}
			if passthrough {
				result, err := svc.handleStreamingResponsePassthrough(context.Background(), resp, c, account, time.Now(), "synthetic", "synthetic")
				require.Error(t, err)
				require.True(t, result.nonBillableUpstreamError)
			} else {
				result, err := svc.handleStreamingResponse(context.Background(), resp, c, account, time.Now(), "synthetic", "synthetic")
				require.Error(t, err)
				require.True(t, result.nonBillableUpstreamError)
			}
			events := assertReleaseSSEEvents(t, rec.Body.String())
			require.Equal(t, "response.failed", gjson.Get(events[len(events)-1], "type").String())
		})
	}
}

func TestDeepSeekStreamSilenceFinishesForwardedSSEEvent(t *testing.T) {
	shortenDeepSeekStreamSilence(t, 150*time.Millisecond)
	for _, route := range []string{"responses", "passthrough", "raw_chat", "raw_chat_error"} {
		t.Run(route, func(t *testing.T) {
			path, wire := "/v1/responses", strings.TrimSuffix(silenceResponsesPrelude, "\n")
			if strings.HasPrefix(route, "raw_chat") {
				path, wire = "/v1/chat/completions", strings.TrimSuffix(silenceChatPrelude, "\n")
				if route == "raw_chat_error" {
					wire = silenceChatPrelude + "data: {\"error\":{\"message\":\"synthetic upstream error\"}}\n"
				}
			}
			svc, c, rec, account, resp := streamSilenceFixture(t, path, wire)
			var err error
			var free bool
			switch route {
			case "responses":
				var result *openaiStreamingResult
				result, err = svc.handleStreamingResponse(context.Background(), resp, c, account, time.Now(), "synthetic", "synthetic")
				free = result.nonBillableUpstreamError
			case "passthrough":
				var result *openaiStreamingResultPassthrough
				result, err = svc.handleStreamingResponsePassthrough(context.Background(), resp, c, account, time.Now(), "synthetic", "synthetic")
				free = result.nonBillableUpstreamError
			default:
				var result *OpenAIForwardResult
				result, err = svc.streamRawChatCompletions(c, resp, account, "synthetic", "synthetic", "synthetic", nil, nil, time.Now(), 0)
				free = result.NonBillableUpstreamError
			}
			requireStreamSilenceError(t, err, account)
			require.True(t, free)
			requireOneStreamSilenceEvent(t, c)
			events := assertReleaseSSEEvents(t, rec.Body.String())
			require.Contains(t, events[len(events)-2], "partial answer")
			if route == "raw_chat_error" {
				require.Contains(t, events[len(events)-1], "synthetic upstream error")
				require.NotContains(t, rec.Body.String(), "Upstream did not respond in time")
			} else {
				require.Contains(t, events[len(events)-1], "Upstream did not respond in time")
			}
		})
	}
}

func assertReleaseSSEEvents(t *testing.T, body string) []string {
	t.Helper()
	require.True(t, strings.HasSuffix(body, "\n\n"), "last SSE event is not terminated")
	var events []string
	for _, frame := range strings.Split(body, "\n\n") {
		var data []string
		for _, line := range strings.Split(frame, "\n") {
			if payload, ok := extractOpenAISSEDataLine(line); ok {
				data = append(data, payload)
			}
		}
		if len(data) > 0 {
			payload := strings.Join(data, "\n")
			require.True(t, json.Valid([]byte(payload)), "one dispatched event must contain one JSON document: %q", payload)
			events = append(events, payload)
		}
	}
	require.GreaterOrEqual(t, len(events), 2)
	return events
}

func TestOpenAIR4BufferedImageIsAnAnswer(t *testing.T) {
	for _, image := range []string{
		`"content":[{"type":"image_url","image_url":{"url":"https://example.invalid/image.png"}}]`,
		`"images":[{"type":"image_url","image_url":{"url":"https://example.invalid/image.png"}}]`,
	} {
		complete := `{"choices":[{"finish_reason":"stop","message":{` + image + `}}],"error":{"message":"relay annotation"}}`
		require.False(t, openAIBufferedUpstreamError(gjson.Parse(complete), ""))
		require.True(t, openAIBufferedUpstreamError(gjson.Parse(complete), "error"))
	}
	require.True(t, openAIBufferedUpstreamError(gjson.Parse(`{"choices":[{"message":{"images":[{}]}}],"error":"synthetic failure"}`), ""))
}

func TestDeepSeekStreamSilenceCompletedJSONWithRelayAnnotation(t *testing.T) {
	const prefix = `{"status":"completed","error":{"message":"relay annotation"},`
	require.True(t, openAIStreamSilenceHasCompletedJSON([]byte(prefix+`"output":[{"type":"message","content":[{"type":"output_text","text":"synthetic answer"}]}]}`)))
	require.False(t, openAIStreamSilenceHasCompletedJSON([]byte(prefix+`"type":"response.failed","output":[{"type":"message"}]}`)))
	require.False(t, openAIStreamSilenceHasCompletedJSON([]byte(prefix+`"output":[]}`)))
}

func TestOpenAIR4NativeAnthropicNegativeDeltaIndex(t *testing.T) {
	const wire = gaps3AnthropicStart + "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"retained answer\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":-1,\"delta\":{\"type\":\"text_delta\",\"text\":\"invalid delta\"}}\n\n" + gaps3AnthropicStop
	for _, route := range []string{"chat", "responses"} {
		t.Run(route, func(t *testing.T) {
			c, rec := gaps3Context()
			svc := &OpenAIGatewayService{}
			resp := explicitErrorResponse(wire, "text/event-stream")
			var result *OpenAIForwardResult
			var err error
			require.NotPanics(t, func() {
				if route == "chat" {
					result, err = svc.handleCCBufferedFromNativeAnthropic(resp, c, "synthetic", "synthetic", "synthetic", nil, time.Now())
				} else {
					result, err = svc.handleResponsesBufferedFromNativeAnthropic(resp, c, "synthetic", "synthetic", "synthetic", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
				}
			})
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Contains(t, rec.Body.String(), "retained answer")
			require.NotContains(t, rec.Body.String(), "invalid delta")
		})
	}
}
