//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

// These cases cover a client that leaves before the upstream answered, and a
// non-stream answer that is one JSON body. The billing rule: a request that was
// fully written to the upstream is billed for its input even though no usage
// was ever reported; a request that never left the gateway is not billed.

var anthropicDisconnectRequestKinds = []anthropicDisconnectKind{
	anthropicKindNativeJSON, anthropicKindPassthroughJSON, anthropicKindBedrockJSON,
	anthropicKindNative, anthropicKindPassthrough, anthropicKindBedrock,
	anthropicKindChatStream, anthropicKindChatBuffered,
	anthropicKindResponsesStream, anthropicKindResponsesBuffered,
}

// anthropicPendingRespond is an upstream call that never answers. Like a real
// transport it fires the request-written hook once the request is on the wire
// (when markWritten is set) and fails with the context error when its request
// context ends. release lets a test that fails free the blocked goroutine.
func anthropicPendingRespond(markWritten bool, release <-chan struct{}) func(*http.Request) (*http.Response, error) {
	return func(req *http.Request) (*http.Response, error) {
		if markWritten {
			if trace := httptrace.ContextClientTrace(req.Context()); trace != nil && trace.WroteRequest != nil {
				trace.WroteRequest(httptrace.WroteRequestInfo{})
			}
		}
		select {
		case <-req.Context().Done():
			return nil, &url.Error{Op: "Post", URL: req.URL.String(), Err: req.Context().Err()}
		case <-release:
			return nil, errors.New("the test released the pending upstream call")
		}
	}
}

func startAnthropicPendingForward(t *testing.T, kind anthropicDisconnectKind, markWritten bool) (context.CancelFunc, *anthropicDisconnectUpstream, <-chan anthropicDisconnectOutcome) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	upstream := newAnthropicDisconnectUpstream(anthropicPendingRespond(markWritten, release))
	svc := newAnthropicDisconnectService(upstream, 0)
	done := startAnthropicDisconnectForward(t, ctx, kind, svc, newAnthropicDisconnectRecorder(-1))
	select {
	case <-upstream.called:
	case <-time.After(3 * time.Second):
		t.Fatal("the upstream request never arrived")
	}
	return cancel, upstream, done
}

// The client leaves while the upstream is still working on a request that was
// fully written to it. No usage can ever arrive, so the input is estimated from
// the request body, 95% of it priced as cache read.
func TestAnthropicDisconnect_ClientLeavesWhileRequestInFlightBillsEstimatedInput(t *testing.T) {
	for _, kind := range anthropicDisconnectRequestKinds {
		t.Run(string(kind), func(t *testing.T) {
			cancel, upstream, done := startAnthropicPendingForward(t, kind, true)
			cancel()

			out := awaitAnthropicDisconnect(t, done, "client left while the upstream was working")

			require.NotNil(t, out.result, "a dispatched request must reach billing even though no usage was reported")
			requireAnthropicClientGone(t, out.err)
			require.Equal(t, 1, upstream.callCount(), "a departed client must not trigger a retry")
			require.True(t, out.result.ClientDisconnect)
			require.Equal(t, kind.clientStream(), out.result.Stream)
			total := estimateRequestInputTokens(kind.estimateBody(upstream.lastBody()))
			require.Greater(t, total, 0)
			split := splitIncompleteInput(total)
			require.Equal(t, split.Fresh, out.result.Usage.InputTokens, "Anthropic InputTokens is the fresh input only")
			require.Equal(t, split.CacheRead, out.result.Usage.CacheReadInputTokens)
			require.True(t, out.result.Usage.CacheReadEstimated)
			require.Zero(t, out.result.Usage.OutputTokens)
			require.Greater(t, billAnthropicDisconnectResult(t, out.result).lastCmd.BalanceCost, 0.0)
		})
	}
}

