package service

import (
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// claudeAdaptiveThinkingMinMinor 是「上游只认 adaptive」的 Claude 5.x 最小次版本号。
//
// 实测上游原文（2026-09-30 / 10-01，6h 120 次，14 个用户，5 个 apikey 直连账号）：
//
//	claude-opus-5-5 requires adaptive thinking; omit thinking or use
//	thinking.type=adaptive and output_config.effort
//
//	claude-sonnet-5-5 requires adaptive thinking or thinking.type=between_tools;
//	omit thinking or use one of those modes
//
//	"thinking.type.enabled" is not supported for this model. Use
//	"thinking.type.adaptive" and "output_config.effort" to control thinking behavior.
const claudeAdaptiveThinkingMinMinor = 5

// anthropicModelRequiresAdaptiveThinking 判断直连 Anthropic 的模型是否已经拒绝
// thinking.type="enabled"。
//
// 只认有上游原文的型号族，不做「版本越大越严」的外推：
//   - claude-fable-5 / claude-fable-5-1：enabled 直接 400，且连 disabling 都不允许
//     （always-on adaptive）
//   - claude-{opus,sonnet}-5.5 及以后：requires adaptive thinking
//
// claude-opus-5（主版本 5、次版本 0）**故意不在**其中：它的 400 是
// ".enabled.budget_tokens: Field required"，说明 enabled 仍然可用，只是必须带预算，
// 转成 adaptive 反而会改变客户端明确指定的行为。
func anthropicModelRequiresAdaptiveThinking(modelID string) bool {
	if modelID == "" {
		return false
	}
	lower := strings.ToLower(modelID)
	if strings.Contains(lower, "claude-fable-5") {
		return true
	}
	// claudeVersionRe 未锚定，因此 "anthropic/claude-opus-5.5" 这类带厂商前缀的
	// 别名同样能命中。
	matches := claudeVersionRe.FindStringSubmatch(lower)
	if matches == nil {
		return false
	}
	major, _ := strconv.Atoi(matches[1])
	minor, _ := strconv.Atoi(matches[2])
	return major == 5 && minor >= claudeAdaptiveThinkingMinMinor
}

// sanitizeAnthropicThinkingForModel 修正直连 Anthropic 路径上的 thinking 协议漂移：
// 对已不接受 "enabled" 的模型把 thinking.type 换成 "adaptive"。
//
// 为什么连 budget_tokens 一起删：
//   - adaptive 的语义是「由模型自决思考预算」，budget_tokens 对它没有作用，删掉不丢语义；
//   - 上游把 adaptive 和 output_config.effort 组合在一起描述，没有定义 adaptive 下的
//     budget_tokens，留着只多一个被拒的可能。
//
// 只动 thinking 一个对象，其余字段字节级原样保留；无命中时返回原切片。
func sanitizeAnthropicThinkingForModel(body []byte, modelID string) []byte {
	if len(body) == 0 || !anthropicModelRequiresAdaptiveThinking(modelID) {
		return body
	}

	thinking := gjson.GetBytes(body, "thinking")
	if !thinking.IsObject() || thinking.Get("type").String() != "enabled" {
		return body
	}

	out := body
	changed := false
	if next, err := sjson.SetBytes(out, "thinking.type", "adaptive"); err == nil {
		out = next
		changed = true
	}
	if thinking.Get("budget_tokens").Exists() {
		if next, err := sjson.DeleteBytes(out, "thinking.budget_tokens"); err == nil {
			out = next
			changed = true
		}
	}
	if !changed {
		return body
	}
	return out
}
