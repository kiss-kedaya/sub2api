package middleware

import "strings"

// gatewayVersionSpec 是“手写的版本片段”里一个版本段的各种写法。
//
// 版本段允许出现重复（/v1/v1/messages），也允许把 v1beta 写成分开的两段
// （/v1/beta/messages），但“不同族”（/v1/v1beta、/v1beta/v1）绝不属于同一个版本
// 片段——这是 R1 里“不同族绝不折叠、/v1beta 永不变成 /v1”的硬边界。
type gatewayVersionSpec struct {
	// segments 是片段里一个版本段写成的段序列：{"v1"} 或 {"v1", "beta"}。
	segments []string
	// family 是规范化后的版本段文本（"v1"、"v1beta"），写进诊断日志。
	family string
}

// gatewayVersionSpecs 返回 “variant 个版本段”的全部写法：v1、v1beta，以及把
// v1beta 拆成 v1/beta 两段的手写形态。
func gatewayVersionSpecs(variant int) []gatewayVersionSpec {
	specs := []gatewayVersionSpec{
		{segments: repeatVersionSegment(gatewayVersionSegmentPrefix, variant), family: gatewayVersionSegmentPrefix},
		{segments: repeatVersionSegment(gatewayVersionSegmentPrefix+"beta", variant), family: gatewayVersionSegmentPrefix + "beta"},
	}
	if variant == 1 {
		specs = append(specs, gatewayVersionSpec{
			segments: []string{gatewayVersionSegmentPrefix, "beta"},
			family:   gatewayVersionSegmentPrefix + "beta",
		})
	}
	return specs
}

func repeatVersionSegment(segment string, count int) []string {
	repeated := make([]string, 0, count)
	for i := 0; i < count; i++ {
		repeated = append(repeated, segment)
	}
	return repeated
}

// matchGatewayVersionRun 判断 segments[from:] 是否以 spec 重复 run 次开头，返回
// run 与匹配到的总段数；不匹配返回 0。
func matchGatewayVersionRun(segments []string, from int, spec gatewayVersionSpec) (int, int) {
	run := 0
	offset := from
	for offset+len(spec.segments) <= len(segments) {
		matched := true
		for i, segment := range spec.segments {
			if segments[offset+i] != segment {
				matched = false
				break
			}
		}
		if !matched {
			break
		}
		run++
		offset += len(spec.segments)
	}
	if run == 0 {
		return 0, 0
	}
	return run, offset - from
}

// gatewayStripMode 是“手写版本片段”的改写方式。
type gatewayStripMode int

const (
	foldToOne gatewayStripMode = iota // 重复的版本片段折叠成一个版本段
	stripAll                          // 整个版本片段都剥掉（无版本段前缀族）
)

// gatewayStripCandidate 是一次可尝试的改写：把 segments[start:end] 换成 replace。
type gatewayStripCandidate struct {
	start   int
	end     int // 版本片段结束位置（不含）
	spec    gatewayVersionSpec
	run     int
	mode    gatewayStripMode
	replace []string
}

// scanVersionFamilyRun 从 from 起数出连续、同族的**单个**版本段（v1、v1beta 等）
// 一共占用的段数，遇到第一个非版本段或不同写法就停止；一个都没有返回 0。
func scanVersionFamilyRun(segments []string, from int) int {
	length := 0
	for from+length < len(segments) && isGatewayVersionSegment(segments[from+length]) {
		length++
	}
	return length
}

