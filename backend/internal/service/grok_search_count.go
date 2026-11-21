package service

import (
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
)

// 本文件统计上游 Responses 载荷里的内置检索工具调用次数，供按次计费使用。
//
// 历史上只对 Grok 通道统计（文件名/函数名沿用 Grok 前缀），但内置检索工具在
// OpenAI / DeepSeek / Anthropic 兼容通道上同样由上游执行并按次收费，因此判定
// 口径已经平台无关（issue #491）；只有“写不写进计费结果”那一层仍由
// openAINativeSearchBillingApplies 决定。
//
// 认的三种形态：
//   - 服务端工具调用条目：web_search_call / web_search_preview_call /
//     x_search_call / tool_search_call（Responses 的原生形态）
//   - function_call / custom_tool_call 且名字是检索工具名（部分中转把服务端工具
//     拍平成函数调用）
//   - 客户端自己声明的同名函数不算：见 openai_native_search_billing.go

// nativeSearchFunctionToolNames 是“名字即检索工具”的候选集合（小写）。
var nativeSearchFunctionToolNames = map[string]struct{}{
	"web_search":         {},
	"web_search_preview": {},
	"x_search":           {},
	"tool_search":        {},
}

// nativeSearchCallItemTypes 是检索工具在 Responses output 里的条目类型（小写）。
var nativeSearchCallItemTypes = map[string]struct{}{
	"web_search_call":         {},
	"web_search_preview_call": {},
	"x_search_call":           {},
	"tool_search_call":        {},
}

// countGrokNativeSearchCallsFromJSONBytes counts completed native search tool
// calls in a Responses-style JSON body (output array or nested response.output).
func countGrokNativeSearchCallsFromJSONBytes(body []byte) int {
	return countNativeSearchCallsFromJSONBytes(body, nil)
}

// countNativeSearchCallsFromJSONBytes is the platform-agnostic variant. skip
// holds client-declared function tool names that must not be billed as builtin
// server tools.
func countNativeSearchCallsFromJSONBytes(body []byte, skip map[string]struct{}) int {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return 0
	}
	// Responses envelopes normally expose either top-level output (JSON mode)
	// or response.output (terminal SSE payload). Compatibility layers can retain
	// both copies; counting both would bill the same search twice. Prefer the
	// canonical nested response when present and fall back to top-level output.
	if nested := gjson.GetBytes(body, "response.output"); nested.IsArray() {
		return countNativeSearchCallsInOutputArray(nested, skip)
	}
	return countNativeSearchCallsInOutputArray(gjson.GetBytes(body, "output"), skip)
}

func countGrokNativeSearchCallsFromSSEBody(body string) int {
	return countNativeSearchCallsFromSSEBody(body, nil)
}

func countNativeSearchCallsFromSSEBody(body string, skip map[string]struct{}) int {
	if strings.TrimSpace(body) == "" {
		return 0
	}
	seen := make(map[string]struct{})
	total := 0
	forEachOpenAISSEDataPayload(body, func(data []byte) {
		total += countNativeSearchCallsInSSEDataDedup(data, seen, skip)
	})
	return total
}

// countGrokNativeSearchCallsInSSEData counts search tool calls in one SSE
// payload without cross-event dedup. Prefer the dedup variant for live streams
// so item.done + response.completed do not double-bill.
func countGrokNativeSearchCallsInSSEData(data []byte) int {
	frame := parseOpenAISSEDataFrame(data, "")
	n, _ := countNativeSearchCallsInSSEFrameWithKeys(frame, nil)
	return n
}

// countGrokNativeSearchCallsInSSEDataDedup increments only unseen call ids.
// Callers must reuse the same seen map for the full stream lifetime.
//
// When call_id/id is missing, a synthetic key is built from item type + name so
// item.done + response.completed for the same tool still count once (never fall
// back to raw multi-event n, which ~2× overbills).
func countGrokNativeSearchCallsInSSEDataDedup(data []byte, seen map[string]struct{}) int {
	return countNativeSearchCallsInSSEDataDedup(data, seen, nil)
}

func countNativeSearchCallsInSSEDataDedup(data []byte, seen map[string]struct{}, skip map[string]struct{}) int {
	return countNativeSearchCallsInSSEFrameDedup(parseOpenAISSEDataFrame(data, ""), seen, skip)
}

