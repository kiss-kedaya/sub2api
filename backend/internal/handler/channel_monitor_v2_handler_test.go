package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestChannelMonitorV2QueryListSupportsRepeatedAndCommaValues(t *testing.T) {
	c, _ := gin.CreateTestContext(nil)
	c.Request = httptest.NewRequest("GET", "/?platform=openai,grok&platform=anthropic", nil)
	require.Equal(t, []string{"openai", "grok", "anthropic"}, queryList(c, "platform"))
}

func TestChannelMonitorV2GroupByQueryDefaultsAndRejectsInvalid(t *testing.T) {
	groupBy, err := service.ParseChannelMonitorV2GroupBy("")
	require.NoError(t, err)
	require.Equal(t, service.ChannelMonitorV2GroupByPlatformGroup, groupBy)
	_, err = service.ParseChannelMonitorV2GroupBy("invalid")
	require.Error(t, err)
}

func TestChannelMonitorV2MatrixHandlerRejectsInvalidGroupBy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/channel-monitor-v2/matrix?group_by=invalid", nil)
	h := NewChannelMonitorV2Handler(service.NewChannelMonitorV2Service(nil), nil)
	h.Matrix(c)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestChannelMonitorV2ScopeKeepsAdminGlobalWhenAuthorizationDependencyMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set(string(middleware.ContextKeyUserRole), service.RoleAdmin)
	filter := service.ChannelMonitorV2Filter{GroupIDs: []int64{99}}
	h := NewChannelMonitorV2Handler(nil, nil)
	require.True(t, h.scopeFilter(c, &filter, true))
	require.Equal(t, []int64{99}, filter.GroupIDs)
	require.False(t, filter.RestrictGroups)
}

func TestChannelMonitorV2ScopeFailsClosedForOrdinaryViewerWithoutAuthorizationDependency(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set(string(middleware.ContextKeyUserRole), "user")
	filter := service.ChannelMonitorV2Filter{}
	h := NewChannelMonitorV2Handler(nil, nil)
	require.False(t, h.scopeFilter(c, &filter, false))
	require.Equal(t, http.StatusInternalServerError, recorder.Code)
	require.False(t, filter.RestrictGroups)
}
