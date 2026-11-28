package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type channelMonitorV2GroupAuthorizerStub struct {
	groups []service.Group
	err    error
	calls  []int64
}

func (s *channelMonitorV2GroupAuthorizerStub) GetAvailableGroups(_ context.Context, userID int64) ([]service.Group, error) {
	s.calls = append(s.calls, userID)
	return s.groups, s.err
}

func TestChannelMonitorV2QueryListSupportsRepeatedAndCommaValues(t *testing.T) {
	c, _ := gin.CreateTestContext(nil)
	c.Request = httptest.NewRequest("GET", "/?platform=openai,grok&platform=anthropic", nil)
	require.Equal(t, []string{"openai", "grok", "anthropic"}, queryList(c, "platform"))
}

func TestObserverCannotUseUserMonitorEndpointAsFullAdmin(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(string(middleware.ContextKeyUserRole), service.RoleAdmin)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 2})
	require.False(t, channelMonitorV2IsAdmin(c))
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
	require.True(t, channelMonitorV2IsAdmin(c))
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
	require.Equal(t, http.StatusUnauthorized, recorder.Code)
	require.False(t, filter.RestrictGroups)
}

func TestChannelMonitorV2ScopeFilterUsesAvailableGroupsForOrdinaryUser(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/channel-monitor-v2/snapshot", nil)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
	authorizer := &channelMonitorV2GroupAuthorizerStub{groups: []service.Group{{ID: 3}, {ID: 7}}}
	h := &ChannelMonitorV2Handler{apiKeyService: authorizer}
	filter := service.ChannelMonitorV2Filter{GroupIDs: []int64{7, 9}}

	require.True(t, h.scopeFilter(c, &filter, false))
	require.True(t, filter.RestrictGroups)
	require.Equal(t, []int64{3, 7}, filter.AllowedGroupIDs)
	require.Equal(t, []int64{42}, authorizer.calls)
}

func TestChannelMonitorV2ScopeFilterPreservesEmptyOrdinaryUserScope(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/channel-monitor-v2/snapshot", nil)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
	h := &ChannelMonitorV2Handler{apiKeyService: &channelMonitorV2GroupAuthorizerStub{}}
	filter := service.ChannelMonitorV2Filter{}

	require.True(t, h.scopeFilter(c, &filter, false))
	require.True(t, filter.RestrictGroups)
	require.Empty(t, filter.AllowedGroupIDs)
}

func TestChannelMonitorV2ScopeFilterLeavesAdminUnrestricted(t *testing.T) {
	authorizer := &channelMonitorV2GroupAuthorizerStub{}
	h := &ChannelMonitorV2Handler{apiKeyService: authorizer}
	filter := service.ChannelMonitorV2Filter{GroupIDs: []int64{9}}

	require.True(t, h.scopeFilter(nil, &filter, true))
	require.False(t, filter.RestrictGroups)
	require.Nil(t, filter.AllowedGroupIDs)
	require.Empty(t, authorizer.calls)
}

func TestChannelMonitorV2ScopeFailsClosedWithoutPanicOnTypedNilAPIKeyService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/channel-monitor-v2/snapshot", nil)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
	c.Set(string(middleware.ContextKeyUserRole), "user")
	filter := service.ChannelMonitorV2Filter{}
	h := NewChannelMonitorV2Handler(nil, nil)
	require.False(t, h.scopeFilter(c, &filter, false))
	require.Equal(t, http.StatusInternalServerError, recorder.Code)
}

type stubQualityCheckRepoForHandler struct {
	events []*service.GroupQualityEvent
}

func (s *stubQualityCheckRepoForHandler) UpsertSettings(context.Context, *service.GroupQualityCheckSettings) (*service.GroupQualityCheckSettings, error) {
	return nil, nil
}
func (s *stubQualityCheckRepoForHandler) GetSettings(context.Context, int64) (*service.GroupQualityCheckSettings, error) {
	return &service.GroupQualityCheckSettings{GroupID: 43, Enabled: true}, nil
}
func (s *stubQualityCheckRepoForHandler) ListAllSettings(context.Context) ([]*service.GroupQualityCheckSettings, error) {
	return nil, nil
}
func (s *stubQualityCheckRepoForHandler) ListRecentResults(context.Context, int64, time.Time, int) ([]*service.GroupQualityCheckResult, error) {
	return nil, nil
}
func (s *stubQualityCheckRepoForHandler) ListGroupBuckets(context.Context, []int64, time.Time, int64) ([]service.GroupQualityBucketRow, error) {
	return nil, nil
}
func (s *stubQualityCheckRepoForHandler) ListGroupEvents(context.Context, int64, int) ([]*service.GroupQualityEvent, error) {
	return s.events, nil
}
func (s *stubQualityCheckRepoForHandler) GetGroupEventArtwork(context.Context, int64, int64) (string, error) {
	return "", service.ErrGroupQualityEventNotFound
}

func TestChannelMonitorV2QualityEventsOmitsAccountIDAndSanitizesErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	qualityRepo := &stubQualityCheckRepoForHandler{
		events: []*service.GroupQualityEvent{
			{
				ID:           101,
				GroupID:      43,
				AccountID:    777,
				ModelID:      "claude-sonnet-5",
				Status:       "degraded",
				ErrorMessage: "upstream https://10.0.0.9:8443/v1/messages failed (account_id=777)",
				CreatedAt:    time.Date(2026, 11, 28, 8, 0, 0, 0, time.UTC),
			},
			{
				ID:           102,
				GroupID:      43,
				AccountID:    888,
				ModelID:      "claude-sonnet-5",
				Status:       "degraded",
				ErrorMessage: "feet do not plausibly contact crank pedals",
				CreatedAt:    time.Date(2026, 11, 28, 7, 55, 0, 0, time.UTC),
			},
			{
				ID:           103,
				GroupID:      43,
				AccountID:    999,
				ModelID:      "claude-sonnet-5",
				Status:       "success",
				ErrorMessage: "should never leak on success",
				CreatedAt:    time.Date(2026, 11, 28, 7, 50, 0, 0, time.UTC),
			},
		},
	}
	v2Svc := service.NewChannelMonitorV2Service(nil)
	v2Svc.SetGroupQualityCheckService(service.NewGroupQualityCheckService(qualityRepo, nil))

	h := &ChannelMonitorV2Handler{
		service:       v2Svc,
		apiKeyService: &channelMonitorV2GroupAuthorizerStub{groups: []service.Group{{ID: 43, Name: "public"}}},
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/channel-monitor-v2/quality-events?group_id=43", nil)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
	c.Set(string(middleware.ContextKeyUserRole), "user")

	h.QualityEvents(c)
	require.Equal(t, http.StatusOK, recorder.Code)

	body := recorder.Body.String()
	require.NotContains(t, body, "account_id")
	require.NotContains(t, body, "777")
	require.NotContains(t, body, "10.0.0.9")
	require.NotContains(t, body, "should never leak")
	require.Contains(t, body, "feet do not plausibly contact crank pedals")

	var envelope struct {
		Data struct {
			GroupID int64 `json:"group_id"`
			Events  []struct {
				ID           int64  `json:"id"`
				ErrorMessage string `json:"error_message"`
			} `json:"events"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	require.Len(t, envelope.Data.Events, 3)
	require.Empty(t, envelope.Data.Events[0].ErrorMessage)
	require.Equal(t, "feet do not plausibly contact crank pedals", envelope.Data.Events[1].ErrorMessage)
	require.Empty(t, envelope.Data.Events[2].ErrorMessage)
}
