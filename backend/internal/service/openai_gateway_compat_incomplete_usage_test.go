//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const compatTestRequestBody = `{"model":"gpt-5.1","input":"Write a very long story about a fox who learns to code.","stream":true}`

func compatTestContext(t *testing.T) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	return c
}

func TestApplyCompatIncompleteUsage(t *testing.T) {
	account := &Account{ID: 7}
	wantTotal := estimateRequestInputTokens([]byte(compatTestRequestBody))
	require.Greater(t, wantTotal, 0)
	wantSplit := splitIncompleteInput(wantTotal)

	tests := []struct {
		name  string
		stash *Account // nil: nothing stashed
		in    compatIncompleteUsage
		usage OpenAIUsage
		want  OpenAIUsage
	}{
		{
			name:  "client gone, nothing produced: input only, 95% as cache read",
			stash: account,
			in:    compatIncompleteUsage{ClientGone: true},
			want:  OpenAIUsage{InputTokens: wantSplit.Total, CacheReadInputTokens: wantSplit.CacheRead, CacheReadEstimated: true},
		},
		{
			name:  "client gone with output: both sides filled",
			stash: account,
			in:    compatIncompleteUsage{ClientGone: true, OutputEstimate: 27},
			want:  OpenAIUsage{InputTokens: wantSplit.Total, CacheReadInputTokens: wantSplit.CacheRead, CacheReadEstimated: true, OutputTokens: 27},
		},
		{
			name:  "client attached and nothing produced stays free",
			stash: account,
			in:    compatIncompleteUsage{OutputEstimate: 0},
			want:  OpenAIUsage{},
		},
		{
			name:  "client attached but output produced is billed",
			stash: account,
			in:    compatIncompleteUsage{OutputEstimate: 9},
			want:  OpenAIUsage{InputTokens: wantSplit.Total, CacheReadInputTokens: wantSplit.CacheRead, CacheReadEstimated: true, OutputTokens: 9},
		},
		{
			name:  "a replayable failover error is never estimated",
			stash: account,
			in:    compatIncompleteUsage{ClientGone: true, OutputEstimate: 27, Cause: &UpstreamFailoverError{StatusCode: http.StatusBadGateway}},
			want:  OpenAIUsage{},
		},
		{
			name:  "a wrapped failover error is never estimated",
			stash: account,
			in:    compatIncompleteUsage{OutputEstimate: 27, Cause: errors.Join(errors.New("x"), &UpstreamFailoverError{StatusCode: 502})},
			want:  OpenAIUsage{},
		},
		{
			name: "no stashed request body: nothing is invented for the input",
			in:   compatIncompleteUsage{ClientGone: true, OutputEstimate: 27},
			want: OpenAIUsage{OutputTokens: 27},
		},
		{
			name:  "a body stashed by another account is not used",
			stash: &Account{ID: 8},
			in:    compatIncompleteUsage{Account: account, ClientGone: true},
			want:  OpenAIUsage{},
		},
		{
			name:  "reported input is kept, only the output is filled",
			stash: account,
			in:    compatIncompleteUsage{ClientGone: true, OutputEstimate: 27},
			usage: OpenAIUsage{InputTokens: 13},
			want:  OpenAIUsage{InputTokens: 13, OutputTokens: 27},
		},
		{
			name:  "a reported cache read counts as reported input",
			stash: account,
			in:    compatIncompleteUsage{ClientGone: true},
			usage: OpenAIUsage{CacheReadInputTokens: 40},
			want:  OpenAIUsage{CacheReadInputTokens: 40},
		},
		{
			name:  "a reported cache write counts as reported input",
			stash: account,
			in:    compatIncompleteUsage{ClientGone: true},
			usage: OpenAIUsage{CacheCreationInputTokens: 40},
			want:  OpenAIUsage{CacheCreationInputTokens: 40},
		},
		{
			name:  "reported output is never replaced",
			stash: account,
			in:    compatIncompleteUsage{ClientGone: true, OutputEstimate: 27},
			usage: OpenAIUsage{InputTokens: 13, OutputTokens: 4},
			want:  OpenAIUsage{InputTokens: 13, OutputTokens: 4},
		},
		{
			name:  "reported image output keeps the output side",
			stash: account,
			in:    compatIncompleteUsage{ClientGone: true, OutputEstimate: 27},
			usage: OpenAIUsage{InputTokens: 13, ImageOutputTokens: 1},
			want:  OpenAIUsage{InputTokens: 13, ImageOutputTokens: 1},
		},
		{
			name:  "a placeholder output is replaced by a larger estimate",
			stash: account,
			in:    compatIncompleteUsage{ClientGone: true, OutputEstimate: 27, ReplaceOutputUpTo: 1},
			usage: OpenAIUsage{InputTokens: 93, OutputTokens: 1},
			want:  OpenAIUsage{InputTokens: 93, OutputTokens: 27},
		},
		{
			name:  "a placeholder output is not lowered by a smaller estimate",
			stash: account,
			in:    compatIncompleteUsage{ClientGone: true, OutputEstimate: 1, ReplaceOutputUpTo: 1},
			usage: OpenAIUsage{InputTokens: 93, OutputTokens: 1},
			want:  OpenAIUsage{InputTokens: 93, OutputTokens: 1},
		},
		{
			name:  "an output above the placeholder bound is real and kept",
			stash: account,
			in:    compatIncompleteUsage{ClientGone: true, OutputEstimate: 27, ReplaceOutputUpTo: 1},
			usage: OpenAIUsage{InputTokens: 93, OutputTokens: 16},
			want:  OpenAIUsage{InputTokens: 93, OutputTokens: 16},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := compatTestContext(t)
			if tt.stash != nil {
				stashCompatUpstreamRequestBody(c, tt.stash, []byte(compatTestRequestBody))
			}
			in := tt.in
			if in.Account == nil {
				in.Account = account
			}
			usage := tt.usage
			applyCompatIncompleteUsage(c, &usage, in)
			require.Equal(t, tt.want, usage)
		})
	}

	t.Run("a nil usage is ignored", func(t *testing.T) {
		require.NotPanics(t, func() {
			applyCompatIncompleteUsage(compatTestContext(t), nil, compatIncompleteUsage{ClientGone: true})
		})
	})
}

