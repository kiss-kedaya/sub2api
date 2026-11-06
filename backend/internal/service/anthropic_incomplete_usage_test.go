//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// The output meter keeps no text, only counts, and must agree with the text
// estimator the rest of the gateway uses.
func TestAnthropicOutputMeter_MatchesTheTextEstimator(t *testing.T) {
	for name, pieces := range map[string][]string{
		"ascii":        {"Once upon a time a quick brown fox", " sat down at a keyboard."},
		"cjk":          {"从前有一只狐狸", "学会了写代码。"},
		"mixed":        {"The word ", "狐狸", " means fox."},
		"single rune":  {"a"},
		"empty pieces": {"", "hello world", ""},
	} {
		t.Run(name, func(t *testing.T) {
			var meter anthropicOutputMeter
			whole := ""
			for _, piece := range pieces {
				meter.add(piece)
				whole += piece
			}
			require.Equal(t, estimateTokensForText(whole), meter.tokens())
		})
	}
	var empty anthropicOutputMeter
	require.Zero(t, empty.tokens())
}

func decodeAnthropicStreamEvent(t *testing.T, data string) *apicompat.AnthropicStreamEvent {
	t.Helper()
	var event apicompat.AnthropicStreamEvent
	require.NoError(t, json.Unmarshal([]byte(data), &event))
	return &event
}

// The raw-data observer (native, passthrough, Bedrock) and the decoded-event
// observer (converted handlers) must count the same content.
func TestAnthropicStreamProgress_BothObserversAgree(t *testing.T) {
	events := []struct {
		name           string
		data           string
		wantTokens     int
		wantOutputSeen bool
	}{
		{"text delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello there friend"}}`, estimateTokensForText("hello there friend"), false},
		{"thinking delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"let me think about it"}}`, estimateTokensForText("let me think about it"), false},
		{"tool arguments", `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"city\":\"Paris\"}"}}`, estimateTokensForText(`{"city":"Paris"}`), false},
		{"tool name", `{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{}}}`, estimateTokensForText("get_weather"), false},
		{"server tool name", `{"type":"content_block_start","index":1,"content_block":{"type":"server_tool_use","id":"srvtoolu_1","name":"web_search","input":{}}}`, estimateTokensForText("web_search"), false},
		{"initial text", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"opening words here"}}`, estimateTokensForText("opening words here"), false},
		{"initial thinking", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"hmm okay then"}}`, estimateTokensForText("hmm okay then"), false},
		{"signature is not output", `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"abcdefabcdefabcdef"}}`, 0, false},
		{"message_start output is provisional", `{"type":"message_start","message":{"usage":{"input_tokens":5,"output_tokens":1}}}`, 0, false},
		{"message_delta reports the cumulative output", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":42}}`, 0, true},
		{"message_delta without usage", `{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`, 0, false},
		{"message_delta with null output", `{"type":"message_delta","usage":{"output_tokens":null}}`, 0, false},
		{"ping", `{"type":"ping"}`, 0, false},
		{"done marker", `[DONE]`, 0, false},
		{"not json", `not json at all`, 0, false},
	}
	for _, tc := range events {
		t.Run(tc.name, func(t *testing.T) {
			raw := &anthropicStreamProgress{}
			raw.observeData(tc.data)
			require.Equal(t, tc.wantTokens, raw.producedTokens())
			require.Equal(t, tc.wantOutputSeen, raw.outputReported)

			if json.Valid([]byte(tc.data)) {
				decoded := &anthropicStreamProgress{}
				decoded.observeEvent(decodeAnthropicStreamEvent(t, tc.data))
				require.Equal(t, raw.producedTokens(), decoded.producedTokens())
				require.Equal(t, raw.outputReported, decoded.outputReported)
			}
		})
	}
	var nilProgress *anthropicStreamProgress
	nilProgress.observeData(`{"type":"content_block_delta","delta":{"type":"text_delta","text":"x"}}`)
	nilProgress.observeEvent(&apicompat.AnthropicStreamEvent{Type: "ping"})
	require.Zero(t, nilProgress.producedTokens())
	(&anthropicStreamProgress{}).observeEvent(nil)
}

const anthropicIncompleteRequestBody = `{"model":"claude-sonnet-4","max_tokens":64,"messages":[{"role":"user","content":"Summarise this paragraph about gateways and billing in two sentences."}]}`

