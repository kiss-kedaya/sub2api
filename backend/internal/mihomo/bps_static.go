package mihomo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyutil"
)

// The static pool serves accounts whose BPS session proxy uses the admin proxy
// list (IP 管理) instead of the managed Mihomo kernel. It reuses the session
// binding, scoring, cooldown and single-flight probe machinery on a dedicated
// manager that never runs a kernel and never registers with the managed set.
// Node identity is the digest of the upstream proxy URL: editing a proxy row
// yields a new identity with fresh health, and duplicate rows pointing at the
// same URL share one exit identity. Static exits use the subscription cooldown
// profile; rotation contracts of the supplier are not modeled here.
var bpsStaticManager = &Manager{bpsStaticMode: true}

func bpsStaticNodeKey(rawURL string) string {
	digest := sha256.Sum256([]byte("bps-static|" + rawURL))
	return "static-" + hex.EncodeToString(digest[:])
}

// SetBPSStaticProxies replaces the static pool membership. Health, cooldown and
// session state survive for URLs that stay in the pool; bindings whose exit was
// removed rebind after their in-flight requests finish, matching node-removal
// semantics of the managed pool. An empty list empties the pool: acquisitions
// then fail and callers surface the usual 503, never a direct fallback.
func SetBPSStaticProxies(urls []string) {
	next := make(map[string]string, len(urls))
	for _, raw := range urls {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		next[bpsStaticNodeKey(trimmed)] = trimmed
	}
	m := bpsStaticManager
	m.bpsMu.Lock()
	m.bpsStatic = next
	m.bpsMu.Unlock()
}

// AcquireBPSStaticLease mirrors AcquireBPSLease for the static pool. Excluded
// proxy URLs prevent retrying an already failed exit within one request.
func AcquireBPSStaticLease(ctx context.Context, scope string, excludedProxyURLs ...string) (*BPSLease, error) {
	return acquireBPSStaticLeaseScoped(ctx, scope, false, excludedProxyURLs...)
}

// AcquireBPSStaticTransientLease uses an isolated request identity and drops
// its binding on failure or final release, matching the managed transient path.
func AcquireBPSStaticTransientLease(ctx context.Context, scope string, excludedProxyURLs ...string) (*BPSLease, error) {
	return acquireBPSStaticLeaseScoped(ctx, scope, true, excludedProxyURLs...)
}

func acquireBPSStaticLeaseScoped(ctx context.Context, scope string, transient bool, excludedProxyURLs ...string) (*BPSLease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if scope == "" {
		return nil, errors.New("BPS session identity required")
	}
	excluded := make(map[string]bool, len(excludedProxyURLs))
	for _, proxy := range excludedProxyURLs {
		if trimmed := strings.TrimSpace(proxy); trimmed != "" {
			excluded[bpsStaticNodeKey(trimmed)] = true
		}
	}
	return bpsStaticManager.acquireScopedBPSLease(ctx, scope, transient, excluded)
}

// Static exits are supplier proxies with optional credentials. Logs must never
// carry the URL, host or auth; only the derived node hash identifies the exit.
func bpsProxyLogValue(m *Manager, proxy string) string {
	if m != nil && m.bpsStaticMode {
		return "ip-pool"
	}
	return proxy
}

func probeBPSStaticHTTPS(ctx context.Context, proxy string) error {
	client, err := newBPSStaticProbeClient(proxy)
	if err != nil {
		return err
	}
	defer client.CloseIdleConnections()
	return probeBPSHTTPSClient(ctx, client)
}

func newBPSStaticProbeClient(proxy string) (*http.Client, error) {
	parsed, err := url.Parse(proxy)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("invalid BPS upstream proxy")
	}
	// Same probe contract as the managed pool: explicit proxy, verified TLS,
	// fixed non-inference URL, no redirects, no OAuth or session headers, and
	// never an environment/direct fallback. socks5(h) is configured as a dialer.
	transport := &http.Transport{TLSHandshakeTimeout: bpsProbeTimeout, DisableKeepAlives: true}
	if err := proxyutil.ConfigureTransportProxy(transport, parsed); err != nil {
		return nil, err
	}
	client := &http.Client{Transport: transport, Timeout: bpsProbeTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return client, nil
}
