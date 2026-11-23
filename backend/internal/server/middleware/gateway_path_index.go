package middleware

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
)

// gatewayRoutePattern 是已注册网关路由的一条模式（method + 路径段）。
type gatewayRoutePattern struct {
	method   string
	segments []string
}

// maxGatewayPrefixDepth 是受支持前缀的最大段数：/backend-api/codex/realtime 是
// 目前最深的网关前缀（3 段），这里给到 4 段留余量。
const maxGatewayPrefixDepth = 4

// maxGatewayVersionFamilySteps 限制单次请求里尝试拼接的版本段族个数。
const maxGatewayVersionFamilySteps = 2

// gatewayEndpointSegments 是网关协议路由的末尾固定段集合。
//
// 判断“一条注册路由是不是网关路由”本来最可靠的是看 handler 链，但 gin v1.9 的
// Routes() 只返回 method + path，DebugPrintRouteFunc 又只在 DEBUG 模式触发，生产
// （ReleaseMode）里拿不到链。所以这里用路由表结构判断：网关协议路由的路径里一定有
// 协议端点段（messages、responses、chat、models、videos …），面板 API 与静态资源不会命中。
//
// 新增网关路由时如果引入新的端点名，测试
// TestGatewayPathAliasCoversEveryGatewayEndpoint 会提醒在集合里补上。
var gatewayEndpointSegments = map[string]bool{
	"messages":       true,
	"count_tokens":   true,
	"responses":      true,
	"chat":           true,
	"completions":    true,
	"embeddings":     true,
	"models":         true,
	"images":         true,
	"videos":         true,
	"usage":          true,
	"billing":        true,
	"realtime":       true,
	"live":           true,
	"tts":            true,
	"stt":            true,
	"custom-voices":  true,
	"web_search":     true,
	"x_search":       true,
	"alpha":          true,
	"tools":          true,
	"systemone":      true,
	"generations":    true,
	"edits":          true,
	"extensions":     true,
	"tasks":          true,
	"contents":       true,
	"cachedContents": true,
	"calls":          true,
	"search":         true,
	"asr":            true,
	"ocr":            true,
}

// isGatewayRoute segments 判断一条注册路由是不是网关协议路由：路径里有协议端点段，
// 且该段是固定段（不是 :param / *catchall）。
func isGatewayRouteSegments(segments []string) bool {
	for _, segment := range segments {
		if strings.HasPrefix(segment, ":") || strings.HasPrefix(segment, "*") {
			continue
		}
		if gatewayEndpointSegments[segment] {
			return true
		}
	}
	return false
}

// gatewayRouteIndex 是已注册网关路由的匹配索引：
//
//   - routes：全部网关注册路由（判断重写后是否真的命中某条路由）；
//   - versionPrefix：这些路由里“版本段 + 末尾段”模板（/v1/{endpoint}、
//     /antigravity/v1/{endpoint} …）去掉末尾段后的路径 → 条数。判断“补一个版本段
//     会不会真的命中”时用；
//   - prefixSegments：网关路由里“根之后、协议端点之前”的固定段（v1、antigravity、
//     backend-api、codex …）。受支持前缀由它推导，不写死。
type gatewayRouteIndex struct {
	routes         []gatewayRoutePattern
	versionPrefix  map[string]int
	prefixSegments map[string]bool
}

func newGatewayRouteIndex(router *gin.Engine) gatewayRouteIndex {
	index := gatewayRouteIndex{
		versionPrefix:  make(map[string]int),
		prefixSegments: make(map[string]bool),
	}
	if router == nil {
		return index
	}
	for _, route := range router.Routes() {
		// gin 不会为 GET 自动注册 HEAD；HEAD 请求交给 gin 默认行为即可。
		if route.Method == http.MethodHead {
			continue
		}
		segments := splitGatewayPathSegments(route.Path)
		if len(segments) == 0 || !isGatewayRouteSegments(segments) {
			continue
		}
		index.routes = append(index.routes, gatewayRoutePattern{method: route.Method, segments: segments})
		index.collectPrefixSegments(segments)
		if len(segments) >= 2 && isGatewayVersionSegment(segments[len(segments)-2]) {
			index.versionPrefix[strings.Join(segments[:len(segments)-2], "/")]++
		}
	}
	return index
}

