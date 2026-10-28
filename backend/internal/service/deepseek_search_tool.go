package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// DeepSeek 搜索工具的注入与执行。
//
// 与 Anthropic 的 server-side web_search 不同，DeepSeek 官方目前把搜索作为
// 独立接口（/v1/tools/search），模型本身不会自己去调。这里做两件事：
//
//  1. 请求上游前，给分组启用搜索的 DeepSeek 请求注入一个 `web_search`
//     function tool，让模型知道「可以要求搜索」。
//  2. 上游返回对这个工具的 tool_call 时，网关**自己**调一次
//     /v1/tools/search，把结果作为 tool result 直接交给下游用户。
//
// 关键取舍：结果**不回填给上游**。也就是说模型这一轮只负责「提出要搜什么」，
// 真正执行由网关完成、结果给客户端。这样不需要多轮上游往返，也不会因为
// 网关代跑工具而拉长上游会话。
const (
	deepSeekSearchToolName = "web_search"
	// 上游若声明了这个名字，说明请求里已经有同名工具，不再重复注入。
	deepSeekSearchToolDescription = "Search the web for current information. " +
		"Call this when the answer needs up-to-date facts, news, prices, or anything " +
		"outside the model's knowledge cutoff."

	// deepSeekSearchToolTimeout 单次搜索的执行上限。搜索挂在主响应路径上，
	// 上游慢的话不能无限等（官方 QPS 5，正常几秒内返回）。
	deepSeekSearchToolTimeout = 15 * time.Second
)

// deepSeekSearchToolSchema 是注入给模型的 function 定义。
var deepSeekSearchToolSchema = map[string]any{
	"type": "function",
	"function": map[string]any{
		"name":        deepSeekSearchToolName,
		"description": deepSeekSearchToolDescription,
		"parameters": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "The search query, 1-128 characters.",
				},
				"count": map[string]any{
					"type":        "integer",
					"description": "How many results to return, 1-50. Defaults to 10.",
				},
			},
			"required": []string{"query"},
		},
	},
}

// injectDeepSeekSearchTool 在请求体里补一个 web_search 工具。
//
// 只在分组启用了搜索、且请求里还没有同名工具时注入。返回新的 body 与是否改动。
func injectDeepSeekSearchTool(body []byte) ([]byte, bool) {
	if len(body) == 0 {
		return body, false
	}
	tools := gjson.GetBytes(body, "tools")
	if tools.IsArray() {
		exists := false
		tools.ForEach(func(_, item gjson.Result) bool {
			if item.Get("function.name").String() == deepSeekSearchToolName ||
				item.Get("name").String() == deepSeekSearchToolName {
				exists = true
				return false
			}
			return true
		})
		if exists {
			return body, false
		}
		next, err := sjson.SetBytes(body, "tools.-1", deepSeekSearchToolSchema)
		if err != nil {
			return body, false
		}
		return next, true
	}
	next, err := sjson.SetBytes(body, "tools", []any{deepSeekSearchToolSchema})
	if err != nil {
		return body, false
	}
	return next, true
}

// isDeepSeekSearchCall 判断某个 tool_call 是否是注入的搜索工具。
func isDeepSeekSearchCall(name string) bool {
	return strings.TrimSpace(name) == deepSeekSearchToolName
}

// deepSeekSearchCallQuery 从 tool_call 的 arguments 里取 query 与 count。
func deepSeekSearchCallQuery(arguments string) (query string, count int) {
	arguments = strings.TrimSpace(arguments)
	if arguments == "" {
		return "", 0
	}
	query = strings.TrimSpace(gjson.Get(arguments, "query").String())
	if n := gjson.Get(arguments, "count"); n.Exists() && n.Type == gjson.Number {
		count = int(n.Int())
	}
	return query, count
}

