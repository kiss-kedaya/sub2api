//go:build unit

package service

// issue #491 回归测试：所有非 Grok 通道的内置工具调用
// （web_search / web_search_preview / image_generation）此前都没有计收按次费用。
//
// 根因有两层：
//  1. Responses/Chat/Messages 到 Responses 的桥接里，内置检索计数只在
//     account.IsGrok() 成立时才执行、才写回结果；
//  2. Anthropic 直通路径根本没有解析 usage.server_tool_use.web_search_requests。
//
// 这里按“三种入口 × 计费落点”覆盖，并锁住边际行为：
//   - 工具名/条目类型别名（web_search / web_search_preview / *_call）
//   - 客户端自己声明的同名函数不能被当成内置工具收费
//   - 流式与非流式都必须计数，且 item.done + response.completed 只算一次
//   - image_generation 走图片计费，与检索按次费用可叠加
//   - 上游报错（平台不支持该工具）时不计费

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// 计数口径
// ---------------------------------------------------------------------------

func TestIssue491NativeSearchCountCoversToolNameAliases(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload string
		want    int
	}{
		{
			name:    "web_search_call",
			payload: `{"output":[{"type":"web_search_call","id":"ws_1"}]}`,
			want:    1,
		},
		{
			name:    "web_search_preview_call",
			payload: `{"output":[{"type":"web_search_preview_call","id":"wsp_1"}]}`,
			want:    1,
		},
		{
			name:    "function_call web_search",
			payload: `{"output":[{"type":"function_call","name":"web_search","call_id":"c1"}]}`,
			want:    1,
		},
		{
			name:    "function_call web_search_preview (issue 样本)",
			payload: `{"output":[{"type":"function_call","name":"web_search_preview","call_id":"c1"}]}`,
			want:    1,
		},
		{
			name:    "custom_tool_call x_search",
			payload: `{"output":[{"type":"custom_tool_call","name":"x_search","call_id":"c1"}]}`,
			want:    1,
		},
		{
			name:    "non search item type",
			payload: `{"output":[{"type":"message","role":"assistant"}]}`,
			want:    0,
		},
		{
			name:    "unrelated function call",
			payload: `{"output":[{"type":"function_call","name":"get_weather","call_id":"c1"}]}`,
			want:    0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, countNativeSearchCallsFromJSONBytes([]byte(tc.payload), nil))
		})
	}
}

// 混合内置工具 + 自定义函数：只有上游执行的内置检索收费。
func TestIssue491NativeSearchCountExcludesClientDeclaredFunctions(t *testing.T) {
	body := []byte(`{"model":"gpt-6.2-sol","tools":[
		{"type":"web_search_preview"},
		{"type":"function","name":"lookup_docs"},
		{"type":"function","function":{"name":"web_search"}}
	]}`)
	skip := declaredClientFunctionToolNames(nil, body)
	require.Contains(t, skip, "web_search")
	require.Contains(t, skip, "lookup_docs")
	require.NotContains(t, skip, "web_search_preview", "内置工具只按类型声明，没有函数名")

	// 上游回包：一次真正执行的内置检索 + 一次要求客户端执行的同名函数调用。
	// 即便两者的可读名字相同，也只有 *_call 条目该收费。
	payload := []byte(`{"output":[
		{"type":"web_search_call","id":"ws_1"},
		{"type":"function_call","name":"web_search","call_id":"c1"}
	]}`)
	require.Equal(t, 1, countNativeSearchCallsFromJSONBytes(payload, skip),
		"客户端自己声明的 web_search 函数调用不是上游内置工具，不能按次收费")

	// 没有客户端声明时，function_call 形态（部分中转把服务端工具拍平成函数调用）
	// 仍应收费，否则这类通道会整体漏收。
	require.Equal(t, 2, countNativeSearchCallsFromJSONBytes(payload, nil))
}

func TestIssue491NativeSearchCountAccumulatesFetchTool(t *testing.T) {
	// fetch / search 由同一个按次预算覆盖，不能因为名字不同就漏掉。
	payload := []byte(`{"output":[
		{"type":"tool_search_call","id":"ts_1"},
		{"type":"function_call","name":"tool_search","call_id":"c1"}
	]}`)
	require.Equal(t, 2, countNativeSearchCallsFromJSONBytes(payload, nil))
}

