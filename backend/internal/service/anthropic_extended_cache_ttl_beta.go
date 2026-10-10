package service

import (
	"bytes"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/tidwall/gjson"
)

// extended-cache-ttl beta 与 body 中 1h cache_control 的对齐。
//
// 上游要求：body 里任何 cache_control 块声明 ttl="1h" 时，anthropic-beta 必须包含
// extended-cache-ttl-2025-04-11。缺了这个 token 上游**不报错**，而是静默按默认 5m
// 建缓存——usage 里 cache_creation_1h_tokens 恒为 0、cache_creation_5m_tokens 照常增长。
// 现象就是「管理员在密钥上设了强制 1h，实际创建的仍是 5m」。
//
// 为什么以前必然踩到：把 body 改写成 1h 的 forceEphemeralCacheControlTTL 只动 body；
// 而 anthropic-beta 的两个来源都不补这个 token——computeFinalAnthropicBeta 的 apikey
// 分支只回固定集合，透传分支更是原样转发客户端 beta。OAuth 伪装路径例外，它的
// FullClaudeCodeMimicryBetas 里本来就带，所以只有 API-key 账号中招。
//
// 规则（只做加法，不改 body）：
//   - body 里没有 ephemeral + ttl="1h" 的断点 → 原样返回，其余请求零影响
//   - beta 里已经有这个 token → 不重复
//   - 管理员 beta 策略明确要丢这个 token → 尊重策略，不硬塞

// forEachCacheControl 遍历 body 中所有 cache_control 对象（顶层 / system / tools /
// messages[].content[]），回调收到的是 cache_control 本身。
func forEachCacheControl(body []byte, fn func(gjson.Result)) {
	if cc := gjson.GetBytes(body, "cache_control"); cc.IsObject() {
		fn(cc)
	}
	if system := gjson.GetBytes(body, "system"); system.IsArray() {
		system.ForEach(func(_, block gjson.Result) bool {
			if cc := block.Get("cache_control"); cc.IsObject() {
				fn(cc)
			}
			return true
		})
	}
	if tools := gjson.GetBytes(body, "tools"); tools.IsArray() {
		tools.ForEach(func(_, tool gjson.Result) bool {
			if cc := tool.Get("cache_control"); cc.IsObject() {
				fn(cc)
			}
			return true
		})
	}
	if messages := gjson.GetBytes(body, "messages"); messages.IsArray() {
		messages.ForEach(func(_, msg gjson.Result) bool {
			if content := msg.Get("content"); content.IsArray() {
				content.ForEach(func(_, block gjson.Result) bool {
					if cc := block.Get("cache_control"); cc.IsObject() {
						fn(cc)
					}
					return true
				})
			}
			return true
		})
	}
}

// requestNeedsExtendedCacheTTLBeta 报告 body 是否声明了 ephemeral + ttl="1h" 的缓存断点。
func requestNeedsExtendedCacheTTLBeta(body []byte) bool {
	if len(body) == 0 || !bytes.Contains(body, []byte(`"`+cacheTTLTarget1h+`"`)) {
		return false
	}
	needed := false
	forEachCacheControl(body, func(cc gjson.Result) {
		if cc.Get("type").String() == "ephemeral" && cc.Get("ttl").String() == cacheTTLTarget1h {
			needed = true
		}
	})
	return needed
}

// ensureExtendedCacheTTLBeta 在需要时把 extended-cache-ttl beta 追加到 anthropic-beta
// 头值上，返回新的头值；不需要时原样返回。
func ensureExtendedCacheTTLBeta(betaHeader string, body []byte, drop map[string]struct{}) string {
	if !requestNeedsExtendedCacheTTLBeta(body) {
		return betaHeader
	}
	token := claude.BetaExtendedCacheTTL
	if containsBetaToken(betaHeader, token) {
		return betaHeader
	}
	if _, dropped := drop[token]; dropped {
		return betaHeader
	}
	if strings.TrimSpace(betaHeader) == "" {
		return token
	}
	return betaHeader + "," + token
}

// applyExtendedCacheTTLBetaHeader 是透传路径用的版本：直接在已组好的上游请求头上补
// beta。客户端可能分多行发 anthropic-beta，先把所有写法合并再判断；不需要时一个字节
// 都不动，保持「原样转发」。
func applyExtendedCacheTTLBetaHeader(h http.Header, body []byte) {
	if h == nil || !requestNeedsExtendedCacheTTLBeta(body) {
		return
	}
	current := strings.Join(anthropicBetaHeaderValues(h), ",")
	next := ensureExtendedCacheTTLBeta(current, body, nil)
	if next == current {
		return
	}
	deleteHeaderAllForms(h, "anthropic-beta")
	setHeaderRaw(h, "anthropic-beta", next)
}
