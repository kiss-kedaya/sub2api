package ipnotice

import (
	"encoding/json"
	"strconv"
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
		"content": []any{map[string]any{"type": "output_text", "text": RenderNotice(notice), "annotations": []any{}}}}
}

// TransformJSON inserts visible text into successful non-streamed responses.
// Error envelopes are deliberately left untouched.
func TransformJSON(body []byte, protocol Protocol, notice string) []byte {
	if notice == "" || !gjson.ValidBytes(body) || gjson.GetBytes(body, "error").Exists() {
		return body
	}
	switch protocol {
	case Anthropic:
		if gjson.GetBytes(body, "type").String() != "message" {
			return body
		}
		parts := gjson.GetBytes(body, "content").Array()
		values := make([]any, 0, len(parts)+1)
		inserted := false
		for _, part := range parts {
			kind := part.Get("type").String()
			if !inserted && kind != "thinking" && kind != "redacted_thinking" {
				values = append(values, map[string]any{"type": "text", "text": RenderNotice(notice)})
				inserted = true
			}
			values = append(values, json.RawMessage(part.Raw))
		}
		if !inserted {
			values = append(values, map[string]any{"type": "text", "text": RenderNotice(notice)})
		}
		return set(body, "content", values)
	case Responses:
		if !gjson.GetBytes(body, "output").IsArray() {
			return body
		}
		return prependArray(body, "output", noticeItem("msg_gateway_notice_"+gjson.GetBytes(body, "id").String(), notice))
	case Chat:
		for i, choice := range gjson.GetBytes(body, "choices").Array() {
			path := "choices." + index(i) + ".message.content"
			content := choice.Get("message.content")
			if content.Type == gjson.String || !content.Exists() || content.Type == gjson.Null {
				body = set(body, path, RenderNotice(notice)+content.String())
			} else if content.IsArray() {
				body = prependArray(body, path, map[string]any{"type": "text", "text": RenderNotice(notice)})
			}
		}
	case Gemini:
		for i := range gjson.GetBytes(body, "candidates").Array() {
			body = prependArray(body, "candidates."+index(i)+".content.parts", map[string]any{"text": RenderNotice(notice)})
		}
	}
	return body
}

// EventTransformer modifies only client-visible frames after upstream parsing
// and billing. Its mutex also serializes WebSocket frame transformation.
type EventTransformer struct {
	mu        sync.Mutex
	protocol  Protocol
	notice    string
	started   bool
	seen      map[int64]bool
	itemID    string
	inserted  bool
	insertAt  int64
	nextBlock int64
}

// NewEventTransformer creates one transformer per response or WS connection.
func NewEventTransformer(protocol Protocol, notice string) *EventTransformer {
	return &EventTransformer{protocol: protocol, notice: notice, seen: map[int64]bool{}}
}

