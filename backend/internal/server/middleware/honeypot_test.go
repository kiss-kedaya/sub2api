package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestHoneypotTrapsAndPasses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Next()
		if reason, ok := GetIngressRejectReason(c); ok {
			c.Header("X-Test-Reason", string(reason))
		}
	})
	r.Use(Honeypot(nil))
	ok := func(c *gin.Context) { c.String(http.StatusOK, "ok") }
	r.GET("/v1/chat/completions", ok)
	r.POST("/v1/chat/completions", ok)
	r.GET("/api/v1/auth/login", ok)
	r.GET("/admin", ok)
	r.GET("/admin/users", ok)
	r.GET("/health", ok)
	r.GET("/readyz", ok)
	r.GET("/canvas", ok)
	r.GET("/assets/index.js", ok)
	r.GET("/payment/result", ok)
	r.NoRoute(func(c *gin.Context) { c.String(http.StatusOK, "spa") })

	traps := []string{
		"/.env",
		"/.env.production",
		"/.git/HEAD",
		"/wp-login.php",
		"/wp-admin/install.php",
		"/xmlrpc.php",
		"/phpmyadmin",
		"/adminer.php",
		"/actuator/env",
		"/cgi-bin/test",
		"/boaform/admin",
		"/vendor/phpunit/eval-stdin.php",
		"/server-status",
		"/telescope/requests",
		"/_ignition/execute-solution",
		"/swagger/index.html",
		"/v2/api-docs",
		"/HNAP1",
		"/api/install",
		"/backup.sql",
		"/.aws/credentials",
		"/login/..%2f.env",
	}
	for _, path := range traps {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound || w.Body.Len() != 0 || w.Header().Get("X-Test-Reason") != string(IngressRejectHoneypot) {
			t.Fatalf("%s got %d body=%q reason=%q", path, w.Code, w.Body.String(), w.Header().Get("X-Test-Reason"))
		}
	}

	passes := []struct {
		method, path string
	}{
		{http.MethodPost, "/v1/chat/completions"},
		{http.MethodGet, "/api/v1/auth/login"},
		{http.MethodGet, "/admin"},
		{http.MethodGet, "/admin/users"},
		{http.MethodGet, "/health"},
		{http.MethodGet, "/readyz"},
		{http.MethodGet, "/canvas"},
		{http.MethodGet, "/assets/index.js"},
		{http.MethodGet, "/login"},
		{http.MethodGet, "/payment/result?out_trade_no=sub2_1&trade_status=TRADE_SUCCESS&sign=abc"},
	}
	for _, tc := range passes {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(tc.method, tc.path, nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK || w.Header().Get("X-Test-Reason") != "" {
			t.Fatalf("%s %s got %d body=%q reason=%q", tc.method, tc.path, w.Code, w.Body.String(), w.Header().Get("X-Test-Reason"))
		}
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/login?XDEBUG_SESSION_START=1", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound || w.Header().Get("X-Test-Reason") != string(IngressRejectHoneypot) {
		t.Fatalf("xdebug got %d reason=%q", w.Code, w.Header().Get("X-Test-Reason"))
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/login?file=php://filter/convert.base64-encode/resource=index", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("php wrapper got %d", w.Code)
	}

	// SigV4 走真实 API 路径不再当扫描器：放行给后续鉴权（这里 NoRoute/ok 返回 200）。
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=abc")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Header().Get("X-Test-Reason") != "" {
		t.Fatalf("aws sig on real path got %d reason=%q", w.Code, w.Header().Get("X-Test-Reason"))
	}

	// 但把本网关当 S3/aws 端点扫（trap 路径 + SigV4）仍然 404 封禁。
	for _, tc := range []struct {
		path   string
		header string
		value  string
	}{
		{"/.aws/credentials", "Authorization", "AWS4-HMAC-SHA256 Credential=abc"},
		{"/.env", "X-Amz-Credential", "abc/20260101/us-east-1/s3/aws4_request"},
	} {
		w = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodGet, tc.path, nil)
		req.Header.Set(tc.header, tc.value)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound || w.Header().Get("X-Test-Reason") != string(IngressRejectHoneypot) {
			t.Fatalf("aws sig on trap %s got %d reason=%q", tc.path, w.Code, w.Header().Get("X-Test-Reason"))
		}
	}

	// S3 预签名回链（X-Amz-* 在 query）打真实路径也不能被误封。
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/models?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=abc", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Header().Get("X-Test-Reason") != "" {
		t.Fatalf("presigned on real path got %d reason=%q", w.Code, w.Header().Get("X-Test-Reason"))
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer sk-test")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Header().Get("X-Test-Reason") != "" {
		t.Fatalf("bearer got %d reason=%q", w.Code, w.Header().Get("X-Test-Reason"))
	}
}

func TestHoneypotBanSpec(t *testing.T) {
	key, value, ok := honeypotBanSpec("203.0.113.8", "trap")
	if !ok || key != "kedaya:security:v1:ban:203.0.113.8" || value != "trap" {
		t.Fatalf("public got %q %q %v", key, value, ok)
	}
	if _, _, ok := honeypotBanSpec("51.222.42.218", "trap"); ok {
		t.Fatal("old host must not be banned")
	}
	if _, _, ok := honeypotBanSpec("10.1.2.3", "scanner"); ok {
		t.Fatal("private must not be banned")
	}
	if _, _, ok := honeypotBanSpec("127.0.0.1", "unknown_php"); ok {
		t.Fatal("loopback must not be banned")
	}
	if _, _, ok := honeypotBanSpec("2607:5300:203:7cda::1", "trap"); ok {
		t.Fatal("exempt v6 must not be banned")
	}
	if _, _, ok := honeypotBanSpec("not-an-ip", "trap"); ok {
		t.Fatal("bad ip")
	}
}
