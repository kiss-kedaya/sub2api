package service

import (
	"strings"

	"github.com/tidwall/gjson"
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

// JSON fragments the patches write, built from the placeholder text.
const (
	deepSeekThinkingPlaceholderJSON   = `"` + deepSeekThinkingPlaceholderText + `"`
	deepSeekThinkingPlaceholderMember = `"thinking":` + deepSeekThinkingPlaceholderJSON
	// Inserted in front of the first block of a non-empty content array.
	deepSeekThinkingPlaceholderBlock = `{"type":"thinking","thinking":` + deepSeekThinkingPlaceholderJSON + `},`
)

// ensureDeepSeekThinkingReplayPlaceholders inserts the placeholder into
// assistant messages of a passback-required DeepSeek model. Non-DeepSeek models
// and bodies without an assistant tool-call turn are returned unchanged.
//
// One scan collects every edit as a span patch and the body is written once;
// the previous per-message sjson.SetRawBytes copied the whole body per turn.
func ensureDeepSeekThinkingReplayPlaceholders(body []byte, model string) ([]byte, bool) {
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "deepseek-") {
		return body, false
	}
	messages := gjson.Get(openAIWSPayloadStringView(body), "messages")
	if !messages.IsArray() {
		return body, false
	}
	patch := newJSONSpanPatcher(body)
	messages.ForEach(func(_, msg gjson.Result) bool {
		if strings.TrimSpace(msg.Get("role").String()) != "assistant" {
			return true
		}
		content := msg.Get("content")
		if !content.IsArray() {
			return true
		}
		blocks, hasThinking, hasEmptyThinking, hasToolUse := 0, false, false, false
		content.ForEach(func(_, block gjson.Result) bool {
			blocks++
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
			return true
		})
		if blocks == 0 {
			return true
		}
		if hasEmptyThinking && hasToolUse {
			// The client kept the thinking slot but blanked its text. Fill the
			// text instead of adding a second block.
			content.ForEach(func(_, block gjson.Result) bool {
				if strings.TrimSpace(block.Get("type").String()) != "thinking" {
					return true
				}
				thinking := block.Get("thinking")
				if strings.TrimSpace(thinking.String()) != "" {
					return true
				}
				if thinking.Exists() {
					patch.replace(thinking, deepSeekThinkingPlaceholderJSON)
				} else {
					patch.appendMember(block, deepSeekThinkingPlaceholderMember)
				}
				return true
			})
			return true
		}
		if hasThinking || !hasToolUse {
			return true
		}
		patch.insertAfterOpen(content, deepSeekThinkingPlaceholderBlock)
		return true
	})
	if out, changed := patch.apply(); changed {
		return out, true
	}
	return body, false
}
