//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type lifecycleFailedClientWriter struct {
	gin.ResponseWriter
	failed chan struct{}
	once   sync.Once
}

func (w *lifecycleFailedClientWriter) Write([]byte) (int, error) {
	w.once.Do(func() { close(w.failed) })
	return 0, io.ErrClosedPipe
}

func TestOpenAILifecycleReview_DownstreamWriteFailureDrainsUsage(t *testing.T) {
	for _, firstWrite := range []string{"keepalive", "first_output"} {
		t.Run(firstWrite, func(t *testing.T) {
			body := []byte(`{"model":"gpt-5.4","input":"hello","stream":true}`)
			c, recorder := newOpenAIPartialUsageContext(t, body)
			writer := &lifecycleFailedClientWriter{ResponseWriter: c.Writer, failed: make(chan struct{})}
			c.Writer = writer
			resp, tracked, pw := newLifecycleTrackingResponse()
			upstream := &httpUpstreamRecorder{resp: resp}
			svc := newOpenAIPartialUsageService(upstream)
			svc.cfg.Gateway.StreamDataIntervalTimeout = 3
			if firstWrite == "keepalive" {
				svc.cfg.Gateway.StreamKeepaliveInterval = 1
			}
			producerDone := make(chan struct{})
			go func() {
				defer close(producerDone)
				defer func() { _ = pw.Close() }()
				if _, err := io.WriteString(pw, partialOpenAIResponsesSSE(firstWrite == "first_output")+"\n"); err != nil {
					return
				}
				select {
				case <-writer.failed:
				case <-tracked.closed:
					return
				}
				_, _ = io.WriteString(pw, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_final\",\"usage\":{\"input_tokens\":13,\"output_tokens\":7,\"input_tokens_details\":{\"cached_tokens\":2}}}}\n\n")
			}()
			done := make(chan headerCancelForwardResult, 1)
			go func() {
				result, err := svc.Forward(context.Background(), c, newOpenAIPartialUsageAccount(false), body)
				done <- headerCancelForwardResult{result, err}
			}()
			t.Cleanup(func() {
				_ = tracked.Close()
				_ = pw.Close()
				<-producerDone
			})
			select {
			case got := <-done:
				var failover *UpstreamFailoverError
				require.False(t, errors.As(got.err, &failover), "downstream write failure must not replay an upstream request")
				require.NoError(t, got.err)
				require.NotNil(t, got.result, "terminal usage must survive a failed first client write")
				require.True(t, got.result.ClientDisconnect)
				require.Equal(t, 13, got.result.Usage.InputTokens)
				require.Equal(t, 7, got.result.Usage.OutputTokens)
				require.Equal(t, 2, got.result.Usage.CacheReadInputTokens)
				require.Len(t, upstream.requests, 1)
				require.Empty(t, recorder.Body.String())
			case <-time.After(5 * time.Second):
				_ = tracked.Close()
				<-done
				t.Fatal("downstream write failure left Forward blocked")
			}
		})
	}
}

