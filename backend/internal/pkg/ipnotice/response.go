package ipnotice

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func index(value int) string { return strconv.Itoa(value) }

func set(body []byte, path string, value any) []byte {
	next, err := sjson.SetBytes(body, path, value)
	if err != nil {
		return body
	}
	return next
}

func remove(body []byte, path string) []byte {
	next, err := sjson.DeleteBytes(body, path)
	if err != nil {
		return body
	}
	return next
}

func prependArray(body []byte, path string, value any) []byte {
	items := gjson.GetBytes(body, path).Array()
	values := make([]any, 0, len(items)+1)
	values = append(values, value)
	for _, item := range items {
		values = append(values, json.RawMessage(item.Raw))
	}
	return set(body, path, values)
}

func noticeItem(id, notice string) map[string]any {
	return map[string]any{"id": id, "type": "message", "role": "assistant", "status": "completed",
		"content": []any{map[string]any{"type": "output_text", "text": WrapOutputText("", notice), "annotations": []any{}}}}
}

// decorateParts edits real text blocks rather than creating a separate message
// that clients may discard. Tool calls, reasoning and signed parts stay intact.
func decorateParts(body []byte, path string, protocol Protocol, notice string) ([]byte, bool) {
	parts := gjson.GetBytes(body, path).Array()
	var visible []int
	for i, part := range parts {
		kind := part.Get("type").String()
		text := kind == "text" || kind == "output_text"
		if protocol == Gemini {
			text = part.Get("text").Type == gjson.String && !part.Get("thought").Bool() && !part.Get("thoughtSignature").Exists()
		}
		if text {
			visible = append(visible, i)
		}
	}
	for n, i := range visible {
		text := parts[i].Get("text").String()
		if protocol == Gemini || protocol == Chat {
			if n == 0 {
				text = RenderNotice(notice) + text
			}
			if n == len(visible)-1 {
				text += RenderNoticeSuffix(notice)
			}
		} else {
			// Responses clients may select the final message/content block.
			text = WrapOutputText(text, notice)
		}
		body = set(body, path+"."+index(i)+".text", text)
	}
	return body, len(visible) > 0
}

// TransformJSON decorates successful visible text, never upstream errors.
// Token usage, tool arguments and original upstream message IDs stay intact.
func TransformJSON(body []byte, protocol Protocol, notice string) []byte {
	if notice == "" || !gjson.ValidBytes(body) || gjson.GetBytes(body, "error").Exists() {
		return body
	}
	switch protocol {
	case Anthropic:
		if gjson.GetBytes(body, "type").String() != "message" {
			return body
		}
		if out, found := decorateParts(body, "content", protocol, notice); found {
			return out
		}
		return set(body, "content.-1", map[string]any{"type": "text", "text": WrapOutputText("", notice)})
	case Responses:
		if !gjson.GetBytes(body, "output").IsArray() {
			return body
		}
		found := false
		for i, item := range gjson.GetBytes(body, "output").Array() {
			if item.Get("type").String() != "message" {
				continue
			}
			var visible bool
			body, visible = decorateParts(body, "output."+index(i)+".content", protocol, notice)
			found = found || visible
		}
		if !found {
			body = set(body, "output.-1", noticeItem("msg_gateway_notice_"+gjson.GetBytes(body, "id").String(), notice))
		}
		if text := gjson.GetBytes(body, "output_text"); text.Type == gjson.String {
			body = set(body, "output_text", WrapOutputText(text.String(), notice))
		}
		return body
	case Chat:
		for i, choice := range gjson.GetBytes(body, "choices").Array() {
			path := "choices." + index(i) + ".message.content"
			content := choice.Get("message.content")
			if content.Type == gjson.String || !content.Exists() || content.Type == gjson.Null {
				body = set(body, path, WrapOutputText(content.String(), notice))
			} else if content.IsArray() {
				var found bool
				body, found = decorateParts(body, path, protocol, notice)
				if !found {
					body = prependArray(body, path, map[string]any{"type": "text", "text": WrapOutputText("", notice)})
				}
			}
		}
	case Gemini:
		for i := range gjson.GetBytes(body, "candidates").Array() {
			path := "candidates." + index(i) + ".content.parts"
			var found bool
			body, found = decorateParts(body, path, protocol, notice)
			if !found {
				body = prependArray(body, path, map[string]any{"text": WrapOutputText("", notice)})
			}
		}
	}
	return body
}

