package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestLogGatewayForwardFailure_ExpectedUpstreamRejectionHasNoStack(t *testing.T) {
	gin.SetMode(gin.TestMode)
	core, observed := observer.New(zapcore.DebugLevel)
	log := zap.New(core, zap.AddStacktrace(zapcore.ErrorLevel))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	service.SetOpsUpstreamError(c, http.StatusBadRequest, "invalid   request\nbody", "")
	c.String(http.StatusBadRequest, "upstream rejected request")

	logGatewayForwardFailure(log, c, "gateway.forward_failed", errors.New(strings.Repeat("x", 700)), zap.Int64("account_id", 42))

	entries := observed.All()
	require.Len(t, entries, 1)
	require.Equal(t, zapcore.WarnLevel, entries[0].Level)
	require.Empty(t, entries[0].Stack)
	fields := entries[0].ContextMap()
	require.EqualValues(t, http.StatusBadRequest, fields["upstream_status"])
	require.Equal(t, "invalid request body", fields["error_summary"])
	require.EqualValues(t, 42, fields["account_id"])
	require.NotContains(t, fields, "error")
}

func TestLogGatewayForwardFailure_TypedRateLimitHasNoStack(t *testing.T) {
	core, observed := observer.New(zapcore.DebugLevel)
	log := zap.New(core, zap.AddStacktrace(zapcore.ErrorLevel))
	err := &service.UpstreamFailoverError{
		StatusCode:   http.StatusTooManyRequests,
		ResponseBody: []byte(`{"error":{"message":"slow down"}}`),
	}

	logGatewayForwardFailure(log, nil, "openai.forward_failed", err)

	entry := observed.All()[0]
	require.Equal(t, zapcore.WarnLevel, entry.Level)
	require.Empty(t, entry.Stack)
	require.EqualValues(t, http.StatusTooManyRequests, entry.ContextMap()["upstream_status"])
	require.Equal(t, "slow down", entry.ContextMap()["error_summary"])
}

func TestLogGatewayForwardFailure_UnexpectedFailureRetainsErrorStack(t *testing.T) {
	core, observed := observer.New(zapcore.DebugLevel)
	log := zap.New(core, zap.AddStacktrace(zapcore.ErrorLevel))
	err := errors.New("database corruption detected")

	logGatewayForwardFailure(log, nil, "gateway.forward_failed", err)

	entry := observed.All()[0]
	require.Equal(t, zapcore.ErrorLevel, entry.Level)
	require.NotEmpty(t, entry.Stack)
	require.Equal(t, err.Error(), entry.ContextMap()["error"])
}

func TestLogGatewayForwardFailureWithWarn_CommittedResponseRemainsWarn(t *testing.T) {
	core, observed := observer.New(zapcore.DebugLevel)
	log := zap.New(core, zap.AddStacktrace(zapcore.ErrorLevel))
	err := errors.New("stream already returned terminal response")

	logGatewayForwardFailureWithWarn(log, nil, "openai.forward_failed", err, true)

	entry := observed.All()[0]
	require.Equal(t, zapcore.WarnLevel, entry.Level)
	require.Empty(t, entry.Stack)
	require.Equal(t, err.Error(), entry.ContextMap()["error"])
}

func TestLogGatewayForwardFailure_DoesNotUseStaleRateLimitContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	core, observed := observer.New(zapcore.DebugLevel)
	log := zap.New(core, zap.AddStacktrace(zapcore.ErrorLevel))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	service.SetOpsUpstreamError(c, http.StatusTooManyRequests, "earlier attempt", "")
	c.String(http.StatusBadGateway, "final transport failure")

	logGatewayForwardFailure(log, c, "openai.forward_failed", errors.New("connection reset"))

	entry := observed.All()[0]
	require.Equal(t, zapcore.ErrorLevel, entry.Level)
	require.NotEmpty(t, entry.Stack)
}

func TestGatewayForwardFailureDetails_TruncatesSummary(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	service.SetOpsUpstreamError(c, http.StatusBadRequest, strings.Repeat("a", gatewayForwardErrorSummaryMaxBytes+100), "")
	c.String(http.StatusBadRequest, "upstream rejected request")

	status, summary := gatewayForwardFailureDetails(c, errors.New("fallback"))

	require.Equal(t, http.StatusBadRequest, status)
	require.Len(t, summary, gatewayForwardErrorSummaryMaxBytes)
}
