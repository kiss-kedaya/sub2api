//go:build unit

package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// delayedGrokSSEResponse emits the first event, waits for the test, then emits
// the terminal event. The wait after terminal proves the converter does not
// use EOF as the boundary for a completed stream.
func delayedGrokSSEResponse(t *testing.T, first, terminal string) (*http.Response, <-chan struct{}, func()) {
	t.Helper()
	firstFlushed := make(chan struct{})
	allowTerminal := make(chan struct{})
	var releaseOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = fmt.Fprint(w, first)
		flusher.Flush()
		close(firstFlushed)
		select {
		case <-allowTerminal:
		case <-r.Context().Done():
			return
		}
		_, _ = fmt.Fprint(w, terminal)
		flusher.Flush()
		<-r.Context().Done()
	}))
	client := server.Client()
	resp, err := client.Get(server.URL)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = resp.Body.Close()
		server.Close()
	})
	return resp, firstFlushed, func() {
		releaseOnce.Do(func() {
			close(allowTerminal)
		})
	}
}

func newGrokTTFTContext(recorder *openAIResponseFlushRecorder) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c
}

func TestGrokResponsesFlushesVisibleTextBeforeTerminalAndMeasuresVisibleText(t *testing.T) {
	first := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n"
	terminal := "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"
	resp, firstFlushed, release := delayedGrokSSEResponse(t, first, terminal)
	defer release()
	recorder := newOpenAIResponseFlushRecorder()
	c := newGrokTTFTContext(recorder)
	account := &Account{ID: 1, Platform: PlatformGrok, Type: AccountTypeOAuth}
	_, mapping, err := patchGrokResponsesBodyWithClientTools([]byte(`{"input":"hello","tools":[{"type":"custom","name":"apply_patch"}]}`), "grok-4.6")
	require.NoError(t, err)
	require.True(t, hasGrokResponsesClientToolMapping(mapping))
	resp.Body = newGrokResponsesBillingPingFilterBody(resp.Body, account, defaultMaxLineSize)
	resp.Body = newGrokResponsesClientToolStreamBody(resp.Body, mapping, defaultMaxLineSize)
	svc := &OpenAIGatewayService{cfg: &config.Config{}, toolCorrector: NewCodexToolCorrector()}
	start := time.Now()
	resultCh := make(chan *openaiStreamingResult, 1)
	errCh := make(chan error, 1)
	go func() {
		defer resp.Body.Close()
		result, err := svc.handleStreamingResponse(context.Background(), resp, c, account, start, "grok", "grok")
		resultCh <- result
		errCh <- err
	}()

	select {
	case <-firstFlushed:
	case <-time.After(time.Second):
		t.Fatal("upstream did not flush first event")
	}
	waitOpenAIResponseFlushCount(t, recorder, 1)
	body, flushes := recorder.snapshot()
	require.Contains(t, body, `response.output_text.delta`)
	require.Contains(t, flushes[0], `response.output_text.delta`)
	require.NotContains(t, flushes[0], `response.completed`)
	release()
	result, err := awaitGrokStreamResult(t, resultCh, errCh)
	require.NoError(t, err)
	require.NotNil(t, result.firstTokenMs)
	require.Less(t, time.Duration(*result.firstTokenMs)*time.Millisecond, 500*time.Millisecond)
}

func TestGrokChatResponsesBridgeDoesNotMeasureResponseCreatedAsFirstToken(t *testing.T) {
	first := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n"
	terminal := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"
	resp, firstFlushed, release := delayedGrokSSEResponse(t, first, terminal)
	defer release()
	recorder := newOpenAIResponseFlushRecorder()
	c := newGrokTTFTContext(recorder)
	svc := &OpenAIGatewayService{cfg: &config.Config{}}
	start := time.Now()
	resultCh := make(chan *OpenAIForwardResult, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := svc.handleChatStreamingResponse(resp, c, &Account{Platform: PlatformGrok}, "grok", "grok", "grok", start, 0)
		resultCh <- result
		errCh <- err
	}()
	<-firstFlushed
	time.Sleep(80 * time.Millisecond)
	release()
	result, err := awaitGrokForwardResult(t, resultCh, errCh)
	require.NoError(t, err)
	require.NotNil(t, result.FirstTokenMs)
	require.GreaterOrEqual(t, *result.FirstTokenMs, 60)
	body, _ := recorder.snapshot()
	require.Contains(t, body, "hello")
}

