package apicompat

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
)

type chatMessageContent struct {
	Text  *string
	Parts []ChatContentPart
}

// ChatCompletionsToResponses converts a Chat Completions request into a
// Responses API request. The upstream always streams, so Stream is forced to
// true. store is always false and reasoning.encrypted_content is always
// included so that the response translator has full context.
func ChatCompletionsToResponses(req *ChatCompletionsRequest) (*ResponsesRequest, error) {
	return chatCompletionsToResponses(req, false)
}

// ChatCompletionsToAnthropicRequest reuses the Responses conversion chain while
// retaining Anthropic cache extensions. Ordinary Responses upstreams must not
// receive these extensions, so ChatCompletionsToResponses leaves them out.
func ChatCompletionsToAnthropicRequest(req *ChatCompletionsRequest) (*AnthropicRequest, error) {
	responses, err := chatCompletionsToResponses(req, true)
	if err != nil {
		return nil, err
	}
	return ResponsesToAnthropicRequest(responses)
}

func chatCompletionsToResponses(req *ChatCompletionsRequest, anthropic bool) (*ResponsesRequest, error) {
	if err := openai.ValidateGPT61SolReasoningEffort(req.Model, req.ReasoningEffort); err != nil {
		return nil, err
	}
	input, err := convertChatMessagesToResponsesInputWithCache(req.Messages, anthropic)
	if err != nil {
		return nil, err
	}

	inputJSON, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}

	out := &ResponsesRequest{
		CacheControl:       cacheControlForAnthropic(req.CacheControl, anthropic),
		Model:              req.Model,
		Instructions:       req.Instructions,
		Input:              inputJSON,
		Stream:             true, // upstream always streams
		Include:            []string{"reasoning.encrypted_content"},
		ServiceTier:        req.ServiceTier,
		PromptCacheOptions: req.PromptCacheOptions,
		ParallelToolCalls:  req.ParallelToolCalls,
	}

	// Reasoning models (gpt-5.x) do not accept sampling parameters.
	// See isReasoningModel in anthropic_to_responses.go.
	if !isReasoningModel(req.Model) || (openai.IsGPT6SolOrLunaModelSpelling(req.Model) && req.ReasoningEffort == "none") {
		out.Temperature = req.Temperature
		out.TopP = req.TopP
	}

	storeFalse := false
	out.Store = &storeFalse

	// max_tokens / max_completion_tokens → max_output_tokens, prefer max_completion_tokens
	maxTokens := 0
	if req.MaxTokens != nil {
		maxTokens = *req.MaxTokens
	}
	if req.MaxCompletionTokens != nil {
		maxTokens = *req.MaxCompletionTokens
	}
	if maxTokens > 0 {
		v := maxTokens
		if v < minMaxOutputTokens {
			v = minMaxOutputTokens
		}
		out.MaxOutputTokens = &v
	}

	// reasoning_effort → reasoning.effort + reasoning.summary="auto"
	if req.ReasoningEffort != "" {
		out.Reasoning = &ResponsesReasoning{
			Effort:  req.ReasoningEffort,
			Summary: "auto",
		}
	}

	if format := chatResponseFormatToResponsesTextFormat(req.ResponseFormat); len(format) > 0 {
		if out.Text == nil {
			out.Text = &ResponsesText{}
		}
		out.Text.Format = format
	}

	// tools[] and legacy functions[] → ResponsesTool[]
	if len(req.Tools) > 0 || len(req.Functions) > 0 {
		out.Tools = convertChatToolsToResponses(req.Tools, req.Functions, anthropic)
	}

	// Named choices and allowed-tool policies use different wire formats.
	if len(req.ToolChoice) > 0 {
		out.ToolChoice, err = convertChatToolChoiceToResponses(req.ToolChoice)
		if err != nil {
			return nil, fmt.Errorf("convert tool_choice: %w", err)
		}
	} else if len(req.FunctionCall) > 0 {
		tc, err := convertChatFunctionCallToToolChoice(req.FunctionCall)
		if err != nil {
			return nil, fmt.Errorf("convert function_call: %w", err)
		}
		out.ToolChoice = tc
	}

	return out, nil
}

