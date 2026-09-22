package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
)

type cloudflareJevInputError struct {
	message string
}

func (e *cloudflareJevInputError) Error() string {
	if e == nil || strings.TrimSpace(e.message) == "" {
		return "typesafe/jev requires state and questions"
	}
	return e.message
}

func isCloudflareJevModel(model string) bool {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "typesafe/jev", "@cf/typesafe/jev":
		return true
	default:
		return false
	}
}

func cloudflareJevRunURL(account *Account) (string, error) {
	accountID, err := account.CloudflareAccountID()
	if err != nil {
		return "", err
	}
	return cloudflareOpenAIAPIRoot + accountID + "/ai/run", nil
}

func prepareCloudflareJevUpstream(account *Account, body []byte) (string, []byte, bool, error) {
	if account == nil || !account.IsCloudflareOpenAI() || !gjson.ValidBytes(body) {
		return "", nil, false, nil
	}
	model := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	if !isCloudflareJevModel(model) {
		return "", nil, false, nil
	}
	input, err := cloudflareJevInput(body)
	if err != nil {
		return "", nil, true, err
	}
	runURL, err := cloudflareJevRunURL(account)
	if err != nil {
		return "", nil, true, err
	}
	payload, err := json.Marshal(map[string]any{
		"model": "typesafe/jev",
		"input": json.RawMessage(input),
	})
	if err != nil {
		return "", nil, true, err
	}
	return runURL, payload, true, nil
}

func cloudflareJevInput(body []byte) (json.RawMessage, error) {
	if input := jevInputObject(gjson.GetBytes(body, "input")); len(input) > 0 {
		return input, nil
	}
	messages := gjson.GetBytes(body, "messages")
	if messages.IsArray() {
		var last string
		messages.ForEach(func(_, message gjson.Result) bool {
			if strings.EqualFold(message.Get("role").String(), "user") {
				if text := jevMessageText(message.Get("content")); text != "" {
					last = text
				}
			}
			return true
		})
		if input := jevInputObject(gjson.Parse(last)); len(input) > 0 {
			return input, nil
		}
		if input := jevInputObject(gjson.Parse(last).Get("input")); len(input) > 0 {
			return input, nil
		}
	}
	return nil, &cloudflareJevInputError{message: "typesafe/jev 需要 input.state 和 input.questions。把判断 JSON 放在 input 字段，或放进一条 user 消息。type 用 noul 表示是否，oul 表示多选。"}
}

func jevMessageText(content gjson.Result) string {
	if content.Type == gjson.String {
		return strings.TrimSpace(content.String())
	}
	if !content.IsArray() {
		return strings.TrimSpace(content.Raw)
	}
	var parts []string
	content.ForEach(func(_, part gjson.Result) bool {
		if text := strings.TrimSpace(part.Get("text").String()); text != "" {
			parts = append(parts, text)
		}
		return true
	})
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func jevInputObject(value gjson.Result) json.RawMessage {
	if !value.IsObject() || !value.Get("state").Exists() || !value.Get("questions").IsObject() {
		return nil
	}
	raw := strings.TrimSpace(value.Raw)
	if raw == "" || !gjson.Valid(raw) {
		return nil
	}
	return json.RawMessage(raw)
}

func adaptCloudflareJevResponse(resp *http.Response, wantStream bool, model string) *http.Response {
	if resp == nil || resp.Body == nil || resp.StatusCode >= 400 {
		return resp
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, cloudflareModelsBodyLimit+1))
	_ = resp.Body.Close()
	if err != nil || int64(len(raw)) > cloudflareModelsBodyLimit {
		resp.StatusCode = http.StatusBadGateway
		resp.Body = io.NopCloser(strings.NewReader(`{"error":{"message":"cloudflare jev response was unreadable","type":"api_error"}}`))
		resp.Header.Set("Content-Type", "application/json")
		resp.ContentLength = int64(len(`{"error":{"message":"cloudflare jev response was unreadable","type":"api_error"}}`))
		return resp
	}
	if gjson.GetBytes(raw, "success").Exists() && !gjson.GetBytes(raw, "success").Bool() {
		message := strings.TrimSpace(gjson.GetBytes(raw, "errors.0.message").String())
		if message == "" {
			message = "cloudflare jev request failed"
		}
		payload, _ := json.Marshal(map[string]any{"error": map[string]string{"message": message, "type": "upstream_error"}})
		resp.StatusCode = http.StatusBadGateway
		resp.Body = io.NopCloser(bytes.NewReader(payload))
		resp.Header.Set("Content-Type", "application/json")
		resp.ContentLength = int64(len(payload))
		return resp
	}
	content := jevResultText(raw)
	promptTokens := estimateJevTokens(string(raw))
	completionTokens := estimateJevTokens(content)
	if wantStream {
		body := jevSSE(model, content, promptTokens, completionTokens)
		resp.Body = io.NopCloser(strings.NewReader(body))
		resp.Header.Set("Content-Type", "text/event-stream")
		resp.ContentLength = int64(len(body))
		return resp
	}
	payload, _ := json.Marshal(map[string]any{
		"id":     "chatcmpl-jev",
		"object": "chat.completion",
		"model":  model,
		"choices": []any{map[string]any{
			"index":         0,
			"finish_reason": "stop",
			"message":       map[string]string{"role": "assistant", "content": content},
		}},
		"usage": map[string]int{
			"prompt_tokens":     promptTokens,
			"completion_tokens": completionTokens,
			"total_tokens":      promptTokens + completionTokens,
		},
	})
	resp.Body = io.NopCloser(bytes.NewReader(payload))
	resp.Header.Set("Content-Type", "application/json")
	resp.ContentLength = int64(len(payload))
	return resp
}

func jevResultText(raw []byte) string {
	result := gjson.GetBytes(raw, "result")
	if result.Exists() && result.Raw != "" && result.Raw != "null" {
		return compactJSON(result.Raw)
	}
	return compactJSON(string(raw))
}

func compactJSON(raw string) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(raw)); err != nil {
		return raw
	}
	return buf.String()
}

func estimateJevTokens(text string) int {
	n := (len(strings.TrimSpace(text)) + 3) / 4
	if n < 1 {
		return 1
	}
	return n
}

func jevSSE(model, content string, promptTokens, completionTokens int) string {
	first, _ := json.Marshal(map[string]any{
		"id":     "chatcmpl-jev",
		"object": "chat.completion.chunk",
		"model":  model,
		"choices": []any{map[string]any{
			"index": 0,
			"delta": map[string]string{"role": "assistant", "content": content},
		}},
	})
	last, _ := json.Marshal(map[string]any{
		"id":     "chatcmpl-jev",
		"object": "chat.completion.chunk",
		"model":  model,
		"choices": []any{map[string]any{
			"index":         0,
			"delta":         map[string]any{},
			"finish_reason": "stop",
		}},
		"usage": map[string]int{
			"prompt_tokens":     promptTokens,
			"completion_tokens": completionTokens,
			"total_tokens":      promptTokens + completionTokens,
		},
	})
	return fmt.Sprintf("data: %s\n\ndata: %s\n\ndata: [DONE]\n\n", first, last)
}
