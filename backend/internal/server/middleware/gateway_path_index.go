package middleware

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ipnotice"
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

// maxGatewayVersionSegments 是一条路径里允许出现的版本段（/v1、/v1beta …）个数上限。
// 合法写法最多 2~3 个（Base URL 已带 /v1，客户端又拼了 /v1/messages），16 已经远超真实需要。
// 超过的路径不进入任何折叠/剥离循环，原样交给 gin（404）：别名改写发生在鉴权之前，
// 重复上千个 /v1 的匿名请求不该换来成倍的 CPU。
const maxGatewayVersionSegments = 16

// gatewayEndpointSegments 是网关协议路由的末尾固定段集合。
//
// 判断“一条注册路由是不是网关路由”本来最可靠的是看 handler 链，但 gin v1.9 的
// Routes() 只返回 method + path，DebugPrintRouteFunc 又只在 DEBUG 模式触发，生产
// （ReleaseMode）里拿不到链。所以这里用路由表结构判断：网关协议路由的路径里一定有
// 协议端点段（messages、responses、chat、models、videos …）。
//
// 只看端点词不够：面板 API 里同样有 usage、models 这类词（/api/v1/admin/usage），
// 所以 newGatewayRouteIndex 还要求路径属于网关族（ipnotice.IsGatewayBusinessPath）。
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
	"key":            true, // /v1/sub2api/key
	"groups":         true, // /v1/sub2api/groups
	"account":        true, // /v1/sub2api/account
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
//     backend-api、codex …）。受支持前缀由它推导，不写死；
//   - versionlessPrefix：真实模板里**没有**版本段的前缀族 → 去掉版本段后的模板集合
//     （/backend-api/codex → {"responses"、"models" …}）。该族真实路由不带版本段，
//     所以客户端多写的任意个版本段都可以剥掉；
//   - versionedPrefix：真实模板里**带**版本段的前缀族 → 去掉版本段后的模板集合
//     （/antigravity → {"models"、"messages/count_tokens" …}）。只有“原路径完全
//     未命中任何路由”、且补版本段后**唯一**命中一条模板时才补（见
//     gatewayCanonicalMissingVersionPath）。
//
// 两个模板集合都只收绝对固定段（不含 :param / *catchall），路径参数与 catch-all
// 参与匹配时按 gin 语义放宽，但仍要求方法一致。
type gatewayRouteIndex struct {
	routes            []gatewayRoutePattern
	versionPrefix     map[string]int
	prefixSegments    map[string]bool
	versionlessPrefix map[string]map[string]bool
	versionedPrefix   map[string]map[string]bool
	// versionlessFamily 是“确认可以整段剥版本段”的前缀族（见
	// gatewayVersionlessPrefixes）：有直接挂着的无版本段模板、且没有以它为前缀的
	// 带版本段模板。建索引后算一次。
	versionlessFamily map[string]bool
}

func newGatewayRouteIndex(router *gin.Engine) gatewayRouteIndex {
	index := gatewayRouteIndex{
		versionPrefix:     make(map[string]int),
		prefixSegments:    make(map[string]bool),
		versionlessPrefix: make(map[string]map[string]bool),
		versionedPrefix:   make(map[string]map[string]bool),
	}
	if router == nil {
		return index
	}
	for _, route := range router.Routes() {
		// gin 不会为 GET 自动注册 HEAD；HEAD 请求交给 gin 默认行为即可。
		if route.Method == http.MethodHead {
			continue
		}
		// 只收网关族路由：管理/用户面板挂在 /api/v1 下，路径里也会有 usage、models 等端点词，
		// 但它们不是网关协议路径，不能成为别名的改写目标（改写后鉴权虽然照常生效，
		// 别名索引也不该吃面板前缀）。网关族的判定与 IP 共享检测共用同一份清单
		// （网关前缀 + 根别名），不在这里另维护一套字符串。
		if !ipnotice.IsGatewayBusinessPath(route.Path) {
			continue
		}
		segments := splitGatewayPathSegments(route.Path)
		if len(segments) == 0 || !isGatewayRouteSegments(segments) {
			continue
		}
		index.routes = append(index.routes, gatewayRoutePattern{method: route.Method, segments: segments})
		index.collectPrefixSegments(segments)
		index.collectVersionTemplates(segments)
		if len(segments) >= 2 && isGatewayVersionSegment(segments[len(segments)-2]) {
			index.versionPrefix[strings.Join(segments[:len(segments)-2], "/")]++
		}
	}
	index.versionlessFamily = index.gatewayVersionlessPrefixes()
	return index
}

