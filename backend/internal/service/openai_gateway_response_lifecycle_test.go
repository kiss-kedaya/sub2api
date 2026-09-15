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
		MaxLineSize: defaultMaxLineSize,
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
	case <-time.After(300 * time.Millisecond):
		t.Fatal("client cancellation left the upstream response body open")
	}
	select {
	case err := <-done:
		require.Error(t, err)
		require.False(t, errors.Is(err, context.DeadlineExceeded))
	case <-time.After(500 * time.Millisecond):
		t.Fatal("client cancellation left the stream handler blocked")
	}
}
