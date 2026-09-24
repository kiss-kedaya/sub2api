package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestUpstreamFinancialExhaustedProtocols(t *testing.T) {
	for _, path := range []string{"/v1/messages", "/v1/responses", "/v1/chat/completions", "/v1beta/models/gemini:generateContent", "/v1/images/generations", "/v1/videos"} {
		for _, started := range []bool{false, true} {
			t.Run(path+map[bool]string{true: "/flushed", false: "/json"}[started], func(t *testing.T) {
				gin.SetMode(gin.TestMode)
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest("POST", path, nil)
				if started {
					_, _ = c.Writer.WriteString(": ping\n\n")
					c.Writer.Flush()
				}
				raw := []byte("{\"error\":{\"code\":\"usage_limit_reached\",\"message\":\"余额不足 sk-secret host.internal 1.64\"}}")
				failure := &service.UpstreamFailoverError{StatusCode: 429, ResponseBody: raw}
				switch {
				case strings.Contains(path, "/messages"):
					(&GatewayHandler{}).handleFailoverExhausted(c, failure, service.PlatformAnthropic, started)
				case strings.Contains(path, "/v1beta/"):
					(&GatewayHandler{}).handleGeminiFailoverExhausted(c, failure)
				default:
					(&OpenAIGatewayHandler{}).handleFailoverExhausted(c, failure, started)
				}
				if started {
					require.Equal(t, 200, rec.Code)
				} else {
					require.Equal(t, 502, rec.Code)
				}
				require.Equal(t, 1, strings.Count(rec.Body.String(), service.UpstreamUnavailableMessage))
				require.NotContains(t, rec.Body.String(), "sk-secret")
				require.NotContains(t, rec.Body.String(), "host.internal")
			})
		}
	}
}

func TestUpstreamFinancialLocalBillingUnchanged(t *testing.T) {
	for _, local := range []error{service.ErrInsufficientBalance, service.ErrBalanceWithholdingFailed, service.ErrDailyLimitExceeded, service.ErrBillingServiceUnavailable} {
		gin.SetMode(gin.TestMode)
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
		status, code, message, _ := billingErrorDetails(local)
		(&GatewayHandler{}).errorResponseWithCode(c, status, "billing_error", code, message)
		require.Equal(t, status, rec.Code)
		require.NotEqual(t, http.StatusBadGateway, rec.Code)
		require.Contains(t, rec.Body.String(), message)
		require.Contains(t, rec.Body.String(), code)
	}
}