// Transform returns one or more valid JSON events. Unrelated and error frames
// retain their original bytes; generated notice events use normal text blocks.
func (t *EventTransformer) Transform(body []byte) [][]byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !gjson.ValidBytes(body) || gjson.GetBytes(body, "error").Exists() || gjson.GetBytes(body, "response.error").Exists() {
		return [][]byte{body}
	}
	kind := gjson.GetBytes(body, "type").String()
	switch t.protocol {
	case Anthropic:
		if kind == "message_start" {
			t.started = true
			t.inserted, t.nextBlock = false, 0
			return [][]byte{body}
		}
		var added [][]byte
		if t.started && !t.inserted {
			blockKind := gjson.GetBytes(body, "content_block.type").String()
			if kind == "content_block_start" && blockKind != "thinking" && blockKind != "redacted_thinking" {
				t.insertAt = gjson.GetBytes(body, "index").Int()
				added = t.anthropicNotice()
			} else if kind == "message_delta" || kind == "message_stop" {
				t.insertAt = t.nextBlock
				added = t.anthropicNotice()
			}
		}
		if t.started && (kind == "content_block_start" || kind == "content_block_delta" || kind == "content_block_stop") {
			if value := gjson.GetBytes(body, "index"); value.Exists() {
				if kind == "content_block_start" && value.Int()+1 > t.nextBlock {
					t.nextBlock = value.Int() + 1
				}
				if t.inserted && value.Int() >= t.insertAt {
					body = set(body, "index", value.Int()+1)
				}
			}
		}
		if len(added) > 0 {
			return append(added, body)
		}
	case Chat:
		for i, choice := range gjson.GetBytes(body, "choices").Array() {
			idx := choice.Get("index").Int()
			if !t.seen[idx] {
				t.seen[idx] = true
				body = set(body, "choices."+index(i)+".delta.content", RenderNotice(t.notice)+choice.Get("delta.content").String())
			}
		}
	case Gemini:
		for i, candidate := range gjson.GetBytes(body, "candidates").Array() {
			idx := candidate.Get("index").Int()
			if !t.seen[idx] {
				t.seen[idx] = true
				body = prependArray(body, "candidates."+index(i)+".content.parts", map[string]any{"text": RenderNotice(t.notice)})
			}
		}
	case Responses:
		if kind == "response.created" {
			t.started = true
			t.itemID = "msg_gateway_notice_" + gjson.GetBytes(body, "response.id").String()
			item := noticeItem(t.itemID, t.notice)
			part := map[string]any{"type": "output_text", "text": RenderNotice(t.notice), "annotations": []any{}}
			addedItem := map[string]any{"id": t.itemID, "type": "message", "role": "assistant", "status": "in_progress", "content": []any{}}
			events := []map[string]any{
				{"type": "response.output_item.added", "item": addedItem},
				{"type": "response.content_part.added", "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}, "content_index": 0, "item_id": t.itemID},
				{"type": "response.output_text.delta", "delta": RenderNotice(t.notice), "content_index": 0, "item_id": t.itemID},
				{"type": "response.output_text.done", "text": RenderNotice(t.notice), "content_index": 0, "item_id": t.itemID},
				{"type": "response.content_part.done", "part": part, "content_index": 0, "item_id": t.itemID},
				{"type": "response.output_item.done", "item": item},
			}
			out := [][]byte{body}
			for i, event := range events {
				event["output_index"] = 0
				event["sequence_number"] = gjson.GetBytes(body, "sequence_number").Int() + int64(i) + 1
				out = append(out, encodeEvents(event)...)
			}
			return out
		}
		if t.started {
			if value := gjson.GetBytes(body, "output_index"); value.Exists() {
				body = set(body, "output_index", value.Int()+1)
			}
			if value := gjson.GetBytes(body, "sequence_number"); value.Exists() {
				body = set(body, "sequence_number", value.Int()+6)
			}
			if kind == "response.completed" || kind == "response.done" || kind == "response.incomplete" {
				if gjson.GetBytes(body, "response.output").IsArray() {
					body = prependArray(body, "response.output", noticeItem(t.itemID, t.notice))
				}
			}
		}
	}
	return [][]byte{body}
}

func (t *EventTransformer) anthropicNotice() [][]byte {
	t.inserted = true
	return encodeEvents(
		map[string]any{"type": "content_block_start", "index": t.insertAt, "content_block": map[string]any{"type": "text", "text": ""}},
		map[string]any{"type": "content_block_delta", "index": t.insertAt, "delta": map[string]any{"type": "text_delta", "text": RenderNotice(t.notice)}},
		map[string]any{"type": "content_block_stop", "index": t.insertAt},
	)
}

func encodeEvents(events ...map[string]any) [][]byte {
	out := make([][]byte, 0, len(events))
	for _, event := range events {
		// These events contain only JSON-safe literals, strings and integers.
		encoded, err := json.Marshal(event)
		if err == nil {
			out = append(out, encoded)
		}
	}
	return out
}
