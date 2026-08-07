package service

import (
	"strings"

	"github.com/tidwall/gjson"
)

// countGrokNativeSearchCallsFromJSONBytes counts completed native search tool
// calls in a Responses-style JSON body (output array or nested response.output).
// Counts: web_search_call, x_search_call, tool_search_call, and function_call
// named tool_search / web_search / x_search.
func countGrokNativeSearchCallsFromJSONBytes(body []byte) int {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return 0
	}
	count := 0
	count += countGrokNativeSearchCallsInOutputArray(gjson.GetBytes(body, "output"))
	count += countGrokNativeSearchCallsInOutputArray(gjson.GetBytes(body, "response.output"))
	return count
}

func countGrokNativeSearchCallsFromSSEBody(body string) int {
	if strings.TrimSpace(body) == "" {
		return 0
	}
	seen := make(map[string]struct{})
	total := 0
	forEachOpenAISSEDataPayload(body, func(data []byte) {
		total += countGrokNativeSearchCallsInSSEDataDedup(data, seen)
	})
	return total
}

// countGrokNativeSearchCallsInSSEData counts search tool calls in one SSE
// payload without cross-event dedup. Prefer countGrokNativeSearchCallsInSSEDataDedup
// for live streams so item.done + response.completed do not double-bill.
func countGrokNativeSearchCallsInSSEData(data []byte) int {
	n, _ := countGrokNativeSearchCallsInSSEDataWithKeys(data)
	return n
}

// countGrokNativeSearchCallsInSSEDataDedup increments only unseen call ids.
// Callers must reuse the same seen map for the full stream lifetime.
//
// When call_id/id is missing, a synthetic key is built from item type + name so
// item.done + response.completed for the same tool still count once (never fall
// back to raw multi-event n, which ~2× overbills).
func countGrokNativeSearchCallsInSSEDataDedup(data []byte, seen map[string]struct{}) int {
	if seen == nil {
		return countGrokNativeSearchCallsInSSEData(data)
	}
	n, keys := countGrokNativeSearchCallsInSSEDataWithKeys(data)
	if n <= 0 {
		return 0
	}
	// Prefer stable ids; fill gaps with synthetic keys so we never raw-add n.
	if len(keys) < n {
		// Rebuild keys for every item so unkeyed items still get a fingerprint.
		keys = collectGrokNativeSearchCallKeys(data)
	}
	if len(keys) == 0 {
		// True empty — should not happen when n>0; fail-closed to 0 extra bill.
		return 0
	}
	added := 0
	local := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		if k == "" {
			continue
		}
		if _, ok := local[k]; ok {
			continue
		}
		local[k] = struct{}{}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		added++
	}
	return added
}

func collectGrokNativeSearchCallKeys(data []byte) []string {
	if len(data) == 0 || !gjson.ValidBytes(data) {
		return nil
	}
	eventType := strings.TrimSpace(gjson.GetBytes(data, "type").String())
	switch eventType {
	case "response.output_item.done", "response.completed", "response.done", "":
	default:
		if eventType != "" {
			return nil
		}
	}
	var keys []string
	consider := func(item gjson.Result) {
		if !isGrokNativeSearchOutputItem(item) {
			return
		}
		key := firstNonEmpty(
			strings.TrimSpace(item.Get("call_id").String()),
			strings.TrimSpace(item.Get("id").String()),
			strings.TrimSpace(item.Get("item.call_id").String()),
			strings.TrimSpace(item.Get("item.id").String()),
		)
		if key == "" {
			// Synthetic fingerprint: type + name is stable across done/completed
			// for the same tool invocation when upstream omits call_id.
			key = "synth:" + strings.ToLower(strings.TrimSpace(item.Get("type").String())) +
				":" + strings.ToLower(strings.TrimSpace(item.Get("name").String()))
		}
		keys = append(keys, key)
	}
	if item := gjson.GetBytes(data, "item"); item.Exists() {
		consider(item)
	}
	gjson.GetBytes(data, "response.output").ForEach(func(_, item gjson.Result) bool {
		consider(item)
		return true
	})
	gjson.GetBytes(data, "output").ForEach(func(_, item gjson.Result) bool {
		consider(item)
		return true
	})
	if len(keys) == 0 && isGrokNativeSearchOutputItem(gjson.ParseBytes(data)) {
		consider(gjson.ParseBytes(data))
	}
	return keys
}

func countGrokNativeSearchCallsInSSEDataWithKeys(data []byte) (int, []string) {
	if len(data) == 0 || !gjson.ValidBytes(data) {
		return 0, nil
	}
	eventType := strings.TrimSpace(gjson.GetBytes(data, "type").String())
	// Count once on item completion / completed response, not on every delta.
	switch eventType {
	case "response.output_item.done", "response.completed", "response.done":
	default:
		// Also allow bare item objects without envelope.
		if eventType != "" {
			return 0, nil
		}
	}
	var keys []string
	n := 0
	consider := func(item gjson.Result) {
		if !isGrokNativeSearchOutputItem(item) {
			return
		}
		n++
		key := firstNonEmpty(
			strings.TrimSpace(item.Get("call_id").String()),
			strings.TrimSpace(item.Get("id").String()),
			strings.TrimSpace(item.Get("item.call_id").String()),
			strings.TrimSpace(item.Get("item.id").String()),
		)
		if key != "" {
			keys = append(keys, key)
		}
	}
	if item := gjson.GetBytes(data, "item"); item.Exists() {
		consider(item)
	}
	gjson.GetBytes(data, "response.output").ForEach(func(_, item gjson.Result) bool {
		consider(item)
		return true
	})
	gjson.GetBytes(data, "output").ForEach(func(_, item gjson.Result) bool {
		consider(item)
		return true
	})
	// Bare output item event without nested item key.
	if n == 0 && isGrokNativeSearchOutputItem(gjson.ParseBytes(data)) {
		consider(gjson.ParseBytes(data))
	}
	return n, keys
}

func countGrokNativeSearchCallsInOutputArray(output gjson.Result) int {
	if !output.IsArray() {
		return 0
	}
	count := 0
	output.ForEach(func(_, item gjson.Result) bool {
		if isGrokNativeSearchOutputItem(item) {
			count++
		}
		return true
	})
	return count
}

func isGrokNativeSearchOutputItem(item gjson.Result) bool {
	if !item.Exists() {
		return false
	}
	itemType := strings.ToLower(strings.TrimSpace(item.Get("type").String()))
	switch itemType {
	case "web_search_call", "x_search_call", "tool_search_call":
		return true
	case "function_call", "custom_tool_call":
		name := strings.ToLower(strings.TrimSpace(item.Get("name").String()))
		return name == "web_search" || name == "x_search" || name == "tool_search"
	default:
		return false
	}
}
