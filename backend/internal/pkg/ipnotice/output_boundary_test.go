package ipnotice

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func assertNoticeBoundary(t *testing.T, text, answer string) {
	t.Helper()
	visible := strings.ReplaceAll(text, noticeFence, "")
	lines := strings.Split(visible, "\n")
	require.Equal(t, testNotice, lines[0])
	require.Equal(t, testNotice, lines[len(lines)-1])
	require.Equal(t, 2, strings.Count(visible, testNotice))
	require.Equal(t, WrapOutputText(answer, testNotice), text)
}

func TestGatewayIPNoticeJSONDecoratesActualAnswerWithoutReplacingMessages(t *testing.T) {
	for _, tc := range []struct {
		protocol Protocol
		body     string
		path     string
	}{
		{Chat, `{"id":"real","choices":[{"message":{"role":"assistant","content":"first answer\nlast answer","tool_calls":[{"id":"call","function":{"arguments":"{}"}}]}}],"usage":{"total_tokens":17}}`, "choices.0.message.content"},
		{Anthropic, `{"type":"message","id":"real","content":[{"type":"thinking","thinking":"signed","signature":"unchanged"},{"type":"text","text":"first answer\nlast answer"},{"type":"tool_use","id":"call","input":{"x":1}}],"usage":{"output_tokens":17}}`, "content.1.text"},
		{Responses, `{"id":"real","output":[{"id":"reason","type":"reasoning","encrypted_content":"unchanged"},{"id":"msg_real","type":"message","phase":"final_answer","role":"assistant","content":[{"type":"output_text","text":"first answer\nlast answer","annotations":[]}]},{"type":"function_call","call_id":"call","arguments":"{}"}],"usage":{"output_tokens":17}}`, "output.1.content.0.text"},
		{Gemini, `{"candidates":[{"content":{"role":"model","parts":[{"thought":true,"thoughtSignature":"unchanged","text":"signed"},{"text":"first answer\nlast answer"},{"functionCall":{"name":"exec","args":{"x":1}}}]}}],"usageMetadata":{"totalTokenCount":17}}`, "candidates.0.content.parts.1.text"},
	} {
		t.Run(string(tc.protocol), func(t *testing.T) {
			out := TransformJSON([]byte(tc.body), tc.protocol, testNotice)
			assertNoticeBoundary(t, gjson.GetBytes(out, tc.path).String(), "first answer\nlast answer")
			require.Equal(t, gjson.Get(tc.body, "usage").Raw, gjson.GetBytes(out, "usage").Raw)
			switch tc.protocol {
			case Anthropic:
				require.Equal(t, gjson.Get(tc.body, "content.0").Raw, gjson.GetBytes(out, "content.0").Raw)
				require.Equal(t, gjson.Get(tc.body, "content.2").Raw, gjson.GetBytes(out, "content.2").Raw)
			case Responses:
				require.Len(t, gjson.GetBytes(out, "output").Array(), 3)
				require.Equal(t, "msg_real", gjson.GetBytes(out, "output.1.id").String())
				require.Equal(t, "final_answer", gjson.GetBytes(out, "output.1.phase").String())
				require.Equal(t, gjson.Get(tc.body, "output.0").Raw, gjson.GetBytes(out, "output.0").Raw)
				require.Equal(t, gjson.Get(tc.body, "output.2").Raw, gjson.GetBytes(out, "output.2").Raw)
			}
		})
	}
}

