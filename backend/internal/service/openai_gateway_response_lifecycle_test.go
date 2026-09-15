package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
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

	select {
	case <-body.closed:
		t.Fatal("client cancellation closed the upstream body before the drain window")
	case <-time.After(100 * time.Millisecond):
	}
	select {
	case err := <-done:
		require.Error(t, err)
		require.Contains(t, err.Error(), "stream usage incomplete after disconnect timeout")
		require.False(t, errors.Is(err, context.DeadlineExceeded))
	case <-time.After(1200 * time.Millisecond):
		t.Fatal("client cancellation left the stream handler blocked")
	}
	select {
	case <-body.closed:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("client cancellation did not release the upstream body after the drain window")
	}
}

func TestOpenAIStreamingResponse_ClientCancellationDrainsTerminalUsage(t *testing.T) {
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
	terminalWritten := make(chan error, 1)
	go func() {
		defer writer.Close()
		_, err := writer.Write([]byte("data: {\"type\":\"response.in_progress\",\"response\":{}}\n\n"))
		initialWritten <- err
		if err != nil {
			return
		}
		// The terminal frame must be sent after cancellation, beyond the old
		// 1.5-second cutoff, even when the read-interval setting is disabled.
		<-ctx.Done()
		timer := time.NewTimer(1700 * time.Millisecond)
		defer timer.Stop()
		<-timer.C
		_, err = writer.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":3,\"output_tokens\":5,\"input_tokens_details\":{\"cached_tokens\":1}}}}\n\n"))
		terminalWritten <- err
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
	cancel()

	select {
	case err := <-terminalWritten:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("terminal usage frame was not accepted during the drain window")
	}
	select {
	case outcome := <-done:
		require.NoError(t, outcome.err)
		require.NotNil(t, outcome.result)
		require.True(t, outcome.result.clientDisconnect)
		require.NotNil(t, outcome.result.usage)
		require.Equal(t, 3, outcome.result.usage.InputTokens)
		require.Equal(t, 5, outcome.result.usage.OutputTokens)
		require.Equal(t, 1, outcome.result.usage.CacheReadInputTokens)
	case <-time.After(2 * time.Second):
		t.Fatal("stream handler did not finish after terminal usage")
	}
}
