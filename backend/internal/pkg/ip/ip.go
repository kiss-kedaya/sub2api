// Package ip 提供客户端 IP 地址提取工具。
package ip

import (
	"net"
	"net/netip"
	"strings"

	"github.com/gin-gonic/gin"
)

const forwardedIPSettingsKey = "sub2api.forwarded_ip_settings"

type forwardedIPSettings struct {
	trustForwarded bool
	headers        []string
	// trustedProxies 是 server.trusted_proxies 的解析快照。只有直连对端命中
	// 这些网段时，原始转发头才被采信；否则一律使用 TCP 对端地址。
	trustedProxies []netip.Prefix
}

// SetForwardedIPSettings snapshots the forwarded-IP mode, custom header list and
// trusted proxy networks for this request.
func SetForwardedIPSettings(c *gin.Context, enabled bool, headers, trustedProxies []string) {
	if c == nil {
		return
	}
	c.Set(forwardedIPSettingsKey, forwardedIPSettings{
		trustForwarded: enabled,
		headers:        append([]string(nil), headers...),
		trustedProxies: parseTrustedProxyPrefixes(trustedProxies),
	})
}

// SetLegacyForwardedIPTrust records whether raw forwarding headers may be used
// for this request. Headers remain conditional on the TCP peer being a
// configured trusted proxy; an empty trusted proxy list never trusts headers.
func SetLegacyForwardedIPTrust(c *gin.Context, enabled bool) {
	SetForwardedIPSettings(c, enabled, nil, nil)
}

// parseTrustedProxyPrefixes 解析 server.trusted_proxies，非法项忽略。
func parseTrustedProxyPrefixes(values []string) []netip.Prefix {
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
			if prefix, err := netip.ParsePrefix(raw); err == nil {
				out = append(out, prefix.Masked())
			}
			continue
		}
		if addr, err := netip.ParseAddr(raw); err == nil {
			addr = addr.Unmap()
			out = append(out, netip.PrefixFrom(addr, addr.BitLen()))
		}
	}
	return out
}

// peerAddr 返回 TCP 对端地址（不含端口）。
func peerAddr(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	return normalizeIP(c.Request.RemoteAddr)
}

// engineTrustedProxy 报告 addr 是否命中可信代理网段。
func engineTrustedProxy(addr string, prefixes []netip.Prefix) bool {
	parsed, err := netip.ParseAddr(strings.TrimSpace(addr))
	if err != nil {
		return false
	}
	parsed = parsed.Unmap()
	for _, prefix := range prefixes {
		if prefix.Contains(parsed) {
			return true
		}
	}
	return false
}

func requestForwardedIPSettings(c *gin.Context) (forwardedIPSettings, bool) {
	if c == nil {
		return forwardedIPSettings{}, false
	}
	value, ok := c.Get(forwardedIPSettingsKey)
	if !ok {
		return forwardedIPSettings{}, false
	}
	settings, ok := value.(forwardedIPSettings)
	return settings, ok
}

func requestUsesLegacyForwardedIPTrust(c *gin.Context) bool {
	settings, ok := requestForwardedIPSettings(c)
	return !ok || settings.trustForwarded
}

// GetClientIP resolves the client address. In legacy forwarded-header mode the
// headers are only trusted when the TCP peer is one of server.trusted_proxies:
// a direct peer controls every forwarding header, so such requests always fall
// back to the TCP peer address. When the peer is trusted, X-Forwarded-For is
// walked right-to-left, skipping trusted proxy hops, so client-supplied values
// on the left cannot win. Custom operator-configured headers keep precedence.
func GetClientIP(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if verified := verifiedGatewayIP(c); verified != "" {
		return verified
	}
	if !requestUsesLegacyForwardedIPTrust(c) {
		return GetTrustedClientIP(c)
	}
	settings, _ := requestForwardedIPSettings(c)
	peer := peerAddr(c)
	if !engineTrustedProxy(peer, settings.trustedProxies) {
		// 直连对端不可信：忽略所有转发头，使用真实 TCP 对端。
		return peer
	}

	customIP, customFallback := resolveCustomForwardedClientIP(c, settings.headers)
	if customIP != "" {
		return customIP
	}
	if legacyIP := resolveLegacyForwardedHeaderIP(c, settings.trustedProxies); legacyIP != "" {
		return legacyIP
	}
	if customFallback != "" {
		return customFallback
	}
	return peer
}

