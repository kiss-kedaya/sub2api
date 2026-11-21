//go:build embed

package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// versionedInjectionProvider is a controllable settings provider: it exposes the
// injection version and cache TTL exactly like service.SettingService does.
type versionedInjectionProvider struct {
	mu       sync.Mutex
	settings any
	version  uint64
	ttl      time.Duration
	calls    int
}

func (p *versionedInjectionProvider) GetPublicSettingsForInjection(context.Context) (any, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	return p.settings, nil
}

func (p *versionedInjectionProvider) SettingsInjectionVersion() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.version
}

func (p *versionedInjectionProvider) SettingsInjectionCacheTTL() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ttl
}

func (p *versionedInjectionProvider) set(settings any, version uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.settings = settings
	p.version = version
}

func (p *versionedInjectionProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func serveIndexOnce(t *testing.T, server *FrontendServer) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Set(middleware.CSPNonceKey, "test-nonce")
	server.serveIndexHTML(c)
	return w
}

func TestInjectedHTMLCache_ExpiresAfterTTL(t *testing.T) {
	cache := NewHTMLCacheWithTTL(20 * time.Millisecond)
	cache.SetBaseHTML([]byte("<html><head></head></html>"))
	cache.SetForVersion([]byte("rendered"), []byte(`{"crypto_enabled":false}`), 0)

	require.NotNil(t, cache.GetForVersion(0), "fresh entry must be served")
	require.NotNil(t, cache.Get())

	time.Sleep(40 * time.Millisecond)

	require.Nil(t, cache.GetForVersion(0), "expired entry must force a re-render")
	require.Nil(t, cache.Get())
}

func TestInjectedHTMLCache_KeyedOnSettingsVersion(t *testing.T) {
	cache := NewHTMLCacheWithTTL(time.Minute)
	cache.SetBaseHTML([]byte("<html><head></head></html>"))
	cache.SetForVersion([]byte("rendered"), []byte(`{"v":1}`), 7)

	require.NotNil(t, cache.GetForVersion(7))
	require.Nil(t, cache.GetForVersion(8),
		"a settings version change (peer broadcast) must invalidate the rendered HTML")
}

func TestInjectedHTMLCache_NonPositiveTTLFallsBackToDefault(t *testing.T) {
	cache := NewHTMLCacheWithTTL(0)
	require.Equal(t, defaultInjectedHTMLCacheTTL, cache.ttl)

	cache.SetBaseHTML([]byte("<html><head></head></html>"))
	cache.SetForVersion([]byte("rendered"), []byte(`{}`), 0)
	require.NotNil(t, cache.GetForVersion(0))
}

// TestFrontendServer_InjectionCacheConvergesViaTTL is the fallback guarantee:
// when the cross-instance broadcast is lost, the rendered HTML must still stop
// injecting the old window.__APP_CONFIG__ after the TTL.
func TestFrontendServer_InjectionCacheConvergesViaTTL(t *testing.T) {
	provider := &versionedInjectionProvider{
		settings: map[string]any{"crypto_enabled": false},
		ttl:      25 * time.Millisecond,
	}
	server, err := NewFrontendServer(provider)
	require.NoError(t, err)

	first := serveIndexOnce(t, server)
	require.Contains(t, first.Body.String(), `"crypto_enabled":false`)
	require.Equal(t, 1, provider.callCount())

	// The value changes on another replica, but the broadcast never arrives, so
	// this process still believes version 0 is current.
	provider.set(map[string]any{"crypto_enabled": true}, 0)

	second := serveIndexOnce(t, server)
	require.Contains(t, second.Body.String(), `"crypto_enabled":false`,
		"within the TTL the cache is still allowed to serve the old injection")
	require.Equal(t, 1, provider.callCount())

	time.Sleep(50 * time.Millisecond)

	third := serveIndexOnce(t, server)
	require.Contains(t, third.Body.String(), `"crypto_enabled":true`,
		"after the TTL the replica must re-read settings instead of injecting stale flags")
	require.Equal(t, 2, provider.callCount())
}

// TestFrontendServer_InjectionCacheInvalidatedByVersion covers the fast path: a
// settings version bump (peer broadcast) must take effect on the very next
// request, without waiting for the TTL.
func TestFrontendServer_InjectionCacheInvalidatedByVersion(t *testing.T) {
	provider := &versionedInjectionProvider{
		settings: map[string]any{"crypto_enabled": false},
		ttl:      time.Minute,
	}
	server, err := NewFrontendServer(provider)
	require.NoError(t, err)

	first := serveIndexOnce(t, server)
	require.Contains(t, first.Body.String(), `"crypto_enabled":false`)
	require.Equal(t, 1, provider.callCount())

	provider.set(map[string]any{"crypto_enabled": true}, 1)

	second := serveIndexOnce(t, server)
	require.Contains(t, second.Body.String(), `"crypto_enabled":true`)
	require.Equal(t, 2, provider.callCount())
	require.NotEqual(t, first.Header().Get("ETag"), second.Header().Get("ETag"),
		"the ETag must follow the settings hash so caches revalidate")
}
