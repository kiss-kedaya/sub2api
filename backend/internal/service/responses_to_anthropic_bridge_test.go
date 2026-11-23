package service

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
	"github.com/tidwall/gjson"
)

// anthropicResponsesSSE 是上游 /v1/messages 的最小流式响应：文本 "ok" + usage。
func anthropicResponsesSSE() string {
	return strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_bridge","type":"message","role":"assistant","content":[],"model":"claude-opus-5-5","usage":{"input_tokens":12,"cache_read_input_tokens":3,"cache_creation_input_tokens":1}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``, ``,
	}, "\n")
}

func newResponsesBridgeTestService(upstream *anthropicHTTPUpstreamRecorder) *GatewayService {
	cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}
	return &GatewayService{cfg: cfg, httpUpstream: upstream, responseHeaderFilter: compileResponseHeaderFilter(cfg)}
}

func responsesBridgeTestContext(t *testing.T, path string, apiKey *APIKey) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, path, nil)
	c.Request.Header.Set("Content-Type", "application/json")
	if apiKey != nil {
		c.Set("api_key", apiKey)
	}
	return c, rec
}

func responsesBridgeUpstreamRecorder() *anthropicHTTPUpstreamRecorder {
	return &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}, "X-Request-Id": {"resp-bridge-test"}},
		Body:       io.NopCloser(strings.NewReader(anthropicResponsesSSE())),
	}}
}

