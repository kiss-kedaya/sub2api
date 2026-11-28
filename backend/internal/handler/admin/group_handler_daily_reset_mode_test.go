package admin

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGroupHandler_DailyResetModeBindingAndPassThrough(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newStubAdminService()
	h := NewGroupHandler(svc, nil, nil)
	r := gin.New()
	r.POST("/groups", h.Create)
	r.PUT("/groups/:id", h.Update)

	// Create with anchored_24h passes through to service.
	req := httptest.NewRequest(http.MethodPost, "/groups", bytes.NewBufferString(
		`{"name":"Sub24h","platform":"anthropic","rate_multiplier":1,"subscription_type":"subscription","daily_reset_mode":"anchored_24h","daily_limit_usd":100}`,
	))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	require.Equal(t, http.StatusOK, res.Code, res.Body.String())
	require.Len(t, svc.createdGroups, 1)
	require.Equal(t, service.DailyResetModeAnchored24h, svc.createdGroups[0].DailyResetMode)

	// Create with invalid mode is rejected with 400.
	req = httptest.NewRequest(http.MethodPost, "/groups", bytes.NewBufferString(
		`{"name":"BadMode","platform":"anthropic","rate_multiplier":1,"subscription_type":"subscription","daily_reset_mode":"hourly"}`,
	))
	req.Header.Set("Content-Type", "application/json")
	res = httptest.NewRecorder()
	r.ServeHTTP(res, req)
	require.Equal(t, http.StatusBadRequest, res.Code)

	// Update with calendar passes through as non-nil pointer.
	req = httptest.NewRequest(http.MethodPut, "/groups/1", bytes.NewBufferString(
		`{"daily_reset_mode":"calendar"}`,
	))
	req.Header.Set("Content-Type", "application/json")
	res = httptest.NewRecorder()
	r.ServeHTTP(res, req)
	require.Equal(t, http.StatusOK, res.Code, res.Body.String())
	require.Len(t, svc.updatedGroups, 1)
	require.NotNil(t, svc.updatedGroups[0].DailyResetMode)
	require.Equal(t, service.DailyResetModeCalendar, *svc.updatedGroups[0].DailyResetMode)

	// Update with invalid mode is rejected with 400.
	req = httptest.NewRequest(http.MethodPut, "/groups/1", bytes.NewBufferString(
		`{"daily_reset_mode":"invalid"}`,
	))
	req.Header.Set("Content-Type", "application/json")
	res = httptest.NewRecorder()
	r.ServeHTTP(res, req)
	require.Equal(t, http.StatusBadRequest, res.Code)
}
