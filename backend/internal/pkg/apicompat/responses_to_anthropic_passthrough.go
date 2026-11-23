package apicompat

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Responses→Anthropic 的「透传优先」策略。
//
// 背景：OpenAI Responses 客户端（Codex 等）与新 SDK 会携带一批 Anthropic
// Messages 没有对应字段的顶层参数（previous_response_id、store、include、
// parallel_tool_calls、text、prompt_cache_key 等），以及未来新增的未知字段。
// 历史转换只把能映射的字段写进 AnthropicRequest，其余在反序列化阶段静默丢弃；
// 用户侧表现为「转换链少参数」，很难归因。
//
// 现在改为：转换器负责字段映射（input/instructions → messages/system、
// max_output_tokens → max_tokens、function tools → anthropic tools、tool_choice、
// stream 等），其余顶层字段原样附在转换后的请求体上，交给上游按自己的策略处理
// （上游忽略即忽略）。只有无法安全表达为 JSON 顶层键的异常键名才降级丢弃。
//
// 注意：本文件只处理请求体顶层的「未映射字段」；tools 数组里的未知工具类型由
// convertResponsesToAnthropicTools 的 default 分支负责原样保留，input 项内部的
// 未知字段也不是这里的职责。

// responsesFieldConvertedByBridge 列出转换器已经消费/改写的顶层字段。它们不能再
// 透传，否则会与转换结果重复（例如原始 text 版 input 与合成后的 messages）。
// 该集合必须与 ResponsesToAnthropicRequestWithOptions 读取的字段保持一致。
var responsesFieldConvertedByBridge = map[string]struct{}{
	"model":             {},
	"input":             {},
	"instructions":      {},
	"max_output_tokens": {},
	"temperature":       {},
	"top_p":             {},
	"stream":            {},
	"tools":             {},
	"tool_choice":       {},
	"reasoning":         {},
	"cache_control":     {},
}

// ResponsesUnsupportedPassthroughFields returns the top-level request fields the
// Responses→Anthropic bridge does not map and therefore forwards verbatim.
//
// rawBody is the client Responses body (after any legacy normalization). The
// returned map is keyed by JSON object key and safe to merge back with
// MergeResponsesPassthroughFields. Malformed bodies, non-object bodies, and
// keys that cannot be expressed as a simple JSON path segment are dropped
// defensively — never a reason to reject the request.
func ResponsesUnsupportedPassthroughFields(rawBody []byte) map[string]json.RawMessage {
	return responsesUnsupportedPassthroughFields(rawBody, false)
}

// responsesUnsupportedPassthroughFields is ResponsesUnsupportedPassthroughFields
// with an explicit reasoning passthrough switch. When passthroughReasoning is
// true the bridge does not synthesize thinking/output_config, so the raw
// `reasoning` object must be forwarded like any other unmapped field.
func responsesUnsupportedPassthroughFields(rawBody []byte, passthroughReasoning bool) map[string]json.RawMessage {
	rawBody = []byte(strings.TrimSpace(string(rawBody)))
	if len(rawBody) == 0 || !gjson.ValidBytes(rawBody) || !gjson.ParseBytes(rawBody).IsObject() {
		return nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rawBody, &raw); err != nil {
		return nil
	}
	fields := make(map[string]json.RawMessage, len(raw))
	for key, value := range raw {
		if _, converted := responsesFieldConvertedByBridge[key]; converted {
			if !passthroughReasoning || key != "reasoning" {
				continue
			}
		}
		if !isPassthroughAnthropicTopLevelKey(key) {
			continue
		}
		trimmed := strings.TrimSpace(string(value))
		if trimmed == "" || trimmed == "null" {
			continue
		}
		fields[key] = value
	}
	if len(fields) == 0 {
		return nil
	}
	return fields
}

// isPassthroughAnthropicTopLevelKey keeps only keys that sjson/gjson treat as a
// single literal path segment (letters, digits, '_' and '-'). Anything with a
// dot/wildcard would be addressed as a nested path and could corrupt the
// converted body, so it is dropped instead of risking a malformed request.
func isPassthroughAnthropicTopLevelKey(key string) bool {
	if key == "" {
		return false
	}
	if key == "model" || key == "messages" || key == "system" || key == "max_tokens" {
		return false
	}
	for i := 0; i < len(key); i++ {
		ch := key[i]
		switch {
		case ch >= 'a' && ch <= 'z':
		case ch >= 'A' && ch <= 'Z':
		case ch >= '0' && ch <= '9':
		case ch == '_' || ch == '-':
		default:
			return false
		}
	}
	return true
}

// MergeResponsesPassthroughFields appends the unmapped Responses top-level
// fields to the converted Anthropic request body. Fields already present in the
// bridge output win — the conversion must never be overwritten by a raw client
// value (for example a raw "messages" key is filtered out earlier anyway).
func MergeResponsesPassthroughFields(body []byte, fields map[string]json.RawMessage) ([]byte, error) {
	if len(fields) == 0 || len(body) == 0 {
		return body, nil
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := body
	for _, key := range keys {
		if gjson.GetBytes(out, key).Exists() {
			continue
		}
		next, err := sjson.SetRawBytes(out, key, []byte(fields[key]))
		if err != nil {
			return body, fmt.Errorf("merge responses passthrough field %q: %w", key, err)
		}
		out = next
	}
	return out, nil
}