// 反例（线上 503 场景）：账号确实支持请求的模型，分组开启跨协议转换，
// 入站是 /v1/responses —— 必须走 Responses→Anthropic 转换并成功，不能
// 在调度层被判成"无账号支持模型 / 协议不匹配"而 503。
func TestResponsesIngressOnAnthropicGroupConvertsInsteadOfNoAvailableAccounts(t *testing.T) {
	account := newAnthropicAPIKeyAccountForTest()
	account.Credentials["base_url"] = "https://api.anthropic.com"
	// 账号实际支持这两个模型（管理端连接测试 14/14 成功的那只号）。
	require.True(t, account.IsModelSupported("claude-opus-5-5"))
	require.True(t, account.IsModelSupported("claude-fable-5-2"))

	upstream := responsesBridgeUpstreamRecorder()
	svc := newResponsesBridgeTestService(upstream)

	groupID := int64(118)
	apiKey := &APIKey{
		ID: 1, UserID: 2, GroupID: &groupID, Status: StatusActive,
		Group: &Group{ID: groupID, Name: "Claude 嘿嘿嘿", Platform: PlatformAnthropic, Status: StatusActive, Hydrated: true,
			CrossProtocolConversionEnabled: true},
	}
	c, rec := responsesBridgeTestContext(t, "/v1/responses", apiKey)

	body := []byte(`{
		"model":"claude-opus-5-5",
		"instructions":"be brief",
		"max_output_tokens":128,
		"input":[{"role":"user","content":[{"type":"input_text","text":"hi"}]}],
		"stream":false,
		"previous_response_id":"resp_prev_9",
		"store":true,
		"metadata":{"trace":"t-1"},
		"reasoning":{"effort":"none"},
		"temperature":0.3,
		"tools":[{"type":"function","name":"read","parameters":{"type":"object"}}]
	}`)
	parsed := &ParsedRequest{Body: NewRequestBodyRef(body), Model: "claude-opus-5-5"}

	result, err := svc.ForwardAsResponses(context.Background(), c, account, body, parsed)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastReq, "conversion must actually reach the Anthropic upstream")
	require.Equal(t, "/v1/messages", upstream.lastReq.URL.Path, "must convert Responses→Anthropic Messages")

	// 请求映射：input/instructions → messages/system；max_output_tokens → max_tokens。
	require.True(t, gjson.GetBytes(upstream.lastBody, "messages").IsArray())
	require.False(t, gjson.GetBytes(upstream.lastBody, "input").Exists())
	require.Equal(t, "hi", gjson.GetBytes(upstream.lastBody, "messages.0.content.0.text").String())
	require.Equal(t, int64(128), gjson.GetBytes(upstream.lastBody, "max_tokens").Int())
	require.True(t, gjson.GetBytes(upstream.lastBody, "stream").Bool(), "upstream is always streamed")

	// 透传保留：转换器不认识的字段原样送给上游，不因未知字段 400。
	require.Equal(t, "resp_prev_9", gjson.GetBytes(upstream.lastBody, "previous_response_id").String())
	require.True(t, gjson.GetBytes(upstream.lastBody, "store").Bool())
	require.Equal(t, "t-1", gjson.GetBytes(upstream.lastBody, "metadata.trace").String(), "metadata is forwarded verbatim")

	// 用户参数一律不改写：reasoning.effort="none" 原样到达上游，不被删除、
	// 不被替换成 thinking/output_config；temperature 保持原值。
	require.Equal(t, "none", gjson.GetBytes(upstream.lastBody, "reasoning.effort").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "thinking").Exists(), "gateway must not synthesize thinking")
	require.False(t, gjson.GetBytes(upstream.lastBody, "output_config").Exists(), "gateway must not synthesize output_config")
	require.InDelta(t, 0.3, gjson.GetBytes(upstream.lastBody, "temperature").Float(), 1e-9)

	// 回程映射 + usage 不能少扣。
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"output_text"`)
	require.Contains(t, rec.Body.String(), "ok")
	require.Equal(t, 12, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.OutputTokens)
	require.Equal(t, 3, result.Usage.CacheReadInputTokens)
	require.Equal(t, 1, result.Usage.CacheCreationInputTokens)
}

// 分组关闭跨协议转换：入口返回 400 PROTOCOL_MISMATCH（带 path/group_platform/hint），
// 不静默转换、不调用上游，更不能落到 503 no available accounts。
func TestResponsesIngressOnAnthropicGroupWithoutConversionReturnsProtocolMismatch(t *testing.T) {
	upstream := responsesBridgeUpstreamRecorder()
	svc := newResponsesBridgeTestService(upstream)

	groupID := int64(118)
	apiKey := &APIKey{
		ID: 1, UserID: 2, GroupID: &groupID, Status: StatusActive,
		Group: &Group{ID: groupID, Name: "Claude 嘿嘿嘿", Platform: PlatformAnthropic, Status: StatusActive, Hydrated: true,
			CrossProtocolConversionEnabled: false},
	}
	c, rec := responsesBridgeTestContext(t, "/v1/responses", apiKey)

	body := []byte(`{"model":"claude-opus-5-5","input":"hi","stream":false}`)
	parsed := &ParsedRequest{Body: NewRequestBodyRef(body), Model: "claude-opus-5-5"}

	_, err := svc.ForwardAsResponses(context.Background(), c, newAnthropicAPIKeyAccountForTest(), body, parsed)
	require.Error(t, err)
	require.True(t, IsCrossProtocolConversionDisabled(err))
	require.Nil(t, upstream.lastReq, "a protocol mismatch must be rejected before any upstream call")

	require.Equal(t, http.StatusBadRequest, rec.Code)
	var payload struct {
		Error struct {
			Code          string `json:"code"`
			Message       string `json:"message"`
			Path          string `json:"path"`
			GroupPlatform string `json:"group_platform"`
			Hint          string `json:"hint"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Equal(t, "PROTOCOL_MISMATCH", payload.Error.Code)
	require.Equal(t, "/v1/responses", payload.Error.Path)
	require.Equal(t, PlatformAnthropic, payload.Error.GroupPlatform)
	require.NotEmpty(t, payload.Error.Hint)
	require.Contains(t, payload.Error.Hint, "/v1/messages")
	require.Contains(t, payload.Error.Message, "跨协议转换")
}