// collectPrefixSegments 收集一条路由里可以用作前缀的固定段：
// 路径开头连续出现的“非协议端点”固定段（/antigravity/v1/messages → antigravity、v1）。
// 一旦遇到协议端点段或通配段就停止，后面的段（模型 ID、缓存 ID、responses …）不算前缀。
func (idx gatewayRouteIndex) collectPrefixSegments(segments []string) {
	for i, segment := range segments {
		if i >= maxGatewayPrefixDepth {
			return
		}
		if strings.ContainsAny(segment, ":*") || gatewayEndpointSegments[segment] || i == len(segments)-1 {
			return
		}
		idx.prefixSegments[segment] = true
	}
}

func splitGatewayPathSegments(path string) []string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

// gatewayPrefixCut 返回路径里“前缀”覆盖到的段数：从第 0 段开始连续匹配受支持前缀段，
// 最多到倒数第二段（最后一段一定是协议端点）。
func (idx gatewayRouteIndex) gatewayPrefixCut(segments []string) int {
	cut := 0
	for cut < len(segments)-1 && idx.prefixSegments[segments[cut]] {
		cut++
	}
	return cut
}

// gatewayVersionRootCount 返回前缀下面“版本段 + 端点”模板的条数。
func (idx gatewayRouteIndex) gatewayVersionRootCount(prefix string) int {
	return idx.versionPrefix[strings.Trim(prefix, "/")]
}

// isRootBareGatewayPath 判断路径是不是根前缀下的裸协议路径（/messages、
// /responses、/chat/completions、/messages/count_tokens …）：不以版本段开头，
// 且最后一段是协议端点。
func (idx gatewayRouteIndex) isRootBareGatewayPath(path string) bool {
	segments := splitGatewayPathSegments(path)
	if len(segments) == 0 || isGatewayVersionSegment(segments[0]) {
		return false
	}
	if gatewayEndpointSegments[segments[len(segments)-1]] {
		return true
	}
	// /models/claude-opus-5 这种“端点 + 路径参数”也是裸协议路径。
	return len(segments) >= 2 && gatewayEndpointSegments[segments[len(segments)-2]]
}

