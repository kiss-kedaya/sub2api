package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unsafe"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const openAIResponsesNamespaceNamesContextKey = "openai_responses_namespace_names"

// shouldFlattenOpenAIResponsesNamespaces 判定原生 Responses 转发前是否摊平
// Codex namespace 工具。
//
// 默认不摊平：OAuth 账号的 HTTP 出口恒为 chatgpt.com/backend-api/codex/responses
// （buildUpstreamRequest 只在 API Key 分支读 base_url），也就是 namespace 扩展的
// 定义方本身；Codex 客户端对 WS 与 HTTP 两条传输发送同一份 tools（codex-rs
// client.rs build_responses_request 无传输分支，WS 失败后会 session 级回落 HTTP
// 继续发同样的声明）。摊平只改写工具名，改不掉客户端在 tools 描述与 developer
// 消息里写死的 `to=functions.<namespace>.<tool>` 寻址约定，模型据此寻址必然落空
// （issue #4978）；命名空间名还可由 features.multi_agent_v2.tool_namespace 自定义、
// 或由 MCP/connector 动态生成（mcp__codex_apps__gmail），保留名单枚举不完。
//
// compact 端点例外：已知它的 schema 比 /responses 窄（连 input[].namespace 都会报
// Unknown parameter，见 issue #4761），而是否接受 namespace 工具声明没有任何实测
// 证据；compact 只做历史摘要、不需要模型寻址工具，回程也没有工具调用可还原，因此
// 保持 0.1.166 起就在跑的摊平行为，不随本次默认值翻转扩大风险面。
//
// 账号开关 openai_responses_flatten_namespaces 为不认识 namespace 的兼容上游保留
// 退路，打开后恢复旧行为：WSv2 上游原生支持 namespace，且 WS 出口
// （openai_ws_forwarder_v2）原样转发上游事件、不经 HTTP 回程还原，摊平后的平名
// 无法还原会破坏客户端工具匹配，因此实际走 WSv2 分支的请求仍保持 namespace 原样；
// 透传账号先于 WSv2 分支经 HTTP 转发返回，仍需摊平。
func shouldFlattenOpenAIResponsesNamespaces(
	account *Account,
	transport OpenAIUpstreamTransport,
	passthroughEnabled bool,
	compactPath bool,
) bool {
	if account == nil || !account.IsOpenAIOAuthLike() {
		return false
	}
	if !compactPath && !account.IsOpenAIResponsesFlattenNamespacesEnabled() {
		return false
	}
	if transport == OpenAIUpstreamTransportResponsesWebsocketV2 && !passthroughEnabled {
		return false
	}
	return true
}

// shouldStripOpenAIResponsesInputNamespaces removes residual input item
// namespaces for OpenAI OAuth and API Key HTTP forwarding. Native WSv2 keeps
// namespaces because that protocol supports them and does not restore payloads.
func shouldStripOpenAIResponsesInputNamespaces(account *Account, transport OpenAIUpstreamTransport, passthroughEnabled bool) bool {
	if account == nil || (!account.IsOpenAIOAuthLike() && !account.IsOpenAIApiKey()) {
		return false
	}
	if transport == OpenAIUpstreamTransportResponsesWebsocketV2 && !passthroughEnabled {
		return false
	}
	return true
}

// shouldKeepOpenAIResponsesToolCallNamespaces 判定清理 input 残留 namespace 时是否
// 保留工具调用项上的 namespace。
//
// 上游对这个字段有两套互斥要求，判定按「出口 + 端点」而非工具声明内容：
//   - /backend-api/codex/responses 会按 namespace 解析历史调用，缺字段直接 400
//     `Missing namespace for function_call '...'. Round-trip the model's
//     function_call item with its namespace field included.`（issue #4761 回帖），
//     故 OAuth 非 compact 请求必须保留。
//   - compact 端点的 schema 不含该字段，携带即 400 `Unknown parameter:
//     input[N].namespace`（issue #4761 正文），故 compact 一律清理。
//   - API Key 出口默认按标准 Responses API 处理并清理该字段；但当请求本身声明
//     namespace 工具时（包括 Responses Lite 的 input[].additional_tools），上游显然
//     使用了 namespace 扩展，此时必须保留调用项上的 namespace，否则声明与历史
//     调用会失配并触发 Missing namespace。
//   - 摊平模式下调用项已被改写成平名，残留 namespace 指向的声明已不存在，一律清理。
func shouldKeepOpenAIResponsesToolCallNamespaces(
	c *gin.Context,
	account *Account,
	transport OpenAIUpstreamTransport,
	passthroughEnabled bool,
	compactPath bool,
	body []byte,
) bool {
	if account == nil {
		return false
	}
	if compactPath {
		return false
	}
	if account.IsOpenAIApiKey() {
		return hasOpenAIResponsesNamespaceToolDeclaration(c, body)
	}
	if !account.IsOpenAIOAuthLike() {
		return false
	}
	return !shouldFlattenOpenAIResponsesNamespaces(account, transport, passthroughEnabled, compactPath)
}

