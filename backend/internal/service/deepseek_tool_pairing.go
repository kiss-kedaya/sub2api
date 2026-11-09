package service

import (
	"encoding/json"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// dropOrphanDeepSeekToolMessages removes role=tool messages whose
// tool_call_id was never announced by a preceding assistant tool_calls
// message. DeepSeek rejects the whole request with
// "Messages with role 'tool' must be a response to a preceding message with
// 'tool_calls'" and an agent history rewritten by a client can contain such
// orphans (e.g. after a reconnect or a dropped assistant turn).
func dropOrphanDeepSeekToolMessages(body []byte) []byte {
	messages := gjson.GetBytes(body, "messages")
	if !messages.IsArray() {
		return body
	}
	announced := make(map[string]struct{})
	kept := make([]string, 0, len(messages.Array()))
	changed := false
	for _, m := range messages.Array() {
		switch m.Get("role").String() {
		case "assistant":
			for _, tc := range m.Get("tool_calls").Array() {
				if id := tc.Get("id").String(); id != "" {
					announced[id] = struct{}{}
				}
			}
		case "tool":
			id := m.Get("tool_call_id").String()
			if _, ok := announced[id]; id == "" || !ok {
				changed = true
				continue
			}
		}
		kept = append(kept, m.Raw)
	}
	if !changed {
		return body
	}
	out, err := sjson.SetRawBytes(body, "messages", []byte("["+strings.Join(kept, ",")+"]"))
	if err != nil {
		return body
	}
	return out
}

// normalizeDeepSeekChatMessages rebuilds tool pairs into the exact order
// DeepSeek requires (assistant tool_calls immediately followed by its tool
// replies). The orphan-only filter is not enough when another message sits
// between the assistant and its replies.
func normalizeDeepSeekChatMessages(body []byte) []byte {
	raw := gjson.GetBytes(body, "messages")
	if !raw.IsArray() {
		return body
	}
	var messages []apicompat.ChatMessage
	if err := json.Unmarshal([]byte(raw.Raw), &messages); err != nil {
		return body
	}
	normalized := apicompat.NormalizeChatMessages(messages)
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return body
	}
	out, err := sjson.SetRawBytes(body, "messages", encoded)
	if err != nil {
		return body
	}
	return out
}