func newProducedProgress(text string) *anthropicStreamProgress {
	progress := &anthropicStreamProgress{}
	progress.text.add(text)
	return progress
}

func TestFillIncompleteAnthropicUsage(t *testing.T) {
	body := []byte(anthropicIncompleteRequestBody)
	wantSplit := splitIncompleteInput(estimateRequestInputTokens(body))
	require.Greater(t, wantSplit.Total, 0)
	produced := "a streamed answer of some length"

	t.Run("nothing reported fills output and the input split", func(t *testing.T) {
		usage := &ClaudeUsage{}
		out, in := fillIncompleteAnthropicUsage(usage, newProducedProgress(produced), body)
		require.Equal(t, estimateTokensForText(produced), out)
		require.Equal(t, wantSplit.Total, in)
		require.Equal(t, estimateTokensForText(produced), usage.OutputTokens)
		require.Equal(t, wantSplit.Fresh, usage.InputTokens, "Anthropic InputTokens is the fresh input only")
		require.Equal(t, wantSplit.CacheRead, usage.CacheReadInputTokens)
		require.True(t, usage.CacheReadEstimated)
	})
	t.Run("the provisional output of message_start is raised to the produced content", func(t *testing.T) {
		usage := &ClaudeUsage{InputTokens: 11, CacheReadInputTokens: 7, OutputTokens: 1}
		out, in := fillIncompleteAnthropicUsage(usage, newProducedProgress(produced), body)
		require.Equal(t, estimateTokensForText(produced), out)
		require.Zero(t, in, "reported input is never re-estimated")
		require.Equal(t, estimateTokensForText(produced), usage.OutputTokens)
		require.Equal(t, 11, usage.InputTokens)
		require.Equal(t, 7, usage.CacheReadInputTokens)
		require.False(t, usage.CacheReadEstimated)
	})
	t.Run("a larger reported output is never lowered", func(t *testing.T) {
		usage := &ClaudeUsage{InputTokens: 11, OutputTokens: 500}
		out, _ := fillIncompleteAnthropicUsage(usage, newProducedProgress(produced), body)
		require.Zero(t, out)
		require.Equal(t, 500, usage.OutputTokens)
	})
	t.Run("a message_delta that reported the output is authoritative", func(t *testing.T) {
		progress := newProducedProgress(produced)
		progress.outputReported = true
		usage := &ClaudeUsage{InputTokens: 11, OutputTokens: 3}
		out, _ := fillIncompleteAnthropicUsage(usage, progress, body)
		require.Zero(t, out)
		require.Equal(t, 3, usage.OutputTokens)
	})
	t.Run("any reported input side keeps the input as reported", func(t *testing.T) {
		for name, reported := range map[string]ClaudeUsage{
			"fresh input":    {InputTokens: 4},
			"cache read":     {CacheReadInputTokens: 900},
			"cache creation": {CacheCreationInputTokens: 600},
		} {
			usage := reported
			_, in := fillIncompleteAnthropicUsage(&usage, nil, body)
			require.Zero(t, in, name)
			require.Equal(t, reported, usage, name)
		}
	})
	t.Run("a body that cannot be estimated invents nothing", func(t *testing.T) {
		usage := &ClaudeUsage{}
		_, in := fillIncompleteAnthropicUsage(usage, nil, []byte(`{"unknown":true}`))
		require.Zero(t, in)
		require.Equal(t, ClaudeUsage{}, *usage)
	})
	t.Run("image output is left alone", func(t *testing.T) {
		usage := &ClaudeUsage{ImageOutputTokens: 1290}
		out, _ := fillIncompleteAnthropicUsage(usage, newProducedProgress(produced), body)
		require.Zero(t, out)
		require.Zero(t, usage.OutputTokens)
	})
	t.Run("nil usage is a no-op", func(t *testing.T) {
		out, in := fillIncompleteAnthropicUsage(nil, newProducedProgress(produced), body)
		require.Zero(t, out)
		require.Zero(t, in)
	})
}

