//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func deepSeekNativeReplayContext(keyID int64, session string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("session-id", session)
	c.Set("api_key", &APIKey{ID: keyID})
	return c, recorder
}

func deepSeekNativeReplayAccount() *Account {
	return &Account{ID: 7, Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"api_key": "model-test-key", "api_protocol": APIProtocolAdaptive, "base_url": "https://api.deepseek.com"}}
}

func TestDeepSeekNativeReasoningReplayAcrossInstances(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "stream"}[stream], func(t *testing.T) {
			first := hostedSearchResponse("resp_reasoning", 10, 3,
				map[string]any{"type": "reasoning", "id": "rs_original", "summary": []any{}, "encrypted_content": "opaque-real",
					"content": []any{map[string]string{"type": "reasoning_text", "text": "Only the genuine upstream reasoning."}}},
				map[string]any{"type": "function_call", "id": "fc_lookup", "call_id": "call_lookup", "name": "lookup", "arguments": "{}", "status": "completed"})
			response, contentType := string(first), "application/json"
			if stream {
				response, contentType = hostedSearchSSE(first), "text/event-stream"
			}
			cache := &reasoningRecordingCache{}
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(response))}}
			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), cache: cache, httpUpstream: upstream}
			c, rec := deepSeekNativeReplayContext(21, "native-thread")
			body, err := sjson.SetBytes([]byte(`{"model":"deepseek-v4.1-flash","input":"read the tool result","tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]}`), "stream", stream)
			require.NoError(t, err)
			c.Request.Body = io.NopCloser(bytes.NewReader(body))
			_, err = svc.Forward(context.Background(), c, deepSeekNativeReplayAccount(), body)
			require.NoError(t, err, rec.Body.String())
			written := cache.snapshotSets()
			require.NotEmpty(t, written, "native output must be cached, not only the Chat fallback")
			for key := range written {
				require.True(t, strings.HasPrefix(key, "deepseek-native-v1:"))
				require.NotContains(t, key, "rs_original")
			}

			// A different process receives Codex's stripped replay and shares only
			// Redis data. No gateway-local map is needed to resume the tool turn.
			replay := []byte(`{"model":"deepseek-v4.1-flash","stream":false,"input":[{"role":"user","content":"read the tool result"},{"type":"reasoning","id":"rs_original","summary":[],"encrypted_content":"opaque-real","opaque":9007199254740993},{"type":"function_call","call_id":"call_lookup","name":"lookup","arguments":"{}"},{"type":"function_call_output","call_id":"call_lookup","output":"42"}],"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]}`)
			nextUpstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(hostedSearchResponse("resp_answer", 12, 2, hostedSearchAnswer())))}}
			next := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), cache: &reasoningRecordingCache{getResp: written}, httpUpstream: nextUpstream}
			nextContext, nextRec := deepSeekNativeReplayContext(21, "native-thread")
			_, err = next.Forward(context.Background(), nextContext, deepSeekNativeReplayAccount(), replay)
			require.NoError(t, err, nextRec.Body.String())
			require.Equal(t, "Only the genuine upstream reasoning.", gjson.GetBytes(nextUpstream.lastBody, "input.1.content.0.text").String())
			require.Equal(t, "9007199254740993", gjson.GetBytes(nextUpstream.lastBody, "input.1.opaque").Raw)
			require.Equal(t, "opaque-real", gjson.GetBytes(nextUpstream.lastBody, "input.1.encrypted_content").String())
			require.Equal(t, "/responses", nextUpstream.lastReq.URL.Path)
			require.False(t, gjson.GetBytes(nextUpstream.lastBody, "messages").Exists())

			// Reasoning items are keyed by API key + opaque item id (digest
			// bound), so a new conversation id or a model alias must still
			// restore the exact text instead of dropping it.
			for _, tc := range []struct {
				name, session, model string
				keyID                int64
				restore              bool
			}{
				{"other-key", "native-thread", "deepseek-v4.1-flash", 22, false},
				{"other-conversation", "other-thread", "deepseek-v4.1-flash", 21, true},
				{"other-model", "native-thread", "deepseek-v4-pro", 21, true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					c, _ := deepSeekNativeReplayContext(tc.keyID, tc.session)
					request, err := sjson.SetBytes(replay, "model", tc.model)
					require.NoError(t, err)
					out := next.prepareDeepSeekNativeReasoningReplay(c, deepSeekNativeReplayAccount(), request)
					if !tc.restore {
						require.False(t, gjson.GetBytes(out, `input.#(type=="reasoning")`).Exists(),
							"another key's reasoning must be dropped, never forwarded empty")
						require.Equal(t, "read the tool result", gjson.GetBytes(out, "input.0.content").String())
						return
					}
					require.Equal(t, "Only the genuine upstream reasoning.", gjson.GetBytes(out, "input.1.content.0.text").String())
					require.Equal(t, "opaque-real", gjson.GetBytes(out, "input.1.encrypted_content").String())
				})
			}
		})
	}
}

