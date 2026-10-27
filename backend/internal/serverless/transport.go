package serverless

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

func blockedHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	return host == "metadata" || host == "instance-data" || host == "metadata.google.internal" || host == "metadata.goog" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".internal")
}
func permanentlyBlocked(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.Zone() != "" || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	for _, raw := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "198.18.0.0/15", "240.0.0.0/4", "64:ff9b::/96", "64:ff9b:1::/48", "2002::/16", "2001::/32"} {
		if netip.MustParsePrefix(raw).Contains(ip) {
			return true
		}
	}
	return !ip.IsGlobalUnicast() && !ip.IsLoopback()
}
func (m *Manager) allowIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if permanentlyBlocked(ip) {
		return false
	}
	if !ip.IsPrivate() && !ip.IsLoopback() {
		return true
	}
	for _, raw := range m.runtime.TrustedPrivateCIDRs {
		if netip.MustParsePrefix(raw).Contains(ip) {
			return true
		}
	}
	return false
}
func (m *Manager) validateTarget(raw string) error {
	if err := ValidateEndpoint(raw); err != nil {
		return err
	}
	u, _ := url.Parse(raw)
	if ip, e := netip.ParseAddr(u.Hostname()); e == nil && !m.allowIP(ip) {
		return errors.New("Pod address is not trusted")
	}
	return nil
}
func (m *Manager) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || blockedHost(host) {
		return nil, errors.New("Pod connection rejected")
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(addresses) == 0 {
		return nil, errors.New("Pod address unavailable")
	}
	// Reject the entire set on a mixed safe/unsafe DNS result. Dial the vetted
	// literal directly so no second resolver can rebind after validation.
	for _, ip := range addresses {
		if !m.allowIP(ip) {
			return nil, errors.New("Pod connection rejected")
		}
	}
	dialer := net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}
	for _, ip := range addresses {
		conn, e := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if e == nil {
			return conn, nil
		}
		err = e
	}
	return nil, err
}
