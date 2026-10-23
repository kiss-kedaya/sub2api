package ipnotice

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const noticeFence = "\u2063\u2063"

// RenderNotice retains a reversible boundary without displaying protocol
// metadata. The plain configured notice remains the visible content.
func RenderNotice(notice string) string { return noticeFence + notice + noticeFence + "\n\n" }

func takeNotice(text string) (notice, rest string, ok bool) {
	if !strings.HasPrefix(text, noticeFence) {
		return "", text, false
	}
	end := strings.Index(text[len(noticeFence):], noticeFence)
	if end < 0 {
		return "", text, false
	}
	end += len(noticeFence)
	if end == len(noticeFence) || !strings.HasPrefix(text[end+len(noticeFence):], "\n\n") {
		return "", text, false
	}
	return text[len(noticeFence):end], text[end+len(noticeFence)+2:], true
}

// ReplayProtocolForPath includes control/tokenization aliases, where generated
// notice references must also be removed before the request reaches upstream.
func ReplayProtocolForPath(path string) Protocol {
	if strings.Contains(path, "/responses") {
		return Responses
	}
	return ProtocolForPath(strings.TrimSuffix(path, "/count_tokens"))
}

// Protocol identifies the client-facing request and response format.
type Protocol string

const (
	Anthropic Protocol = "anthropic"
	Chat      Protocol = "chat"
	Responses Protocol = "responses"
	Gemini    Protocol = "gemini"
)

// ProtocolForPath selects generation endpoints only. Token counting, media,
// discovery, billing, and compact control endpoints do not carry visible text.
func ProtocolForPath(path string) Protocol {
	switch {
	case strings.HasSuffix(path, "/messages"):
		return Anthropic
	case strings.HasSuffix(path, "/chat/completions"):
		return Chat
	case strings.HasSuffix(path, "/responses"):
		return Responses
	case strings.Contains(path, "/models/") && (strings.HasSuffix(path, ":generateContent") || strings.HasSuffix(path, ":streamGenerateContent")):
		return Gemini
	default:
		return ""
	}
}

func prefix(text, notice string) string {
	if strings.HasPrefix(text, notice) {
		return text
	}
	if text == "" {
		return notice + "\n\n"
	}
	return notice + "\n\n" + text
}

// InjectRequest adds a plain text notice to the current user input. Historical
// messages, function arguments, tool results, signatures, and image data remain
// byte-for-byte intact through targeted JSON edits.
func InjectRequest(body []byte, protocol Protocol, notice string) ([]byte, error) {
	if notice == "" || !gjson.ValidBytes(body) || !gjson.ParseBytes(body).IsObject() {
		return body, nil
	}
	body = NormalizeReplay(body, protocol)
	field, partsField, textType := "messages", "content", "text"
	switch protocol {
	case Responses:
		field, textType = "input", "input_text"
		input := gjson.GetBytes(body, field)
		if input.Type == gjson.String {
			return sjson.SetBytes(body, field, prefix(input.String(), notice))
		}
	case Gemini:
		field, partsField = "contents", "parts"
	case Chat, Anthropic:
	default:
		return body, nil
	}
	items := gjson.GetBytes(body, field).Array()
	// Only the last message belongs to this request's current input. If it is
	// a tool output, append a separate user message rather than alter its JSON.
	if len(items) > 0 && items[len(items)-1].Get("role").String() == "user" {
		path := field + "." + index(len(items)-1) + "." + partsField
		content := gjson.GetBytes(body, path)
		if content.Type == gjson.String {
			return sjson.SetBytes(body, path, prefix(content.String(), notice))
		}
		if content.IsArray() {
			for i, part := range content.Array() {
				kind := part.Get("type").String()
				if (kind == "text" || kind == "input_text" || protocol == Gemini) && part.Get("text").Type == gjson.String {
					return sjson.SetBytes(body, path+"."+index(i)+".text", prefix(part.Get("text").String(), notice))
				}
			}
			part := map[string]any{"text": notice}
			if protocol != Gemini {
				part["type"] = textType
			}
			return sjson.SetBytes(body, path+".-1", part)
		}
	}
	message := map[string]any{"role": "user", partsField: notice}
	switch protocol {
	case Responses:
		message[partsField] = []any{map[string]any{"type": "input_text", "text": notice}}
	case Gemini:
		message[partsField] = []any{map[string]any{"text": notice}}
	}
	if !gjson.GetBytes(body, field).IsArray() {
		return sjson.SetBytes(body, field, []any{message})
	}
	return sjson.SetBytes(body, field+".-1", message)
}

