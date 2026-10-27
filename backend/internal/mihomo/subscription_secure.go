package mihomo

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
)

// Resolve once and send that exact address through either route. Preflight-only
// hostname checks would let a proxy resolve the same hostname to a private IP.
func publicSubscriptionTarget(ctx context.Context, raw string) (*url.URL, string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" {
		return nil, "", errors.New("invalid HTTP(S) subscription address")
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", u.Hostname())
	if err != nil || len(addresses) == 0 {
		return nil, "", errors.New("subscription DNS lookup failed")
	}
	for _, addr := range addresses {
		if !publicSubscriptionIP(addr) {
			return nil, "", errors.New("subscription destination must be a public IP")
		}
	}
	return u, addresses[0].Unmap().String(), nil
}

func publicSubscriptionIP(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() {
		return false
	}
	for _, raw := range []string{"100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "2001::/32", "2002::/16", "64:ff9b::/96", "64:ff9b:1::/48"} {
		if netip.MustParsePrefix(raw).Contains(addr) {
			return false
		}
	}
	return true
}

func (m *Manager) getSubscriptionSecure(ctx context.Context, address string, limit int64, ua, proxyAddress string) ([]byte, error) {
	for redirects := 0; redirects <= 5; redirects++ {
		u, pinned, err := publicSubscriptionTarget(ctx, address)
		if err != nil {
			return nil, err
		}
		transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, ServerName: u.Hostname()}}
		if proxyAddress != "" {
			proxy, parseErr := url.Parse(proxyAddress)
			if parseErr != nil || proxy.Hostname() != "127.0.0.1" || proxy.Scheme != "http" || proxy.User != nil {
				return nil, errors.New("subscription proxy must be the managed loopback endpoint")
			}
			transport.Proxy = http.ProxyURL(proxy)
		}
		originalHost := u.Host
		if port := u.Port(); port != "" {
			u.Host = net.JoinHostPort(pinned, port)
		} else if strings.Contains(pinned, ":") {
			u.Host = "[" + pinned + "]"
		} else {
			u.Host = pinned
		}
		client := &http.Client{Transport: transport, Timeout: m.client.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			client.CloseIdleConnections()
			return nil, errors.New("invalid subscription request")
		}
		req.Host = originalHost
		req.Header.Set("User-Agent", ua)
		resp, err := client.Do(req)
		if err != nil {
			client.CloseIdleConnections()
			return nil, errors.New("subscription download failed")
		}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			location := resp.Header.Get("Location")
			_ = resp.Body.Close()
			client.CloseIdleConnections()
			base, _ := url.Parse(address)
			next, parseErr := base.Parse(location)
			if parseErr != nil || location == "" || (base.Scheme == "https" && next.Scheme != "https") {
				return nil, errors.New("unsafe subscription redirect")
			}
			address = next.String()
			continue
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			client.CloseIdleConnections()
			return nil, fmt.Errorf("subscription returned HTTP %d", resp.StatusCode)
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, limit+1))
		_ = resp.Body.Close()
		client.CloseIdleConnections()
		if readErr != nil || int64(len(body)) > limit {
			return nil, errors.New("subscription incomplete or too large")
		}
		return body, nil
	}
	return nil, errors.New("too many subscription redirects")
}