func hasOpenAIResponsesNamespaceToolDeclaration(c *gin.Context, body []byte) bool {
	hasNamespaceTool := func(tools gjson.Result) bool {
		if !tools.IsArray() {
			return false
		}
		toolsSlice := unsafe.Slice(unsafe.StringData(tools.Raw), len(tools.Raw))
		return jsonSpanHasNamespaceTool(toolsSlice, 0, len(toolsSlice))
	}

	top := RequestBodyTop(c, body)
	if hasNamespaceTool(top.Get("tools")) {
		return true
	}

	// Responses Lite moves private namespace declarations out of top-level
	// tools and into an input.additional_tools carrier. API-key requests are
	// intentionally not rewritten by normalizeOpenAIResponsesLiteTools, so the
	// declaration can arrive here only in this form.
	if !bytes.Contains(body, []byte(`\u`)) && !containsASCIIFoldAdditionalTools(jsonView(body)) {
		return false
	}
	if idx := top.responsesInput(); idx != nil && idx.usable {
		return idx.hasAdditionalNamespaceTool
	}
	input := top.Get("input")
	if !input.IsArray() {
		return false
	}
	if !strings.Contains(input.Raw, `\u`) && !containsASCIIFoldAdditionalTools(input.Raw) {
		return false
	}
	inputSlice := unsafe.Slice(unsafe.StringData(input.Raw), len(input.Raw))
	found := false
	elemOK := jsonForEachElementSpan(inputSlice, 0, len(inputSlice), func(elemStart, elemEnd int) bool {
		if inputSlice[elemStart] != '{' {
			return true
		}
		elem := input.Raw[elemStart:elemEnd]
		if !strings.Contains(elem, `\u`) && !containsASCIIFoldAdditionalTools(elem) {
			return true
		}
		var typeRaw, toolsRaw string
		var hasType, hasTools bool
		memberOK := jsonForEachMemberSpan(inputSlice, elemStart, elemEnd, func(key string, valStart, valEnd, _, _ int) bool {
			switch key {
			case "type":
				if !hasType {
					typeRaw = input.Raw[valStart:valEnd]
					hasType = true
				}
			case "tools":
				if !hasTools {
					toolsRaw = input.Raw[valStart:valEnd]
					hasTools = true
				}
			}
			return !hasType || !hasTools
		})
		if !memberOK {
			item := gjson.Parse(elem)
			if !strings.EqualFold(strings.TrimSpace(item.Get("type").String()), "additional_tools") {
				return true
			}
			if hasNamespaceTool(item.Get("tools")) {
				found = true
				return false
			}
			return true
		}
		if !hasType || !strings.EqualFold(strings.TrimSpace(extractJSONMemberString(typeRaw, false)), "additional_tools") {
			return true
		}
		if hasTools && len(toolsRaw) > 0 && toolsRaw[0] == '[' {
			toolsSlice := unsafe.Slice(unsafe.StringData(toolsRaw), len(toolsRaw))
			if jsonSpanHasNamespaceTool(toolsSlice, 0, len(toolsSlice)) {
				found = true
				return false
			}
		}
		return true
	})
	if elemOK {
		return found
	}
	input.ForEach(func(_, item gjson.Result) bool {
		if !strings.EqualFold(strings.TrimSpace(item.Get("type").String()), "additional_tools") {
			return true
		}
		if hasNamespaceTool(item.Get("tools")) {
			found = true
			return false
		}
		return true
	})
	return found
}

// openAIResponsesToolCallItemTypes 是携带 namespace 的调用项类型集合。与
// removeOpenAIResponsesRejectedNamespaceAtIndex 的反应式白名单保持一致；codex-rs
// protocol/src/models.rs 中只有 FunctionCall 与 CustomToolCall 序列化 namespace，
// 其余类型带该字段一定是非 Codex 客户端或历史残留，清掉才安全。
var openAIResponsesToolCallItemTypes = map[string]bool{
	"function_call":    true,
	"tool_call":        true,
	"custom_tool_call": true,
	"mcp_tool_call":    true,
}