func TestSanitizeDeepSeekNativeResponsesInputRewritesPreviousAssistantMessages(t *testing.T) {
	body := []byte(`{"model":"deepseek-flash","input":[
		{"type":"message","role":"assistant","content":[{"type":"output_text","text":"[Previous web search]\nStatus: completed"}]},
		{"type":"message","role":"assistant","content":"[Previous fetch]\nStatus: completed"},
		{"type":"message","role":"assistant","content":"normal answer"},
		{"type":"message","role":"user","content":"hi"}
	]}`)
	out := sanitizeDeepSeekNativeResponsesInput(body)
	require.Equal(t, "user", gjson.GetBytes(out, "input.0.role").String())
	require.Equal(t, "user", gjson.GetBytes(out, "input.1.role").String())
	require.Equal(t, "assistant", gjson.GetBytes(out, "input.2.role").String())
	require.Equal(t, "user", gjson.GetBytes(out, "input.3.role").String())
	require.Contains(t, gjson.GetBytes(out, "input.0.content.0.text").String(), "[Previous web search]")
	require.Equal(t, "input_text", gjson.GetBytes(out, "input.0.content.0.type").String(),
		"DeepSeek rejects output_text on user-role messages")
}

func TestSanitizeDeepSeekNativeResponsesInputLeavesNormalRequestsUntouched(t *testing.T) {
	body := []byte(`{"model":"deepseek-flash","input":[{"type":"message","role":"user","content":"hi"}]}`)
	require.Equal(t, body, sanitizeDeepSeekNativeResponsesInput(body))
}

func TestDeepSeekNativeReasoningReplayCollectsDeltaAndSnapshotAlias(t *testing.T) {
	cache := &reasoningRecordingCache{}
	svc := &OpenAIGatewayService{cache: cache}
	account := deepSeekNativeReplayAccount()
	c, _ := deepSeekNativeReplayContext(21, "delta-thread")
	svc.prepareDeepSeekNativeReasoningReplay(c, account, []byte(`{"model":"deepseek-v4.1-flash","input":"question"}`))
	recorder := svc.newDeepSeekNativeReasoningRecorder(c, account)
	for _, frame := range []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs_live","summary":[]}}`,
		`{"type":"response.reasoning_text.delta","output_index":0,"content_index":0,"item_id":"rs_live","delta":"think "}`,
		`{"type":"response.reasoning_text.delta","output_index":0,"content_index":0,"item_id":"rs_live","delta":"carefully"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs_live","summary":[]}}`,
		`{"type":"response.completed","response":{"output":[{"type":"reasoning","id":"rs_snapshot","summary":[]}]}}`,
	} {
		recorder.ObserveFrame(parseOpenAISSEDataFrame([]byte(frame), ""))
	}
	svc.cache = &reasoningRecordingCache{getResp: cache.snapshotSets()}
	for _, id := range []string{"rs_live", "rs_snapshot"} {
		request, err := sjson.SetBytes([]byte(`{"model":"deepseek-v4.1-flash","input":[{"type":"reasoning","summary":[]}]}`), "input.0.id", id)
		require.NoError(t, err)
		c, _ := deepSeekNativeReplayContext(21, "delta-thread")
		out := svc.prepareDeepSeekNativeReasoningReplay(c, account, request)
		require.Equal(t, "think carefully", gjson.GetBytes(out, "input.0.content.0.text").String(), id)
	}
}

