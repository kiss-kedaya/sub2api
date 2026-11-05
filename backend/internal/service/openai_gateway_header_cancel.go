package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"
)

// Only bound the header wait after the original client cancels. Once headers
// arrive, the existing response handlers own draining and body cleanup.
//
// A streaming request is canceled upstream at once when the client leaves before
// any header arrived: nothing was produced yet, so nothing is billed. A buffered
// (non-stream) request keeps the drain budget because its usage only exists in the
// final body.
func (s *OpenAIGatewayService) doOpenAIUpstreamWithHeaderCancel(clientCtx context.Context, request *http.Request, proxyURL string, account *Account, streaming bool) (*http.Response, error) {
	return s.doOpenAIRequestWithHeaderCancel(clientCtx, request, streaming, func(req *http.Request) (*http.Response, error) {
		return s.doOpenAIUpstream(req, proxyURL, account)
	})
}

// Independent OAuth resource servers share cancellation handoff without running
// Codex-specific token, ticket or cookie injection on their requests.
func (s *OpenAIGatewayService) doOpenAIRequestWithHeaderCancel(clientCtx context.Context, request *http.Request, streaming bool, send func(*http.Request) (*http.Response, error)) (*http.Response, error) {
	if clientCtx == nil || clientCtx.Done() == nil {
		return send(request)
	}
	if err := clientCtx.Err(); err != nil {
		return nil, err
	}
	// A zero budget means cancel the upstream the moment the client leaves.
	budget := time.Duration(0)
	if !streaming {
		budget = openAIStreamClientDisconnectDrainTimeoutDefault
		if s.cfg != nil && s.cfg.Gateway.StreamDataIntervalTimeout > 0 {
			budget = time.Duration(s.cfg.Gateway.StreamDataIntervalTimeout) * time.Second
		}
	}
	upstreamCtx, cancelUpstream := context.WithCancel(request.Context())
	headerDone := make(chan struct{})
	watchDone := make(chan struct{})
	stopWatch := context.AfterFunc(clientCtx, func() {
		defer close(watchDone)
		if budget <= 0 {
			cancelUpstream()
			return
		}
		timer := time.NewTimer(budget)
		defer timer.Stop()
		select {
		case <-headerDone:
		case <-timer.C:
			cancelUpstream()
		}
	})
	resp, err := send(request.WithContext(upstreamCtx))
	close(headerDone)
	if !stopWatch() {
		// Join a started callback so it cannot cancel the body after handoff.
		<-watchDone
	}
	if err != nil {
		cancelUpstream()
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return nil, err
	}
	if upstreamCtx.Err() != nil && clientCtx.Err() != nil {
		cancelUpstream()
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
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

// Buffered success/error reads have no SSE drain loop after header handoff.
// Give only canceled clients the same existing drain budget, then close the
// source body to unblock ReadAll. Closing the returned body joins the watcher.
func (s *OpenAIGatewayService) openAIBufferedBodyWithCancel(clientCtx context.Context, body io.ReadCloser) io.ReadCloser {
	if clientCtx == nil || clientCtx.Done() == nil {
		return body
	}
	budget := openAIStreamClientDisconnectDrainTimeoutDefault
	if s.cfg != nil && s.cfg.Gateway.StreamDataIntervalTimeout > 0 {
		budget = time.Duration(s.cfg.Gateway.StreamDataIntervalTimeout) * time.Second
	}
	readDone := make(chan struct{})
	watchDone := make(chan struct{})
	stopWatch := context.AfterFunc(clientCtx, func() {
		defer close(watchDone)
		timer := time.NewTimer(budget)
		defer timer.Stop()
		select {
		case <-readDone:
		case <-timer.C:
			_ = body.Close()
		}
	})
	return &openAIRequestContextReadCloser{ReadCloser: body, cleanup: func() {
		close(readDone)
		if !stopWatch() {
			<-watchDone
		}
	}}
}
