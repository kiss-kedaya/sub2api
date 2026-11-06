//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/stretchr/testify/require"
)

func newIncompleteStreamAccumulator(events ...apicompat.ResponsesStreamEvent) *apicompat.BufferedResponseAccumulator {
	acc := apicompat.NewBufferedResponseAccumulator()
	for i := range events {
		acc.ProcessEvent(&events[i])
	}
	return acc
}

func TestEstimateIncompleteStreamOutputTokens(t *testing.T) {
	require.Zero(t, estimateIncompleteStreamOutputTokens(nil))
	require.Zero(t, estimateIncompleteStreamOutputTokens(newIncompleteStreamAccumulator()))

	text := "hello world, this is a streamed answer."
	require.Equal(t, estimateTokensForText(text), estimateIncompleteStreamOutputTokens(newIncompleteStreamAccumulator(
		apicompat.ResponsesStreamEvent{Type: "response.output_text.delta", Delta: "hello world, "},
		apicompat.ResponsesStreamEvent{Type: "response.output_text.delta", Delta: "this is a streamed answer."},
	)))

	mixed := newIncompleteStreamAccumulator(
		apicompat.ResponsesStreamEvent{Type: "response.reasoning_summary_text.delta", Delta: "thinking about the plan"},
		apicompat.ResponsesStreamEvent{Type: "response.output_text.delta", Delta: "done"},
		apicompat.ResponsesStreamEvent{Type: "response.output_item.added", OutputIndex: 2, Item: &apicompat.ResponsesOutput{
			Type: "function_call", CallID: "call_1", Name: "get_weather",
		}},
		apicompat.ResponsesStreamEvent{Type: "response.function_call_arguments.delta", OutputIndex: 2, Delta: `{"city":"Paris"}`},
	)
	want := estimateTokensForText("thinking about the plan") + estimateTokensForText("done") +
		estimateTokensForText("get_weather") + estimateTokensForText(`{"city":"Paris"}`)
	require.Equal(t, want, estimateIncompleteStreamOutputTokens(mixed))
}

func TestFillIncompleteStreamUsage(t *testing.T) {
	body := []byte(incompleteStreamRequestBody)

	t.Run("nothing observed fills output and input with the cache split", func(t *testing.T) {
		usage := &OpenAIUsage{}
		out, in := fillIncompleteStreamUsage(usage, 10, body)
		require.Equal(t, 10, out)
		require.Greater(t, in, 0)
		require.Equal(t, 10, usage.OutputTokens)
		require.Equal(t, in, usage.InputTokens, "InputTokens stays the total, as OpenAI reports it")
		require.Equal(t, in*95/100, usage.CacheReadInputTokens, "95% of the estimated input is priced as cache read")
		require.Less(t, usage.CacheReadInputTokens, usage.InputTokens, "some input must stay at the normal price")
		require.Zero(t, usage.CacheCreationInputTokens)
		require.True(t, usage.CacheReadEstimated, "an estimated cache share must be marked so a missing cache price bills as input")
	})
	t.Run("reported input is kept", func(t *testing.T) {
		usage := &OpenAIUsage{InputTokens: 13}
		out, in := fillIncompleteStreamUsage(usage, 10, body)
		require.Equal(t, 10, out)
		require.Zero(t, in)
		require.Equal(t, 13, usage.InputTokens)
		require.Zero(t, usage.CacheReadInputTokens, "a reported input count is never re-split")
		require.False(t, usage.CacheReadEstimated, "nothing was estimated on the input side")
		require.Equal(t, 10, usage.OutputTokens)
	})
	t.Run("reported cache read counts as an input side", func(t *testing.T) {
		usage := &OpenAIUsage{CacheReadInputTokens: 5}
		_, in := fillIncompleteStreamUsage(usage, 10, body)
		require.Zero(t, in)
		require.Zero(t, usage.InputTokens)
		require.False(t, usage.CacheReadEstimated, "a reported cache read is never marked as estimated")
	})
	t.Run("reported output is never overwritten", func(t *testing.T) {
		usage := &OpenAIUsage{InputTokens: 13, OutputTokens: 4}
		out, in := fillIncompleteStreamUsage(usage, 99, body)
		require.Zero(t, out)
		require.Zero(t, in)
		require.Equal(t, OpenAIUsage{InputTokens: 13, OutputTokens: 4}, *usage)
	})
	t.Run("reported image output is never overwritten", func(t *testing.T) {
		usage := &OpenAIUsage{ImageOutputTokens: 7}
		out, in := fillIncompleteStreamUsage(usage, 99, body)
		require.Zero(t, out)
		require.Zero(t, usage.OutputTokens)
		require.Equal(t, 7, usage.ImageOutputTokens)
		require.Greater(t, in, 0, "the input side was not reported, so it is still estimated")
	})
	t.Run("no output estimate still estimates the input", func(t *testing.T) {
		usage := &OpenAIUsage{}
		out, in := fillIncompleteStreamUsage(usage, 0, body)
		require.Zero(t, out)
		require.Zero(t, usage.OutputTokens)
		require.Greater(t, in, 0)
		require.Equal(t, in, usage.InputTokens)
		require.Equal(t, in*95/100, usage.CacheReadInputTokens)
	})
	t.Run("nil usage is safe", func(t *testing.T) {
		out, in := fillIncompleteStreamUsage(nil, 10, body)
		require.Zero(t, out)
		require.Zero(t, in)
	})
	t.Run("unparseable request still bills the output", func(t *testing.T) {
		usage := &OpenAIUsage{}
		out, in := fillIncompleteStreamUsage(usage, 10, []byte("not json"))
		require.Equal(t, 10, out)
		require.Zero(t, in)
		require.Equal(t, OpenAIUsage{OutputTokens: 10}, *usage)
	})
}