// The client leaves before the request was written to the upstream. Nothing was
// sent, so nothing is billed, and the departed client does not fail over.
func TestAnthropicDisconnect_ClientLeavesBeforeTheRequestWasSentIsNotBilled(t *testing.T) {
	for _, kind := range anthropicDisconnectRequestKinds {
		t.Run(string(kind), func(t *testing.T) {
			cancel, upstream, done := startAnthropicPendingForward(t, kind, false)
			cancel()

			out := awaitAnthropicDisconnect(t, done, "client left before the request was sent")

			require.Nil(t, out.result, "a request that never left the gateway must not be billed")
			require.Equal(t, 1, upstream.callCount())
			requireAnthropicClientGone(t, out.err)
		})
	}
}

// anthropicBlockedBody is a 2xx body whose read blocks until the request context
// ends, like a real transport body when the client cancels mid-answer.
type anthropicBlockedBody struct {
	ctx     context.Context
	release <-chan struct{}
	started chan struct{}
	once    sync.Once
}

func (b *anthropicBlockedBody) Read([]byte) (int, error) {
	b.once.Do(func() { close(b.started) })
	select {
	case <-b.ctx.Done():
		return 0, b.ctx.Err()
	case <-b.release:
		return 0, io.ErrUnexpectedEOF
	}
}

func (b *anthropicBlockedBody) Close() error { return nil }

// The upstream accepted the request (2xx) and the client leaves while the one
// JSON body is still being produced. There is no final JSON to read usage from.
func TestAnthropicDisconnect_ClientLeavesWhileResponseBodyIsReadBillsEstimatedInput(t *testing.T) {
	for _, kind := range []anthropicDisconnectKind{anthropicKindNativeJSON, anthropicKindPassthroughJSON, anthropicKindBedrockJSON} {
		t.Run(string(kind), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			release := make(chan struct{})
			t.Cleanup(func() { close(release) })
			started := make(chan struct{})
			upstream := newAnthropicDisconnectUpstream(func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": {"application/json"}, "X-Request-Id": {"req_json_disconnect"}},
					Body:       &anthropicBlockedBody{ctx: req.Context(), release: release, started: started},
				}, nil
			})
			done := startAnthropicDisconnectForward(t, ctx, kind, newAnthropicDisconnectService(upstream, 0), newAnthropicDisconnectRecorder(-1))
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("the gateway never started reading the upstream body")
			}
			cancel()

			out := awaitAnthropicDisconnect(t, done, "client left while the answer was being produced")

			require.NotNil(t, out.result, "an accepted request must reach billing even though the final JSON never arrived")
			requireAnthropicClientGone(t, out.err)
			require.True(t, out.result.ClientDisconnect)
			require.False(t, out.result.Stream)
			require.Equal(t, "req_json_disconnect", out.result.RequestID)
			split := splitIncompleteInput(estimateRequestInputTokens(kind.estimateBody(upstream.lastBody())))
			require.Equal(t, split.Fresh, out.result.Usage.InputTokens)
			require.Equal(t, split.CacheRead, out.result.Usage.CacheReadInputTokens)
			require.True(t, out.result.Usage.CacheReadEstimated)
			require.Zero(t, out.result.Usage.OutputTokens)
		})
	}
}

// anthropicRealTransportUpstream sends the gateway's upstream request through a
// real net/http client to a local server, so these cases exercise the real
// transport: the request-written hook, cancellation and connection close.
type anthropicRealTransportUpstream struct {
	client *http.Client
	target *url.URL
	mu     sync.Mutex
	body   []byte
}

func (u *anthropicRealTransportUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	local := req.Clone(req.Context())
	local.URL.Scheme = u.target.Scheme
	local.URL.Host = u.target.Host
	local.Host = ""
	return u.client.Do(local)
}

func (u *anthropicRealTransportUpstream) DoWithTLS(req *http.Request, proxy string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, accountID, concurrency)
}

func (u *anthropicRealTransportUpstream) record(body []byte) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.body = body
}

func (u *anthropicRealTransportUpstream) received() []byte {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.body
}

// newAnthropicRealTransportUpstream serves handler; the handler receives the
// request body it read through record.
func newAnthropicRealTransportUpstream(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, record func([]byte))) *anthropicRealTransportUpstream {
	t.Helper()
	upstream := &anthropicRealTransportUpstream{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler(w, r, upstream.record)
	}))
	t.Cleanup(func() {
		server.CloseClientConnections()
		server.Close()
	})
	target, err := url.Parse(server.URL)
	require.NoError(t, err)
	upstream.client = server.Client()
	upstream.target = target
	return upstream
}