// NormalizeReplay reverses only gateway-created assistant/model notice blocks.
// Each marker identifies a response boundary. Replaying the same input edit on
// the preceding request prefix restores the original upstream conversation,
// including synthetic user input after a tool result. It does not store prompts
// or inspect user/tool/signature text for notices. Run this before IP matching,
// including after an IP change or after the operator disables/edits the policy.
func NormalizeReplay(body []byte, protocol Protocol) []byte {
	if protocol == "" || (!bytes.Contains(body, []byte(noticeFence)) && !bytes.Contains(body, []byte(`\u2063\u2063`)) && !bytes.Contains(body, []byte("msg_gateway_notice_"))) {
		return body
	}
	if !gjson.ValidBytes(body) {
		return body
	}
	field := "messages"
	if protocol == Responses {
		field = "input"
	}
	if protocol == Gemini {
		field = "contents"
	}
	items := gjson.GetBytes(body, field)
	if !items.IsArray() {
		return body
	}
	projected := make([]json.RawMessage, 0, len(items.Array()))
	for _, item := range items.Array() {
		if protocol == Responses && item.Get("type").String() == "item_reference" && strings.HasPrefix(item.Get("id").String(), "msg_gateway_notice_") {
			continue
		}
		role := item.Get("role").String()
		if role != "assistant" && role != "model" {
			projected = append(projected, json.RawMessage(item.Raw))
			continue
		}
		cleaned, notices := cleanAssistantNotice([]byte(item.Raw), protocol)
		for _, notice := range notices {
			// Input injection touches only the final message or appends one.
			// Rebuild that tail, avoiding a quadratic scan of long histories.
			tail := len(projected)
			if tail > 0 {
				tail--
			}
			prefixBody := set([]byte(`{}`), field, projected[tail:])
			if injected, err := InjectRequest(prefixBody, protocol, notice); err == nil {
				projected = projected[:tail]
				for _, prior := range gjson.GetBytes(injected, field).Array() {
					projected = append(projected, json.RawMessage(prior.Raw))
				}
			}
		}
		if protocol == Responses && strings.HasPrefix(item.Get("id").String(), "msg_gateway_notice_") {
			continue
		}
		projected = append(projected, json.RawMessage(cleaned))
	}
	return set(body, field, projected)
}

func cleanAssistantNotice(body []byte, protocol Protocol) ([]byte, []string) {
	var notices []string
	field, textField := "content", "text"
	if protocol == Gemini {
		field = "parts"
	}
	content := gjson.GetBytes(body, field)
	if content.Type == gjson.String {
		if notice, rest, ok := takeNotice(content.String()); ok {
			notices = append(notices, notice)
			return set(body, field, rest), notices
		}
		return body, nil
	}
	if !content.IsArray() {
		return body, nil
	}
	parts := make([]json.RawMessage, 0, len(content.Array()))
	for _, part := range content.Array() {
		kind := part.Get("type").String()
		if protocol == Gemini && (part.Get("thought").Bool() || part.Get("thoughtSignature").Exists()) {
			parts = append(parts, json.RawMessage(part.Raw))
			continue
		}
		if kind != "text" && kind != "output_text" && (protocol != Gemini || part.Get("text").Type != gjson.String) {
			parts = append(parts, json.RawMessage(part.Raw))
			continue
		}
		if notice, rest, ok := takeNotice(part.Get(textField).String()); ok {
			notices = append(notices, notice)
			if rest != "" {
				parts = append(parts, json.RawMessage(set([]byte(part.Raw), textField, rest)))
			}
		} else {
			parts = append(parts, json.RawMessage(part.Raw))
		}
	}
	if len(notices) == 0 {
		return body, nil
	}
	return set(body, field, parts), notices
}