type responseTextState struct {
	prefix, suffix bool
	hasText        bool
	itemID         string
	output, part   int64
}

// EventTransformer edits only the downstream projection after upstream parsing
// and billing. Its mutex also serializes WebSocket frame transformation.
type EventTransformer struct {
	mu          sync.Mutex
	protocol    Protocol
	notice      string
	started     bool
	seen, ended map[int64]bool
	textBlocks  map[int64]bool
	nextBlock   int64
	lastChat    []byte
	responseID  string
	parts       map[string]*responseTextState
	itemIDs     map[int64]string
	sequence    int64
}

func NewEventTransformer(protocol Protocol, notice string) *EventTransformer {
	t := &EventTransformer{protocol: protocol, notice: notice}
	t.reset()
	return t
}

func (t *EventTransformer) reset() {
	t.seen, t.ended = map[int64]bool{}, map[int64]bool{}
	t.textBlocks = map[int64]bool{}
	t.parts, t.itemIDs = map[string]*responseTextState{}, map[int64]string{}
	t.nextBlock, t.sequence = 0, 0
}

// Transform keeps streaming text and final snapshots in agreement. Terminal
// notices precede finish_reason/message_stop/response.completed because clients
// may stop consuming the stream as soon as a finish event arrives.
func (t *EventTransformer) Transform(body []byte) [][]byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.notice == "" {
		return [][]byte{body}
	}
	if t.protocol == Chat && strings.TrimSpace(string(body)) == "[DONE]" {
		var out [][]byte
		for idx := range t.seen {
			if !t.ended[idx] {
				out = append(out, t.chatTextFrame(t.lastChat, idx, RenderNoticeSuffix(t.notice)))
				t.ended[idx] = true
			}
		}
		return append(out, body)
	}
	if !gjson.ValidBytes(body) || gjson.GetBytes(body, "error").Exists() || gjson.GetBytes(body, "response.error").Exists() {
		return [][]byte{body}
	}
	kind := gjson.GetBytes(body, "type").String()
	switch t.protocol {
	case Anthropic:
		return t.anthropic(body, kind)
	case Chat:
		return t.chat(body)
	case Gemini:
		for i, candidate := range gjson.GetBytes(body, "candidates").Array() {
			idx := candidate.Get("index").Int()
			path := "candidates." + index(i) + ".content.parts"
			for j, part := range candidate.Get("content.parts").Array() {
				if part.Get("text").Type == gjson.String && !part.Get("thought").Bool() && !part.Get("thoughtSignature").Exists() && !t.seen[idx] {
					body = set(body, path+"."+index(j)+".text", RenderNotice(t.notice)+part.Get("text").String())
					t.seen[idx] = true
				}
			}
			if candidate.Get("finishReason").String() != "" && !t.ended[idx] {
				text := RenderNoticeSuffix(t.notice)
				if !t.seen[idx] {
					text = RenderNotice(t.notice) + text
				}
				body = set(body, path+".-1", map[string]any{"text": text})
				t.seen[idx], t.ended[idx] = true, true
			}
		}
	case Responses:
		if kind == "response.created" {
			t.reset()
			t.started = true
			t.responseID = gjson.GetBytes(body, "response.id").String()
		}
		out := t.responses(body, kind)
		if value := gjson.GetBytes(body, "sequence_number"); value.Exists() {
			for i := range out {
				out[i] = set(out[i], "sequence_number", value.Int()+t.sequence+int64(i))
			}
			t.sequence += int64(len(out) - 1)
		}
		return out
	}
	return [][]byte{body}
}

