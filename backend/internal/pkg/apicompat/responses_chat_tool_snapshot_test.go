package apicompat

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResponsesChatToolSnapshots(t *testing.T) {
	item := ResponsesOutput{Type: "function_call", ID: "fc_terminal", CallID: "call_terminal", Name: "exec_command", Arguments: `{"cmd":"pwd"}`, Status: "completed"}
	for _, variant := range []string{"item_done", "terminal_only", "partial_delta", "full_added", "complete_deltas", "compacted_terminal"} {
		t.Run(variant, func(t *testing.T) {
			state := NewResponsesEventToChatState()
			var events []ResponsesStreamEvent
			if variant == "partial_delta" || variant == "complete_deltas" {
				added := item
				added.Arguments = ""
				events = append(events, ResponsesStreamEvent{Type: "response.output_item.added", OutputIndex: 1, Item: &added})
				args := `{"cmd":`
				if variant == "complete_deltas" {
					args = item.Arguments
				}
				events = append(events, ResponsesStreamEvent{Type: "response.function_call_arguments.delta", OutputIndex: 1, Delta: args})
			}
			if variant == "full_added" {
				events = append(events, ResponsesStreamEvent{Type: "response.output_item.added", OutputIndex: 1, Item: &item})
			}
			if variant != "terminal_only" {
				events = append(events, ResponsesStreamEvent{Type: "response.output_item.done", OutputIndex: 1, Item: &item})
			}
			terminal := ResponsesStreamEvent{Type: "response.completed", Response: &ResponsesResponse{Status: "completed", Output: []ResponsesOutput{{Type: "reasoning"}, item}}}
			if variant == "item_done" {
				terminal.Response.Output = nil
			}
			if variant == "compacted_terminal" {
				terminal.Response.Output = []ResponsesOutput{item}
			}
			events = append(events, terminal)
			var name, id, arguments string
			finishes := 0
			for i := range events {
				for _, chunk := range ResponsesEventToChatChunks(&events[i], state) {
					for _, choice := range chunk.Choices {
						for _, call := range choice.Delta.ToolCalls {
							require.NotNil(t, call.Index)
							require.Equal(t, 0, *call.Index)
							name += call.Function.Name
							id += call.ID
							arguments += call.Function.Arguments
						}
						if choice.FinishReason != nil {
							require.Equal(t, "tool_calls", *choice.FinishReason)
							finishes++
						}
					}
				}
			}
			require.Equal(t, "exec_command", name)
			require.Equal(t, "call_terminal", id)
			require.Equal(t, item.Arguments, arguments)
			require.Equal(t, 1, finishes)
			require.Empty(t, ResponsesEventToChatChunks(&terminal, state), "terminal replay must not execute a tool twice")
		})
	}
}

func TestBufferedResponsesChatToolSnapshot(t *testing.T) {
	acc := NewBufferedResponseAccumulator()
	item := ResponsesOutput{Type: "function_call", CallID: "call_terminal", Name: "exec_command", Arguments: `{"cmd":"pwd"}`}
	acc.ProcessEvent(&ResponsesStreamEvent{Type: "response.output_item.done", OutputIndex: 2, Item: &item})
	acc.ProcessEvent(&ResponsesStreamEvent{Type: "response.output_item.done", OutputIndex: 2, Item: &item})
	response := &ResponsesResponse{Status: "completed"}
	acc.SupplementResponseOutput(response)
	chat := ResponsesToChatCompletions(response, "gpt-5.6-sol")
	require.Len(t, chat.Choices[0].Message.ToolCalls, 1)
	call := chat.Choices[0].Message.ToolCalls[0]
	require.Equal(t, "call_terminal", call.ID)
	require.Equal(t, "exec_command", call.Function.Name)
	require.Equal(t, item.Arguments, call.Function.Arguments)
}

func TestResponsesChatTerminalNewToolAfterCompactedIndices(t *testing.T) {
	state := NewResponsesEventToChatState()
	first := ResponsesOutput{Type: "function_call", CallID: "call_first", Name: "exec_command", Arguments: `{"cmd":"pwd"}`}
	second := ResponsesOutput{Type: "function_call", CallID: "call_second", Name: "exec_command", Arguments: `{"cmd":"ls"}`}
	ResponsesEventToChatChunks(&ResponsesStreamEvent{Type: "response.output_item.done", OutputIndex: 1, Item: &first}, state)
	chunks := ResponsesEventToChatChunks(&ResponsesStreamEvent{
		Type: "response.completed", Response: &ResponsesResponse{Status: "completed", Output: []ResponsesOutput{first, second}},
	}, state)
	var id, arguments string
	for _, chunk := range chunks {
		for _, choice := range chunk.Choices {
			for _, call := range choice.Delta.ToolCalls {
				id += call.ID
				arguments += call.Function.Arguments
				require.Equal(t, 1, *call.Index)
			}
		}
	}
	require.Equal(t, "call_second", id, "terminal array index must not alias another streamed call")
	require.Equal(t, second.Arguments, arguments)
}