func TestCompatBufferedGoneResult(t *testing.T) {
	c := compatTestContext(t)
	account := &Account{ID: 7}
	stashCompatUpstreamRequestBody(c, account, []byte(compatTestRequestBody))

	result := compatBufferedGoneResult(c, account, "test", &OpenAIForwardResult{Model: "gpt-5.1"}, OpenAIUsage{}, nil, io.ErrClosedPipe)
	require.NotNil(t, result)
	require.True(t, result.ClientDisconnect)
	require.True(t, result.Usage.CacheReadEstimated)
	require.Greater(t, result.Usage.InputTokens, 0)

	// Nothing stashed and nothing produced: nothing to bill, the caller keeps its error.
	empty := compatBufferedGoneResult(compatTestContext(t), account, "test", &OpenAIForwardResult{}, OpenAIUsage{}, nil, io.ErrClosedPipe)
	require.Nil(t, empty)
}

func TestCompatChatTally(t *testing.T) {
	const (
		text      = "Once upon a time a quick brown fox sat down at a keyboard "
		reasoning = "The user wants a story, so start with the setting."
		toolName  = "lookup_weather"
		toolArgs  = `{"city":"Paris"}`
	)
	payloads := []string{
		`{"choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`,
		`{"choices":[{"index":0,"delta":{"reasoning_content":"` + reasoning + `"}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"` + text + `"}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"` + toolName + `","arguments":""}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":\"Paris\"}"}}]}}]}`,
		`{"choices":[],"usage":{"prompt_tokens":3,"completion_tokens":4}}`,
	}
	want := estimateTokensForText(text) + estimateTokensForText(reasoning) + estimateTokensForText(toolName+toolArgs)

	var fromPayloads compatChatTally
	for _, payload := range payloads {
		fromPayloads.AddPayload(payload)
	}
	require.Equal(t, want, fromPayloads.EstimateTokens())

	// The typed path agrees with the raw path.
	var fromChunks compatChatTally
	for _, payload := range payloads {
		var chunk apicompat.ChatCompletionsChunk
		require.NoError(t, json.Unmarshal([]byte(payload), &chunk))
		fromChunks.AddChunk(&chunk)
	}
	require.Equal(t, want, fromChunks.EstimateTokens())

	t.Run("reasoning_content wins over reasoning and is not counted twice", func(t *testing.T) {
		var tally compatChatTally
		tally.AddPayload(`{"choices":[{"delta":{"reasoning_content":"` + reasoning + `","reasoning":"` + reasoning + `"}}]}`)
		require.Equal(t, estimateTokensForText(reasoning), tally.EstimateTokens())
	})
	t.Run("content that is not a string is ignored", func(t *testing.T) {
		var tally compatChatTally
		tally.AddPayload(`{"choices":[{"delta":{"content":null}}]}`)
		tally.AddPayload(`{"choices":[{"delta":{"content":[{"type":"text","text":"ignored"}]}}]}`)
		require.Zero(t, tally.EstimateTokens())
	})
	t.Run("refusal and the legacy function_call are counted", func(t *testing.T) {
		var tally compatChatTally
		tally.AddPayload(`{"choices":[{"delta":{"refusal":"I cannot help with that request."}}]}`)
		tally.AddPayload(`{"choices":[{"delta":{"function_call":{"name":"f","arguments":"{}"}}}]}`)
		require.Equal(t, estimateTokensForText("I cannot help with that request.")+estimateTokensForText("f{}"), tally.EstimateTokens())
	})
	t.Run("a nil chunk is ignored", func(t *testing.T) {
		var tally compatChatTally
		tally.AddChunk(nil)
		require.Zero(t, tally.EstimateTokens())
	})
}