func TestDeepSeekNativeReasoningReplayPreservesExplicitContentAndOpaqueBinding(t *testing.T) {
	cache := &reasoningRecordingCache{}
	svc := &OpenAIGatewayService{cache: cache}
	account := deepSeekNativeReplayAccount()
	c, _ := deepSeekNativeReplayContext(21, "binding-thread")
	svc.prepareDeepSeekNativeReasoningReplay(c, account, []byte(`{"model":"deepseek-v4.1-flash","input":"q"}`))
	svc.cacheDeepSeekNativeReasoningBody(c, account, []byte(`{"output":[{"type":"reasoning","id":"rs_1","encrypted_content":"real","content":[{"type":"reasoning_text","text":"original"}]}]}`))
	svc.cache = &reasoningRecordingCache{getResp: cache.snapshotSets()}
	explicit := `{"model":"deepseek-v4.1-flash","input":[{"type":"reasoning","id":"rs_1","content":[{"type":"reasoning_text","text":"client-provided"}]}]}`
	c, _ = deepSeekNativeReplayContext(21, "binding-thread")
	require.Equal(t, explicit, string(svc.prepareDeepSeekNativeReasoningReplay(c, account, []byte(explicit))))
	for _, request := range []string{
		`{"model":"deepseek-v4.1-flash","input":[{"type":"reasoning","id":"rs_1","encrypted_content":"different"}]}`,
		`{"model":"deepseek-v4.1-flash","input":[{"type":"reasoning","id":"rs_1"}]}`,
		`{"model":"deepseek-v4.1-flash","input":[{"type":"reasoning","id":"rs_unknown","summary":[{"type":"summary_text","text":"never fabricate full reasoning from this"}]}]}`,
	} {
		c, _ := deepSeekNativeReplayContext(21, "binding-thread")
		out := svc.prepareDeepSeekNativeReasoningReplay(c, account, []byte(request))
		require.False(t, gjson.GetBytes(out, `input.#(type=="reasoning")`).Exists(), request)
	}
	request := []byte(`{"model":"deepseek-v4.1-flash","input":[{"type":"reasoning","id":"rs_1"}]}`)
	for _, other := range []*Account{{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, {Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_protocol": APIProtocolChatCompletions}}} {
		c, _ := deepSeekNativeReplayContext(21, "binding-thread")
		require.Equal(t, request, svc.prepareDeepSeekNativeReasoningReplay(c, other, request))
	}
}

func TestDeepSeekNativeReasoningReplayReusedContextRechecksIdentity(t *testing.T) {
	cache := &reasoningRecordingCache{}
	svc := &OpenAIGatewayService{cache: cache}
	account := deepSeekNativeReplayAccount()
	c, _ := deepSeekNativeReplayContext(21, "initial-session")
	svc.prepareDeepSeekNativeReasoningReplay(c, account, []byte(`{"model":"deepseek-v4.1-flash","input":"question"}`))
	svc.cacheDeepSeekNativeReasoningBody(c, account, []byte(`{"output":[{"type":"reasoning","id":"rs_reused","content":[{"type":"reasoning_text","text":"this conversation only"}]}]}`))
	svc.cache = &reasoningRecordingCache{getResp: cache.snapshotSets()}
	request := []byte(`{"model":"deepseek-v4.1-flash","input":[{"type":"reasoning","id":"rs_reused"}]}`)
	require.Equal(t, "this conversation only", gjson.GetBytes(svc.prepareDeepSeekNativeReasoningReplay(c, account, request), "input.0.content.0.text").String())
	// The cache is keyed by API key + opaque reasoning item id, so session,
	// thread and model changes must still restore the exact text. Only a
	// different API key is denied.
	for _, tc := range []struct {
		name, session, model, thread string
		keyID                        int64
		restore                      bool
	}{
		{"changed-key", "initial-session", "deepseek-v4.1-flash", "", 22, false},
		{"changed-model", "initial-session", "deepseek-v4-pro", "", 21, true},
		{"changed-session", "different-session", "deepseek-v4.1-flash", "", 21, true},
		{"changed-thread", "initial-session", "deepseek-v4.1-flash", "different-thread", 21, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Reuse the exact gin context, as a WS connection does between turns.
			c.Set("api_key", &APIKey{ID: tc.keyID})
			c.Request.Header.Set("session-id", tc.session)
			c.Request.Header.Set("thread-id", tc.thread)
			body, err := sjson.SetBytes(request, "model", tc.model)
			require.NoError(t, err)
			out := svc.prepareDeepSeekNativeReasoningReplay(c, account, body)
			if !tc.restore {
				require.False(t, gjson.GetBytes(out, `input.#(type=="reasoning")`).Exists(),
					"a denied key must not receive another key's reasoning; drop it instead")
				return
			}
			require.Equal(t, "this conversation only", gjson.GetBytes(out, "input.0.content.0.text").String())
		})
	}
	c.Set("api_key", &APIKey{ID: 21})
	c.Request.Header.Set("session-id", "initial-session")
	c.Request.Header.Del("thread-id")
	require.Equal(t, "this conversation only", gjson.GetBytes(svc.prepareDeepSeekNativeReasoningReplay(c, account, request), "input.0.content.0.text").String())
	c.Set("api_key", &APIKey{ID: 22})
	require.Nil(t, svc.newDeepSeekNativeReasoningRecorder(c, account), "an identity change must not write through a prior scope even before Forward runs")
}

func TestDeepSeekNativeReasoningReplayHasTotalRestoreBudget(t *testing.T) {
	cache := &reasoningRecordingCache{}
	svc := &OpenAIGatewayService{cache: cache}
	account := deepSeekNativeReplayAccount()
	c, _ := deepSeekNativeReplayContext(21, "large-history")
	svc.prepareDeepSeekNativeReasoningReplay(c, account, []byte(`{"model":"deepseek-v4.1-flash","input":"question"}`))
	content, err := json.Marshal([]map[string]string{{"type": "reasoning_text", "text": strings.Repeat("r", 512<<10)}})
	require.NoError(t, err)
	entry, err := json.Marshal(deepSeekNativeReasoningEntry{Content: content})
	require.NoError(t, err)
	entryText := string(entry)
	getResp := make(map[string]string)
	items := []map[string]any{{"role": "user", "content": "question"}}
	for i := 0; i < deepSeekNativeReasoningMaxItems; i++ {
		id := fmt.Sprintf("rs_large_%d", i)
		items = append(items, map[string]any{"type": "reasoning", "id": id, "summary": []any{}})
		getResp[deepSeekNativeReasoningCacheKey(21, id)] = entryText
	}
	request, err := json.Marshal(map[string]any{"model": "deepseek-v4.1-flash", "input": items, "opaque": json.Number("9007199254740993")})
	require.NoError(t, err)
	svc.cache = &reasoningRecordingCache{getResp: getResp}
	out := svc.prepareDeepSeekNativeReasoningReplay(c, account, request)
	restored, restoredBytes := 0, 0
	for _, item := range gjson.GetBytes(out, "input").Array() {
		if content := item.Get("content"); item.Get("type").String() == "reasoning" && content.IsArray() {
			restored++
			restoredBytes += len(content.Raw)
		}
	}
	require.Positive(t, restored)
	require.Less(t, restored, deepSeekNativeReasoningMaxItems)
	require.LessOrEqual(t, restoredBytes, deepSeekNativeReasoningMaxBytes)
	require.LessOrEqual(t, len(out)-len(request), deepSeekNativeReasoningMaxBytes)
	require.Equal(t, "9007199254740993", gjson.GetBytes(out, "opaque").Raw)
}

func TestDeepSeekNativeReasoningReplayRequiresConversationAnchor(t *testing.T) {
	cache := &reasoningRecordingCache{}
	svc := &OpenAIGatewayService{cache: cache}
	account := deepSeekNativeReplayAccount()
	c, _ := deepSeekNativeReplayContext(21, "")
	for _, request := range []string{
		`{"model":"deepseek-v4.1-flash"}`,
		`{"model":"deepseek-v4.1-flash","input":[]}`,
		`{"model":"deepseek-v4.1-flash","input":[{"type":"reasoning","id":"rs_1"}]}`,
		`{"model":"deepseek-v4.1-flash","input":[{"role":"user","content":"   "},{"type":"reasoning","id":"rs_1"}]}`,
	} {
		// Even a prior anchored turn on this context cannot authorize an
		// anonymous model-only turn to access its cache namespace.
		svc.prepareDeepSeekNativeReasoningReplay(c, account, []byte(`{"model":"deepseek-v4.1-flash","input":"real question"}`))
		require.Equal(t, request, string(svc.prepareDeepSeekNativeReasoningReplay(c, account, []byte(request))))
		require.Nil(t, svc.newDeepSeekNativeReasoningRecorder(c, account))
	}
}

func TestDeepSeekNativeReasoningReplayOpaqueCannotBeAddedToPlainItem(t *testing.T) {
	cache := &reasoningRecordingCache{}
	svc := &OpenAIGatewayService{cache: cache}
	account := deepSeekNativeReplayAccount()
	c, _ := deepSeekNativeReplayContext(21, "plain-thread")
	svc.prepareDeepSeekNativeReasoningReplay(c, account, []byte(`{"model":"deepseek-v4.1-flash","input":"q"}`))
	svc.cacheDeepSeekNativeReasoningBody(c, account, []byte(`{"output":[{"type":"reasoning","id":"rs_plain","content":[{"type":"reasoning_text","text":"real"}]}]}`))
	svc.cache = &reasoningRecordingCache{getResp: cache.snapshotSets()}
	request := []byte(`{"model":"deepseek-v4.1-flash","input":[{"type":"reasoning","id":"rs_plain","encrypted_content":"invented"}]}`)
	out := svc.prepareDeepSeekNativeReasoningReplay(c, account, request)
	require.False(t, gjson.GetBytes(out, `input.#(type=="reasoning")`).Exists(),
		"foreign opaque reasoning must be dropped, never filled with another item's content")
}

func TestDeepSeekNativeReasoningReplayHostedContinuationKeepsAnchoredScope(t *testing.T) {
	svc := &OpenAIGatewayService{cache: &reasoningRecordingCache{}}
	account := deepSeekNativeReplayAccount()
	c, _ := deepSeekNativeReplayContext(21, "")
	svc.prepareDeepSeekNativeReasoningReplay(c, account, []byte(`{"model":"deepseek-v4.1-flash","input":"question","tools":[{"type":"web_search"}]}`))
	initial, _ := c.Get(deepSeekNativeReasoningScopeKey)
	require.NotNil(t, initial)
	shadow := c.Copy()
	shadow.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), deepSeekHostedSearchSkipContextKey{}, true))
	request := []byte(`{"model":"deepseek-v4.1-flash","input":[{"role":"user","content":"question"}],"tools":[{"type":"function","name":"web_search"}]}`)
	svc.prepareDeepSeekNativeReasoningReplay(shadow, account, request)
	continued, _ := shadow.Get(deepSeekNativeReasoningScopeKey)
	require.Equal(t, initial, continued, "trusted tool declaration rewriting must not orphan the original anonymous turn")
	shadow.Set("api_key", &APIKey{ID: 22})
	svc.prepareDeepSeekNativeReasoningReplay(shadow, account, request)
	changed, _ := shadow.Get(deepSeekNativeReasoningScopeKey)
	require.NotEqual(t, initial, changed, "even internal recursion must recheck its authenticated key")
}