// ---------------------------------------------------------------------------
// 入口一：/v1/responses
// ---------------------------------------------------------------------------

func issue491ResponsesBody(stream bool) []byte {
	body := `{"model":"gpt-6.1-sol","input":"最新的发布说明","tools":[{"type":"web_search_preview"}]`
	if stream {
		body += `,"stream":true`
	} else {
		body += `,"stream":false`
	}
	return []byte(body + `}`)
}

func issue491UpstreamJSON() string {
	return `{
		"id":"resp_491",
		"object":"response",
		"model":"gpt-6.1-sol",
		"status":"completed",
		"output":[
			{"type":"web_search_call","id":"ws_491","call_id":"c_491","status":"completed"},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}
		],
		"usage":{"input_tokens":12,"output_tokens":7}
	}`
}

func issue491UpstreamSSE() string {
	return strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_491s","status":"in_progress"}}`,
		"",
		`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"web_search_call","id":"ws_491","call_id":"c_491","status":"completed"}}`,
		"",
		`data: {"type":"response.completed","response":{"id":"resp_491s","status":"completed","output":[{"type":"web_search_call","id":"ws_491","call_id":"c_491"}],"usage":{"input_tokens":12,"output_tokens":7}}}`,
		"",
	}, "\n")
}

func issue491OpenAIAccount() *Account {
	return &Account{
		ID: 101, Name: "issue491-openai", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-test", "base_url": "https://upstream.example"},
	}
}

func issue491Service(upstream *httpUpstreamRecorder) *OpenAIGatewayService {
	return &OpenAIGatewayService{
		cfg:           &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}},
		httpUpstream:  upstream,
		toolCorrector: NewCodexToolCorrector(),
	}
}

func issue491Context(t *testing.T, path string, body []byte) *gin.Context {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(body)))
	c.Request.Header.Set("Content-Type", "application/json")
	return c
}

// 非 Grok 平台（OpenAI 兼容）在 /v1/responses 非流式入口必须把内置检索次数
// 写进计费结果——这正是 issue #491 里 347 次 gpt-6.1-sol web_search_preview 漏收的位置。
func TestIssue491ResponsesEntryBillsNativeSearchForNonGrokAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name     string
		account  *Account
		platform string
		model    string
	}{
		{name: "openai", account: issue491OpenAIAccount(), platform: PlatformOpenAI},
		{name: "kimi-responses", account: &Account{
			ID: 103, Name: "issue491-kimi", Platform: PlatformKimi, Type: AccountTypeAPIKey, Concurrency: 1,
			Credentials: map[string]any{
				"api_key": "sk-test", "base_url": "https://upstream.example",
				"api_protocol": APIProtocolResponses,
			},
		}, platform: PlatformKimi, model: "kimi-k2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := issue491ResponsesBody(false)
			if tc.model != "" {
				body = []byte(`{"model":"` + tc.model + `","input":"hi","tools":[{"type":"web_search"}],"stream":false}`)
			}
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"application/json"}, "X-Request-Id": {"req_491"}},
				Body:       io.NopCloser(strings.NewReader(issue491UpstreamJSON())),
			}}
			svc := issue491Service(upstream)
			c := issue491Context(t, "/v1/responses", body)

			result, err := svc.Forward(context.Background(), c, tc.account, body)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, 1, result.SearchCount,
				"%s 通道的内置检索次数必须进入计费结果（issue #491）", tc.platform)
			require.Equal(t, 12, result.Usage.InputTokens)
			require.Equal(t, 7, result.Usage.OutputTokens)
		})
	}
}

// 流式路径同样要计数，并且 item.done + response.completed 不能重复计。
func TestIssue491ResponsesStreamingBillsNativeSearchForNonGrokAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := issue491ResponsesBody(true)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}, "X-Request-Id": {"req_491s"}},
		Body:       io.NopCloser(strings.NewReader(issue491UpstreamSSE())),
	}}
	svc := issue491Service(upstream)
	c := issue491Context(t, "/v1/responses", body)

	result, err := svc.Forward(context.Background(), c, issue491OpenAIAccount(), body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Stream)
	require.Equal(t, 1, result.SearchCount, "流式路径必须计数且去重")
}

