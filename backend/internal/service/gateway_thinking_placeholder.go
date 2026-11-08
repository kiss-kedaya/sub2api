package service

import (
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// DeepSeek's Anthropic-compatible endpoint enforces the thinking round-trip:
// an assistant turn that produced a tool call must replay its thinking block,
// otherwise the upstream rejects the whole request with
// "The `content[].thinking` in the thinking mode must be passed back to the API".
//
// Clients (Claude Code included) drop thinking blocks when they rewrite history,
// so the slot is empty by the time the request reaches us. A single-space
// placeholder satisfies the check, and the upstream ignores the text: the probe
// on 2026-11-08 showed the same tool-call turn returns 400 without it and 200
// with it. Only assistant messages that already carry a tool_use block are
// touched; text-only assistant turns and real thinking blocks are left alone.
const deepSeekThinkingPlaceholderText = " "

// ensureDeepSeekThinkingReplayPlaceholders inserts the placeholder into
// assistant messages of a passback-required DeepSeek model. Non-DeepSeek models
// and bodies without an assistant tool-call turn are returned unchanged.
func ensureDeepSeekThinkingReplayPlaceholders(body []byte, model string) ([]byte, bool) {
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
			// The client kept the thinking slot but blanked its text. Fill the
			// text instead of adding a second block.
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
