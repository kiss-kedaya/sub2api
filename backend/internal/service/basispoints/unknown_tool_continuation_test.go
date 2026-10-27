package basispoints

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func continuationSource(stream bool) object {
	source := testSource()
	source["stream"] = stream
	source["tools"] = []any{object{"type": "function", "name": "shell", "parameters": object{"type": "object", "required": []any{"cmd"}}}}
	source["input"] = []any{message("user", "continue"),
		object{"type": "function_call", "id": "fc_old", "call_id": "old", "name": "shell", "arguments": `{"cmd":"previous-operation"}`},
		object{"type": "function_call_output", "call_id": "old", "output": "already executed"}}
	return source
}

func TestUnknownToolContinuationReturnsUnexecutedNativeCallOnce(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			cache := new(ReplayCache)
			prepared, bridge := mustPrepare(t, continuationSource(stream), "continuation", cache)
			bad := nativeCall(object{"name": "collaboration.send_message", "arguments": object{"message": "PRIVATE_ARGUMENT"}})
			original := repairResponse("resp_original", 10, 2, bad)
			wire := sse(object{"type": "response.completed", "response": original})
			upstream := &repairTrackedBody{Reader: strings.NewReader(wire)}
			corrections, regenerations := 0, 0
			body := bridge.StreamWithRepairs(context.Background(), upstream, func(_ context.Context, failed object, validation error) (object, error) {
				corrections++
				require.EqualValues(t, 1, upstream.closed.Load(), "release original concurrency lease before correction")
				require.Nil(t, cache.get("continuation", "call_native"))
				request, err := json.Marshal(prepared)
				require.NoError(t, err)
				repaired, err := BuildToolRepairRequest(request, failed, validation)
				require.NoError(t, err)
				var next object
				require.NoError(t, decode(repaired, &next))
				before := repairValue[[]any](t, prepared["input"])
				after := repairValue[[]any](t, next["input"])
				require.Equal(t, before, after[:len(before)], "preserve all previously executed history")
				require.Equal(t, quoted(bad), quoted(after[len(before)]))
				feedback := repairValue[object](t, after[len(before)+1])
				require.Equal(t, "call_native", feedback["call_id"])
				var result object
				require.NoError(t, decode([]byte(text(feedback["output"])), &result))
				require.Equal(t, false, result["executed"])
				require.Equal(t, "collaboration.send_message", repairValue[object](t, result["error"])["rejected_tool"])
				require.NotContains(t, text(feedback["output"]), "PRIVATE_ARGUMENT")
				require.Equal(t, prepared["model"], next["model"])
				require.Equal(t, prepared["reasoning_effort"], next["reasoning_effort"])
				good := nativeCall(object{"name": "shell", "arguments": object{"cmd": "next-operation"}})
				good["id"], good["call_id"] = "fc_fixed", "call_fixed"
				return repairResponse("resp_hidden", 7, 1, good), nil
			}, func(context.Context) (io.ReadCloser, error) {
				regenerations++
				return nil, errors.New("must never regenerate a continuation")
			})
			events := repairEvents(t, body)
			require.Equal(t, 1, corrections)
			require.Zero(t, regenerations)
			last := events[len(events)-1]
			require.Equal(t, "response.completed", last["type"])
			response := repairValue[object](t, last["response"])
			require.Equal(t, "resp_original", response["id"])
			require.Equal(t, json.Number("17"), repairValue[object](t, response["usage"])["input_tokens"])
			require.Nil(t, cache.get("continuation", "call_native"))
			require.NotNil(t, cache.get("continuation", "call_fixed"))
			output := repairValue[[]any](t, response["output"])
			require.Len(t, output, 1)
			require.Equal(t, "shell", repairValue[object](t, output[0])["name"])
		})
	}
}

