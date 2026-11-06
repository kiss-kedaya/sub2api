//go:build unit

package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// A buffered body has no deadline while the client is attached, and is closed the
// moment the client leaves (no drain window).
func TestOpenAILifecycleReview_BufferedBodyClosesAtOnceOnCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		svc := &OpenAIGatewayService{cfg: &config.Config{}}
		_, tracked, pw := newLifecycleTrackingResponse()
		defer func() { _ = pw.Close() }()
		body := svc.openAIBufferedBodyWithCancel(ctx, tracked)
		defer func() { _ = body.Close() }()
		time.Sleep(10 * time.Minute)
		select {
		case <-tracked.closed:
			t.Fatal("an active request must not gain a body deadline")
		default:
		}
		cancel()
		synctest.Wait()
		select {
		case <-tracked.closed:
		default:
			t.Fatal("cancellation did not close the source at once")
		}
		require.NoError(t, body.Close())
	})
}

// A cancellation that lands after the header handoff does not cancel the handed-off
// response by itself (its handler owns it); closing the body does, even when Close
// races with the buffered-body watcher.
func TestOpenAILifecycleReview_CancelAfterHeaderHandoffLeavesTheBodyToItsOwner(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		for range 100 {
			ctx, cancel := context.WithCancel(context.Background())
			resp, tracked, pw := newLifecycleTrackingResponse()
			var upstreamCtx context.Context
			svc := &OpenAIGatewayService{
				cfg: &config.Config{},
				httpUpstream: headerCancelUpstreamFunc(func(req *http.Request) (*http.Response, error) {
					upstreamCtx = req.Context()
					return resp, nil
				}),
			}
			got, err := svc.doOpenAIUpstreamWithHeaderCancel(ctx, httptest.NewRequest(http.MethodPost, "/v1/responses", nil), "", &Account{ID: 1})
			require.NoError(t, err)
			cancel()
			synctest.Wait()
			require.NoError(t, upstreamCtx.Err(), "a cancellation after the handoff must not cancel the response by itself")
			got.Body = svc.openAIBufferedBodyWithCancel(ctx, got.Body)
			synctest.Wait()
			require.ErrorIs(t, upstreamCtx.Err(), context.Canceled)
			select {
			case <-tracked.closed:
			default:
				t.Fatal("the buffered-body watcher did not release the upstream source")
			}
			closeDone := make(chan error, 1)
			go func() { closeDone <- got.Body.Close() }()
			require.NoError(t, got.Body.Close())
			require.NoError(t, <-closeDone)
			require.NoError(t, pw.Close())
		}
	})
}
