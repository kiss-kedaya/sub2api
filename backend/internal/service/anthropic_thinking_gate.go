package service

import (
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// claudeAdaptiveThinkingMinMinor 是「上游只认 adaptive」的 Claude 5.x 最小次版本号。
//
// 上游协议错误原文：
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
// 只把上游已不再接受的 thinking.type="enabled" 换成 "adaptive"（保留「开思考」语义）。
//
// 为什么连 budget_tokens 一起删：
//   - adaptive 的语义是「由模型自决思考预算」，budget_tokens 对它没有作用，删掉不丢语义；
//   - 上游把 adaptive 和 output_config.effort 组合在一起描述，没有定义 adaptive 下的
//     budget_tokens，留着只多一个被拒的可能。
//
// disabled / between_tools 一律原样返回，不改写。2026-11-20 对上游实测：
//   - 省略 thinking 不等于关闭思考：5.5 代模型省略 thinking 的默认语义是 adaptive，
//     思考是开着的。因此不能把 disabled 删成「省略」——那会把用户明确关掉的思考
//     重新打开，上游还会按 thinking token 计费（opus-5-5 / fable-5-2：直发 disabled
//     是 400，删成省略后是 200 且 thinking_tokens > 0）。
//   - haiku-5-5 本来就接受 disabled（直发 disabled + effort=high 是 200 且不思考）。
//   - between_tools 是「不思考」的另一种表达，转成 adaptive 同样会把思考打开；
//     不支持的型号交给上游回 400 文案。
//
// 上游对这两种取值本来就有明确的 400 文案（告诉客户端用 adaptive / effort /
// between_tools），原样放行客户端才能拿到与官方一致的行为；网关只修「上游不再接受
// enabled」这一种漂移。
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
