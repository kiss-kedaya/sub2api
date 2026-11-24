package service

// Claude 模型跨平台准入闸门。
//
// 漏洞复现（第三方 + 生产库确认）：0.12 倍率的 Grok 分组 Key（group#25）通过
// Anthropic /v1/messages 入口请求 claude-opus-4-8 / claude-sonnet-4-5：
//  1. /v1/messages 由路由按“分组平台”分派，grok 分组落到 OpenAI 兼容网关，其
//     平台覆盖被钉成 grok，调度只在 grok 账号里找；
//  2. Grok 账号空映射会被填入含 claude-* 通配的默认映射（xai.DefaultModelMapping，
//     受 grok_cross_client_model_map_enabled 控制且默认开启），账号据此被判定
//     “支持”该模型，请求经 Anthropic→OpenAI 转换真实打到 grok 上游并返回 200；
//  3. 同一模型走 /v1/chat/completions 却返回 404——两条入口判定口径不一致。
//
// 修复口径：claude-* 请求落到“平台本身不能服务 Claude”的上游（grok/CN 供应商/
// typeSafe）时必须被拒绝，除非分组显式开启 cross_protocol_conversion_enabled。
// Anthropic 原生入口、非 Claude 模型、Anthropic/passthrough 账号一律放行。
//
// 判定输入的“平台”必须是**本次请求实际使用分组的上游平台**，不能是入站协议：
// /v1/messages 入站协议是 anthropic，但 grok 分组实际由 OpenAI 兼容网关的
// platformOverride 钉成 grok 去调度。用入站协议判定会让闸门恒不收紧（曾在
// 早期实现中出现过，漏洞因此没被堵住）。

import (
	"context"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

// claudeModelAdmission 是闸门的核心判定（纯函数，便于直接单测）。
//
//	model       本次请求的模型名；
//	platform    本次请求实际使用分组的上游平台（空 = 未知，不收紧）；
//	group       本次请求实际使用的分组（读 cross_protocol_conversion_enabled）；
//	account     候选账号。
//
// 返回 true 表示该账号必须被拒绝。
func claudeModelAdmission(model, platform string, group *Group, account *Account) bool {
	// 非 Claude 模型不进闸门。
	if !isClaudeModel(model) {
		return false
	}
	// 分组显式开启跨协议转换：闸门不否决该分组（尊重运维部署）。
	if group != nil && group.CrossProtocolConversionEnabled {
		return false
	}
	// 账号平台本身能服务 Claude（anthropic / passthrough / nil）时不收紧。
	if account == nil || upstreamAccountServesClaudeFamily(account) {
		return false
	}
	upstreamPlatform := strings.TrimSpace(platform)
	if upstreamPlatform == "" && group != nil {
		upstreamPlatform = strings.TrimSpace(group.Platform)
	}
	// 上游平台本身能服务 Claude（anthropic/openai/gemini/antigravity…）或未知时不收紧。
	return !upstreamPlatformServesClaudeFamily(upstreamPlatform)
}

// claudeAdmissionBlocksAccount 是调度/诊断热路径的包装：语义与 claudeModelAdmission 相同。
func claudeAdmissionBlocksAccount(model, platform string, group *Group, account *Account) bool {
	return claudeModelAdmission(model, platform, group, account)
}

// shouldGateClaudeModel 报告该模型是否需要统一“能否被该上游服务”的口径。
func shouldGateClaudeModel(model string) bool {
	return isClaudeModel(model)
}

// schedulerClaudeGateContext 由调度入口（GatewayService / OpenAIGatewayService）
// 装进请求 ctx，供逐账号候选过滤时判定准入。字段固定为模型与上游平台，
// 不承载可变状态，避免不同调度尝试之间互相影响。
type schedulerClaudeGateContextKey struct{}

type schedulerClaudeGate struct {
	model    string
	platform string
}

// WithSchedulerClaudeModelGate 在调度 ctx 上开启 Claude 模型跨上游准入判定。
// platform 必须是本次请求实际分组的上游平台（不是入站协议）。
// model 非 Claude 族时不做任何事（返回原 ctx，非 Claude 请求零变化）。
func WithSchedulerClaudeModelGate(ctx context.Context, platform, model string) context.Context {
	if ctx == nil {
		return ctx
	}
	if !shouldGateClaudeModel(model) {
		return ctx
	}
	return context.WithValue(ctx, schedulerClaudeGateContextKey{}, schedulerClaudeGate{
		model:    strings.TrimSpace(model),
		platform: strings.TrimSpace(platform),
	})
}

func schedulerClaudeGateFromContext(ctx context.Context) (schedulerClaudeGate, bool) {
	if ctx == nil {
		return schedulerClaudeGate{}, false
	}
	gate, ok := ctx.Value(schedulerClaudeGateContextKey{}).(schedulerClaudeGate)
	if !ok || gate.model == "" {
		return schedulerClaudeGate{}, false
	}
	return gate, true
}

// schedulerClaudeGateAllows 是调度热路径的统一入口：ctx 未装闸门时恒放行。
// group 传调度过程中的实际分组（可能已因 Claude Code 限制/回退被替换）。
func schedulerClaudeGateAllows(ctx context.Context, group *Group, account *Account) bool {
	gate, ok := schedulerClaudeGateFromContext(ctx)
	if !ok {
		return true
	}
	return !claudeModelAdmission(gate.model, gate.platform, group, account)
}

// upstreamPlatformForClaudeGate 解析“本次请求实际使用的上游平台”。
//
// 顺序与 handler 的 effectiveAPIKeyPlatform 一致：分组平台优先（OpenAI 兼容
// 网关的 platformOverride 就是分组平台），composite 分组再退到已解析目标平台。
// group 为 nil 时读 ctx 里的分组对象（ctxkey.Group）。
func upstreamPlatformForClaudeGate(ctx context.Context, group *Group) string {
	if group == nil && ctx != nil {
		if ctxGroup, ok := ctx.Value(ctxkey.Group).(*Group); ok {
			group = ctxGroup
		}
	}
	if group != nil {
		if platform := strings.TrimSpace(group.Platform); platform != "" && platform != PlatformComposite {
			return platform
		}
	}
	if platform, ok := ResolvedTargetPlatformFromContext(ctx); ok {
		return strings.TrimSpace(platform)
	}
	return ""
}
