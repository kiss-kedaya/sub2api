package apicompat

import (
	"bytes"
	"encoding/json"
	"strings"
)

// responsesReasoningContentText returns provider-supplied chain-of-thought.
// summary_text is a display summary and must never be treated as replayable
// reasoning content.
func responsesReasoningContentText(parts []ResponsesContentPart) string {
	var out []string
	for _, part := range parts {
		if part.Type == "reasoning_text" && part.Text != "" {
			out = append(out, part.Text)
		}
	}
	return strings.Join(out, "\n")
}

func responsesReasoningSummaryText(summary []ResponsesSummary) string {
	var out []string
	for _, part := range summary {
		if part.Type == "summary_text" && part.Text != "" {
			out = append(out, part.Text)
		}
	}
	return strings.Join(out, "\n")
}

// responsesReasoningText prefers genuine reasoning_text content and falls back
// to summary only when the provider did not supply content.
func responsesReasoningText(content []ResponsesContentPart, summary []ResponsesSummary) string {
	if text := responsesReasoningContentText(content); text != "" {
		return text
	}
	return responsesReasoningSummaryText(summary)
}

func responsesInputReasoningContentText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	var parts []ResponsesContentPart
	if err := json.Unmarshal(raw, &parts); err == nil {
		return responsesReasoningContentText(parts)
	}
	var part ResponsesContentPart
	if err := json.Unmarshal(raw, &part); err == nil {
		if part.Type == "reasoning_text" {
			return part.Text
		}
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	return ""
}

func responsesReasoningContentJSON(text string) (json.RawMessage, bool) {
	if text == "" {
		return nil, false
	}
	raw, err := json.Marshal([]ResponsesContentPart{{Type: "reasoning_text", Text: text}})
	if err != nil {
		return nil, false
	}
	return raw, true
}
