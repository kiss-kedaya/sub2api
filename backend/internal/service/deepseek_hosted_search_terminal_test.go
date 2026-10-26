//go:build unit

package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newDeepSeekHostedTerminalFixture(t *testing.T, stream bool) (*deepSeekHostedSearchState, *deepSeekHostedRoundWriter, *httptest.ResponseRecorder) {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	state := &deepSeekHostedSearchState{c: c, stream: stream, started: time.Now(), model: "deepseek-test", header: make(http.Header)}
	writer := &deepSeekHostedRoundWriter{ResponseWriter: c.Writer, state: state, header: make(http.Header), status: 200, size: -1, indices: make(map[int]int), owned: make(map[string]*deepSeekHostedCall)}
	return state, writer, recorder
}

func TestDeepSeekHostedSearchRejectsUnknownAndMissingTerminal(t *testing.T) {
	for _, payload := range []string{
		`{"choices":[{"delta":{"content":"chat must not become an empty Responses event"}}]}`,
		`{"type":""}`, `{"type":"chat.completion.chunk"}`,
		`{"type":"response.completed"}`,
	} {
		t.Run(payload, func(t *testing.T) {
			_, writer, recorder := newDeepSeekHostedTerminalFixture(t, true)
			require.Error(t, writer.frame([]byte("data: "+payload)))
			require.Empty(t, recorder.Body.String())
		})
	}
	t.Run("SSE EOF", func(t *testing.T) {
		_, writer, recorder := newDeepSeekHostedTerminalFixture(t, true)
		writer.header.Set("Content-Type", "text/event-stream")
		_, err := writer.Write([]byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_terminal\"}}\n\n"))
		require.NoError(t, err)
		writer.finish()
		require.ErrorContains(t, writer.err, "without a terminal response")
		require.NotContains(t, recorder.Body.String(), "response.completed")
	})
	for _, status := range []string{"", "queued", "in_progress"} {
		t.Run("JSON status "+status, func(t *testing.T) {
			_, writer, _ := newDeepSeekHostedTerminalFixture(t, false)
			_, err := writer.Write([]byte(`{"id":"resp_terminal","status":"` + status + `","output":[]}`))
			require.NoError(t, err)
			writer.finish()
			require.ErrorContains(t, writer.err, "valid terminal status")
		})
	}
	t.Run("JSON completed", func(t *testing.T) {
		_, writer, _ := newDeepSeekHostedTerminalFixture(t, false)
		_, err := writer.Write([]byte(`{"id":"resp_terminal","status":"completed","output":[]}`))
		require.NoError(t, err)
		writer.finish()
		require.NoError(t, writer.err)
		require.Equal(t, "response.completed", writer.terminal)
	})
}

func TestDeepSeekHostedSearchPreservesFailureAndExcludesNonBillableUsage(t *testing.T) {
	state, writer, recorder := newDeepSeekHostedTerminalFixture(t, true)
	state.responseID = "resp_visible"
	writer.response = []byte(`{"id":"resp_internal","object":"response","created_at":1,"status":"failed","model":"deepseek-test","output":[],"error":{"code":"context_length_exceeded","message":"The input exceeds the context window."}}`)
	writer.terminal = "response.failed"
	state.addResult(&OpenAIForwardResult{Model: state.model, Usage: OpenAIUsage{InputTokens: 100, OutputTokens: 20, CacheReadInputTokens: 40}})
	state.addResult(&OpenAIForwardResult{Model: state.model, NonBillableUpstreamError: true, Usage: OpenAIUsage{InputTokens: 9999, OutputTokens: 999, CacheReadInputTokens: 999}})
	state.searchCount = 1
	result := state.finishResult()
	require.Equal(t, OpenAIUsage{InputTokens: 100, OutputTokens: 20, CacheReadInputTokens: 40}, result.Usage)
	require.Equal(t, 1, result.SearchCount)
	require.Zero(t, result.WebSearchCalls)
	require.False(t, result.NonBillableUpstreamError)
	require.NoError(t, state.writeFinal(writer))
	require.Contains(t, recorder.Body.String(), `"code":"context_length_exceeded"`)
	require.Contains(t, recorder.Body.String(), `"message":"The input exceeds the context window."`)
	require.Contains(t, recorder.Body.String(), `"id":"resp_visible"`)
	require.NotContains(t, recorder.Body.String(), "resp_internal")

	state, _, _ = newDeepSeekHostedTerminalFixture(t, false)
	state.addResult(&OpenAIForwardResult{NonBillableUpstreamError: true, Usage: OpenAIUsage{InputTokens: 12}})
	require.Equal(t, OpenAIUsage{}, state.finishResult().Usage)
	require.True(t, state.finishResult().NonBillableUpstreamError)
	state.searchCount = 1
	require.False(t, state.finishResult().NonBillableUpstreamError)
}

type deepSeekHostedTerminalFailWriter struct{ gin.ResponseWriter }

func (w *deepSeekHostedTerminalFailWriter) Write([]byte) (int, error) {
	return 0, errors.New("test downstream closed")
}

func TestDeepSeekHostedSearchMarksDownstreamAndCancellation(t *testing.T) {
	for _, stream := range []bool{true, false} {
		t.Run("write failure "+map[bool]string{true: "SSE", false: "JSON"}[stream], func(t *testing.T) {
			state, writer, _ := newDeepSeekHostedTerminalFixture(t, stream)
			state.c.Writer = &deepSeekHostedTerminalFailWriter{ResponseWriter: state.c.Writer}
			if stream {
				require.Error(t, state.event(map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_closed"}}))
			} else {
				writer.response = []byte(`{"id":"resp_closed","status":"completed","output":[]}`)
				require.Error(t, state.writeFinal(writer))
			}
			require.True(t, state.finishResult().ClientDisconnect)
		})
	}
	t.Run("already canceled request does not call upstream", func(t *testing.T) {
		price := 5.0
		state, _, recorder := newDeepSeekHostedTerminalFixture(t, true)
		state.c.Set("api_key", &APIKey{Group: &Group{Platform: PlatformDeepseek, SearchPricePer1k: &price}})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		state.c.Request = state.c.Request.WithContext(ctx)
		account := &Account{Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_protocol": APIProtocolResponses}}
		service := &OpenAIGatewayService{} // No HTTP upstream: cancellation must stop before it can be used.
		result, handled, err := service.forwardDeepSeekHostedSearch(ctx, state.c, account, []byte(`{"model":"deepseek-test","stream":true,"input":"search","tools":[{"type":"web_search"}]}`))
		require.True(t, handled)
		require.Error(t, err)
		require.NotNil(t, result)
		require.True(t, result.ClientDisconnect)
		require.Empty(t, strings.TrimSpace(recorder.Body.String()))
	})
}