type failingNativeReasoningCache struct {
	stubGatewayCache
	writes int
}

func (c *failingNativeReasoningCache) SetReasoningContent(context.Context, string, string, time.Duration) error {
	c.writes++
	return errors.New("cache unavailable")
}

func TestDeepSeekNativeReasoningReplayCacheFailureStopsFurtherWrites(t *testing.T) {
	cache := &failingNativeReasoningCache{}
	svc := &OpenAIGatewayService{cache: cache}
	account := deepSeekNativeReplayAccount()
	c, _ := deepSeekNativeReplayContext(21, "failure-thread")
	svc.prepareDeepSeekNativeReasoningReplay(c, account, []byte(`{"model":"deepseek-v4.1-flash","input":"question"}`))
	recorder := svc.newDeepSeekNativeReasoningRecorder(c, account)
	for _, frame := range []string{
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs_1","content":[{"type":"reasoning_text","text":"first"}]}}`,
		`{"type":"response.output_item.done","output_index":1,"item":{"type":"reasoning","id":"rs_2","content":[{"type":"reasoning_text","text":"second"}]}}`,
		`{"type":"response.completed","response":{"output":[{"type":"reasoning","id":"rs_3","content":[{"type":"reasoning_text","text":"third"}]}]}}`,
	} {
		recorder.ObserveFrame(parseOpenAISSEDataFrame([]byte(frame), ""))
	}
	require.Equal(t, 1, cache.writes, "one Redis failure must not be retried for each SSE frame")
	require.True(t, recorder.cacheFailed)

	// Successful prior writes also consume a single total request budget.
	budgetRecorder := svc.newDeepSeekNativeReasoningRecorder(c, account)
	budgetRecorder.cacheWriteTime = 2 * time.Second
	budgetRecorder.ObserveFrame(parseOpenAISSEDataFrame([]byte(`{"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs_budget","content":[{"type":"reasoning_text","text":"actual"}]}}`), ""))
	require.Equal(t, 1, cache.writes)
	require.True(t, budgetRecorder.cacheFailed)
}