func TestGrokRawChatFlushesBeforeDoneAndDoesNotWaitForEOF(t *testing.T) {
	first := "data: {\"id\":\"chatcmpl_1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"}}]}\n\n"
	terminal := "data: {\"id\":\"chatcmpl_1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":3,\"prompt_tokens_details\":{\"cached_tokens\":7}}}\n\ndata: [DONE]\n\n"
	resp, firstFlushed, release := delayedGrokSSEResponse(t, first, terminal)
	defer release()
	recorder := newOpenAIResponseFlushRecorder()
	c := newGrokTTFTContext(recorder)
	svc := &OpenAIGatewayService{cfg: &config.Config{}}
	resultCh := make(chan *OpenAIForwardResult, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := svc.streamRawChatCompletions(c, resp, &Account{Platform: PlatformGrok}, "grok", "grok", "grok", nil, nil, time.Now(), 0)
		resultCh <- result
		errCh <- err
	}()
	<-firstFlushed
	waitOpenAIResponseFlushCount(t, recorder, 1)
	_, flushes := recorder.snapshot()
	require.Contains(t, flushes[0], "hello")
	release()
	result, err := awaitGrokForwardResult(t, resultCh, errCh)
	require.NoError(t, err)
	require.NotNil(t, result.FirstTokenMs)
	require.Equal(t, 12, result.Usage.InputTokens)
	require.Equal(t, 3, result.Usage.OutputTokens)
	require.Equal(t, 7, result.Usage.CacheReadInputTokens)
	body, _ := recorder.snapshot()
	require.True(t, strings.HasSuffix(body, "data: [DONE]\n\n"))
}

func TestGrokMessagesResponsesConversionMeasuresVisibleTextAfterCreated(t *testing.T) {
	first := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n"
	terminal := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"
	resp, firstFlushed, release := delayedGrokSSEResponse(t, first, terminal)
	defer release()
	recorder := newOpenAIResponseFlushRecorder()
	c := newGrokTTFTContext(recorder)
	svc := &OpenAIGatewayService{cfg: &config.Config{}}
	resultCh := make(chan *OpenAIForwardResult, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := svc.handleAnthropicStreamingResponse(resp, c, &Account{Platform: PlatformGrok}, "grok", "grok", "grok", time.Now())
		resultCh <- result
		errCh <- err
	}()
	<-firstFlushed
	time.Sleep(80 * time.Millisecond)
	release()
	result, err := awaitGrokForwardResult(t, resultCh, errCh)
	require.NoError(t, err)
	require.NotNil(t, result.FirstTokenMs)
	require.GreaterOrEqual(t, *result.FirstTokenMs, 60)
	body, _ := recorder.snapshot()
	require.Contains(t, body, "hello")
}

func awaitGrokStreamResult(t *testing.T, resultCh <-chan *openaiStreamingResult, errCh <-chan error) (*openaiStreamingResult, error) {
	t.Helper()
	select {
	case result := <-resultCh:
		return result, <-errCh
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not finish")
		return nil, nil
	}
}

func awaitGrokForwardResult(t *testing.T, resultCh <-chan *OpenAIForwardResult, errCh <-chan error) (*OpenAIForwardResult, error) {
	t.Helper()
	select {
	case result := <-resultCh:
		return result, <-errCh
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not finish")
		return nil, nil
	}
}