// 客户端自定义了同名 web_search 函数时，不能把 function_call 当成上游内置工具收费。
func TestIssue491ResponsesEntrySkipsClientDeclaredWebSearchFunction(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-6.1-sol","input":"hi","stream":false,
		"tools":[{"type":"function","name":"web_search","parameters":{"type":"object"}}]}`)
	upstreamBody := `{"id":"resp_491f","object":"response","model":"gpt-6.1-sol","status":"completed",
		"output":[{"type":"function_call","name":"web_search","call_id":"c1"}],
		"usage":{"input_tokens":3,"output_tokens":2}}`
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}
	svc := issue491Service(upstream)
	c := issue491Context(t, "/v1/responses", body)

	result, err := svc.Forward(context.Background(), c, issue491OpenAIAccount(), body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Zero(t, result.SearchCount, "客户端自己的 web_search 函数不是上游内置工具，不得按次收费")
}

// ---------------------------------------------------------------------------
// 入口二：/v1/chat/completions（转换到 Responses 上游）
// ---------------------------------------------------------------------------

func TestIssue491ChatEntryBillsNativeSearchForNonGrokAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-6.2-sol","messages":[{"role":"user","content":"find it"}],"stream":true}`)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}, "X-Request-Id": {"req_491c"}},
		Body:       io.NopCloser(strings.NewReader(issue491UpstreamSSE())),
	}}
	svc := issue491Service(upstream)
	c := issue491Context(t, "/v1/chat/completions", body)

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, issue491OpenAIAccount(), body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, result.SearchCount, "Chat Completions 入口的内置检索次数必须进入计费结果")
}

func TestIssue491ChatBufferedEntryBillsNativeSearchForNonGrokAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-6.2-sol","messages":[{"role":"user","content":"find it"}],"stream":false}`)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(issue491UpstreamSSE())),
	}}
	svc := issue491Service(upstream)
	c := issue491Context(t, "/v1/chat/completions", body)

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, issue491OpenAIAccount(), body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, result.SearchCount, "非流式（缓冲）路径同样必须计数")
}

// ---------------------------------------------------------------------------
// 入口三：/v1/messages（Anthropic 原生 usage.server_tool_use）
// ---------------------------------------------------------------------------

func TestIssue491AnthropicUsageParsesServerToolUseWebSearch(t *testing.T) {
	body := []byte(`{"id":"msg_491","type":"message","usage":{
		"input_tokens":11,"output_tokens":4,
		"server_tool_use":{"web_search_requests":3}
	}}`)
	usage := parseClaudeUsageFromResponseBody(body)
	require.Equal(t, 3, usage.WebSearchRequests)

	// 缺字段 / null / 负数都不能把计费打成非零或负值。
	for _, tc := range []struct {
		name string
		raw  string
		want int
	}{
		{name: "absent", raw: `{"usage":{"input_tokens":3}}`, want: 0},
		{name: "null", raw: `{"usage":{"server_tool_use":null}}`, want: 0},
		{name: "empty block", raw: `{"usage":{"server_tool_use":{}}}`, want: 0},
		{name: "negative clamped", raw: `{"usage":{"server_tool_use":{"web_search_requests":-2}}}`, want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, parseClaudeUsageFromResponseBody([]byte(tc.raw)).WebSearchRequests)
		})
	}
}

func TestIssue491AnthropicStreamUsageMergesServerToolUseAsCumulativeMax(t *testing.T) {
	var usage ClaudeUsage
	parseSSEUsagePassthrough(`{"type":"message_start","message":{"usage":{"input_tokens":9,"output_tokens":1,"server_tool_use":{"web_search_requests":2}}}}`, &usage)
	parseSSEUsagePassthrough(`{"type":"message_delta","usage":{"output_tokens":5,"server_tool_use":{"web_search_requests":4}}}`, &usage)
	require.Equal(t, 4, usage.WebSearchRequests, "累计值取最大，不能把 start+delta 加成两份")

	// 重复上报同一个终值也不能翻倍。
	parseSSEUsagePassthrough(`{"type":"message_delta","usage":{"output_tokens":5,"server_tool_use":{"web_search_requests":4}}}`, &usage)
	require.Equal(t, 4, usage.WebSearchRequests)
}

