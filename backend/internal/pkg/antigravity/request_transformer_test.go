package antigravity

import (
	"encoding/json"
	"testing"
)

// TestBuildParts_ThinkingBlockWithoutSignature 测试thinking block无signature时的处理
func TestBuildParts_ThinkingBlockWithoutSignature(t *testing.T) {
	tests := []struct {
		name              string
		content           string
		allowDummyThought bool
		expectedParts     int
		description       string
	}{
		{
			name: "Claude model - skip thinking block without signature",
			content: `[
				{"type": "text", "text": "Hello"},
				{"type": "thinking", "thinking": "Let me think...", "signature": ""},
				{"type": "text", "text": "World"}
			]`,
			allowDummyThought: false,
			expectedParts:     2, // 只有两个text block
			description:       "Claude模型应该跳过无signature的thinking block",
		},
		{
			name: "Claude model - keep thinking block with signature",
			content: `[
				{"type": "text", "text": "Hello"},
				{"type": "thinking", "thinking": "Let me think...", "signature": "valid_sig"},
				{"type": "text", "text": "World"}
			]`,
			allowDummyThought: false,
			expectedParts:     3, // 三个block都保留
			description:       "Claude模型应该保留有signature的thinking block",
		},
		{
			name: "Gemini model - use dummy signature",
			content: `[
				{"type": "text", "text": "Hello"},
				{"type": "thinking", "thinking": "Let me think...", "signature": ""},
				{"type": "text", "text": "World"}
			]`,
			allowDummyThought: true,
			expectedParts:     3, // 三个block都保留，thinking使用dummy signature
			description:       "Gemini模型应该为无signature的thinking block使用dummy signature",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toolIDToName := make(map[string]string)
			parts, err := buildParts(json.RawMessage(tt.content), toolIDToName, tt.allowDummyThought)

			if err != nil {
				t.Fatalf("buildParts() error = %v", err)
			}

			if len(parts) != tt.expectedParts {
				t.Errorf("%s: got %d parts, want %d parts", tt.description, len(parts), tt.expectedParts)
			}
		})
	}
}

// TestBuildTools_CustomTypeTools 测试custom类型工具转换
func TestBuildTools_CustomTypeTools(t *testing.T) {
	tests := []struct {
		name        string
		tools       []ClaudeTool
		expectedLen int
		description string
	}{
		{
			name: "Standard tool format",
			tools: []ClaudeTool{
				{
					Name:        "get_weather",
					Description: "Get weather information",
					InputSchema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"location": map[string]any{"type": "string"},
						},
					},
				},
			},
			expectedLen: 1,
			description: "标准工具格式应该正常转换",
		},
		{
			name: "Custom type tool (MCP format)",
			tools: []ClaudeTool{
				{
					Type: "custom",
					Name: "mcp_tool",
					Custom: &ClaudeCustomToolSpec{
						Description: "MCP tool description",
						InputSchema: map[string]any{
							"type": "object",
							"properties": map[string]any{
								"param": map[string]any{"type": "string"},
							},
						},
					},
				},
			},
			expectedLen: 1,
			description: "Custom类型工具应该从Custom字段读取description和input_schema",
		},
		{
			name: "Mixed standard and custom tools",
			tools: []ClaudeTool{
				{
					Name:        "standard_tool",
					Description: "Standard tool",
					InputSchema: map[string]any{"type": "object"},
				},
				{
					Type: "custom",
					Name: "custom_tool",
					Custom: &ClaudeCustomToolSpec{
						Description: "Custom tool",
						InputSchema: map[string]any{"type": "object"},
					},
				},
			},
			expectedLen: 1, // 返回一个GeminiToolDeclaration，包含2个function declarations
			description: "混合标准和custom工具应该都能正确转换",
		},
		{
			name: "Invalid custom tool - nil Custom field",
			tools: []ClaudeTool{
				{
					Type: "custom",
					Name: "invalid_custom",
					// Custom 为 nil
				},
			},
			expectedLen: 0, // 应该被跳过
			description: "Custom字段为nil的custom工具应该被跳过",
		},
		{
			name: "Invalid custom tool - nil InputSchema",
			tools: []ClaudeTool{
				{
					Type: "custom",
					Name: "invalid_custom",
					Custom: &ClaudeCustomToolSpec{
						Description: "Invalid",
						// InputSchema 为 nil
					},
				},
			},
			expectedLen: 0, // 应该被跳过
			description: "InputSchema为nil的custom工具应该被跳过",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildTools(tt.tools)

			if len(result) != tt.expectedLen {
				t.Errorf("%s: got %d tool declarations, want %d", tt.description, len(result), tt.expectedLen)
			}

			// 验证function declarations存在
			if len(result) > 0 && result[0].FunctionDeclarations != nil {
				if len(result[0].FunctionDeclarations) != len(tt.tools) {
					t.Errorf("%s: got %d function declarations, want %d",
						tt.description, len(result[0].FunctionDeclarations), len(tt.tools))
				}
			}
		})
	}
}
