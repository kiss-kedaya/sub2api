//go:build unit

package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

type adminAuthProbeRecorderStub struct {
	calls []service.AdminAuthProbeInput
}

func (r *adminAuthProbeRecorderStub) RecordAdminAuthFailure(_ context.Context, input service.AdminAuthProbeInput) bool {
	r.calls = append(r.calls, input)
	return false
}

func TestAdminAuthProbeMonitor_RecordsOnlyAuthFailures(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := &adminAuthProbeRecorderStub{}
	router := gin.New()
	router.Use(AdminAuthProbeMonitor(recorder))

	admin := router.Group("/api/v1/admin")
	admin.GET("/users", func(c *gin.Context) { c.AbortWithStatus(http.StatusUnauthorized) })
	admin.GET("/accounts", func(c *gin.Context) { c.AbortWithStatus(http.StatusForbidden) })
	admin.GET("/settings", func(c *gin.Context) { c.Status(http.StatusOK) })
	router.GET("/public", func(c *gin.Context) { c.Status(http.StatusUnauthorized) })

	do := func(method, path, ua string) int {
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("User-Agent", ua)
		req.Header.Set("Authorization", "Bearer must-not-be-recorded")
		req.Header.Set("X-Api-Key", "must-not-be-recorded")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w.Code
	}

	if code := do(http.MethodGet, "/api/v1/admin/users", "probe/1.0"); code != http.StatusUnauthorized {
		t.Fatalf("status = %d", code)
	}
	if code := do(http.MethodGet, "/api/v1/admin/accounts", "probe/1.0"); code != http.StatusForbidden {
		t.Fatalf("status = %d", code)
	}
	if code := do(http.MethodGet, "/api/v1/admin/settings", "probe/1.0"); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	// 非 admin 路由的 401 不计入 admin 探测。
	if code := do(http.MethodGet, "/public", "probe/1.0"); code != http.StatusUnauthorized {
		t.Fatalf("status = %d", code)
	}

	if len(recorder.calls) != 2 {
		t.Fatalf("expected 2 recorded admin failures, got %d: %+v", len(recorder.calls), recorder.calls)
	}
	first := recorder.calls[0]
	if first.Path != "/api/v1/admin/users" || first.StatusCode != http.StatusUnauthorized || first.Method != http.MethodGet {
		t.Fatalf("unexpected first record: %+v", first)
	}
	second := recorder.calls[1]
	if second.StatusCode != http.StatusForbidden || second.Path != "/api/v1/admin/accounts" {
		t.Fatalf("unexpected second record: %+v", second)
	}
	for _, call := range recorder.calls {
		if call.UserAgent != "probe/1.0" {
			t.Fatalf("user agent = %q", call.UserAgent)
		}
	}
}

func TestAdminAuthProbeMonitor_NilRecorderIsSafe(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(AdminAuthProbeMonitor(nil))
	router.GET("/api/v1/admin/users", func(c *gin.Context) { c.AbortWithStatus(http.StatusUnauthorized) })

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/users", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", w.Code)
	}
}
