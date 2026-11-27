package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptrace"
	"sync/atomic"
)

// errOpenAIRequestNotSent marks a client that was already gone before the request
// was handed to the upstream transport: nothing was dispatched, so nothing is
// billed. It is joined with the client's context error, so errors.Is(err,
// context.Canceled) still holds.
var errOpenAIRequestNotSent = errors.New("openai upstream request not sent: client already gone")

// openAIClientErr returns ctx.Err() safely when ctx may be nil (e.g. unit tests
// invoking gateway methods without an attached gin HTTP request).
func openAIClientErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

// A client that leaves cancels the upstream request at once (owner decision
// 2026-11-06: disconnect closes the connection immediately, sync or stream). Once
// headers arrive, the response handlers own body cleanup: the SSE handler closes
// the body when the client leaves, buffered bodies are closed by
// openAIBufferedBodyWithCancel.
//
// A request that was dispatched and whose client then left returns a client
// context error; the caller bills the input estimate (see
// newClientGoneOpenAIForwardResult). A request that never left the gateway returns
// the same error joined with errOpenAIRequestNotSent.
func (s *OpenAIGatewayService) doOpenAIUpstreamWithHeaderCancel(clientCtx context.Context, request *http.Request, proxyURL string, account *Account) (*http.Response, error) {
	return s.doOpenAIRequestWithHeaderCancel(clientCtx, request, func(req *http.Request) (*http.Response, error) {
		return s.doOpenAIUpstream(req, proxyURL, account)
	})
}

// Independent OAuth resource servers share cancellation handoff without running
// Codex-specific token, ticket or cookie injection on their requests.
func (s *OpenAIGatewayService) doOpenAIRequestWithHeaderCancel(clientCtx context.Context, request *http.Request, send func(*http.Request) (*http.Response, error)) (*http.Response, error) {
	if clientCtx == nil || clientCtx.Done() == nil {
		return send(request)
	}
	if err := clientCtx.Err(); err != nil {
		return nil, errors.Join(err, errOpenAIRequestNotSent)
	}
	upstreamCtx, cancelUpstream := context.WithCancel(request.Context())
	var sawTransport atomic.Bool
	var wroteRequest atomic.Bool
	upstreamCtx = httptrace.WithClientTrace(upstreamCtx, &httptrace.ClientTrace{
		GetConn: func(string) {
			sawTransport.Store(true)
		},
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			if info.Err == nil {
				wroteRequest.Store(true)
			}
		},
	})
	watchDone := make(chan struct{})
	stopWatch := context.AfterFunc(clientCtx, func() {
		defer close(watchDone)
		cancelUpstream()
	})
	resp, err := send(request.WithContext(upstreamCtx))
	if !stopWatch() {
		// Join a started callback so the cancellation is settled before the handoff
		// decision below, and cannot cancel the body after handoff.
		<-watchDone
	}
	if err != nil {
		cancelUpstream()
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		// On a real net/http.Transport (GetConn fired), if cancellation interrupted
		// DNS/TCP/TLS dial before the request finished writing onto the wire, do not
		// bill the client for an undelivered request.
		if clientCtx.Err() != nil && sawTransport.Load() && !wroteRequest.Load() {
			return nil, errors.Join(err, errOpenAIRequestNotSent)
		}
		return nil, err
	}
	if upstreamCtx.Err() != nil && clientCtx.Err() != nil {
		cancelUpstream()
		rejectedByUpstream := resp != nil && resp.StatusCode >= http.StatusBadRequest
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		if rejectedByUpstream || (sawTransport.Load() && !wroteRequest.Load()) {
			return nil, errors.Join(clientCtx.Err(), errOpenAIRequestNotSent)
		}
		return nil, errors.Join(clientCtx.Err(), context.Cause(clientCtx))
	}
	if resp == nil || resp.Body == nil {
		cancelUpstream()
		return resp, nil
	}
	resp.Body = &openAIRequestContextReadCloser{ReadCloser: resp.Body, cleanup: cancelUpstream}
	return resp, nil
}

// Buffered success/error reads have no SSE loop after header handoff. A canceled
// client closes the source body at once, which unblocks ReadAll. Closing the
// returned body joins the watcher.
func (s *OpenAIGatewayService) openAIBufferedBodyWithCancel(clientCtx context.Context, body io.ReadCloser) io.ReadCloser {
	if clientCtx == nil || clientCtx.Done() == nil {
		return body
	}
	watchDone := make(chan struct{})
	stopWatch := context.AfterFunc(clientCtx, func() {
		defer close(watchDone)
		_ = body.Close()
	})
	return &openAIRequestContextReadCloser{ReadCloser: body, cleanup: func() {
		if !stopWatch() {
			<-watchDone
		}
	}}
}