func TestIssue491AnthropicEntryBillsServerToolUseWebSearch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name   string
		stream bool
		resp   *http.Response
	}{
		{
			name:   "non-stream",
			stream: false,
			resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"application/json"}, "X-Request-Id": {"req_491a"}},
				Body: io.NopCloser(strings.NewReader(`{"id":"msg_491","type":"message","role":"assistant",
					"content":[{"type":"text","text":"ok"}],"model":"claude-opus-5-5",
					"stop_reason":"end_turn",
					"usage":{"input_tokens":11,"output_tokens":4,"server_tool_use":{"web_search_requests":2}}}`)),
			},
		},
		{
			name:   "stream",
			stream: true,
			resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"text/event-stream"}, "X-Request-Id": {"req_491as"}},
				Body: io.NopCloser(strings.NewReader(
					"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_491s\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-opus-5-5\",\"usage\":{\"input_tokens\":11,\"output_tokens\":1,\"server_tool_use\":{\"web_search_requests\":2}}}}\n\n" +
						"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
						"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n" +
						"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
						"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":4,\"server_tool_use\":{\"web_search_requests\":2}}}\n\n" +
						"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")),
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			streamField := "false"
			if tc.stream {
				streamField = "true"
			}
			body := []byte(`{"model":"claude-opus-5-5","max_tokens":64,"stream":` + streamField + `,
				"messages":[{"role":"user","content":"search"}],
				"tools":[{"type":"web_search_20250305","name":"web_search"}]}`)
			cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}
			svc := &GatewayService{
				cfg:                  cfg,
				httpUpstream:         &httpUpstreamRecorder{resp: tc.resp},
				responseHeaderFilter: compileResponseHeaderFilter(cfg),
				rateLimitService:     &RateLimitService{},
				deferredService:      &DeferredService{},
			}
			c := issue491Context(t, "/v1/messages", body)
			account := &Account{
				ID: 201, Name: "issue491-anthropic", Platform: PlatformAnthropic, Type: AccountTypeAPIKey,
				Concurrency: 1, Status: StatusActive, Schedulable: true,
				Credentials: map[string]any{
					"api_key":  "upstream-anthropic-key",
					"base_url": "https://api.anthropic.com",
				},
				Extra: map[string]any{"anthropic_passthrough": true},
			}
			parsed := &ParsedRequest{Body: NewRequestBodyRef(body), Model: "claude-opus-5-5", Stream: tc.stream}

			result, err := svc.Forward(context.Background(), c, account, parsed)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, 2, result.SearchCount, "Anthropic 通道必须从 usage.server_tool_use.web_search_requests 取数")
			require.Equal(t, 2, result.Usage.WebSearchRequests)
		})
	}
}

