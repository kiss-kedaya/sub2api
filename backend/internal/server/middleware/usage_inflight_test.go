package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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
		c.Set(string(ContextKeyAPIKey), &service.APIKey{ID: 4, UserID: 8, User: &service.User{ID: 8, Email: "duck@kedaya.ai"}})
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
		c.Set(string(ContextKeyAPIKey), &service.APIKey{ID: 4, UserID: 8, User: &service.User{ID: 8, Email: "duck@kedaya.ai"}})
		c.Set(string(ContextKeyUser), AuthSubject{UserID: 8})
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
		if row.Email != "duck@kedaya.ai" {
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
