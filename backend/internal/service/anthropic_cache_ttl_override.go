package service

import (
	"context"

	"github.com/gin-gonic/gin"
)

// Anthropic 缓存 TTL 注入的「密钥级覆盖」解析。
//
// 背景：管理员设置页有一个全站开关 enable_anthropic_cache_ttl_1h_injection，
// 打开后所有 Anthropic OAuth/SetupToken 出站请求里已存在的 ephemeral
// cache_control 断点会被强制改写成 1h。它是全站单值，同一个站上无法同时满足
// 「长会话要 1h」和「短会话要 5m」两类客户。
//
// 本文件把这件事下推到 API 密钥：每把密钥可以声明 inherit / off / 1h / 5m。
//
// 优先级（避免与全局开关冲突的关键）：
//
//	密钥 = inherit  -> 完全走既有全局逻辑（含 account 类型限制），行为一字不变
//	密钥 = off      -> 本请求不做任何 TTL 注入与改写
//	密钥 = 1h / 5m  -> 强制改写本请求所有 ephemeral 断点的 ttl
//
// 密钥级是「覆盖」而不是「合并」：它不读取、也不写回全局设置，因此两个入口
// 不会互相污染。管理员改全局开关时，inherit 的密钥立即跟随变化。
//
// 生效范围：仅当请求最终落在 Anthropic 平台时。判定依次看
//  1. 调度出来的账号平台
//  2. 智能路由解析出的目标平台
//  3. 密钥所属分组的平台
//
// 三者任一为 anthropic 即认为命中；OpenAI / Grok / CN 供应商分组不做任何改写。

// anthropicTargetForRequest 报告本次请求的目标平台是否为 Anthropic。
func anthropicTargetForRequest(c *gin.Context, account *Account) bool {
	if account != nil && account.Platform == PlatformAnthropic {
		return true
	}
	if c == nil {
		return false
	}
	if c.Request != nil {
		if platform, ok := ResolvedTargetPlatformFromContext(c.Request.Context()); ok && platform == PlatformAnthropic {
			return true
		}
	}
	if apiKey := getAPIKeyFromContext(c); apiKey != nil && apiKey.Group != nil && apiKey.Group.Platform == PlatformAnthropic {
		return true
	}
	return false
}

// resolveAnthropicCacheTTLMode 返回本次请求生效的 TTL 模式，以及是否需要执行
// 「强制改写所有 ephemeral 断点」。
//
// 返回 (mode, apply)：
//   - apply=false 表示不改写（off，或全局关且密钥 inherit，或请求目标不是 Anthropic）
//   - apply=true  时 mode 一定是 1h 或 5m（inherit 不会出现在 apply=true 的情况里）
func (s *GatewayService) resolveAnthropicCacheTTLMode(ctx context.Context, c *gin.Context, account *Account) (string, bool) {
	apiKey := getAPIKeyFromContext(c)
	mode := AnthropicCacheTTLModeInherit
	if apiKey != nil {
		mode = NormalizeAnthropicCacheTTLMode(apiKey.AnthropicCacheTTLMode)
	}

	if mode == AnthropicCacheTTLModeInherit {
		// 存量行为：只有 Anthropic OAuth/SetupToken + 全局开关打开才注入 1h。
		if !s.shouldInjectAnthropicCacheTTL1h(ctx, account) {
			return AnthropicCacheTTLModeInherit, false
		}
		return AnthropicCacheTTLMode1h, true
	}

	// 密钥级覆盖只对 Anthropic 目标生效；其它平台保持原样，避免误改 OpenAI 侧。
	if !anthropicTargetForRequest(c, account) {
		return AnthropicCacheTTLModeInherit, false
	}
	if mode == AnthropicCacheTTLModeOff {
		return AnthropicCacheTTLModeOff, false
	}
	return mode, true
}