func TestGatewayIPNoticeResponsesDeltasAndEveryFinalSnapshotHaveBothBoundaries(t *testing.T) {
	transform := NewEventTransformer(Responses, testNotice)
	for turn := range 3 {
		var stream strings.Builder
		var finalTexts []string
		lastSequence := int64(-1)
		events := []string{
			`{"type":"response.created","response":{"id":"resp_fixture","output":[]}}`,
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg_real","type":"message","role":"assistant","phase":"final_answer","content":[]}}`,
			`{"type":"response.content_part.added","output_index":0,"content_index":0,"item_id":"msg_real","part":{"type":"output_text","text":"","annotations":[]}}`,
			`{"type":"response.output_text.delta","output_index":0,"content_index":0,"item_id":"msg_real","delta":"first answer\n"}`,
			`{"type":"response.output_text.delta","output_index":0,"content_index":0,"item_id":"msg_real","delta":"last answer"}`,
			`{"type":"response.output_text.done","output_index":0,"content_index":0,"item_id":"msg_real","text":"first answer\nlast answer"}`,
			`{"type":"response.content_part.done","output_index":0,"content_index":0,"item_id":"msg_real","part":{"type":"output_text","text":"first answer\nlast answer","annotations":[]}}`,
			`{"type":"response.output_item.done","output_index":0,"item":{"id":"msg_real","type":"message","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"first answer\nlast answer","annotations":[]}]}}`,
			`{"type":"response.completed","response":{"id":"resp_fixture","output":[{"id":"msg_real","type":"message","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"first answer\nlast answer","annotations":[]}]}],"usage":{"output_tokens":17}}}`,
		}
		for seq, event := range events {
			for _, frame := range transform.Transform(set([]byte(event), "sequence_number", seq)) {
				sequence := gjson.GetBytes(frame, "sequence_number").Int()
				require.Greater(t, sequence, lastSequence, "turn %d", turn)
				lastSequence = sequence
				switch gjson.GetBytes(frame, "type").String() {
				case "response.output_text.delta":
					require.Equal(t, "msg_real", gjson.GetBytes(frame, "item_id").String())
					require.Equal(t, int64(0), gjson.GetBytes(frame, "output_index").Int())
					_, _ = stream.WriteString(gjson.GetBytes(frame, "delta").String())
				case "response.output_text.done":
					finalTexts = append(finalTexts, gjson.GetBytes(frame, "text").String())
				case "response.content_part.done":
					finalTexts = append(finalTexts, gjson.GetBytes(frame, "part.text").String())
				case "response.output_item.done":
					finalTexts = append(finalTexts, gjson.GetBytes(frame, "item.content.0.text").String())
				case "response.completed":
					finalTexts = append(finalTexts, gjson.GetBytes(frame, "response.output.0.content.0.text").String())
					require.Equal(t, int64(17), gjson.GetBytes(frame, "response.usage.output_tokens").Int())
					require.Len(t, gjson.GetBytes(frame, "response.output").Array(), 1)
				}
			}
		}
		assertNoticeBoundary(t, stream.String(), "first answer\nlast answer")
		require.Len(t, finalTexts, 4)
		for _, snapshot := range finalTexts {
			require.Equal(t, stream.String(), snapshot)
		}
	}
}

func TestGatewayIPNoticeFooterArrivesBeforeChatFinishAndDoneSentinel(t *testing.T) {
	for _, sentinelOnly := range []bool{false, true} {
		transform := NewEventTransformer(Chat, testNotice)
		events := []string{`{"id":"c","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`, `{"id":"c","choices":[{"index":0,"delta":{"content":"answer"},"finish_reason":null}]}`}
		if !sentinelOnly {
			events = append(events, `{"id":"c","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"total_tokens":17}}`)
		}
		events = append(events, "[DONE]")
		var visible strings.Builder
		for _, event := range events {
			for _, frame := range transform.Transform([]byte(event)) {
				if string(frame) == "[DONE]" || gjson.GetBytes(frame, "choices.0.finish_reason").String() != "" {
					assertNoticeBoundary(t, visible.String(), "answer")
					continue
				}
				_, _ = visible.WriteString(gjson.GetBytes(frame, "choices.0.delta.content").String())
			}
		}
	}
}

func TestGatewayIPNoticeAnthropicTextKeepsIndicesAndEndsBeforeBlockStop(t *testing.T) {
	transform := NewEventTransformer(Anthropic, testNotice)
	events := []string{`{"type":"message_start","message":{"id":"real"}}`, `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":"signed"}}`, `{"type":"content_block_stop","index":0}`, `{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`, `{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"answer"}}`, `{"type":"content_block_stop","index":1}`, `{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`, `{"type":"message_stop"}`}
	var visible strings.Builder
	for _, event := range events {
		for _, frame := range transform.Transform([]byte(event)) {
			if gjson.GetBytes(frame, "delta.type").String() == "text_delta" {
				require.Equal(t, int64(1), gjson.GetBytes(frame, "index").Int())
				_, _ = visible.WriteString(gjson.GetBytes(frame, "delta.text").String())
			}
			if gjson.GetBytes(frame, "type").String() == "content_block_stop" && gjson.GetBytes(frame, "index").Int() == 1 {
				assertNoticeBoundary(t, visible.String(), "answer")
			}
		}
	}
}

func TestGatewayIPNoticeInlineFirstAndLastLinesAreRemovedFromReplay(t *testing.T) {
	for _, protocol := range []Protocol{Chat, Anthropic, Responses, Gemini} {
		field, content, role := "messages", "content", "assistant"
		switch protocol {
		case Responses:
			field = "input"
		case Gemini:
			field, content, role = "contents", "parts", "model"
		}
		text := map[string]any{"type": "text", "text": WrapOutputText("original answer", testNotice)}
		if protocol == Responses {
			text["type"] = "output_text"
		}
		var messageContent any = []any{text}
		if protocol == Chat {
			messageContent = WrapOutputText("original answer", testNotice)
		}
		body, err := json.Marshal(map[string]any{field: []any{map[string]any{"role": "user", content: "question"}, map[string]any{"role": role, content: messageContent}}})
		require.NoError(t, err)
		clean := NormalizeReplay(body, protocol)
		path := field + ".1." + content
		if protocol != Chat {
			path += ".0.text"
		}
		require.Equal(t, "original answer", gjson.GetBytes(clean, path).String())
		require.NotContains(t, string(clean), noticeFence)
		require.Equal(t, testNotice+"\n\nquestion", gjson.GetBytes(clean, field+".0."+content).String())
	}
}
