package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

func TestTrackUsageInflightSkipsRedisForFastRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	server := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: server.Addr()})
	service.SetUsageInflightStore(repository.NewUsageInflightCache(rdb))
	t.Cleanup(func() { service.SetUsageInflightStore(nil) })

	router := gin.New()
	router.Use(ClientRequestID())
	router.GET("/v1/responses", func(c *gin.Context) {
		c.Set(string(ContextKeyAPIKey), &service.APIKey{ID: 4, UserID: 8, User: &service.User{ID: 8, Email: "user@example.test"}})
		stop := trackUsageInflight(c)
		defer stop()
		_, _ = c.Writer.Write([]byte("hello world!!!!"))
	})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/responses", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if n := len(server.Keys()); n != 0 {
		t.Fatalf("fast request touched redis keys: %v", server.Keys())
	}
}

func TestTrackUsageInflightRecordsFirstByteThenDeletes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	server := miniredis.RunT(t)
	service.SetUsageInflightStore(repository.NewUsageInflightCache(redis.NewClient(&redis.Options{Addr: server.Addr()})))
	t.Cleanup(func() { service.SetUsageInflightStore(nil) })

	seen := make(chan service.UsageInflightSnapshot, 1)
	router := gin.New()
	router.Use(ClientRequestID())
	router.GET("/v1/responses", func(c *gin.Context) {
		c.Set(string(ContextKeyAPIKey), &service.APIKey{ID: 4, UserID: 8, Name: "test-key", User: &service.User{ID: 8, Email: "user@example.test"}})
		c.Set(string(ContextKeyUser), AuthSubject{UserID: 8})
		c.Set("_gateway_inbound_endpoint", "/v1/responses")
		c.Set("openai_actual_upstream_endpoint", "/v1/chat/completions")
		c.Request = c.Request.WithContext(service.WithRequestedReasoningEffort(c.Request.Context(), "high"))
		stop := trackUsageInflight(c)
		defer stop()
		c.Writer.WriteHeader(http.StatusOK)
		_, _ = c.Writer.Write([]byte("hello world!!!!"))
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			rows := service.VisibleUsageInflight(c.Request.Context(), 8, usagestats.UsageLogFilters{}, nil)
			if len(rows) == 1 && rows[0].FirstTokenMs != nil && rows[0].OutputTokens >= 1 {
				seen <- rows[0]
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	select {
	case row := <-seen:
		if row.Email != "user@example.test" || row.APIKeyName != "test-key" || row.InboundEndpoint != "/v1/responses" || row.UpstreamEndpoint != "/v1/chat/completions" || row.ReasoningEffort != "high" {
			t.Fatalf("row=%+v", row)
		}
	default:
		t.Fatal("inflight row was not visible during the request")
	}
	deadline := time.Now().Add(time.Second)
	for {
		left := service.VisibleUsageInflight(req.Context(), 8, usagestats.UsageLogFilters{}, nil)
		if len(left) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("should be gone, got %+v", left)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// headerSpyWriter 记下静默期内谁读了响应头。静默期里 handler 不碰 header，读的只能是别的 goroutine。
type headerSpyWriter struct {
	gin.ResponseWriter
	quiet atomic.Bool
	hits  atomic.Int32
}

func (w *headerSpyWriter) Header() http.Header {
	if w.quiet.Load() {
		w.hits.Add(1)
	}
	return w.ResponseWriter.Header()
}

// 线上 fatal error: concurrent map read and map write 的回归：
// 1 秒定时器不能去读 handler 还在写的响应头；SSE 标记改在首字节时由请求 goroutine 记下。
func TestTrackUsageInflightTimerLeavesResponseHeaderAlone(t *testing.T) {
	gin.SetMode(gin.TestMode)
	server := miniredis.RunT(t)
	service.SetUsageInflightStore(repository.NewUsageInflightCache(redis.NewClient(&redis.Options{Addr: server.Addr()})))
	t.Cleanup(func() { service.SetUsageInflightStore(nil) })

	var spy *headerSpyWriter
	seen := make(chan service.UsageInflightSnapshot, 1)
	router := gin.New()
	router.Use(ClientRequestID())
	router.GET("/v1/responses", func(c *gin.Context) {
		c.Set(string(ContextKeyAPIKey), &service.APIKey{ID: 4, UserID: 8, User: &service.User{ID: 8, Email: "user@example.test"}})
		spy = &headerSpyWriter{ResponseWriter: c.Writer}
		c.Writer = spy
		stop := trackUsageInflight(c)
		defer stop()
		c.Header("Content-Type", "text/event-stream")
		_, _ = c.Writer.Write([]byte("hello world!!!!"))
		spy.quiet.Store(true)
		defer spy.quiet.Store(false)
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if rows := service.VisibleUsageInflight(c.Request.Context(), 8, usagestats.UsageLogFilters{}, nil); len(rows) == 1 {
				seen <- rows[0]
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/responses", nil))
	select {
	case row := <-seen:
		if !row.Stream {
			t.Fatalf("响应是 SSE，在途行应标 stream: %+v", row)
		}
	default:
		t.Fatal("inflight row was not visible during the request")
	}
	if n := spy.hits.Load(); n != 0 {
		t.Fatalf("定时器 goroutine 读了 %d 次响应头", n)
	}
}

// 线上死锁的回归。两种装心跳的时机，首个字节都必须是心跳，且请求必须正常结束：
//   - 早于 1 秒落库定时器：旧代码里落库刷新读被包装的响应头，会把心跳停掉；
//   - 晚于 1 秒落库定时器（先读大请求体或排队再装心跳）：首个心跳字节持着心跳锁，
//     在途记录首字节刷新又读被包装的响应头，要同一把锁，自己等自己，请求永远不返回。
func TestTrackUsageInflightHeartbeatFirstByte(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name         string
		installAfter time.Duration
		interval     time.Duration
		runFor       time.Duration
	}{
		{"before persist timer", 0, 1200 * time.Millisecond, 1500 * time.Millisecond},
		{"after persist timer", 1200 * time.Millisecond, 300 * time.Millisecond, 700 * time.Millisecond},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := miniredis.RunT(t)
			service.SetUsageInflightStore(repository.NewUsageInflightCache(redis.NewClient(&redis.Options{Addr: server.Addr()})))
			t.Cleanup(func() { service.SetUsageInflightStore(nil) })

			router := gin.New()
			router.Use(ClientRequestID())
			router.GET("/v1/responses", func(c *gin.Context) {
				c.Set(string(ContextKeyAPIKey), &service.APIKey{ID: 4, UserID: 8, User: &service.User{ID: 8, Email: "user@example.test"}})
				stop := trackUsageInflight(c)
				defer stop()
				time.Sleep(tc.installAfter)
				stopHeartbeat := service.StartOpenAIStreamSSEKeepalive(c, tc.interval)
				time.Sleep(tc.runFor)
				stopHeartbeat()
				_, _ = c.Writer.Write([]byte("data: done\n\n"))
			})
			rec := httptest.NewRecorder()
			done := make(chan struct{})
			go func() {
				defer close(done)
				router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/responses", nil))
			}()
			select {
			case <-done:
			case <-time.After(6 * time.Second):
				t.Fatal("请求卡死：心跳首字节触发的快照刷新和心跳锁互相等待")
			}
			if got := rec.Body.String(); !strings.HasPrefix(got, ": keepalive") {
				t.Fatalf("首个字节应是心跳，实际 %q", got)
			}
		})
	}
}

// A handler may replace Request while the timer refreshes account and model metadata.
func TestUsageInflightSnapshotUsesPublishedContext(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	initial := c.Request.Context()
	state := &usageInflightWriteState{ginCtx: c, requestCtx: initial}
	ctx := context.WithValue(initial, ctxkey.Model, "test-model")
	ctx = context.WithValue(ctx, ctxkey.AccountID, int64(42))
	ctx = service.WithRequestedReasoningEffort(ctx, "high")
	c.Set(service.UsageInflightRequestContextKey, ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			c.Request = c.Request.WithContext(context.WithValue(initial, ctxkey.Model, "other"))
		}
	}()
	for i := 0; i < 1000; i++ {
		state.mu.Lock()
		snap := state.snapshotLocked()
		state.mu.Unlock()
		if snap.Model != "test-model" || snap.AccountID != 42 || snap.ReasoningEffort != "high" {
			t.Fatalf("published metadata lost: %+v", snap)
		}
	}
	<-done
}

func TestTrackUsageInflightCancellationStopsRefresh(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), ctxkey.ClientRequestID, "test-request"))
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil).WithContext(ctx)
	c.Set(string(ContextKeyAPIKey), &service.APIKey{ID: 4, UserID: 8})
	stop := trackUsageInflight(c)
	defer stop()
	writer, ok := c.Writer.(*usageInflightWriter)
	if !ok {
		t.Fatal("tracker writer missing")
	}
	cancel()
	select {
	case <-writer.state.done:
	case <-time.After(time.Second):
		t.Fatal("cancelled request left the refresh worker running")
	}
}

func TestUsageInflightSnapshotFollowsSelectedAccountAndGroup(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/messages", nil)
	state := &usageInflightWriteState{ginCtx: c, requestCtx: c.Request.Context()}
	for _, selected := range []struct {
		accountID, groupID     int64
		accountName, groupName string
	}{
		{11, 21, "first-account", "first-group"},
		{12, 22, "fallback-account", "fallback-group"},
		{13, 23, "", ""},
	} {
		ctx := context.WithValue(c.Request.Context(), ctxkey.Group, &service.Group{ID: selected.groupID, Name: selected.groupName})
		ctx = context.WithValue(ctx, ctxkey.AccountID, selected.accountID)
		ctx = context.WithValue(ctx, ctxkey.AccountName, selected.accountName)
		c.Set(service.UsageInflightRequestContextKey, ctx)
		state.mu.Lock()
		snap := state.snapshotLocked()
		state.mu.Unlock()
		if snap.AccountID != selected.accountID || snap.AccountName != selected.accountName || snap.GroupID != selected.groupID || snap.GroupName != selected.groupName {
			t.Fatalf("selected routing identity stale: %+v", snap)
		}
	}
}
