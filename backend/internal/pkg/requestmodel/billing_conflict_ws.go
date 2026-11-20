package requestmodel

import (
	"strings"

	"github.com/tidwall/gjson"
)

// WS 帧没有 HTTP 请求体那道 RejectConflictingRequestFields 准入，网关只对
// Responses WebSocket 帧做了重复 model 检查（openAIConflictingModels）。
// 但计费与策略读取的字段不止 model：service_tier（priority/flex/ultrafast 倍率）、
// reasoning.effort / output_config.effort（推理等级倍率）都按 gjson 首个值判断，
// 而帧以原始字节（仅 sjson 只改首个同名字段）转发给上游，上游 JSON 解析器按末值
// 绑定——同名字段重复即可让「计费看到的档位」与「上游实际收到的档位」分叉，
// 与 GHSA-p3r4-jcm3-2hph 在 HTTP 入口修复的是同一类问题。
//
// 本函数是 WS 帧的补充检查：只覆盖不在 model 候选逻辑里的计费/策略字段，
// 目的是把帧检查的成本压在固定几个顶层键上，不引入整帧任意深度扫描
// （WS 单帧上限 64MB，且每 turn 都要检查）。

// wsBillingConflictTopLevelKeys 是 WS 帧里按 gjson 首个值做计费/策略判断、
// 且随后被原样转发给上游的顶层键。model 不在其中：它由 FromBodyCandidates +
// ConflictingModelCandidates 覆盖（含大小写变体与 session.model）。
var wsBillingConflictTopLevelKeys = []string{
	"service_tier",
	"speed",
	"reasoning",
	"output_config",
}

func isWSBillingConflictKey(name string) bool {
	for _, candidate := range wsBillingConflictTopLevelKeys {
		if strings.EqualFold(name, candidate) {
			return true
		}
	}
	return false
}

// wsBillingConflictNestedKeys 是上述顶层键内部同样会被 gjson 单独读取的成员：
// reasoning.effort / output_config.effort 决定推理等级倍率。
var wsBillingConflictNestedKeys = []string{"effort"}

// ConflictingWSBillingField 检查 Responses WebSocket 帧里计费/策略相关字段的
// 重复声明。返回首个冲突字段的路径（如 service_tier、reasoning.effort）。
//
// 语义与 HTTP 入口的 ConflictingField 对齐：同名字段重复即拒绝（即便取值相同，
// 因为网关的 sjson 改写只改首个，改写后两者就不再相同）；大小写不敏感。
func ConflictingWSBillingField(body []byte) (string, bool) {
	if len(body) == 0 {
		return "", false
	}
	// unsafeStringView 与同包 model 候选解析一致：只在本调用内读取，不持有视图，
	// 避免 gjson.ParseBytes 对整帧做一次拷贝（WS 单帧上限 64MB）。
	root := gjson.Parse(unsafeStringView(body))
	if !root.IsObject() {
		return "", false
	}
	seen := make(map[string]struct{}, len(wsBillingConflictTopLevelKeys))
	var field string
	var conflict bool
	root.ForEach(func(key, value gjson.Result) bool {
		name := key.String()
		if !isWSBillingConflictKey(name) {
			return true
		}
		folded := strings.ToLower(name)
		if _, dup := seen[folded]; dup {
			field, conflict = folded, true
			return false
		}
		seen[folded] = struct{}{}
		if !value.IsObject() {
			return true
		}
		var nestedSeen map[string]struct{}
		value.ForEach(func(nestedKey, _ gjson.Result) bool {
			nestedName := nestedKey.String()
			matched := false
			for _, candidate := range wsBillingConflictNestedKeys {
				if strings.EqualFold(nestedName, candidate) {
					matched = true
					break
				}
			}
			if !matched {
				return true
			}
			if nestedSeen == nil {
				nestedSeen = make(map[string]struct{}, len(wsBillingConflictNestedKeys))
			}
			foldedNested := strings.ToLower(nestedName)
			if _, dup := nestedSeen[foldedNested]; dup {
				field, conflict = folded+"."+foldedNested, true
				return false
			}
			nestedSeen[foldedNested] = struct{}{}
			return true
		})
		return !conflict
	})
	return field, conflict
}
