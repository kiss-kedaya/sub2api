//go:build unit

package openai_ws_v2

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
)

func observeAll(t *testing.T, events ...string) *apicompat.BufferedResponseAccumulator {
	t.Helper()
	acc := apicompat.NewBufferedResponseAccumulator()
	for _, event := range events {
		ObserveOutputEvent(acc, eventTypeOf(event), []byte(event))
	}
	return acc
}

func eventTypeOf(event string) string {
	// Test events always start with {"type":"..."
	const prefix = `{"type":"`
	rest := event[len(prefix):]
	for i := 0; i < len(rest); i++ {
		if rest[i] == '"' {
			return rest[:i]
		}
	}
	return ""
}

func TestObserveOutputEvent_CollectsWhatTheUpstreamProduced(t *testing.T) {
	t.Parallel()

	acc := observeAll(t,
		`{"type":"response.reasoning_summary_text.delta","delta":"thinking it over"}`,
		`{"type":"response.output_text.delta","output_index":1,"delta":"Hello "}`,
		`{"type":"response.output_text.delta","output_index":1,"delta":"world"}`,
		`{"type":"response.refusal.delta","output_index":1,"delta":"but no"}`,
	)

	output := acc.BuildOutput()
	require.Len(t, output, 2)
	require.Equal(t, "reasoning", output[0].Type)
	require.Equal(t, "thinking it over", output[0].Summary[0].Text)
	require.Equal(t, "message", output[1].Type)
	require.Equal(t, "Hello world", output[1].Content[0].Text)
	require.Equal(t, "but no", output[1].Content[1].Refusal)
}

func TestObserveOutputEvent_CollectsToolCallNameAndArguments(t *testing.T) {
	t.Parallel()

	acc := observeAll(t,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","call_id":"call_1","name":"get_weather"}}`,
		`{"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"city\":"}`,
		`{"type":"response.function_call_arguments.delta","output_index":0,"delta":"\"Paris\"}"}`,
	)

	output := acc.BuildOutput()
	require.Len(t, output, 1)
	require.Equal(t, "function_call", output[0].Type)
	require.Equal(t, "get_weather", output[0].Name)
	require.Equal(t, `{"city":"Paris"}`, output[0].Arguments)
}

func TestObserveOutputEvent_ArgumentsDoneReplacesTheDeltasOfACustomToolCall(t *testing.T) {
	t.Parallel()

	acc := observeAll(t,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"custom_tool_call","call_id":"call_2","name":"apply_patch"}}`,
		`{"type":"response.custom_tool_call_input.delta","output_index":0,"delta":"*** Begin"}`,
		`{"type":"response.custom_tool_call_input.done","output_index":0,"input":"*** Begin Patch"}`,
	)

	output := acc.BuildOutput()
	require.Len(t, output, 1)
	require.Equal(t, "apply_patch", output[0].Name)
	require.Equal(t, "*** Begin Patch", output[0].Arguments)
}

func TestObserveOutputEvent_IgnoresEverythingThatIsNotOutput(t *testing.T) {
	t.Parallel()

	acc := observeAll(t,
		`{"type":"response.created","response":{"id":"resp_x"}}`,
		`{"type":"response.in_progress","response":{"id":"resp_x"}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_1"}}`,
		`{"type":"response.output_item.added","output_index":1,"item":{"type":"image_generation_call","result":"AAAA"}}`,
		`{"type":"response.output_text.done","text":"already counted through deltas"}`,
		`{"type":"response.completed","response":{"id":"resp_x"}}`,
	)

	require.False(t, acc.HasContent())
}

func TestObserveOutputEvent_ToleratesNilAndMalformedInput(t *testing.T) {
	t.Parallel()

	require.NotPanics(t, func() {
		ObserveOutputEvent(nil, "response.output_text.delta", []byte(`{"delta":"x"}`))
		acc := apicompat.NewBufferedResponseAccumulator()
		ObserveOutputEvent(acc, "response.output_text.delta", nil)
		ObserveOutputEvent(acc, "response.output_item.added", []byte(`{"type":"response.output_item.added","item":{"type":"function_call"`))
		ObserveOutputEvent(acc, "response.output_text.delta", []byte(`not json`))
		require.False(t, acc.HasContent())
	})
}
