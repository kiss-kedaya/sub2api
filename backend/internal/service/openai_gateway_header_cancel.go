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
func (s *OpenAIGatewayService) doOpenAIUpstreamWithHeaderCancel(clientCtx context.Context, request *http.Request, proxyURL string, account *Account) (*http.Response, error) {
	if clientCtx == nil || clientCtx.Done() == nil {
		return s.doOpenAIUpstream(request, proxyURL, account)
	}
	if err := clientCtx.Err(); err != nil {
		return nil, err
	}
	budget := openAIStreamClientDisconnectDrainTimeoutDefault
	if s.cfg != nil && s.cfg.Gateway.StreamDataIntervalTimeout > 0 {
		budget = time.Duration(s.cfg.Gateway.StreamDataIntervalTimeout) * time.Second
	}
	upstreamCtx, cancelUpstream := context.WithCancel(request.Context())
	headerDone := make(chan struct{})
	watchDone := make(chan struct{})
	stopWatch := context.AfterFunc(clientCtx, func() {
		defer close(watchDone)
		timer := time.NewTimer(budget)
		defer timer.Stop()
		select {
		case <-headerDone:
		case <-timer.C:
			cancelUpstream()
		}
	})
	resp, err := s.doOpenAIUpstream(request.WithContext(upstreamCtx), proxyURL, account)
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