// stripGatewayVersionPath 依次尝试每一种“手写版本片段”的改写方式，返回第一个真实
// 命中路由的结果：
//
//   - 折叠成一个版本段：/v1/v1/messages → /v1/messages、
//     /antigravity/v1beta/v1beta/models → /antigravity/v1beta/models、
//     /v1/beta/messages → /v1beta/messages；
//   - 整段剥掉（只对“真实路由不带版本段”的前缀族开放，即 /backend-api/codex）：
//     /backend-api/codex/v1/responses → /backend-api/codex/responses、
//     /backend-api/codex/v1/v1/v1/responses → /backend-api/codex/responses、
//     /backend-api/codex/v1beta/responses → /backend-api/codex/responses。
//
// 顺序硬约束：“折叠成一个版本段”的候选永远排在“整段剥掉”前面，所以
// /v1/v1beta/models 这种“不同族”的写法不会被剥成 /v1/models（它连候选都进不去，
// 因为 v1/v1beta 不构成同一个版本片段）。
func (idx gatewayRouteIndex) stripGatewayVersionPath(method string, segments []string) (gatewayPathAliasInfo, bool) {
	var candidates []gatewayStripCandidate
	for variant := 1; variant <= maxGatewayVersionFamilySteps; variant++ {
		for _, spec := range gatewayVersionSpecs(variant) {
			// 每个 (variant, spec) 只从左到右扫一遍：命中一段连续的版本片段后整段跳过，
			// 不再从片段内部的每个位置各数一遍。这一步发生在鉴权之前，/v1 重复上千次时
			// 逐位置重扫是 O(N²)（8KB 路径约 0.4s CPU）。片段内部起点的候选只会把多余的
			// 版本段留在改写结果里，而路由表里没有连续两个版本段的路由，不会命中，所以丢掉它们
			// 对生产路由表无损（独立差分 48.7 万条路径：版本段 ≤16 时只有通配尾路由上 ≥4 个混合族版本段的
			// 极端写法 4 行不同——旧版折叠、新版不改写直接 404，可接受）。
			for start := 0; start < len(segments); {
				run, length := matchGatewayVersionRun(segments, start, spec)
				if run == 0 {
					start++
					continue
				}
				if run >= 2 {
					// 同一个版本段连续写了 run 次 → 折叠成一个版本段。
					candidates = append(candidates, gatewayStripCandidate{
						start: start, end: start + length, spec: spec, run: run, mode: foldToOne,
						replace: []string{spec.family},
					})
					start += length
					continue
				}
				// 没有重复的版本段（只写了一遍）：无版本段前缀族可以整段剥掉。
				// 整个连续的同族版本片段都要一次剥光（/backend-api/codex/v1/v1/v1/
				// responses 里三段的 v1 是一起剥掉的）；片段后面必须紧跟端点，避免
				// 把 /v1/v1/messages 里第一个 v1 当成“没有重复”的版本片段剥掉。
				whole := scanVersionFamilyRun(segments, start)
				if whole != 0 && start+whole < len(segments) && !isGatewayVersionSegment(segments[start+whole]) {
					candidates = append(candidates, gatewayStripCandidate{
						start: start, end: start + whole, spec: spec, run: whole / len(spec.segments), mode: stripAll,
					})
				}
				start += max(length, whole, 1)
			}
		}
	}
	for _, candidate := range candidates {
		// 折叠优先：先把所有“重复版本片段”折叠完，再考虑整段剥掉。
		if candidate.mode == stripAll {
			continue
		}
		if idx.applyStripCandidate(method, segments, candidate) {
			return idx.stripInfo(segments, candidate, false), true
		}
	}
	for _, candidate := range candidates {
		if candidate.mode != stripAll {
			continue
		}
		// 整段剥掉只对“真实路由不带版本段”的前缀族开放（见 isVersionlessPrefixFamily）。
		if !idx.isVersionlessPrefixFamily(strings.Join(segments[:candidate.start], "/")) {
			continue
		}
		// 版本片段后面还跟着另一个版本段（/v1/v1beta/models）说明用户有意写了
		// 第二个版本族，属于“不同族绝不折叠”，原样透传。
		if candidate.end < len(segments) && isGatewayVersionSegment(segments[candidate.end]) {
			continue
		}
		if idx.applyStripCandidate(method, segments, candidate) {
			return idx.stripInfo(segments, candidate, true), true
		}
	}
	return gatewayPathAliasInfo{}, false
}

