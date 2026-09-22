package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResponsesToChatCompletionsRequest_LocalShellBecomesFunctionTool(t *testing.T) {
	req := &ResponsesRequest{
		Model: "gpt-5.4",
		Input: json.RawMessage(`"ls"`),
		Tools: []ResponsesTool{{Type: "local_shell"}},
	}

	out, err := ResponsesToChatCompletionsRequest(req)
	require.NoError(t, err)
	require.Len(t, out.Tools, 1)
	assert.Equal(t, "function", out.Tools[0].Type)
	assert.Equal(t, "local_shell", out.Tools[0].Function.Name)
	assert.JSONEq(t, localShellToolParameters, string(out.Tools[0].Function.Parameters))
}

func TestResponsesInputToChatMessages_LocalShellCallHistory(t *testing.T) {
	req := &ResponsesRequest{
		Model: "gpt-5.4",
		Input: json.RawMessage(`[
			{"role":"user","content":"list files"},
			{"type":"local_shell_call","call_id":"call_sh","action":{"type":"exec","command":["ls","-la"]}},
			{"type":"local_shell_call_output","call_id":"call_sh","output":"ok"}
		]`),
		Tools: []ResponsesTool{{Type: "local_shell"}},
	}

	out, err := ResponsesToChatCompletionsRequest(req)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(out.Messages), 3)
	assert.Equal(t, "assistant", out.Messages[1].Role)
	require.Len(t, out.Messages[1].ToolCalls, 1)
	assert.Equal(t, "call_sh", out.Messages[1].ToolCalls[0].ID)
	assert.Equal(t, "local_shell", out.Messages[1].ToolCalls[0].Function.Name)
	assert.Contains(t, out.Messages[1].ToolCalls[0].Function.Arguments, `"ls"`)
	assert.Equal(t, "tool", out.Messages[2].Role)
	assert.Equal(t, "call_sh", out.Messages[2].ToolCallID)
}

func TestChatCompletionsResponseToResponses_LocalShellCallOutputItem(t *testing.T) {
	resp := &ChatCompletionsResponse{
		ID: "cc-1",
		Choices: []ChatChoice{{
			Message: ChatMessage{
				Role: "assistant",
				ToolCalls: []ChatToolCall{
					{ID: "call_sh", Function: ChatFunctionCall{Name: "local_shell", Arguments: `{"command":["pwd"]}`}},
				},
			},
		}},
	}

	out := ChatCompletionsResponseToResponsesWithLocalShell(resp, "gpt-5.4", nil, nil, false, nil, map[string]bool{"local_shell": true})
	require.Len(t, out.Output, 1)
	assert.Equal(t, "local_shell_call", out.Output[0].Type)
	assert.Equal(t, "call_sh", out.Output[0].CallID)

	b, err := json.Marshal(out.Output[0])
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	action, ok := m["action"].(map[string]any)
	require.True(t, ok, "action 必须序列化为对象")
	assert.Equal(t, "exec", action["type"])
	_, hasArguments := m["arguments"]
	assert.False(t, hasArguments, "线上形态不能再带 arguments 字符串")
}

func TestChatCompletionsResponseToResponses_LocalShellNotDeclaredKeepsFunctionCall(t *testing.T) {
	resp := &ChatCompletionsResponse{
		Choices: []ChatChoice{{
			Message: ChatMessage{
				ToolCalls: []ChatToolCall{
					{ID: "call_sh", Function: ChatFunctionCall{Name: "local_shell", Arguments: `{"command":["pwd"]}`}},
				},
			},
		}},
	}

	out := ChatCompletionsResponseToResponses(resp, "gpt-5.4", nil, nil, false, nil)
	require.Len(t, out.Output, 1)
	assert.Equal(t, "function_call", out.Output[0].Type)
}

func TestChatCompletionsChunkToResponsesEvents_LocalShellCallStream(t *testing.T) {
	state := NewChatCompletionsToResponsesStreamState("gpt-5.4")
	state.LocalShellTools = map[string]bool{"local_shell": true}

	idx := 0
	chunk := &ChatCompletionsChunk{
		ID: "cc-1",
		Choices: []ChatChunkChoice{{
			Delta: ChatDelta{
				ToolCalls: []ChatToolCall{{
					Index:    &idx,
					ID:       "call_sh",
					Function: ChatFunctionCall{Name: "local_shell", Arguments: `{"command":["ls"]}`},
				}},
			},
		}},
	}

	events := ChatCompletionsChunkToResponsesEvents(chunk, state)
	events = append(events, FinalizeChatCompletionsResponsesStream(state)...)

	var added, itemDone *ResponsesStreamEvent
	for i := range events {
		evt := &events[i]
		switch evt.Type {
		case "response.output_item.added":
			if evt.Item != nil && evt.Item.Type != "message" && evt.Item.Type != "reasoning" {
				added = evt
			}
		case "response.output_item.done":
			if evt.Item != nil && evt.Item.Type == "local_shell_call" {
				itemDone = evt
			}
		case "response.function_call_arguments.delta", "response.function_call_arguments.done",
			"response.custom_tool_call_input.delta", "response.custom_tool_call_input.done":
			t.Fatalf("local_shell 调用不应产出 %s", evt.Type)
		}
	}

	require.NotNil(t, added, "缺少 local_shell_call 的 output_item.added")
	assert.Equal(t, "local_shell_call", added.Item.Type)
	require.NotNil(t, itemDone, "缺少 local_shell_call 的 output_item.done")
	assert.Equal(t, "call_sh", itemDone.Item.CallID)

	sse, err := ResponsesEventToSSE(*itemDone)
	require.NoError(t, err)
	assert.Contains(t, sse, `"type":"local_shell_call"`)
	assert.Contains(t, sse, `"action"`)
	assert.Contains(t, sse, `"command"`)
	assert.NotContains(t, sse, `"arguments"`)

	final := events[len(events)-1]
	require.Equal(t, "response.completed", final.Type)
	require.NotNil(t, final.Response)
	found := false
	for _, item := range final.Response.Output {
		if item.Type == "local_shell_call" {
			found = true
			assert.Equal(t, "call_sh", item.CallID)
		}
	}
	assert.True(t, found, "response.completed 缺少 local_shell_call 输出项")
}
