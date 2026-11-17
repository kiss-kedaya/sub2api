//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Override only the continuation so the first model turn and paid search still
// run through Forward before a stream=false request receives unexpected SSE.
type deepSeekHostedBufferedSSEUpstream struct {
	*hostedSearchUpstream
	continuation string
}

func (u *deepSeekHostedBufferedSSEUpstream) Do(req *http.Request, proxy string, id int64, concurrency int) (*http.Response, error) {
	resp, err := u.hostedSearchUpstream.Do(req, proxy, id, concurrency)
	if err == nil && strings.HasSuffix(req.URL.Path, "/responses") && len(u.modelRequests) == 2 {
		if closeErr := resp.Body.Close(); closeErr != nil {
			return nil, closeErr
		}
		resp.Header.Set("Content-Type", "text/event-stream")
		resp.Body = io.NopCloser(strings.NewReader(u.continuation))
	}
	return resp, err
}

func (u *deepSeekHostedBufferedSSEUpstream) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, id, concurrency)
}

func TestDeepSeekHostedBufferedSSEContinuationBilling(t *testing.T) {
	failed := "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_synthetic_failure\",\"status\":\"failed\",\"output\":[],\"usage\":{\"input_tokens\":20,\"output_tokens\":3},\"error\":{\"code\":\"invalid_request\",\"message\":\"unknown parameter synthetic\"}}}\n\n"
	bareError := "event: error\ndata: {\"type\":\"error\",\"error\":{\"code\":\"invalid_request\",\"message\":\"unknown parameter synthetic\"}}\n\n"
	truncated := "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_synthetic_partial\",\"status\":\"in_progress\",\"output\":[]}}\n\nevent: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"partial answer\"}\n\n"
	answer := hostedSearchResponse("resp_synthetic_answer", 20, 3, hostedSearchAnswer())
	for _, tc := range []struct {
		name         string
		continuation string
		free         bool
		wantErr      bool
	}{
		{name: "response_failed", continuation: failed, free: true, wantErr: true},
		{name: "error", continuation: bareError, free: true, wantErr: true},
		{name: "EOF_without_error", continuation: truncated, wantErr: true},
		{name: "error_then_completed_answer", continuation: bareError + hostedSearchSSE(answer)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := &hostedSearchUpstream{modelResponses: [][]byte{
				hostedSearchResponse("resp_synthetic_search", 10, 2, hostedSearchCall()), answer,
			}, searchResponse: `{"results":[{"title":"Synthetic source","url":"https://example.invalid/source"}]}`}
			svc, c, recorder, account, body := hostedSearchFixture(t, base, false, "auto", false)
			account.Credentials["base_url"] = "https://example.invalid"
			svc.httpUpstream = &deepSeekHostedBufferedSSEUpstream{hostedSearchUpstream: base, continuation: tc.continuation}
			result, err := svc.Forward(c.Request.Context(), c, account, body)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err, recorder.Body.String())
				require.Contains(t, recorder.Body.String(), "structured web results")
			}
			require.NotNil(t, result)
			require.Len(t, base.modelRequests, 2)
			require.Len(t, base.searchRequests, 1)
			for _, request := range base.modelRequests {
				require.False(t, gjson.GetBytes(request, "stream").Bool())
			}
			wantUsage := OpenAIUsage{InputTokens: 10, OutputTokens: 2}
			if !tc.wantErr {
				wantUsage = OpenAIUsage{InputTokens: 30, OutputTokens: 5}
			}
			require.Equal(t, wantUsage, result.Usage)
			require.Equal(t, 1, result.SearchCount)
			require.False(t, result.ClientDisconnect)
			requireDeepSeekHostedBilling(t, result, tc.free)
		})
	}
}

func TestDeepSeekHostedBufferedSSESourceObservation(t *testing.T) {
	answer := hostedSearchResponse("resp_synthetic_completed", 20, 3, hostedSearchAnswer())
	bareError := "event: error\ndata: {\"type\":\"error\",\"error\":{\"code\":\"invalid_request\",\"message\":\"unknown parameter synthetic\"}}\n\n"
	for _, passthrough := range []bool{false, true} {
		path := "normal"
		if passthrough {
			path = "passthrough"
		}
		for _, hosted := range []bool{false, true} {
			scope := "ordinary"
			if hosted {
				scope = "hosted"
			}
			for _, tc := range []struct {
				name      string
				body      string
				wantError bool
			}{
				{name: "error", body: bareError, wantError: true},
				{name: "response_failed", body: "event: response.failed\ndata: {\"type\":\"response.failed\",\"error\":{\"code\":\"invalid_request\",\"message\":\"unknown parameter synthetic\"}}\n\n", wantError: true},
				{name: "error_then_completed", body: bareError + hostedSearchSSE(answer)},
				{name: "error_then_done", body: bareError + strings.ReplaceAll(hostedSearchSSE(answer), "response.completed", "response.done")},
				{name: "EOF_without_error", body: "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_synthetic_partial\",\"status\":\"in_progress\"}}\n\n"},
			} {
				t.Run(path+"/"+scope+"/"+tc.name, func(t *testing.T) {
					c, recorder := newNonStreamingFailoverContext(t)
					svc := newNonStreamingFailoverService()
					state, round, _ := newDeepSeekHostedTerminalFixture(t, false)
					if hosted {
						ctx := context.WithValue(c.Request.Context(), deepSeekHostedRoundContextKey{}, round)
						c.Request = c.Request.WithContext(ctx)
						// Retain an earlier successful model turn and search, as the
						// outer hosted loop does when this parser returns nil usage.
						state.searchCount = 1
						state.addResult(&OpenAIForwardResult{Model: "deepseek-test", Usage: OpenAIUsage{InputTokens: 10, OutputTokens: 2}})
					}
					resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(tc.body))}
					var usage *OpenAIUsage
					var err error
					if passthrough {
						result, handleErr := svc.handleNonStreamingResponsePassthrough(c.Request.Context(), resp, c, newNonStreamingFailoverAccount(), "model", "model")
						err = handleErr
						if result != nil {
							usage = result.usage
						}
					} else {
						result, handleErr := svc.handleNonStreamingResponse(c.Request.Context(), resp, c, newNonStreamingFailoverAccount(), "model", "model")
						err = handleErr
						if result != nil {
							usage = result.usage
						}
					}
					require.Equal(t, hosted && tc.wantError, round.upstreamStreamError)
					require.False(t, round.upstreamHTTPError)
					if tc.wantError {
						require.Error(t, err)
						require.Nil(t, usage)
						require.Equal(t, http.StatusBadGateway, recorder.Code)
						require.Contains(t, recorder.Body.String(), "unknown parameter synthetic")
					} else {
						require.NoError(t, err)
						require.NotNil(t, usage)
						require.Equal(t, http.StatusOK, recorder.Code)
						if tc.name == "EOF_without_error" {
							require.Equal(t, tc.body, recorder.Body.String())
						} else {
							require.JSONEq(t, string(answer), recorder.Body.String())
							require.Equal(t, OpenAIUsage{InputTokens: 20, OutputTokens: 3}, *usage)
						}
					}
					if hosted {
						// The hosted loop must carry the raw observation even when
						// error mapping returned no result for the current round.
						state.nonBillableUpstreamError = round.upstreamHTTPError || round.upstreamStreamError
						result := state.finishResult()
						require.Equal(t, OpenAIUsage{InputTokens: 10, OutputTokens: 2}, result.Usage)
						require.Equal(t, 1, result.SearchCount)
						requireDeepSeekHostedBilling(t, result, tc.wantError)
					}
				})
			}
		}
	}
}