func (t *EventTransformer) anthropic(body []byte, kind string) [][]byte {
	if kind == "message_start" {
		t.reset()
		t.started = true
		return [][]byte{body}
	}
	idx := gjson.GetBytes(body, "index").Int()
	if kind == "content_block_start" {
		if idx >= t.nextBlock {
			t.nextBlock = idx + 1
		}
		if gjson.GetBytes(body, "content_block.type").String() == "text" {
			t.textBlocks[idx], t.seen[idx] = true, true
			text := gjson.GetBytes(body, "content_block.text").String()
			if text != "" {
				return [][]byte{set(body, "content_block.text", RenderNotice(t.notice)+text)}
			}
			return append([][]byte{body}, encodeEvents(map[string]any{"type": "content_block_delta", "index": idx, "delta": map[string]any{"type": "text_delta", "text": RenderNotice(t.notice)}})...)
		}
	}
	if kind == "content_block_stop" && t.textBlocks[idx] && !t.ended[idx] {
		t.ended[idx] = true
		return append(encodeEvents(map[string]any{"type": "content_block_delta", "index": idx, "delta": map[string]any{"type": "text_delta", "text": RenderNoticeSuffix(t.notice)}}), body)
	}
	if t.started && (kind == "message_delta" || kind == "message_stop") && len(t.textBlocks) == 0 {
		t.textBlocks[t.nextBlock] = true
		return append(encodeEvents(
			map[string]any{"type": "content_block_start", "index": t.nextBlock, "content_block": map[string]any{"type": "text", "text": ""}},
			map[string]any{"type": "content_block_delta", "index": t.nextBlock, "delta": map[string]any{"type": "text_delta", "text": WrapOutputText("", t.notice)}},
			map[string]any{"type": "content_block_stop", "index": t.nextBlock},
		), body)
	}
	return [][]byte{body}
}

func (t *EventTransformer) chatTextFrame(template []byte, idx int64, text string) []byte {
	if len(template) == 0 {
		template = []byte(`{"object":"chat.completion.chunk"}`)
	}
	frame := set(template, "choices", []any{map[string]any{"index": idx, "delta": map[string]any{"content": text}, "finish_reason": nil}})
	return remove(frame, "usage")
}

func (t *EventTransformer) chat(body []byte) [][]byte {
	t.lastChat = append(t.lastChat[:0], body...)
	var before [][]byte
	for i, choice := range gjson.GetBytes(body, "choices").Array() {
		idx := choice.Get("index").Int()
		text := choice.Get("delta.content")
		finishing := choice.Get("finish_reason").Type == gjson.String && choice.Get("finish_reason").String() != ""
		if finishing && !t.ended[idx] {
			value := text.String()
			if !t.seen[idx] {
				value = RenderNotice(t.notice) + value
			}
			before = append(before, t.chatTextFrame(body, idx, value+RenderNoticeSuffix(t.notice)))
			body = remove(body, "choices."+index(i)+".delta.content")
			t.seen[idx], t.ended[idx] = true, true
		} else if text.Type == gjson.String && !t.seen[idx] {
			body = set(body, "choices."+index(i)+".delta.content", RenderNotice(t.notice)+text.String())
			t.seen[idx] = true
		}
	}
	return append(before, body)
}

func (t *EventTransformer) partState(body []byte) *responseTextState {
	output, part := gjson.GetBytes(body, "output_index").Int(), gjson.GetBytes(body, "content_index").Int()
	key := strconv.FormatInt(output, 10) + ":" + strconv.FormatInt(part, 10)
	state := t.parts[key]
	if state == nil {
		state = &responseTextState{output: output, part: part}
		t.parts[key] = state
	}
	if id := gjson.GetBytes(body, "item_id").String(); id != "" {
		state.itemID = id
	} else if state.itemID == "" {
		state.itemID = t.itemIDs[output]
	}
	return state
}

func responseDelta(state *responseTextState, text string) []byte {
	event := map[string]any{"type": "response.output_text.delta", "output_index": state.output, "content_index": state.part, "delta": text}
	if state.itemID != "" {
		event["item_id"] = state.itemID
	}
	return encodeEvents(event)[0]
}

func (t *EventTransformer) finishResponseText(state *responseTextState, text string) [][]byte {
	if state.suffix {
		return nil
	}
	value := ""
	if !state.prefix {
		value = RenderNotice(t.notice)
	}
	if !state.hasText {
		value += text
	}
	state.prefix, state.suffix = true, true
	return [][]byte{responseDelta(state, value+RenderNoticeSuffix(t.notice))}
}

