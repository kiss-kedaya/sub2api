package service

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
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

// ensureDeepSeekChatReasoningPlaceholders fills an empty reasoning_content on
// every assistant message. Non-empty plaintext and non-DeepSeek upstreams are
// returned byte-for-byte unchanged.
func ensureDeepSeekChatReasoningPlaceholders(account *Account, body []byte) []byte {
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

// ensureDeepSeekNativeReasoningPlaceholders is the native Responses analogue:
// a reasoning input item that still has no reasoning_text after the cache
// attempt gets a single-space placeholder. Items with client-provided text and
// items of other types are untouched.
func ensureDeepSeekNativeReasoningPlaceholders(account *Account, body []byte) []byte {
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