// TestDeepSeekNativeReasoningReplayKeepsEveryTurnByteForByte proves that a
// multi-turn replay forwards every user input, prompt, tool call, tool result
// and model output unchanged, and only fills the missing reasoning text with
// the exact bytes that were captured from the upstream response.
func TestDeepSeekNativeReasoningReplayKeepsEveryTurnByteForByte(t *testing.T) {
	cache := &reasoningRecordingCache{}
	svc := &OpenAIGatewayService{cache: cache}
	account := deepSeekNativeReplayAccount()
	c, _ := deepSeekNativeReplayContext(21, "lossless-thread")
	svc.prepareDeepSeekNativeReasoningReplay(c, account, []byte(`{"model":"deepseek-v4.1-flash","input":"anchor"}`))
	firstThinking := "第一轮思考：保留 \"引号\"、反斜杠 \\ 与 emoji 🚀"
	secondThinking := "second turn thinking, keep 100% bytes"
	svc.cacheDeepSeekNativeReasoningBody(c, account, []byte(`{"output":[`+
		`{"type":"reasoning","id":"rs_t1","encrypted_content":"opaque-1","content":[{"type":"reasoning_text","text":"第一轮思考：保留 \"引号\"、反斜杠 \\ 与 emoji 🚀"}]},`+
		`{"type":"reasoning","id":"rs_t2","encrypted_content":"opaque-2","content":[{"type":"reasoning_text","text":"second turn thinking, keep 100% bytes"}]}]}`))
	svc.cache = &reasoningRecordingCache{getResp: cache.snapshotSets()}

	replay := []byte(`{
		"model":"deepseek-v4.1-flash",
		"instructions":"系统提示：不要丢任何字段。",
		"input":[
			{"role":"user","content":[{"type":"input_text","text":"第一问：😀 用户输入必须原样"}],"opaque_big":9007199254740993},
			{"type":"reasoning","id":"rs_t1","summary":[{"type":"summary_text","text":"SUMMARY-KEEP"}],"encrypted_content":"opaque-1"},
			{"type":"function_call","id":"fc_1","call_id":"call_1","name":"lookup","arguments":"{\"q\":\"中文 & <tag>\"}","status":"completed"},
			{"type":"function_call_output","call_id":"call_1","output":"工具结果：原样保留 ✅"},
			{"role":"assistant","content":[{"type":"output_text","text":"模型输出：保留\n换行与 emoji 🧠"}]},
			{"role":"user","content":[{"type":"input_text","text":"第二问：继续"},{"type":"input_image","image_url":"data:image/png;base64,AAAA"}]},
			{"type":"reasoning","id":"rs_t2","summary":[],"encrypted_content":"opaque-2"}
		],
		"tools":[{"type":"function","name":"lookup","parameters":{"type":"object","properties":{"q":{"type":"string"}}}}],
		"metadata":{"trace":"keep-me"}
	}`)

	out := svc.prepareDeepSeekNativeReasoningReplay(c, account, replay)
	require.Equal(t, firstThinking, gjson.GetBytes(out, "input.1.content.0.text").String())
	require.Equal(t, secondThinking, gjson.GetBytes(out, "input.6.content.0.text").String())

	var before, after map[string]any
	require.NoError(t, json.Unmarshal(replay, &before))
	require.NoError(t, json.Unmarshal(out, &after))
	strip := func(doc map[string]any) {
		for _, item := range doc["input"].([]any) {
			entry := item.(map[string]any)
			if entry["type"] == "reasoning" {
				delete(entry, "content")
			}
		}
	}
	strip(before)
	strip(after)
	// A model assistant message after a completed tool output starts a new
	// thinking turn. It must be demoted when that turn has no reasoning_text;
	// tool calls and results themselves remain lossless.
	expectedAssistant := after["input"].([]any)[4].(map[string]any)
	expectedAssistant["role"] = "user"
	expectedAssistant["content"].([]any)[0].(map[string]any)["type"] = "input_text"
	before["input"].([]any)[4].(map[string]any)["role"] = "user"
	before["input"].([]any)[4].(map[string]any)["content"].([]any)[0].(map[string]any)["type"] = "input_text"
	require.Equal(t, before, after, "only missing reasoning and the unsafe post-tool assistant turn may change")

	require.Len(t, after["input"].([]any), 7)
	require.Equal(t, "第一问：😀 用户输入必须原样", gjson.GetBytes(out, "input.0.content.0.text").String())
	require.Equal(t, "工具结果：原样保留 ✅", gjson.GetBytes(out, "input.3.output").String())
	require.Equal(t, "模型输出：保留\n换行与 emoji 🧠", gjson.GetBytes(out, "input.4.content.0.text").String())
	require.Equal(t, `{"q":"中文 & <tag>"}`, gjson.GetBytes(out, "input.2.arguments").String())
	require.Equal(t, "keep-me", gjson.GetBytes(out, "metadata.trace").String())
}

