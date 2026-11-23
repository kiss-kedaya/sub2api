package urlvalidator

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
)

// 管理面出站目标的统一校验。此前各入口（备份 S3 / 图床 / 风控 / 审计节点 /
// 上游渠道站点）各写一套：有的只做 url.Parse，有的看私网标志，有的在
// 注释里明确「允许私网」。口径分散本身就是 SSRF 入口的来源，这里收成一处。
//
// 校验分三层，缺一不可：
//  1. 语法与字面量：scheme/port 合法，字面量私网、localhost 直接拒绝；
//  2. 解析：域名必须解析到公网地址（DNS 失败/超时一律按拒绝处理）；
//  3. 连接与重定向：真正 connect 之前按「即将连接的那个 IP」再校验一次，
//     并禁止 3xx 跳到私网，堵住 DNS rebinding 与重定向绕过。
//
// 所有拒绝都返回 ErrOutboundTargetNotAllowed（文案固定），绝不回显解析结果：
// 否则「解析失败」与「解析到内网」的差异会变成内网存在性预言机。

// ErrOutboundTargetNotAllowed 表示目标未通过出站安全策略。
// 对外只给一句固定文案，细节不外露。
var ErrOutboundTargetNotAllowed = errors.New("outbound target is not allowed")

// OutboundPolicy 描述一次出站校验的放行范围。
type OutboundPolicy struct {
	// AllowPrivate 显式允许私网/环回目标。默认 false；只有管理员显式打开
	// 对应开关（如审计节点的 allow_private_endpoints）时才为 true。
	AllowPrivate bool
	// AllowInsecureHTTP 是否允许 http。与既有的
	// security.url_allowlist.allow_insecure_http 语义一致。
	AllowInsecureHTTP bool
}

const (
	outboundResolveTimeout = 5 * time.Second
	outboundDialTimeout    = 5 * time.Second
	outboundMaxRedirects   = 5
)

// resolveOutboundHost 是测试可替换的解析入口；生产默认用系统解析器。
//
// 它只能通过 SetResolveOutboundHostForTest 在测试里整体替换，替换是
// 进程级全局状态。测试用注入版（WithResolver）从下面 contextResolver
// 取值，不改这里，因此可继续 t.Parallel()。
var resolveOutboundHost = systemResolveOutboundHost

// systemResolveOutboundHost 是生产默认解析器（系统 DNS）。
func systemResolveOutboundHost(ctx context.Context, host string) ([]netip.Addr, error) {
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	addrs := make([]netip.Addr, 0, len(ips))
	for _, ip := range ips {
		addr, ok := netip.AddrFromSlice(ip)
		if !ok {
			return nil, errors.New("invalid resolved address")
		}
		addrs = append(addrs, addr.Unmap())
	}
	return addrs, nil
}

// resolverKey 是 context 中承载解析器的键（私有类型，不会被外部撞键）。
type resolverKey struct{}

// contextResolver 是「per-call 注入解析器」的入口，也是 resolveOutboundHost
// 实际执行的实现：context 里没有注入的解析器时退回系统解析器。
//
// 出站校验的三层（URL 校验、重定向守卫、拨号前校验）都走这里，测试只需
// 给 ctx 挂一个假解析器，就能锁死 DNS 结果，且不碰任何包级变量——这样
// 多个 t.Parallel() 用例、甚至不同包的测试并发跑也不会互相覆盖。
func contextResolver(ctx context.Context, host string) ([]netip.Addr, error) {
	if ctx != nil {
		if fn, ok := ctx.Value(resolverKey{}).(Resolver); ok && fn != nil {
			return fn(ctx, host)
		}
	}
	if fn := resolveOutboundHost; fn != nil {
		return fn(ctx, host)
	}
	return nil, errors.New("no resolver configured")
}

// Resolver 是出站校验使用的 DNS 解析函数签名。
type Resolver = func(ctx context.Context, host string) ([]netip.Addr, error)

// WithResolver 返回一个把解析器固定为 fn 的 context：本次调用链里所有出站
// 校验（含重定向守卫、拨号前校验）都按 fn 的结果判断，不读系统 DNS。
//
// 这是测试首选的注入方式（fn 为 nil 时视为未注入）。生产代码只应在明确
// 需要指定解析器时使用；语义等同于把出站地址判断交给调用方指定的来源。
func WithResolver(ctx context.Context, fn func(ctx context.Context, host string) ([]netip.Addr, error)) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, resolverKey{}, Resolver(fn))
}

