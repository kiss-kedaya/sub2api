package handler

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func retryAfterTestContext(path string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, path, nil)
	return c, rec
}

func requireRetryAfterInRange(t *testing.T, rec *httptest.ResponseRecorder, lo, hi int) {
	t.Helper()
	value := rec.Header().Get("Retry-After")
	require.NotEmpty(t, value, "gateway-generated 503/429 must carry Retry-After")
	seconds, err := strconv.Atoi(value)
	require.NoError(t, err)
	require.GreaterOrEqual(t, seconds, lo)
	require.LessOrEqual(t, seconds, hi)
}

func TestSetGatewayRetryAfterOnlyOnGatewayGenerated503And429(t *testing.T) {
	t.Cleanup(func() { ConfigureRetryGuard(nil) })
	ConfigureRetryGuard(nil)

	for _, status := range []int{http.StatusServiceUnavailable, http.StatusTooManyRequests} {
		c, rec := retryAfterTestContext("/v1/messages")
		setGatewayRetryAfter(c, status)
		c.JSON(status, gin.H{"ok": false})
		requireRetryAfterInRange(t, rec, 5, 30)
		require.Equal(t, status, rec.Code, "status code must not change")
		require.JSONEq(t, `{"ok":false}`, rec.Body.String(), "body must not change")
	}

	for _, status := range []int{200, 400, 401, 403, 404, 500, 502, 504, 529} {
		c, rec := retryAfterTestContext("/v1/messages")
		setGatewayRetryAfter(c, status)
		c.JSON(status, gin.H{})
		require.Empty(t, rec.Header().Get("Retry-After"), "status %d must not get Retry-After", status)
	}
}

func TestSetGatewayRetryAfterKeepsExistingAndSkipsPassthroughAndCommitted(t *testing.T) {
	t.Run("an existing Retry-After (billing errors, upstream hint) is never overridden", func(t *testing.T) {
		c, rec := retryAfterTestContext("/v1/messages")
		c.Header("Retry-After", "3")
		setGatewayRetryAfter(c, http.StatusServiceUnavailable)
		c.JSON(http.StatusServiceUnavailable, gin.H{})
		require.Equal(t, "3", rec.Header().Get("Retry-After"))
	})
	t.Run("upstream passthrough errors are not the gateway's own decision", func(t *testing.T) {
		c, rec := retryAfterTestContext("/v1/messages")
		markUpstreamPassthroughError(c)
		setGatewayRetryAfter(c, http.StatusServiceUnavailable)
		c.JSON(http.StatusServiceUnavailable, gin.H{})
		require.Empty(t, rec.Header().Get("Retry-After"))
	})
	t.Run("headers already sent (stream in progress) cannot be changed", func(t *testing.T) {
		c, rec := retryAfterTestContext("/v1/messages")
		_, err := c.Writer.Write([]byte("data: x\n\n"))
		require.NoError(t, err)
		setGatewayRetryAfter(c, http.StatusServiceUnavailable)
		require.Empty(t, rec.Header().Get("Retry-After"))
	})
	t.Run("nil context is safe", func(t *testing.T) {
		require.NotPanics(t, func() { setGatewayRetryAfter(nil, 503); markUpstreamPassthroughError(nil) })
	})
}

func TestGatewayRetryAfterIsJitteredAndConfigurable(t *testing.T) {
	t.Cleanup(func() { ConfigureRetryGuard(nil) })
	ConfigureRetryGuard(nil)
	seen := map[int]struct{}{}
	for i := 0; i < 400; i++ {
		s := gatewayRetryAfterSeconds()
		require.GreaterOrEqual(t, s, 5)
		require.LessOrEqual(t, s, 30)
		seen[s] = struct{}{}
	}
	require.Greater(t, len(seen), 15, "Retry-After must be jittered so rejected clients do not come back in the same second")

	cfg := &config.Config{}
	cfg.Gateway.RetryGuard = config.GatewayRetryGuardConfig{RetryAfterMinSeconds: 7, RetryAfterMaxSeconds: 7}
	ConfigureRetryGuard(cfg)
	for i := 0; i < 20; i++ {
		require.Equal(t, 7, gatewayRetryAfterSeconds())
	}
}

// ---- 各 handler 家族的错误出口 ----------------------------------------------------