func TestCompatAnthropicTally(t *testing.T) {
	var tally compatAnthropicTally
	for _, data := range []string{
		`{"type":"message_start","message":{"usage":{"input_tokens":93,"output_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Plan the story first."}}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Once upon a time"}}`,
		`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"t1","name":"lookup","input":{}}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"q\":\"fox\"}"}}`,
	} {
		tally.AddData(data)
	}
	want := estimateTokensForText("Plan the story first.") + estimateTokensForText("Once upon a time") + estimateTokensForText(`lookup{"q":"fox"}`)
	require.Equal(t, want, tally.EstimateTokens())
	require.False(t, tally.sawFinalOutput, "message_start only carries a placeholder output count")

	tally.AddData(`{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`)
	require.False(t, tally.sawFinalOutput, "a message_delta without usage reports nothing")
	tally.AddData(`{"type":"message_delta","delta":{},"usage":{"output_tokens":null}}`)
	require.False(t, tally.sawFinalOutput, "a null output count reports nothing")
	tally.AddData(`{"type":"message_delta","delta":{},"usage":{"output_tokens":16}}`)
	require.True(t, tally.sawFinalOutput)
}

type compatCountingCloser struct{ closed atomic.Int32 }

func (c *compatCountingCloser) Close() error {
	c.closed.Add(1)
	return nil
}

func TestCompatClientWatch(t *testing.T) {
	t.Run("without a request it is inert and Disconnect closes the body once", func(t *testing.T) {
		body := &compatCountingCloser{}
		watch := newCompatClientWatch(nil, body)
		defer watch.Stop()
		require.False(t, watch.Gone())
		require.Nil(t, watch.Done())
		watch.Disconnect()
		watch.Disconnect()
		require.True(t, watch.Gone())
		require.Equal(t, int32(1), body.closed.Load())
	})

	t.Run("a request that ends closes the body", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		c := compatTestContext(t)
		c.Request = c.Request.WithContext(ctx)
		body := &compatCountingCloser{}
		watch := newCompatClientWatch(c, body)
		defer watch.Stop()
		require.False(t, watch.Gone())
		cancel()
		require.Eventually(t, func() bool { return body.closed.Load() == 1 }, 2*time.Second, time.Millisecond)
		require.True(t, watch.Gone())
	})

	t.Run("a request that already ended is gone at once", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		c := compatTestContext(t)
		c.Request = c.Request.WithContext(ctx)
		watch := newCompatClientWatch(c, &compatCountingCloser{})
		defer watch.Stop()
		require.True(t, watch.Gone())
	})

	t.Run("Stop ends the watching without closing the body", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		c := compatTestContext(t)
		c.Request = c.Request.WithContext(ctx)
		body := &compatCountingCloser{}
		watch := newCompatClientWatch(c, body)
		watch.Stop()
		watch.Stop()
		cancel()
		time.Sleep(20 * time.Millisecond)
		require.Zero(t, body.closed.Load())
	})

	t.Run("a read error after the client left reads as a canceled context", func(t *testing.T) {
		c := compatTestContext(t)
		resp := &http.Response{Body: passthroughErrReadCloser{err: io.ErrClosedPipe}}
		watch := newCompatClientWatch(c, resp.Body)
		defer watch.Stop()
		wrapped := watch.wrapResponse(resp)
		_, err := wrapped.Body.Read(make([]byte, 8))
		require.ErrorIs(t, err, io.ErrClosedPipe, "an upstream fault stays an upstream fault while the client is here")
		watch.Disconnect()
		_, err = wrapped.Body.Read(make([]byte, 8))
		require.ErrorIs(t, err, context.Canceled)
		_, isWrapped := resp.Body.(*compatWatchedBody)
		require.False(t, isWrapped, "the original response is not modified")
	})
}
