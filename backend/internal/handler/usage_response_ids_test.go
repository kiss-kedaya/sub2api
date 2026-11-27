//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestUsageRecordContextSnapshotsResponseIDsAcrossWorkerHandoff(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Header("X-Request-Id", "4276cee5-69e9-497a-9318-f99c84625bac")
	c.Header("X-Client-Request-Id", "564966d3-8995-44d3-bc78-b5730b19a922")

	parent := service.WithOpsGinContext(c.Request.Context(), c)
	parent = context.WithValue(parent, ctxkey.RequestID, "local-initial-id")
	parent = context.WithValue(parent, ctxkey.ClientRequestID, "564966d3-8995-44d3-bc78-b5730b19a922")
	parent = context.WithValue(parent, ctxkey.Group, &service.Group{
		ID:                77,
		Platform:          service.PlatformAnthropic,
		SaveResponseMsgID: true,
	})

	var capturedRespID, capturedClientID string
	var capturedSaveMsgID bool
	var capturedGinCtx *gin.Context
	wrapped, abandon := wrapUsageRecordTaskContext(parent, func(ctx context.Context) {
		capturedRespID, _ = ctx.Value(ctxkey.ResponseRequestID).(string)
		capturedClientID, _ = ctx.Value(ctxkey.ClientRequestID).(string)
		capturedSaveMsgID, _ = ctx.Value(ctxkey.SaveResponseMsgID).(bool)
		capturedGinCtx = service.OpsGinContextFrom(ctx)
	})
	defer abandon()

	wrapped(context.Background())

	require.Equal(t, "4276cee5-69e9-497a-9318-f99c84625bac", capturedRespID)
	require.Equal(t, "564966d3-8995-44d3-bc78-b5730b19a922", capturedClientID)
	require.True(t, capturedSaveMsgID)
	require.Nil(t, capturedGinCtx, "worker context must not retain gin.Context")
}

func TestUsageRecordContextSnapshotsEmptyResponseRequestIDWhenStripped(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	// No X-Request-Id written to c.Writer.Header() (e.g. force_remove stripped it).
	parent := service.WithOpsGinContext(c.Request.Context(), c)
	parent = context.WithValue(parent, ctxkey.RequestID, "local-id-not-sent")

	var capturedRespID string
	var hasRespID bool
	wrapped, abandon := wrapUsageRecordTaskContext(parent, func(ctx context.Context) {
		capturedRespID, hasRespID = ctx.Value(ctxkey.ResponseRequestID).(string)
	})
	defer abandon()

	wrapped(context.Background())

	require.True(t, hasRespID, "stripped X-Request-Id must still bind explicit empty key")
	require.Empty(t, capturedRespID)
}

func TestValidateUserUsageRequestIDSearch(t *testing.T) {
	now := time.Now().UTC()
	within48h := now.Add(-48 * time.Hour)
	weekAgo := now.Add(-7 * 24 * time.Hour)

	require.NoError(t, validateUserUsageRequestIDSearch("", nil, nil))
	require.NoError(t, validateUserUsageRequestIDSearch("=4276cee5-69e9-497a-9318-f99c84625bac", nil, nil))
	require.NoError(t, validateUserUsageRequestIDSearch("msg_011ChSws", &weekAgo, &now))
	require.NoError(t, validateUserUsageRequestIDSearch("ab", &within48h, &now))
	require.Error(t, validateUserUsageRequestIDSearch("=", nil, nil))
	require.Error(t, validateUserUsageRequestIDSearch("ab", &weekAgo, &now))
	require.Error(t, validateUserUsageRequestIDSearch("ab", nil, nil))
}

func TestUsageLogDTO_ExposesResponseIDsToUserAndAdminNotObserver(t *testing.T) {
	respID := "4276cee5-69e9-497a-9318-f99c84625bac"
	clientID := "564966d3-8995-44d3-bc78-b5730b19a922"
	msgID := "msg_011ChSwsWaLgTDX3Ay9jabZw"
	upstreamID := "secret-upstream-id"
	record := &service.UsageLog{
		ID:                101,
		UserID:            11,
		APIKeyID:          22,
		AccountID:         33,
		RequestID:         "client:" + clientID,
		ResponseRequestID: &respID,
		ClientRequestID:   &clientID,
		ResponseMsgID:     &msgID,
		UpstreamRequestID: &upstreamID,
		Model:             "claude-opus-5-5",
	}

	userDTO := dto.UsageLogFromService(record)
	require.NotNil(t, userDTO.ResponseRequestID)
	require.Equal(t, respID, *userDTO.ResponseRequestID)
	require.NotNil(t, userDTO.ClientRequestID)
	require.Equal(t, clientID, *userDTO.ClientRequestID)
	require.NotNil(t, userDTO.ResponseMsgID)
	require.Equal(t, msgID, *userDTO.ResponseMsgID)

	adminDTO := dto.UsageLogFromServiceAdmin(record)
	require.Equal(t, respID, *adminDTO.ResponseRequestID)
	require.Equal(t, clientID, *adminDTO.ClientRequestID)
	require.Equal(t, msgID, *adminDTO.ResponseMsgID)
	require.Equal(t, upstreamID, *adminDTO.UpstreamRequestID)

	observerDTO := dto.UsageLogForObserver(*adminDTO)
	raw, err := json.Marshal(observerDTO)
	require.NoError(t, err)
	for _, forbidden := range []string{"response_request_id", "client_request_id", "response_msg_id", "upstream_request_id", respID, clientID, msgID, upstreamID} {
		require.NotContains(t, string(raw), forbidden, "observer DTO must never expose %s", forbidden)
	}
}
