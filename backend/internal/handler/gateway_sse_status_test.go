package handler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGatewayFailoverExhaustedPreservesConfirmedSSEStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, status := range []int{400, 401, 403, 429, 500, 502, 504, 529} {
		for _, started := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/started=%t", status, started), func(t *testing.T) {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
				if started {
					_, _ = c.Writer.WriteString(partialMessageStartSSE)
					c.Writer.Flush()
				}
				(&GatewayHandler{}).handleFailoverExhausted(c, &service.UpstreamFailoverError{
					StatusCode: status, ClientStatusCode: status,
					ResponseBody: []byte(`{"type":"error","error":{"type":"relay_stream_error","message":"stream relay failure: scanner_error: unexpected EOF"}}`),
				}, service.PlatformAnthropic, started)
				require.Contains(t, rec.Body.String(), "unexpected EOF")
				require.Contains(t, rec.Body.String(), "relay_stream_error")
				if started {
					require.Equal(t, http.StatusOK, rec.Code)
					err, ok := service.GetOpsStreamError(c)
					require.True(t, ok)
					require.Equal(t, status, err.IntendedStatus)
					require.Equal(t, 1, strings.Count(rec.Body.String(), `"type":"error"`))
				} else {
					require.Equal(t, status, rec.Code)
				}
			})
		}
	}
}

func TestGatewayFailoverExhaustedDefaultCredentialsAndFinancialGuard(t *testing.T) {
	for _, tc := range []struct {
		name    string
		failure *service.UpstreamFailoverError
	}{
		{"http_credentials", &service.UpstreamFailoverError{StatusCode: 403, ResponseBody: []byte(`{"error":{"type":"permission_error","message":"Forbidden"}}`)}},
		{"financial", &service.UpstreamFailoverError{StatusCode: 403, ClientStatusCode: 403, ResponseBody: []byte(`{"error":{"code":"usage_limit_reached","message":"余额不足 sk-secret host.internal"}}`)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			(&GatewayHandler{}).handleFailoverExhausted(c, tc.failure, service.PlatformAnthropic, false)
			require.Equal(t, http.StatusBadGateway, rec.Code)
			require.NotContains(t, rec.Body.String(), "sk-secret")
			require.NotContains(t, rec.Body.String(), "host.internal")
		})
	}
}
