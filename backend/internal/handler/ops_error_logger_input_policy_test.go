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

func TestOpsErrorLoggerMinimumInputPolicyAfterProtocolConversion(t *testing.T) {
	const minimumInputDetail = `{"error":{"code":"input_too_small","type":"invalid_request_error","message":"Minimum input is 2000 tokens"}}`
	for _, tc := range []struct {
		name, path, detail, clientCode, wantCategory string
		upstreamStatus                               int
		lastEvent                                    *service.OpsUpstreamErrorEvent
		streamFailure                                bool
	}{
		{name: "chat without client code", path: "/v1/chat/completions", detail: minimumInputDetail, upstreamStatus: 400, wantCategory: "context_limit"},
		{name: "messages without client code", path: "/v1/messages", detail: minimumInputDetail, upstreamStatus: 400, wantCategory: "context_limit"},
		{name: "other upstream code", detail: `{"error":{"code":"invalid_request"}}`, upstreamStatus: 400, wantCategory: "invalid_request"},
		{name: "message is not a code", detail: `{"error":{"message":"input_too_small"}}`, upstreamStatus: 400, wantCategory: "invalid_request"},
		{name: "malformed detail", detail: `{"error":{"code":"input_too_small"}`, upstreamStatus: 400, wantCategory: "invalid_request"},
		{name: "no detail", upstreamStatus: 400, wantCategory: "invalid_request"},
		{name: "no upstream status", detail: minimumInputDetail, wantCategory: "invalid_request"},
		{name: "upstream server failure", detail: minimumInputDetail, upstreamStatus: 502, wantCategory: "invalid_request"},
		{name: "different final client code", detail: minimumInputDetail, clientCode: "invalid_request", upstreamStatus: 400, wantCategory: "invalid_request"},
		{name: "stream failure cannot reuse previous detail", detail: minimumInputDetail, upstreamStatus: 400, streamFailure: true, wantCategory: "invalid_request"},
		{name: "earlier minimum input attempt", detail: minimumInputDetail, upstreamStatus: 400, wantCategory: "invalid_request",
			lastEvent: &service.OpsUpstreamErrorEvent{UpstreamStatusCode: 400, Message: "Invalid parameter", Detail: `{"error":{"code":"invalid_request"}}`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupOpsErrorLogTestQueue(t, 2)
			gin.SetMode(gin.TestMode)
			path := tc.path
			if path == "" {
				path = "/v1/chat/completions"
			}
			clientError := gin.H{"type": "invalid_request_error", "message": "Minimum input is 2000 tokens"}
			if tc.clientCode != "" {
				clientError["code"] = tc.clientCode
			}
			body, err := json.Marshal(gin.H{"error": clientError})
			require.NoError(t, err)
			wireBody, wireStatus, contentType := body, http.StatusBadRequest, "application/json"
			if tc.streamFailure {
				wireBody = []byte("event: error\ndata: " + string(body) + "\n\n")
				wireStatus, contentType = http.StatusOK, "text/event-stream"
			}
			ops := service.NewOpsService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
			router := gin.New()
			router.Use(OpsErrorLoggerMiddleware(ops))
			router.POST(path, func(c *gin.Context) {
				setOpsRequestContext(c, "gpt-5.5", true)
				service.SetOpsUpstreamError(c, tc.upstreamStatus, "Minimum input is 2000 tokens", tc.detail)
				if tc.lastEvent != nil {
					c.Set(service.OpsUpstreamErrorsKey, []*service.OpsUpstreamErrorEvent{tc.lastEvent})
				}
				c.Data(wireStatus, contentType, wireBody)
			})
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, nil))
			require.Equal(t, wireStatus, recorder.Code)
			require.Equal(t, string(wireBody), recorder.Body.String())
			require.Equal(t, int64(1), OpsErrorLogQueueLength())
			entry := (<-opsErrorLogQueue).entry
			if !tc.streamFailure {
				require.JSONEq(t, string(body), entry.ErrorBody)
			}
			upstreamStatus := 0
			if entry.UpstreamStatusCode != nil {
				upstreamStatus = *entry.UpstreamStatusCode
			}
			category := service.ClassifyChannelMonitorV2Error(service.ChannelMonitorV2ErrorInput{
				ErrorType: entry.ErrorType, ErrorOwner: entry.ErrorOwner, ErrorSource: entry.ErrorSource,
				StatusCode: entry.StatusCode, UpstreamStatusCode: upstreamStatus, Message: entry.ErrorMessage,
			})
			require.Equal(t, tc.wantCategory, category)
			if tc.wantCategory == "context_limit" {
				require.JSONEq(t, minimumInputDetail, *entry.UpstreamErrorDetail)
				require.Equal(t, "Minimum input is 2000 tokens", *entry.UpstreamErrorMessage)
			}
		})
	}
}
