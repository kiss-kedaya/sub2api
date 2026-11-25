package service

import (
	"bytes"
	"encoding/json"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/tidwall/gjson"
)

// dropOrphanDeepSeekToolMessages removes role=tool messages whose
// tool_call_id was never announced by a preceding assistant tool_calls
// message. DeepSeek rejects the whole request with
// "Messages with role 'tool' must be a response to a preceding message with
// 'tool_calls'" and an agent history rewritten by a client can contain such
// orphans (e.g. after a reconnect or a dropped assistant turn).
//
// The scan reads the body in place (no copy of the messages array); the
// rebuilt array is written once, and only when something is dropped.
func dropOrphanDeepSeekToolMessages(body []byte) []byte {
	messages := gjson.Get(openAIWSPayloadStringView(body), "messages")
	if !messages.IsArray() {
		return body
	}
	var announced map[string]struct{}
	var dropped []int
	index := 0
	messages.ForEach(func(_, m gjson.Result) bool {
		switch m.Get("role").String() {
		case "assistant":
			for _, tc := range m.Get("tool_calls").Array() {
				if id := tc.Get("id").String(); id != "" {
					if announced == nil {
						announced = make(map[string]struct{})
					}
					announced[id] = struct{}{}
				}
			}
		case "tool":
			id := m.Get("tool_call_id").String()
			if _, ok := announced[id]; id == "" || !ok {
				dropped = append(dropped, index)
			}
		}
		index++
		return true
	})
	if len(dropped) == 0 {
		return body
	}
	arrayStart, arrayEnd, ok := jsonResultSpan(body, messages)
	if !ok || !gjson.Valid(messages.Raw) {
		return body
	}
	out := make([]byte, 0, len(body))
	out = append(out, body[:arrayStart]...)
	out = append(out, '[')
	index = 0
	next := 0
	intact := true
	messages.ForEach(func(_, m gjson.Result) bool {
		i := index
		index++
		if next < len(dropped) && dropped[next] == i {
			next++
			return true
		}
		start, end, ok := jsonResultSpan(body, m)
		if !ok {
			intact = false
			return false
		}
		if len(out) > arrayStart+1 {
			out = append(out, ',')
		}
		out = append(out, body[start:end]...)
		return true
	})
	if !intact {
		return body
	}
	out = append(out, ']')
	return append(out, body[arrayEnd:]...)
}

// normalizeDeepSeekChatMessages rebuilds tool pairs into the exact order
// DeepSeek requires (assistant tool_calls immediately followed by its tool
// replies). The orphan-only filter is not enough when another message sits
// between the assistant and its replies.
func normalizeDeepSeekChatMessages(body []byte) []byte {
	raw := gjson.Get(openAIWSPayloadStringView(body), "messages")
	if !raw.IsArray() {
		return body
	}
	start, end, ok := jsonResultSpan(body, raw)
	if !ok {
		return body
	}
	var messages []apicompat.ChatMessage
	if err := json.Unmarshal(body[start:end], &messages); err != nil {
		return body
	}
	normalized := apicompat.NormalizeChatMessages(messages)
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return body
	}
	if bytes.Equal(encoded, body[start:end]) {
		return body
	}
	out := make([]byte, 0, len(body)-(end-start)+len(encoded))
	out = append(out, body[:start]...)
	out = append(out, encoded...)
	return append(out, body[end:]...)
}