func TestDeepSeekNativeReasoningReplayDropsUnrecoverableHistory(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &OpenAIGatewayService{cache: &reasoningRecordingCache{}}
	c, _ := deepSeekNativeReplayContext(21, "thread-drop")
	body := []byte(`{"model":"deepseek-v4.1-flash","reasoning":{"effort":"none"},"input":[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},
		{"type":"reasoning","id":"rs_missing","summary":[]},
		{"type":"message","role":"assistant","content":[{"type":"output_text","text":"old answer"}]},
		{"type":"message","role":"user","content":[{"type":"input_text","text":"next"}]}
	]}`)

	out := svc.prepareDeepSeekNativeReasoningReplay(c, deepSeekNativeReplayAccount(), body)

	require.False(t, gjson.GetBytes(out, `input.#(type=="reasoning")`).Exists(), "unrecoverable reasoning must be dropped")
	require.Equal(t, "user", gjson.GetBytes(out, "input.1.role").String(), "assistant without reasoning becomes user context")
	require.Equal(t, "old answer", gjson.GetBytes(out, "input.1.content.0.text").String())
	require.Len(t, gjson.GetBytes(out, "input").Array(), 3)
}

func TestDeepSeekNativeReasoningReplayDropsReasoningWithoutID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &OpenAIGatewayService{cache: &reasoningRecordingCache{}}
	c, _ := deepSeekNativeReplayContext(21, "thread-drop-no-id")
	body := []byte(`{"model":"deepseek-v4.1-flash","input":[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},
		{"type":"reasoning","summary":[]},
		{"type":"message","role":"assistant","content":[{"type":"output_text","text":"old answer"}]},
		{"type":"message","role":"user","content":[{"type":"input_text","text":"next"}]}
	]}`)

	out := svc.prepareDeepSeekNativeReasoningReplay(c, deepSeekNativeReplayAccount(), body)

	require.False(t, gjson.GetBytes(out, `input.#(type=="reasoning")`).Exists())
	require.Equal(t, "user", gjson.GetBytes(out, "input.1.role").String())
}

