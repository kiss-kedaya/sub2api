package service

import (
	"net/url"
	"strings"

	"github.com/tidwall/gjson"
)

// DeepSeek thinking mode rejects a replayed assistant turn that produced
// reasoning but carries no plaintext reasoning. The official sub2api build
// fills the gap with a single-space placeholder at the shared Chat Completions
// egress and lets the native Responses path keep what the client sent. This
// fork replays reasoning from a Redis cache; when the cache misses (eviction,
// another key, an item that was never recorded) the history would otherwise
// reach DeepSeek with an empty reasoning slot and get a hard 400.
//
// A single space is what DeepSeek accepts (it rejects the empty string) and is
// what LiteLLM uses. Real plaintext always wins: the placeholder only fills a
// slot that is still empty after the cache attempt.
const deepSeekReasoningPlaceholderText = " "

// targetsDeepSeekAPIHost reports whether the account's real upstream follows
// DeepSeek thinking-mode semantics. platform=deepseek covers our relay accounts;
// platform=openai with base_url on api.deepseek.com is the classic mapped
// account shape (GPT model name → DeepSeek upstream).
func targetsDeepSeekAPIHost(account *Account) bool {
	if account == nil {
		return false
	}
	if account.Platform == PlatformDeepseek {
		return true
	}
	u, err := url.Parse(strings.TrimSpace(account.GetOpenAIBaseURL()))
	if err != nil || u.Hostname() == "" {
		return false
	}
	ds, err := url.Parse(DefaultDeepseekBaseURL)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Hostname(), ds.Hostname())
}

// JSON fragments the placeholder patches write. They are built from the
// placeholder text so the constant stays the single source of truth.
const (
	deepSeekReasoningPlaceholderJSON   = `"` + deepSeekReasoningPlaceholderText + `"`
	deepSeekReasoningPlaceholderMember = `"reasoning_content":` + deepSeekReasoningPlaceholderJSON
	deepSeekNativeReasoningContentJSON = `[{"type":"reasoning_text","text":` + deepSeekReasoningPlaceholderJSON + `}]`
	deepSeekNativeReasoningMember      = `"content":` + deepSeekNativeReasoningContentJSON
)

// ensureDeepSeekChatReasoningPlaceholders fills an empty reasoning_content on
// every assistant message. Non-empty plaintext and non-DeepSeek upstreams are
// returned byte-for-byte unchanged.
//
// One scan collects every fill as a span patch and the body is written once.
// The previous per-message sjson.SetBytes copied the whole body for each
// assistant turn (O(messages × body)).
func ensureDeepSeekChatReasoningPlaceholders(account *Account, body []byte) []byte {
	if !targetsDeepSeekAPIHost(account) {
		return body
	}
	messages := gjson.Get(openAIWSPayloadStringView(body), "messages")
	if !messages.IsArray() {
		return body
	}
	patch := newJSONSpanPatcher(body)
	messages.ForEach(func(_, msg gjson.Result) bool {
		if strings.TrimSpace(msg.Get("role").String()) != "assistant" {
			return true
		}
		reasoning := msg.Get("reasoning_content")
		if reasoning.String() != "" {
			return true
		}
		if reasoning.Exists() {
			patch.replace(reasoning, deepSeekReasoningPlaceholderJSON)
		} else {
			patch.appendMember(msg, deepSeekReasoningPlaceholderMember)
		}
		return true
	})
	if out, changed := patch.applyIfValid(messages); changed {
		return out
	}
	return body
}

// ensureDeepSeekNativeReasoningPlaceholders is the native Responses analogue:
// a reasoning input item that still has no reasoning_text after the cache
// attempt gets a single-space placeholder. Items with client-provided text and
// items of other types are untouched.
func ensureDeepSeekNativeReasoningPlaceholders(account *Account, body []byte) []byte {
	if account == nil || account.Platform != PlatformDeepseek || !account.UsesNativeCNResponses() {
		return body
	}
	input := gjson.Get(openAIWSPayloadStringView(body), "input")
	if !input.IsArray() {
		return body
	}
	patch := newJSONSpanPatcher(body)
	input.ForEach(func(_, item gjson.Result) bool {
		if strings.TrimSpace(item.Get("type").String()) != "reasoning" {
			return true
		}
		content := item.Get("content")
		if hasDeepSeekNativeReasoningText(content) {
			return true
		}
		// A non-array vendor shape belongs to the client; do not rewrite it.
		if content.Exists() && content.Type != gjson.Null && !content.IsArray() {
			return true
		}
		if content.IsArray() && len(content.Array()) > 0 && strings.TrimSpace(gjson.Get(item.Raw, "content.0.text").String()) != "" {
			return true
		}
		if content.Exists() {
			patch.replace(content, deepSeekNativeReasoningContentJSON)
		} else {
			patch.appendMember(item, deepSeekNativeReasoningMember)
		}
		return true
	})
	if out, changed := patch.applyIfValid(input); changed {
		return out
	}
	return body
}