func TestApplyIncompleteOpenAIStreamUsage(t *testing.T) {
	body := []byte(incompleteStreamRequestBody)
	newResult := func() *openaiStreamingResult {
		return &openaiStreamingResult{usage: &OpenAIUsage{}, incompleteStream: true, incompleteOutputTokens: 10}
	}
	streamErr := errors.New("stream usage incomplete: missing terminal event")

	t.Run("generic stream error is estimated", func(t *testing.T) {
		result := newResult()
		applyIncompleteOpenAIStreamUsage(&Account{ID: 1}, result, body, "gpt-5.1", streamErr)
		require.Equal(t, 10, result.usage.OutputTokens)
		require.Greater(t, result.usage.InputTokens, 0)
		require.Equal(t, incompleteCacheReadTokens(result.usage.InputTokens), result.usage.CacheReadInputTokens)
	})
	t.Run("no error means a terminal frame was handled elsewhere", func(t *testing.T) {
		result := newResult()
		applyIncompleteOpenAIStreamUsage(&Account{ID: 1}, result, body, "gpt-5.1", nil)
		require.Equal(t, OpenAIUsage{}, *result.usage)
	})
	t.Run("a stream that reached a terminal frame is never estimated", func(t *testing.T) {
		result := newResult()
		result.incompleteStream = false
		applyIncompleteOpenAIStreamUsage(&Account{ID: 1}, result, body, "gpt-5.1", streamErr)
		require.Equal(t, OpenAIUsage{}, *result.usage)
	})
	t.Run("replayable failover attempts are never estimated", func(t *testing.T) {
		result := newResult()
		result.clientDisconnect = true
		applyIncompleteOpenAIStreamUsage(&Account{ID: 1}, result, body, "gpt-5.1", &UpstreamFailoverError{StatusCode: http.StatusBadGateway})
		require.Equal(t, OpenAIUsage{}, *result.usage)
	})
	t.Run("an upstream break that produced nothing stays free", func(t *testing.T) {
		result := newResult()
		result.incompleteOutputTokens = 0
		applyIncompleteOpenAIStreamUsage(&Account{ID: 1}, result, body, "gpt-5.1", errors.New("boom"))
		require.Equal(t, OpenAIUsage{}, *result.usage)
	})
	t.Run("a client that left before any output is billed for its input only", func(t *testing.T) {
		result := newResult()
		result.incompleteOutputTokens = 0
		result.clientDisconnect = true
		applyIncompleteOpenAIStreamUsage(&Account{ID: 1}, result, body, "gpt-5.1", streamErr)
		require.Zero(t, result.usage.OutputTokens)
		require.Greater(t, result.usage.InputTokens, 0)
		require.Equal(t, incompleteCacheReadTokens(result.usage.InputTokens), result.usage.CacheReadInputTokens)
	})
	t.Run("reported usage survives a client that left", func(t *testing.T) {
		result := newResult()
		result.usage = &OpenAIUsage{InputTokens: 13, OutputTokens: 4}
		result.clientDisconnect = true
		applyIncompleteOpenAIStreamUsage(&Account{ID: 1}, result, body, "gpt-5.1", streamErr)
		require.Equal(t, OpenAIUsage{InputTokens: 13, OutputTokens: 4}, *result.usage)
	})
	t.Run("nil result and nil usage are safe", func(t *testing.T) {
		applyIncompleteOpenAIStreamUsage(nil, nil, body, "gpt-5.1", errors.New("boom"))
		applyIncompleteOpenAIStreamUsage(nil, &openaiStreamingResult{incompleteStream: true, incompleteOutputTokens: 10}, body, "gpt-5.1", errors.New("boom"))
	})
}

