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

// 本文件在 gin 路由匹配之前规范化网关协议路径，容忍客户端把 Base URL 填错：
//
//  1. 重复的 /v1 前缀折叠成一个：/v1/v1/messages、/v1/v1/v1/messages …
//     → /v1/messages，前缀个数不设上限；
//  2. 漏写 /v1 的裸协议路径补齐到 /v1 下：/messages → /v1/messages。
//
// 规范化只修改 URL.Path，query string、header、body、HTTP method 全部保持原样；
// 重写之后仍然交给同一个 gin 路由 + 同一套中间件链处理，因此鉴权、限流、计费、
// 访问日志与直接请求规范路径完全一致，不需要第二套路由或第二套中间件。
//
// 只有规范化结果确实命中已注册的 /v1 路由（method 也要一致）时才会重写，
// 所以 /api/v1/*、/admin/*、/assets/* 等静态资源、/health 探针和 SPA 页面路由
// 不会被误伤。

const gatewayVersionPrefix = "/v1"

// PathAliasRule 描述一次网关路径规范化采用的规则。
type PathAliasRule string

const (
	// PathAliasRuleDuplicateVersionPrefix 表示请求把 /v1 写了多遍（/v1/v1/...），已折叠成一个。
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
	DuplicatePrefixes int
}

type gatewayPathAliasContextKey struct{}

func withGatewayPathAliasInfo(ctx context.Context, info gatewayPathAliasInfo) context.Context {
	return context.WithValue(ctx, gatewayPathAliasContextKey{}, info)
}

// gatewayPathAliasInfoFromContext 读取路径规范化信息，由 Logger() 写入访问日志。
func gatewayPathAliasInfoFromContext(ctx context.Context) (gatewayPathAliasInfo, bool) {
	if ctx == nil {
		return gatewayPathAliasInfo{}, false
	}
	info, ok := ctx.Value(gatewayPathAliasContextKey{}).(gatewayPathAliasInfo)
	return info, ok
}

// FoldDuplicateVersionPrefix 计算把重复的 /v1 前缀折叠成一个之后的结果。
//
// 返回值 count 表示请求实际携带的连续 /v1 前缀个数：0 表示路径没有以 /v1 开头，
// 1 表示写法本来就规范。只有 count >= 2 时 folded 才与 path 不同。
func FoldDuplicateVersionPrefix(path string) (folded string, count int) {
	if path == gatewayVersionPrefix {
		return path, 1
	}
	if !strings.HasPrefix(path, gatewayVersionPrefix+"/") {
		return path, 0
	}
	rest := path[len(gatewayVersionPrefix):]
	count = 1
	for rest == gatewayVersionPrefix || strings.HasPrefix(rest, gatewayVersionPrefix+"/") {
		rest = rest[len(gatewayVersionPrefix):]
		count++
	}
	if count == 1 {
		return path, 1
	}
	return gatewayVersionPrefix + rest, count
}

// canonicalizeMissingVersionPrefix 把没有 /v1 前缀的路径补成 /v1/* 候选。
// 第二个返回值表示是否是一个可以补前缀的候选路径（不代表该路径真实存在）。
func canonicalizeMissingVersionPrefix(path string) (string, bool) {
	if path == "" || path == "/" || !strings.HasPrefix(path, "/") {
		return "", false
	}
	if path == gatewayVersionPrefix || strings.HasPrefix(path, gatewayVersionPrefix+"/") {
		return "", false
	}
	// /v1beta/... 属于 Gemini 原生 API（前缀是 /v1beta，不是 /v1），不要碰。
	return gatewayVersionPrefix + path, true
}

func isExcludedBareGatewayPath(path string) bool {
	for _, excluded := range gatewayBarePathExclusions {
		if path == excluded || strings.HasPrefix(path, excluded) {
			return true
		}
	}
	return false
}

// gatewayRoutePattern 是已注册 /v1 路由的一条模式（method + 路径模板）。
type gatewayRoutePattern struct {
	method   string
	segments []string
}

// gatewayRouteIndex 按 "method#路径第二段" 给 /v1 路由分桶，避免每个非网关请求
// （静态资源、SPA 页面、/api/v1 管理接口）都遍历完整路由表。
type gatewayRouteIndex map[string][]gatewayRoutePattern