func TestRetryAfterOnMessagesFamilySinks(t *testing.T) {
	t.Cleanup(func() { ConfigureRetryGuard(nil) })
	ConfigureRetryGuard(nil)
	h := &GatewayHandler{}

	t.Run("no available accounts (503)", func(t *testing.T) {
		c, rec := retryAfterTestContext("/v1/messages")
		h.handleStreamingAwareError(c, http.StatusServiceUnavailable, "api_error", "No available accounts for this request", false)
		require.Equal(t, http.StatusServiceUnavailable, rec.Code)
		require.Equal(t, "api_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String(), "error body must be unchanged")
		requireRetryAfterInRange(t, rec, 5, 30)
	})
	t.Run("too many pending requests (429)", func(t *testing.T) {
		c, rec := retryAfterTestContext("/v1/messages")
		h.handleStreamingAwareErrorWithCode(c, http.StatusTooManyRequests, "rate_limit_error", gatewayQueueFullCode, "Too many pending requests, please retry later", false)
		require.Equal(t, http.StatusTooManyRequests, rec.Code)
		require.Equal(t, "Too many pending requests, please retry later", gjson.GetBytes(rec.Body.Bytes(), "error.message").String())
		requireRetryAfterInRange(t, rec, 5, 30)
	})
	t.Run("chat completions and responses variants", func(t *testing.T) {
		for _, write := range []func(*gin.Context){
			func(c *gin.Context) { h.chatCompletionsErrorResponse(c, 503, "server_error", "No available accounts") },
			func(c *gin.Context) {
				h.chatCompletionsErrorResponseWithCode(c, 429, "rate_limit_error", "gateway_queue_full", "Too many pending requests")
			},
			func(c *gin.Context) { h.responsesErrorResponse(c, 503, "server_error", "No available accounts") },
			func(c *gin.Context) {
				h.responsesErrorResponseWithTypeAndCode(c, 429, "rate_limit_error", "gateway_queue_full", "Too many pending requests")
			},
		} {
			c, rec := retryAfterTestContext("/v1/chat/completions")
			write(c)
			requireRetryAfterInRange(t, rec, 5, 30)
		}
	})
	t.Run("a 502 is not touched", func(t *testing.T) {
		c, rec := retryAfterTestContext("/v1/messages")
		h.handleStreamingAwareError(c, http.StatusBadGateway, "upstream_error", "Upstream request failed", false)
		require.Empty(t, rec.Header().Get("Retry-After"))
	})
	t.Run("a passthrough of the upstream's own 503 is left alone, its Retry-After is preserved", func(t *testing.T) {
		c, rec := retryAfterTestContext("/v1/messages")
		h.handleFailoverExhausted(c, &service.UpstreamFailoverError{
			StatusCode:      http.StatusServiceUnavailable,
			ResponseBody:    []byte(`{"type":"error","error":{"type":"overloaded_error","message":"upstream says no"}}`),
			ResponseHeaders: http.Header{"Retry-After": []string{"7"}},
		}, service.PlatformAnthropic, false)
		require.Equal(t, http.StatusServiceUnavailable, rec.Code)
		require.Equal(t, "7", rec.Header().Get("Retry-After"))

		c2, rec2 := retryAfterTestContext("/v1/messages")
		h.handleFailoverExhausted(c2, &service.UpstreamFailoverError{
			StatusCode:   http.StatusServiceUnavailable,
			ResponseBody: []byte(`{"type":"error","error":{"type":"overloaded_error","message":"upstream says no"}}`),
		}, service.PlatformAnthropic, false)
		require.Equal(t, http.StatusServiceUnavailable, rec2.Code)
		require.Empty(t, rec2.Header().Get("Retry-After"), "the gateway must not invent a back-off for a plain upstream passthrough")
	})
}

func TestRetryAfterOnGeminiNativeSink(t *testing.T) {
	t.Cleanup(func() { ConfigureRetryGuard(nil) })
	ConfigureRetryGuard(nil)
	c, rec := retryAfterTestContext("/v1beta/models/gemini-2.5-pro:generateContent")
	googleError(c, http.StatusServiceUnavailable, "No available accounts")
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Equal(t, int64(http.StatusServiceUnavailable), gjson.GetBytes(rec.Body.Bytes(), "error.code").Int())
	requireRetryAfterInRange(t, rec, 5, 30)

	c2, rec2 := retryAfterTestContext("/v1beta/models/gemini-2.5-pro:generateContent")
	googleError(c2, http.StatusBadGateway, "Upstream request failed")
	require.Empty(t, rec2.Header().Get("Retry-After"))
}

func TestRetryAfterOnOpenAIFamilySinks(t *testing.T) {
	t.Cleanup(func() { ConfigureRetryGuard(nil) })
	ConfigureRetryGuard(nil)
	h := &OpenAIGatewayHandler{}

	for name, write := range map[string]func(*gin.Context){
		"errorResponse 503":          func(c *gin.Context) { h.errorResponse(c, 503, "api_error", "No available accounts for this request") },
		"errorResponse 429":          func(c *gin.Context) { h.errorResponse(c, 429, "rate_limit_error", "Too many pending requests") },
		"anthropicErrorResponse 503": func(c *gin.Context) { h.anthropicErrorResponse(c, 503, "api_error", "No available accounts") },
		"streamingAware 503":         func(c *gin.Context) { h.handleStreamingAwareError(c, 503, "api_error", "No available accounts", false) },
		"streamingAware 429 with code": func(c *gin.Context) {
			h.handleStreamingAwareErrorWithCode(c, 429, "rate_limit_error", "gateway_queue_full", "Too many pending requests", false, false)
		},
	} {
		t.Run(name, func(t *testing.T) {
			c, rec := retryAfterTestContext("/v1/responses")
			write(c)
			require.Contains(t, []int{429, 503}, rec.Code, "status code is unchanged")
			requireRetryAfterInRange(t, rec, 5, 30)
		})
	}

	t.Run("502 and 400 untouched", func(t *testing.T) {
		for _, status := range []int{502, 400} {
			c, rec := retryAfterTestContext("/v1/responses")
			h.errorResponse(c, status, "upstream_error", "x")
			require.Empty(t, rec.Header().Get("Retry-After"))
		}
	})
	t.Run("upstream passthrough 503 keeps its own headers only", func(t *testing.T) {
		c, rec := retryAfterTestContext("/v1/responses")
		h.handleFailoverExhausted(c, &service.UpstreamFailoverError{
			StatusCode:   http.StatusServiceUnavailable,
			ResponseBody: []byte(`{"error":{"message":"upstream says no"}}`),
		}, false)
		require.Equal(t, http.StatusServiceUnavailable, rec.Code)
		require.Empty(t, rec.Header().Get("Retry-After"))
	})
}