func resolveCustomForwardedClientIP(c *gin.Context, headers []string) (string, string) {
	if c == nil {
		return "", ""
	}
	var fallback string
	for _, header := range headers {
		for _, value := range c.Request.Header.Values(header) {
			for _, candidate := range strings.Split(value, ",") {
				parsed := net.ParseIP(strings.TrimSpace(candidate))
				if parsed == nil {
					continue
				}
				normalized := parsed.String()
				if isPrivateIP(normalized) {
					if fallback == "" {
						fallback = normalized
					}
					continue
				}
				return normalized, fallback
			}
		}
	}
	return "", fallback
}

// resolveLegacyForwardedHeaderIP 只在直连对端已通过可信代理校验后被调用。
// X-Forwarded-For 从右往左解析：跳过可信代理自身追加的跳，第一个非可信地址
// 才是真正的客户端；客户端自带的左侧值永远不会被选中。只有 XFF 无法解析时
// 才回退到 X-Real-IP / CF-Connecting-IP（两者都是单值头部，边缘必须覆盖写入）。
func resolveLegacyForwardedHeaderIP(c *gin.Context, trustedProxies []netip.Prefix) string {
	if clientIP := rightmostForwardedForClientIP(c.GetHeader("X-Forwarded-For"), trustedProxies); clientIP != "" {
		return clientIP
	}
	if realIP := normalizeValidIP(c.GetHeader("X-Real-IP")); realIP != "" {
		return realIP
	}
	if forwarded := normalizeValidIP(c.GetHeader("CF-Connecting-IP")); forwarded != "" {
		return forwarded
	}
	return ""
}

// rightmostForwardedForClientIP walks an X-Forwarded-For chain from right to
// left, skipping addresses that belong to configured trusted proxies, and
// returns the first remaining address. The left-most entries are client
// controlled, so they never take precedence.
func rightmostForwardedForClientIP(raw string, trustedProxies []netip.Prefix) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	parts := strings.Split(raw, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		candidate := normalizeValidIP(parts[i])
		if candidate == "" {
			continue
		}
		if engineTrustedProxy(candidate, trustedProxies) {
			continue
		}
		return candidate
	}
	return ""
}

// GetTrustedClientIP 从 Gin 的可信代理解析链提取客户端 IP。
// 该方法依赖 gin.Engine.SetTrustedProxies 配置，不会优先直接信任原始转发头值。
// 适用于 ACL / 风控等安全敏感场景。
func GetTrustedClientIP(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if verified := verifiedGatewayIP(c); verified != "" {
		return verified
	}
	return normalizeIP(c.ClientIP())
}

// GetSecurityClientIP returns the address used by security-sensitive paths.
// When legacy forwarded-IP trust is enabled, raw forwarding headers are only
// consulted for requests whose TCP peer is a configured trusted proxy
// (server.trusted_proxies); otherwise the TCP peer address is authoritative.
// When disabled, Gin's server.trusted_proxies chain is authoritative.
func GetSecurityClientIP(c *gin.Context, trustForwarded bool) string {
	if verified := verifiedGatewayIP(c); verified != "" {
		return verified
	}
	if requestSettings, ok := requestForwardedIPSettings(c); ok {
		trustForwarded = requestSettings.trustForwarded
	}
	if trustForwarded {
		return GetClientIP(c)
	}
	return GetTrustedClientIP(c)
}

// normalizeIP 规范化 IP 地址，去除端口号和空格。
func normalizeIP(ip string) string {
	ip = strings.TrimSpace(ip)
	// 移除端口号（如 "192.168.1.1:8080" -> "192.168.1.1"）
	if host, _, err := net.SplitHostPort(ip); err == nil {
		return host
	}
	return ip
}

// normalizeValidIP 规范化并验证代理头中的候选值，避免把 unknown、主机名等非法值传给安全服务。
func normalizeValidIP(value string) string {
	normalized := normalizeIP(value)
	parsed := net.ParseIP(normalized)
	if parsed == nil {
		return ""
	}
	return parsed.String()
}

// privateNets contains the private/loopback ranges skipped while selecting a
// public address from a legacy X-Forwarded-For chain.
var privateNets []*net.IPNet

func init() {
	for _, cidr := range []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"127.0.0.0/8",
		"::1/128",
		"fc00::/7",
	} {
		_, block, err := net.ParseCIDR(cidr)
		if err != nil {
			panic("invalid CIDR: " + cidr)
		}
		privateNets = append(privateNets, block)
	}
}

// CompiledIPRules 表示预编译的 IP 匹配规则。
// PatternCount 记录原始规则数量，用于保留“规则存在但全无效”时的行为语义。
type CompiledIPRules struct {
	CIDRs        []*net.IPNet
	IPs          []net.IP
	PatternCount int
}

