package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// 本文件在 gin 路由匹配之前规范化网关协议路径，容忍客户端把 Base URL 填错。
// 路由表推导（受支持前缀、版本段位置、模板集合）与匹配逻辑在 gateway_path_index.go，
// “手写版本片段”的折叠/剥离在 gateway_path_strip.go。
//
//  1. 重复版本段折叠：任意受支持前缀（根、/antigravity、/backend-api/codex …）之后
//     连续重复的同一族版本段折叠成一个：
//     /v1/v1/messages、/v1/v1/v1/messages、/antigravity/v1/v1/messages、
//     /antigravity/v1beta/v1beta/models …；不同族（/v1/v1beta）绝不折叠，
//     /v1beta 永远不会变成 /v1。
//  2. 无版本段前缀族剥离任意个版本段：/backend-api/codex 的真实路由本身不带版本段
//     （/backend-api/codex/responses），所以该族里客户端手写的版本段（一个或多个、
//     v1 或 v1beta）全部剥掉：/backend-api/codex/v1/responses、
//     /backend-api/codex/v1/v1/responses、/backend-api/codex/v1beta/responses、
//     /backend-api/codex/v1/v1/v1/responses → /backend-api/codex/responses。
//  3. 缺版本段补齐：根前缀与各前缀之后漏写版本段的路径补上，且必须真实命中路由：
//     /messages → /v1/messages、/chat/completions → /v1/chat/completions、
//     /antigravity/messages → /antigravity/v1/messages。带版本段前缀族（/antigravity）
//     的补版本段是保守的：只有**原路径完全未命中任何路由**、且补一个版本段后
//     **唯一命中一条模板**时才补（/antigravity/models/xxx → 唯一命中
//     /antigravity/v1beta/models/:model）；命中多条或零条一律保持原样 404。
//
// 规范写法（/v1/messages、/v1beta/models、/antigravity/v1beta/models、
// /backend-api/codex/responses）一个字节都不改；真实路由（/antigravity/models）也
// 一个字节都不改；/api/v1/*、/admin/*、静态资源、/health、SPA 页面（/usage、
// /dashboard …）同样不会被改写。
//
// 规范化只修改 URL.Path，query string、header、body、HTTP method 全部保持原样；
// 重写之后仍然交给同一个 gin 路由 + 同一套中间件链处理，因此鉴权、限流、计费、
// 访问日志与直接请求规范路径完全一致，不需要第二套路由或第二套中间件。
//
// 只有规范化结果确实命中已注册的网关路由（method 也要一致）时才会重写，
// 所以 /api/v1/*、/admin/*、/assets/* 等静态资源、/health 探针和 SPA 页面路由
// 不会被误伤。

// gatewayVersionSegmentPrefix 是协议版本段的公共前缀（匹配 v1 / v1beta …，不含 v2）。
const gatewayVersionSegmentPrefix = "v1"

// PathAliasRule 描述一次网关路径规范化采用的规则。
type PathAliasRule string

const (
	// PathAliasRuleDuplicateVersionPrefix 表示路径里重复写了同一族版本段
	// （/v1/v1/...、/antigravity/v1beta/v1beta/...），已折叠成一个。
	PathAliasRuleDuplicateVersionPrefix PathAliasRule = "duplicate_version_prefix"
	// PathAliasRuleMissingVersionPrefix 表示请求没有带 /v1（/messages），已补齐到 /v1 下。
	PathAliasRuleMissingVersionPrefix PathAliasRule = "missing_version_prefix"
)

// gatewayBarePathExclusions 定义不参与"漏写 /v1 自动补齐"的网关路径。
//
// /v1/usage 与浏览器端 SPA 的 /usage 页面同路径：浏览器访问 /usage 必须继续拿到页面，
// 因此裸 /usage 不自动补前缀（显式 /v1/usage、/v1/v1/usage 仍然正常工作）。
// /v1/sub2api/* 是后台/浏览器读取 Key 倍率配置的接口，不属于客户端 SDK 协议根路径，
// 同样不开裸路径别名。
var gatewayBarePathExclusions = []string{
	"/v1/usage",
	"/v1/sub2api/",
}

// gatewayPathAliasInfo 记录一次路径规范化的结果，供 access log 补充原始路径，
// 便于统计到底有多少用户把 Base URL 填错、以及填错的方式。
type gatewayPathAliasInfo struct {
	Original          string
	Normalized        string
	Rule              PathAliasRule
	Prefix            string
	Version           string
	DuplicatePrefixes int
	// VersionlessPrefix 为 true 表示这次是把“该前缀族不存在版本段”的多写版本段
	// 整段剥掉了（/backend-api/codex/v1/v1/responses → /backend-api/codex/responses），
	// 而不是折叠成单个版本段（/antigravity/v1/v1/messages → /antigravity/v1/messages）。
	VersionlessPrefix bool
}

type gatewayPathAliasContextKey struct{}

func withGatewayPathAliasInfo(ctx context.Context, info gatewayPathAliasInfo) context.Context {
	return context.WithValue(ctx, gatewayPathAliasContextKey{}, info)
}