// Input is billed once the request was dispatched and the client then left, or
// whenever output was produced. An upstream break that produced nothing is free.
func TestEstimateIncompleteAnthropicUsage_Gate(t *testing.T) {
	body := []byte(anthropicIncompleteRequestBody)
	for _, tc := range []struct {
		name           string
		clientGone     bool
		produced       string
		wantOutput     bool
		wantInput      bool
		wantNoEstimate bool
	}{
		{name: "client gone with nothing produced bills the input only", clientGone: true, wantInput: true},
		{name: "client gone with output bills both", clientGone: true, produced: "some real output", wantOutput: true, wantInput: true},
		{name: "client attached with output bills both", produced: "some real output", wantOutput: true, wantInput: true},
		{name: "client attached with nothing produced stays free", wantNoEstimate: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usage := &ClaudeUsage{}
			out, in := estimateIncompleteAnthropicUsage(usage, newProducedProgress(tc.produced), body, tc.clientGone)
			require.Equal(t, tc.wantOutput, out > 0)
			require.Equal(t, tc.wantInput, in > 0)
			if tc.wantNoEstimate {
				require.Equal(t, ClaudeUsage{}, *usage)
			}
		})
	}
	out, in := estimateIncompleteAnthropicUsage(nil, nil, body, true)
	require.Zero(t, out)
	require.Zero(t, in)
}

func TestApplyIncompleteAnthropicStreamUsage_Eligibility(t *testing.T) {
	body := []byte(anthropicIncompleteRequestBody)
	incomplete := func(gone bool) *streamingResult {
		return &streamingResult{usage: &ClaudeUsage{}, incompleteStream: true, clientDisconnect: gone, progress: newProducedProgress("output the upstream produced")}
	}
	apply := func(result *streamingResult, err error) (int, int) {
		return applyIncompleteAnthropicStreamUsage(&Account{ID: 5}, result, body, "claude-sonnet-4", "req_1", err)
	}

	t.Run("an incomplete stream is estimated", func(t *testing.T) {
		result := incomplete(false)
		out, in := apply(result, errors.New("stream usage incomplete: missing terminal event"))
		require.Greater(t, out, 0)
		require.Greater(t, in, 0)
		require.Equal(t, out, result.usage.OutputTokens)
	})
	t.Run("a stream that reached its terminal frame is not estimated", func(t *testing.T) {
		result := incomplete(true)
		result.incompleteStream = false
		out, in := apply(result, nil)
		require.Zero(t, out)
		require.Zero(t, in)
		require.Equal(t, ClaudeUsage{}, *result.usage)
	})
	t.Run("errors with their own accounting are skipped", func(t *testing.T) {
		for name, err := range map[string]error{
			"replayable failover":      &UpstreamFailoverError{StatusCode: http.StatusBadGateway},
			"wrapped failover":         fmt.Errorf("attempt failed: %w", &UpstreamFailoverError{StatusCode: http.StatusBadGateway}),
			"forwarded upstream error": &AnthropicPassthroughStreamError{StatusCode: http.StatusBadGateway},
			"balance abort":            wrapStreamOutputHoldTopUpFailure(ErrBalanceWithholdingFailed),
		} {
			result := incomplete(true)
			out, in := apply(result, err)
			require.Zero(t, out, name)
			require.Zero(t, in, name)
			require.Equal(t, ClaudeUsage{}, *result.usage, name)
		}
	})
	t.Run("nothing to work with is a no-op", func(t *testing.T) {
		for name, result := range map[string]*streamingResult{
			"nil result": nil,
			"nil usage":  {incompleteStream: true},
		} {
			out, in := apply(result, errors.New("boom"))
			require.Zero(t, out, name)
			require.Zero(t, in, name)
		}
	})
}

// A departed client is never an upstream fault: the error matches context.Canceled
// (so affinity and account health are left alone) and keeps the underlying cause.
func TestAnthropicClientGoneError(t *testing.T) {
	cause := io.ErrClosedPipe
	err := newAnthropicClientGoneError(anthropicGoneStream, cause)
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorIs(t, err, io.ErrClosedPipe)
	require.Equal(t, "stream usage incomplete after disconnect: io: read/write on closed pipe", err.Error())

	require.ErrorIs(t, newAnthropicClientGoneError(anthropicGoneRequest, nil), context.Canceled, "a missing cause is the cancellation itself")
	require.ErrorIs(t, newAnthropicClientGoneError(anthropicGoneRequest, context.DeadlineExceeded), context.Canceled)

	var failover *UpstreamFailoverError
	require.False(t, errors.As(err, &failover), "never a failover")
}

