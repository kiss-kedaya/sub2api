package ip

import (
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

const trustedProxyConfigKey = "sub2api.trusted_proxy_config"

type trustedProxyConfig struct {
	configured bool
	prefixes   []netip.Prefix
}

// SetTrustedProxyConfig snapshots server.trusted_proxies and its explicit
// configured flag on the request context. Wildcard ranges (0.0.0.0/0, ::/0) are
// ignored so proxy-header trust always fails closed.
func SetTrustedProxyConfig(c *gin.Context, trustedProxies []string, configured bool) {
	if c == nil {
		return
	}
	var prefixes []netip.Prefix
	if configured {
		prefixes = parseStrictTrustedProxyPrefixes(trustedProxies)
	}
	c.Set(trustedProxyConfigKey, trustedProxyConfig{
		configured: configured,
		prefixes:   prefixes,
	})
}

// parseStrictTrustedProxyPrefixes parses IP/CIDR entries and excludes wildcard
// prefixes (0.0.0.0/0, ::/0) that would match every peer.
func parseStrictTrustedProxyPrefixes(values []string) []netip.Prefix {
	if len(values) == 0 {
		return nil
	}
	out := make([]netip.Prefix, 0, len(values))
	for _, raw := range values {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if strings.Contains(raw, "/") {
			prefix, err := netip.ParsePrefix(raw)
			if err != nil || !prefix.IsValid() || prefix.Bits() == 0 {
				continue
			}
			addr := prefix.Addr().Unmap()
			if prefix.Addr().Is4In6() {
				bits := prefix.Bits() - 96
				if bits <= 0 {
					continue
				}
				prefix = netip.PrefixFrom(addr, bits)
			}
			out = append(out, prefix.Masked())
			continue
		}
		if addr, err := netip.ParseAddr(raw); err == nil && addr.IsValid() {
			addr = addr.Unmap()
			out = append(out, netip.PrefixFrom(addr, addr.BitLen()))
		}
	}
	return out
}

func parsePeerAddr(remoteAddr string) (netip.Addr, bool) {
	host := normalizeIP(remoteAddr)
	host = strings.TrimPrefix(host, "[")
	host = strings.TrimSuffix(host, "]")
	if host == "" {
		return netip.Addr{}, false
	}
	addr, err := netip.ParseAddr(host)
	if err != nil || !addr.IsValid() {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

func matchesStrictTrustedPrefixes(peer netip.Addr, prefixes []netip.Prefix) bool {
	if !peer.IsValid() || len(prefixes) == 0 {
		return false
	}
	for _, prefix := range prefixes {
		if prefix.Bits() == 0 {
			continue
		}
		if prefix.Contains(peer) {
			return true
		}
	}
	return false
}

// IsTrustedProxyPeer reports whether remoteAddr belongs to an explicitly
// configured non-wildcard trusted proxy range.
func IsTrustedProxyPeer(remoteAddr string, trustedProxies []string, configured bool) bool {
	if !configured || len(trustedProxies) == 0 {
		return false
	}
	peer, ok := parsePeerAddr(remoteAddr)
	if !ok {
		return false
	}
	return matchesStrictTrustedPrefixes(peer, parseStrictTrustedProxyPrefixes(trustedProxies))
}

// IsRequestFromTrustedProxy reports whether the request's TCP peer matches an
// explicitly configured non-wildcard trusted proxy.
func IsRequestFromTrustedProxy(c *gin.Context, trustedProxies []string, configured bool) bool {
	if c == nil || c.Request == nil {
		return false
	}
	return IsTrustedProxyPeer(c.Request.RemoteAddr, trustedProxies, configured)
}

// IsContextFromTrustedProxy reports whether the request's TCP peer matches the
// trusted proxy configuration attached to the gin context via
// SetTrustedProxyConfig. Fails closed when absent or unconfigured.
func IsContextFromTrustedProxy(c *gin.Context) bool {
	if c == nil || c.Request == nil {
		return false
	}
	raw, ok := c.Get(trustedProxyConfigKey)
	if !ok {
		return false
	}
	cfg, ok := raw.(trustedProxyConfig)
	if !ok || !cfg.configured || len(cfg.prefixes) == 0 {
		return false
	}
	peer, ok := parsePeerAddr(c.Request.RemoteAddr)
	if !ok {
		return false
	}
	return matchesStrictTrustedPrefixes(peer, cfg.prefixes)
}

// IsRequestHTTPS reports whether the request arrived over HTTPS, trusting a
// single X-Forwarded-Proto header only when the TCP peer is in the explicitly
// configured non-wildcard trusted proxy list.
func IsRequestHTTPS(c *gin.Context, trustedProxies []string, configured bool) bool {
	if c == nil || c.Request == nil {
		return false
	}
	if c.Request.TLS != nil {
		return true
	}
	if !IsRequestFromTrustedProxy(c, trustedProxies, configured) {
		return false
	}
	return hasSingleHTTPSForwardedProto(c)
}

// IsContextHTTPS reports whether the request arrived over HTTPS using the
// trusted proxy config stored on the gin context. Fails closed for
// X-Forwarded-Proto when no trusted proxy config is present.
func IsContextHTTPS(c *gin.Context) bool {
	if c == nil || c.Request == nil {
		return false
	}
	if c.Request.TLS != nil {
		return true
	}
	if !IsContextFromTrustedProxy(c) {
		return false
	}
	return hasSingleHTTPSForwardedProto(c)
}

func hasSingleHTTPSForwardedProto(c *gin.Context) bool {
	values := c.Request.Header.Values("X-Forwarded-Proto")
	if len(values) != 1 {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(values[0]), "https")
}

// TrustedForwardedHost returns a validated single-value X-Forwarded-Host header
// only when the TCP peer is an explicitly configured non-wildcard trusted proxy.
func TrustedForwardedHost(c *gin.Context, trustedProxies []string, configured bool) (string, bool) {
	if c == nil || c.Request == nil {
		return "", false
	}
	if !IsRequestFromTrustedProxy(c, trustedProxies, configured) {
		return "", false
	}
	values := c.Request.Header.Values("X-Forwarded-Host")
	if len(values) != 1 {
		return "", false
	}
	raw := strings.TrimSpace(values[0])
	if raw == "" || strings.Contains(raw, ",") {
		return "", false
	}
	return ValidateRequestHost(raw)
}

// ValidateRequestHost validates strict host[:port] syntax and rejects
// multi-value commas, userinfo (@), path/query/fragment delimiters, backslashes,
// whitespace, and control characters.
func ValidateRequestHost(raw string) (string, bool) {
	hostport := strings.TrimSpace(raw)
	if hostport == "" || len(hostport) > 255 {
		return "", false
	}
	for i := 0; i < len(hostport); i++ {
		ch := hostport[i]
		if ch <= 0x20 || ch == 0x7f {
			return "", false
		}
		switch ch {
		case ',', '@', '/', '\\', '?', '#':
			return "", false
		}
	}

	var host, port string
	if strings.HasPrefix(hostport, "[") {
		if strings.HasSuffix(hostport, "]") {
			host = hostport[1 : len(hostport)-1]
		} else {
			var err error
			host, port, err = net.SplitHostPort(hostport)
			if err != nil || port == "" {
				return "", false
			}
		}
		addr, err := netip.ParseAddr(host)
		if err != nil || !addr.Is6() || addr.Zone() != "" {
			return "", false
		}
	} else if strings.Contains(hostport, ":") {
		var err error
		host, port, err = net.SplitHostPort(hostport)
		if err != nil || port == "" {
			return "", false
		}
	} else {
		host = hostport
	}

	if port != "" {
		if !isValidPortString(port) {
			return "", false
		}
	}

	if !isValidHostNameOrIP(host) {
		return "", false
	}
	return hostport, true
}

func isValidPortString(port string) bool {
	if port == "" || len(port) > 5 || port[0] == '0' {
		return false
	}
	for i := 0; i < len(port); i++ {
		if port[i] < '0' || port[i] > '9' {
			return false
		}
	}
	n, err := strconv.Atoi(port)
	return err == nil && n >= 1 && n <= 65535
}

func isValidHostNameOrIP(host string) bool {
	if host == "" {
		return false
	}
	if addr, err := netip.ParseAddr(host); err == nil && addr.IsValid() && addr.Zone() == "" {
		return true
	}
	if len(host) > 253 {
		return false
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 {
			return false
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := 0; i < len(label); i++ {
			ch := label[i]
			isAlpha := (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')
			isDigit := ch >= '0' && ch <= '9'
			if !isAlpha && !isDigit && ch != '-' {
				return false
			}
		}
	}
	return true
}

// ExtractHTTPOrigin extracts a normalized "<scheme>://<host[:port]>" origin
// from an absolute HTTP(S) URL, rejecting credentials, opaque URLs, backslashes,
// and invalid hosts.
func ExtractHTTPOrigin(raw string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || strings.ContainsAny(trimmed, "\\\r\n\t") {
		return "", false
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || !parsed.IsAbs() || parsed.Opaque != "" || parsed.User != nil {
		return "", false
	}
	scheme := strings.ToLower(strings.TrimSpace(parsed.Scheme))
	if scheme != "http" && scheme != "https" {
		return "", false
	}
	host, ok := ValidateRequestHost(parsed.Host)
	if !ok {
		return "", false
	}
	return scheme + "://" + host, true
}