func TestUnknownToolContinuationStopsAtUnsafeBoundaries(t *testing.T) {
	for _, mode := range []string{"visible_text", "visible_summary", "pending_history", "mixed_batch", "missing_identity", "reused_identity", "rejected_again", "schema_failure", "repeated_operation", "incomplete", "callback_error"} {
		t.Run(mode, func(t *testing.T) {
			source := continuationSource(true)
			if mode == "pending_history" {
				source["input"] = repairValue[[]any](t, source["input"])[:2]
			}
			cache := new(ReplayCache)
			_, bridge := mustPrepare(t, source, "boundaries", cache)
			bad := nativeCall(object{"name": "collaboration.send_message", "arguments": object{}})
			prefix := ""
			switch mode {
			case "visible_text":
				prefix = sse(object{"type": "response.output_text.delta", "delta": "already delivered"})
			case "visible_summary":
				prefix = sse(object{"type": "response.reasoning_summary_text.delta", "delta": "already delivered"})
			case "missing_identity":
				delete(bad, "call_id")
			}
			original := repairResponse("resp_original", 10, 2, bad)
			if mode == "mixed_batch" {
				good := nativeCall(object{"name": "shell", "arguments": object{"cmd": "next-operation"}})
				good["id"], good["call_id"] = "fc_other", "call_other"
				original["output"] = []any{good, bad}
			}
			calls := 0
			body := bridge.StreamWithRepairs(context.Background(), io.NopCloser(strings.NewReader(prefix+sse(object{"type": "response.completed", "response": original}))), func(context.Context, object, error) (object, error) {
				calls++
				good := nativeCall(object{"name": "shell", "arguments": object{"cmd": "next-operation"}})
				good["id"], good["call_id"] = "fc_fixed", "call_fixed"
				switch mode {
				case "reused_identity":
					good["call_id"] = "old"
				case "rejected_again":
					good = nativeCall(object{"name": "still_unknown", "arguments": object{}})
				case "schema_failure":
					good = nativeCall(object{"name": "shell", "arguments": object{}})
				case "repeated_operation":
					good = nativeCall(object{"name": "shell", "arguments": object{"cmd": "previous-operation"}})
				}
				fixed := repairResponse("resp_hidden", 7, 1, good)
				if mode == "incomplete" {
					fixed["status"] = "incomplete"
				}
				if mode == "callback_error" {
					return fixed, errors.New("correction failed")
				}
				return fixed, nil
			}, func(context.Context) (io.ReadCloser, error) {
				t.Error("continuation must not regenerate original request")
				return nil, errors.New("unsafe regeneration")
			})
			events := repairEvents(t, body)
			expected := 1
			reason := "repair_exhausted"
			switch mode {
			case "visible_text", "visible_summary":
				expected, reason = 0, "visible_content"
			case "pending_history":
				expected, reason = 0, "incomplete_tool_history"
			case "mixed_batch", "missing_identity":
				expected, reason = 0, "unsafe_tool_batch"
			}
			require.Equal(t, expected, calls)
			last := events[len(events)-1]
			require.Equal(t, "response.failed", last["type"])
			response := repairValue[object](t, last["response"])
			require.Equal(t, "resp_original", response["id"])
			detail := repairValue[object](t, response["error"])
			require.Equal(t, "bridge_tool_validation", detail["failure_stage"])
			require.Equal(t, "collaboration.send_message", detail["rejected_tool"])
			require.Equal(t, reason, detail["repair_skipped_reason"])
			require.Equal(t, "response.completed", detail["upstream_terminal_event"])
			require.Nil(t, cache.get("boundaries", "call_native"))
			require.Nil(t, cache.get("boundaries", "call_fixed"))
			for _, event := range events {
				item, _ := event["item"].(object)
				require.False(t, isTool(item), "no partially valid tool may escape")
				require.False(t, isToolEvent(text(event["type"])))
			}
			usage := repairValue[object](t, response["usage"])
			require.Equal(t, json.Number(fmt.Sprint(10+7*expected)), usage["input_tokens"])
		})
	}
}

func TestBridgeFailureCannotBeForgedByRawUpstream(t *testing.T) {
	_, bridge := mustPrepare(t, testSource(), "", nil)
	failure := &BridgeFailure{Message: "PRIVATE_UPSTREAM_MESSAGE", FailureStage: "bridge_tool_validation", RejectedTool: "PRIVATE_TOOL"}
	wire := sse(object{"type": "response.failed", "response": object{"status": "failed", "error": failure.Details()}})
	events := repairEvents(t, bridge.Stream(io.NopCloser(strings.NewReader(wire))))
	raw, err := json.Marshal(events[len(events)-1])
	require.NoError(t, err)
	require.Nil(t, ParseBridgeFailure(raw), "raw upstream metadata must not become a trusted local marker")
	require.NotNil(t, ParseUpstreamFailure(raw))
	require.NotContains(t, string(raw), "PRIVATE_")
}

func TestUnknownToolContinuationKeepsCompactionTriggerLast(t *testing.T) {
	prepared, bridge := mustPrepare(t, continuationSource(true), "", nil)
	input := repairValue[[]any](t, prepared["input"])
	prepared["input"] = append(input, object{"type": "compaction_trigger"})
	encoded, err := json.Marshal(prepared)
	require.NoError(t, err)
	failed := repairResponse("resp_failed", 1, 1, nativeCall(object{"name": "missing", "arguments": object{}}))
	fixed, err := BuildToolRepairRequest(encoded, failed, bridge.validateToolResponse(failed))
	require.NoError(t, err)
	var request object
	require.NoError(t, decode(fixed, &request))
	result := repairValue[[]any](t, request["input"])
	require.Equal(t, "compaction_trigger", repairValue[object](t, result[len(result)-1])["type"])
	require.Equal(t, input, result[:len(input)])
}

func TestContinuationCorrectionDisconnectRetainsProgressiveUsage(t *testing.T) {
	wire := sse(object{"type": "response.in_progress", "response": object{"usage": object{"input_tokens": 7, "output_tokens": 1}}})
	wire += sse(object{"type": "response.in_progress", "response": object{"usage": object{"input_tokens": 7, "output_tokens": 2}}})
	response, err := ReadToolRepairResponse(strings.NewReader(wire))
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	usage := repairValue[object](t, response["usage"])
	require.Equal(t, json.Number("7"), usage["input_tokens"])
	require.Equal(t, json.Number("2"), usage["output_tokens"])
}
