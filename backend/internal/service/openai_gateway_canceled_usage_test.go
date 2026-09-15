//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestOpenAIGatewayService_Forward_CanceledStreamPreservesUsage(t *testing.T) {
	for _, ending := range []string{"eof", "read_error", "response_failed"} {
		t.Run(ending, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			body := []byte(`{"model":"gpt-5.4","instructions":"answer","input":"hello","stream":true}`)
			c, recorder := newOpenAIPartialUsageContext(t, body)
			c.Request = c.Request.WithContext(ctx)
			pr, pw := io.Pipe()
			defer func() { _ = pr.Close() }()
			defer func() { _ = pw.Close() }()
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       pr,
			}}
			svc := newOpenAIPartialUsageService(upstream)
			svc.cfg.Gateway.StreamDataIntervalTimeout = 2
			written := make(chan error, 1)
			go func() {
				defer func() { _ = pw.Close() }()
				_, err := io.WriteString(pw, partialOpenAIResponsesSSE(false)+"\n")
				written <- err
				if err != nil {
					return
				}
				<-ctx.Done()
				switch ending {
				case "read_error":
					_ = pw.CloseWithError(io.ErrUnexpectedEOF)
				case "response_failed":
					_, _ = io.WriteString(pw, "data: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_partial\",\"error\":{\"message\":\"An error occurred while processing your request.\"},\"usage\":{\"input_tokens\":13,\"output_tokens\":4}}}\n\n")
				}
			}()
			type outcome struct {
				result *OpenAIForwardResult
				err    error
			}
			done := make(chan outcome, 1)
			go func() {
				result, err := svc.Forward(ctx, c, newOpenAIPartialUsageAccount(false), body)
				done <- outcome{result, err}
			}()
			select {
			case err := <-written:
				require.NoError(t, err)
			case <-time.After(2 * time.Second):
				t.Fatal("preamble was not read")
			}
			cancel()
			select {
			case got := <-done:
				require.Error(t, got.err)
				var failover *UpstreamFailoverError
				require.Len(t, upstream.requests, 1, "canceled requests must not retry")
				if errors.As(got.err, &failover) {
					t.Error("canceled client was classified as replayable failover")
				}
				require.NotNil(t, got.result, "already observed usage must reach billing when retry is canceled")
				require.True(t, got.result.ClientDisconnect)
				require.Equal(t, 13, got.result.Usage.InputTokens)
				require.Empty(t, recorder.Body.String(), "cancellation must not inject an error event")
				if ending == "response_failed" {
					require.Equal(t, 4, got.result.Usage.OutputTokens)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("Forward did not finish")
			}
		})
	}
}

func TestOpenAIStreamingResponse_GrokWrappedBodyClosesOnTerminalOrCancel(t *testing.T) {
	for _, cancelClient := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel_%t", cancelClient), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			upstreamClosed := make(chan struct{})
			allowTerminal := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(upstreamClosed)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n")
				w.(http.Flusher).Flush()
				select {
				case <-allowTerminal:
					_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_review\",\"usage\":{\"input_tokens\":13,\"output_tokens\":4,\"input_tokens_details\":{\"cached_tokens\":2}}}}\n\n")
					w.(http.Flusher).Flush()
				case <-r.Context().Done():
					return
				}
				<-r.Context().Done()
			}))
			defer server.Close()
			resp, err := server.Client().Get(server.URL)
			require.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()
			account := &Account{ID: 1, Platform: PlatformGrok, Type: AccountTypeOAuth}
			_, mapping, err := patchGrokResponsesBodyWithClientTools([]byte(`{"input":"hello","tools":[{"type":"custom","name":"apply_patch"}]}`), "grok-4.6")
			require.NoError(t, err)
			resp.Body = newGrokResponsesBillingPingFilterBody(resp.Body, account, defaultMaxLineSize)
			resp.Body = newGrokResponsesClientToolStreamBody(resp.Body, mapping, defaultMaxLineSize)
			recorder := newOpenAIResponseFlushRecorder()
			c := newGrokTTFTContext(recorder)
			c.Request = c.Request.WithContext(ctx)
			svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{StreamDataIntervalTimeout: 1}}, toolCorrector: NewCodexToolCorrector()}
			resultCh := make(chan *openaiStreamingResult, 1)
			errCh := make(chan error, 1)
			go func() {
				result, err := svc.handleStreamingResponse(ctx, resp, c, account, time.Now(), "grok", "grok")
				resultCh <- result
				errCh <- err
			}()
			waitOpenAIResponseFlushCount(t, recorder, 1)
			if cancelClient {
				cancel()
			} else {
				close(allowTerminal)
			}
			result, err := awaitGrokStreamResult(t, resultCh, errCh)
			if cancelClient {
				require.ErrorContains(t, err, "disconnect timeout")
			} else {
				require.NoError(t, err)
				require.Equal(t, 13, result.usage.InputTokens)
				require.Equal(t, 4, result.usage.OutputTokens)
				require.Equal(t, 2, result.usage.CacheReadInputTokens)
			}
			select {
			case <-upstreamClosed:
			case <-time.After(time.Second):
				t.Fatal("wrapper Close did not close the HTTP source")
			}
		})
	}
}