func isOpenAIResponsesToolCallItemType(itemType string) bool {
	return openAIResponsesToolCallItemTypes[strings.ToLower(strings.TrimSpace(itemType))]
}

func flattenOpenAIResponsesNamespaces(c *gin.Context, body []byte) ([]byte, error) {
	if !bytes.Contains(body, []byte(`"namespace"`)) {
		return body, nil
	}
	var requestBody map[string]any
	if err := json.Unmarshal(body, &requestBody); err != nil {
		return body, fmt.Errorf("decode OpenAI namespace body: %w", err)
	}
	names, changed, err := apicompat.FlattenResponsesNamespacesExcept(requestBody, map[string]bool{"image_gen": true})
	if err != nil {
		return body, err
	}
	if !changed {
		return body, nil
	}
	rebuilt, err := marshalOpenAIUpstreamJSON(requestBody)
	if err != nil {
		return body, fmt.Errorf("encode OpenAI namespace body: %w", err)
	}
	setOpenAIResponsesNamespaceNames(c, names)
	return rebuilt, nil
}

// stripOpenAIResponsesInputNamespaces removes namespace only from direct input
// array items. Namespace declarations and nested namespace fields are left
// untouched. Rebuilding the input array once keeps this linear for long
// histories and avoids decoding JSON numbers through float64.
//
// keepToolCallNamespaces 保留工具调用项（function_call / custom_tool_call 等）上的
// namespace，让 Codex 调用能按上游要求原样回传；判定见
// shouldKeepOpenAIResponsesToolCallNamespaces。
func stripOpenAIResponsesInputNamespaces(body []byte, keepToolCallNamespaces bool) ([]byte, error) {
	return stripOpenAIResponsesInputNamespacesCtx(nil, body, keepToolCallNamespaces)
}

