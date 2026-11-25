package service

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// 本文件是 json_span_patch 改造之前的旧实现原样副本（逐次 sjson.Set*/DeleteBytes），
// 只在测试里当 oracle 和 benchmark 基线用：新实现必须在语义上与它们一致。
// 不要在生产代码里引用，也不要“顺手优化”它们，否则对照就失去意义。

func legacyEnsureDeepSeekChatReasoningPlaceholders(account *Account, body []byte) []byte {
	if !targetsDeepSeekAPIHost(account) {
		return body
	}
	messages := gjson.GetBytes(body, "messages")
	if !messages.IsArray() || len(messages.Array()) == 0 {
		return body
	}
	updated := body
	changed := false
	for i, msg := range messages.Array() {
		if strings.TrimSpace(msg.Get("role").String()) != "assistant" {
			continue
		}
		if msg.Get("reasoning_content").String() != "" {
			continue
		}
		next, err := sjson.SetBytes(updated, "messages."+strconv.Itoa(i)+".reasoning_content", deepSeekReasoningPlaceholderText)
		if err != nil {
			return body
		}
		updated = next
		changed = true
	}
	if !changed {
		return body
	}
	return updated
}

func legacyEnsureDeepSeekNativeReasoningPlaceholders(account *Account, body []byte) []byte {
	if account == nil || account.Platform != PlatformDeepseek || !account.UsesNativeCNResponses() {
		return body
	}
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() || len(input.Array()) == 0 {
		return body
	}
	placeholder := []byte(`[{"type":"reasoning_text","text":" "}]`)
	updated := body
	changed := false
	for i, item := range input.Array() {
		if strings.TrimSpace(item.Get("type").String()) != "reasoning" {
			continue
		}
		if hasDeepSeekNativeReasoningText(item.Get("content")) {
			continue
		}
		content := item.Get("content")
		// A non-array vendor shape belongs to the client; do not rewrite it.
		if content.Exists() && content.Type != gjson.Null && !content.IsArray() {
			continue
		}
		if content.IsArray() && len(content.Array()) > 0 && strings.TrimSpace(gjson.Get(item.Raw, "content.0.text").String()) != "" {
			continue
		}
		next, err := sjson.SetRawBytes(updated, "input."+strconv.Itoa(i)+".content", placeholder)
		if err != nil {
			return body
		}
		updated = next
		changed = true
	}
	if !changed {
		return body
	}
	return updated
}

func legacyEnsureDeepSeekThinkingReplayPlaceholders(body []byte, model string) ([]byte, bool) {
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "deepseek-") {
		return body, false
	}
	messages := gjson.GetBytes(body, "messages")
	if !messages.IsArray() || len(messages.Array()) == 0 {
		return body, false
	}
	updated := body
	changed := false
	for i, msg := range messages.Array() {
		if strings.TrimSpace(msg.Get("role").String()) != "assistant" {
			continue
		}
		content := msg.Get("content")
		if !content.IsArray() || len(content.Array()) == 0 || !strings.HasPrefix(content.Raw, "[") {
			continue
		}
		hasThinking, hasEmptyThinking, hasToolUse := false, false, false
		for _, block := range content.Array() {
			switch strings.TrimSpace(block.Get("type").String()) {
			case "thinking":
				hasThinking = true
				if strings.TrimSpace(block.Get("thinking").String()) == "" {
					hasEmptyThinking = true
				}
			case "redacted_thinking":
				hasThinking = true
			case "tool_use":
				hasToolUse = true
			}
		}
		if hasEmptyThinking && hasToolUse {
			blocks := make([]string, 0, len(content.Array()))
			for _, block := range content.Array() {
				raw := block.Raw
				if strings.TrimSpace(block.Get("type").String()) == "thinking" &&
					strings.TrimSpace(block.Get("thinking").String()) == "" {
					if fixed, err := sjson.SetBytes([]byte(raw), "thinking", deepSeekThinkingPlaceholderText); err == nil {
						raw = string(fixed)
					}
				}
				blocks = append(blocks, raw)
			}
			rewritten := "[" + strings.Join(blocks, ",") + "]"
			next, err := sjson.SetRawBytes(updated, fmt.Sprintf("messages.%d.content", i), []byte(rewritten))
			if err != nil {
				continue
			}
			updated = next
			changed = true
			continue
		}
		if hasThinking || !hasToolUse {
			continue
		}
		placeholder := `{"type":"thinking","thinking":"` + deepSeekThinkingPlaceholderText + `"}`
		rewritten := "[" + placeholder + "," + strings.TrimPrefix(content.Raw, "[")
		next, err := sjson.SetRawBytes(updated, fmt.Sprintf("messages.%d.content", i), []byte(rewritten))
		if err != nil {
			continue
		}
		updated = next
		changed = true
	}
	if !changed {
		return body, false
	}
	return updated, true
}

