//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestDeepSeekNativePreservesResponsesFields(t *testing.T) {
	account := deepSeekNativeReplayAccount()
	body := []byte(`{"model":"deepseek-flash","store":true,"previous_response_id":"resp_opaque","reasoning":{"effort":"high","summary":"auto"},"text":{"verbosity":"high"},"input":[{"role":"user","content":"hi"}]}`)
	upstream := &httpUpstreamRecorder{err: errors.New("stop after wire capture")}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
	c, _ := deepSeekNativeReplayContext(21, "field-preservation")
	_, err := svc.Forward(context.Background(), c, account, body)
	require.Error(t, err)
	require.NotNil(t, upstream.lastReq)
	for _, path := range []string{"model", "store", "previous_response_id", "reasoning", "text", "input"} {
		require.JSONEq(t, gjson.GetBytes(body, path).Raw, gjson.GetBytes(upstream.lastBody, path).Raw, path)
	}
}

func TestDeepSeekNativePreservesAnthropicSearchHistory(t *testing.T) {
	account := deepSeekNativeReplayAccount()
	body := []byte(`{"model":"deepseek-flash","max_tokens":128,"thinking":{"type":"disabled"},"messages":[{"role":"assistant","content":[{"type":"server_tool_use","id":"srv_opaque","name":"web_search","input":{"query":"synthetic"}},{"type":"web_search_tool_result","tool_use_id":"srv_opaque","content":[]},{"type":"text","text":""}]},{"role":"user","content":"next"}]}`)
	upstream := &httpUpstreamRecorder{err: errors.New("stop after wire capture")}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
	c, _ := deepSeekNativeReplayContext(21, "native-anthropic-history")
	_, err := svc.ForwardAsAnthropic(context.Background(), c, account, body, "", "")
	require.Error(t, err)
	require.NotNil(t, upstream.lastReq)
	require.JSONEq(t, gjson.GetBytes(body, "messages").Raw, gjson.GetBytes(upstream.lastBody, "messages").Raw, "native upstream validates its own blocks; never drop replayed results")
}

func TestDeepSeekNativeDoesNotRetryStrippedRejectedFields(t *testing.T) {
	body := []byte(`{"model":"deepseek-flash","reasoning":{"effort":"high","summary":"auto"},"input":"hi"}`)
	rejection := []byte(`{"error":{"type":"invalid_request_error","code":"unknown_parameter","message":"Unknown parameter: reasoning.summary","param":"reasoning.summary"}}`)
	next, _, changed, err := normalizeOpenAIResponsesRejectedFieldRetryBodyForAccount(deepSeekNativeReplayAccount(), 400, body, rejection)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, body, next)
}