// Anthropic 原生上游经 OpenAI 入口转换时，按次次数不能在转换中丢失。
func TestIssue491AnthropicNativeUpstreamKeepsSearchCountForOpenAIEntry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"claude-sonnet-5-5","max_tokens":64,"messages":[{"role":"user","content":"hi"}],"stream":false}`)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}, "X-Request-Id": {"req_491n"}},
		Body: io.NopCloser(strings.NewReader(`{"id":"msg_491n","type":"message","role":"assistant",
			"content":[{"type":"text","text":"ok"}],"model":"claude-sonnet-5-5",
			"usage":{"input_tokens":10,"output_tokens":3,"server_tool_use":{"web_search_requests":5}}}`)),
	}}
	svc := issue491Service(upstream)
	c := issue491Context(t, "/v1/messages", body)

	// 走 Anthropic 协议直通（native anthropic），结果类型是 OpenAIForwardResult。
	account := adaptiveProtocolTestAccount(PlatformKimi, map[string]any{
		APIProtocolAnthropic: "https://anthropic.example",
	})
	result, err := svc.ForwardAsAnthropic(context.Background(), c, account, body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 5, result.SearchCount, "原生 Anthropic 上游的次数必须带到 OpenAI 计费结果")
}

// ---------------------------------------------------------------------------
// image_generation：图片计费与检索按次费用可以叠加
// ---------------------------------------------------------------------------

func TestIssue491ImageGenerationAndSearchCostsAreAdditive(t *testing.T) {
	billing := newTestBillingService()
	price := 10.0 // 每千次 $10
	svc := &OpenAIGatewayService{billingService: billing}
	key := &APIKey{ID: 7, Group: &Group{ID: 43, SearchPricePer1k: &price}}

	// 纯图片：不带检索扣费。
	imageOnly := svc.addOpenAISearchSurcharge(&OpenAIForwardResult{ImageCount: 1}, key, 1, &CostBreakdown{TotalCost: 0.04, ActualCost: 0.04})
	require.InDelta(t, 0.04, imageOnly.TotalCost, 1e-12)

	// 图片 + 2 次检索：0.04 + 2/1000*10。
	mixed := svc.addOpenAISearchSurcharge(&OpenAIForwardResult{ImageCount: 1, SearchCount: 2}, key, 1, &CostBreakdown{TotalCost: 0.04, ActualCost: 0.04})
	require.InDelta(t, 0.06, mixed.TotalCost, 1e-12)
	require.InDelta(t, 0.06, mixed.ActualCost, 1e-12)
}

// image_generation 的按图计费本身与平台无关（走 ImageCount），不能被检索开关连累。
func TestIssue491ImageGenerationCountingIsPlatformAgnostic(t *testing.T) {
	body := []byte(`{"output":[{"type":"image_generation_call","id":"ig_1","result":"aGVsbG8=","size":"1024x1024"}]}`)
	require.Equal(t, 1, countOpenAIResponseImageOutputsFromJSONBytes(body))

	usage := parseClaudeUsageFromResponseBody([]byte(`{"usage":{"input_tokens":1,"output_tokens":1}}`))
	require.Zero(t, usage.WebSearchRequests, "纯图片请求不应产生检索按次费")
}

// 上游报错（平台不支持该内置工具 / 上游 4xx）时，没有任何“已完成的检索”，
// 因此不能计费。锁住降级路径不会因为计数改动而误收费。
func TestIssue491UnsupportedBuiltinToolUpstreamErrorIsNotBilled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := issue491ResponsesBody(false)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"error":{"type":"invalid_request_error","message":"Unsupported tool type: web_search_preview"}}`)),
	}}
	svc := issue491Service(upstream)
	c := issue491Context(t, "/v1/responses", body)

	result, err := svc.Forward(context.Background(), c, issue491OpenAIAccount(), body)
	require.Error(t, err, "上游拒绝内置工具时按现有约定把错误交给客户端/失败切换，不静默降级")
	if result != nil {
		require.Zero(t, result.SearchCount, "失败的请求没有任何已完成的检索，不得收取按次费用")
	}
}

