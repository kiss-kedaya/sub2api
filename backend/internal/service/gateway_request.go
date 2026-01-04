package service

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// ParsedRequest 保存网关请求的预解析结果
//
// 性能优化说明：
// 原实现在多个位置重复解析请求体（Handler、Service 各解析一次）：
// 1. gateway_handler.go 解析获取 model 和 stream
// 2. gateway_service.go 再次解析获取 system、messages、metadata
// 3. GenerateSessionHash 又一次解析获取会话哈希所需字段
//
// 新实现一次解析，多处复用：
// 1. 在 Handler 层统一调用 ParseGatewayRequest 一次性解析
// 2. 将解析结果 ParsedRequest 传递给 Service 层
// 3. 避免重复 json.Unmarshal，减少 CPU 和内存开销
type ParsedRequest struct {
	Body           []byte // 原始请求体（保留用于转发）
	Model          string // 请求的模型名称
	Stream         bool   // 是否为流式请求
	MetadataUserID string // metadata.user_id（用于会话亲和）
	System         any    // system 字段内容
	Messages       []any  // messages 数组
	HasSystem      bool   // 是否包含 system 字段（包含 null 也视为显式传入）
}

// ParseGatewayRequest 解析网关请求体并返回结构化结果
// 性能优化：一次解析提取所有需要的字段，避免重复 Unmarshal
func ParseGatewayRequest(body []byte) (*ParsedRequest, error) {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}

	parsed := &ParsedRequest{
		Body: body,
	}

	if rawModel, exists := req["model"]; exists {
		model, ok := rawModel.(string)
		if !ok {
			return nil, fmt.Errorf("invalid model field type")
		}
		parsed.Model = model
	}
	if rawStream, exists := req["stream"]; exists {
		stream, ok := rawStream.(bool)
		if !ok {
			return nil, fmt.Errorf("invalid stream field type")
		}
		parsed.Stream = stream
	}
	if metadata, ok := req["metadata"].(map[string]any); ok {
		if userID, ok := metadata["user_id"].(string); ok {
			parsed.MetadataUserID = userID
		}
	}
	// system 字段只要存在就视为显式提供（即使为 null），
	// 以避免客户端传 null 时被默认 system 误注入。
	if system, ok := req["system"]; ok {
		parsed.HasSystem = true
		parsed.System = system
	}
	if messages, ok := req["messages"].([]any); ok {
		parsed.Messages = messages
	}

	return parsed, nil
}

// FilterThinkingBlocks removes thinking blocks from request body
// Returns filtered body or original body if filtering fails (fail-safe)
// This prevents 400 errors from invalid thinking block signatures
//
// Strategy:
//   - When thinking.type != "enabled": Remove all thinking blocks
//   - When thinking.type == "enabled": Only remove thinking blocks without valid signatures
//     (blocks with missing/empty/dummy signatures that would cause 400 errors)
func FilterThinkingBlocks(body []byte) []byte {
	return filterThinkingBlocksInternal(body, false)
}