func TestOpenAILifecycleReview_CanceledBufferedBodyAfterHeadersIsBounded(t *testing.T) {
	for _, errorResponse := range []bool{false, true} {
		name, requestBody := "nonstream", `{"model":"gpt-5.4","input":"hello","stream":false}`
		if errorResponse {
			name, requestBody = "http_error", `{"model":"gpt-5.4","input":"hello","stream":true}`
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			body := []byte(requestBody)
			c, _ := newOpenAIPartialUsageContext(t, body)
			c.Request = c.Request.WithContext(ctx)
			resp, tracked, pw := newLifecycleTrackingResponse()
			if errorResponse {
				resp.StatusCode = http.StatusServiceUnavailable
			}
			upstream := &httpUpstreamRecorder{resp: resp}
			svc := newOpenAIPartialUsageService(upstream)
			svc.cfg.Gateway.StreamDataIntervalTimeout = 1
			done := make(chan headerCancelForwardResult, 1)
			go func() {
				result, err := svc.Forward(context.WithoutCancel(ctx), c, newOpenAIPartialUsageAccount(false), body)
				done <- headerCancelForwardResult{result, err}
			}()
			defer func() { _ = tracked.Close(); _ = pw.Close() }()
			select {
			case <-tracked.readStarted:
			case <-time.After(2 * time.Second):
				_ = tracked.Close()
				cancel()
				<-done
				t.Fatal("Forward did not hand headers off to the body reader")
			}
			select {
			case got := <-done:
				t.Fatalf("active body read gained a deadline: %v", got.err)
			case <-time.After(1200 * time.Millisecond):
			}
			cancel()
			select {
			case <-tracked.closed:
				t.Fatal("body was closed before the configured drain window")
			case <-time.After(150 * time.Millisecond):
			}
			select {
			case got := <-done:
				require.ErrorIs(t, got.err, context.Canceled)
				var failover *UpstreamFailoverError
				require.False(t, errors.As(got.err, &failover))
				require.Len(t, upstream.requests, 1)
				select {
				case <-tracked.closed:
				default:
					t.Fatal("canceled body reader left the response open")
				}
			case <-time.After(1500 * time.Millisecond):
				_ = tracked.Close()
				<-done
				t.Fatal("client cancellation after headers left the buffered body read unbounded")
			}
		})
	}
}

func TestOpenAILifecycleReview_DetachedNonstreamCancellationSkipsInvalidFailover(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	body := []byte(`{"model":"gpt-5.4","input":"hello","stream":false}`)
	c, recorder := newOpenAIPartialUsageContext(t, body)
	c.Request = c.Request.WithContext(ctx)
	svc := &OpenAIGatewayService{
		cfg: &config.Config{},
		httpUpstream: headerCancelUpstreamFunc(func(*http.Request) (*http.Response, error) {
			cancel()
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("invalid response"))}, nil
		}),
	}
	result, err := svc.Forward(context.WithoutCancel(ctx), c, newOpenAIPartialUsageAccount(false), body)
	require.Nil(t, result)
	var failover *UpstreamFailoverError
	require.False(t, errors.As(err, &failover), "the original canceled request must prevent invalid-response failover")
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, recorder.Body.String())
}

func TestOpenAILifecycleReview_CanceledBufferedSuccessPreservesUsage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	body := []byte(`{"model":"gpt-5.4","input":"hello","stream":false}`)
	c, _ := newOpenAIPartialUsageContext(t, body)
	c.Request = c.Request.WithContext(ctx)
	resp, tracked, pw := newLifecycleTrackingResponse()
	defer func() { _ = tracked.Close(); _ = pw.Close() }()
	upstream := &httpUpstreamRecorder{resp: resp}
	svc := newOpenAIPartialUsageService(upstream)
	svc.cfg.Gateway.StreamDataIntervalTimeout = 1
	producerDone := make(chan struct{})
	go func() {
		defer close(producerDone)
		defer func() { _ = pw.Close() }()
		<-tracked.readStarted
		cancel()
		_, _ = io.WriteString(pw, `{"id":"resp_final","status":"completed","output":[],"usage":{"input_tokens":13,"output_tokens":7,"input_tokens_details":{"cached_tokens":2}}}`)
	}()
	result, err := svc.Forward(context.WithoutCancel(ctx), c, newOpenAIPartialUsageAccount(false), body)
	<-producerDone
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 13, result.Usage.InputTokens)
	require.Equal(t, 7, result.Usage.OutputTokens)
	require.Equal(t, 2, result.Usage.CacheReadInputTokens)
	require.Len(t, upstream.requests, 1)
}