// 保证 Anthropic usage 结构在 JSON 序列化/反序列化里保留 server_tool_use，
// 供网关内部的协议转换链路（apicompat）透传。
func TestIssue491AnthropicUsageJSONRoundTripKeepsServerToolUse(t *testing.T) {
	raw := []byte(`{"input_tokens":5,"output_tokens":2,"server_tool_use":{"web_search_requests":7}}`)
	var usage = parseClaudeUsageFromResponseBody([]byte(`{"usage":` + string(raw) + `}`))
	converted := claudeUsageToAnthropicUsage(*usage)
	require.Equal(t, 7, converted.WebSearchRequests())

	encoded, err := json.Marshal(converted)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"server_tool_use"`)

	var merged ClaudeUsage
	mergeAnthropicUsage(&merged, converted)
	require.Equal(t, 7, merged.WebSearchRequests, "apicompat 合并链路不能丢掉按次次数")
}

// 空 tools / tools=null / 大小写混写：声明解析必须安全，计数必须大小写无关。
func TestIssue491ToolDeclarationEdgeCases(t *testing.T) {
	for _, body := range []string{
		`{"model":"m","input":"x"}`,
		`{"model":"m","input":"x","tools":[]}`,
		`{"model":"m","input":"x","tools":null}`,
		`{"model":"m","input":"x","tools":[{}]}`,
	} {
		require.Nil(t, declaredClientFunctionToolNames(nil, []byte(body)), "空/缺省 tools 不应产生跳过名单: %s", body)
	}

	// 大小写：类型和名字都按小写归一。
	lower := declaredClientFunctionToolNames(nil, []byte(`{"tools":[{"type":"Function","name":"WEB_SEARCH"}]}`))
	require.Contains(t, lower, "web_search")
	require.Equal(t, 0, countNativeSearchCallsFromJSONBytes(
		[]byte(`{"output":[{"type":"FUNCTION_CALL","name":"Web_Search","call_id":"c1"}]}`), lower),
		"大小写不同的同名客户端函数也必须被排除")

	require.Equal(t, 1, countNativeSearchCallsFromJSONBytes(
		[]byte(`{"output":[{"type":"WEB_SEARCH_PREVIEW_CALL","id":"w1"}]}`), nil),
		"条目类型大小写不影响内置检索计数")
}

// 转换入口：/v1/chat/completions 与 /v1/responses 常被转到 Anthropic 上游。
// 这条链路上按次次数同样必须从 usage.server_tool_use 带进计费结果。
func TestIssue491ConvertedEntriesBillAnthropicServerToolUse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, protocol := range []string{"chat", "responses"} {
		for _, stream := range []bool{false, true} {
			name := protocol
			if stream {
				name += "-stream"
			}
			t.Run(name, func(t *testing.T) {
				streamField := "false"
				if stream {
					streamField = "true"
				}
				// 该链路一律强制流式打上游，再由网关聚合，所以两种客户端模式都用 SSE 上游。
				const contentType = "text/event-stream"
				upstreamBody := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_c\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-sonnet-5-5\",\"usage\":{\"input_tokens\":7,\"output_tokens\":1,\"server_tool_use\":{\"web_search_requests\":2}}}}\n\n" +
					"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
					"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n" +
					"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
					"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":3,\"server_tool_use\":{\"web_search_requests\":2}}}\n\n" +
					"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
				upstream := &httpUpstreamRecorder{resp: &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": {contentType}, "X-Request-Id": {"req_491cc"}},
					Body:       io.NopCloser(strings.NewReader(upstreamBody)),
				}}
				cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}
				svc := &GatewayService{
					cfg:                  cfg,
					httpUpstream:         upstream,
					responseHeaderFilter: compileResponseHeaderFilter(cfg),
					rateLimitService:     &RateLimitService{},
					deferredService:      &DeferredService{},
				}
				path := "/v1/chat/completions"
				_ = streamField
				payload := map[string]any{"model": "claude-sonnet-5-5", "messages": []map[string]string{{"role": "user", "content": "search"}}, "stream": stream}
				if protocol == "responses" {
					path = "/v1/responses"
					payload = map[string]any{"model": "claude-sonnet-5-5", "input": "search", "stream": stream}
				}
				reqBody, err := json.Marshal(payload)
				require.NoError(t, err)
				c := issue491Context(t, path, reqBody)
				c.Set("api_key", &APIKey{Group: &Group{CrossProtocolConversionEnabled: true}})
				parsed := &ParsedRequest{Body: NewRequestBodyRef(reqBody), Model: "claude-sonnet-5-5", Stream: stream}
				account := newAnthropicAPIKeyAccountForTest()

				var result *ForwardResult
				if protocol == "chat" {
					result, err = svc.ForwardAsChatCompletions(context.Background(), c, account, reqBody, parsed)
				} else {
					result, err = svc.ForwardAsResponses(context.Background(), c, account, reqBody, parsed)
				}
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, 2, result.SearchCount,
					"%s 入口转 Anthropic 上游时必须把 server_tool_use 次数带进计费", protocol)
			})
		}
	}
}