// SetResolveOutboundHostForTest 仅供单元测试替换解析实现，返回还原函数。
//
// 注意：这是进程级全局状态，并行用例之间会互相覆盖（曾导致 CI 间歇失败）。
// 新测试请改用 WithResolver 做 per-call 注入；只有必须验证「默认解析器」行为
// 的用例才在这里替换，且这类用例不得 t.Parallel()。
func SetResolveOutboundHostForTest(fn func(ctx context.Context, host string) ([]netip.Addr, error)) func() {
	previous := resolveOutboundHost
	if fn != nil {
		resolveOutboundHost = fn
	}
	return func() { resolveOutboundHost = previous }
}

// ValidateOutboundURL 校验并规范化一个由管理员填写的出站 URL。
//
// 校验项：语法/端口（复用 ValidateHTTPURL）、字面量私网拒绝、DNS 解析后
// 每个地址都必须是公网。DNS 失败或超时按拒绝处理。
// 返回规范化后的 URL；失败时只返回 ErrOutboundTargetNotAllowed，
// 不带 host / IP / DNS 细节。
func ValidateOutboundURL(ctx context.Context, raw string, policy OutboundPolicy) (string, error) {
	normalized, err := ValidateHTTPURL(raw, policy.AllowInsecureHTTP, ValidationOptions{
		AllowPrivate: policy.AllowPrivate,
	})
	if err != nil {
		// 不把 urlvalidator 的原始错误（可能含 host）继续往上抛给响应层。
		return "", ErrOutboundTargetNotAllowed
	}
	if policy.AllowPrivate {
		return normalized, nil
	}
	parsed, err := url.Parse(normalized)
	if err != nil || parsed.Hostname() == "" {
		return "", ErrOutboundTargetNotAllowed
	}
	if err := validateResolvedHostAllowed(ctx, parsed.Hostname()); err != nil {
		return "", err
	}
	return normalized, nil
}

// ValidateOutboundHost 校验一个主机名（不含 scheme）：字面量私网拒绝，
// 域名解析后每个地址都必须是公网；DNS 失败/超时按拒绝处理。
func ValidateOutboundHost(ctx context.Context, host string, allowPrivate bool) error {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "" {
		return ErrOutboundTargetNotAllowed
	}
	if allowPrivate {
		return nil
	}
	if isBlockedHost(host) {
		return ErrOutboundTargetNotAllowed
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		// 字面量 IP 也走全量封禁清单（CGNAT / 保留段 / 文档段等），
		// 不能只看标准库的 IsPrivate/IsLoopback。
		if !IsPublicResolvedIP(ip) {
			return ErrOutboundTargetNotAllowed
		}
		return nil
	}
	return validateResolvedHostAllowed(ctx, host)
}

func validateResolvedHostAllowed(ctx context.Context, host string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, outboundResolveTimeout)
	defer cancel()
	addrs, err := contextResolver(ctx, host)
	if err != nil || len(addrs) == 0 {
		// 解析失败、超时、空结果一律拒绝，且不区分原因。
		return ErrOutboundTargetNotAllowed
	}
	for _, addr := range addrs {
		if !IsPublicResolvedIP(addr) {
			return ErrOutboundTargetNotAllowed
		}
	}
	return nil
}

// NewRedirectGuard 返回 http.Client.CheckRedirect 处理器：每一跳都重新做
// 字面量 + 解析校验，禁止跳到私网/环回，并限制最大跳数。
func NewRedirectGuard(policy OutboundPolicy) func(req *http.Request, via []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if len(via) >= outboundMaxRedirects {
			return ErrOutboundTargetNotAllowed
		}
		if req == nil || req.URL == nil {
			return ErrOutboundTargetNotAllowed
		}
		if policy.AllowPrivate {
			return nil
		}
		if err := ValidateOutboundHost(req.Context(), req.URL.Hostname(), false); err != nil {
			return err
		}
		return nil
	}
}

// HTTPClientOptions 是 NewSafeHTTPClient 的可选参数。
type HTTPClientOptions struct {
	// Timeout 请求总超时；TLSHandshakeTimeout / ResponseHeaderTimeout 可选。
	Timeout               time.Duration
	TLSHandshakeTimeout   time.Duration
	ResponseHeaderTimeout time.Duration
	MaxIdleConns          int
	MaxIdleConnsPerHost   int
	// ProxyURL 非空时沿用调用方的代理配置（由调用方负责代理本身的安全）。
	ProxyURL string
}