func requireAnthropicSignal(t *testing.T, signal <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(3 * time.Second):
		t.Fatal(what)
	}
}

// With a real transport: the client leaves while the upstream is still working on
// a request it fully received. The hook fires on the real wire write, the input is
// billed from the estimate, and the upstream connection is really closed.
func TestAnthropicDisconnect_RealTransportClientLeavesWhileUpstreamWorks(t *testing.T) {
	for _, kind := range anthropicDisconnectRequestKinds {
		t.Run(string(kind), func(t *testing.T) {
			arrived := make(chan struct{})
			serverSawDisconnect := make(chan struct{})
			upstream := newAnthropicRealTransportUpstream(t, func(w http.ResponseWriter, r *http.Request, record func([]byte)) {
				body, _ := io.ReadAll(r.Body)
				record(body)
				close(arrived)
				<-r.Context().Done()
				close(serverSawDisconnect)
			})
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			done := startAnthropicDisconnectForward(t, ctx, kind, newAnthropicDisconnectService(upstream, 0), newAnthropicDisconnectRecorder(-1))
			requireAnthropicSignal(t, arrived, "the upstream never received the request")
			cancel()

			out := awaitAnthropicDisconnect(t, done, "client left while the upstream was working")

			require.NotNil(t, out.result, "a request the upstream fully received must reach billing")
			requireAnthropicClientGone(t, out.err)
			requireAnthropicSignal(t, serverSawDisconnect, "the upstream connection was not closed")
			require.True(t, out.result.ClientDisconnect)
			split := splitIncompleteInput(estimateRequestInputTokens(kind.estimateBody(upstream.received())))
			require.Greater(t, split.Total, 0)
			require.Equal(t, split.Fresh, out.result.Usage.InputTokens)
			require.Equal(t, split.CacheRead, out.result.Usage.CacheReadInputTokens)
			require.True(t, out.result.Usage.CacheReadEstimated)
			require.Zero(t, out.result.Usage.OutputTokens)
		})
	}
}

// With a real transport: the client leaves mid-stream. The upstream connection is
// really closed (the server sees the disconnect) instead of being read to the end.
func TestAnthropicDisconnect_RealTransportClientLeavesMidStreamClosesTheConnection(t *testing.T) {
	for _, kind := range anthropicDisconnectClientStreamKinds {
		t.Run(string(kind), func(t *testing.T) {
			sent := make(chan struct{})
			serverSawDisconnect := make(chan struct{})
			upstream := newAnthropicRealTransportUpstream(t, func(w http.ResponseWriter, r *http.Request, record func([]byte)) {
				body, _ := io.ReadAll(r.Body)
				record(body)
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, anthropicDisconnectRealStartFrame()+anthropicDisconnectBlockStartFrame()+
					anthropicDisconnectDeltaFrame(anthropicDisconnectTextOne))
				w.(http.Flusher).Flush()
				close(sent)
				<-r.Context().Done()
				close(serverSawDisconnect)
			})
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			recorder := newAnthropicDisconnectRecorder(-1)
			done := startAnthropicDisconnectForward(t, ctx, kind, newAnthropicDisconnectService(upstream, 30), recorder)
			requireAnthropicSignal(t, sent, "the upstream never streamed")
			recorder.waitForText(t, anthropicDisconnectTextOne)
			cancel()

			out := awaitAnthropicDisconnect(t, done, "client left mid stream")

			requireAnthropicClientGone(t, out.err)
			requireAnthropicSignal(t, serverSawDisconnect, "the upstream connection was left open after the client left")
			require.NotNil(t, out.result)
			require.True(t, out.result.ClientDisconnect)
			require.Equal(t, anthropicDisconnectInput, out.result.Usage.InputTokens)
			require.Equal(t, estimateTokensForText(anthropicDisconnectTextOne), out.result.Usage.OutputTokens)
		})
	}
}