func countNativeSearchCallsInSSEFrameDedup(frame openAISSEDataFrame, seen map[string]struct{}, skip map[string]struct{}) int {
	if !openAISSEFrameMayContainGrokSearch(frame) {
		return 0
	}
	if seen == nil {
		n, _ := countNativeSearchCallsInSSEFrameWithKeys(frame, skip)
		return n
	}
	n, keys := countNativeSearchCallsInSSEFrameWithKeys(frame, skip)
	if n <= 0 {
		return 0
	}
	// Prefer stable ids; fill gaps with synthetic keys so we never raw-add n.
	if len(keys) < n {
		// Rebuild keys for every item so unkeyed items still get a fingerprint.
		keys = collectNativeSearchCallKeysFromFrame(frame, skip)
	}
	if len(keys) == 0 {
		// True empty — should not happen when n>0; fail-closed to 0 extra bill.
		return 0
	}
	added := 0
	local := make(map[string]struct{}, len(keys))
	isItemDone := frame.eventType == "response.output_item.done"
	for _, k := range keys {
		if k == "" {
			continue
		}
		if _, ok := local[k]; ok {
			continue
		}
		local[k] = struct{}{}
		if _, ok := seen[k]; ok {
			if !isItemDone || !strings.HasPrefix(k, "synth:") {
				continue
			}
			// Each id-less item.done is a distinct completed invocation. Advance
			// its ordinal so interrupted streams remain accurately billable.
			separator := strings.LastIndexByte(k, ':')
			if separator < 0 {
				continue
			}
			base := k[:separator]
			for ordinal := 2; ; ordinal++ {
				candidate := base + ":" + strconv.Itoa(ordinal)
				if _, exists := seen[candidate]; !exists {
					k = candidate
					break
				}
			}
		}
		seen[k] = struct{}{}
		added++
	}
	return added
}

func collectNativeSearchCallKeysFromFrame(frame openAISSEDataFrame, skip map[string]struct{}) []string {
	if !openAISSEFrameMayContainGrokSearch(frame) {
		return nil
	}
	var keys []string
	syntheticOrdinals := make(map[string]int)
	consider := func(item gjson.Result) {
		if !isNativeSearchOutputItem(item, skip) {
			return
		}
		key := firstNonEmpty(
			strings.TrimSpace(item.Get("call_id").String()),
			strings.TrimSpace(item.Get("id").String()),
			strings.TrimSpace(item.Get("item.call_id").String()),
			strings.TrimSpace(item.Get("item.id").String()),
		)
		if key == "" {
			// Include the ordinal among same-kind calls. A plain type:name key
			// collapses two id-less web searches in one completed response into
			// one charge. The ordinal remains stable between ordered item.done
			// events and response.completed output.
			base := "synth:" + strings.ToLower(strings.TrimSpace(item.Get("type").String())) +
				":" + strings.ToLower(strings.TrimSpace(item.Get("name").String()))
			syntheticOrdinals[base]++
			key = base + ":" + strconv.Itoa(syntheticOrdinals[base])
		}
		keys = append(keys, key)
	}
	if item := frame.root.Get("item"); item.Exists() {
		consider(item)
	}
	frame.root.Get("response.output").ForEach(func(_, item gjson.Result) bool {
		consider(item)
		return true
	})
	frame.root.Get("output").ForEach(func(_, item gjson.Result) bool {
		consider(item)
		return true
	})
	if len(keys) == 0 && isNativeSearchOutputItem(frame.root, skip) {
		consider(frame.root)
	}
	return keys
}

func countNativeSearchCallsInSSEFrameWithKeys(frame openAISSEDataFrame, skip map[string]struct{}) (int, []string) {
	if !openAISSEFrameMayContainGrokSearch(frame) {
		return 0, nil
	}
	var keys []string
	n := 0
	consider := func(item gjson.Result) {
		if !isNativeSearchOutputItem(item, skip) {
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
	if item := frame.root.Get("item"); item.Exists() {
		consider(item)
	}
	frame.root.Get("response.output").ForEach(func(_, item gjson.Result) bool {
		consider(item)
		return true
	})
	frame.root.Get("output").ForEach(func(_, item gjson.Result) bool {
		consider(item)
		return true
	})
	// Bare output item event without nested item key.
	if n == 0 && isNativeSearchOutputItem(frame.root, skip) {
		consider(frame.root)
	}
	return n, keys
}

func openAISSEFrameMayContainGrokSearch(frame openAISSEDataFrame) bool {
	if !frame.validJSON {
		return false
	}
	// Count once on item completion / completed response, not on every delta.
	// An empty type means a bare item object without an SSE envelope.
	switch frame.eventType {
	case "response.output_item.done", "response.completed", "response.done", "":
		return true
	default:
		return false
	}
}

func countNativeSearchCallsInOutputArray(output gjson.Result, skip map[string]struct{}) int {
	if !output.IsArray() {
		return 0
	}
	count := 0
	output.ForEach(func(_, item gjson.Result) bool {
		if isNativeSearchOutputItem(item, skip) {
			count++
		}
		return true
	})
	return count
}

// isNativeSearchOutputItem 判断一个 Responses output 条目是否代表一次由上游执行
// （并计费）的内置检索调用。skip 是客户端自己声明的函数工具名。
func isNativeSearchOutputItem(item gjson.Result, skip map[string]struct{}) bool {
	if !item.Exists() {
		return false
	}
	itemType := strings.ToLower(strings.TrimSpace(item.Get("type").String()))
	if _, ok := nativeSearchCallItemTypes[itemType]; ok {
		return true
	}
	switch itemType {
	case "function_call", "custom_tool_call":
		name := strings.ToLower(strings.TrimSpace(item.Get("name").String()))
		if _, ok := nativeSearchFunctionToolNames[name]; !ok {
			return false
		}
		// 客户端自己声明了同名函数时，这只是模型要求客户端执行的一次函数调用，
		// 不是上游内置工具，绝不能按次收费。
		return !isClientDeclaredFunctionTool(name, skip)
	default:
		return false
	}
}