// NewSafeHTTPClient 构造一个对出站目标做强制校验的 http.Client：
// 连接前按「即将连接的 IP」校验（防 DNS rebinding），每一跳都校验（防重定向
// 到私网）。policy.AllowPrivate 为 true 时只保留重定向跳数限制，不做地址拦截。
func NewSafeHTTPClient(policy OutboundPolicy, opts HTTPClientOptions) *http.Client {
	dialer := &net.Dialer{Timeout: outboundDialTimeout}
	transport := &http.Transport{
		// 不继承 HTTP(S)_PROXY：代理会把真正的目的地移出本地校验范围。
		Proxy:                 nil,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          opts.MaxIdleConns,
		MaxIdleConnsPerHost:   opts.MaxIdleConnsPerHost,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   opts.TLSHandshakeTimeout,
		ResponseHeaderTimeout: opts.ResponseHeaderTimeout,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	if policy.AllowPrivate {
		transport.DialContext = dialer.DialContext
	} else {
		transport.DialContext = safeOutboundDialContext(dialer)
	}
	if opts.ProxyURL != "" {
		// 代理模式下无法在本地按最终 IP 校验，调用方需自行确认代理可信；
		// 这里仍保留重定向守卫。
		if proxyURL, err := url.Parse(opts.ProxyURL); err == nil && proxyURL.Host != "" {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	}
	return &http.Client{
		Transport:     transport,
		Timeout:       opts.Timeout,
		CheckRedirect: NewRedirectGuard(policy),
	}
}

// NewSafeDialContext 返回一个在 connect 前校验目标的拨号函数，
// 供使用自有 http.Transport 的调用方接入（防 DNS rebinding）。
func NewSafeDialContext(policy OutboundPolicy) func(ctx context.Context, network, addr string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: outboundDialTimeout}
	if policy.AllowPrivate {
		return dialer.DialContext
	}
	return safeOutboundDialContext(dialer)
}

// safeOutboundDialContext 在建立 TCP 连接前解析并校验地址，随后直接对
// 已校验的 IP 拨号，避免「校验一个地址、连接另一个地址」的 TOCTOU。
func safeOutboundDialContext(dialer *net.Dialer) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, ErrOutboundTargetNotAllowed
		}
		if IsBlockedHost(host) {
			return nil, ErrOutboundTargetNotAllowed
		}
		if ip, parseErr := netip.ParseAddr(host); parseErr == nil {
			if !IsPublicResolvedIP(ip) {
				return nil, ErrOutboundTargetNotAllowed
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		}
		ips, err := contextResolver(ctx, host)
		if err != nil || len(ips) == 0 {
			return nil, ErrOutboundTargetNotAllowed
		}
		var lastErr error
		for _, ip := range ips {
			if !IsPublicResolvedIP(ip) {
				return nil, ErrOutboundTargetNotAllowed
			}
		}
		for _, ip := range ips {
			conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if dialErr == nil {
				return conn, nil
			}
			lastErr = dialErr
		}
		if lastErr == nil {
			lastErr = ErrOutboundTargetNotAllowed
		}
		return nil, lastErr
	}
}

// RedirectGuardOnlyClient 返回一个只在重定向层做地址校验的客户端。
//
// 用于无法注入拨号逻辑、但必须堵住「公网端点 302 到内网」的调用方
// （例如 AWS S3 SDK：允许内网 MinIO 的部署仍然要能连，但公网端点把
// 请求重定向到私网永远不是合法行为）。
func RedirectGuardOnlyClient(transport http.RoundTripper, policy OutboundPolicy) *http.Client {
	return &http.Client{
		Transport:     transport,
		CheckRedirect: NewRedirectGuard(policy),
	}
}

// IsOutboundTargetRejected 判断错误是否来自出站安全策略，
// 调用方据此映射成固定的对外文案。
func IsOutboundTargetRejected(err error) bool {
	return errors.Is(err, ErrOutboundTargetNotAllowed)
}

// WrapOutboundRejection 把内部错误压成统一的固定文案错误；
// 非策略错误原样返回。
func WrapOutboundRejection(err error, fallback error) error {
	if err == nil {
		return nil
	}
	if IsOutboundTargetRejected(err) {
		return ErrOutboundTargetNotAllowed
	}
	if fallback != nil {
		return fmt.Errorf("%w: %v", fallback, err)
	}
	return err
}
