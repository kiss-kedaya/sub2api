//go:build embed

package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestNormalizeInjectedSettingsRemovesInlineLogo(t *testing.T) {
	raw := []byte(`{"site_name":"Kedaya","site_logo":"data:image/png;base64,aGVsbG8="}`)
	normalized := string(normalizeInjectedSettings(raw))

	if !strings.Contains(normalized, `"site_logo":"/site-logo"`) {
		t.Fatalf("expected site_logo to point to the cached endpoint, got %s", normalized)
	}
	if strings.Contains(normalized, "data:image/") {
		t.Fatalf("inline image data leaked into injected settings: %s", normalized)
	}
}

func TestDecodeSiteLogoDataURL(t *testing.T) {
	contentType, content, ok := decodeSiteLogoDataURL("data:image/png;base64,aGVsbG8=")
	if !ok || contentType != "image/png" || string(content) != "hello" {
		t.Fatalf("unexpected decoded logo: ok=%v contentType=%q content=%q", ok, contentType, content)
	}
	contentType, content, ok = decodeSiteLogoDataURL("DATA:IMAGE/PNG;BASE64,aGVsbG8=")
	if !ok || contentType != "image/png" || string(content) != "hello" {
		t.Fatalf("uppercase data URL should decode: ok=%v contentType=%q content=%q", ok, contentType, content)
	}

	if _, _, ok := decodeSiteLogoDataURL("https://example.com/logo.png"); ok {
		t.Fatal("external logo URL must not be decoded as an inline image")
	}
}

func TestFrontendServer_ServesSiteLogoWithETag(t *testing.T) {
	provider := &mockSettingsProvider{
		settings: map[string]string{
			"site_logo": "data:image/png;base64,aGVsbG8=",
		},
	}
	server, err := NewFrontendServer(provider)
	if err != nil {
		t.Fatalf("create frontend server: %v", err)
	}

	router := gin.New()
	router.Use(server.Middleware())

	first := httptest.NewRecorder()
	router.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/site-logo", nil))
	if first.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", first.Code, first.Body.String())
	}
	body, err := io.ReadAll(first.Body)
	if err != nil {
		t.Fatalf("read logo response: %v", err)
	}
	if string(body) != "hello" {
		t.Fatalf("unexpected logo body: %q", body)
	}
	if got := first.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("expected image/png content type, got %q", got)
	}
	if first.Header().Get("ETag") == "" {
		t.Fatal("expected ETag on logo response")
	}
	if !strings.Contains(first.Header().Get("Cache-Control"), "max-age=3600") {
		t.Fatalf("expected cache policy, got %q", first.Header().Get("Cache-Control"))
	}

	second := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/site-logo", nil)
	request.Header.Set("If-None-Match", first.Header().Get("ETag"))
	router.ServeHTTP(second, request)
	if second.Code != http.StatusNotModified {
		t.Fatalf("expected 304 for matching ETag, got %d", second.Code)
	}
	if provider.called != 1 {
		t.Fatalf("expected logo settings to be cached, provider called %d times", provider.called)
	}

}
