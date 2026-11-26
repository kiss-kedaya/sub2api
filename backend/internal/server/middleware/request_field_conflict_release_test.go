package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"
	"weak"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"

	"github.com/gin-gonic/gin"
)

func requireBodyCollected(t *testing.T, w weak.Pointer[byte], msg string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		runtime.GC()
		if w.Value() == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal(msg)
}

// A finished request must not leave its body reachable through the gin.Context. gin hands the context back to its
// pool without resetting it, and billing / log tasks carry it. `held` plays such a task: it keeps the context alive
// after the chain has returned. The body the middleware read from the network is tracked from inside the handler,
// which is the only place that sees the slice the middleware produced.
func TestRejectConflictingRequestFieldsReleasesTheBodyAfterTheChain(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	var held *gin.Context
	router.Use(func(c *gin.Context) {
		held = c
		c.Next()
	})
	router.Use(RejectConflictingRequestFields())
	var tracked weak.Pointer[byte]
	var handlerSaw int
	router.POST("/v1/responses", func(c *gin.Context) {
		body, err := httputil.ReadRequestBodyWithPrealloc(c.Request)
		if err != nil {
			t.Errorf("handler could not read the body: %v", err)
			return
		}
		handlerSaw = len(body)
		tracked = weak.Make(&body[0])
		c.Status(http.StatusOK)
	})

	payload := []byte(`{"model":"m","input":"` + strings.Repeat("p", 1<<20) + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(payload))
	req.ContentLength = int64(len(payload))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if handlerSaw != len(payload) {
		t.Fatalf("handler saw %d bytes, want %d: releasing must not touch the body while the chain runs", handlerSaw, len(payload))
	}
	requireBodyCollected(t, tracked, "the finished request's body is still reachable through the gin.Context")
	runtime.KeepAlive(held)
}

// The same holds when the middleware itself ends the chain (duplicate model field -> 400, handler never runs).
func TestRejectConflictingRequestFieldsReleasesTheBodyWhenItAbortsTheChain(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	var held *gin.Context
	var tracked weak.Pointer[byte]
	router.Use(func(c *gin.Context) {
		held = c
		c.Next()
		// outermost position: by now the inner middleware has returned, so Request.Body is what is left over
		if p, ok := c.Request.Body.(*httputil.PrereadBody); ok && len(p.Bytes()) > 0 {
			t.Errorf("PrereadBody still holds %d bytes after the chain", len(p.Bytes()))
		}
	})
	router.Use(func(c *gin.Context) {
		// observe the slice the next middleware will read: read it here once (zero copy afterwards)
		body, err := httputil.ReadRequestBodyWithPrealloc(c.Request)
		if err == nil && len(body) > 0 {
			tracked = weak.Make(&body[0])
		}
		c.Request.Body = httputil.NewPrereadBody(body)
		c.Next()
	})
	router.Use(RejectConflictingRequestFields())
	router.POST("/v1/responses", func(c *gin.Context) {
		t.Error("handler must not run for a body with a duplicated model field")
	})

	payload := []byte(`{"model":"a","model":"b","input":"` + strings.Repeat("p", 1<<20) + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(payload))
	req.ContentLength = int64(len(payload))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	requireBodyCollected(t, tracked, "the rejected request's body is still reachable through the gin.Context")
	runtime.KeepAlive(held)
}