// collectVersionTemplates 把一条路由登记进“无版本段前缀族 / 带版本段前缀族”：
//
//	/backend-api/codex/responses            → 无版本段族  backend-api/codex → responses
//	/antigravity/v1/messages                → 带版本段族  antigravity/v1    → messages
//	/antigravity/v1beta/models/:model       → 路径参数，不进模板集合（只允许整条模板都
//	                                          是固定段时才参与“补版本段”判断，避免
//	                                          /antigravity/models/任意值 被放宽补成
//	                                          /antigravity/v1/models/任意值）
//	/v1/messages                            → 带版本段族  v1                → messages
//
// 前缀族只取“根之后、第一个版本段/协议端点之前”的固定段；/v1/images/batches 这类
// 带版本段模板的前缀族是 "v1"、模板是 "images/batches"，与真实路由一一对应。
func (idx gatewayRouteIndex) collectVersionTemplates(segments []string) {
	versionAt := -1
	for i, segment := range segments {
		if isGatewayVersionSegment(segment) {
			versionAt = i
			break
		}
		if strings.ContainsAny(segment, ":*") || gatewayEndpointSegments[segment] || i == len(segments)-1 {
			break
		}
	}
	if versionAt >= 0 {
		// 带版本段：前缀族 = 版本段之前固定段 + 版本段本身；模板 = 其后的全部段。
		family := strings.Join(segments[:versionAt+1], "/")
		registerGatewayTemplate(idx.versionedPrefix, family, segments[versionAt+1:])
		return
	}
	// 无版本段：前缀族 = 协议端点之前的全部固定段；模板 = 其后的全部段。
	endpointAt := -1
	for i, segment := range segments {
		if strings.ContainsAny(segment, ":*") {
			break
		}
		if gatewayEndpointSegments[segment] {
			endpointAt = i
			break
		}
	}
	if endpointAt <= 0 {
		// 根路径（/messages 这种没有前缀段）或完全没有端点段：不登记。
		return
	}
	registerGatewayTemplate(idx.versionlessPrefix, strings.Join(segments[:endpointAt], "/"), segments[endpointAt:])
}

