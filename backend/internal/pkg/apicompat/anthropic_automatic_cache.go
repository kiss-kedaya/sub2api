package apicompat

import (
	"encoding/json"
	"fmt"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// ApplyAutomaticCacheControl implements Anthropic's automatic prompt caching at
// the gateway boundary.
//
// Recent Anthropic clients may put `cache_control` on the request body itself
// (`{"cache_control":{"type":"ephemeral"}}`) and rely on the API placing the
// breakpoint on the last cacheable block. Anthropic-compatible relays that only
// understand block-level markers silently ignore that field, so the request is
// billed without any cache hit.
//
// This helper converts the top-level intent into an explicit block-level
// breakpoint, but only when the request carries no block-level marker at all:
// explicit client markers always win. It never removes the top-level field, so
// native Anthropic upstreams keep their automatic behavior.
func ApplyAutomaticCacheControl(body []byte) []byte {
	top := gjson.GetBytes(body, "cache_control")
	if !top.Exists() || !top.IsObject() || top.Get("type").String() != "ephemeral" {
		return body
	}
	if hasBlockCacheControl(body) {
		return body
	}
	marker := automaticCacheMarker(top.Get("ttl").String())
	if next, ok := injectLastMessageBlock(body, marker); ok {
		return next
	}
	if next, ok := injectSystemBlock(body, marker); ok {
		return next
	}
	if next, ok := injectLastTool(body, marker); ok {
		return next
	}
	return body
}

func automaticCacheMarker(ttl string) string {
	if ttl == "" {
		return `{"type":"ephemeral"}`
	}
	return fmt.Sprintf(`{"type":"ephemeral","ttl":%s}`, mustJSONString(ttl))
}

func mustJSONString(s string) string {
	encoded, _ := json.Marshal(s)
	return string(encoded)
}

// hasBlockCacheControl reports whether any cacheable block already carries an
// explicit marker (system, tools or message content).
func hasBlockCacheControl(body []byte) bool {
	system := gjson.GetBytes(body, "system")
	if system.IsArray() {
		found := false
		system.ForEach(func(_, block gjson.Result) bool {
			if block.Get("cache_control").Exists() {
				found = true
				return false
			}
			return true
		})
		if found {
			return true
		}
	}
	tools := gjson.GetBytes(body, "tools")
	if tools.IsArray() {
		found := false
		tools.ForEach(func(_, tool gjson.Result) bool {
			if tool.Get("cache_control").Exists() {
				found = true
				return false
			}
			return true
		})
		if found {
			return true
		}
	}
	messages := gjson.GetBytes(body, "messages")
	if !messages.IsArray() {
		return false
	}
	found := false
	messages.ForEach(func(_, msg gjson.Result) bool {
		content := msg.Get("content")
		if !content.IsArray() {
			return true
		}
		content.ForEach(func(_, block gjson.Result) bool {
			if block.Get("cache_control").Exists() {
				found = true
				return false
			}
			return true
		})
		return !found
	})
	return found
}

func cacheableBlockType(t string) bool {
	switch t {
	case "thinking", "redacted_thinking":
		return false
	default:
		return true
	}
}

// injectLastMessageBlock marks the last cacheable content block of the final
// message, which matches Anthropic automatic caching for conversational turns.
func injectLastMessageBlock(body []byte, marker string) ([]byte, bool) {
	messages := gjson.GetBytes(body, "messages")
	if !messages.IsArray() {
		return body, false
	}
	idx := -1
	messages.ForEach(func(_, _ gjson.Result) bool {
		idx++
		return true
	})
	for ; idx >= 0; idx-- {
		msg := messages.Get(fmt.Sprintf("%d", idx))
		content := msg.Get("content")
		if content.Type == gjson.String {
			raw := fmt.Sprintf(`[{"type":"text","text":%s,"cache_control":%s}]`, mustJSONString(content.String()), marker)
			if next, err := sjson.SetRawBytes(body, fmt.Sprintf("messages.%d.content", idx), []byte(raw)); err == nil {
				return next, true
			}
			return body, false
		}
		if !content.IsArray() {
			continue
		}
		blocks := content.Array()
		for i := len(blocks) - 1; i >= 0; i-- {
			if !cacheableBlockType(blocks[i].Get("type").String()) {
				continue
			}
			path := fmt.Sprintf("messages.%d.content.%d.cache_control", idx, i)
			if next, err := sjson.SetRawBytes(body, path, []byte(marker)); err == nil {
				return next, true
			}
			return body, false
		}
	}
	return body, false
}

func injectSystemBlock(body []byte, marker string) ([]byte, bool) {
	system := gjson.GetBytes(body, "system")
	if !system.Exists() {
		return body, false
	}
	if system.Type == gjson.String {
		raw := fmt.Sprintf(`[{"type":"text","text":%s,"cache_control":%s}]`, mustJSONString(system.String()), marker)
		if next, err := sjson.SetRawBytes(body, "system", []byte(raw)); err == nil {
			return next, true
		}
		return body, false
	}
	if !system.IsArray() {
		return body, false
	}
	blocks := system.Array()
	for i := len(blocks) - 1; i >= 0; i-- {
		if !cacheableBlockType(blocks[i].Get("type").String()) {
			continue
		}
		path := fmt.Sprintf("system.%d.cache_control", i)
		if next, err := sjson.SetRawBytes(body, path, []byte(marker)); err == nil {
			return next, true
		}
		return body, false
	}
	return body, false
}

func injectLastTool(body []byte, marker string) ([]byte, bool) {
	tools := gjson.GetBytes(body, "tools")
	if !tools.IsArray() || len(tools.Array()) == 0 {
		return body, false
	}
	path := fmt.Sprintf("tools.%d.cache_control", len(tools.Array())-1)
	if next, err := sjson.SetRawBytes(body, path, []byte(marker)); err == nil {
		return next, true
	}
	return body, false
}
