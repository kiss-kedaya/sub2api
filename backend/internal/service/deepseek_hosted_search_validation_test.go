//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func TestDeepSeekHostedSearchValidationReturnsOneCommittedJSON(t *testing.T) {
	for _, protocol := range []string{APIProtocolResponses, APIProtocolChatCompletions} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", protocol, stream), func(t *testing.T) {
				upstream := &hostedSearchUpstream{}
				svc, c, recorder, account, body := hostedSearchFixture(t, upstream, stream, "auto", false)
				account.Credentials["api_protocol"] = protocol
				body, err := sjson.SetBytes(body, "tools.0.unsupported_option", true)
				require.NoError(t, err)

				result, err := svc.Forward(c.Request.Context(), c, account, body)
				require.ErrorContains(t, err, "unsupported option unsupported_option")
				require.Nil(t, result, "a locally invalid request has no billable model or search result")
				require.Empty(t, upstream.modelRequests)
				require.Empty(t, upstream.searchRequests)
				require.Equal(t, http.StatusBadRequest, recorder.Code)
				require.Contains(t, recorder.Header().Get("Content-Type"), "application/json")
				require.True(t, gjson.ValidBytes(recorder.Body.Bytes()), "the HTTP error must be a single JSON document")
				require.Equal(t, "invalid_request_error", gjson.GetBytes(recorder.Body.Bytes(), "error.type").String())
				require.Equal(t, "tools", gjson.GetBytes(recorder.Body.Bytes(), "error.param").String())
				require.NotContains(t, recorder.Body.String(), "response.failed")
				require.True(t, IsResponseCommitted(c), "the handler must not append its generic fallback")
			})
		}
	}
}

func TestDeepSeekHostedSearchValidationAfterHeartbeatOnlyWritesTerminalSSE(t *testing.T) {
	upstream := &hostedSearchUpstream{}
	svc, c, recorder, account, body := hostedSearchFixture(t, upstream, true, "auto", false)
	body, err := sjson.SetBytes(body, "tools.0.unsupported_option", true)
	require.NoError(t, err)
	stop := StartOpenAIStreamSSEKeepalive(c, time.Hour)
	defer stop()
	_, err, handled := WriteOpenAIStreamSSEHeartbeat(c, []byte(": waiting\n\n"))
	require.NoError(t, err)
	require.True(t, handled)

	result, err := svc.Forward(c.Request.Context(), c, account, body)
	require.ErrorContains(t, err, "unsupported option unsupported_option")
	require.Nil(t, result)
	require.Empty(t, upstream.modelRequests)
	require.Empty(t, upstream.searchRequests)
	require.Equal(t, http.StatusOK, recorder.Code, "an already committed heartbeat fixes the wire status at 200")
	require.Contains(t, recorder.Header().Get("Content-Type"), "text/event-stream")
	require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: response.failed\n"))
	semantic := stripKeepaliveComments(recorder.Body.String())
	require.True(t, strings.HasPrefix(semantic, "event: response.failed\n"), "do not insert an unframed JSON error into SSE")
	frame := strings.TrimSpace(strings.TrimPrefix(semantic, "event: response.failed\ndata: "))
	require.True(t, gjson.Valid(frame))
	require.Equal(t, "invalid_request_error", gjson.Get(frame, "response.error.code").String())
	require.Contains(t, gjson.Get(frame, "response.error.message").String(), "unsupported option unsupported_option")
	require.True(t, IsResponseCommitted(c))
}

func TestDeepSeekHostedSearchValidationDoesNotWriteAfterCancellation(t *testing.T) {
	for _, canceledSource := range []string{"forward context", "inbound request"} {
		t.Run(canceledSource, func(t *testing.T) {
			upstream := &hostedSearchUpstream{}
			svc, c, recorder, account, body := hostedSearchFixture(t, upstream, true, "auto", false)
			body, err := sjson.SetBytes(body, "tools.0.unsupported_option", true)
			require.NoError(t, err)
			canceled, cancel := context.WithCancel(context.Background())
			cancel()
			ctx := context.Background()
			if canceledSource == "forward context" {
				ctx = canceled
			} else {
				c.Request = c.Request.WithContext(canceled)
			}

			result, err := svc.Forward(ctx, c, account, body)
			require.ErrorIs(t, err, context.Canceled)
			require.Nil(t, result)
			require.False(t, c.Writer.Written())
			require.Empty(t, recorder.Body.String())
			require.Empty(t, upstream.modelRequests)
			require.Empty(t, upstream.searchRequests)
		})
	}
}