// gatewayPathDiagnosticContextKey 标记“折叠后仍不存在”的请求：别名层只在 context 里留下
// 诊断信息，请求原样交给 gin，404 JSON 由 gin 的 404 链末端输出（见 gatewayPathNotFoundDiagnostic）。
type gatewayPathDiagnosticContextKey struct{}

func withGatewayPathDiagnostic(ctx context.Context, info gatewayPathAliasInfo) context.Context {
	return context.WithValue(ctx, gatewayPathDiagnosticContextKey{}, info)
}

func gatewayPathDiagnosticFromContext(ctx context.Context) (gatewayPathAliasInfo, bool) {
	if ctx == nil {
		return gatewayPathAliasInfo{}, false
	}
	info, ok := ctx.Value(gatewayPathDiagnosticContextKey{}).(gatewayPathAliasInfo)
	return info, ok
}

// gatewayPathAliasInfoFromContext 读取路径规范化信息，由 Logger() 写入访问日志。
func gatewayPathAliasInfoFromContext(ctx context.Context) (gatewayPathAliasInfo, bool) {
	if ctx == nil {
		return gatewayPathAliasInfo{}, false
	}
	info, ok := ctx.Value(gatewayPathAliasContextKey{}).(gatewayPathAliasInfo)
	return info, ok
}

// isGatewayVersionSegment 判断一个路径段是不是协议版本段（v1、v1beta、v1alpha、v1/v1）。
// 只认小写，且必须以 v1 开头："v12"、"v2"、"v1x" 都不算，避免把模型名或
// 业务段当成版本段去折叠。
func isGatewayVersionSegment(segment string) bool {
	if !strings.HasPrefix(segment, gatewayVersionSegmentPrefix) {
		return false
	}
	for _, ch := range segment[len(gatewayVersionSegmentPrefix):] {
		if ch < 'a' || ch > 'z' {
			return false
		}
	}
	return true
}

func isExcludedBareGatewayPath(path string) bool {
	for _, excluded := range gatewayBarePathExclusions {
		if path == excluded || strings.HasPrefix(path, excluded) {
			return true
		}
	}
	return false
}

// GatewayPathAlias 包装 gin 引擎，在路由匹配之前规范化网关协议路径。
//
// 返回的 http.Handler 应作为 HTTP server 的最终 handler 使用（见 ProvideHTTPServer），
// 这样规范化发生在 gin 路由之前，重写后的请求仍由原来的网关路由与中间件链处理。
//
// router 必须已经注册完所有路由（ProvideHTTPServer 在 registerRoutes 之后才被调用）；
// 索引在启动时算一次即可。
//
// 这里会给 router 追加一个全局中间件（gatewayPathNotFoundDiagnostic）。gin 的路由 handler 链
// 在注册路由那一刻就定型，所以路由注册完之后再 Use 的中间件只会进入 404/405 链：命中路由的请求
// 完全不受影响，未命中的请求先走完前面的全局中间件再到它。
func GatewayPathAlias(router *gin.Engine) http.Handler {
	var next http.Handler
	if router != nil {
		next = router
		router.Use(gatewayPathNotFoundDiagnostic)
	} else {
		next = http.NotFoundHandler()
	}
	return &gatewayPathAliasHandler{
		next:  next,
		index: newGatewayRouteIndex(router),
	}
}

type gatewayPathAliasHandler struct {
	next  http.Handler
	index gatewayRouteIndex
}

func (h *gatewayPathAliasHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r == nil || r.URL == nil {
		h.next.ServeHTTP(w, r)
		return
	}
	path := r.URL.Path

	info, ok := h.index.canonicalGatewayPath(r.Method, path)
	if !ok {
		// 折叠后仍然不存在的版本根路径（/v1/v1/typo…）要给带 Base URL 提示的 JSON 诊断；
		// GET/POST 是协议客户端的调用方式，OPTIONS（CORS 预检）、HEAD 等方法不诊断，避免
		// 破坏浏览器行为。带前缀的拼写错误同样不诊断，不扩散诊断面。
		//
		// 诊断不在 gin 之外直接响应：这里只在 context 里留下信息，请求原样进 gin，404 JSON 由
		// gin 404 链末端的 gatewayPathNotFoundDiagnostic 输出。这样 Recovery、ipRisk、
		// Honeypot、访问日志对这类请求照常生效（/v1/v1/../../.env 这类扫描器路径会被 Honeypot
		// 拦下并封禁，被封 IP 也拿不到诊断）。
		if gatewayUnmatchedDiagnostic(info) && (r.Method == http.MethodGet || r.Method == http.MethodPost) {
			r = r.WithContext(withGatewayPathDiagnostic(r.Context(), info))
		}
		h.next.ServeHTTP(w, r)
		return
	}
	h.rewriteAndServe(w, r, info)
}