// applyStripCandidate 按候选改写路径并校验是否命中路由。直接在段数组上匹配，
// 不把整条路径 Join 成字符串再 Split 回来（每个候选各一次，路径越长越贵）。
func (idx gatewayRouteIndex) applyStripCandidate(method string, segments []string, candidate gatewayStripCandidate) bool {
	normalizedSegments := make([]string, 0, len(segments))
	normalizedSegments = append(normalizedSegments, segments[:candidate.start]...)
	normalizedSegments = append(normalizedSegments, candidate.replace...)
	normalizedSegments = append(normalizedSegments, segments[candidate.end:]...)
	// 注意：这里不复用 isExcludedBareGatewayPath——/v1/usage、/v1/sub2api/* 的排除
	// 只作用于“补缺失版本段”，显式写了 /v1/v1/usage 的重复前缀仍然要折叠并命中。
	return matchGatewayRouteParts(idx, method, normalizedSegments)
}

// stripInfo 组装诊断信息。
func (idx gatewayRouteIndex) stripInfo(segments []string, candidate gatewayStripCandidate, versionless bool) gatewayPathAliasInfo {
	normalizedSegments := make([]string, 0, len(segments))
	normalizedSegments = append(normalizedSegments, segments[:candidate.start]...)
	normalizedSegments = append(normalizedSegments, candidate.replace...)
	normalizedSegments = append(normalizedSegments, segments[candidate.end:]...)
	// 前缀标签统一写成折叠后的形态：/backend-api/codex/v1、/backend-api/codex/v1beta，
	// 不写成 /backend-api/codex/v1/v1。
	prefixSegments := append(append([]string{}, segments[:candidate.start]...), candidate.spec.family)
	// DuplicatePrefixes 统计“路径里手写了几个版本段”：/backend-api/codex/v1/v1/responses
	// 虽然最终是整段剥掉（该族真实路由不带版本段），仍然算写了 2 个。
	written := 0
	for _, segment := range segments[candidate.start:candidate.end] {
		if isGatewayVersionSegment(segment) {
			written++
		}
	}
	return gatewayPathAliasInfo{
		Original:          "/" + strings.Join(segments, "/"),
		Normalized:        "/" + strings.Join(normalizedSegments, "/"),
		Rule:              PathAliasRuleDuplicateVersionPrefix,
		Prefix:            gatewayPrefixLabel(prefixSegments),
		Version:           candidate.spec.family,
		DuplicatePrefixes: written,
		VersionlessPrefix: versionless,
	}
}

// unmatchedDuplicateVersionInfo 为“确实重复写了同一族版本段、但折叠后仍不存在”的
// 版本根路径构造诊断信息（/v1/v1/typo 这种 Base URL 填错的情形）。调用方据此返回
// gateway_path_not_found 的 JSON 而不是空 404。
func unmatchedDuplicateVersionInfo(segments []string) (gatewayPathAliasInfo, bool) {
	specs := gatewayVersionSpecs(1)
	for start := 0; start < len(segments); start++ {
		for _, spec := range specs {
			run, length := matchGatewayVersionRun(segments, start, spec)
			if run < 2 {
				continue
			}
			folded := make([]string, 0, len(segments))
			folded = append(folded, segments[:start]...)
			folded = append(folded, spec.family)
			folded = append(folded, segments[start+length:]...)
			return gatewayPathAliasInfo{
				Original:          "/" + strings.Join(segments, "/"),
				Normalized:        "/" + strings.Join(folded, "/"),
				Rule:              PathAliasRuleDuplicateVersionPrefix,
				Prefix:            gatewayPrefixLabel(append(append([]string{}, segments[:start]...), spec.family)),
				Version:           spec.family,
				DuplicatePrefixes: run,
			}, true
		}
	}
	return gatewayPathAliasInfo{}, false
}