func newGatewayRouteIndex(router *gin.Engine) gatewayRouteIndex {
	index := make(gatewayRouteIndex)
	if router == nil {
		return index
	}
	for _, route := range router.Routes() {
		if route.Path != gatewayVersionPrefix && !strings.HasPrefix(route.Path, gatewayVersionPrefix+"/") {
			continue
		}
		if route.Method == http.MethodHead {
			// gin 不会为 GET 自动注册 HEAD；HEAD 请求交给 gin 默认行为即可。
			continue
		}
		segments := splitGatewayPathSegments(route.Path)
		if len(segments) < 2 {
			continue
		}
		key := route.Method + "#" + segments[1]
		index[key] = append(index[key], gatewayRoutePattern{method: route.Method, segments: segments})
	}
	return index
}

func splitGatewayPathSegments(path string) []string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

// matchGatewayRoute 判断 method + path 是否会被某条已注册的 /v1 路由命中。
// 匹配只用于"要不要重写"的决策，宽松一点无害：真正响应仍由 gin 内部路由树决定。
func matchGatewayRoute(index gatewayRouteIndex, method, path string) bool {
	actual := splitGatewayPathSegments(path)
	if len(actual) < 2 || actual[0] != strings.TrimPrefix(gatewayVersionPrefix, "/") {
		return false
	}
	patterns := index[method+"#"+actual[1]]
	for _, pattern := range patterns {
		if matchGatewayRouteSegments(pattern.segments, actual) {
			return true
		}
	}
	return false
}

func matchGatewayRouteSegments(pattern, actual []string) bool {
	for i, segment := range pattern {
		if strings.HasPrefix(segment, "*") {
			// gin 的 catch-all 段（如 /responses/*subpath）至少要吃掉一段实际路径。
			return len(actual) > i
		}
		if i >= len(actual) {
			return false
		}
		if strings.HasPrefix(segment, ":") {
			// 路径参数（:model、:task_id 等）匹配任意单段。
			continue
		}
		if segment != actual[i] {
			return false
		}
	}
	return len(actual) == len(pattern)
}

// GatewayPathAlias 包装 gin 引擎，在路由匹配之前规范化网关协议路径。
//
// 返回的 http.Handler 应作为 HTTP server 的最终 handler 使用（见 ProvideHTTPServer），
// 这样规范化发生在 gin 路由之前，重写后的请求仍由原来的 /v1 路由与中间件链处理。
func GatewayPathAlias(router *gin.Engine) http.Handler {
	var next http.Handler
	if router != nil {
		next = router
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

	// 规则 1：重复 /v1 前缀折叠成一个（个数不设上限）。
	if folded, count := FoldDuplicateVersionPrefix(path); count >= 2 {
		info := gatewayPathAliasInfo{
			Original:          path,
			Normalized:        folded,
			Rule:              PathAliasRuleDuplicateVersionPrefix,
			DuplicatePrefixes: count,
		}
		if !matchGatewayRoute(h.index, r.Method, folded) {
			// GET/POST 是协议客户端的调用方式，返回带提示的 JSON 诊断；
			// OPTIONS（CORS 预检）、HEAD 等方法原样交给 gin，避免破坏浏览器行为。
			if r.Method == http.MethodGet || r.Method == http.MethodPost {
				h.writeUnmatchedDiagnostic(w, r, info)
				return
			}
			h.next.ServeHTTP(w, r)
			return
		}
		h.rewriteAndServe(w, r, info)
		return
	}

	// 规则 2：漏写 /v1 的裸协议路径补齐到 /v1 下。
	if candidate, ok := canonicalizeMissingVersionPrefix(path); ok &&
		!isExcludedBareGatewayPath(candidate) &&
		matchGatewayRoute(h.index, r.Method, candidate) {
		h.rewriteAndServe(w, r, gatewayPathAliasInfo{
			Original:   path,
			Normalized: candidate,
			Rule:       PathAliasRuleMissingVersionPrefix,
		})
		return
	}

	h.next.ServeHTTP(w, r)
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

// writeUnmatchedDiagnostic 对折叠后仍然不存在的 /v1/v1/* 子路径返回 JSON 诊断，
// 避免用户只看到空 404 而不知道是 Base URL 多写了一层 /v1。
func (h *gatewayPathAliasHandler) writeUnmatchedDiagnostic(w http.ResponseWriter, r *http.Request, info gatewayPathAliasInfo) {
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
	logger.FromContext(ctx).Info("gateway base url path normalized",
		zap.String("component", "gateway.path_alias"),
		zap.String("rule", string(info.Rule)),
		zap.String("original_path", info.Original),
		zap.String("normalized_path", info.Normalized),
		zap.Int("duplicate_v1_prefixes", info.DuplicatePrefixes),
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
