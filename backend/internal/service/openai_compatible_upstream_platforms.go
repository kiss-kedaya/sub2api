package service

import "strings"

// openAICompatibleUpstreamPlatform 返回 p 是否属于“经 OpenAI 兼容网关转发”的
// 上游平台集合。它是 api_key_routes.go / openai_key_routes.go /
// openai_gateway_scheduling.go 三处 switch 的唯一权威来源，避免新增平台时
// 路由、调度与准入各自维护一份名单而漂移。
func openAICompatibleUpstreamPlatform(p string) bool {
	switch p {
	case PlatformOpenAI, PlatformGrok, PlatformKimi, PlatformZhipu,
		PlatformDeepseek, PlatformMiniMax, PlatformCline, PlatformCommandCode:
		return true
	default:
		return false
	}
}

// IsOpenAICompatibleUpstreamPlatform 是 openAICompatibleUpstreamPlatform 的
// 导出入口，语义同上（api_key_routes.go 保留同名导出函数，此处不重复声明）。

// upstreamOpenAICompatModelPolicy 描述“该上游平台/账号实际能服务哪些模型族”，
// 用于跨平台准入与计费锚点校正。
//
// 说明：平台名只说明走哪套转发骨架（OpenAI 兼容线格式），不说明它接受了哪些
// 模型族。Grok/Kimi/Zhipu/DeepSeek/MiniMax 等供应商账号只会服务自家模型；
// 只有 openai 平台（含开启透传的 OpenAI 账号）和显式开启跨协议转换的分组才
// 允许 claude-* 请求转发过去（Anthropic → OpenAI 转换）。据此判定：
//
//   - 准入：非 Anthropic 平台账号不得被选择去服务 claude-* 请求，除非分组
//     显式开启跨协议转换（此时判定退化为“账号模型映射是否声明支持该模型”，
//     由模型可用性诊断负责给出 404/503）。
//   - 计费：被非 Claude 上游实际服务时，请求不得锚定该分组的供应商档价。
//
// 平台级策略只对“明确会拒收 claude-* 的供应商账号”生效；openai/antigravity/
// gemini/anthropic/unknown 平台一律返回“不限制”，不改变它们现有的
// account.model_mapping 语义。
func upstreamPlatformServesClaudeFamily(p string) bool {
	switch strings.TrimSpace(p) {
	case PlatformGrok, PlatformKimi, PlatformZhipu, PlatformDeepseek,
		PlatformMiniMax, PlatformCline, PlatformCommandCode, PlatformTypeSafe:
		return false
	default:
		return true
	}
}

// upstreamAccountServesClaudeFamily 是 upstreamPlatformServesClaudeFamily 的
// 账号维度版本：passthrough 账号显式声明可服务任意模型，不做平台级否决。
func upstreamAccountServesClaudeFamily(account *Account) bool {
	if account == nil {
		return true
	}
	if accountPassthroughsAllModels(account) {
		return true
	}
	return upstreamPlatformServesClaudeFamily(account.Platform)
}

// modelFamily isClaudeModel 报告请求模型是否属于 Claude（Anthropic）模型族。
// 复用 DetectModelPlatform 的唯一判定口径：claude-* / anthropic.claude-* /
// 以及 provider 前缀形式（anthropic/xxx、claude/xxx）。
func isClaudeModel(model string) bool {
	detected, ok := DetectModelPlatform(model)
	return ok && detected == PlatformAnthropic
}

// crossProtocolAllowsClaudeOnUpstream 报告分组是否显式允许跨协议转换，
// 从而允许 claude-* 请求落到非 Anthropic 上游账号。分组未加载视为不允许
// （fail-closed）：调用方此时应改用模型可用性诊断给出的 404 分类。
func crossProtocolAllowsClaudeOnUpstream(group *Group) bool {
	return group != nil && group.CrossProtocolConversionEnabled
}
