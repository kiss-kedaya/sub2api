//go:build unit

package service

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newGrokClientGoneAccount() *Account {
	return &Account{
		ID: 813, Name: "grok-client-gone", Platform: PlatformGrok,
		Type: AccountTypeAPIKey, Concurrency: 1,
		Credentials: map[string]any{"api_key": "xai-test"},
		Status:      StatusActive, Schedulable: true,
	}
}

// Grok shares the policy: a client that leaves after the request was dispatched
// cancels the upstream at once and is billed for its estimated input.
func TestOpenAIGatewayService_GrokClientLeavesAfterDispatchIsBilledForInput(t *testing.T) {
	for _, stream := range []bool{false, true} {
		name := "sync"
		if stream {
			name = "stream"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			body := []byte(fmt.Sprintf(`{"model":"grok-4.1","input":"Write a very long story about a fox who learns to code.","stream":%t}`, stream))
			c, _ := newOpenAIPartialUsageContext(t, body)
			c.Request = c.Request.WithContext(ctx)
			arrived, closed := make(chan struct{}), make(chan struct{})
			svc := newOpenAIPartialUsageService(nil)
			svc.httpUpstream = headerCancelUpstreamFunc(func(req *http.Request) (*http.Response, error) {
				close(arrived)
				<-req.Context().Done()
				close(closed)
				return nil, req.Context().Err()
			})
			done := make(chan headerCancelForwardResult, 1)
			go func() {
				result, err := svc.Forward(context.WithoutCancel(ctx), c, newGrokClientGoneAccount(), body)
				done <- headerCancelForwardResult{result, err}
			}()
			select {
			case <-arrived:
			case <-time.After(2 * time.Second):
				t.Fatal("the upstream request did not arrive")
			}
			canceledAt := time.Now()
			cancel()
			got := awaitClientGone(t, done, 2*time.Second)

			require.Less(t, time.Since(canceledAt), 800*time.Millisecond)
			requireClientGoneNotFailover(t, got.err)
			require.NotNil(t, got.result, "a dispatched Grok request must be billed for its input")
			require.True(t, got.result.ClientDisconnect)
			require.Equal(t, stream, got.result.Stream)
			require.Greater(t, got.result.Usage.InputTokens, 0)
			require.Zero(t, got.result.Usage.OutputTokens)
			require.Equal(t, incompleteCacheReadTokens(got.result.Usage.InputTokens), got.result.Usage.CacheReadInputTokens)
			require.True(t, got.result.Usage.CacheReadEstimated)
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("the upstream connection was not closed")
			}
		})
	}
}

// A Grok client that was already gone before the request left the gateway costs
// nothing and never reaches the upstream.
func TestOpenAIGatewayService_GrokClientGoneBeforeSendIsNotBilled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	body := []byte(`{"model":"grok-4.1","input":"hello","stream":false}`)
	c, _ := newOpenAIPartialUsageContext(t, body)
	c.Request = c.Request.WithContext(ctx)
	var calls atomic.Int32
	svc := newOpenAIPartialUsageService(nil)
	svc.httpUpstream = headerCancelUpstreamFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, nil
	})

	result, err := svc.Forward(context.WithoutCancel(ctx), c, newGrokClientGoneAccount(), body)

	requireClientGoneNotFailover(t, err)
	require.Nil(t, result)
	require.Zero(t, calls.Load())
}