// The stream handler only reports an estimate when no terminal frame was seen.
func TestHandleStreamingResponse_IncompleteOutputTokensOnlyWithoutTerminalFrame(t *testing.T) {
	completed := "event: response.completed\n" +
		`data: {"type":"response.completed","response":{"id":"resp_incomplete","status":"completed","usage":{"input_tokens":13,"output_tokens":4}}}` + "\n\n"
	deltas := incompleteStreamCreatedFrame + incompleteStreamDeltaOneFrame + incompleteStreamDeltaTwoFrame

	t.Run("no terminal frame", func(t *testing.T) {
		result, err := runOpenAIResponseFlushTest(
			newOpenAIResponseFlushRecorder(), io.NopCloser(strings.NewReader(deltas)), config.GatewayConfig{},
		)
		require.ErrorContains(t, err, "missing terminal event")
		require.NotNil(t, result)
		require.True(t, result.incompleteStream)
		require.Equal(t, estimateTokensForText(incompleteStreamDeltaOneText+incompleteStreamDeltaTwoText), result.incompleteOutputTokens)
		require.Zero(t, result.usage.OutputTokens, "the estimate stays separate from parsed usage")
	})
	t.Run("terminal frame", func(t *testing.T) {
		result, err := runOpenAIResponseFlushTest(
			newOpenAIResponseFlushRecorder(), io.NopCloser(strings.NewReader(deltas+completed)), config.GatewayConfig{},
		)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.False(t, result.incompleteStream)
		require.Zero(t, result.incompleteOutputTokens)
		require.Equal(t, 4, result.usage.OutputTokens)
	})
	t.Run("no produced output", func(t *testing.T) {
		result, err := runOpenAIResponseFlushTest(
			newOpenAIResponseFlushRecorder(), io.NopCloser(strings.NewReader(incompleteStreamCreatedFrame)), config.GatewayConfig{},
		)
		require.Error(t, err)
		if result != nil {
			require.True(t, result.incompleteStream)
			require.Zero(t, result.incompleteOutputTokens)
		}
	})
}

// Grok shares the streaming handler and gets the same fallback.
func TestOpenAIGatewayService_GrokIncompleteStreamIsEstimated(t *testing.T) {
	body := []byte(`{"model":"grok-4.1","input":"Write a very long story about a fox who learns to code.","stream":true}`)
	c, _ := newOpenAIPartialUsageContext(t, body)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":   []string{"text/event-stream"},
			"Xai-Request-Id": []string{"rid-grok-incomplete"},
		},
		Body: io.NopCloser(strings.NewReader(incompleteStreamCreatedFrame + incompleteStreamDeltaOneFrame + incompleteStreamDeltaTwoFrame)),
	}}
	account := &Account{
		ID: 812, Name: "grok-incomplete-usage", Platform: PlatformGrok,
		Type: AccountTypeAPIKey, Concurrency: 1,
		Credentials: map[string]any{"api_key": "xai-test"},
		Status:      StatusActive, Schedulable: true,
	}

	result, err := newOpenAIPartialUsageService(upstream).Forward(context.Background(), c, account, body)

	require.ErrorContains(t, err, "missing terminal event")
	require.NotNil(t, result, "delivered Grok output must reach billing")
	t.Logf("usage=%+v", result.Usage)
	require.Equal(t, estimateTokensForText(incompleteStreamDeltaOneText+incompleteStreamDeltaTwoText), result.Usage.OutputTokens)
	require.Greater(t, result.Usage.InputTokens, 0)
	require.Equal(t, incompleteCacheReadTokens(result.Usage.InputTokens), result.Usage.CacheReadInputTokens)
	require.Equal(t, "rid-grok-incomplete", result.RequestID)
}
