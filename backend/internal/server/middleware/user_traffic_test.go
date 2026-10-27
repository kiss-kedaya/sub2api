// Adapted from ranxi2001/sub2api @ 9620d315, licensed under LGPL-3.0-or-later.
package middleware

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type trafficTestRecorder struct{ events []service.TrafficEvent }

func (r *trafficTestRecorder) RecordTraffic(e service.TrafficEvent) { r.events = append(r.events, e) }
func trafficTestAuth(c *gin.Context) {
	group := int64(3)
	c.Set(string(ContextKeyAPIKey), &service.APIKey{ID: 2, UserID: 1, GroupID: &group})
	c.Next()
}

func TestTrafficCountsActualBytesOnceAcrossPreReadAndSSE(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, path := range []string{"/v1/messages", "/v1/chat/completions", "/v1/responses", "/v1/responses/compact", "/v1beta/models/*modelAction", "/v1/images/generations"} {
		t.Run(path, func(t *testing.T) {
			recorder := &trafficTestRecorder{}
			r := gin.New()
			r.Use(UserTraffic(recorder), trafficTestAuth, func(c *gin.Context) {
				p, err := io.ReadAll(c.Request.Body)
				require.NoError(t, err)
				c.Request.Body = io.NopCloser(strings.NewReader(string(p)))
				c.Next()
			})
			r.POST(path, func(c *gin.Context) {
				_, err := io.ReadAll(c.Request.Body)
				require.NoError(t, err)
				_, err = c.Writer.WriteString(": ping\n\n")
				require.NoError(t, err)
				c.Writer.Flush()
				_, err = c.Writer.Write([]byte("data: 你好\n\n"))
				require.NoError(t, err)
				c.Writer.Flush()
			})
			url := strings.ReplaceAll(path, "*modelAction", "gemini:generateContent")
			req := httptest.NewRequest(http.MethodPost, url, strings.NewReader("你好🌏"))
			req.ContentLength = -1 // chunked/unknown lengths must use actual reads
			r.ServeHTTP(httptest.NewRecorder(), req)
			require.Len(t, recorder.events, 1)
			e := recorder.events[0]
			require.EqualValues(t, len("你好🌏"), e.UploadBytes)
			require.EqualValues(t, len(": ping\n\ndata: 你好\n\n"), e.DownloadBytes)
			require.True(t, e.RequestComplete)
			require.Equal(t, path, e.Endpoint)
			require.EqualValues(t, 1, e.UserID)
			require.EqualValues(t, 3, e.GroupID)
		})
	}
}

func TestTrafficRecordsErrorsAndCanceledPartialRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := &trafficTestRecorder{}
	r := gin.New()
	r.Use(UserTraffic(recorder), trafficTestAuth)
	r.POST("/v1/messages", func(c *gin.Context) {
		p := make([]byte, 2)
		_, err := io.ReadFull(c.Request.Body, p)
		require.NoError(t, err)
		c.String(429, "limited")
	})
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader("abcdef"))
		req.Header.Set("X-Request-ID", "same-client-id")
		if i == 1 {
			ctx, cancel := context.WithCancel(req.Context())
			cancel()
			req = req.WithContext(ctx)
		}
		r.ServeHTTP(httptest.NewRecorder(), req)
	}
	require.Len(t, recorder.events, 2)
	// IDs are assigned after the nonblocking handoff, never on this goroutine.
	require.Empty(t, recorder.events[0].EventID)
	require.Empty(t, recorder.events[1].EventID)
	require.Equal(t, "http_error", recorder.events[0].Outcome)
	require.Equal(t, "canceled", recorder.events[1].Outcome)
	require.EqualValues(t, 2, recorder.events[0].UploadBytes)
	require.EqualValues(t, 7, recorder.events[0].DownloadBytes)
	require.False(t, recorder.events[0].RequestComplete)
}

func TestTrafficExcludesUnauthenticatedAndUnsupportedProtocols(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, header, path             string
		authenticated, forwarded, want bool
	}{
		{name: "anonymous", path: "/v1/messages"},
		{name: "websocket", path: "/v1/responses", header: "websocket", authenticated: true},
		{name: "forged upgrade header", path: "/v1/messages", header: "websocket", authenticated: true, want: true},
		{name: "live", path: "/v1/live", authenticated: true},
		{name: "codex live", path: "/backend-api/codex/realtime/calls", authenticated: true},
		{name: "verified internal forwarding", path: "/v1/messages", authenticated: true, forwarded: true},
		{name: "forged forwarding header", path: "/v1/messages", authenticated: true, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := &trafficTestRecorder{}
			r := gin.New()
			if tc.forwarded {
				r.Use(func(c *gin.Context) { c.Set("serverless_verified_ip", "203.0.113.1"); c.Next() })
			}
			r.Use(UserTraffic(recorder))
			if tc.authenticated {
				r.Use(trafficTestAuth)
			}
			r.POST(tc.path, func(c *gin.Context) {
				if tc.path == "/v1/responses" && tc.header == "websocket" {
					c.Status(http.StatusSwitchingProtocols)
					return
				}
				c.String(403, "denied")
			})
			req := httptest.NewRequest("POST", tc.path, nil)
			req.Header.Set("Upgrade", tc.header)
			req.Header.Set("X-Sub2api-Route", "forged")
			r.ServeHTTP(httptest.NewRecorder(), req)
			if tc.want {
				require.Len(t, recorder.events, 1)
			} else {
				require.Empty(t, recorder.events)
			}
		})
	}
}

type trafficPartialWriter struct{ *httptest.ResponseRecorder }

func (w trafficPartialWriter) Write(p []byte) (int, error) {
	_, _ = w.ResponseRecorder.Write(p[:2])
	return 2, errors.New("disconnected")
}

func TestTrafficCountsPartialWritesAndFlushErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := &trafficTestRecorder{}
	r := gin.New()
	r.Use(UserTraffic(recorder), trafficTestAuth)
	r.POST("/v1/messages", func(c *gin.Context) { _, _ = c.Writer.Write([]byte("abcdef")) })
	r.ServeHTTP(trafficPartialWriter{httptest.NewRecorder()}, httptest.NewRequest("POST", "/v1/messages", nil))
	require.Len(t, recorder.events, 1)
	require.EqualValues(t, 2, recorder.events[0].DownloadBytes)
	require.Equal(t, "write_error", recorder.events[0].Outcome)
	flushRecorder := &timingFlushErrorRecorder{ResponseRecorder: httptest.NewRecorder(), err: errors.New("flush failed")}
	r = gin.New()
	r.Use(UserTraffic(recorder), trafficTestAuth)
	r.POST("/v1/messages", func(c *gin.Context) { c.Writer.Flush() })
	r.ServeHTTP(flushRecorder, httptest.NewRequest("POST", "/v1/messages", nil))
	require.Equal(t, "write_error", recorder.events[1].Outcome)
}

func TestTrafficPanicRemainsVisibleToRecovery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := &trafficTestRecorder{}
	r := gin.New()
	r.Use(gin.Recovery(), UserTraffic(recorder), trafficTestAuth)
	r.POST("/v1/messages", func(c *gin.Context) { panic("test") })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/v1/messages", nil))
	require.Equal(t, 500, w.Code)
	require.Len(t, recorder.events, 1)
	require.Equal(t, 500, recorder.events[0].StatusCode)
	require.Equal(t, "panic", recorder.events[0].Outcome)
}

func TestTrafficPanicIncludesProductionRecoveryResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, written := range []bool{false, true} {
		t.Run(map[bool]string{false: "before response", true: "after response"}[written], func(t *testing.T) {
			recorder := &trafficTestRecorder{}
			r := gin.New()
			r.Use(Recovery(), UserTraffic(recorder), trafficTestAuth)
			r.POST("/v1/messages", func(c *gin.Context) {
				if written {
					c.String(http.StatusOK, "partial response")
				}
				panic("test production recovery")
			})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("POST", "/v1/messages", nil))
			require.NotEmpty(t, w.Body.String())
			require.Len(t, recorder.events, 1)
			require.EqualValues(t, w.Body.Len(), recorder.events[0].DownloadBytes)
			require.Equal(t, w.Code, recorder.events[0].StatusCode)
			require.Equal(t, "panic", recorder.events[0].Outcome)
			if written {
				require.Equal(t, "partial response", w.Body.String())
			} else {
				require.Equal(t, http.StatusInternalServerError, w.Code)
			}
		})
	}
}

func TestTrafficRecoveryBrokenPipeStillFinalizesOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := &trafficTestRecorder{}
	r := gin.New()
	r.Use(Recovery(), UserTraffic(recorder), trafficTestAuth)
	r.POST("/v1/messages", func(c *gin.Context) {
		panic(&net.OpError{Op: "write", Net: "tcp", Err: &os.SyscallError{Syscall: "write", Err: syscall.EPIPE}})
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/v1/messages", nil))
	require.Empty(t, w.Body.String())
	require.Len(t, recorder.events, 1)
	require.Zero(t, recorder.events[0].DownloadBytes)
	require.Equal(t, "panic", recorder.events[0].Outcome)
}

func TestTrafficRecoveryDoesNotRepeatFinalizationOnNextRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := &trafficTestRecorder{}
	r := gin.New()
	r.Use(Recovery(), UserTraffic(recorder), trafficTestAuth)
	r.POST("/v1/messages", func(c *gin.Context) {
		if c.Query("panic") == "true" {
			panic("test")
		}
		c.String(http.StatusOK, "ok")
	})
	for _, path := range []string{"/v1/messages?panic=true", "/v1/messages", "/v1/messages?panic=true"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("POST", path, nil))
		require.EqualValues(t, w.Body.Len(), recorder.events[len(recorder.events)-1].DownloadBytes)
	}
	require.Len(t, recorder.events, 3)
	require.Equal(t, "completed", recorder.events[1].Outcome)
}

func TestTrafficAuxiliaryQueueCannotDelayGatewayResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	entered, releaseCh := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(releaseCh) })
	base := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(base, "blocked-mount"), []byte("file"), 0600))
	delivery, err := service.NewTrafficDeliveryResolved(func() string { close(entered); <-releaseCh; return filepath.Join(base, "blocked-mount", "pending") }, func(context.Context, service.TrafficEvent) error { return nil })
	require.NoError(t, err)
	t.Cleanup(func() { release(); delivery.Stop() })
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("storage resolver did not start")
	}
	router := gin.New()
	router.Use(UserTraffic(delivery), trafficTestAuth)
	router.POST("/v1/messages", func(c *gin.Context) { c.String(200, "business-ok") })
	health, err := delivery.Health(context.Background())
	require.NoError(t, err)
	done := make(chan struct{})
	badResponse := make(chan int, 1)
	go func() {
		defer close(done)
		for i := 0; i < health.QueueCapacity+5; i++ {
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest("POST", "/v1/messages", strings.NewReader("hello")))
			if response.Code != 200 || response.Body.String() != "business-ok" {
				badResponse <- response.Code
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("gateway waited for optional statistics storage")
	}
	select {
	case code := <-badResponse:
		t.Fatalf("statistics changed business response: %d", code)
	default:
	}
	health, err = delivery.Health(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, 5, health.QueueDroppedEvents)
	require.Equal(t, health.QueueCapacity, health.QueuedEvents)
}
