package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGatewayForwardSSEErrorStatusBeforeAndAfterOutput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, payload string
		status        int
	}{
		{"relay_eof", `{"type":"error","error":{"type":"relay_stream_error","message":"stream relay failure: scanner_error: unexpected EOF"}}`, 502},
		{"permission", `{"type":"error","error":{"type":"permission_error","message":"Request forbidden"}}`, 403},
		{"authentication", `{"type":"error","error":{"type":"authentication_error","message":"Authentication failed"}}`, 401},
		{"rate_limit", `{"type":"error","error":{"type":"rate_limit_error","message":"Rate limited"}}`, 429},
		{"overloaded", `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`, 529},
		{"api", `{"type":"error","error":{"type":"api_error","message":"Internal server error"}}`, 500},
		{"explicit_forbidden", `{"type":"error","error":{"type":"relay_stream_error","status_code":403,"message":"Request denied"}}`, 403},
		{"explicit_timeout", `{"type":"error","status_code":504,"error":{"type":"relay_stream_error","message":"Gateway timeout"}}`, 504},
	} {
		for _, output := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/output=%t", tc.name, output), func(t *testing.T) {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
				request := []byte(`{"model":"claude-3-5-sonnet-latest","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
				parsed, err := ParseGatewayRequest(NewRequestBodyRef(request), PlatformAnthropic)
				require.NoError(t, err)
				wire := ""
				if output {
					wire = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_test\",\"usage\":{\"input_tokens\":11}}}\n\n" +
						"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n"
				}
				wire += "event: error\ndata: " + tc.payload + "\n\n"
				upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
					Body:       io.NopCloser(strings.NewReader(wire)),
				}}
				svc := newForwardPartialUsageServiceForTest(upstream)
				svc.rateLimitService = NewRateLimitService(&gatewayForwardErrorPolicyRepoStub{}, nil, svc.cfg, nil, nil)
				_, err = svc.Forward(context.Background(), c, newAnthropicOAuthAccountForPartialUsageTest(), parsed)
				var failure *UpstreamFailoverError
				require.ErrorAs(t, err, &failure)
				require.Equal(t, tc.status, failure.StatusCode)
				require.Equal(t, tc.status, failure.ClientStatusCode)
				require.JSONEq(t, tc.payload, string(failure.ResponseBody))
				raw, ok := c.Get(OpsUpstreamErrorsKey)
				require.True(t, ok)
				events, ok := raw.([]*OpsUpstreamErrorEvent)
				require.True(t, ok)
				require.Len(t, events, 1)
				require.Equal(t, tc.status, events[0].UpstreamStatusCode)
				require.Equal(t, "stream_error", events[0].Kind)
				if output {
					require.Contains(t, rec.Body.String(), `"text":"hi"`)
				}
			})
		}
	}
}

func TestAnthropicStreamErrorSemanticsExplicitStatusValidation(t *testing.T) {
	for _, tc := range []struct {
		field  string
		status int
	}{
		{`"status_code":"403"`, 403},
		{`"status":520`, 520},
		{`"status_code":200`, 502},
		{`"status_code":600`, 502},
		{`"status_code":403.5`, 502},
		{`"status_code":true`, 502},
		{`"status_code":"403junk"`, 502},
	} {
		t.Run(tc.field, func(t *testing.T) {
			status, _, _ := anthropicStreamErrorSemantics(`{"type":"error","error":{"type":"relay_stream_error"},` + tc.field + `}`)
			require.Equal(t, tc.status, status)
		})
	}
}