// FilterThinkingBlocksForRetry removes thinking blocks from HISTORICAL messages for retry scenarios.
// This is used when upstream returns signature-related 400 errors.
//
// Key insight:
//   - User's thinking.type = "enabled" should be PRESERVED (user's intent)
//   - Only HISTORICAL assistant messages have thinking blocks with signatures
//   - These signatures may be invalid when switching accounts/platforms
//   - New responses will generate fresh thinking blocks without signature issues
//
// Strategy:
//   - Keep thinking.type = "enabled" (preserve user intent)
//   - Remove thinking/redacted_thinking blocks from historical assistant messages
//   - Ensure no message has empty content after filtering
func FilterThinkingBlocksForRetry(body []byte) []byte {
	// Fast path: check for presence of thinking-related keys in messages
	if !bytes.Contains(body, []byte(`"type":"thinking"`)) &&
		!bytes.Contains(body, []byte(`"type": "thinking"`)) &&
		!bytes.Contains(body, []byte(`"type":"redacted_thinking"`)) &&
		!bytes.Contains(body, []byte(`"type": "redacted_thinking"`)) {
		return body
	}

	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return body
	}

	// DO NOT modify thinking.type - preserve user's intent to use thinking mode
	// The issue is with historical message signatures, not the thinking mode itself

	messages, ok := req["messages"].([]any)
	if !ok {
		return body
	}

	modified := false
	newMessages := make([]any, 0, len(messages))

	for _, msg := range messages {
		msgMap, ok := msg.(map[string]any)
		if !ok {
			newMessages = append(newMessages, msg)
			continue
		}

		role, _ := msgMap["role"].(string)
		content, ok := msgMap["content"].([]any)
		if !ok {
			// String content or other format - keep as is
			newMessages = append(newMessages, msg)
			continue
		}

		newContent := make([]any, 0, len(content))
		modifiedThisMsg := false

		for _, block := range content {
			blockMap, ok := block.(map[string]any)
			if !ok {
				newContent = append(newContent, block)
				continue
			}

			blockType, _ := blockMap["type"].(string)

			// Remove thinking/redacted_thinking blocks from historical messages
			// These have signatures that may be invalid across different accounts
			if blockType == "thinking" || blockType == "redacted_thinking" {
				modifiedThisMsg = true
				continue
			}

			newContent = append(newContent, block)
		}

		if modifiedThisMsg {
			modified = true
			// Handle empty content after filtering
			if len(newContent) == 0 {
				// For assistant messages, skip entirely (remove from conversation)
				// For user messages, add placeholder to avoid empty content error
				if role == "user" {
					newContent = append(newContent, map[string]any{
						"type": "text",
						"text": "(content removed)",
					})
					msgMap["content"] = newContent
					newMessages = append(newMessages, msgMap)
				}
				// Skip assistant messages with empty content (don't append)
				continue
			}
			msgMap["content"] = newContent
		}
		newMessages = append(newMessages, msgMap)
	}

	if modified {
		req["messages"] = newMessages
	}

	newBody, err := json.Marshal(req)
	if err != nil {
		return body
	}
	return newBody
}

// filterThinkingBlocksInternal removes invalid thinking blocks from request
// Strategy:
//   - When thinking.type != "enabled": Remove all thinking blocks
//   - When thinking.type == "enabled": Only remove thinking blocks without valid signatures
func filterThinkingBlocksInternal(body []byte, _ bool) []byte {
	// Fast path: if body doesn't contain "thinking", skip parsing
	if !bytes.Contains(body, []byte(`"type":"thinking"`)) &&
		!bytes.Contains(body, []byte(`"type": "thinking"`)) &&
		!bytes.Contains(body, []byte(`"type":"redacted_thinking"`)) &&
		!bytes.Contains(body, []byte(`"type": "redacted_thinking"`)) &&
		!bytes.Contains(body, []byte(`"thinking":`)) &&
		!bytes.Contains(body, []byte(`"thinking" :`)) {
		return body
	}

	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return body
	}

	// Check if thinking is enabled
	thinkingEnabled := false
	if thinking, ok := req["thinking"].(map[string]any); ok {
		if thinkType, ok := thinking["type"].(string); ok && thinkType == "enabled" {
			thinkingEnabled = true
		}
	}

	messages, ok := req["messages"].([]any)
	if !ok {
		return body
	}

	filtered := false
	for _, msg := range messages {
		msgMap, ok := msg.(map[string]any)
		if !ok {
			continue
		}

		role, _ := msgMap["role"].(string)
		content, ok := msgMap["content"].([]any)
		if !ok {
			continue
		}

		newContent := make([]any, 0, len(content))
		filteredThisMessage := false

		for _, block := range content {
			blockMap, ok := block.(map[string]any)
			if !ok {
				newContent = append(newContent, block)
				continue
			}

			blockType, _ := blockMap["type"].(string)

			if blockType == "thinking" || blockType == "redacted_thinking" {
				// When thinking is enabled and this is an assistant message,
				// only keep thinking blocks with valid signatures
				if thinkingEnabled && role == "assistant" {
					signature, _ := blockMap["signature"].(string)
					if signature != "" && signature != "skip_thought_signature_validator" {
						newContent = append(newContent, block)
						continue
					}
				}
				filtered = true
				filteredThisMessage = true
				continue
			}

			// Handle blocks without type discriminator but with "thinking" key
			if blockType == "" {
				if _, hasThinking := blockMap["thinking"]; hasThinking {
					filtered = true
					filteredThisMessage = true
					continue
				}
			}

			newContent = append(newContent, block)
		}

		if filteredThisMessage {
			msgMap["content"] = newContent
		}
	}

	if !filtered {
		return body
	}

	newBody, err := json.Marshal(req)
	if err != nil {
		return body
	}
	return newBody
}
