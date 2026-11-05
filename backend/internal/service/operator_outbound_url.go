package service

import (
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
)

// ValidateOperatorOutboundURL vets an address an administrator typed in, before
// the server sends a request to it. It applies the operator's global outbound
// URL policy and returns the normalized URL:
//
//   - allowlist enabled: https only, host must be on UpstreamHosts, private and
//     loopback literals are refused unless AllowPrivateHosts is set;
//   - allowlist disabled (or cfg nil): format check only (scheme, host, port);
//     plain http is accepted only when AllowInsecureHTTP is set.
//
// subject names the thing being checked in error messages ("probe", "upstream
// site", ...). A literal address is checked here; a host name that later
// resolves to a private address is not, that needs the resolved-IP check at
// connection time.
func ValidateOperatorOutboundURL(cfg *config.Config, subject, raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("%s url is required", subject)
	}
	if cfg != nil && cfg.Security.URLAllowlist.Enabled {
		normalized, err := urlvalidator.ValidateHTTPSURL(trimmed, urlvalidator.ValidationOptions{
			AllowedHosts:     cfg.Security.URLAllowlist.UpstreamHosts,
			RequireAllowlist: true,
			AllowPrivate:     cfg.Security.URLAllowlist.AllowPrivateHosts,
		})
		if err != nil {
			return "", fmt.Errorf("%s target rejected by URL security policy: %w", subject, err)
		}
		return normalized, nil
	}
	var allowInsecureHTTP bool
	if cfg != nil {
		allowInsecureHTTP = cfg.Security.URLAllowlist.AllowInsecureHTTP
	}
	normalized, err := urlvalidator.ValidateURLFormat(trimmed, allowInsecureHTTP)
	if err != nil {
		return "", fmt.Errorf("%s target rejected by URL security policy: %w", subject, err)
	}
	return normalized, nil
}
