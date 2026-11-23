package apicompat

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Responses→Anthropic 的顶层字段透传。
//
// 背景：OpenAI Responses 客户端（Codex 等）会携带一批 Anthropic Messages 没有
// 对应字段的顶层参数（previous_response_id、store、include、parallel_tool_calls、
// text、prompt_cache_key、reasoning、metadata 任意键、service_tier 取值等）。
//
// 默认只放行白名单，不能「其余全部原样附上」：Anthropic Messages 的请求体按
// extra='forbid' 校验，未知顶层键直接 400 "xxx: Extra inputs are not permitted"，
// 并且这类 400 属于客户端错误，网关不会换号、不会换组、不会重试，最终原样回给用户。
// 仓库自己已经踩过同类事故（见 gateway_request.go 里
// sanitizeAnthropicBodyForBetaTokens 的说明：context_management / fallbacks /
// thinking.block_binding / output_config 缺 beta 时「所有模型 400」）。Codex 的
// 每个请求几乎都带 store/include/parallel_tool_calls/prompt_cache_key/reasoning，
// 全量透传等于让「Responses 入站 → Anthropic 上游」整条链路对严格上游整体失败。
//
// 所以：转换器负责字段映射（input/instructions → messages/system、
// max_output_tokens → max_tokens、function tools → anthropic tools、tool_choice、
// stream 等），其余顶层字段默认丢弃（与 2.3.153 的类型化结构体行为一致），只有
// 下面白名单里「Anthropic Messages 自己就认识、且不改变计费」的键才原样附上。
// 需要放行新键时只改 anthropicPassthroughTopLevelKeys 并补测试。
//
// 例外（显式开启）：ResponsesToAnthropicOptions.PassthroughUnmappedFields。调用方
// 对「透明账号」（accounts.extra.anthropic_passthrough，语义就是「只替换认证、不改写
// 请求」，上游是宽容的中转）打开它，此时全部未映射顶层键原样附上，交给上游处理。
// 这是账号级选择，不是全局默认。
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

// anthropicPassthroughTopLevelKeys 是允许从 Responses 请求体原样带给 Anthropic
// 上游的顶层键。只放 Anthropic Messages 本身定义的、不改变计费档位的参数：
//   - top_k / stop_sequences：采样与停止序列，Anthropic 原生字段。
//
// 刻意不放：reasoning / store / include / parallel_tool_calls / prompt_cache_key /
// previous_response_id / text / truncation / user / background 等 OpenAI 专属键
// （严格上游会 400）；metadata（Anthropic 只认 metadata.user_id，Responses 的任意键会 400）；
// service_tier / speed / inference_geo / container / mcp_servers（会改变上游计费或
// 能力档位，而桥路径不做对应的计费与 beta 策略处理）。
var anthropicPassthroughTopLevelKeys = map[string]struct{}{
	"top_k":          {},
	"stop_sequences": {},
}

// ResponsesUnsupportedPassthroughFields returns the top-level request fields the
// Responses→Anthropic bridge does not map but Anthropic Messages itself defines
// (see anthropicPassthroughTopLevelKeys), which are therefore forwarded verbatim.
// Every other unmapped field is dropped, exactly like the typed conversion did.
//
// rawBody is the client Responses body (after any legacy normalization). The
// returned map is keyed by JSON object key and safe to merge back with
// MergeResponsesPassthroughFields. Malformed bodies, non-object bodies, and
// keys that cannot be expressed as a simple JSON path segment are dropped
// defensively — never a reason to reject the request.
func ResponsesUnsupportedPassthroughFields(rawBody []byte) map[string]json.RawMessage {
	return collectResponsesPassthroughFields(rawBody, false, false)
}

// collectResponsesPassthroughFields is the shared collector. all=false keeps only
// anthropicPassthroughTopLevelKeys; all=true (transparent accounts) keeps every
// unmapped key. passthroughReasoning only matters with all=true: the bridge does
// not synthesize thinking/output_config then, so the raw `reasoning` object is
// forwarded like any other unmapped field.
func collectResponsesPassthroughFields(rawBody []byte, passthroughReasoning, all bool) map[string]json.RawMessage {
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
			if !all || !passthroughReasoning || key != "reasoning" {
				continue
			}
		}
		if !all {
			if _, allowed := anthropicPassthroughTopLevelKeys[key]; !allowed {
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
