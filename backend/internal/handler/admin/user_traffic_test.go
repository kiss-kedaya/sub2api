// Adapted from ranxi2001/sub2api @ 9620d315, licensed under LGPL-3.0-or-later.
package admin

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type trafficKeyRepository struct {
	service.UsageLogRepository
	userID int64
	query  string
}

func (r *trafficKeyRepository) TrafficKeyOptions(_ context.Context, userID int64, query string) ([]service.TrafficKeyOption, error) {
	r.userID = userID
	r.query = query
	return []service.TrafficKeyOption{{ID: 3, Name: "Old key", Deleted: true}}, nil
}

func TestTrafficAdminKeysScopeAndValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &trafficKeyRepository{}
	h := &UsageHandler{usageService: service.NewUsageService(repo, nil, nil, nil)}
	r := gin.New()
	r.GET("/traffic/keys", h.TrafficKeys)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/traffic/keys?user_id=7&q=Old", nil))
	require.Equal(t, 200, w.Code)
	require.EqualValues(t, 7, repo.userID)
	require.Equal(t, "Old", repo.query)
	require.Contains(t, w.Body.String(), `"deleted":true`)
	for _, query := range []string{"user_id=-1", "user_id=bad"} {
		w = httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/traffic/keys?"+query, nil))
		require.Equal(t, 400, w.Code)
	}
}

func TestTrafficAdminHealthReportsOptionalWorkerAndCachedState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &UsageHandler{}
	r := gin.New()
	r.GET("/traffic/health", h.TrafficHealth)
	// An unconfigured optional recorder never calls a nil service or SQL.
	h.RecordTraffic(service.TrafficEvent{})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/traffic/health", nil))
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"unavailable_dropped_events":1`)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "corrupt.json.invalid"), []byte("preserve"), 0600))
	d, err := service.NewTrafficDelivery(dir, func(context.Context, service.TrafficEvent) error { return nil })
	require.NoError(t, err)
	defer d.Stop()
	h.trafficDelivery = d
	require.Eventually(t, func() bool {
		health, err := d.Health(context.Background())
		return err == nil && health.QuarantinedEvents == 1
	}, time.Second, time.Millisecond)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/traffic/health", nil))
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"quarantined_events":1`)
	require.Contains(t, w.Body.String(), `"status":"degraded"`)
	require.NotContains(t, w.Body.String(), dir)
	d.Stop()
	require.NoError(t, os.RemoveAll(dir))
	// Cached health must still return without touching an unavailable mount.
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/traffic/health", nil))
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"worker_running":false`)
}
