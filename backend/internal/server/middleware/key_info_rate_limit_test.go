package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type keyInfoLimiterStub struct {
	counts map[string]int
	keys   []string
	err    error
	retry  time.Duration
}

func (s *keyInfoLimiterStub) Allow(_ context.Context, key string, limit int, _ time.Duration) (middleware.AllowResult, error) {
	s.keys = append(s.keys, key)
	if s.err != nil {
		return middleware.AllowResult{}, s.err
	}
	if s.counts == nil {
		s.counts = map[string]int{}
	}
	s.counts[key]++
	if s.counts[key] > limit {
		return middleware.AllowResult{Allowed: false, Count: int64(s.counts[key]), RetryAfter: s.retry}, nil
	}
	return middleware.AllowResult{Allowed: true, Count: int64(s.counts[key])}, nil
}

func keyInfoLimitedRouter(limiter panelRateLimitAllower, limit int, keyID int64) (*gin.Engine, *int) {
	gin.SetMode(gin.TestMode)
	served := 0
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if keyID > 0 {
			c.Set(string(ContextKeyAPIKey), &service.APIKey{ID: keyID})
		}
		c.Next()
	})
	router.GET("/v1/sub2api/key", keyInfoRateLimit(limiter, limit, "key"), func(c *gin.Context) { served++; c.Status(http.StatusOK) })
	return router, &served
}

func TestKeyInfoRateLimitCountsPerKeyNotPerClient(t *testing.T) {
	limiter := &keyInfoLimiterStub{}
	routerA, servedA := keyInfoLimitedRouter(limiter, 3, 100)
	routerB, servedB := keyInfoLimitedRouter(limiter, 3, 200)

	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		routerA.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/sub2api/key", nil))
		require.Equal(t, http.StatusOK, w.Code, "call %d is inside the limit", i+1)
	}
	w := httptest.NewRecorder()
	routerA.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/sub2api/key", nil))
	require.Equal(t, http.StatusTooManyRequests, w.Code)
	require.Equal(t, 3, *servedA, "the limited call never reaches the handler")

	// Another key is not affected by the first one running out.
	w = httptest.NewRecorder()
	routerB.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/sub2api/key", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, 1, *servedB)
	require.Equal(t, []string{"key_info:key:100", "key_info:key:100", "key_info:key:100", "key_info:key:100", "key_info:key:200"}, limiter.keys,
		"the bucket is the API key id: never an IP, never the key text")
}

// The three endpoints each get their own counter: polling all of them at the allowed rate must not trip any.
func TestKeyInfoRateLimitGivesEachEndpointItsOwnCounter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	limiter := &keyInfoLimiterStub{}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(ContextKeyAPIKey), &service.APIKey{ID: 100})
		c.Next()
	})
	for _, endpoint := range []string{"key", "groups", "usage"} {
		router.GET("/"+endpoint, keyInfoRateLimit(limiter, 2, endpoint), func(c *gin.Context) { c.Status(http.StatusOK) })
	}
	for _, endpoint := range []string{"key", "groups", "usage"} {
		for i := 0; i < 2; i++ {
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/"+endpoint, nil))
			require.Equal(t, http.StatusOK, w.Code, "%s call %d", endpoint, i+1)
		}
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/groups", nil))
	require.Equal(t, http.StatusTooManyRequests, w.Code, "only the endpoint that is out of budget is limited")
	require.Equal(t, 2, limiter.counts["key_info:key:100"])
	require.Equal(t, 3, limiter.counts["key_info:groups:100"])
}