type deepSeekHostedHTTPErrorUpstream struct {
	base       *hostedSearchUpstream
	failAt     int
	status     int
	errorBody  string
	modelCalls int
}

func (u *deepSeekHostedHTTPErrorUpstream) Do(req *http.Request, proxy string, accountID int64, concurrency int) (*http.Response, error) {
	if !strings.HasSuffix(req.URL.Path, "/tools/search") {
		u.modelCalls++
		if u.modelCalls == u.failAt {
			_, err := io.Copy(io.Discard, req.Body)
			if err != nil {
				return nil, err
			}
			return &http.Response{StatusCode: u.status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(u.errorBody))}, nil
		}
	}
	return u.base.Do(req, proxy, accountID, concurrency)
}

func (u *deepSeekHostedHTTPErrorUpstream) DoWithTLS(req *http.Request, proxy string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, accountID, concurrency)
}

func TestDeepSeekHostedSearchFirstHTTPErrorKeepsSingleJSON(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusForbidden} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/stream=%t", status, stream), func(t *testing.T) {
				base := &hostedSearchUpstream{}
				svc, c, recorder, account, body := hostedSearchFixture(t, base, stream, "auto", false)
				// cyber_policy is an existing raw-body rejection path at either HTTP
				// status. The hosted bridge must preserve its response byte for byte.
				upstream := &deepSeekHostedHTTPErrorUpstream{base: base, failAt: 1, status: status,
					errorBody: `{"error":{"code":"cyber_policy","type":"invalid_request_error","message":"This request is blocked by policy.","param":"input"}}`}
				svc.httpUpstream = upstream

				result, err := svc.Forward(c.Request.Context(), c, account, body)
				require.Error(t, err)
				var failover *UpstreamFailoverError
				require.False(t, errors.As(err, &failover), "a visible HTTP rejection cannot be replayed on another account")
				require.NotNil(t, result)
				require.Equal(t, OpenAIUsage{}, result.Usage)
				require.Zero(t, result.SearchCount)
				require.Equal(t, 1, upstream.modelCalls)
				require.Empty(t, base.searchRequests)
				require.Equal(t, status, recorder.Code)
				require.Equal(t, upstream.errorBody, recorder.Body.String())
				require.True(t, gjson.ValidBytes(recorder.Body.Bytes()))
				require.NotContains(t, recorder.Body.String(), "response.failed")
				require.True(t, IsResponseCommitted(c))
			})
		}
	}
}

func TestDeepSeekHostedSearchContinuationHTTPErrorKeepsOneTerminalSSE(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusForbidden} {
		t.Run(fmt.Sprintf("status=%d", status), func(t *testing.T) {
			base := &hostedSearchUpstream{modelResponses: [][]byte{hostedSearchResponse("resp_before_error", 10, 2, hostedSearchCall())},
				searchResponse: `{"results":[{"title":"Search result","url":"https://example.com/search","summary":"A public result."}]}`}
			svc, c, recorder, account, body := hostedSearchFixture(t, base, true, "auto", false)
			upstream := &deepSeekHostedHTTPErrorUpstream{base: base, failAt: 2, status: status,
				errorBody: `{"error":{"code":"cyber_policy","message":"private provider diagnostics"}}`}
			svc.httpUpstream = upstream

			result, err := svc.Forward(c.Request.Context(), c, account, body)
			require.Error(t, err)
			var failover *UpstreamFailoverError
			require.False(t, errors.As(err, &failover), "a completed search and visible stream cannot be replayed")
			require.NotNil(t, result)
			require.Equal(t, 10, result.Usage.InputTokens)
			require.Equal(t, 2, result.Usage.OutputTokens)
			require.Equal(t, 1, result.SearchCount, "retain the one successful search without replaying it")
			require.Equal(t, 2, upstream.modelCalls)
			require.Len(t, base.searchRequests, 1)
			require.Equal(t, http.StatusOK, recorder.Code)
			require.Contains(t, recorder.Header().Get("Content-Type"), "text/event-stream")
			require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: response.failed\n"))
			require.NotContains(t, recorder.Body.String(), "private provider diagnostics")
			require.NotContains(t, recorder.Body.String(), "event: response.completed\n")
			for _, frame := range strings.Split(strings.TrimSpace(recorder.Body.String()), "\n\n") {
				require.True(t, strings.HasPrefix(frame, "event: response."), "every visible payload must be a Responses SSE frame")
			}
			require.True(t, IsResponseCommitted(c))
		})
	}
}