// buildDeepSeekSearchToolResult 把 /v1/tools/search 的结果压成给模型的
// tool result 内容。保留标题、URL、摘要，丢掉正文（体积太大）。
func buildDeepSeekSearchToolResult(raw []byte) (string, error) {
	results := gjson.GetBytes(raw, "results")
	if !results.IsArray() {
		return "", fmt.Errorf("deepseek search: unexpected response shape")
	}

	type hit struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Snippet string `json:"snippet"`
		Summary string `json:"summary"`
		Date    string `json:"date,omitempty"`
	}
	hits := make([]hit, 0, len(results.Array()))
	results.ForEach(func(_, item gjson.Result) bool {
		hits = append(hits, hit{
			Title:   item.Get("title").String(),
			URL:     item.Get("url").String(),
			Snippet: item.Get("snippet").String(),
			Summary: item.Get("summary").String(),
			Date:    item.Get("date").String(),
		})
		return true
	})

	encoded, err := json.Marshal(map[string]any{
		"results": hits,
		"count":   len(hits),
	})
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// executeDeepSeekSearchCalls 遍历下游将要看到的 tool_call 列表，对命中
// web_search 的调用执行一次真实搜索，返回 call_id → tool result 内容。
//
// 调用方拿到 map 后把结果作为 tool result 交给下游；**不回填上游**，
// 因此一轮上游请求只会产生一次搜索，不存在多轮循环。
func (s *OpenAIGatewayService) executeDeepSeekSearchCalls(
	ctx context.Context, c *gin.Context, account *Account, group *Group, calls []apicompat.DeepSeekCall,
) map[string]string {
	if s == nil || account == nil || len(calls) == 0 {
		return nil
	}
	results := make(map[string]string, len(calls))
	for _, call := range calls {
		if !isDeepSeekSearchCall(call.Name) {
			continue
		}
		query, count := deepSeekSearchCallQuery(call.Arguments)
		if query == "" {
			// The model called an injected tool without a usable query. Pair the
			// call with an error result so the next upstream turn never sees a
			// tool call without its output.
			results[call.CallID] = `{"error":"invalid_query","message":"search query was empty"}`
			continue
		}
		payload, err := json.Marshal(map[string]any{"query": query, "count": clampDeepSeekSearchCount(count)})
		if err != nil {
			results[call.CallID] = `{"error":"invalid_query","message":"search query could not be encoded"}`
			continue
		}
		content, ok := s.runDeepSeekSearchCall(ctx, c, account, group, payload)
		if !ok {
			// Retry once: hosted search failures are often transient.
			time.Sleep(deepSeekSearchRetryDelay)
			content, ok = s.runDeepSeekSearchCall(ctx, c, account, group, payload)
		}
		if !ok {
			content = `{"error":"search_unavailable","message":"search tool failed after retry"}`
		}
		results[call.CallID] = content
	}
	if len(results) == 0 {
		return nil
	}
	return results
}

// runDeepSeekSearchCall executes one injected search call with its own timeout.
func (s *OpenAIGatewayService) runDeepSeekSearchCall(
	ctx context.Context, c *gin.Context, account *Account, group *Group, payload []byte,
) (string, bool) {
	searchCtx, cancel := context.WithTimeout(ctx, deepSeekSearchToolTimeout)
	defer cancel()
	recorder := httptest.NewRecorder()
	shadow, _ := gin.CreateTestContext(recorder)
	// c.Request 在某些内部路径下可能为空；用一个最小请求占位，
	// ForwardDeepSeekTools 只用到 Host/Header，不依赖入站 body。
	if c != nil && c.Request != nil {
		shadow.Request = c.Request.Clone(searchCtx)
	} else {
		shadow.Request = httptest.NewRequest("POST", "/v1/tools/search", nil).WithContext(searchCtx)
	}
	if _, err := s.ForwardDeepSeekTools(searchCtx, shadow, account, group, DeepSeekToolsPathSearch, "", payload); err != nil {
		slog.Warn("deepseek search tool call failed", "account_id", account.ID, "error", err)
		return "", false
	}
	content, err := buildDeepSeekSearchToolResult(recorder.Body.Bytes())
	if err != nil {
		return "", false
	}
	return content, true
}

const deepSeekSearchRetryDelay = 150 * time.Millisecond

// clampDeepSeekSearchCount 把 count 收进官方允许的 1..50。
func clampDeepSeekSearchCount(count int) int {
	if count <= 0 {
		return 10
	}
	if count > 50 {
		return 50
	}
	return count
}

// deepSeekToolInjectionEnabled controls only tools added by the gateway. An
// omitted setting on an old key (or old auth-cache entry) keeps injection on.
// This must not gate deepSeekSearchGroupEnabled: a client-declared native search
// and the independent Search/Fetch endpoints keep their existing capabilities.
func deepSeekToolInjectionEnabled(c *gin.Context) bool {
	if c == nil {
		return true
	}
	value, exists := c.Get("api_key")
	if !exists {
		return true
	}
	apiKey, ok := value.(*APIKey)
	return !ok || apiKey == nil || apiKey.IsDeepSeekToolInjectionEnabled()
}