func legacyDropOrphanDeepSeekToolMessages(body []byte) []byte {
	messages := gjson.GetBytes(body, "messages")
	if !messages.IsArray() {
		return body
	}
	announced := make(map[string]struct{})
	kept := make([]string, 0, len(messages.Array()))
	changed := false
	for _, m := range messages.Array() {
		switch m.Get("role").String() {
		case "assistant":
			for _, tc := range m.Get("tool_calls").Array() {
				if id := tc.Get("id").String(); id != "" {
					announced[id] = struct{}{}
				}
			}
		case "tool":
			id := m.Get("tool_call_id").String()
			if _, ok := announced[id]; id == "" || !ok {
				changed = true
				continue
			}
		}
		kept = append(kept, m.Raw)
	}
	if !changed {
		return body
	}
	out, err := sjson.SetRawBytes(body, "messages", []byte("["+strings.Join(kept, ",")+"]"))
	if err != nil {
		return body
	}
	return out
}

func legacyNormalizeDeepSeekChatMessages(body []byte) []byte {
	raw := gjson.GetBytes(body, "messages")
	if !raw.IsArray() {
		return body
	}
	var messages []apicompat.ChatMessage
	if err := json.Unmarshal([]byte(raw.Raw), &messages); err != nil {
		return body
	}
	normalized := apicompat.NormalizeChatMessages(messages)
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return body
	}
	out, err := sjson.SetRawBytes(body, "messages", encoded)
	if err != nil {
		return body
	}
	return out
}

func legacyRepairDeepSeekMissingToolOutputs(body []byte) ([]byte, bool, error) {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return body, false, nil
	}
	if gjson.GetBytes(body, "previous_response_id").String() != "" {
		return body, false, nil
	}
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return body, false, nil
	}
	paired, changed := legacyDeepSeekPairMissingToolOutputs(input.Array())
	if !changed {
		return body, false, nil
	}
	raw, err := json.Marshal(paired)
	if err != nil {
		return body, false, err
	}
	next, err := sjson.SetRawBytes(body, "input", raw)
	if err != nil {
		return body, false, err
	}
	return next, true, nil
}

func legacyDeepSeekPairMissingToolOutputs(input []gjson.Result) ([]json.RawMessage, bool) {
	answered := make(map[string]struct{}, len(input))
	for _, item := range input {
		if !isDeepSeekToolOutputType(item.Get("type").String()) {
			continue
		}
		if callID := item.Get("call_id").String(); callID != "" {
			answered[callID] = struct{}{}
		}
	}

	out := make([]json.RawMessage, 0, len(input))
	type pendingCall struct {
		callID   string
		callType string
	}
	pending := make([]pendingCall, 0, 4)
	flush := func() bool {
		if len(pending) == 0 {
			return false
		}
		for _, call := range pending {
			raw, err := json.Marshal(map[string]string{
				"type":    deepSeekToolOutputType(call.callType),
				"call_id": call.callID,
				"output":  deepSeekMissingToolOutput,
			})
			if err != nil {
				continue
			}
			out = append(out, raw)
		}
		pending = pending[:0]
		return true
	}

	changed := false
	for _, item := range input {
		itemType := item.Get("type").String()
		if isDeepSeekToolCallType(itemType) {
			out = append(out, json.RawMessage(item.Raw))
			callID := item.Get("call_id").String()
			if callID != "" {
				if _, ok := answered[callID]; !ok {
					answered[callID] = struct{}{}
					pending = append(pending, pendingCall{callID: callID, callType: itemType})
				}
			}
			continue
		}
		if isDeepSeekToolOutputType(itemType) {
			out = append(out, json.RawMessage(item.Raw))
			continue
		}
		if flush() {
			changed = true
		}
		out = append(out, json.RawMessage(item.Raw))
	}
	if flush() {
		changed = true
	}
	return out, changed
}

func legacyEnsureOpenAIChatStreamUsage(body []byte) ([]byte, error) {
	updated, err := sjson.SetBytes(body, "stream_options.include_usage", true)
	if err != nil {
		return body, err
	}
	return updated, nil
}