func stripOpenAIResponsesInputNamespacesCtx(c *gin.Context, body []byte, keepToolCallNamespaces bool) ([]byte, error) {
	if len(body) == 0 || (!bytes.Contains(body, []byte(`"namespace"`)) && !bytes.Contains(body, []byte(`\u`))) {
		return body, nil
	}
	if idx := RequestBodyTop(c, body).responsesInput(); idx != nil && idx.usable {
		if !idx.isArray || !idx.hasNamespaceField {
			return body, nil
		}
		var edits []jsonEdit
		for _, it := range idx.items {
			if it.nsMemberIdx < 0 || (keepToolCallNamespaces && it.isNSToolCall) {
				continue
			}
			if it.tight && it.nsDropEnd > it.nsDropStart {
				edits = append(edits, jsonEdit{start: int(it.nsDropStart), end: int(it.nsDropEnd)})
				continue
			}
			rawItem := unsafe.Slice(unsafe.SliceData(body[it.start:it.end]), int(it.end-it.start))
			deleted, deleteErr := sjson.DeleteBytes(rawItem, "namespace")
			if deleteErr != nil {
				return body, fmt.Errorf("delete OpenAI input namespace: %w", deleteErr)
			}
			edits = append(edits, jsonEdit{
				start:       int(it.start),
				end:         int(it.end),
				replacement: unsafe.String(unsafe.SliceData(deleted), len(deleted)),
			})
		}
		if len(edits) == 0 {
			return body, nil
		}
		stripped, editErr := applyJSONEdits(body, edits)
		if editErr != nil {
			return body, editErr
		}
		return stripped, nil
	}
	if inputStart, inputEnd, isArray, ok := locateJSONInputArraySpan(c, body); ok {
		if !isArray {
			return body, nil
		}
		var (
			edits      []jsonEdit
			membersBuf [16]jsonObjMember
			walkErr    error
		)
		elemOK := jsonForEachElementSpan(body, inputStart, inputEnd, func(elemStart, elemEnd int) bool {
			if body[elemStart] != '{' {
				return true
			}
			elem := jsonView(body[elemStart:elemEnd])
			if !strings.Contains(elem, `"namespace"`) && !strings.Contains(elem, `\u`) {
				return true
			}
			members := membersBuf[:0]
			var typeRaw string
			var hasType bool
			nsMemberIdx := -1
			memberOK := jsonForEachMemberSpan(body, elemStart, elemEnd, func(key string, valStart, valEnd, memberStart, memberEnd int) bool {
				switch key {
				case "namespace":
					if nsMemberIdx < 0 {
						nsMemberIdx = len(members)
					}
				case "type":
					if !hasType {
						typeRaw = jsonView(body[valStart:valEnd])
						hasType = true
					}
				}
				members = append(members, jsonObjMember{start: memberStart, end: memberEnd})
				return true
			})
			if !memberOK {
				item := gjson.Parse(elem)
				if !item.IsObject() || !item.Get("namespace").Exists() ||
					(keepToolCallNamespaces && isOpenAIResponsesToolCallItemType(item.Get("type").String())) {
					return true
				}
				deleted, deleteErr := sjson.DeleteBytes(unsafe.Slice(unsafe.SliceData(body[elemStart:elemEnd]), elemEnd-elemStart), "namespace")
				if deleteErr != nil {
					walkErr = fmt.Errorf("delete OpenAI input namespace: %w", deleteErr)
					return false
				}
				edits = append(edits, jsonEdit{
					start:       elemStart,
					end:         elemEnd,
					replacement: unsafe.String(unsafe.SliceData(deleted), len(deleted)),
				})
				return true
			}
			if nsMemberIdx < 0 {
				return true
			}
			if keepToolCallNamespaces && hasType && isOpenAIResponsesToolCallItemType(extractJSONMemberString(typeRaw, false)) {
				return true
			}
			if jsonObjMembersTight(body, elemStart, members) {
				edits = appendJSONMemberDrops(edits, members, nsMemberIdx)
				return true
			}
			deleted, deleteErr := sjson.DeleteBytes(unsafe.Slice(unsafe.SliceData(body[elemStart:elemEnd]), elemEnd-elemStart), "namespace")
			if deleteErr != nil {
				walkErr = fmt.Errorf("delete OpenAI input namespace: %w", deleteErr)
				return false
			}
			edits = append(edits, jsonEdit{
				start:       elemStart,
				end:         elemEnd,
				replacement: unsafe.String(unsafe.SliceData(deleted), len(deleted)),
			})
			return true
		})
		if walkErr != nil {
			return body, walkErr
		}
		if elemOK {
			if len(edits) == 0 {
				return body, nil
			}
			stripped, editErr := applyJSONEdits(body, edits)
			if editErr != nil {
				return body, editErr
			}
			return stripped, nil
		}
	}
	stripped, _, err := rewriteOpenAIResponsesInput(body, func(_ int, item gjson.Result) (string, bool, bool, error) {
		// Long histories mostly contain message/reasoning items without namespace;
		// avoid reading type unless namespace is actually present.
		if !item.IsObject() || !item.Get("namespace").Exists() ||
			(keepToolCallNamespaces && isOpenAIResponsesToolCallItemType(item.Get("type").String())) {
			return item.Raw, true, false, nil
		}
		itemBody, deleteErr := sjson.Delete(item.Raw, "namespace")
		if deleteErr != nil {
			return "", false, false, fmt.Errorf("delete OpenAI input namespace: %w", deleteErr)
		}
		return itemBody, true, true, nil
	})
	if err != nil {
		return body, err
	}
	return stripped, nil
}

func setOpenAIResponsesNamespaceNames(c *gin.Context, names map[string]apicompat.ResponsesNamespaceName) {
	if c != nil && len(names) > 0 {
		c.Set(openAIResponsesNamespaceNamesContextKey, names)
	}
}

// clearOpenAIResponsesNamespaceNames 清除上一次尝试登记的摊平名映射。handler 的
// failover 会在同一个 *gin.Context 上重试下一个账号，映射不清会让保留 namespace 的
// 账号拿着上一个账号的摊平名做回程还原。
func clearOpenAIResponsesNamespaceNames(c *gin.Context) {
	if c == nil {
		return
	}
	if _, exists := c.Get(openAIResponsesNamespaceNamesContextKey); exists {
		c.Set(openAIResponsesNamespaceNamesContextKey, map[string]apicompat.ResponsesNamespaceName(nil))
	}
}

func openAIResponsesNamespaceNames(c *gin.Context) map[string]apicompat.ResponsesNamespaceName {
	if c == nil {
		return nil
	}
	value, ok := c.Get(openAIResponsesNamespaceNamesContextKey)
	if !ok {
		return nil
	}
	names, _ := value.(map[string]apicompat.ResponsesNamespaceName)
	return names
}

func restoreOpenAIResponsesNamespacePayload(c *gin.Context, payload []byte) ([]byte, error) {
	names := openAIResponsesNamespaceNames(c)
	if len(names) == 0 || !json.Valid(payload) {
		return payload, nil
	}
	restored, changed, err := apicompat.RestoreResponsesNamespaceCalls(payload, names)
	if err != nil {
		return payload, err
	}
	if changed {
		return restored, nil
	}
	return payload, nil
}