// convertChatMessagesToResponsesInput converts the Chat Completions messages
// array into a Responses API input items array.
func convertChatMessagesToResponsesInput(msgs []ChatMessage) ([]ResponsesInputItem, error) {
	return convertChatMessagesToResponsesInputWithCache(msgs, false)
}

func convertChatMessagesToResponsesInputWithCache(msgs []ChatMessage, anthropic bool) ([]ResponsesInputItem, error) {
	var out []ResponsesInputItem
	legacyIDs := legacyFunctionCallIDs{msgs: msgs}
	for _, m := range msgs {
		switch {
		case m.Role == "assistant" && m.FunctionCall != nil && len(m.ToolCalls) == 0:
			m.ToolCalls = []ChatToolCall{{
				ID:       legacyIDs.assign(m.FunctionCall.Name),
				Type:     "function",
				Function: *m.FunctionCall,
			}}
		case m.Role == "function" && m.ToolCallID == "":
			m.ToolCallID = legacyIDs.claim(m.Name)
		}
		items, err := chatMessageToResponsesItems(m, anthropic)
		if err != nil {
			return nil, err
		}
		// Message-level extensions describe the end of this message, including
		// its tool calls. Do not move a marker from a discarded/empty message.
		if anthropic && m.CacheControl != nil && len(items) > 0 && items[len(items)-1].Type != "reasoning" && items[len(items)-1].CacheControl == nil {
			items[len(items)-1].CacheControl = m.CacheControl
		}
		out = append(out, items...)
	}
	return out, nil
}

// legacyFunctionCallIDs 给没有 call id 的旧式 function 调用和 role=function 结果配对。
// Responses 只按 call_id 配对。已有 tool_calls 的 id 不复用。
type legacyFunctionCallIDs struct {
	msgs    []ChatMessage
	used    map[string]bool
	next    int
	pending map[string][]string
}

func (ids *legacyFunctionCallIDs) assign(name string) string {
	if ids.used == nil {
		ids.used = make(map[string]bool)
		ids.pending = make(map[string][]string)
		for _, m := range ids.msgs {
			for _, tc := range m.ToolCalls {
				ids.used[tc.ID] = true
			}
			if m.ToolCallID != "" {
				ids.used[m.ToolCallID] = true
			}
		}
	}
	var id string
	for {
		ids.next++
		id = fmt.Sprintf("call_legacy_%d", ids.next)
		if !ids.used[id] {
			break
		}
	}
	ids.used[id] = true
	ids.pending[name] = append(ids.pending[name], id)
	return id
}

func (ids *legacyFunctionCallIDs) claim(name string) string {
	queue := ids.pending[name]
	if len(queue) == 0 {
		return ""
	}
	ids.pending[name] = queue[1:]
	return queue[0]
}

// chatMessageToResponsesItems converts a single ChatMessage into one or more
// ResponsesInputItem values.
func chatMessageToResponsesItems(m ChatMessage, anthropic bool) ([]ResponsesInputItem, error) {
	switch m.Role {
	case "system", "developer":
		return chatSystemToResponses(m, anthropic)
	case "user":
		return chatUserToResponses(m, anthropic)
	case "assistant":
		return chatAssistantToResponses(m, anthropic)
	case "tool":
		return chatToolToResponses(m, anthropic)
	case "function":
		return chatFunctionToResponses(m, anthropic)
	default:
		return chatUserToResponses(m, anthropic)
	}
}

// chatSystemToResponses preserves system and developer instruction roles.
func chatSystemToResponses(m ChatMessage, anthropic bool) ([]ResponsesInputItem, error) {
	parsed, err := parseChatMessageContent(m.Content)
	if err != nil {
		return nil, err
	}
	content, err := marshalChatInputContent(parsed, anthropic)
	if err != nil {
		return nil, err
	}
	return []ResponsesInputItem{{Type: "message", Role: m.Role, Content: content}}, nil
}

// chatUserToResponses converts a user message, handling both plain strings and
// multi-modal content arrays.
func chatUserToResponses(m ChatMessage, anthropic bool) ([]ResponsesInputItem, error) {
	parsed, err := parseChatMessageContent(m.Content)
	if err != nil {
		return nil, fmt.Errorf("parse user content: %w", err)
	}
	content, err := marshalChatInputContent(parsed, anthropic)
	if err != nil {
		return nil, err
	}
	return []ResponsesInputItem{{Type: "message", Role: "user", Content: content}}, nil
}