func TestOpenAILifecycleReview_BufferedDrainDefaultAndCleanup(t *testing.T) {
	for _, seconds := range []int{0, 2} {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{StreamDataIntervalTimeout: seconds}}}
			_, tracked, pw := newLifecycleTrackingResponse()
			defer func() { _ = pw.Close() }()
			body := svc.openAIBufferedBodyWithCancel(ctx, tracked)
			defer func() { _ = body.Close() }()
			budget := 180 * time.Second
			if seconds > 0 {
				budget = time.Duration(seconds) * time.Second
			}
			time.Sleep(2 * budget)
			select {
			case <-tracked.closed:
				t.Fatal("active request must not gain a body deadline")
			default:
			}
			cancel()
			synctest.Wait()
			time.Sleep(budget - time.Nanosecond)
			select {
			case <-tracked.closed:
				t.Fatal("cancellation shortened the drain budget")
			default:
			}
			time.Sleep(time.Nanosecond)
			synctest.Wait()
			select {
			case <-tracked.closed:
			default:
				t.Fatal("cancellation did not close the source at the drain deadline")
			}
			require.NoError(t, body.Close())
		})
	}
}

func TestOpenAILifecycleReview_HeaderHandoffCancelAndBodyCloseRace(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		for range 100 {
			ctx, cancel := context.WithCancel(context.Background())
			resp, tracked, pw := newLifecycleTrackingResponse()
			var upstreamCtx context.Context
			cancelDone := make(chan struct{})
			svc := &OpenAIGatewayService{
				cfg: &config.Config{Gateway: config.GatewayConfig{StreamDataIntervalTimeout: 1}},
				httpUpstream: headerCancelUpstreamFunc(func(req *http.Request) (*http.Response, error) {
					upstreamCtx = req.Context()
					go func() { defer close(cancelDone); cancel() }()
					return resp, nil
				}),
			}
			got, err := svc.doOpenAIUpstreamWithHeaderCancel(ctx, httptest.NewRequest(http.MethodPost, "/v1/responses", nil), "", &Account{ID: 1})
			require.NoError(t, err)
			<-cancelDone
			time.Sleep(2 * time.Second)
			require.NoError(t, upstreamCtx.Err(), "header callback must not cancel a handed-off stream")
			got.Body = svc.openAIBufferedBodyWithCancel(ctx, got.Body)
			closeDone := make(chan error, 1)
			go func() { closeDone <- got.Body.Close() }()
			require.NoError(t, got.Body.Close())
			require.NoError(t, <-closeDone)
			require.ErrorIs(t, upstreamCtx.Err(), context.Canceled)
			require.NoError(t, pw.Close())
			synctest.Wait()
			select {
			case <-tracked.closed:
			default:
				t.Fatal("concurrent Close did not release the upstream source")
			}
		}
	})
}

func TestOpenAILifecycleReview_StageCopyDistinguishesClientAndSpoolErrors(t *testing.T) {
	for _, spool := range []bool{false, true} {
		stage := newOpenAIFirstOutputStage(128 * 1024)
		stage.memoryOnly = true
		if spool {
			file, err := os.CreateTemp(t.TempDir(), "lifecycle-spool-*")
			require.NoError(t, err)
			stage.tempFile, stage.tempPath = file, file.Name()
		}
		t.Cleanup(func() { _ = stage.Close() })
		_, err := stage.WriteString(strings.Repeat("x", 70*1024))
		require.NoError(t, err)
		writer := &lifecycleFailedClientWriter{failed: make(chan struct{})}
		err = stage.CommitTo(writer)
		require.ErrorIs(t, err, errOpenAIFirstOutputClientWrite)
		require.ErrorIs(t, err, io.ErrClosedPipe)
		if spool {
			require.NotNil(t, stage.tempFile)
			require.NoError(t, stage.tempFile.Close())
			err = stage.CommitTo(io.Discard)
			require.Error(t, err)
			require.False(t, errors.Is(err, errOpenAIFirstOutputClientWrite), "a local spool failure must retain its original classification")
		}
		_ = stage.Close()
	}
}
