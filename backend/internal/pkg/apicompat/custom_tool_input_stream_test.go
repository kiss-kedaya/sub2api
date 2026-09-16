package apicompat

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func newCustomInputRestorer(t *testing.T) *ResponsesClientToolStreamRestorer {
	t.Helper()
	r := NewResponsesClientToolStreamRestorer(ResponsesClientToolMapping{CustomTools: map[string]bool{"exec": true}})
	out, _, err := r.RestoreEvent([]byte(`{"type":"response.output_item.added","sequence_number":7,"output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"exec","arguments":""}}`))
	require.NoError(t, err)
	require.Len(t, out, 1)
	return r
}

func restoreCustomInputEvent(t *testing.T, r *ResponsesClientToolStreamRestorer, kind, field, value string) ([][]byte, error) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"type": kind, "item_id": "fc_1", "output_index": 0, field: value})
	require.NoError(t, err)
	out, _, err := r.RestoreEvent(payload)
	return out, err
}

func TestCustomToolInputStreamingEverySplit(t *testing.T) {
	for _, arguments := range []string{
		`{"input":"abc"}`,
		` { "in\u0070ut" : "one\n\t\r\b\f\"\\\/two\u4e2d\u6587\ud83d\ude80" } `,
		`{"input":"\ud83dX\udc00Y"}`,
		`{"input":"\ud83d\ud83d\ude80"}`,
		`{"input":""}`,
		`{"input":"alpha","ignored":{"input":"wrong"}}`,
		`{"meta":{"input":"wrong"},"input":"right"}`,
		`{"other":"input","input":"right"}`,
	} {
		for split := 0; split <= len(arguments); split++ {
			r := newCustomInputRestorer(t)
			var received strings.Builder
			nextSeq := int64(8)
			check := func(out [][]byte) {
				for _, event := range out {
					require.Equal(t, nextSeq, gjson.GetBytes(event, "sequence_number").Int())
					nextSeq++
					if gjson.GetBytes(event, "type").String() == "response.custom_tool_call_input.delta" {
						_, _ = received.WriteString(gjson.GetBytes(event, "delta").String())
					}
				}
			}
			for _, part := range []string{arguments[:split], arguments[split:]} {
				out, err := restoreCustomInputEvent(t, r, "response.function_call_arguments.delta", "delta", part)
				require.NoError(t, err, "split=%d arguments=%s", split, arguments)
				check(out)
			}
			out, err := restoreCustomInputEvent(t, r, "response.function_call_arguments.done", "arguments", arguments)
			require.NoError(t, err)
			check(out)
			require.Equal(t, extractCustomToolCallInput(arguments), received.String())
			require.Equal(t, received.String(), gjson.GetBytes(out[len(out)-1], "input").String())
		}
	}
}

func TestCustomToolInputStreamingBytewiseAndInvalidEscapes(t *testing.T) {
	args := `{"input":"` + "\u4e2d\u6587" + `\ud83d\ude80\n\"\\end"}`
	var stream customToolInputStream
	var got strings.Builder
	for i := 1; i <= len(args); i++ {
		delta, err := stream.append(args[:i])
		require.NoError(t, err)
		_, _ = got.WriteString(delta)
	}
	require.Equal(t, extractCustomToolCallInput(args), got.String())
	for _, bad := range []string{`{"input":"\q`, `{"input":"\uZZZZ`, "{\"input\":\"bad\x01"} {
		var decoder customToolInputStream
		_, err := decoder.append(bad)
		require.Error(t, err)
	}
}

func TestCustomToolInputStreamingMissingDoneAndDuplicateDone(t *testing.T) {
	r := newCustomInputRestorer(t)
	out, err := restoreCustomInputEvent(t, r, "response.function_call_arguments.delta", "delta", `{"input":"first`)
	require.NoError(t, err)
	require.Len(t, out, 1)
	out, _, err = r.RestoreEvent([]byte(`{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"exec","arguments":"{\"input\":\"first last\"}"}}`))
	require.NoError(t, err)
	require.Len(t, out, 3)
	require.Equal(t, " last", gjson.GetBytes(out[0], "delta").String())
	require.Equal(t, "first last", gjson.GetBytes(out[1], "input").String())
	require.Equal(t, "first last", gjson.GetBytes(out[2], "item.input").String())
	r = newCustomInputRestorer(t)
	_, err = restoreCustomInputEvent(t, r, "response.function_call_arguments.done", "arguments", `{"input":"first"}`)
	require.NoError(t, err)
	out, err = restoreCustomInputEvent(t, r, "response.function_call_arguments.done", "arguments", `{"input":"first"}`)
	require.NoError(t, err)
	require.Empty(t, out)
}

