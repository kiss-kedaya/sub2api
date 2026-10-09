package service

import "strings"

// Command Code 按模型选端点：Claude 只走 Anthropic，GPT 可以走 Responses，其余走 Chat。
// 不替换现有国产供应商的协议表，只在入站 Responses 分流时使用。

const (
	DefaultCommandCodeTestModel = "deepseek/deepseek-v4-flash"
)

func (a *Account) IsCommandCode() bool {
	return a != nil && a.Platform == PlatformCommandCode
}

func commandCodeResponsesProtocol(model string) string {
	name := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	switch {
	case strings.HasPrefix(name, "claude"):
		return APIProtocolAnthropic
	case strings.HasPrefix(name, "gpt"):
		return APIProtocolResponses
	default:
		return APIProtocolChatCompletions
	}
}