// CompileIPRules 将 IP/CIDR 字符串规则预编译为可复用结构。
// 非法规则会被忽略，但 PatternCount 会保留原始规则条数。
func CompileIPRules(patterns []string) *CompiledIPRules {
	compiled := &CompiledIPRules{
		CIDRs:        make([]*net.IPNet, 0, len(patterns)),
		IPs:          make([]net.IP, 0, len(patterns)),
		PatternCount: len(patterns),
	}
	for _, pattern := range patterns {
		normalized := strings.TrimSpace(pattern)
		if normalized == "" {
			continue
		}
		if strings.Contains(normalized, "/") {
			_, cidr, err := net.ParseCIDR(normalized)
			if err != nil || cidr == nil {
				continue
			}
			compiled.CIDRs = append(compiled.CIDRs, cidr)
			continue
		}
		parsedIP := net.ParseIP(normalized)
		if parsedIP == nil {
			continue
		}
		compiled.IPs = append(compiled.IPs, parsedIP)
	}
	return compiled
}

func matchesCompiledRules(parsedIP net.IP, rules *CompiledIPRules) bool {
	if parsedIP == nil || rules == nil {
		return false
	}
	for _, cidr := range rules.CIDRs {
		if cidr.Contains(parsedIP) {
			return true
		}
	}
	for _, ruleIP := range rules.IPs {
		if parsedIP.Equal(ruleIP) {
			return true
		}
	}
	return false
}

func isPrivateIP(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	for _, block := range privateNets {
		if block.Contains(ip) {
			return true
		}
	}
	return false
}

// MatchesPattern 检查 IP 是否匹配指定的模式（支持单个 IP 或 CIDR）。
// pattern 可以是：
// - 单个 IP: "192.168.1.100"
// - CIDR 范围: "192.168.1.0/24"
func MatchesPattern(clientIP, pattern string) bool {
	ip := net.ParseIP(clientIP)
	if ip == nil {
		return false
	}

	// 尝试解析为 CIDR
	if strings.Contains(pattern, "/") {
		_, cidr, err := net.ParseCIDR(pattern)
		if err != nil {
			return false
		}
		return cidr.Contains(ip)
	}

	// 作为单个 IP 处理
	patternIP := net.ParseIP(pattern)
	if patternIP == nil {
		return false
	}
	return ip.Equal(patternIP)
}

// MatchesAnyPattern 检查 IP 是否匹配任意一个模式。
func MatchesAnyPattern(clientIP string, patterns []string) bool {
	for _, pattern := range patterns {
		if MatchesPattern(clientIP, pattern) {
			return true
		}
	}
	return false
}

// CheckIPRestriction 检查 IP 是否被 API Key 的 IP 限制允许。
// 返回值：(是否允许, 拒绝原因)
// 逻辑：
// 1. 先检查黑名单，如果在黑名单中则直接拒绝
// 2. 如果白名单不为空，IP 必须在白名单中
// 3. 如果白名单为空，允许访问（除非被黑名单拒绝）
func CheckIPRestriction(clientIP string, whitelist, blacklist []string) (bool, string) {
	return CheckIPRestrictionWithCompiledRules(
		clientIP,
		CompileIPRules(whitelist),
		CompileIPRules(blacklist),
	)
}

// CheckIPRestrictionWithCompiledRules 使用预编译规则检查 IP 是否允许访问。
func CheckIPRestrictionWithCompiledRules(clientIP string, whitelist, blacklist *CompiledIPRules) (bool, string) {
	// 规范化 IP
	clientIP = normalizeIP(clientIP)
	if clientIP == "" {
		return false, "access denied"
	}
	parsedIP := net.ParseIP(clientIP)
	if parsedIP == nil {
		return false, "access denied"
	}

	// 1. 检查黑名单
	if blacklist != nil && blacklist.PatternCount > 0 && matchesCompiledRules(parsedIP, blacklist) {
		return false, "access denied"
	}

	// 2. 检查白名单（如果设置了白名单，IP 必须在其中）
	if whitelist != nil && whitelist.PatternCount > 0 && !matchesCompiledRules(parsedIP, whitelist) {
		return false, "access denied"
	}

	return true, ""
}

// ValidateIPPattern 验证 IP 或 CIDR 格式是否有效。
func ValidateIPPattern(pattern string) bool {
	if strings.Contains(pattern, "/") {
		_, _, err := net.ParseCIDR(pattern)
		return err == nil
	}
	return net.ParseIP(pattern) != nil
}

// ValidateIPPatterns 验证多个 IP 或 CIDR 格式。
// 返回无效的模式列表。
func ValidateIPPatterns(patterns []string) []string {
	var invalid []string
	for _, p := range patterns {
		if !ValidateIPPattern(p) {
			invalid = append(invalid, p)
		}
	}
	return invalid
}
