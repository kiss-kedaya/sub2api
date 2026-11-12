//go:build unit

package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// TestGatewayIPSharingBusinessPathsOnly proves the wiring: probes and metrics
// never touch the counter, while three different accounts on a /v1 endpoint
// auto-add the source IP to the existing notice policy without rejecting the
// request.
func TestGatewayIPSharingBusinessPathsOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })

	settings := noticeTestSettings(t)
	detector := NewIPKeySharingDetector(settings, client)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		userID, _ := strconv.ParseInt(c.GetHeader("X-Test-User"), 10, 64)
		c.Set(string(ContextKeyAPIKey), &service.APIKey{ID: userID, UserID: userID, Key: fmt.Sprintf("sk-%d-secret", userID)})
		c.Next()
	})
	router.Use(GatewayIPSharing(settings, detector))
	for _, path := range []string{"/healthz", "/readyz", "/metrics"} {
		router.GET(path, func(c *gin.Context) { c.Status(http.StatusOK) })
	}
	router.POST("/v1/messages", func(c *gin.Context) { c.Status(http.StatusOK) })

	call := func(method, path string, userID int64) int {
		request := httptest.NewRequest(method, path, nil)
		request.RemoteAddr = "203.0.113.77:41234"
		request.Header.Set("X-Test-User", strconv.FormatInt(userID, 10))
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		return recorder.Code
	}

	for _, path := range []string{"/healthz", "/readyz", "/metrics"} {
		for _, userID := range []int64{1, 2, 3, 4} {
			require.Equal(t, http.StatusOK, call(http.MethodGet, path, userID))
		}
	}
	require.False(t, server.Exists("gateway_ip_sharing:users:203.0.113.77"), "probe traffic must not be counted")
	require.Empty(t, settings.GatewayIPNoticeSnapshot().AutoEntries)

	require.Equal(t, http.StatusOK, call(http.MethodPost, "/v1/messages", 1))
	require.Equal(t, http.StatusOK, call(http.MethodPost, "/v1/messages", 2))
	require.Equal(t, http.StatusOK, call(http.MethodPost, "/v1/messages", 3))

	policy := settings.GatewayIPNoticeSnapshot()
	require.Len(t, policy.AutoEntries, 1)
	require.Equal(t, "203.0.113.77", policy.AutoEntries[0].IP)
	require.Equal(t, "auto", policy.AutoEntries[0].Source)
	require.Equal(t, 3, policy.AutoEntries[0].DistinctUsers)
	require.Equal(t, 3, len(policy.AutoEntries[0].KeyPrefixes))

	// The triggering request itself is never rejected: this middleware only
	// records. (The existing notice middleware decides enforcement.)
	require.Equal(t, http.StatusOK, call(http.MethodPost, "/v1/messages", 4))
	require.Len(t, settings.GatewayIPNoticeSnapshot().AutoEntries, 1, "repeat detections must not duplicate the entry")
}