// chatAssistantToResponses converts an assistant message. If there is both
// text content and tool_calls, the text is emitted as an assistant message
// first, then each tool_call becomes a function_call item. If the content is
// empty/nil and there are tool_calls, only function_call items are emitted.
func chatAssistantToResponses(m ChatMessage, anthropic bool) ([]ResponsesInputItem, error) {
	var items []ResponsesInputItem
	text := ""
	thinking := strings.TrimSpace(m.ReasoningContent)

	if len(m.Content) > 0 {
		parsedText, parsedThinking, err := parseAssistantContent(m.Content)
		if err != nil {
			return nil, err
		}
		text = parsedText
		if thinking == "" {
			thinking = strings.TrimSpace(parsedThinking)
		}
	}

	if thinking != "" {
		reasoningItem := ResponsesInputItem{
			Type: "reasoning",
			Summary: []ResponsesSummary{{
				Type: "summary_text",
				Text: thinking,
			}},
		}
		if content, ok := responsesReasoningContentJSON(thinking); ok {
			reasoningItem.Content = content
		}
		items = append(items, reasoningItem)
	}

	if text != "" {
		parts := []ResponsesContentPart{{Type: "output_text", Text: text}}
		if anthropic {
			var original []ChatContentPart
			if json.Unmarshal(m.Content, &original) == nil {
				parts = nil
				for _, part := range original {
					if part.Type != "thinking" && part.Type != "reasoning" && part.Text != "" {
						parts = append(parts, ResponsesContentPart{Type: "output_text", Text: part.Text, CacheControl: part.CacheControl})
					}
				}
			}
		}
		partsJSON, err := json.Marshal(parts)
		if err != nil {
			return nil, err
		}
		items = append(items, ResponsesInputItem{Type: "message", Role: "assistant", Content: partsJSON})
	}

	// Emit one function_call item per tool_call.
	for _, tc := range m.ToolCalls {
		args := tc.Function.Arguments
		if args == "" {
			args = "{}"
		}
		items = append(items, ResponsesInputItem{
			CacheControl: cacheControlForAnthropic(tc.CacheControl, anthropic),
			Type:         "function_call",
			CallID:       tc.ID,
			Name:         tc.Function.Name,
			Arguments:    args,
		})
	}

	return items, nil
}

// parseAssistantContent splits assistant content into visible text and thinking.
// Thinking stays off the message body.
func parseAssistantContent(raw json.RawMessage) (string, string, error) {
	if len(raw) == 0 {
		return "", "", nil
	}

	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, "", nil
	}

	var parts []map[string]any
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", "", nil
	}

	var text strings.Builder
	var thinking strings.Builder
	for _, part := range parts {
		typ, _ := part["type"].(string)
		partText, _ := part["text"].(string)
		partThinking, _ := part["thinking"].(string)
		switch typ {
		case "thinking", "reasoning":
			chunk := partThinking
			if chunk == "" {
				chunk = partText
			}
			if chunk == "" {
				continue
			}
			if thinking.Len() > 0 {
				_, _ = thinking.WriteString("\n")
			}
			_, _ = thinking.WriteString(chunk)
		default:
			if partText != "" {
				_, _ = text.WriteString(partText)
			}
		}
	}
	return text.String(), thinking.String(), nil
}

// chatToolToResponses converts a tool result message (role=tool) into a
// function_call_output item.
func chatToolToResponses(m ChatMessage, anthropic bool) ([]ResponsesInputItem, error) {
	output, err := parseChatContent(m.Content)
	if err != nil {
		return nil, err
	}
	if output == "" {
		output = "(empty)"
	}
	item := ResponsesInputItem{
		Type:   "function_call_output",
		CallID: m.ToolCallID,
		Output: output,
	}
	if anthropic {
		var parts []ChatContentPart
		if json.Unmarshal(m.Content, &parts) == nil {
			item.outputRaw, err = json.Marshal(convertChatContentPartsToResponses(parts, true))
			if err != nil {
				return nil, err
			}
		}
	}
	return []ResponsesInputItem{item}, nil
}