// registerGatewayTemplate 登记一条模板，family 为前缀族、tail 为端点之后的段。
//
// 与“补/剥版本段”的匹配无关地保留路径参数：内置模板匹配按 gin 语义
// （matchGatewayRouteSegments）放宽 :param / *catchall，真正要不要改写由调用方用
// matchGatewayRoute 再校验一次；这里只做“命中了几条模板”的计数。
func registerGatewayTemplate(target map[string]map[string]bool, family string, tail []string) {
	if family == "" || len(tail) == 0 {
		return
	}
	templates, ok := target[family]
	if !ok {
		templates = make(map[string]bool)
		target[family] = templates
	}
	templates[strings.Join(tail, "/")] = true
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

// gatewayVersionRootCount 返回前缀下面“版本段 + 端点”模板的条数。
func (idx gatewayRouteIndex) gatewayVersionRootCount(prefix string) int {
	return idx.versionPrefix[strings.Trim(prefix, "/")]
}

// isRootBareGatewayPath 判断路径是不是根前缀下的裸协议路径（/messages、
// /responses、/chat/completions、/messages/count_tokens …）：不以版本段开头、
// 第一段不是受支持的前缀段（/antigravity/models 是 /antigravity 前缀下的路径，
// 不是根裸路径），且最后一段是协议端点。
func (idx gatewayRouteIndex) isRootBareGatewayPath(path string) bool {
	segments := splitGatewayPathSegments(path)
	if len(segments) == 0 || isGatewayVersionSegment(segments[0]) {
		return false
	}
	if idx.prefixSegments[segments[0]] {
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
	return matchGatewayRouteParts(index, method, splitGatewayPathSegments(path))
}

// matchGatewayRouteParts 是 matchGatewayRoute 的段数组版本：调用方手里已经有拆好的
// 段时直接匹配，避免 Join 再 Split 一遍。
func matchGatewayRouteParts(index gatewayRouteIndex, method string, actual []string) bool {
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
// 只做三件事，结果都必须真实命中已注册路由（method 也要一致）：
//
//  1. 重复版本段折叠（任意前缀之后）：/v1/v1/messages、/antigravity/v1/v1/messages、
//     /antigravity/v1beta/v1beta/models、/backend-api/codex/v1/v1/responses …
//     连续重复的同一族版本段折叠成一个；不同族（/v1/v1beta）绝不折叠。
//  2. 无版本段前缀族剥离版本段：/backend-api/codex 下的真实路由本来就不带版本段
//     （/backend-api/codex/responses），所以该族里客户端多写的**任意个**版本段都剥掉：
//     /backend-api/codex/v1/responses、/backend-api/codex/v1/v1/responses、
//     /backend-api/codex/v1beta/responses … 全部 → /backend-api/codex/responses。
//     只剥长度 1（/v1）或 2（/v1beta、/v1alpha …）的版本片段，/backend-api 里的连字符
//     段永远不算版本段。剥完必须命中该族的无版本段模板。
//  3. 缺版本段补齐（含给带版本段前缀族补版本段）：在前缀（根 / 每个受支持前缀段）
//     之后插一个版本段，/messages → /v1/messages、/chat/completions →
//     /v1/chat/completions、/antigravity/messages → /antigravity/v1/messages。
//     根前缀只接受裸协议路径，/dashboard、/api/v1/* 不会被改写。
//
// 规范写法（/v1/messages、/antigravity/v1beta/models、/v1beta/models、
// /backend-api/codex/responses）处理前后完全一致，一个字节都不动。
// 版本段超过 maxGatewayVersionSegments 个的路径一律不处理，原样透传。
func (idx gatewayRouteIndex) canonicalGatewayPath(method, path string) (gatewayPathAliasInfo, bool) {
	segments := splitGatewayPathSegments(path)
	if len(segments) == 0 {
		return gatewayPathAliasInfo{}, false
	}
	versionSegments := countGatewayVersionSegments(segments)
	if versionSegments == 0 {
		// 没有版本段可剥，只有“补缺失版本段”这条路。
		return idx.gatewayMissingVersionPath(method, path)
	}
	if versionSegments > maxGatewayVersionSegments {
		// 版本段多到不可能是填错 Base URL（见 maxGatewayVersionSegments）：既不改写也不诊断，
		// 原样交给 gin 404。补版本段那条路对已带版本段的路径本来就返回 false，这里直接收口。
		return gatewayPathAliasInfo{}, false
	}

	// 规则 1 + 2：先尝试剥掉手写的版本片段（重复的折叠成一个，无版本段前缀族全剥）。
	if info, ok := idx.stripGatewayVersionPath(method, segments); ok {
		return info, true
	}
	if diagnostic, ok := unmatchedDuplicateVersionInfo(segments); ok {
		// 折叠后仍然不存在的 /v1/v1/* 子路径：返回折叠结果，交给上层返回 JSON 诊断。
		return diagnostic, false
	}

	// 规则 3：补缺失的版本段（原路径带版本段时只走保守的“补前缀”路径）。
	return idx.gatewayMissingVersionPath(method, path)
}

// maxGatewayCleanPathBytes 是斜杠清理愿意处理的路径长度上限。清理发生在鉴权之前，
// 合法的网关路径只有几十字节；更长的路径不可能是填错 Base URL，原样交给 gin，
// 不为它做任何分配。
const maxGatewayCleanPathBytes = 2048

// collapseGatewaySlashes 去掉路径里的空段：/v1//chat/completions、//v1/chat/completions、
// /v1/chat/completions/ 都变成 /v1/chat/completions。只处理斜杠——不改大小写（路径里的模型
// ID 区分大小写），也不碰 "." / ".."。没有重复斜杠也没有结尾斜杠的路径原样返回。
// 单趟线性扫描，不 Split：连续上千个斜杠的请求不会换来成倍的内存。
func collapseGatewaySlashes(path string) string {
	if len(path) < 2 || len(path) > maxGatewayCleanPathBytes {
		return path
	}
	if !strings.Contains(path, "//") && !strings.HasSuffix(path, "/") {
		return path
	}
	cleaned := make([]byte, 0, len(path))
	previousSlash := false
	for i := 0; i < len(path); i++ {
		c := path[i]
		if c == '/' {
			if previousSlash {
				continue
			}
			previousSlash = true
		} else {
			previousSlash = false
		}
		cleaned = append(cleaned, c)
	}
	if len(cleaned) > 1 && cleaned[len(cleaned)-1] == '/' {
		cleaned = cleaned[:len(cleaned)-1]
	}
	return string(cleaned)
}

// canonicalCleanedGatewayPath 处理「路径本身没问题、只是多了斜杠」的请求（也包括斜杠之外还多写了 /v1
// 或漏写 /v1 的）。去掉斜杠后必须真实命中已注册的网关路由才改写，所以面板页面、静态资源、
// /health 这些路径不会被碰。ok=false 表示不需要或不能清理。
func (idx gatewayRouteIndex) canonicalCleanedGatewayPath(method, path string) (gatewayPathAliasInfo, bool) {
	cleaned := collapseGatewaySlashes(path)
	if cleaned == path {
		return gatewayPathAliasInfo{}, false
	}
	if info, ok := idx.canonicalGatewayPath(method, cleaned); ok {
		// 日志里记原始路径，规范路径是折叠/补齐之后的结果。
		info.Original = path
		return info, true
	}
	if !matchGatewayRoute(idx, method, cleaned) {
		return gatewayPathAliasInfo{}, false
	}
	segments := splitGatewayPathSegments(cleaned)
	prefix := segments
	version := ""
	for i, segment := range segments {
		if isGatewayVersionSegment(segment) {
			prefix, version = segments[:i], segment
			break
		}
	}
	if version == "" {
		prefix = nil
	}
	return gatewayPathAliasInfo{
		Original:   path,
		Normalized: cleaned,
		Rule:       PathAliasRuleCleanedSlashes,
		Prefix:     gatewayPrefixLabel(prefix),
		Version:    version,
	}, true
}

// gatewayVersionlessPrefixes 返回真正“无版本段前缀族”的集合：
//
//	/backend-api/codex 有害真实路由 /backend-api/codex/responses，
//	且没有任何 /backend-api/codex/v1/... 这类带版本段的真实路由 → 可以剥版本段；
//	/antigravity 有 /antigravity/models，但还有 /antigravity/v1/messages 这类带版本段
//	的真实路由 → 不是“无版本段前缀族”，绝不整段剥版本段。
func (idx gatewayRouteIndex) gatewayVersionlessPrefixes() map[string]bool {
	prefixes := make(map[string]bool, len(idx.versionlessPrefix))
	for prefix := range idx.versionlessPrefix {
		if idx.isVersionlessPrefixFamily(prefix) {
			prefixes[prefix] = true
		}
	}
	return prefixes
}

// isVersionlessPrefixFamily 判断 prefix 是不是“真实路由不带版本段的前缀族”：
// 有直接挂在它下面的无版本段模板（/backend-api/codex/responses），**并且**没有
// 以它自己为前缀的带版本段模板（/antigravity/v1/models 的前缀族是 antigravity/v1，
// 说明 /antigravity 这个族带版本段）。只有这样的前缀族才允许把手写的版本段整段剥掉。
//
// 前缀段用 "a/b" 拼接。
func (idx gatewayRouteIndex) isVersionlessPrefixFamily(prefix string) bool {
	if prefix == "" || idx.versionlessFamily != nil {
		return idx.versionlessFamily[prefix]
	}
	if _, ok := idx.versionlessPrefix[prefix]; !ok {
		return false
	}
	for family := range idx.versionedPrefix {
		if family == prefix || strings.HasPrefix(family, prefix+"/") {
			return false
		}
	}
	return true
}

// gatewayTemplateMatches 判断 tail 是否命中该前缀族下的至少一条模板。模板里可能带
// 路径参数（/models/:model），这里按 gin 语义匹配：:param 吃掉一段、*catchall 吃掉
// 至少一段且必须在末尾。
func gatewayTemplateMatches(templates map[string]bool, tail string) bool {
	actual := splitGatewayPathSegments(tail)
	if len(actual) == 0 {
		return false
	}
	for template := range templates {
		if matchGatewayRouteSegments(splitGatewayPathSegments(template), actual) {
			return true
		}
	}
	return false
}

// gatewayCanonicalMissingVersionPath 是“补版本段”的保守入口（需求 R2）：
//
// 只有当**原路径完全未命中任何网关路由**、**补一个版本段只命中一条模板**时才补，
// 命中多条或零条都保持原样交给 gin（继续 404）。这样 /antigravity/models/:model
// 如果只存在 /antigravity/v1/models/:model，/antigravity/models/xxx 会补成
// /antigravity/v1/models/xxx；而 /antigravity 下同时在 v1 与 v1beta 里挂了
// models 时保持原样，不猜用户的意图。
func (idx gatewayRouteIndex) gatewayCanonicalMissingVersionPath(method, path string) (gatewayPathAliasInfo, bool) {
	segments := splitGatewayPathSegments(path)
	if len(segments) == 0 || pathHasVersionSegment(segments) {
		return gatewayPathAliasInfo{}, false
	}
	if matchGatewayRoute(idx, method, path) {
		// 原路径本来就命中真实路由（/antigravity/models），一个字节都不动。
		return gatewayPathAliasInfo{}, false
	}
	matched := make([]gatewayPathAliasInfo, 0, len(idx.versionedPrefix))
	for versionFamily, templates := range idx.versionedPrefix {
		versionSegments := splitGatewayPathSegments(versionFamily)
		if len(versionSegments) < 2 {
			continue
		}
		// 族名 antigravity/v1 拆成：“前缀” antigravity + “版本段” v1。
		prefixText := strings.Join(versionSegments[:len(versionSegments)-1], "/")
		versionText := versionSegments[len(versionSegments)-1]
		prefixSegments := splitGatewayPathSegments(prefixText)
		if len(prefixSegments) == 0 || len(segments) <= len(prefixSegments) {
			continue
		}
		// 路径必须以这个前缀开头，剩下的是“没写版本段”的尾部。
		if strings.Join(segments[:len(prefixSegments)], "/") != prefixText {
			continue
		}
		tail := strings.Join(segments[len(prefixSegments):], "/")
		if !gatewayTemplateMatches(templates, tail) {
			continue
		}
		normalized := "/" + versionFamily + "/" + tail
		if isExcludedBareGatewayPath(normalized) || !matchGatewayRoute(idx, method, normalized) {
			continue
		}
		matched = append(matched, gatewayPathAliasInfo{
			Original:   path,
			Normalized: normalized,
			Rule:       PathAliasRuleMissingVersionPrefix,
			Prefix:     gatewayPrefixLabel(prefixSegments),
			Version:    versionText,
		})
	}
	if len(matched) != 1 {
		return gatewayPathAliasInfo{}, false
	}
	return matched[0], true
}

// gatewayMissingVersionPath 在每个“版本段可能被漏写”的位置插一个版本段：
//
//	位置 0：根前缀之后（/messages → /v1/messages、/chat/completions → /v1/chat/completions）
//	位置 1..：每个受支持前缀段之后（/antigravity/messages → /antigravity/v1/messages、
//	         /backend-api/codex/responses → /backend-api/codex/v1/models）
//
// 插完必须真实命中路由才算数，所以 /antigravity/media、/dashboard、/api/v1/* 都保持原样。
// 已经带版本段的路径（/v1/v1beta/models、/v1beta/models）也原样交给 gin。
//
// 带版本段前缀族的“补版本段”（/antigravity/models/:model → /antigravity/v1/models/:model）
// 只走保守入口 gatewayCanonicalMissingVersionPath：原路径必须完全未命中、且补完只命中
// 一条模板；这里的位置循环只负责“前缀位置 + v1”这种历史行为。
func (idx gatewayRouteIndex) gatewayMissingVersionPath(method, path string) (gatewayPathAliasInfo, bool) {
	segments := splitGatewayPathSegments(path)
	if len(segments) == 0 || pathHasVersionSegment(segments) {
		return gatewayPathAliasInfo{}, false
	}
	// 原路径本来就是真实路由时一个字节都不改。根前缀例外：/messages 也注册了裸
	// 路由，但它属于“历史裸别名”，规范路径仍然是 /v1/messages（/messages/count_tokens
	// 必须补成 /v1/messages/count_tokens，与历史行为一致）。
	if !idx.isRootBareGatewayPath(path) && matchGatewayRoute(idx, method, path) {
		return gatewayPathAliasInfo{}, false
	}
	if info, ok := idx.gatewayCanonicalMissingVersionPath(method, path); ok {
		return info, true
	}
	if idx.isRootBareGatewayPath(path) {
		// 根前缀只对裸协议路径生效（/messages → /v1/messages）。
		if relocated, ok := idx.insertVersionAt(method, path, segments, 0); ok {
			return relocated, true
		}
		return gatewayPathAliasInfo{}, false
	}
	// 带前缀的路径（/antigravity/*）只走保守规则：原路径必须完全未命中、补上
	// 版本段后必须唯一命中一条模板（见 gatewayCanonicalMissingVersionPath）。
	// 不做“每个前缀段后面都插一个版本段”的枚举，避免 /antigravity/models/xxx
	// 同时在 v1 与 v1beta 下挂着时随便挑一个版本族。
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

// countGatewayVersionSegments 统计路径里版本段的个数。
func countGatewayVersionSegments(segments []string) int {
	count := 0
	for _, segment := range segments {
		if isGatewayVersionSegment(segment) {
			count++
		}
	}
	return count
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
