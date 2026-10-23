package middleware

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	coremiddleware "github.com/Wei-Shaw/sub2api/internal/middleware"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ipnotice"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func noticeTestSettings(t *testing.T) *service.SettingService {
	t.Helper()
	svc := service.NewSettingService(&panelRateLimitStubRepo{}, nil)
	require.NoError(t, svc.SetGatewayIPNotice(context.Background(), ipnotice.Settings{
		Enabled: true, IPs: []string{"203.0.113.1", "203.0.113.2"}, Message: "test operator notice", RPM: 100,
	}))
	return svc
}

func TestGatewayIPNoticeQuotaSharedByIPAcrossKeysAndReplicas(t *testing.T) {
	gin.SetMode(gin.TestMode)
	server := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, redisClient.Close()) })
	var upstreamCalls atomic.Int32
	var routers []*gin.Engine
	for range 2 {
		r := gin.New()
		require.NoError(t, r.SetTrustedProxies(nil))
		r.Use(gatewayIPNoticeWithLimiter(noticeTestSettings(t), coremiddleware.NewRateLimiter(redisClient)))
		r.POST("/v1/chat/completions", func(c *gin.Context) {
			upstreamCalls.Add(1)
			body, err := io.ReadAll(c.Request.Body)
			require.NoError(t, err)
			if c.ClientIP() == "203.0.113.1" || c.ClientIP() == "203.0.113.2" {
				require.Contains(t, gjson.GetBytes(body, "messages.0.content").String(), "test operator notice")
			}
			c.JSON(200, gin.H{"choices": []any{gin.H{"message": gin.H{"content": "original reply"}}}, "usage": gin.H{"total_tokens": 1}})
		})
		routers = append(routers, r)
	}
	request := func(n int, ip string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"current input"}]}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer fixture-"+string(rune('a'+n%2)))
		r.Header.Set("X-Forwarded-For", "203.0.113.200")
		r.Header.Set("CF-Connecting-IP", "203.0.113.200")
		r.RemoteAddr = ip + ":1234"
		response := httptest.NewRecorder()
		routers[n%2].ServeHTTP(response, r)
		return response
	}
	for n := range 100 {
		response := request(n, "203.0.113.1")
		require.Equal(t, 200, response.Code)
		require.Equal(t, ipnotice.WrapOutputText("original reply", "test operator notice"), gjson.Get(response.Body.String(), "choices.0.message.content").String())
	}
	limited := request(100, "203.0.113.1")
	require.Equal(t, 400, limited.Code)
	require.Equal(t, "test operator notice", gjson.Get(limited.Body.String(), "error.message").String())
	require.NotEmpty(t, limited.Header().Get("Retry-After"))
	require.Equal(t, int32(100), upstreamCalls.Load())
	require.Equal(t, 200, request(101, "203.0.113.2").Code)
	ordinary := request(102, "203.0.113.9")
	require.Equal(t, 200, ordinary.Code)
	require.Equal(t, "original reply", gjson.Get(ordinary.Body.String(), "choices.0.message.content").String())
	server.FastForward(time.Minute + time.Second)
	require.Equal(t, 200, request(103, "203.0.113.1").Code)
}

func TestGatewayIPNoticeFragmentedStreamingAndUpstreamErrors(t *testing.T) {
	for _, protocol := range []ipnotice.Protocol{ipnotice.Chat, ipnotice.Anthropic, ipnotice.Responses, ipnotice.Gemini} {
		t.Run(string(protocol), func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Writer.Header().Set("Content-Type", "text/event-stream")
			writer := newGatewayIPNoticeWriter(c.Writer, protocol, "notice")
			var body string
			switch protocol {
			case ipnotice.Chat:
				body = "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"reply\"}}]}\n\ndata: [DONE]\n\n"
			case ipnotice.Anthropic:
				body = "event: message_start\r\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\"}}\r\n\r\nevent: content_block_start\r\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"reply\"}}\r\n\r\n"
			case ipnotice.Responses:
				body = "event: response.created\ndata: {\"type\":\"response.created\",\"sequence_number\":0,\"response\":{\"id\":\"resp\"}}\n\nevent: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"sequence_number\":1,\"output_index\":0,\"delta\":\"reply\"}\n\n"
			case ipnotice.Gemini:
				body = "data: {\"candidates\":[{\"index\":0,\"content\":{\"parts\":[{\"text\":\"reply\"}]}}]}\n\n"
			}
			for i := range len(body) {
				_, err := writer.Write([]byte(body[i : i+1]))
				require.NoError(t, err)
				writer.Flush()
			}
			require.NoError(t, writer.finish())
			require.Contains(t, recorder.Body.String(), "notice")
			require.Contains(t, recorder.Body.String(), "reply")
			require.True(t, recorder.Flushed)
		})
	}
	for _, status := range []int{400, 403, 429, 502, 503} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Writer.Header().Set("Content-Type", "application/json")
		c.Writer.WriteHeader(status)
		writer := newGatewayIPNoticeWriter(c.Writer, ipnotice.Responses, "notice")
		original := `{"error":{"message":"unmodified upstream error"}}`
		_, err := writer.WriteString(original)
		require.NoError(t, err)
		require.NoError(t, writer.finish())
		require.Equal(t, status, recorder.Code)
		require.Equal(t, original, recorder.Body.String())
	}
}

type unavailableIPNoticeLimiter struct{}

func (unavailableIPNoticeLimiter) Allow(context.Context, string, int, time.Duration) (coremiddleware.AllowResult, error) {
	return coremiddleware.AllowResult{}, errors.New("Redis unavailable")
}

func TestGatewayIPNoticeRedisFailureOnlyRejectsSelectedIPs(t *testing.T) {
	r := gin.New()
	require.NoError(t, r.SetTrustedProxies(nil))
	r.Use(gatewayIPNoticeWithLimiter(noticeTestSettings(t), unavailableIPNoticeLimiter{}))
	r.GET("/v1/models", func(c *gin.Context) { c.JSON(200, gin.H{"data": []any{}}) })
	for _, tt := range []struct {
		ip     string
		status int
	}{{"203.0.113.1", 400}, {"203.0.113.9", 200}} {
		req := httptest.NewRequest("GET", "/v1/models", nil)
		req.RemoteAddr = tt.ip + ":1234"
		response := httptest.NewRecorder()
		r.ServeHTTP(response, req)
		require.Equal(t, tt.status, response.Code)
	}
}