// 认证缓存里由旧进程写入的快照没有 cross_protocol_conversion_enabled 字段时，
// 必须按"允许"解析（迁移 251 把存量分组回填为 true），并且能真的走到转换链路，
// 不能因为缓存字段缺失把 /v1/responses 判成 400/503。
func TestResponsesIngressWithLegacyAuthSnapshotStillConverts(t *testing.T) {
	payload := crossProtocolSnapshotPayload(t, true)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(payload, &raw))
	snapshot, ok := raw["snapshot"].(map[string]any)
	require.True(t, ok)
	group, ok := snapshot["group"].(map[string]any)
	require.True(t, ok)
	delete(group, "cross_protocol_conversion_enabled") // 旧版进程写的快照
	legacy, err := json.Marshal(raw)
	require.NoError(t, err)
	apiKey := materializeAuthSnapshot(t, legacy)
	require.True(t, apiKey.Group.Hydrated)
	require.True(t, CrossProtocolConversionAllowed(apiKey))

	upstream := responsesBridgeUpstreamRecorder()
	svc := newResponsesBridgeTestService(upstream)
	c, rec := responsesBridgeTestContext(t, "/v1/responses", apiKey)
	body := []byte(`{"model":"claude-opus-5-5","input":"hi","stream":false}`)
	parsed := &ParsedRequest{Body: NewRequestBodyRef(body), Model: "claude-opus-5-5"}

	result, err := svc.ForwardAsResponses(context.Background(), c, newAnthropicAPIKeyAccountForTest(), body, parsed)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, "/v1/messages", upstream.lastReq.URL.Path)
	require.Equal(t, http.StatusOK, rec.Code)
}

// 入站 /v1/responses 的平台分发：只有 OpenAI 兼容平台走 OpenAI 网关；
// anthropic（以及其它协议族之外）必须交给 Gateway.Responses 做转换。
func TestResponsesIngressDispatchKeepsAnthropicOnConversionHandler(t *testing.T) {
	require.False(t, IsOpenAIResponsesCompatibleGatewayPlatform(PlatformAnthropic),
		"anthropic 分组的 /v1/responses 必须走 Gateway.Responses 转换链，不能误分发到 OpenAI 网关")
	require.False(t, IsOpenAIResponsesCompatibleGatewayPlatform(PlatformComposite),
		"composite 分组按 resolved target platform 分发，不能按 composite 直接走 OpenAI 网关")
	for _, platform := range []string{PlatformOpenAI, PlatformGrok, PlatformKimi, PlatformZhipu, PlatformDeepseek, PlatformMiniMax, PlatformCline, PlatformCommandCode} {
		require.True(t, IsOpenAIResponsesCompatibleGatewayPlatform(platform), platform)
	}
}

// 上游拒绝用户参数（如 reasoning.effort="none" 不被支持）时，状态码与错误文本
// 原样透传，不改写成统一的 "Upstream request failed"。
func TestResponsesIngressUpstreamRejectionIsPassedThrough(t *testing.T) {
	upstreamBody := `{"type":"error","error":{"type":"invalid_request_error","message":"Unsupported value: 'none'"}}`
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": {"application/json"}, "X-Request-Id": {"upstream-reject"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}
	svc := newResponsesBridgeTestService(upstream)

	groupID := int64(118)
	apiKey := &APIKey{
		ID: 1, UserID: 2, GroupID: &groupID, Status: StatusActive,
		Group: &Group{ID: groupID, Name: "Claude 嘿嘿嘿", Platform: PlatformAnthropic, Status: StatusActive, Hydrated: true,
			CrossProtocolConversionEnabled: true},
	}
	c, rec := responsesBridgeTestContext(t, "/v1/responses", apiKey)
	body := []byte(`{"model":"claude-opus-5-5","input":"hi","stream":false,"reasoning":{"effort":"none"}}`)
	parsed := &ParsedRequest{Body: NewRequestBodyRef(body), Model: "claude-opus-5-5"}

	result, err := svc.ForwardAsResponses(context.Background(), c, newAnthropicAPIKeyAccountForTest(), body, parsed)
	require.Error(t, err)
	require.Nil(t, result)
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, "none", gjson.GetBytes(upstream.lastBody, "reasoning.effort").String(),
		"user parameter must reach upstream unchanged even when it will be rejected")

	require.Equal(t, http.StatusBadRequest, rec.Code, "upstream status code is preserved")
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Equal(t, "invalid_request_error", payload.Error.Code, "upstream error type is preserved as error code")
	require.Contains(t, payload.Error.Message, "Unsupported value: 'none'", "upstream error text must pass through verbatim")
}