// deepSeekSearchGroupEnabled 判断当前请求所属分组是否启用了搜索能力。
//
// 判定标准：DeepSeek 平台 + 分组配了搜索单价（search_price_per_1k）。
// 没配价格就不注入工具，避免给免费用户白送按次计费的上游搜索。
func deepSeekSearchGroupEnabled(c *gin.Context, account *Account) (*Group, bool) {
	if c == nil || account == nil || account.Platform != PlatformDeepseek {
		return nil, false
	}
	value, exists := c.Get("api_key")
	if !exists {
		return nil, false
	}
	apiKey, ok := value.(*APIKey)
	if !ok || apiKey == nil || apiKey.Group == nil {
		return nil, false
	}
	group := apiKey.Group
	if group.Platform != PlatformDeepseek {
		return nil, false
	}
	if group.SearchPricePer1k == nil {
		return nil, false
	}
	return group, true
}

// applyDeepSeekSearchResults 在响应体里找出 web_search 的 tool_call，
// 逐个执行搜索，并把结果作为 tool result 附在响应里交给客户端。
//
// 支持 Responses 形状（output[] 里的 function_call）与 Chat Completions
// 形状（choices[].message.tool_calls[]）。只处理注入过的工具名，其它
// tool_call 原样保留，客户端自己的工具不受影响。
func (s *OpenAIGatewayService) applyDeepSeekSearchResults(
	c *gin.Context, account *Account, group *Group, body []byte,
) ([]byte, bool) {
	if len(body) == 0 || account == nil || !deepSeekToolInjectionEnabled(c) ||
		(c != nil && c.Request != nil && c.Request.Context().Value(deepSeekHostedSearchSkipContextKey{}) == true) {
		return body, false
	}
	calls := collectDeepSeekSearchCalls(body)
	if len(calls) == 0 {
		return body, false
	}
	results := s.executeDeepSeekSearchCalls(c.Request.Context(), c, account, group, calls)
	if len(results) == 0 {
		return body, false
	}
	next, changed := attachDeepSeekSearchResults(body, results)
	if !changed {
		return body, false
	}
	return next, true
}

// collectDeepSeekSearchCalls 从响应体里收集注入工具的调用。
func collectDeepSeekSearchCalls(body []byte) []apicompat.DeepSeekCall {
	var calls []apicompat.DeepSeekCall
	// Responses: output[].type == "function_call"
	gjson.GetBytes(body, "output").ForEach(func(_, item gjson.Result) bool {
		if item.Get("type").String() != "function_call" {
			return true
		}
		name := item.Get("name").String()
		if !isDeepSeekSearchCall(name) {
			return true
		}
		calls = append(calls, apicompat.DeepSeekCall{
			CallID:    item.Get("call_id").String(),
			Name:      name,
			Arguments: item.Get("arguments").String(),
		})
		return true
	})
	// Chat Completions: choices[].message.tool_calls[]
	gjson.GetBytes(body, "choices").ForEach(func(_, choice gjson.Result) bool {
		choice.Get("message.tool_calls").ForEach(func(_, item gjson.Result) bool {
			name := item.Get("function.name").String()
			if !isDeepSeekSearchCall(name) {
				return true
			}
			calls = append(calls, apicompat.DeepSeekCall{
				CallID:    item.Get("id").String(),
				Name:      name,
				Arguments: item.Get("function.arguments").String(),
			})
			return true
		})
		return true
	})
	return calls
}

// attachDeepSeekSearchResults 把搜索结果写回响应体。
//
// Responses 形状追加一个 function_call_output 到 output；Chat Completions
// 形状把结果挂到 message.search_results（非标准字段，客户端可选用）。
func attachDeepSeekSearchResults(body []byte, results map[string]string) ([]byte, bool) {
	changed := false
	// Responses
	if gjson.GetBytes(body, "output").IsArray() {
		next := body
		for callID, content := range results {
			item := map[string]any{
				"type":    "function_call_output",
				"call_id": callID,
				"output":  content,
			}
			updated, err := sjson.SetBytes(next, "output.-1", item)
			if err != nil {
				continue
			}
			next = updated
			changed = true
		}
		return next, changed
	}
	// Chat Completions
	next := body
	index := 0
	gjson.GetBytes(body, "choices").ForEach(func(_, choice gjson.Result) bool {
		for callID, content := range results {
			updated, err := sjson.SetBytes(next,
				fmt.Sprintf("choices.%d.message.search_results", index),
				map[string]any{"call_id": callID, "results": content})
			if err == nil {
				next = updated
				changed = true
			}
		}
		index++
		return true
	})
	return next, changed
}