// matchGatewayRoute 判断 method + path 是否会被某条已注册的网关路由命中。
// 匹配只用于“要不要重写”的决策，宽松一点无害：真正响应仍由 gin 路由树决定。
func matchGatewayRoute(index gatewayRouteIndex, method, path string) bool {
	if path == "/" {
		return false
	}
	actual := splitGatewayPathSegments(path)
	if len(actual) == 0 {
		return false
	}
	for _, pattern := range index.routes {
		if pattern.method != method {
			continue
		}
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

// gatewayVersionFamilies 返回 rest 里出现的版本段族（v1、v1beta …），最多两个。
func gatewayVersionFamilies(rest string) []string {
	segments := splitGatewayPathSegments(rest)
	families := make([]string, 0, maxGatewayVersionFamilySteps)
	seen := make(map[string]bool, maxGatewayVersionFamilySteps)
	for _, segment := range segments {
		if !isGatewayVersionSegment(segment) || seen[segment] {
			continue
		}
		seen[segment] = true
		families = append(families, segment)
		if len(families) == maxGatewayVersionFamilySteps {
			break
		}
	}
	return families
}

// canonicalGatewayPath 计算规范化后的路径，ok=false 表示不需要/不能规范化。
//
// 只做两件事，结果都必须真实命中已注册路由（method 也要一致）：
//
//  1. 重复版本段折叠（任意前缀之后）：/v1/v1/messages、/antigravity/v1/v1/messages、
//     /antigravity/v1beta/v1beta/models、/backend-api/codex/v1/v1/responses …
//     连续重复的同一族版本段折叠成一个；不同族（/v1/v1beta）绝不折叠。
//  2. 缺版本段补齐：在前缀（根 / 每个受支持前缀段）之后插一个版本段，
//     /messages → /v1/messages、/chat/completions → /v1/chat/completions、
//     /antigravity/messages → /antigravity/v1/messages、
//     /backend-api/codex/responses → /backend-api/codex/v1/responses。
//     根前缀只接受裸协议路径，/dashboard、/api/v1/* 不会被改写。
//
// 规范写法（/v1/messages、/antigravity/v1beta/models、/v1beta/models）折叠前后
// 完全一致，一个字节都不动。
func (idx gatewayRouteIndex) canonicalGatewayPath(method, path string) (gatewayPathAliasInfo, bool) {
	segments := splitGatewayPathSegments(path)
	if len(segments) == 0 {
		return gatewayPathAliasInfo{}, false
	}

	// 规则 1：重复版本段折叠。
	mustFold := false
	for _, segment := range segments {
		if isGatewayVersionSegment(segment) {
			mustFold = true
			break
		}
	}
	if mustFold {
		folded := make([]string, 0, len(segments))
		family := ""
		runLength := 0
		foldAt := -1
		for i := 0; i < len(segments); {
			segment := segments[i]
			if !isGatewayVersionSegment(segment) {
				folded = append(folded, segment)
				i++
				continue
			}
			run := 1
			for i+run < len(segments) && segments[i+run] == segment {
				run++
			}
			if run >= 2 && runLength < run {
				runLength = run
				family = segment
				foldAt = len(folded)
			}
			folded = append(folded, segment)
			i += run
		}
		if runLength >= 2 {
			normalized := "/" + strings.Join(folded, "/")
			info := gatewayPathAliasInfo{
				Original:          path,
				Normalized:        normalized,
				Rule:              PathAliasRuleDuplicateVersionPrefix,
				Prefix:            "/" + strings.Join(folded[:foldAt+1], "/"),
				Version:           family,
				DuplicatePrefixes: runLength,
			}
			if matchGatewayRoute(idx, method, normalized) {
				return info, true
			}
			// 折叠后的路径还缺版本段（/backend-api/codex/v1/v1/responses →
			// /backend-api/codex/v1/responses 不存在，但 /backend-api/codex/responses
			// 存在）：把用户的真实意图（多写了一层版本段）继续归到折叠规则上。
			if relocated, ok := idx.forceGatewayMissingVersionPath(method, normalized); ok {
				relocated.Original = path
				relocated.Rule = PathAliasRuleDuplicateVersionPrefix
				relocated.Version = family
				relocated.DuplicatePrefixes = runLength
				return relocated, true
			}
			return info, false
		}
	}

	// 规则 2：补缺失的版本段。
	return idx.gatewayMissingVersionPath(method, path)
}

// forceGatewayMissingVersionPath 在“已经确认要改写”的场景里直接对 normalized 插版本段：
// 只用来把折叠后的结果（/backend-api/codex/v1/v1/responses →）归并出用户真正想调
// 的路径，仍然要求结果真实命中路由。
func (idx gatewayRouteIndex) forceGatewayMissingVersionPath(method, path string) (gatewayPathAliasInfo, bool) {
	segments := splitGatewayPathSegments(path)
	if len(segments) == 0 {
		return gatewayPathAliasInfo{}, false
	}
	for cut := 0; cut < len(segments); cut++ {
		if cut > maxGatewayPrefixDepth {
			break
		}
		headSegments := segments[:cut]
		tailSegments := segments[cut:]
		families := append([]string{gatewayVersionSegmentPrefix}, gatewayVersionFamilies("/"+strings.Join(tailSegments, "/"))...)
		for _, family := range families {
			candidate := "/" + strings.Join(append(append(append([]string{}, headSegments...), family), tailSegments...), "/")
			if isExcludedBareGatewayPath(candidate) || !matchGatewayRoute(idx, method, candidate) {
				continue
			}
			return gatewayPathAliasInfo{
				Original:   path,
				Normalized: candidate,
				Rule:       PathAliasRuleMissingVersionPrefix,
				Prefix:     gatewayPrefixLabel(headSegments),
				Version:    family,
			}, true
		}
	}
	return gatewayPathAliasInfo{}, false
}

// gatewayMissingVersionPath 在每个“版本段可能被漏写”的位置插一个版本段：
//
//	位置 0：根前缀之后（/messages → /v1/messages、/chat/completions → /v1/chat/completions）
//	位置 1..：每个受支持前缀段之后（/antigravity/messages → /antigravity/v1/messages、
//	         /backend-api/codex/responses → /backend-api/codex/v1/responses）
//
// 插完必须真实命中路由才算数，所以 /antigravity/media、/dashboard、/api/v1/* 都保持原样。
// 已经带版本段的路径（/v1/v1beta/models、/v1beta/models）也原样交给 gin。
func (idx gatewayRouteIndex) gatewayMissingVersionPath(method, path string) (gatewayPathAliasInfo, bool) {
	segments := splitGatewayPathSegments(path)
	if len(segments) == 0 || pathHasVersionSegment(segments) {
		return gatewayPathAliasInfo{}, false
	}
	rootBare := idx.isRootBareGatewayPath(path)
	limit := idx.gatewayPrefixCut(segments)
	if rootBare {
		// 根前缀只对裸协议路径生效：先试位置 0（/messages → /v1/messages），
		// 再试各前缀位置（/antigravity/models 的规范路径是 /antigravity/v1/models）。
		if relocated, ok := idx.insertVersionAt(method, path, segments, 0); ok {
			return relocated, true
		}
	}
	for cut := 1; cut <= limit; cut++ {
		if !idx.prefixSegments[segments[cut-1]] {
			continue
		}
		if relocated, ok := idx.insertVersionAt(method, path, segments, cut); ok {
			return relocated, true
		}
	}
	return gatewayPathAliasInfo{}, false
}

// insertVersionAt 在 segments 的第 cut 段之前插入一个版本段，命中路由才返回。
func (idx gatewayRouteIndex) insertVersionAt(method, path string, segments []string, cut int) (gatewayPathAliasInfo, bool) {
	headSegments := segments[:cut]
	tailSegments := segments[cut:]
	if len(tailSegments) == 0 {
		return gatewayPathAliasInfo{}, false
	}
	families := append([]string{gatewayVersionSegmentPrefix}, gatewayVersionFamilies("/"+strings.Join(tailSegments, "/"))...)
	for _, family := range families {
		candidate := "/" + strings.Join(append(append(append([]string{}, headSegments...), family), tailSegments...), "/")
		if isExcludedBareGatewayPath(candidate) || !matchGatewayRoute(idx, method, candidate) {
			continue
		}
		return gatewayPathAliasInfo{
			Original:   path,
			Normalized: candidate,
			Rule:       PathAliasRuleMissingVersionPrefix,
			Prefix:     gatewayPrefixLabel(headSegments),
			Version:    family,
		}, true
	}
	return gatewayPathAliasInfo{}, false
}

// gatewayPrefixLabel 把前缀段拼成日志用的标签：根前缀是 ""，其余是 "/antigravity" 这样的路径。
func gatewayPrefixLabel(headSegments []string) string {
	if len(headSegments) == 0 {
		return ""
	}
	return "/" + strings.Join(headSegments, "/")
}

// pathHasVersionSegment 判断路径里是否出现过版本段。
func pathHasVersionSegment(segments []string) bool {
	for _, segment := range segments {
		if isGatewayVersionSegment(segment) {
			return true
		}
	}
	return false
}

// isVersionRootedGatewayPath 判断路径第一段是不是版本段（/v1/…、/v1beta/…）。
func isVersionRootedGatewayPath(path string) bool {
	segments := splitGatewayPathSegments(path)
	if len(segments) == 0 {
		return false
	}
	return isGatewayVersionSegment(segments[0])
}

// gatewayUnmatchedDiagnostic 判断“折叠后不存在”的请求要不要返回 JSON 诊断：
// 只有可能来自 Base URL 填错（折叠发生在 /v1、/v1beta 这类版本根路径下）时才返回诊断，
// 其它前缀下的拼写错误交给 gin 原样 404。
func gatewayUnmatchedDiagnostic(info gatewayPathAliasInfo) bool {
	if info.Rule != PathAliasRuleDuplicateVersionPrefix {
		return false
	}
	return isVersionRootedGatewayPath(info.Normalized)
}

// supportedGatewayPrefixes 返回当前路由表推导出的受支持前缀段（测试/诊断用）。
func supportedGatewayPrefixes(index gatewayRouteIndex) []string {
	prefixes := make([]string, 0, len(index.prefixSegments))
	for segment := range index.prefixSegments {
		prefixes = append(prefixes, segment)
	}
	sort.Strings(prefixes)
	return prefixes
}

// gatewayIndexDebugString 返回索引的可读摘要，出问题时方便定位。
func gatewayIndexDebugString(index gatewayRouteIndex) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "gateway routes=%d prefix segments=%v\n", len(index.routes), supportedGatewayPrefixes(index))
	versionRoots := make([]string, 0, len(index.versionPrefix))
	for key := range index.versionPrefix {
		versionRoots = append(versionRoots, key)
	}
	sort.Strings(versionRoots)
	fmt.Fprintf(&builder, "version roots=%v", versionRoots)
	return builder.String()
}
