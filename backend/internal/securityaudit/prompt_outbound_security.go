package securityaudit

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const maxGuardResponseBytes int64 = 256 * 1024

var (
	errRedirectBlocked = errors.New("prompt guard redirect blocked")
	metadataHosts      = map[string]struct{}{
		"metadata": {}, "metadata.google.internal": {}, "metadata.azure.internal": {},
		"instance-data": {}, "instance-data.ec2.internal": {},
	}
	blockedPrefixes = []netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/8"),
		netip.MustParsePrefix("100.64.0.0/10"),
		netip.MustParsePrefix("169.254.0.0/16"),
		netip.MustParsePrefix("192.0.0.0/24"),
		netip.MustParsePrefix("192.0.2.0/24"),
		netip.MustParsePrefix("198.18.0.0/15"),
		netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("203.0.113.0/24"),
		netip.MustParsePrefix("224.0.0.0/4"),
		netip.MustParsePrefix("240.0.0.0/4"),
		netip.MustParsePrefix("::/128"),
		netip.MustParsePrefix("fe80::/10"),
		netip.MustParsePrefix("ff00::/8"),
		netip.MustParsePrefix("2001:db8::/32"),
	}
)

type DNSResolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

type netResolver struct{ resolver *net.Resolver }

func (r netResolver) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	return r.resolver.LookupNetIP(ctx, network, host)
}

func NormalizeBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", infraerrors.BadRequest("prompt_audit_invalid_base_url", "审计节点地址无效")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", infraerrors.BadRequest("prompt_audit_invalid_base_url_scheme", "审计节点仅支持 HTTP(S)")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", infraerrors.BadRequest("prompt_audit_unsafe_base_url", "审计节点地址不能包含凭据、查询参数或片段")
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if host == "" {
		return "", infraerrors.BadRequest("prompt_audit_invalid_base_url", "审计节点地址无效")
	}
	if _, blocked := metadataHosts[host]; blocked || strings.HasSuffix(host, ".metadata.google.internal") {
		return "", infraerrors.BadRequest("prompt_audit_unsafe_base_url", "审计节点地址不在允许范围")
	}
	allowPrivate := isExplicitPrivateHost(host)
	if addr, err := netip.ParseAddr(host); err == nil {
		if isBlockedAddress(addr) {
			return "", infraerrors.BadRequest("prompt_audit_unsafe_base_url", "审计节点地址不在允许范围")
		}
		allowPrivate = addr.IsPrivate() || addr.IsLoopback()
	}
	if parsed.Scheme == "http" && !allowPrivate {
		return "", infraerrors.BadRequest("prompt_audit_https_required", "公网审计节点必须使用 HTTPS")
	}
	path := strings.TrimRight(parsed.EscapedPath(), "/")
	if strings.EqualFold(path, "/v1") {
		path = ""
	}
	parsed.Path = path
	parsed.RawPath = ""
	return strings.TrimRight(parsed.String(), "/"), nil
}

func ChatCompletionsURL(base string) (string, error) {
	normalized, err := NormalizeBaseURL(base)
	if err != nil {
		return "", err
	}
	return normalized + "/v1/chat/completions", nil
}

func ModelsURL(base string) (string, error) {
	normalized, err := NormalizeBaseURL(base)
	if err != nil {
		return "", err
	}
	return normalized + "/v1/models", nil
}

func NewSecureHTTPClient(endpoint ActiveEndpoint) (*http.Client, error) {
	normalized, err := NormalizeBaseURL(endpoint.BaseURL)
	if err != nil {
		return nil, err
	}
	parsed, _ := url.Parse(normalized)
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	allowPrivate := isExplicitPrivateHost(host)
	if addr, parseErr := netip.ParseAddr(host); parseErr == nil {
		allowPrivate = addr.IsPrivate() || addr.IsLoopback()
	}
	resolver := netResolver{resolver: net.DefaultResolver}
	dialer := &net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		// Do not inherit HTTP(S)_PROXY. A proxy would move the actual destination
		// dial outside secureDialContext and bypass this module's DNS/IP validation.
		Proxy:                 nil,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: time.Duration(endpoint.TimeoutMS) * time.Millisecond,
		ExpectContinueTimeout: time.Second,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	transport.DialContext = secureDialContext(dialer, resolver, allowPrivate)
	timeout := time.Duration(endpoint.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = DefaultTimeoutMS * time.Millisecond
	}
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errRedirectBlocked
		},
	}, nil
}

func secureDialContext(dialer *net.Dialer, resolver DNSResolver, allowPrivate bool) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("prompt guard dial address invalid")
		}
		addresses, err := resolver.LookupNetIP(ctx, "ip", host)
		if err != nil || len(addresses) == 0 {
			return nil, fmt.Errorf("prompt guard dns unavailable")
		}
		var lastErr error
		for _, addr := range addresses {
			if isBlockedAddress(addr) || (!allowPrivate && (addr.IsPrivate() || addr.IsLoopback())) {
				lastErr = fmt.Errorf("prompt guard resolved address blocked")
				continue
			}
			if !addr.IsGlobalUnicast() && !addr.IsPrivate() && !addr.IsLoopback() {
				lastErr = fmt.Errorf("prompt guard resolved address blocked")
				continue
			}
			conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(addr.String(), port))
			if dialErr == nil {
				return conn, nil
			}
			lastErr = dialErr
		}
		if lastErr == nil {
			lastErr = fmt.Errorf("prompt guard no allowed resolved address")
		}
		return nil, lastErr
	}
}

func isExplicitPrivateHost(host string) bool {
	return host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local")
}

func isBlockedAddress(addr netip.Addr) bool {
	if !addr.IsValid() || addr.IsUnspecified() || addr.IsMulticast() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() {
		return true
	}
	for _, prefix := range blockedPrefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}