func TestAnthropicClientContexts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	newCtx := func(reqCtx context.Context) *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(reqCtx)
		return c
	}
	execCtx, cancelExec := context.WithCancel(context.Background())
	defer cancelExec()
	reqCtx, cancelReq := context.WithCancel(context.Background())
	defer cancelReq()

	require.Len(t, anthropicClientContexts(execCtx, newCtx(reqCtx)), 2, "two different cancelation sources are both watched")
	require.Len(t, anthropicClientContexts(reqCtx, newCtx(reqCtx)), 1, "the same source is watched once")
	derived := context.WithValue(reqCtx, upstreamContextTestKey("k"), "v")
	require.Len(t, anthropicClientContexts(derived, newCtx(reqCtx)), 1, "a value-derived context shares the cancelation")
	require.Empty(t, anthropicClientContexts(context.Background(), newCtx(context.Background())), "a context that can never end is not watched")
	var noContext context.Context
	require.Empty(t, anthropicClientContexts(noContext, nil), "a missing context and request are tolerated")
	require.Len(t, anthropicClientContexts(execCtx, nil), 1)
}

type anthropicCloseCounter struct{ closed atomic.Int32 }

func (c *anthropicCloseCounter) Close() error {
	c.closed.Add(1)
	return nil
}

func TestAnthropicClientWatch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	newCtx := func(ctx context.Context) *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(ctx)
		return c
	}

	t.Run("ending the context closes the upstream once", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		closer := &anthropicCloseCounter{}
		watch := newAnthropicClientWatch(ctx, newCtx(ctx), closer)
		defer watch.stop()
		require.False(t, watch.isGone())
		cancel()
		require.Eventually(t, func() bool { return closer.closed.Load() == 1 }, time.Second, time.Millisecond)
		require.True(t, watch.isGone())
		watch.markGone()
		watch.markGone()
		require.Equal(t, int32(1), closer.closed.Load(), "closing is idempotent")
	})
	t.Run("a failed client write closes the upstream without any context ending", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		closer := &anthropicCloseCounter{}
		watch := newAnthropicClientWatch(ctx, newCtx(ctx), closer)
		defer watch.stop()
		watch.markGone()
		require.True(t, watch.isGone())
		require.Equal(t, int32(1), closer.closed.Load())
	})
	t.Run("isGone sees an ended context before the callback ran", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		closer := &anthropicCloseCounter{}
		watch := newAnthropicClientWatch(ctx, newCtx(ctx), closer)
		defer watch.stop()
		require.True(t, watch.isGone())
		require.Eventually(t, func() bool { return closer.closed.Load() >= 1 }, time.Second, time.Millisecond)
	})
	t.Run("stopping the watch leaves the upstream alone", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		closer := &anthropicCloseCounter{}
		watch := newAnthropicClientWatch(ctx, newCtx(ctx), closer)
		watch.stop()
		cancel()
		time.Sleep(20 * time.Millisecond)
		require.Zero(t, closer.closed.Load())
	})
	t.Run("a nil watch is safe", func(t *testing.T) {
		var watch *anthropicClientWatch
		watch.markGone()
		watch.stop()
		require.False(t, watch.isGone())
	})
}

func newAnthropicCallTestContext(ctx context.Context) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(ctx)
	return c
}

// An upstream call ends with its client, also when a stream's upstream context is
// detached from the request: the departed client closes the upstream at once.
func TestAnthropicUpstreamCall_EndsWithTheClient(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			clientCtx, cancel := context.WithCancel(context.Background())
			defer cancel()
			call := newAnthropicUpstreamCall(clientCtx, newAnthropicCallTestContext(clientCtx), stream)
			defer call.close()
			attemptCtx := call.attempt()
			require.NoError(t, attemptCtx.Err())
			require.False(t, call.clientGone())

			cancel()

			require.Eventually(t, func() bool { return attemptCtx.Err() != nil }, time.Second, time.Millisecond,
				"the upstream request must end with the client")
			require.True(t, call.clientGone())
		})
	}
	t.Run("close ends the call and ignores a later client end", func(t *testing.T) {
		clientCtx, cancel := context.WithCancel(context.Background())
		call := newAnthropicUpstreamCall(clientCtx, newAnthropicCallTestContext(clientCtx), true)
		attemptCtx := call.attempt()
		call.close()
		require.Error(t, attemptCtx.Err())
		cancel()
		call.close()
	})
}

