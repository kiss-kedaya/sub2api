// Adapted from ranxi2001/sub2api @ 9620d315, LGPL-3.0-or-later.
package urlvalidator

import (
	"errors"
	"net/netip"
	"net/url"
	"strings"
)

// ValidateHTTPSOrigin is syntax-only and performs no DNS/network I/O. Callers
// making outbound requests must still validate and pin resolved addresses.
// Errors never echo potentially credential-bearing input.
func ValidateHTTPSOrigin(raw string) error {
	invalid := errors.New("HTTPS origin must use a public host and valid port without credentials, path, query or fragment")
	if raw != strings.TrimSpace(raw) {
		return invalid
	}
	if _, err := ValidateURLFormat(raw, false); err != nil {
		return invalid
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Opaque != "" ||
		u.RawQuery != "" || u.ForceQuery || strings.Contains(raw, "#") || strings.HasSuffix(u.Host, ":") ||
		u.RawPath != "" || (u.Path != "" && u.Path != "/") {
		return invalid
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") || strings.Contains(host, "%") {
		return invalid
	}
	if ip, err := netip.ParseAddr(host); err == nil && !IsPublicResolvedIP(ip) {
		return invalid
	}
	return nil
}
