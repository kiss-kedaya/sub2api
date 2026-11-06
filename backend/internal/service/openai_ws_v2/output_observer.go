package openai_ws_v2

import (
	"encoding/json"

	"github.com/tidwall/gjson"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
)

// ObserveOutputEvent feeds one upstream Responses event into acc when the event
// carries output the upstream really produced: visible text, refusal, reasoning
// summary, and the name and arguments of tool calls. Every other event is ignored.
//
// A turn that ends without a terminal event (the client left, so the upstream was
// closed) never reports usage. The accumulator is what lets the gateway estimate the
// output of such a turn from content instead of billing nothing.
//
// Deltas are read with gjson instead of decoding the whole event: this runs for every
// frame of every turn.
func ObserveOutputEvent(acc *apicompat.BufferedResponseAccumulator, eventType string, message []byte) {
	if acc == nil || len(message) == 0 {
		return
	}
	switch eventType {
	case "response.output_text.delta",
		"response.refusal.delta",
		"response.reasoning_summary_text.delta",
		"response.reasoning_text.delta",
		"response.function_call_arguments.delta",
		"response.custom_tool_call_input.delta":
		values := gjson.GetManyBytes(message, "delta", "output_index")
		acc.ProcessEvent(&apicompat.ResponsesStreamEvent{
			Type:        eventType,
			Delta:       values[0].String(),
			OutputIndex: int(values[1].Int()),
		})
	case "response.function_call_arguments.done",
		"response.custom_tool_call_input.done":
		values := gjson.GetManyBytes(message, "arguments", "input", "output_index")
		acc.ProcessEvent(&apicompat.ResponsesStreamEvent{
			Type:        eventType,
			Arguments:   values[0].String(),
			Input:       values[1].String(),
			OutputIndex: int(values[2].Int()),
		})
	case "response.output_item.added", "response.output_item.done":
		// Only tool calls matter here. Other items (image generation results can be
		// megabytes of base64) are not decoded at all.
		switch gjson.GetBytes(message, "item.type").String() {
		case "function_call", "custom_tool_call":
		default:
			return
		}
		var event apicompat.ResponsesStreamEvent
		if err := json.Unmarshal(message, &event); err != nil {
			return
		}
		acc.ProcessEvent(&event)
	}
}