func TestKeyInfoRateLimitAnswersWithRetryAfterAndAMachineReadableError(t *testing.T) {
	limiter := &keyInfoLimiterStub{retry: 2500 * time.Millisecond}
	router, _ := keyInfoLimitedRouter(limiter, 1, 100)
	httptest.NewRecorder()
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/sub2api/key", nil))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/sub2api/key", nil))

	require.Equal(t, http.StatusTooManyRequests, w.Code)
	require.Equal(t, "3", w.Header().Get("Retry-After"), "rounded up to whole seconds")
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	require.Contains(t, w.Header().Get("Content-Type"), "application/json")
	require.Contains(t, w.Body.String(), `"code":"key_info_rate_limited"`)
	require.Contains(t, w.Body.String(), `"type":"rate_limit_error"`)
}

func TestKeyInfoRateLimitMarksClientLimitForOps(t *testing.T) {
	gin.SetMode(gin.TestMode)
	limiter := &keyInfoLimiterStub{counts: map[string]int{"key_info:x:100": 99}}
	router := gin.New()
	var marked bool
	var reason string
	router.Use(func(c *gin.Context) {
		c.Set(string(ContextKeyAPIKey), &service.APIKey{ID: 100})
		c.Next()
		marked = service.HasOpsClientBusinessLimited(c)
		if v, ok := c.Get(service.OpsClientBusinessLimitedReasonKey); ok {
			reason, _ = v.(string)
		}
	})
	router.GET("/x", keyInfoRateLimit(limiter, 5, "x"), func(c *gin.Context) { c.Status(http.StatusOK) })

	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))

	require.True(t, marked, "a client-side limit must not count as a gateway error")
	require.Equal(t, service.OpsClientBusinessLimitedReasonKeyInfoRateLimit, reason)
}

func TestKeyInfoRateLimitFailsOpenAndIgnoresMissingContext(t *testing.T) {
	t.Run("redis error lets the request through", func(t *testing.T) {
		router, served := keyInfoLimitedRouter(&keyInfoLimiterStub{err: errors.New("redis down")}, 1, 100)
		for i := 0; i < 5; i++ {
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/sub2api/key", nil))
			require.Equal(t, http.StatusOK, w.Code)
		}
		require.Equal(t, 5, *served)
	})
	t.Run("no authenticated key: nothing to count", func(t *testing.T) {
		limiter := &keyInfoLimiterStub{}
		router, served := keyInfoLimitedRouter(limiter, 1, 0)
		for i := 0; i < 3; i++ {
			router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/sub2api/key", nil))
		}
		require.Equal(t, 3, *served)
		require.Empty(t, limiter.keys)
	})
	t.Run("no redis client: the middleware is a pass-through", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		router := gin.New()
		router.GET("/x", KeyInfoRateLimit(nil, "x"), func(c *gin.Context) { c.Status(http.StatusOK) })
		for i := 0; i < 50; i++ {
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
			require.Equal(t, http.StatusOK, w.Code)
		}
	})
}

func TestKeyInfoRateLimitBudgetIsGenerousForPollingAndTightForLoops(t *testing.T) {
	// A status bar polling every 5 seconds makes 12 calls a minute; the budget must not get in its way, and it
	// must stop a loop that polls as fast as it can.
	require.GreaterOrEqual(t, KeyInfoRateLimitPerMinute, 12)
	require.LessOrEqual(t, KeyInfoRateLimitPerMinute, 120)
	require.Equal(t, strconv.Itoa(KeyInfoRateLimitPerMinute), strconv.Itoa(30), "changing the budget is a product decision: update the docs page with it")
}

func TestIsKeySelfServicePath(t *testing.T) {
	for path, want := range map[string]bool{
		"/v1/sub2api/billing": true,
		"/v1/sub2api/key":     true,
		"/v1/sub2api/groups":  true,
		"/v1/sub2api/usage":   true,
		"/v1/usage":           false, // keeps its own, older handling
		"/v1/models":          false,
		"/v1/sub2api/":        false,
		"/v1/sub2api/keys":    false,
		"/v1/sub2api/key/":    false,
		"/sub2api/key":        false,
		"/api/v1/keys":        false,
	} {
		require.Equal(t, want, isKeySelfServicePath(path), path)
	}
}
