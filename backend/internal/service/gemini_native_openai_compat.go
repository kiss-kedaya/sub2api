package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

func (s *GeminiMessagesCompatService) forwardGeminiNativeViaOpenAICompat(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	originalModel string,
	action string,
	stream bool,
	body []byte,
) (*ForwardResult, error) {
	if s == nil || s.httpUpstream == nil {
		return nil, s.writeGoogleError(c, http.StatusBadGateway, "OpenAI-compatible Gemini upstream is not configured")
	}
	apiKey := strings.TrimSpace(account.GetCredential("api_key"))
	if apiKey == "" {
		return nil, s.writeGoogleError(c, http.StatusBadGateway, "gemini api_key not configured")
	}
	baseURL := strings.TrimSpace(account.GetCNProtocolBaseURL(APIProtocolChatCompletions))
	if baseURL == "" {
		baseURL = strings.TrimSpace(account.GetCredential("base_url"))
	}
	if baseURL == "" {
		return nil, s.writeGoogleError(c, http.StatusBadGateway, "OpenAI-compatible Gemini base_url is not configured")
	}
	mappedModel := account.GetMappedModel(originalModel)
	if action == "countTokens" {
		return s.writeGeminiCountTokensFromBody(c, mappedModel, body)
	}

	chatBody, err := geminiNativeRequestToChatCompletions(mappedModel, body, stream)
	if err != nil {
		return nil, s.writeGoogleError(c, http.StatusBadRequest, err.Error())
	}
	fullURL := strings.TrimRight(baseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bytes.NewReader(chatBody))
	if err != nil {
		return nil, s.writeGoogleError(c, http.StatusBadGateway, err.Error())
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	resp, err := s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		return nil, s.writeGoogleError(c, http.StatusBadGateway, err.Error())
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		return nil, s.writeGoogleError(c, resp.StatusCode, strings.TrimSpace(string(errBody)))
	}

	if stream {
		return s.pipeOpenAIChatStreamAsGemini(c, resp.Body, mappedModel)
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	geminiBody, usage := chatCompletionsToGeminiNative(raw, mappedModel)
	c.Data(http.StatusOK, "application/json", geminiBody)
	return &ForwardResult{
		Model:         mappedModel,
		UpstreamModel: mappedModel,
		Usage:         usage,
	}, nil
}

func (s *GeminiMessagesCompatService) writeGeminiCountTokensFromBody(c *gin.Context, model string, body []byte) (*ForwardResult, error) {
	text := strings.TrimSpace(gjson.GetBytes(body, "contents").Raw)
	if text == "" {
		text = string(body)
	}
	count := len([]rune(text)) / 4
	if count < 1 {
		count = 1
	}
	payload, _ := json.Marshal(map[string]any{
		"totalTokens": count,
		"model":       model,
	})
	c.Data(http.StatusOK, "application/json", payload)
	return &ForwardResult{Model: model, UpstreamModel: model}, nil
}

func geminiNativeRequestToChatCompletions(model string, body []byte, stream bool) ([]byte, error) {
	contents := gjson.GetBytes(body, "contents")
	if !contents.IsArray() {
		return nil, fmt.Errorf("missing contents")
	}
	msgs := make([]map[string]any, 0)
	for _, content := range contents.Array() {
		role := strings.ToLower(strings.TrimSpace(content.Get("role").String()))
		switch role {
		case "model":
			role = "assistant"
		case "user", "system":
		default:
			role = "user"
		}
		var parts []string
		for _, part := range content.Get("parts").Array() {
			if text := strings.TrimSpace(part.Get("text").String()); text != "" {
				parts = append(parts, text)
			}
		}
		if len(parts) == 0 {
			continue
		}
		msgs = append(msgs, map[string]any{"role": role, "content": strings.Join(parts, "\n")})
	}
	if sys := strings.TrimSpace(gjson.GetBytes(body, "systemInstruction.parts.0.text").String()); sys != "" {
		msgs = append([]map[string]any{{"role": "system", "content": sys}}, msgs...)
	}
	if len(msgs) == 0 {
		return nil, fmt.Errorf("empty Gemini contents")
	}
	out := map[string]any{
		"model":    model,
		"messages": msgs,
		"stream":   stream,
	}
	if temp := gjson.GetBytes(body, "generationConfig.temperature"); temp.Exists() {
		out["temperature"] = temp.Value()
	}
	if maxTok := gjson.GetBytes(body, "generationConfig.maxOutputTokens"); maxTok.Exists() {
		out["max_tokens"] = maxTok.Value()
	}
	return json.Marshal(out)
}

func chatCompletionsToGeminiNative(raw []byte, model string) ([]byte, ClaudeUsage) {
	text := gjson.GetBytes(raw, "choices.0.message.content").String()
	finish := gjson.GetBytes(raw, "choices.0.finish_reason").String()
	geminiFinish := "STOP"
	if finish == "length" {
		geminiFinish = "MAX_TOKENS"
	}
	prompt := int(gjson.GetBytes(raw, "usage.prompt_tokens").Int())
	completion := int(gjson.GetBytes(raw, "usage.completion_tokens").Int())
	payload, _ := json.Marshal(map[string]any{
		"candidates": []map[string]any{
			{
				"content": map[string]any{
					"role":  "model",
					"parts": []map[string]string{{"text": text}},
				},
				"finishReason": geminiFinish,
			},
		},
		"usageMetadata": map[string]any{
			"promptTokenCount":     prompt,
			"candidatesTokenCount": completion,
			"totalTokenCount":      prompt + completion,
		},
		"modelVersion": model,
	})
	return payload, ClaudeUsage{InputTokens: prompt, OutputTokens: completion}
}

func (s *GeminiMessagesCompatService) pipeOpenAIChatStreamAsGemini(c *gin.Context, body io.Reader, model string) (*ForwardResult, error) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Status(http.StatusOK)
	flusher, _ := c.Writer.(http.Flusher)
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	usage := ClaudeUsage{}
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		delta := gjson.Get(data, "choices.0.delta.content").String()
		if delta != "" {
			chunk, _ := json.Marshal(map[string]any{
				"candidates": []map[string]any{
					{"content": map[string]any{"role": "model", "parts": []map[string]string{{"text": delta}}}},
				},
				"modelVersion": model,
			})
			_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", chunk)
			if flusher != nil {
				flusher.Flush()
			}
		}
		if gjson.Get(data, "usage").Exists() {
			usage.InputTokens = int(gjson.Get(data, "usage.prompt_tokens").Int())
			usage.OutputTokens = int(gjson.Get(data, "usage.completion_tokens").Int())
		}
	}
	done, _ := json.Marshal(map[string]any{
		"candidates": []map[string]any{
			{
				"content":      map[string]any{"role": "model", "parts": []map[string]string{{"text": ""}}},
				"finishReason": "STOP",
			},
		},
		"modelVersion": model,
	})
	_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", done)
	if flusher != nil {
		flusher.Flush()
	}
	return &ForwardResult{Model: model, UpstreamModel: model, Usage: usage}, nil
}
