package handler

import (
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/tidwall/gjson"
)

// ValidateStrictOpusMessagesRequest 校验 claude-opus-5-5 的 /v1/messages 请求体。
//
// 口径（2026-10-10）：该模型对非法参数直接 400，不得静默修正后再转发。
//
//	max_tokens 缺失或 < 1        -> max_tokens: Field required
//	temperature 非数字 / 越界     -> temperature: range: 0..1
//	type=text 缺 text 或全空白    -> messages.<i>.content.<j>.text: text content blocks must contain non-whitespace text
//	messages 缺失或空数组         -> field messages is required
//	开 thinking 又传非默认采样    -> temperature/top_p/top_k may only be set to <默认值> when thinking is enabled
//
// 返回 ("", true) 放行；否则返回文案，调用方以 400 + invalid_request_error 回复。
//
// 必须在拿到客户端原始请求体后、任何默认值注入之前调用。OAuth 归一化会在
// service 层补 max_tokens=128000 / temperature=1，那属于本函数之后的行为，
// 不能当作放行理由。
func ValidateStrictOpusMessagesRequest(model string, body []byte) (string, bool) {
	if !claude.IsOpus55(model) {
		return "", true
	}
	if len(body) == 0 {
		return "field messages is required", false
	}

	messages := gjson.GetBytes(body, "messages")
	if !messages.IsArray() || len(messages.Array()) == 0 {
		return "field messages is required", false
	}

	maxTokens := gjson.GetBytes(body, "max_tokens")
	if !maxTokens.Exists() || maxTokens.Type != gjson.Number || maxTokens.Float() < 1 {
		return "max_tokens: Field required", false
	}

	if temperature := gjson.GetBytes(body, "temperature"); temperature.Exists() {
		if temperature.Type != gjson.Number || temperature.Float() < 0 || temperature.Float() > 1 {
			return "temperature: range: 0..1", false
		}
	}

	for i, message := range messages.Array() {
		content := message.Get("content")
		if !content.IsArray() {
			continue
		}
		for j, block := range content.Array() {
			if block.Get("type").String() != "text" {
				continue
			}
			// text 缺失、非字符串、或只有空白都属于同一类：Anthropic 拒收空文本块。
			if strings.TrimSpace(block.Get("text").String()) == "" {
				return fmt.Sprintf(
					"messages.%d.content.%d.text: text content blocks must contain non-whitespace text", i, j), false
			}
		}
	}

	// 开启 thinking 时不允许同时指定非默认采样参数。这里只做拒绝，不改写请求。
	if thinkingType := strings.TrimSpace(gjson.GetBytes(body, "thinking.type").String()); thinkingType == "enabled" || thinkingType == "adaptive" {
		if temperature := gjson.GetBytes(body, "temperature"); temperature.Exists() && temperature.Float() != 1 {
			return "temperature may only be set to 1 when thinking is enabled", false
		}
		if topP := gjson.GetBytes(body, "top_p"); topP.Exists() && topP.Float() != 1 {
			return "top_p may only be set to 1 when thinking is enabled", false
		}
		if topK := gjson.GetBytes(body, "top_k"); topK.Exists() && topK.Float() != 0 {
			return "top_k may only be set to 0 when thinking is enabled", false
		}
	}

	return "", true
}
