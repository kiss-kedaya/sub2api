//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

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

// A failed client write means the client is gone: the upstream is closed at once
// instead of being drained for a terminal frame. Usage the upstream already
// reported is kept; the call never replays on another account.
func TestOpenAILifecycleReview_DownstreamWriteFailureClosesUpstream(t *testing.T) {
	for _, firstWrite := range []string{"keepalive", "first_output"} {
		t.Run(firstWrite, func(t *testing.T) {
			body := []byte(`{"model":"gpt-5.4","input":"hello","stream":true}`)
			c, recorder := newOpenAIPartialUsageContext(t, body)
			writer := &lifecycleFailedClientWriter{ResponseWriter: c.Writer, failed: make(chan struct{})}
			c.Writer = writer
			resp, tracked, pw := newLifecycleTrackingResponse()
			upstream := &httpUpstreamRecorder{resp: resp}
			svc := newOpenAIPartialUsageService(upstream)
			// A long idle interval proves the return is driven by the failed write,
			// not by the upstream going quiet.
			svc.cfg.Gateway.StreamDataIntervalTimeout = 30
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
				<-tracked.closed
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
				require.ErrorContains(t, got.err, "stream usage incomplete")
				require.NotNil(t, got.result, "usage the upstream already reported must survive a failed client write")
				require.True(t, got.result.ClientDisconnect)
				require.Equal(t, 13, got.result.Usage.InputTokens)
				require.Zero(t, got.result.Usage.CacheReadInputTokens, "reported input is never re-split")
				if firstWrite == "first_output" {
					require.Equal(t, 4, got.result.Usage.OutputTokens)
				} else {
					require.Zero(t, got.result.Usage.OutputTokens)
				}
				select {
				case <-tracked.closed:
				default:
					t.Fatal("the upstream body was left open after the client write failed")
				}
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
