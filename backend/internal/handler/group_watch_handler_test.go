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

type stubGroupWatchHandlerRepo struct {
	subs   map[int64]map[int64]struct{}
	notifs []service.UserGroupWatchNotification
}

func (r *stubGroupWatchHandlerRepo) ListSubscribedGroupIDs(_ context.Context, userID int64) ([]int64, error) {
	out := make([]int64, 0, len(r.subs[userID]))
	for gid := range r.subs[userID] {
		out = append(out, gid)
	}
	return out, nil
}
func (r *stubGroupWatchHandlerRepo) Subscribe(_ context.Context, userID, groupID int64) error {
	if r.subs[userID] == nil {
		r.subs[userID] = map[int64]struct{}{}
	}
	r.subs[userID][groupID] = struct{}{}
	return nil
}
func (r *stubGroupWatchHandlerRepo) Unsubscribe(_ context.Context, userID, groupID int64) error {
	delete(r.subs[userID], groupID)
	return nil
}
func (r *stubGroupWatchHandlerRepo) ListWatchedGroupIDs(_ context.Context) ([]int64, error) {
	return nil, nil
}
func (r *stubGroupWatchHandlerRepo) ListSubscriberUsersByGroupID(_ context.Context, _ int64) ([]service.GroupWatchSubscriber, error) {
	return nil, nil
}
func (r *stubGroupWatchHandlerRepo) GetGroupWatchStates(_ context.Context, _ []int64) (map[int64]service.GroupMonitorWatchState, error) {
	return map[int64]service.GroupMonitorWatchState{}, nil
}
func (r *stubGroupWatchHandlerRepo) UpsertGroupWatchState(_ context.Context, _ service.GroupMonitorWatchState) error {
	return nil
}
func (r *stubGroupWatchHandlerRepo) InsertNotificationIfNew(_ context.Context, _ service.UserGroupWatchNotification) (bool, int64, error) {
	return true, 1, nil
}
func (r *stubGroupWatchHandlerRepo) SummaryNotifications(_ context.Context, userID int64) (int, int, error) {
	total, unread := 0, 0
	for _, n := range r.notifs {
		if n.UserID != userID {
			continue
		}
		total++
		if n.ReadAt == nil {
			unread++
		}
	}
	return total, unread, nil
}
func (r *stubGroupWatchHandlerRepo) ListNotifications(_ context.Context, userID int64, unreadOnly bool, _ int) ([]service.UserGroupWatchNotification, error) {
	out := make([]service.UserGroupWatchNotification, 0)
	for _, n := range r.notifs {
		if n.UserID != userID {
			continue
		}
		if unreadOnly && n.ReadAt != nil {
			continue
		}
		out = append(out, n)
	}
	return out, nil
}
func (r *stubGroupWatchHandlerRepo) MarkNotificationRead(_ context.Context, userID, id int64, readAt time.Time) (bool, error) {
	for i := range r.notifs {
		if r.notifs[i].ID == id && r.notifs[i].UserID == userID {
			t := readAt
			r.notifs[i].ReadAt = &t
			return true, nil
		}
	}
	return false, nil
}
func (r *stubGroupWatchHandlerRepo) MarkAllNotificationsRead(_ context.Context, userID int64, readAt time.Time) error {
	for i := range r.notifs {
		if r.notifs[i].UserID == userID && r.notifs[i].ReadAt == nil {
			t := readAt
			r.notifs[i].ReadAt = &t
		}
	}
	return nil
}
func (r *stubGroupWatchHandlerRepo) PruneOldNotifications(_ context.Context, _ time.Time, _ int) (int64, error) {
	return 0, nil
}

type stubGroupWatchAuthz struct {
	groups []service.Group
}

func (a *stubGroupWatchAuthz) GetAvailableGroups(_ context.Context, _ int64) ([]service.Group, error) {
	return a.groups, nil
}

func TestGroupWatchHandler_SubscribeListAndNotifications(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &stubGroupWatchHandlerRepo{
		subs: map[int64]map[int64]struct{}{},
		notifs: []service.UserGroupWatchNotification{
			{ID: 1, UserID: 7, GroupID: 12, EventType: service.GroupWatchEventStatusBlack, Title: "分组不可用", Content: "缓存率低于 30%"},
		},
	}
	svc := service.NewGroupWatchService(repo, nil, &stubGroupWatchAuthz{
		groups: []service.Group{{ID: 12, Name: "Claude 混合号池"}},
	}, nil, nil)
	h := NewGroupWatchHandler(svc)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7, Concurrency: 1})
		c.Next()
	})
	gw := router.Group("/api/v1/group-watch")
	gw.GET("/subscriptions", h.ListSubscriptions)
	gw.PUT("/subscriptions/:group_id", h.Subscribe)
	gw.DELETE("/subscriptions/:group_id", h.Unsubscribe)
	gw.GET("/notifications/summary", h.NotificationSummary)
	gw.GET("/notifications", h.ListNotifications)
	gw.POST("/notifications/:id/read", h.MarkNotificationRead)
	gw.POST("/notifications/read-all", h.MarkAllNotificationsRead)

	// Unauthorized group rejected with 403.
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/group-watch/subscriptions/999", nil)
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusForbidden, w.Code)

	// Authorized group succeeds.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/v1/group-watch/subscriptions/12", nil)
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	// List returns [12].
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/group-watch/subscriptions", nil)
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var listResp struct {
		Data struct {
			GroupIDs []int64 `json:"group_ids"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &listResp))
	require.Equal(t, []int64{12}, listResp.Data.GroupIDs)

	// Notification summary reports 1 unread.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/group-watch/notifications/summary", nil)
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var sumResp struct {
		Data struct {
			Total  int `json:"total"`
			Unread int `json:"unread"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &sumResp))
	require.Equal(t, 1, sumResp.Data.Total)
	require.Equal(t, 1, sumResp.Data.Unread)

	// Mark read clears unread count.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/group-watch/notifications/1/read", nil)
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/group-watch/notifications/summary", nil)
	router.ServeHTTP(w, req)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &sumResp))
	require.Equal(t, 0, sumResp.Data.Unread)

	// Unsubscribe removes group 12.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/group-watch/subscriptions/12", nil)
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
}
