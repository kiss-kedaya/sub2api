package service

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestSanitizeOpenAIResponsesOrphanToolOutputs(t *testing.T) {
	t.Run("preserves matches regardless of item order", func(t *testing.T) {
		input := []any{
			map[string]any{"type": "tool_search_output", "call_id": "search_1", "output": "first"},
			map[string]any{"type": "tool_search_call", "id": "search_1", "query": "docs"},
			map[string]any{"type": "custom_tool_call_output", "call_id": "custom_1", "output": "second"},
			map[string]any{"type": "item_reference", "id": "custom_1"},
		}
		reqBody := map[string]any{"input": input}

		require.False(t, sanitizeOpenAIResponsesOrphanToolOutputs(reqBody, input, false))
		require.Equal(t, input, reqBody["input"])
	})

	t.Run("outputs do not legitimize each other", func(t *testing.T) {
		input := []any{
			map[string]any{"type": "function_call_output", "call_id": "missing", "output": "one"},
			map[string]any{"type": "tool_search_output", "call_id": "missing", "output": "two"},
			map[string]any{"type": "custom_tool_call_output", "call_id": "missing", "output": "three"},
			map[string]any{"type": "mcp_tool_call_output", "call_id": "missing", "output": "four"},
		}
		reqBody := map[string]any{"input": input}

		require.True(t, sanitizeOpenAIResponsesOrphanToolOutputs(reqBody, input, false))
		got := reqBody["input"].([]any)
		require.Empty(t, got)
	})

	t.Run("preserves all output variants with matching calls", func(t *testing.T) {
		pairs := []struct {
			callType   string
			outputType string
		}{
			{callType: "function_call", outputType: "function_call_output"},
			{callType: "tool_search_call", outputType: "tool_search_output"},
			{callType: "custom_tool_call", outputType: "custom_tool_call_output"},
			{callType: "mcp_tool_call", outputType: "mcp_tool_call_output"},
		}
		input := make([]any, 0, len(pairs)*2)
		for index, pair := range pairs {
			callID := string(rune('a' + index))
			input = append(input,
				map[string]any{"type": pair.callType, "call_id": callID},
				map[string]any{"type": pair.outputType, "call_id": callID, "output": "ok"},
			)
		}
		reqBody := map[string]any{"input": input}

		require.False(t, sanitizeOpenAIResponsesOrphanToolOutputs(reqBody, input, false))
	})

	t.Run("previous response may contain the missing call", func(t *testing.T) {
		input := []any{map[string]any{"type": "function_call_output", "call_id": "remote", "output": "ok"}}
		reqBody := map[string]any{"input": input, "previous_response_id": "resp_1"}

		require.False(t, sanitizeOpenAIResponsesOrphanToolOutputs(reqBody, input, true))
		require.Equal(t, input, reqBody["input"])
	})
}

func TestTruncateOpenAIResponsesInputText(t *testing.T) {
	oversized := strings.Repeat("a", openAIResponsesInputTextMaxChars+1)
	input := []any{
		map[string]any{"type": "function_call_output", "call_id": "a", "output": oversized},
		map[string]any{"type": "tool_search_output", "call_id": "b", "output": oversized},
		map[string]any{"type": "custom_tool_call_output", "call_id": "c", "output": oversized},
		map[string]any{"type": "mcp_tool_call_output", "call_id": "d", "output": oversized},
		map[string]any{
			"role": "user",
			"content": []any{
				map[string]any{"type": "input_text", "text": "short"},
				map[string]any{"type": "input_text", "text": oversized},
			},
		},
	}
	reqBody := map[string]any{"input": input}

	require.True(t, truncateOpenAIResponsesInputText(reqBody))
	for _, rawItem := range input[:4] {
		item := rawItem.(map[string]any)
		require.Len(t, item["output"].(string), openAIResponsesInputTextMaxChars)
	}
	content := input[4].(map[string]any)["content"].([]any)
	require.Equal(t, "short", content[0].(map[string]any)["text"])
	require.Len(t, content[1].(map[string]any)["text"].(string), openAIResponsesInputTextMaxChars)
}

func TestTruncateOpenAIResponsesInputStringPreservesUTF8Boundary(t *testing.T) {
	value := strings.Repeat("a", openAIResponsesInputTextMaxChars-1) + "中中"

	got, changed := truncateOpenAIResponsesInputString(value)

	require.True(t, changed)
	require.True(t, utf8.ValidString(got))
	require.Equal(t, openAIResponsesInputTextMaxChars, utf8.RuneCountInString(got))
	require.Equal(t, "中", got[len(got)-len("中"):])
}

func TestOpenAIResponsesInputMayNeedTruncation(t *testing.T) {
	short := []byte(`{"input":[{"type":"function_call_output","output":"ok"}]}`)
	largeUnrelated := []byte(`{"input":"` + strings.Repeat("x", openAIResponsesInputTextMaxChars+1) + `"}`)
	largeOutput := []byte(`{"input":[{"type":"function_call_output","output":"` + strings.Repeat("x", openAIResponsesInputTextMaxChars+1) + `"}]}`)

	require.False(t, openAIResponsesInputMayNeedTruncation(short))
	require.False(t, openAIResponsesInputMayNeedTruncation(largeUnrelated))
	require.True(t, openAIResponsesInputMayNeedTruncation(largeOutput))
}

func TestOpenAIGatewayService_OAuthDropsOrphanAfterDroppingPreviousResponse(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","stream":false,"previous_response_id":"resp_missing","input":[{"type":"function_call_output","call_id":"call_missing","output":"keep this result"}]}`)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		newOpenAIRejectedFieldTestResponse(http.StatusOK, `{"id":"resp_ok","output":[],"usage":{"input_tokens":1,"output_tokens":1,"input_tokens_details":{"cached_tokens":0}}}`),
	}}

	result, err := newOpenAIRejectedFieldTestService(upstream).Forward(
		context.Background(),
		newOpenAIRejectedFieldTestContext(body),
		newOpenAIOAuthNamespaceTestAccount(),
		body,
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.bodies, 1)
	require.False(t, gjson.GetBytes(upstream.bodies[0], "previous_response_id").Exists())
	require.Empty(t, gjson.GetBytes(upstream.bodies[0], "input").Array())
}

func TestOpenAIGatewayService_TruncatesOversizedToolOutputBeforeForward(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","stream":false,"input":[{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":"` + strings.Repeat("x", openAIResponsesInputTextMaxChars+1) + `"}]}`)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		newOpenAIRejectedFieldTestResponse(http.StatusOK, `{"id":"resp_ok","output":[],"usage":{"input_tokens":1,"output_tokens":1,"input_tokens_details":{"cached_tokens":0}}}`),
	}}

	result, err := newOpenAIRejectedFieldTestService(upstream).Forward(
		context.Background(),
		newOpenAIRejectedFieldTestContext(body),
		newOpenAIRejectedFieldTestAccount(),
		body,
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.bodies, 1)
	require.Equal(t, openAIResponsesInputTextMaxChars, len(gjson.GetBytes(upstream.bodies[0], "input.1.output").String()))
}
