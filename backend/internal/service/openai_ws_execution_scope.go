package service

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const (
	openAIWSThreadIDHeader = "thread-id"
	openAIWSWindowIDHeader = "x-codex-window-id"
)

// resolveOpenAIWSClientThreadID 提取 codex 客户端的线程标识。codex 多智能体会话里
// 父线程与全部子智能体共用同一个 session-id 头，只有线程标识能把它们区分开。
// 优先级：thread-id 头 → x-codex-turn-metadata 头 → x-codex-window-id 头 → 请求体 client_metadata。
func resolveOpenAIWSClientThreadID(c *gin.Context, body []byte) string {
	if c != nil && c.Request != nil {
		if id := strings.TrimSpace(c.GetHeader(openAIWSThreadIDHeader)); id != "" {
			return id
		}
		if id := codexTurnMetadataThreadID(c.GetHeader(openAIWSTurnMetadataHeader)); id != "" {
			return id
		}
		if window := strings.TrimSpace(c.GetHeader(openAIWSWindowIDHeader)); window != "" {
			if id := strings.TrimSpace(strings.SplitN(window, ":", 2)[0]); id != "" {
				return id
			}
		}
	}
	if len(body) == 0 {
		return ""
	}
	if id := strings.TrimSpace(gjson.GetBytes(body, "client_metadata.thread_id").String()); id != "" {
		return id
	}
	return codexTurnMetadataThreadID(gjson.GetBytes(body, "client_metadata."+openAIWSTurnMetadataHeader).String())
}

// openAIWSExecutionScopeBodyFromRequest 只序列化执行作用域会读取的字段，
// 供已解析成 map 的 HTTP 请求体取键，避免为此重新编码整个请求体。
func openAIWSExecutionScopeBodyFromRequest(reqBody map[string]any) []byte {
	if len(reqBody) == 0 {
		return nil
	}
	subset := make(map[string]any, 3)
	for _, key := range []string{"client_metadata", "prompt_cache_key", "previous_response_id"} {
		if value, ok := reqBody[key]; ok {
			subset[key] = value
		}
	}
	if len(subset) == 0 {
		return nil
	}
	raw, err := json.Marshal(subset)
	if err != nil {
		return nil
	}
	return raw
}

func codexTurnMetadataThreadID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var metadata struct {
		ThreadID string `json:"thread_id"`
	}
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		return ""
	}
	return strings.TrimSpace(metadata.ThreadID)
}

// resolveOpenAIWSExecutionScope 派生 WS 接入用于抢占与会话级状态的执行作用域键，
// 并一并返回解析到的线程标识供日志使用。身份只取客户端声明的线程标识或显式会话标识，
// 不掺入请求内容，因此同线程重连在输入变化后仍命中同一键；键里带 API key，同组不同 key
// 互不影响。两种身份都没有时返回空作用域：按请求内容推导的粘性种子只服务账号亲和，
// 不是可靠身份，不参与抢占。
func resolveOpenAIWSExecutionScope(c *gin.Context, body []byte, apiKeyID int64) (scope, threadID string) {
	if threadID = resolveOpenAIWSClientThreadID(c, body); threadID != "" {
		scope, _ = deriveOpenAISessionHashes(fmt.Sprintf("openai_ws_exec:%d|thread=%s", apiKeyID, threadID))
		return scope, threadID
	}
	if sessionID := strings.TrimSpace(explicitOpenAIRequestSessionID(c, body)); sessionID != "" {
		scope, _ = deriveOpenAISessionHashes(fmt.Sprintf("openai_ws_exec:%d|session=%s", apiKeyID, sessionID))
		return scope, ""
	}
	return "", ""
}