func (t *EventTransformer) finishResponseItem(body []byte, path string, output int64) ([][]byte, []byte) {
	var before [][]byte
	item := gjson.GetBytes(body, path)
	if item.Get("type").String() != "message" {
		return before, body
	}
	t.itemIDs[output] = item.Get("id").String()
	for i, part := range item.Get("content").Array() {
		if part.Get("type").String() != "output_text" {
			continue
		}
		meta := set([]byte(`{}`), "output_index", output)
		meta = set(meta, "content_index", i)
		state := t.partState(meta)
		before = append(before, t.finishResponseText(state, part.Get("text").String())...)
		body = set(body, path+".content."+index(i)+".text", WrapOutputText(part.Get("text").String(), t.notice))
	}
	return before, body
}

func (t *EventTransformer) responses(body []byte, kind string) [][]byte {
	switch kind {
	case "response.output_item.added":
		t.itemIDs[gjson.GetBytes(body, "output_index").Int()] = gjson.GetBytes(body, "item.id").String()
	case "response.content_part.added":
		if gjson.GetBytes(body, "part.type").String() == "output_text" {
			state := t.partState(body)
			if !state.prefix {
				text := gjson.GetBytes(body, "part.text").String()
				body = set(body, "part.text", "")
				state.prefix, state.hasText = true, text != ""
				return [][]byte{body, responseDelta(state, RenderNotice(t.notice)+text)}
			}
		}
	case "response.output_text.delta":
		state := t.partState(body)
		text := gjson.GetBytes(body, "delta").String()
		state.hasText = state.hasText || text != ""
		if !state.prefix {
			state.prefix = true
			body = set(body, "delta", RenderNotice(t.notice)+text)
		}
	case "response.output_text.done", "response.content_part.done":
		path := "text"
		if kind == "response.content_part.done" {
			if gjson.GetBytes(body, "part.type").String() != "output_text" {
				break
			}
			path = "part.text"
		}
		state := t.partState(body)
		text := gjson.GetBytes(body, path).String()
		before := t.finishResponseText(state, text)
		return append(before, set(body, path, WrapOutputText(text, t.notice)))
	case "response.output_item.done":
		before, final := t.finishResponseItem(body, "item", gjson.GetBytes(body, "output_index").Int())
		return append(before, final)
	case "response.completed", "response.done", "response.incomplete":
		var before [][]byte
		output := gjson.GetBytes(body, "response.output").Array()
		for i := range output {
			var frames [][]byte
			frames, body = t.finishResponseItem(body, "response.output."+index(i), int64(i))
			before = append(before, frames...)
		}
		for _, state := range t.parts {
			before = append(before, t.finishResponseText(state, "")...)
		}
		if len(t.parts) == 0 {
			id := "msg_gateway_notice_" + t.responseID
			item := noticeItem(id, t.notice)
			text := WrapOutputText("", t.notice)
			part := map[string]any{"type": "output_text", "text": text, "annotations": []any{}}
			at := len(output)
			before = encodeEvents(
				map[string]any{"type": "response.output_item.added", "output_index": at, "item": map[string]any{"id": id, "type": "message", "role": "assistant", "status": "in_progress", "content": []any{}}},
				map[string]any{"type": "response.content_part.added", "output_index": at, "content_index": 0, "item_id": id, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}},
				map[string]any{"type": "response.output_text.delta", "output_index": at, "content_index": 0, "item_id": id, "delta": text},
				map[string]any{"type": "response.output_text.done", "output_index": at, "content_index": 0, "item_id": id, "text": text},
				map[string]any{"type": "response.content_part.done", "output_index": at, "content_index": 0, "item_id": id, "part": part},
				map[string]any{"type": "response.output_item.done", "output_index": at, "item": item},
			)
			body = set(body, "response.output.-1", item)
		}
		if text := gjson.GetBytes(body, "response.output_text"); text.Type == gjson.String {
			body = set(body, "response.output_text", WrapOutputText(text.String(), t.notice))
		}
		return append(before, body)
	}
	return [][]byte{body}
}

func encodeEvents(events ...map[string]any) [][]byte {
	out := make([][]byte, 0, len(events))
	for _, event := range events {
		encoded, err := json.Marshal(event)
		if err == nil {
			out = append(out, encoded)
		}
	}
	return out
}