// chatFunctionToResponses converts a legacy function result message
// (role=function) into a function_call_output item. The Name field is used as
// call_id since legacy function calls do not carry a separate call_id.
func chatFunctionToResponses(m ChatMessage, anthropic bool) ([]ResponsesInputItem, error) {
	if m.ToolCallID == "" {
		m.ToolCallID = m.Name
	}
	return chatToolToResponses(m, anthropic)
}

// parseChatContent returns the string value of a ChatMessage Content field.
// Content can be a JSON string or an array of typed parts. Array content is
// flattened to text by concatenating text parts and ignoring non-text parts.
func parseChatContent(raw json.RawMessage) (string, error) {
	parsed, err := parseChatMessageContent(raw)
	if err != nil {
		return "", err
	}
	if parsed.Text != nil {
		return *parsed.Text, nil
	}
	return flattenChatContentParts(parsed.Parts), nil
}

func parseChatMessageContent(raw json.RawMessage) (chatMessageContent, error) {
	if len(raw) == 0 {
		return chatMessageContent{Text: stringPtr("")}, nil
	}

	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return chatMessageContent{Text: &s}, nil
	}

	var parts []ChatContentPart
	if err := json.Unmarshal(raw, &parts); err == nil {
		return chatMessageContent{Parts: parts}, nil
	}

	return chatMessageContent{}, fmt.Errorf("parse content as string or parts array")
}

func marshalChatInputContent(content chatMessageContent, anthropic bool) (json.RawMessage, error) {
	if content.Text != nil {
		return json.Marshal(*content.Text)
	}
	parts := convertChatContentPartsToResponses(content.Parts, anthropic)
	if len(parts) == 0 {
		// A nil slice marshals to JSON null, which the upstream Responses API
		// rejects ("expected an array of objects or string, but got null").
		// Fall back to an empty string when no usable parts remain.
		return json.Marshal("")
	}
	return json.Marshal(parts)
}

func convertChatContentPartsToResponses(parts []ChatContentPart, anthropic bool) []ResponsesContentPart {
	var responseParts []ResponsesContentPart
	for _, p := range parts {
		switch p.Type {
		case "text":
			if p.Text != "" {
				responseParts = append(responseParts, ResponsesContentPart{
					CacheControl:          cacheControlForAnthropic(p.CacheControl, anthropic),
					PromptCacheBreakpoint: p.PromptCacheBreakpoint,
					Type:                  "input_text",
					Text:                  p.Text,
				})
			}
		case "image_url":
			if p.ImageURL != nil && p.ImageURL.URL != "" && !isEmptyBase64DataURI(p.ImageURL.URL) {
				responseParts = append(responseParts, ResponsesContentPart{
					CacheControl:          cacheControlForAnthropic(p.CacheControl, anthropic),
					PromptCacheBreakpoint: p.PromptCacheBreakpoint,
					Type:                  "input_image",
					ImageURL:              p.ImageURL.URL,
				})
			}
		case "file":
			if p.File != nil && (p.File.FileData != "" || p.File.FileID != "") {
				responseParts = append(responseParts, ResponsesContentPart{
					CacheControl:          cacheControlForAnthropic(p.CacheControl, anthropic),
					PromptCacheBreakpoint: p.PromptCacheBreakpoint,
					Type:                  "input_file",
					Filename:              p.File.Filename,
					FileData:              p.File.FileData,
					FileID:                p.File.FileID,
				})
			}
		}
	}
	return responseParts
}

func isEmptyBase64DataURI(raw string) bool {
	if !strings.HasPrefix(raw, "data:") {
		return false
	}
	rest := strings.TrimPrefix(raw, "data:")
	semicolonIdx := strings.Index(rest, ";")
	if semicolonIdx < 0 {
		return false
	}
	rest = rest[semicolonIdx+1:]
	if !strings.HasPrefix(rest, "base64,") {
		return false
	}
	return strings.TrimSpace(strings.TrimPrefix(rest, "base64,")) == ""
}

func flattenChatContentParts(parts []ChatContentPart) string {
	var textParts []string
	for _, p := range parts {
		if p.Type == "text" && p.Text != "" {
			textParts = append(textParts, p.Text)
		}
	}
	return strings.Join(textParts, "")
}

