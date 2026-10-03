package service

import (
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/tidwall/gjson"
)

// thinking.display="updates" 与 anthropic-beta 的对齐。
//
// 官方在请求 body 写 thinking.display="updates" 时，要求 anthropic-beta 里带
// thinking-display-updates-2026-08-18；缺了只接受 summarized / omitted，回 400：
//
//	thinking.display: Input should be 'summarized', 'omitted'
//
// 网关此前对 anthropic-beta 只做两件事：透传路径原样转发客户端值、OAuth 伪装路径
// 整份替换成固定的 Claude Code beta 集合——两者都不会按 body 补这个 token，
// 后者甚至会把客户端自己带的也替换掉。于是客户端只要写了 display=updates，
// 这一请求就必然 400。
//
// 规则（只做加法，不改 body）：
//   - body 的 thinking.display 不是 "updates" → 原样返回，其余请求零影响。
//   - beta 里已经有这个 token → 原样返回，不重复。
//   - 管理员 beta 策略的 drop 集合里明确要丢这个 token → 尊重策略，不硬塞。
//   - 其余情况追加到末尾。

// requestNeedsThinkingDisplayUpdatesBeta 报告 body 是否声明了 thinking.display="updates"。
func requestNeedsThinkingDisplayUpdatesBeta(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	display := gjson.GetBytes(body, "thinking.display")
	if !display.Exists() || display.Type != gjson.String {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(display.String()), "updates")
}

// ensureThinkingDisplayUpdatesBeta 在需要时把 thinking-display-updates beta 追加到
// anthropic-beta 头值上，返回新的头值；不需要时原样返回。
func ensureThinkingDisplayUpdatesBeta(betaHeader string, body []byte, drop map[string]struct{}) string {
	if !requestNeedsThinkingDisplayUpdatesBeta(body) {
		return betaHeader
	}
	token := claude.BetaThinkingDisplayUpdates
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

// applyThinkingDisplayUpdatesBetaHeader 是透传路径用的版本：直接在已组好的上游
// 请求头上补 beta。客户端可能分多行发 anthropic-beta，这里先把所有写法（原样小写 /
// 规范大小写）的值按逗号合并再判断；只有确实需要追加时才把它们收成一行，
// 不需要时一个字节都不动，保持"原样转发"。
func applyThinkingDisplayUpdatesBetaHeader(h http.Header, body []byte) {
	if h == nil || !requestNeedsThinkingDisplayUpdatesBeta(body) {
		return
	}
	current := strings.Join(anthropicBetaHeaderValues(h), ",")
	next := ensureThinkingDisplayUpdatesBeta(current, body, nil)
	if next == current {
		return
	}
	deleteHeaderAllForms(h, "anthropic-beta")
	setHeaderRaw(h, "anthropic-beta", next)
}

func anthropicBetaHeaderValues(h http.Header) []string {
	keys := []string{"anthropic-beta", resolveWireCasing("anthropic-beta"), http.CanonicalHeaderKey("anthropic-beta")}
	seen := make(map[string]struct{}, len(keys))
	var out []string
	for _, key := range keys {
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		for _, v := range h[key] {
			if v = strings.TrimSpace(v); v != "" {
				out = append(out, v)
			}
		}
	}
	return out
}
