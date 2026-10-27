// Adapted from ranxi2001/sub2api @ 9620d315, licensed under LGPL-3.0-or-later.
package handler

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type trafficHandlerRepository struct {
	service.UsageLogRepository
	filters    []service.TrafficFilter
	keyUsers   []int64
	keyQueries []string
}

func (r *trafficHandlerRepository) RecordTraffic(context.Context, service.TrafficEvent) error {
	return nil
}
func (r *trafficHandlerRepository) Traffic(_ context.Context, f service.TrafficFilter) (*service.TrafficReport, error) {
	r.filters = append(r.filters, f)
	return &service.TrafficReport{}, nil
}

func TestTrafficUserEndpointAlwaysScopesAuthenticatedUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &trafficHandlerRepository{}
	h := &UsageHandler{usageService: service.NewUsageService(repo, nil, nil, nil)}
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7})
		c.Next()
	})
	r.GET("/usage/traffic", h.Traffic)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/usage/traffic?user_id=999&api_key_id=123&period=year&date=2024-02-29", nil))
	require.Equal(t, 200, w.Code)
	require.Len(t, repo.filters, 1)
	require.EqualValues(t, 7, repo.filters[0].UserID)
	require.EqualValues(t, 123, repo.filters[0].APIKeyID)
	for _, query := range []string{"period=invalid", "date=2026-02-30", "api_key_id=-1", "page=0", "page_size=101", "view=unknown", "cursor=bad"} {
		w = httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/usage/traffic?"+query, nil))
		require.Equal(t, 400, w.Code)
	}
	require.Len(t, repo.filters, 1)
	r = gin.New()
	r.GET("/usage/traffic", h.Traffic)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/usage/traffic", nil))
	require.Equal(t, 401, w.Code)
}

func (r *trafficHandlerRepository) TrafficKeyOptions(_ context.Context, userID int64, query string) ([]service.TrafficKeyOption, error) {
	r.keyUsers = append(r.keyUsers, userID)
	r.keyQueries = append(r.keyQueries, query)
	return []service.TrafficKeyOption{{ID: 3, Name: "Deleted key", Deleted: true}}, nil
}

func TestTrafficKeysIgnoreForeignUserAndNeverExposeCredentials(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &trafficHandlerRepository{}
	h := &UsageHandler{usageService: service.NewUsageService(repo, nil, nil, nil)}
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7})
		c.Next()
	})
	r.GET("/usage/traffic/keys", h.TrafficKeys)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/usage/traffic/keys?user_id=999&q=Deleted", nil))
	require.Equal(t, 200, w.Code)
	require.Equal(t, []int64{7}, repo.keyUsers)
	require.Equal(t, []string{"Deleted"}, repo.keyQueries)
	require.JSONEq(t, `{"code":0,"message":"success","data":[{"id":3,"name":"Deleted key","deleted":true}]}`, w.Body.String())
	r = gin.New()
	r.GET("/usage/traffic/keys", h.TrafficKeys)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/usage/traffic/keys", nil))
	require.Equal(t, 401, w.Code)
}

func TestTrafficCursorValidationAndUserScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &trafficHandlerRepository{}
	h := &UsageHandler{usageService: service.NewUsageService(repo, nil, nil, nil)}
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7}) })
	r.GET("/traffic", h.Traffic)
	cursor := service.EncodeTrafficCursor(service.TrafficEvent{StartedAt: time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC), EventID: "00000000-0000-0000-0000-000000000001"})
	base := "/traffic?period=month&date=2026-10-26&cursor=" + cursor
	for _, suffix := range []string{"&page=2", "&view=summary", "&date=2027-01-01"} {
		path := base + suffix
		if suffix == "&date=2027-01-01" {
			path = "/traffic?period=month&date=2027-01-01&cursor=" + cursor
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		require.Equal(t, 400, w.Code)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", base+"&view=details&user_id=999", nil))
	require.Equal(t, 200, w.Code)
	require.Len(t, repo.filters, 1)
	require.EqualValues(t, 7, repo.filters[0].UserID)
	require.NotNil(t, repo.filters[0].Cursor)
	require.Equal(t, "details", repo.filters[0].View)
}