func TestCustomToolInputStreamingFinalSnapshots(t *testing.T) {
	for _, terminal := range []string{"response.completed", "response.done"} {
		for _, closeFirst := range []bool{false, true} {
			for _, final := range []string{"abc", "abcdef", "xyz"} {
				r := newCustomInputRestorer(t)
				_, err := restoreCustomInputEvent(t, r, "response.function_call_arguments.delta", "delta", `{"input":"abc`)
				require.NoError(t, err)
				if closeFirst {
					_, err = restoreCustomInputEvent(t, r, "response.function_call_arguments.done", "arguments", `{"input":"abc"}`)
					require.NoError(t, err)
				}
				args, err := json.Marshal(map[string]string{"input": final})
				require.NoError(t, err)
				payload, err := json.Marshal(map[string]any{"type": terminal, "sequence_number": 55, "response": map[string]any{"output": []any{map[string]any{"type": "function_call", "id": "fc_1", "call_id": "call_1", "name": "exec", "arguments": string(args)}}}})
				require.NoError(t, err)
				out, _, err := r.RestoreEvent(payload)
				if final == "xyz" || (closeFirst && final != "abc") {
					require.Error(t, err)
					require.Empty(t, out)
					continue
				}
				require.NoError(t, err)
				require.Equal(t, terminal, gjson.GetBytes(out[len(out)-1], "type").String())
				require.Equal(t, final, gjson.GetBytes(out[len(out)-1], "response.output.0.input").String())
				if final == "abcdef" {
					require.Equal(t, "def", gjson.GetBytes(out[0], "delta").String())
				}
			}
		}
	}
	for _, doneKind := range []string{"response.function_call_arguments.done", "response.output_item.done"} {
		r := newCustomInputRestorer(t)
		_, err := restoreCustomInputEvent(t, r, "response.function_call_arguments.done", "arguments", `{"input":"abc"}`)
		require.NoError(t, err)
		if doneKind == "response.function_call_arguments.done" {
			_, err = restoreCustomInputEvent(t, r, doneKind, "arguments", `{"input":"abcdef"}`)
		} else {
			_, _, err = r.RestoreEvent([]byte(`{"type":"response.output_item.done","item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"exec","arguments":"{\"input\":\"abcdef\"}"}}`))
		}
		require.Error(t, err)
	}
}

func TestCustomToolInputStreamingRejectsChangedFinalInput(t *testing.T) {
	for _, final := range []string{`{"input":"other"}`, `{"input":"hello","input":"other"}`, `{"input":"hello`, `{"input":42}`} {
		r := newCustomInputRestorer(t)
		out, err := restoreCustomInputEvent(t, r, "response.function_call_arguments.delta", "delta", `{"input":"hello`)
		require.NoError(t, err)
		require.Len(t, out, 1)
		_, err = restoreCustomInputEvent(t, r, "response.function_call_arguments.done", "arguments", final)
		require.Error(t, err, final)
	}
}

func TestCustomToolInputStreamingDoneOnlyPreservesFallback(t *testing.T) {
	for _, args := range []string{`not json`, `{"input":12}`, `{"nested":{"input":"wrong"}}`, `{"input":"value"}`, ""} {
		r := newCustomInputRestorer(t)
		out, err := restoreCustomInputEvent(t, r, "response.function_call_arguments.done", "arguments", args)
		require.NoError(t, err)
		require.Equal(t, extractCustomToolCallInput(args), gjson.GetBytes(out[len(out)-1], "input").String())
	}
}

func BenchmarkCustomToolInputStreaming(b *testing.B) {
	args := `{"input":"` + strings.Repeat("abcdefghijklmnop", 4096) + `"}`
	for n := 0; n < b.N; n++ {
		r := NewResponsesClientToolStreamRestorer(ResponsesClientToolMapping{CustomTools: map[string]bool{"exec": true}})
		r.Restore(ResponsesStreamEvent{Type: "response.output_item.added", Item: &ResponsesOutput{Type: "function_call", ID: "fc_1", CallID: "call_1", Name: "exec"}})
		for start := 0; start < len(args); start += 16 {
			r.Restore(ResponsesStreamEvent{Type: "response.function_call_arguments.delta", ItemID: "fc_1", Delta: args[start:min(start+16, len(args))]})
		}
	}
}
