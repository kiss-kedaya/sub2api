package middleware

import (
	"context"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// Scanner-only traps. Real panel, gateway, payment return, canvas, and assets are not in this list.
var honeypotPrefixes = []string{
	"/.aws",
	"/.bzr",
	"/.dockerenv",
	"/.ds_store",
	"/.env",
	"/.git",
	"/.hg",
	"/.ssh",
	"/.svn",
	"/_ignition",
	"/actuator",
	"/adminer",
	"/api/install",
	"/autodiscover",
	"/boaform",
	"/cgi-bin",
	"/cfide",
	"/dana-na",
	"/debug/pprof",
	"/debug/vars",
	"/druid",
	"/ecp",
	"/elmah.axd",
	"/exchange",
	"/goform",
	"/gponform",
	"/heapdump",
	"/hnap1",
	"/horizon",
	"/id_rsa",
	"/invoker",
	"/jenkins",
	"/jmx-console",
	"/jndi",
	"/jolokia",
	"/manager/html",
	"/manager/text",
	"/myadmin",
	"/owa",
	"/phpmyadmin",
	"/pma",
	"/rdweb",
	"/remote/login",
	"/server-info",
	"/server-status",
	"/solr",
	"/sslvpn",
	"/telescope",
	"/trace.axd",
	"/v2/api-docs",
	"/v3/api-docs",
	"/vendor/phpunit",
	"/web-console",
	"/web.config",
	"/wp-admin",
	"/wp-config.php",
	"/wp-content",
	"/wp-includes",
	"/wp-login.php",
	"/xmlrpc.php",
	"/swagger",
	"/swagger-ui",
	"/swagger-ui.html",
}

var honeypotExts = []string{".php", ".asp", ".aspx", ".jsp", ".cgi", ".axd", ".sql"}

const (
	honeypotBanTTL       = 24 * time.Hour
	honeypotBanKeyPrefix = "kedaya:security:v1:ban:"
)

// honeypotExemptNets are the edge's own exempt CIDRs. A callback that shows
// up as one of these addresses must not be banned again.
var honeypotExemptNets = []netip.Prefix{
	netip.MustParsePrefix("51.222.42.218/32"),
	netip.MustParsePrefix("51.161.119.83/32"),
	netip.MustParsePrefix("2607:5300:203:7cda::/64"),
	netip.MustParsePrefix("2607:5300:203:7153::/64"),
}

func Honeypot(rdb *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request == nil || c.Request.URL == nil {
			c.Next()
			return
		}
		class := honeypotClass(c)
		if class == "" {
			c.Next()
			return
		}
		MarkIngressRejected(c, IngressRejectHoneypot)
		noteHoneypotBan(rdb, SecurityClientIP(c), class)
		c.Header("Cache-Control", "no-store")
		c.AbortWithStatus(http.StatusNotFound)
	}
}

func honeypotClass(c *gin.Context) string {
	path, rawPath, rawQuery := honeypotRequestParts(c)
	if honeypotTraversal(path) || honeypotTraversal(rawPath) || honeypotQuery(rawQuery) {
		return "scanner"
	}
	// SigV4 只在“本网关根本不存在的路径”上才算扫描（如把这里当 S3/aws 端点扫）。
	// 合法的 S3 预签名回链、任何正常 SigV4 调用方走真实 API 路径时，这里放行，
	// 由后续鉴权返回 401，不再直接 404 + 封 IP。honeypotPath 已覆盖 /.aws、/.env 等
	// 扫描器特征路径，所以拦得住的仍然是扫描器。
	if honeypotPath(path) {
		if honeypotSignature(c) {
			return "scanner"
		}
		if strings.HasSuffix(honeypotNorm(path), ".php") {
			return "unknown_php"
		}
		return "trap"
	}
	return ""
}

func noteHoneypotBan(rdb *redis.Client, ip, class string) {
	if rdb == nil {
		return
	}
	key, value, ok := honeypotBanSpec(ip, class)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	// NX keeps a longer edge ban intact. ponytail: fixed 24h, edge escalates on its own hits.
	_ = rdb.SetNX(ctx, key, value, honeypotBanTTL).Err()
}

