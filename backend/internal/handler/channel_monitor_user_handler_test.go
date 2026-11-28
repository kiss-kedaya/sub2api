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

type channelMonitorUserRepoStub struct {
	service.ChannelMonitorRepository
	monitors []*service.ChannelMonitor
}

func (r *channelMonitorUserRepoStub) ListEnabled(context.Context) ([]*service.ChannelMonitor, error) {
	out := make([]*service.ChannelMonitor, 0, len(r.monitors))
	for _, m := range r.monitors {
		if m != nil && m.Enabled {
			cp := *m
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (r *channelMonitorUserRepoStub) GetByID(_ context.Context, id int64) (*service.ChannelMonitor, error) {
	for _, m := range r.monitors {
		if m != nil && m.ID == id {
			cp := *m
			return &cp, nil
		}
	}
	return nil, service.ErrChannelMonitorNotFound
}

func (r *channelMonitorUserRepoStub) ListLatestForMonitorIDs(_ context.Context, ids []int64) (map[int64][]*service.ChannelMonitorLatest, error) {
	out := make(map[int64][]*service.ChannelMonitorLatest, len(ids))
	for _, id := range ids {
		out[id] = []*service.ChannelMonitorLatest{{
			Model:     "gpt-5",
			Status:    "ok",
			CheckedAt: time.Date(2026, 11, 28, 8, 0, 0, 0, time.UTC),
		}}
	}
	return out, nil
}

func (r *channelMonitorUserRepoStub) ComputeAvailabilityForMonitors(_ context.Context, ids []int64, _ int) (map[int64][]*service.ChannelMonitorAvailability, error) {
	out := make(map[int64][]*service.ChannelMonitorAvailability, len(ids))
	for _, id := range ids {
		out[id] = []*service.ChannelMonitorAvailability{{
			Model:           "gpt-5",
			AvailabilityPct: 99.5,
		}}
	}
	return out, nil
}

func (r *channelMonitorUserRepoStub) ListRecentHistoryForMonitors(context.Context, []int64, map[int64]string, int) (map[int64][]*service.ChannelMonitorHistoryEntry, error) {
	return map[int64][]*service.ChannelMonitorHistoryEntry{}, nil
}

func (r *channelMonitorUserRepoStub) ListLatestPerModel(_ context.Context, _ int64) ([]*service.ChannelMonitorLatest, error) {
	return []*service.ChannelMonitorLatest{{
		Model:     "gpt-5",
		Status:    "ok",
		CheckedAt: time.Date(2026, 11, 28, 8, 0, 0, 0, time.UTC),
	}}, nil
}

func (r *channelMonitorUserRepoStub) ComputeAvailability(context.Context, int64, int) ([]*service.ChannelMonitorAvailability, error) {
	return []*service.ChannelMonitorAvailability{{
		Model:           "gpt-5",
		AvailabilityPct: 99.5,
	}}, nil
}

func newTestChannelMonitorUserHandler(monitors []*service.ChannelMonitor, authorizer channelMonitorUserGroupAuthorizer) *ChannelMonitorUserHandler {
	repo := &channelMonitorUserRepoStub{monitors: monitors}
	svc := service.NewChannelMonitorService(repo, nil)
	return &ChannelMonitorUserHandler{
		monitorService: svc,
		settingService: nil, // nil => V1 feature enabled for tests
		apiKeyService:  authorizer,
	}
}

func TestChannelMonitorUserHandlerFiltersByAvailableGroups(t *testing.T) {
	gin.SetMode(gin.TestMode)
	monitors := []*service.ChannelMonitor{
		{ID: 1, Name: "public-monitor", Provider: service.MonitorProviderOpenAI, PrimaryModel: "gpt-5", GroupName: "public-group", Enabled: true},
		{ID: 2, Name: "vip-monitor", Provider: service.MonitorProviderOpenAI, PrimaryModel: "gpt-5", GroupName: "vip-exclusive", Enabled: true},
		{ID: 3, Name: "ungrouped-monitor", Provider: service.MonitorProviderOpenAI, PrimaryModel: "gpt-5", GroupName: "", Enabled: true},
	}

	t.Run("ordinary user only sees allowed groups in List and GetStatus", func(t *testing.T) {
		authorizer := &channelMonitorV2GroupAuthorizerStub{
			groups: []service.Group{{ID: 10, Name: "public-group"}},
		}
		h := newTestChannelMonitorUserHandler(monitors, authorizer)

		// List returns only monitor 1.
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/channel-monitors", nil)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
		c.Set(string(middleware.ContextKeyUserRole), "user")
		h.List(c)
		require.Equal(t, http.StatusOK, rec.Code)

		var listResp struct {
			Data struct {
				Items []struct {
					ID        int64  `json:"id"`
					GroupName string `json:"group_name"`
				} `json:"items"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &listResp))
		require.Len(t, listResp.Data.Items, 1)
		require.Equal(t, int64(1), listResp.Data.Items[0].ID)
		require.Equal(t, "public-group", listResp.Data.Items[0].GroupName)

		// GetStatus succeeds for allowed group monitor 1.
		recOK := httptest.NewRecorder()
		cOK, _ := gin.CreateTestContext(recOK)
		cOK.Request = httptest.NewRequest(http.MethodGet, "/api/v1/channel-monitors/1/status", nil)
		cOK.Params = gin.Params{{Key: "id", Value: "1"}}
		cOK.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
		cOK.Set(string(middleware.ContextKeyUserRole), "user")
		h.GetStatus(cOK)
		require.Equal(t, http.StatusOK, recOK.Code)

		// GetStatus returns 404 (fail-closed) for hidden group monitor 2 and ungrouped monitor 3.
		for _, deniedID := range []string{"2", "3"} {
			recDenied := httptest.NewRecorder()
			cDenied, _ := gin.CreateTestContext(recDenied)
			cDenied.Request = httptest.NewRequest(http.MethodGet, "/api/v1/channel-monitors/"+deniedID+"/status", nil)
			cDenied.Params = gin.Params{{Key: "id", Value: deniedID}}
			cDenied.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
			cDenied.Set(string(middleware.ContextKeyUserRole), "user")
			h.GetStatus(cDenied)
			require.Equal(t, http.StatusNotFound, recDenied.Code, "monitor %s must be hidden", deniedID)
		}
	})

	t.Run("ordinary user with no available groups sees empty list and 404 detail", func(t *testing.T) {
		h := newTestChannelMonitorUserHandler(monitors, &channelMonitorV2GroupAuthorizerStub{})

		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/channel-monitors", nil)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 99})
		c.Set(string(middleware.ContextKeyUserRole), "user")
		h.List(c)
		require.Equal(t, http.StatusOK, rec.Code)
		require.Contains(t, rec.Body.String(), `"items":[]`)

		recDetail := httptest.NewRecorder()
		cDetail, _ := gin.CreateTestContext(recDetail)
		cDetail.Request = httptest.NewRequest(http.MethodGet, "/api/v1/channel-monitors/1/status", nil)
		cDetail.Params = gin.Params{{Key: "id", Value: "1"}}
		cDetail.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 99})
		cDetail.Set(string(middleware.ContextKeyUserRole), "user")
		h.GetStatus(cDetail)
		require.Equal(t, http.StatusNotFound, recDetail.Code)
	})

	t.Run("observer admin (non-primary admin) is still group-scoped", func(t *testing.T) {
		authorizer := &channelMonitorV2GroupAuthorizerStub{
			groups: []service.Group{{ID: 10, Name: "public-group"}},
		}
		h := newTestChannelMonitorUserHandler(monitors, authorizer)

		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/channel-monitors/2/status", nil)
		c.Params = gin.Params{{Key: "id", Value: "2"}}
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 2})
		c.Set(string(middleware.ContextKeyUserRole), service.RoleAdmin)
		h.GetStatus(c)
		require.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("primary admin sees all enabled monitors", func(t *testing.T) {
		h := newTestChannelMonitorUserHandler(monitors, &channelMonitorV2GroupAuthorizerStub{})

		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/channel-monitors", nil)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
		c.Set(string(middleware.ContextKeyUserRole), service.RoleAdmin)
		h.List(c)
		require.Equal(t, http.StatusOK, rec.Code)

		var listResp struct {
			Data struct {
				Items []struct {
					ID int64 `json:"id"`
				} `json:"items"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &listResp))
		require.Len(t, listResp.Data.Items, 3)
	})

	t.Run("missing auth context returns 401 and typed-nil apiKeyService fails closed with 500", func(t *testing.T) {
		repo := &channelMonitorUserRepoStub{monitors: monitors}
		svc := service.NewChannelMonitorService(repo, nil)
		h := NewChannelMonitorUserHandler(svc, nil, nil)

		rec401 := httptest.NewRecorder()
		c401, _ := gin.CreateTestContext(rec401)
		c401.Request = httptest.NewRequest(http.MethodGet, "/api/v1/channel-monitors", nil)
		h.List(c401)
		require.Equal(t, http.StatusUnauthorized, rec401.Code)

		rec500 := httptest.NewRecorder()
		c500, _ := gin.CreateTestContext(rec500)
		c500.Request = httptest.NewRequest(http.MethodGet, "/api/v1/channel-monitors", nil)
		c500.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
		c500.Set(string(middleware.ContextKeyUserRole), "user")
		h.List(c500)
		require.Equal(t, http.StatusInternalServerError, rec500.Code)
	})
}