// "Dispatched" means the whole request was written and nothing answered yet; each
// try starts from scratch, so a try the upstream rejected never makes a later try
// that was cut off look dispatched.
func TestAnthropicUpstreamCall_DispatchedIsPerTry(t *testing.T) {
	call := newAnthropicUpstreamCall(context.Background(), nil, false)
	defer call.close()
	require.False(t, call.dispatched(), "no try yet")

	trace := httptrace.ContextClientTrace(call.attempt())
	require.NotNil(t, trace)
	require.False(t, call.dispatched(), "built but not written")
	trace.WroteRequest(httptrace.WroteRequestInfo{Err: errors.New("write failed")})
	require.False(t, call.dispatched(), "a failed write is not a dispatch")
	trace.WroteRequest(httptrace.WroteRequestInfo{})
	require.True(t, call.dispatched())
	trace.GotFirstResponseByte()
	require.False(t, call.dispatched(), "an answered try is judged by its answer")

	retry := httptrace.ContextClientTrace(call.attempt())
	require.False(t, call.dispatched(), "the next try starts clean")
	retry.WroteRequest(httptrace.WroteRequestInfo{})
	require.True(t, call.dispatched())
	call.markAnswered()
	require.False(t, call.dispatched())
}

func TestAnthropicUpstreamCall_DepartedClient(t *testing.T) {
	clientCtx, cancel := context.WithCancel(context.Background())
	call := newAnthropicUpstreamCall(clientCtx, newAnthropicCallTestContext(clientCtx), false)
	defer call.close()
	wrapped := fmt.Errorf("Post: %w", context.Canceled)

	require.False(t, call.departedClient(wrapped), "the client is still there")
	cancel()
	require.True(t, call.departedClient(wrapped))
	require.True(t, call.departedClient(context.DeadlineExceeded))
	require.False(t, call.departedClient(nil))
	require.False(t, call.departedClient(errors.New("connection reset")), "a real transport fault stays a fault")
	require.False(t, call.departedClient(&UpstreamFailoverError{StatusCode: http.StatusBadGateway}))
	require.False(t, call.departedClient(fmt.Errorf("x: %w", &UpstreamFailoverError{StatusCode: http.StatusBadGateway})))
}

func TestAnthropicClientGoneResult(t *testing.T) {
	body := []byte(anthropicIncompleteRequestBody)
	split := splitIncompleteInput(estimateRequestInputTokens(body))
	require.Greater(t, split.Total, 0)

	t.Run("a request without a response", func(t *testing.T) {
		result := anthropicClientGoneResult(&Account{ID: 9}, body, "claude-sonnet-4", "claude-sonnet-4-5", true, time.Now().Add(-time.Second), nil)
		require.NotNil(t, result)
		require.True(t, result.ClientDisconnect)
		require.True(t, result.Stream)
		require.Equal(t, "claude-sonnet-4", result.Model)
		require.Equal(t, "claude-sonnet-4-5", result.UpstreamModel)
		require.Equal(t, ClaudeUsage{InputTokens: split.Fresh, CacheReadInputTokens: split.CacheRead, CacheReadEstimated: true}, result.Usage)
		require.Empty(t, result.RequestID)
		require.Nil(t, result.UpstreamHeaders)
		require.GreaterOrEqual(t, result.Duration, time.Second)
	})
	t.Run("a request whose headers arrived keeps the upstream identity", func(t *testing.T) {
		resp := &http.Response{Header: http.Header{"X-Request-Id": {"req_up_1"}}}
		result := anthropicClientGoneResult(nil, body, "m", "m", false, time.Now(), resp)
		require.NotNil(t, result)
		require.False(t, result.Stream)
		require.Equal(t, "req_up_1", result.RequestID)
		require.Equal(t, resp.Header, result.UpstreamHeaders)
	})
	t.Run("a body that cannot be estimated bills nothing", func(t *testing.T) {
		require.Nil(t, anthropicClientGoneResult(&Account{ID: 9}, []byte(`{"unknown":true}`), "m", "m", false, time.Now(), nil))
		require.Nil(t, anthropicClientGoneResult(&Account{ID: 9}, nil, "m", "m", false, time.Now(), nil))
	})
}