func stringPtr(s string) *string {
	return &s
}

// convertChatToolsToResponses maps Chat Completions tool definitions and legacy
// function definitions to Responses API tool definitions.
func convertChatToolsToResponses(tools []ChatTool, functions []ChatFunction, anthropic bool) []ResponsesTool {
	var out []ResponsesTool

	for _, t := range tools {
		toolType := strings.ToLower(strings.TrimSpace(t.Type))
		if toolType == "x_search" {
			out = append(out, ResponsesTool{
				CacheControl:             cacheControlForAnthropic(t.CacheControl, anthropic),
				Type:                     "x_search",
				AllowedXHandles:          t.AllowedXHandles,
				ExcludedXHandles:         t.ExcludedXHandles,
				FromDate:                 t.FromDate,
				ToDate:                   t.ToDate,
				EnableImageUnderstanding: t.EnableImageUnderstanding,
				EnableVideoUnderstanding: t.EnableVideoUnderstanding,
			})
			continue
		}
		if toolType == "web_search" || toolType == "code_execution" {
			out = append(out, ResponsesTool{Type: toolType, CacheControl: cacheControlForAnthropic(t.CacheControl, anthropic)})
			continue
		}
		if t.Type != "function" || t.Function == nil {
			continue
		}
		rt := ResponsesTool{
			CacheControl: cacheControlForAnthropic(t.CacheControl, anthropic),
			Type:         "function",
			Name:         t.Function.Name,
			Description:  t.Function.Description,
			Parameters:   t.Function.Parameters,
			Strict:       defaultStrictFalse(t.Function.Strict),
		}
		if anthropic && rt.CacheControl == nil {
			rt.CacheControl = t.Function.CacheControl
		}
		out = append(out, rt)
	}

	// Legacy functions[] are treated as function-type tools.
	for _, f := range functions {
		rt := ResponsesTool{
			CacheControl: cacheControlForAnthropic(f.CacheControl, anthropic),
			Type:         "function",
			Name:         f.Name,
			Description:  f.Description,
			Parameters:   f.Parameters,
			Strict:       defaultStrictFalse(f.Strict),
		}
		out = append(out, rt)
	}

	return out
}

func cacheControlForAnthropic(cc *AnthropicCacheControl, anthropic bool) *AnthropicCacheControl {
	if anthropic {
		return cc
	}
	return nil
}

func convertChatToolChoiceToResponses(raw json.RawMessage) (json.RawMessage, error) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	choice, ok := value.(map[string]any)
	if !ok {
		return raw, nil
	}
	flattenFunction := func(tool map[string]any) {
		if tool["type"] != "function" {
			return
		}
		if function, ok := tool["function"].(map[string]any); ok {
			if _, exists := tool["name"]; !exists {
				tool["name"] = function["name"]
			}
			delete(tool, "function")
		}
	}
	flattenFunction(choice)
	if choice["type"] == "allowed_tools" {
		if allowed, ok := choice["allowed_tools"].(map[string]any); ok {
			for _, key := range []string{"mode", "tools"} {
				if field, exists := allowed[key]; exists {
					choice[key] = field
				}
			}
			delete(choice, "allowed_tools")
		}
		if allowed, ok := choice["tools"].([]any); ok {
			for _, tool := range allowed {
				if tool, ok := tool.(map[string]any); ok {
					flattenFunction(tool)
				}
			}
		}
	}
	return json.Marshal(choice)
}

func defaultStrictFalse(src *bool) *bool {
	if src == nil {
		value := false
		return &value
	}
	return src
}

// convertChatFunctionCallToToolChoice maps the legacy function_call field to a
// Responses API tool_choice value.
//
//	"auto" → "auto"
//	"none" → "none"
//	{"name":"X"} → {"type":"function","name":"X"}
func convertChatFunctionCallToToolChoice(raw json.RawMessage) (json.RawMessage, error) {
	// Try string first ("auto", "none", etc.) — pass through as-is.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return json.Marshal(s)
	}

	// Object form: {"name":"X"}
	var obj struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"type": "function",
		"name": obj.Name,
	})
}
