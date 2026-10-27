//go:build unit

package service

import (
	"bytes"
	"context"
	"errors"
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

			for _, tc := range []struct {
				name, session, model string
				keyID                int64
			}{
				{"other-key", "native-thread", "deepseek-v4.1-flash", 22},
				{"other-conversation", "other-thread", "deepseek-v4.1-flash", 21},
				{"other-model", "native-thread", "deepseek-v4-pro", 21},
			} {
				t.Run(tc.name, func(t *testing.T) {
					c, _ := deepSeekNativeReplayContext(tc.keyID, tc.session)
					request, err := sjson.SetBytes(replay, "model", tc.model)
					require.NoError(t, err)
					require.Equal(t, request, next.prepareDeepSeekNativeReasoningReplay(c, deepSeekNativeReplayAccount(), request))
				})
			}
		})
	}
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
	for _, request := range []string{
		`{"model":"deepseek-v4.1-flash","input":[{"type":"reasoning","id":"rs_1","content":[{"type":"reasoning_text","text":"client-provided"}]}]}`,
		`{"model":"deepseek-v4.1-flash","input":[{"type":"reasoning","id":"rs_1","encrypted_content":"different"}]}`,
		`{"model":"deepseek-v4.1-flash","input":[{"type":"reasoning","id":"rs_unknown","summary":[{"type":"summary_text","text":"never fabricate full reasoning from this"}]}]}`,
	} {
		c, _ := deepSeekNativeReplayContext(21, "binding-thread")
		require.Equal(t, request, string(svc.prepareDeepSeekNativeReasoningReplay(c, account, []byte(request))))
	}
	request := []byte(`{"model":"deepseek-v4.1-flash","input":[{"type":"reasoning","id":"rs_1"}]}`)
	for _, other := range []*Account{{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, {Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_protocol": APIProtocolChatCompletions}}} {
		c, _ := deepSeekNativeReplayContext(21, "binding-thread")
		require.Equal(t, request, svc.prepareDeepSeekNativeReasoningReplay(c, other, request))
	}
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
