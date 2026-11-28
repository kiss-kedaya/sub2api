package securityaudit

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/urljoin"
	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
)

const maxGuardResponseBytes int64 = 256 * 1024

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
	host := strings.TrimSpace(parsed.Hostname())
	if host == "" {
		return "", infraerrors.BadRequest("prompt_audit_invalid_base_url", "审计节点地址无效")
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
	return urljoin.APIPath(normalized, "/v1/chat/completions"), nil
}

func ModelsURL(base string) (string, error) {
	normalized, err := NormalizeBaseURL(base)
	if err != nil {
		return "", err
	}
	return urljoin.APIPath(normalized, "/v1/models"), nil
}

func NewSecureHTTPClient(endpoint ActiveEndpoint) (*http.Client, error) {
	_, err := NormalizeBaseURL(endpoint.BaseURL)
	if err != nil {
		return nil, err
	}
	policy := urlvalidator.OutboundPolicy{
		AllowPrivate:      endpoint.AllowPrivateEndpoints,
		AllowInsecureHTTP: true,
	}
	if !endpoint.AllowPrivateEndpoints {
		// 字面量私网/环回/云元数据地址在建立客户端前就拒绝，让探测返回
		// endpoint_unsafe 而不是模糊的"连接失败"；解析到私网的兜底由拨号期校验负责。
		parsed, parseErr := url.Parse(endpoint.BaseURL)
		if parseErr == nil && urlvalidator.IsBlockedHost(parsed.Hostname()) {
			return nil, urlvalidator.ErrOutboundTargetNotAllowed
		}
	}
	// 默认只允许公网：连接前按解析后的 IP 校验（防 DNS rebinding），
	// 每一跳重定向都重新校验（禁止 302 到私网）。显式打开
	// allow_private_endpoints 时才放行私网目标。
	client := urlvalidator.NewSafeHTTPClient(policy, urlvalidator.HTTPClientOptions{
		Timeout:               time.Duration(endpoint.TimeoutMS) * time.Millisecond,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: time.Duration(endpoint.TimeoutMS) * time.Millisecond,
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   16,
	})
	if client.Timeout <= 0 {
		client.Timeout = DefaultTimeoutMS * time.Millisecond
	}
	// Apply HTTPS->HTTP downgrade protection uniformly to both Probe (/v1/models)
	// and OpenAICompatibleScanner (/v1/chat/completions) so bearer tokens are
	// never forwarded over plaintext after an HTTPS redirect.
	client.CheckRedirect = promptProbeRedirectGuard(client.CheckRedirect)
	return client, nil
}

// promptProbeRedirectGuard wraps the base SSRF redirect guard to also forbid
// HTTPS->HTTP scheme downgrades and non-HTTP(S) schemes on guard redirects.
func promptProbeRedirectGuard(base func(req *http.Request, via []*http.Request) error) func(req *http.Request, via []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if base != nil {
			if err := base(req, via); err != nil {
				return err
			}
		}
		if req == nil || req.URL == nil {
			return urlvalidator.ErrOutboundTargetNotAllowed
		}
		scheme := strings.ToLower(strings.TrimSpace(req.URL.Scheme))
		if scheme != "http" && scheme != "https" {
			return urlvalidator.ErrOutboundTargetNotAllowed
		}
		if len(via) > 0 && via[0] != nil && via[0].URL != nil &&
			strings.EqualFold(via[0].URL.Scheme, "https") && scheme != "https" {
			return urlvalidator.ErrOutboundTargetNotAllowed
		}
		return nil
	}
}

// validateEndpointHostsAllowed 在保存配置时拒绝私网/环回字面量地址。
// 只做字面量判断（不到 DNS），解析到私网的拦截由运行期连接前的校验负责；
// allowPrivate 为 true（管理员显式打开开关）时全部放行。
func validateEndpointHostsAllowed(endpoints []StorageEndpoint, allowPrivate bool) error {
	if allowPrivate {
		return nil
	}
	for _, endpoint := range endpoints {
		parsed, err := url.Parse(strings.TrimSpace(endpoint.BaseURL))
		if err != nil || parsed.Hostname() == "" {
			return infraerrors.BadRequest("prompt_audit_invalid_base_url", "审计节点地址无效")
		}
		if urlvalidator.IsBlockedHost(parsed.Hostname()) {
			return infraerrors.BadRequest("prompt_audit_endpoint_private_blocked",
				"审计节点地址不允许指向私网/环回地址；如确需内网部署，请显式打开 allow_private_endpoints")
		}
	}
	return nil
}