func (h *gatewayPathAliasHandler) rewriteAndServe(w http.ResponseWriter, r *http.Request, info gatewayPathAliasInfo) {
	ctx := withGatewayPathAliasInfo(r.Context(), info)
	logGatewayPathNormalized(ctx, r, info)

	rewritten := r.WithContext(ctx)
	// 复制 URL，避免污染外层 handler 持有的 *url.URL。
	urlCopy := *r.URL
	urlCopy.Path = info.Normalized
	// RawPath 是 Path 的编码形式；折叠后的路径按新 Path 重新编码即可。
	urlCopy.RawPath = ""
	rewritten.URL = &urlCopy
	h.next.ServeHTTP(w, rewritten)
}

// gatewayPathNotFoundDiagnostic 在 gin 的 404 链末端对折叠后仍然不存在的 /v1/v1/* 子路径
// 返回 JSON 诊断，避免用户只看到空 404 而不知道是 Base URL 多写了一层 /v1。
// 只处理别名层打过标记、且确实走到 404 链的请求；其它请求原样继续（405 链、已被前面中间件
// 响应的请求都不碰）。
func gatewayPathNotFoundDiagnostic(c *gin.Context) {
	info, ok := gatewayPathDiagnosticFromContext(c.Request.Context())
	if !ok || c.Writer.Written() || c.Writer.Status() != http.StatusNotFound {
		c.Next()
		return
	}
	writeUnmatchedDiagnostic(c.Writer, c.Request, info)
	c.Abort()
}

// writeUnmatchedDiagnostic 输出 gateway_path_not_found 诊断 JSON（404）。
func writeUnmatchedDiagnostic(w http.ResponseWriter, r *http.Request, info gatewayPathAliasInfo) {
	host := sanitizeGatewayHost(r)
	hint := fmt.Sprintf(
		"Base URL 请填写 https://%s（Anthropic SDK / Claude Code 推荐）或 https://%s/v1（OpenAI SDK 推荐）；"+
			"重复的 /v1 前缀会被自动折叠为单个 /v1，但 %s 不在网关支持的协议路径内，请检查路径是否拼错。",
		host, host, info.Normalized,
	)
	logGatewayPathNormalized(r.Context(), r, info)
	payload := map[string]any{
		"error": map[string]any{
			"type":            "invalid_request_error",
			"code":            "gateway_path_not_found",
			"message":         fmt.Sprintf("Gateway path %q does not exist after collapsing duplicated /v1 prefixes to %q.", info.Original, info.Normalized),
			"hint":            hint,
			"original_path":   info.Original,
			"normalized_path": info.Normalized,
		},
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusNotFound)
	_ = json.NewEncoder(w).Encode(payload)
}

// logGatewayPathNormalized 记录一条 Info 级日志（每条被规范化/诊断的请求一条），
// 字段包含原始路径与规范路径，便于统计用户 Base URL 的填法。不要升级成 ERROR/WARN，
// 否则填错 Base URL 的客户端会把日志刷爆。
func logGatewayPathNormalized(ctx context.Context, r *http.Request, info gatewayPathAliasInfo) {
	method := ""
	userAgent := ""
	remoteAddr := ""
	if r != nil {
		method = r.Method
		userAgent = r.UserAgent()
		remoteAddr = r.RemoteAddr
	}
	prefix := info.Prefix
	if prefix == "" {
		prefix = "/"
	}
	// 兼容旧看板：根前缀的重复版本段等价于原来的"重复 /v1 前缀数"，其它前缀填 0。
	duplicateV1Prefixes := 0
	if info.Prefix == "" {
		duplicateV1Prefixes = info.DuplicatePrefixes
	}
	logger.FromContext(ctx).Info("gateway base url path normalized",
		zap.String("component", "gateway.path_alias"),
		zap.String("rule", string(info.Rule)),
		zap.String("gateway_prefix", prefix),
		zap.String("gateway_version", info.Version),
		zap.String("original_path", info.Original),
		zap.String("normalized_path", info.Normalized),
		zap.Int("duplicate_version_segments", info.DuplicatePrefixes),
		zap.Int("duplicate_v1_prefixes", duplicateV1Prefixes),
		zap.Bool("versionless_prefix", info.VersionlessPrefix),
		zap.String("method", method),
		zap.String("remote_addr", remoteAddr),
		zap.String("user_agent", truncateGatewayLogField(userAgent, 256)),
		// 统计类日志只留在标准日志里，避免污染 Ops 系统日志采集。
		zap.Bool(logger.OpsSystemLogSkipField, true),
	)
}

func truncateGatewayLogField(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max] + "..."
}

// sanitizeGatewayHost 只保留合法的 host 字符，避免把客户端可控的 Host 头原样
// 反射进错误提示（JSON 响应仍加了 nosniff）。
func sanitizeGatewayHost(r *http.Request) string {
	host := ""
	if r != nil {
		host = strings.TrimSpace(r.Host)
	}
	if host == "" {
		return "your-gateway-host"
	}
	for _, ch := range host {
		switch {
		case ch >= 'a' && ch <= 'z':
		case ch >= 'A' && ch <= 'Z':
		case ch >= '0' && ch <= '9':
		case ch == '.', ch == '-', ch == ':', ch == '[', ch == ']':
		default:
			return "your-gateway-host"
		}
	}
	return host
}