func TestDeepSeekNativeReasoningReplayLeavesPlainConversationAlone(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &OpenAIGatewayService{cache: &reasoningRecordingCache{}}
	c, _ := deepSeekNativeReplayContext(21, "thread-plain")
	body := []byte(`{"model":"deepseek-v4.1-flash","reasoning":{"effort":"none"},"input":[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},
		{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]},
		{"type":"message","role":"user","content":[{"type":"input_text","text":"next"}]}
	]}`)

	out := svc.prepareDeepSeekNativeReasoningReplay(c, deepSeekNativeReplayAccount(), body)

	require.Equal(t, string(body), string(out), "requests without reasoning items keep their original roles")
}

func TestDeepSeekNativeReasoningReplayDemotesAssistantHistoryInThinkingMode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &OpenAIGatewayService{cache: &reasoningRecordingCache{}}
	c, _ := deepSeekNativeReplayContext(21, "thread-thinking")
	body := []byte(`{"model":"deepseek-v4.1-flash","reasoning":{"effort":"max"},"input":[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},
		{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer without reasoning"}]},
		{"type":"message","role":"user","content":[{"type":"input_text","text":"next"}]}
	]}`)

	out := svc.prepareDeepSeekNativeReasoningReplay(c, deepSeekNativeReplayAccount(), body)

	require.Equal(t, "user", gjson.GetBytes(out, "input.1.role").String(),
		"thinking mode requires reasoning_text on assistant history; demote instead of 400")
	require.Equal(t, "answer without reasoning", gjson.GetBytes(out, "input.1.content.0.text").String())
	require.Equal(t, "input_text", gjson.GetBytes(out, "input.1.content.0.type").String(),
		"DeepSeek rejects output_text on user-role messages")
}