func honeypotBanSpec(ip, class string) (key, value string, ok bool) {
	switch class {
	case "trap", "unknown_php", "scanner":
	default:
		return "", "", false
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return "", "", false
	}
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() || addr.IsPrivate() || honeypotExempt(addr) {
		return "", "", false
	}
	return honeypotBanKeyPrefix + addr.String(), class, true
}

func honeypotExempt(addr netip.Addr) bool {
	for _, prefix := range honeypotExemptNets {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func honeypotRequestParts(c *gin.Context) (path, rawPath, rawQuery string) {
	path = c.Request.URL.Path
	rawQuery = c.Request.URL.RawQuery
	rawPath = c.Request.RequestURI
	if i := strings.IndexByte(rawPath, '?'); i >= 0 {
		rawPath = rawPath[:i]
	}
	if c.Request.URL.RawPath != "" {
		rawPath = c.Request.URL.RawPath
	}
	return path, rawPath, rawQuery
}

func honeypotTraversal(s string) bool {
	if s == "" {
		return false
	}
	if strings.Contains(s, "..") || strings.Contains(s, "\x00") {
		return true
	}
	lower := strings.ToLower(s)
	return strings.Contains(lower, "%2e%2e") || strings.Contains(lower, "%00") || strings.Contains(lower, "%252e")
}

func honeypotQuery(raw string) bool {
	if raw == "" {
		return false
	}
	lower := strings.ToLower(raw)
	return strings.Contains(lower, "php://") ||
		strings.Contains(lower, "phar://") ||
		strings.Contains(lower, "expect://") ||
		strings.Contains(lower, "gopher://") ||
		strings.Contains(lower, "dict://") ||
		strings.Contains(lower, "xdebug_session") ||
		strings.Contains(lower, "auto_prepend_file") ||
		strings.Contains(lower, "allow_url_include") ||
		strings.Contains(lower, "auto_append_file")
	// 注意：x-amz-* 不再在这里判。真实路径上的 S3 预签名回链会带这些参数，
	// 误判就会封正常调用方的 IP。SigV4 的判定收在 honeypotSignature，只在
	// 与 honeypotPath 命中的扫描器路径一起出现时才升级为 scanner。
}

// honeypotSignature 报告请求是否自称 Cos/S3 端点（SigV4）：Authorization 头
// 是 aws4-hmac-sha256 / aws，或带了 X-Amz-Algorithm / X-Amz-Credential（头或 query）。
// 本网关是 API 入口，不是 S3 端点，所以这本身不是罪证，调用方要配合路径一起判。
func honeypotSignature(c *gin.Context) bool {
	if c == nil {
		return false
	}
	auth := strings.ToLower(strings.TrimSpace(c.GetHeader("Authorization")))
	if strings.HasPrefix(auth, "aws4-hmac-sha256") || strings.HasPrefix(auth, "aws ") {
		return true
	}
	if c.GetHeader("X-Amz-Algorithm") != "" || c.GetHeader("X-Amz-Credential") != "" {
		return true
	}
	if c.Request != nil && c.Request.URL != nil {
		raw := strings.ToLower(c.Request.URL.RawQuery)
		return strings.Contains(raw, "x-amz-algorithm") || strings.Contains(raw, "x-amz-credential")
	}
	return false
}

func honeypotPath(path string) bool {
	p := honeypotNorm(path)
	if p == "" || p == "/" {
		return false
	}
	for _, prefix := range honeypotPrefixes {
		if p == prefix || strings.HasPrefix(p, prefix+"/") || strings.HasPrefix(p, prefix+".") {
			return true
		}
	}
	base := p
	if i := strings.LastIndex(p, "/"); i >= 0 {
		base = p[i+1:]
	}
	for _, ext := range honeypotExts {
		if strings.HasSuffix(base, ext) {
			return true
		}
	}
	return false
}

func honeypotNorm(path string) string {
	p := strings.ToLower(strings.TrimSpace(path))
	if p == "" {
		return ""
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	for strings.Contains(p, "//") {
		p = strings.ReplaceAll(p, "//", "/")
	}
	return strings.TrimRight(p, "/")
}
