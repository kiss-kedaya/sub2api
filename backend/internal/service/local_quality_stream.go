package service

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"

	"github.com/gin-gonic/gin"
)

const localQualityStrictKey = "sub2api.local_quality_strict_completion"
const localQualityTerminalKey = "sub2api.local_quality_protocol_terminal"

// This strict parser is exclusive to optional quality samples and their judges.
// Ordinary connectivity tests retain their existing compatibility behavior.
func (s *AccountTestService) processLocalQualityStream(c *gin.Context, body io.Reader, protocol string) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 8192), 1<<20)
	stopReason := ""
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		raw := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if raw == "[DONE]" {
			break
		}
		var event struct {
			Type     string          `json:"type"`
			Error    json.RawMessage `json:"error"`
			Delta    json.RawMessage `json:"delta"`
			Response json.RawMessage `json:"response"`
			Choices  []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				Finish *string `json:"finish_reason"`
			} `json:"choices"`
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text    string `json:"text"`
						Thought bool   `json:"thought"`
					} `json:"parts"`
				} `json:"content"`
				Finish string `json:"finishReason"`
			} `json:"candidates"`
		}
		if json.Unmarshal([]byte(raw), &event) != nil {
			return s.sendErrorAndEnd(c, "Quality sample contained invalid JSON")
		}
		if event.Type == "error" || event.Type == "response.failed" || event.Type == "response.incomplete" || len(event.Error) > 0 && string(event.Error) != "null" {
			return s.sendErrorAndEnd(c, "Quality sample failed")
		}
		complete := false
		switch protocol {
		case "responses":
			if event.Type == "response.output_text.delta" {
				var text string
				if json.Unmarshal(event.Delta, &text) == nil && text != "" {
					s.sendEvent(c, TestEvent{Type: "content", Text: text})
				}
			}
			if event.Type == "response.completed" || event.Type == "response.done" {
				var response struct {
					Status string `json:"status"`
				}
				if json.Unmarshal(event.Response, &response) != nil || response.Status != "completed" {
					return s.sendErrorAndEnd(c, "Quality sample was incomplete")
				}
				complete = true
			}
		case "chat":
			for _, choice := range event.Choices {
				if choice.Delta.Content != "" {
					s.sendEvent(c, TestEvent{Type: "content", Text: choice.Delta.Content})
				}
				if choice.Finish != nil && *choice.Finish != "" {
					if *choice.Finish != "stop" {
						return s.sendErrorAndEnd(c, "Quality sample was incomplete")
					}
					complete = true
				}
			}
		case "anthropic":
			var delta struct {
				Text       string `json:"text"`
				StopReason string `json:"stop_reason"`
			}
			_ = json.Unmarshal(event.Delta, &delta)
			if event.Type == "content_block_delta" && delta.Text != "" {
				s.sendEvent(c, TestEvent{Type: "content", Text: delta.Text})
			}
			if event.Type == "message_delta" {
				stopReason = delta.StopReason
			}
			if event.Type == "message_stop" {
				if stopReason != "end_turn" && stopReason != "stop_sequence" {
					return s.sendErrorAndEnd(c, "Quality sample was incomplete")
				}
				complete = true
			}
		case "gemini":
			if len(event.Response) > 0 {
				var nested struct {
					Candidates []struct {
						Content struct {
							Parts []struct {
								Text    string `json:"text"`
								Thought bool   `json:"thought"`
							} `json:"parts"`
						} `json:"content"`
						Finish string `json:"finishReason"`
					} `json:"candidates"`
				}
				if json.Unmarshal(event.Response, &nested) == nil {
					event.Candidates = nested.Candidates
				}
			}
			for _, candidate := range event.Candidates {
				for _, part := range candidate.Content.Parts {
					if part.Text != "" && !part.Thought {
						s.sendEvent(c, TestEvent{Type: "content", Text: part.Text})
					}
				}
				if candidate.Finish != "" {
					if candidate.Finish != "STOP" {
						return s.sendErrorAndEnd(c, "Quality sample was incomplete")
					}
					complete = true
				}
			}
		}
		if complete {
			c.Set(localQualityTerminalKey, true)
			s.sendEvent(c, TestEvent{Type: "test_complete", Success: true})
			return nil
		}
	}
	return s.sendErrorAndEnd(c, "Quality sample ended before a successful protocol terminal")
}
