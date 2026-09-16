//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGatewayHTMLRouteNotFoundLeavesResponseForFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range []string{"messages", "passthrough", "chat", "responses"} {
		for _, policy := range []string{"default", "retry404", "disable404"} {
			t.Run(mode+"/"+policy, func(t *testing.T) {
				body := []byte(`{"model":"claude-opus-4-8","stream":true,"max_tokens":32,"messages":[{"role":"user","content":"hi"}]}`)
				if mode == "responses" {
					body = []byte(`{"model":"claude-opus-4-8","stream":true,"max_output_tokens":32,"input":"hi"}`)
				}
				errorBody := "<html><head><title>404 Not Found</title></head><body><h1>404 Not Found</h1><hr><center>nginx</center></body></html>"
				upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{{
					StatusCode: http.StatusNotFound,
					Header:     http.Header{"Content-Type": {"text/html"}},
					Body:       io.NopCloser(strings.NewReader(errorBody)),
				}}}
				repo := &rateLimitAccountRepoStub{}
				svc := &GatewayService{
					cfg:                 &config.Config{},
					httpUpstream:        upstream,
					rateLimitService:    NewRateLimitService(repo, nil, &config.Config{}, nil, nil),
					tlsFPProfileService: &TLSFingerprintProfileService{},
				}
				account := newAnthropicAPIKeyAccountForTest()
				account.Extra["anthropic_passthrough"] = mode == "passthrough"
				account.Credentials["pool_mode"] = true
				account.Credentials["pool_mode_retry_status_codes"] = []any{404}
				if policy != "default" {
					account.Credentials["custom_error_codes_enabled"] = true
					account.Credentials["custom_error_codes"] = []any{float64(401)}
					if policy == "disable404" {
						account.Credentials["custom_error_codes"] = []any{float64(404)}
					}
				}
				require.Equal(t, policy != "retry404", account.ShouldHandleErrorCode(http.StatusNotFound))
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(string(body)))
				var result *ForwardResult
				var err error
				switch mode {
				case "chat":
					result, err = svc.ForwardAsChatCompletions(context.Background(), c, account, body, nil)
				case "responses":
					result, err = svc.ForwardAsResponses(context.Background(), c, account, body, nil)
				default:
					var parsed *ParsedRequest
					parsed, err = ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
					require.NoError(t, err)
					result, err = svc.Forward(context.Background(), c, account, parsed)
				}
				var failoverErr *UpstreamFailoverError
				require.ErrorAs(t, err, &failoverErr)
				require.Nil(t, result)
				require.Equal(t, http.StatusNotFound, failoverErr.StatusCode)
				require.False(t, failoverErr.RetryableOnSameAccount)
				require.True(t, failoverErr.ShouldRetryNextAccount())
				require.Equal(t, 1, upstream.callCount)
				require.False(t, c.Writer.Written())
				require.Empty(t, recorder.Body.String())
				require.Zero(t, repo.setErrorCalls)
			})
		}
	}
}

func TestOpenAIHTMLRouteNotFoundSkipsSameAccountRetry(t *testing.T) {
	body := []byte("<html><title>404 Not Found</title><h1>404 Not Found</h1><center>nginx</center></html>")
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	svc := &OpenAIGatewayService{}
	require.True(t, svc.shouldFailoverOpenAIUpstreamResponse(account, http.StatusNotFound, "", body))
	require.True(t, shouldFailoverOpenAIPassthroughResponse(account, http.StatusNotFound, body))
	err := newOpenAIUpstreamFailoverError(http.StatusNotFound, nil, body, "", true)
	require.False(t, err.RetryableOnSameAccount)
	require.False(t, err.RequestScopedTransient)
	require.True(t, err.ShouldRetryNextAccount())
}
