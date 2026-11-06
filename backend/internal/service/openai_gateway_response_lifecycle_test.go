package service

import (
	"context"
	"errors"
	"io"
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

type lifecycleTrackingBody struct {
	reader      *io.PipeReader
	readStarted chan struct{}
	closed      chan struct{}
	closeOnce   sync.Once
}

func (b *lifecycleTrackingBody) Read(p []byte) (int, error) {
	select {
	case <-b.readStarted:
	default:
		close(b.readStarted)
	}
	return b.reader.Read(p)
}

func (b *lifecycleTrackingBody) Close() error {
	b.closeOnce.Do(func() { close(b.closed) })
	return b.reader.Close()
}

func newLifecycleTrackingResponse() (*http.Response, *lifecycleTrackingBody, *io.PipeWriter) {
	reader, writer := io.Pipe()
	body := &lifecycleTrackingBody{
		reader:      reader,
		readStarted: make(chan struct{}),
		closed:      make(chan struct{}),
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: body}, body, writer
}

func newLifecycleTestContext(ctx context.Context) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequestWithContext(ctx, http.MethodPost, "/v1/responses", nil)
	return c, recorder
}

func TestOpenAIStreamingResponse_IntervalTimeoutClosesBlockedUpstreamBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{
		StreamDataIntervalTimeout: 1,
		MaxLineSize:               defaultMaxLineSize,
	}}}
	resp, body, writer := newLifecycleTrackingResponse()
	defer func() { _ = writer.Close() }()
	c, _ := newLifecycleTestContext(context.Background())

	_, err := svc.handleStreamingResponse(c.Request.Context(), resp, c, &Account{ID: 1}, time.Now(), "model", "model")

	require.Error(t, err)
	require.Contains(t, err.Error(), "stream data interval timeout")
	select {
	case <-body.closed:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("stream interval timeout left the upstream response body open")
	}
}

func TestOpenAIStreamingResponse_ClientCancellationClosesBlockedUpstreamBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{
		StreamDataIntervalTimeout: 1,
		MaxLineSize:               defaultMaxLineSize,
	}}}
	resp, body, writer := newLifecycleTrackingResponse()
	defer func() { _ = writer.Close() }()
	c, _ := newLifecycleTestContext(ctx)

	done := make(chan error, 1)
	go func() {
		_, err := svc.handleStreamingResponse(ctx, resp, c, &Account{ID: 1}, time.Now(), "model", "model")
		done <- err
	}()

	select {
	case <-body.readStarted:
	case <-time.After(time.Second):
		t.Fatal("stream scanner did not start reading the upstream body")
	}
	cancel()

	// The upstream connection is closed the moment the client leaves.
	select {
	case <-body.closed:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("client cancellation did not close the upstream body at once")
	}
	select {
	case err := <-done:
		require.Error(t, err)
		require.Contains(t, err.Error(), "stream usage incomplete after disconnect")
		require.False(t, errors.Is(err, context.DeadlineExceeded))
	case <-time.After(900 * time.Millisecond):
		t.Fatal("client cancellation left the stream handler blocked")
	}
}

// A departed client is not waited for: a terminal frame that only shows up long
// after the cancellation is never read, the handler returns at once and the late
// write finds the upstream closed.
func TestOpenAIStreamingResponse_ClientCancellationDoesNotWaitForLateTerminalUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{
		MaxLineSize: defaultMaxLineSize,
	}}}
	resp, _, writer := newLifecycleTrackingResponse()
	defer func() { _ = writer.Close() }()
	c, _ := newLifecycleTestContext(ctx)

	initialWritten := make(chan error, 1)
	lateWrite := make(chan error, 1)
	go func() {
		defer func() { _ = writer.Close() }()
		_, err := writer.Write([]byte("data: {\"type\":\"response.in_progress\",\"response\":{}}\n\n"))
		initialWritten <- err
		if err != nil {
			return
		}
		<-ctx.Done()
		time.Sleep(700 * time.Millisecond)
		_, err = writer.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":3,\"output_tokens\":5}}}\n\n"))
		lateWrite <- err
	}()

	done := make(chan struct {
		result *openaiStreamingResult
		err    error
	}, 1)
	go func() {
		result, err := svc.handleStreamingResponse(ctx, resp, c, &Account{ID: 1}, time.Now(), "model", "model")
		done <- struct {
			result *openaiStreamingResult
			err    error
		}{result: result, err: err}
	}()

	select {
	case err := <-initialWritten:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("initial streaming frame was not consumed")
	}
	canceledAt := time.Now()
	cancel()

	select {
	case outcome := <-done:
		require.Less(t, time.Since(canceledAt), 800*time.Millisecond, "the handler waited for the departed client upstream")
		require.ErrorContains(t, outcome.err, "stream usage incomplete after disconnect")
		require.NotNil(t, outcome.result)
		require.True(t, outcome.result.clientDisconnect)
		require.True(t, outcome.result.incompleteStream)
		require.Zero(t, outcome.result.usage.OutputTokens, "the late terminal frame must not be read")
	case <-time.After(2 * time.Second):
		t.Fatal("stream handler kept waiting after the client left")
	}
	select {
	case err := <-lateWrite:
		require.Error(t, err, "the upstream was still open for the late terminal frame")
	case <-time.After(3 * time.Second):
		t.Fatal("late upstream write never returned")
	}
}

// Frames the scanner already delivered before the client left are honoured: a
// terminal frame among them keeps its real usage and nothing is estimated.
func TestOpenAIStreamingResponse_ClientCancellationKeepsTerminalUsageAlreadyDelivered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{
		MaxLineSize: defaultMaxLineSize,
	}}}
	// A body whose Close does nothing, so everything written is certainly read.
	frames := "data: {\"type\":\"response.in_progress\",\"response\":{}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":3,\"output_tokens\":5,\"input_tokens_details\":{\"cached_tokens\":1}}}}\n\n"
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(frames))}
	c, _ := newLifecycleTestContext(ctx)

	result, err := svc.handleStreamingResponse(ctx, resp, c, &Account{ID: 1}, time.Now(), "model", "model")

	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.clientDisconnect)
	require.False(t, result.incompleteStream)
	require.Zero(t, result.incompleteOutputTokens)
	require.Equal(t, 3, result.usage.InputTokens)
	require.Equal(t, 5, result.usage.OutputTokens)
	require.Equal(t, 1, result.usage.CacheReadInputTokens)
}
