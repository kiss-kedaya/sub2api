package service

import (
	"context"
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
//   - allowlist disabled (or cfg nil): scheme/format check plus outbound target
//     validation — private/loopback literals and host names that resolve to
//     private, loopback, CGNAT, link-local or cloud-metadata addresses are
//     refused. DNS failure/timeout is a rejection too. Private targets are only
//     accepted when UpstreamChannelAllowPrivateHosts is explicitly set.
//
// subject names the thing being checked in error messages ("probe", "upstream
// site", ...). The returned error text never contains the resolved address.
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
	allowInsecureHTTP := false
	allowPrivate := false
	if cfg != nil {
		allowInsecureHTTP = cfg.Security.URLAllowlist.AllowInsecureHTTP
		allowPrivate = cfg.Security.URLAllowlist.UpstreamChannelAllowPrivateHosts
	}
	normalized, err := urlvalidator.ValidateOutboundURL(context.Background(), trimmed, urlvalidator.OutboundPolicy{
		AllowPrivate:      allowPrivate,
		AllowInsecureHTTP: allowInsecureHTTP,
	})
	if err != nil {
		// 固定文案：不区分 DNS 失败 / 解析到私网 / 字面量私网，避免把
		// 保存入口变成内网存在性预言机。
		return "", fmt.Errorf("%s target rejected by URL security policy", subject)
	}
	return normalized, nil
}
