package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpsErrorLoggerMinimumInputPolicyPreservesDiagnosticAndChannelHealth(t *testing.T) {
	for _, tc := range []struct {
		name, code, wantCategory string
		status                   int
	}{
		{"minimum input", "input_too_small", "context_limit", http.StatusBadRequest},
		{"other invalid request", "invalid_request", "invalid_request", http.StatusBadRequest},
		{"server failure", "input_too_small", "invalid_request", http.StatusBadGateway},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupOpsErrorLogTestQueue(t, 2)
			gin.SetMode(gin.TestMode)
			message := "This key does not accept requests with fewer than 2000 input tokens (judged by request body size)."
			body, err := json.Marshal(gin.H{"error": gin.H{"type": "invalid_request_error", "code": tc.code, "message": message}})
			require.NoError(t, err)
			ops := service.NewOpsService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
			router := gin.New()
			router.Use(OpsErrorLoggerMiddleware(ops))
			router.POST("/v1/responses", func(c *gin.Context) {
				setOpsRequestContext(c, "gpt-5.5", true)
				service.SetOpsUpstreamError(c, tc.status, message, string(body))
				c.Data(tc.status, "application/json", body)
			})
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
			require.Equal(t, tc.status, recorder.Code)
			require.JSONEq(t, string(body), recorder.Body.String())
			require.Equal(t, int64(1), OpsErrorLogQueueLength())
			entry := (<-opsErrorLogQueue).entry
			require.Equal(t, tc.status, entry.StatusCode)
			require.JSONEq(t, string(body), entry.ErrorBody)
			require.Equal(t, message, *entry.UpstreamErrorMessage)
			require.JSONEq(t, string(body), *entry.UpstreamErrorDetail)
			require.Equal(t, "invalid_request_error", entry.ErrorType)
			category := service.ClassifyChannelMonitorV2Error(service.ChannelMonitorV2ErrorInput{
				ErrorType: entry.ErrorType, ErrorOwner: entry.ErrorOwner, ErrorSource: entry.ErrorSource,
				StatusCode: entry.StatusCode, UpstreamStatusCode: *entry.UpstreamStatusCode, Message: entry.ErrorMessage,
			})
			require.Equal(t, tc.wantCategory, category)
			ignored := service.ChannelMonitorV2IgnoredCategorySet(service.ChannelMonitorV2Config{
				IgnoredErrorCategories: service.DefaultChannelMonitorV2IgnoredErrorCategories,
			})
			_, isIgnored := ignored[category]
			require.Equal(t, tc.wantCategory == "context_limit", isIgnored)
		})
	}
}
