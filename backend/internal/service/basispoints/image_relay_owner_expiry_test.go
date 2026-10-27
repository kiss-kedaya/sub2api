package basispoints

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestIndependentImageRelayOwnersEnforceExpiry(t *testing.T) {
	a, err := newTestImageRelay(t, "https://node-a.example.invalid")
	require.NoError(t, err)
	b, err := newTestImageRelay(t, "https://node-b.example.invalid")
	require.NoError(t, err)
	raw := relayTestRequest(t, relayTestPNG(t))
	urls := make([]string, 0, 2)
	for _, relay := range []*ImageRelay{a, b} {
		out, err := relay.Rewrite(raw, "account:201/key:101/thread:a")
		require.NoError(t, err)
		urls = append(urls, relayTestURL(t, out))
	}
	probe := func(relay *ImageRelay, method, target string, code int) {
		t.Helper()
		w := httptest.NewRecorder()
		relay.ServeHTTP(w, httptest.NewRequest(method, target, nil))
		require.Equal(t, code, w.Code)
	}
	for i, relay := range []*ImageRelay{a, b} {
		probe(relay, http.MethodGet, urls[i], http.StatusOK)
		probe(relay, http.MethodGet, urls[1-i], http.StatusNotFound)
	}
	u, err := url.Parse(urls[0])
	require.NoError(t, err)
	token := strings.TrimPrefix(u.Path, ImageRelayPath)
	a.mu.Lock()
	entry := a.entries[token]
	if entry != nil {
		entry.expires = time.Now().Add(-time.Second)
	}
	a.mu.Unlock()
	require.NotNil(t, entry, "expire the actual published owner entry, not a missing token")
	// Read-time expiry must work before the periodic cleanup worker runs.
	probe(a, http.MethodHead, urls[0], http.StatusNotFound)
	probe(a, http.MethodGet, urls[0], http.StatusNotFound)
	probe(b, http.MethodGet, urls[1], http.StatusOK)
	probe(b, http.MethodHead, urls[1], http.StatusOK)
	a.mu.Lock()
	remaining, bytes := len(a.entries), a.bytes
	a.mu.Unlock()
	require.Zero(t, remaining)
	require.Zero(t, bytes)
}
