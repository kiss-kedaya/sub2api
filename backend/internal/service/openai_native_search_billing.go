package service

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// 内置检索工具（web_search / web_search_preview / x_search / tool_search）的按次
// 计费口径（issue #491）。
//
// 历史实现把“统计次数”和“写入计费结果”都锁死在 Grok 账号上，导致 OpenAI /
// Anthropic / DeepSeek 等通道虽然同样在上游调用内置检索，SearchCount 却恒为 0，
// 整批按次费用漏收。这里把判定收敛成一个与平台无关的口径：
//
//  1. 上游确实执行了内置检索工具（Responses output 里的 *_search_call，或
//     function_call / custom_tool_call 且名字是检索工具名）；
//  2. 该名字不是客户端自己声明的函数工具——function_call 表示“模型要求客户端
//     调用这个函数”，网关既没有执行也没有向上游付费，绝不能按内置工具收费。
const openAINativeSearchSkipFunctionToolsContextKey = "openai_native_search_skip_function_tools"

// openAINativeSearchBillingApplies 报告本次请求是否把内置检索次数写进计费结果。
// 只按账号是否存在判断（没有账号就没有计费主体），不再按平台过滤。
func openAINativeSearchBillingApplies(account *Account) bool {
	return account != nil
}

// rememberOpenAINativeSearchSkipFunctionTools 记录客户端在请求里自己声明的函数
// 工具名（小写）。上游回包里的同名 function_call 属于客户端工具调用，不能算成
// 上游内置工具，否则会和客户端自己的实现重复收费。
func rememberOpenAINativeSearchSkipFunctionTools(c *gin.Context, body []byte) {
	if c == nil {
		return
	}
	names := declaredClientFunctionToolNames(c, body)
	if len(names) == 0 {
		c.Set(openAINativeSearchSkipFunctionToolsContextKey, map[string]struct{}{})
		return
	}
	c.Set(openAINativeSearchSkipFunctionToolsContextKey, names)
}

// openAINativeSearchSkipFunctionTools 取回 rememberOpenAINativeSearchSkipFunctionTools
// 记录的客户端函数工具名。
func openAINativeSearchSkipFunctionTools(c *gin.Context) map[string]struct{} {
	if c == nil {
		return nil
	}
	value, ok := c.Get(openAINativeSearchSkipFunctionToolsContextKey)
	if !ok {
		return nil
	}
	names, _ := value.(map[string]struct{})
	return names
}

// declaredClientFunctionToolNames 从请求体里收集客户端声明的函数/自定义工具名。
//
// 同时支持三种协议写法：
//   - Responses:  tools[].name（type 为 function / custom 或缺失）
//   - Chat:       tools[].function.name
//   - 旧版 Chat:  functions[].name
//
// 内置工具（例如 {"type":"web_search_preview"}）没有 name，不会被收集。
//
// 内存：返回的 map 会挂在 gin.Context 上活到请求结束（流式请求可能几十秒到几分钟）。
// 这里必须同时做到两件事，否则一个 map 就会把整份请求体拖到请求结束：
//   - 不能用 gjson.ParseBytes——它会把整个请求体复制成 string，而 tool.Get("name").String()
//     返回的是该 string 的子串；ToLower/TrimSpace 在无需改动时不复制，map key 就把整份
//     复制品钉住（生产 heap profile：298 个存活复制品、551 MB）。改用零拷贝只读视图。
//   - 视图上取到的 name 仍指向请求体本身，key 必须 strings.Clone 成独立的小字符串，
//     既不钉住请求体，也不会因为请求体之后被改写而变形。
func declaredClientFunctionToolNames(c *gin.Context, body []byte) map[string]struct{} {
	if len(body) == 0 || !RequestBodyTop(c, body).Valid() {
		return nil
	}
	root := parseRawJSONView(body)
	if !root.IsObject() {
		return nil
	}
	names := make(map[string]struct{}, 4)
	add := func(name string) {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			return
		}
		if _, dup := names[name]; dup {
			return
		}
		names[strings.Clone(name)] = struct{}{}
	}
	collectTools := func(tools gjson.Result) {
		tools.ForEach(func(_, tool gjson.Result) bool {
			if !tool.IsObject() {
				return true
			}
			kind := strings.ToLower(strings.TrimSpace(tool.Get("type").String()))
			switch kind {
			case "", "function", "custom":
			default:
				// 内置工具（web_search / web_search_preview / image_generation ...）
				// 只按类型声明，没有可冲突的客户端函数名。
				return true
			}
			add(tool.Get("name").String())
			add(tool.Get("function.name").String())
			return true
		})
	}
	collectFunctions := func(functions gjson.Result) {
		functions.ForEach(func(_, fn gjson.Result) bool {
			add(fn.Get("name").String())
			add(fn.Get("function.name").String())
			return true
		})
	}
	// 一次遍历顶层成员取 tools / functions；重复键只认第一个（与 gjson.Get 一致）。
	var seenTools, seenFunctions bool
	root.ForEach(func(key, value gjson.Result) bool {
		switch key.Str {
		case "tools":
			if !seenTools {
				seenTools = true
				collectTools(value)
			}
		case "functions":
			if !seenFunctions {
				seenFunctions = true
				collectFunctions(value)
			}
		}
		return !seenTools || !seenFunctions
	})
	if len(names) == 0 {
		return nil
	}
	return names
}

// isClientDeclaredFunctionTool 判断某个 function_call / custom_tool_call 的名字
// 是否命中客户端自己声明的函数工具。
func isClientDeclaredFunctionTool(name string, skip map[string]struct{}) bool {
	if len(skip) == 0 {
		return false
	}
	_, ok := skip[strings.ToLower(strings.TrimSpace(name))]
	return ok
}

// openAIWSTerminalEventMayContainNativeSearch 报告某个 WebSocket Responses 事件
// 是否可能携带完整 output 数组（内置检索条目只在这些事件里出现）。
//
// 单独抽出来是为了让 WS 热路径只在终止事件上解析 JSON，而不是逐帧解析。
func openAIWSTerminalEventMayContainNativeSearch(eventType string) bool {
	switch eventType {
	case "response.output_item.done", "response.completed", "response.done":
		return true
	default:
		return false
	}
}
