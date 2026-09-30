package service

import (
	"encoding/json"
	"strings"
	"unsafe"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// unsafeFinancialEventString 把 []byte 零拷贝地当成 string 用。
// 仅用于 gjson 这类只读消费者，且调用方保证字节切片在结果消费期间存活。
func unsafeFinancialEventString(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return unsafe.String(&b[0], len(b))
}

// Rebuild failure envelopes from an allowlist, never replace text inside output,
// tools or usage. Internal callers retain the original event for side effects.
func redactUpstreamFinancialEvent(payload []byte, eventType string) []byte {
	errorBody := gin.H{"type": "upstream_error", "code": "upstream_error", "message": UpstreamUnavailableMessage}
	event := gin.H{"type": eventType, "error": errorBody}
	if eventType == "response.failed" {
		response := gin.H{"id": gjson.GetBytes(payload, "response.id").String(), "object": "response", "created_at": gjson.GetBytes(payload, "response.created_at").Int(), "status": "failed", "output": []any{}, "error": errorBody}
		event = gin.H{"type": "response.failed", "sequence_number": gjson.GetBytes(payload, "sequence_number").Int(), "response": response}
	}
	result, _ := json.Marshal(event)
	return result
}

// Only inspect known failure envelopes on successful HTTP responses. In
// particular, user-generated content, tool arguments and usage are not scanned.
func upstreamFinancialFailureEnvelope(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	// 零拷贝视图：gjson 只读、不持有，而 body 在整个调用期间都存活。
	// 递归里也一直传 string，避免每层都复制一次（原先每层是
	// []byte(item.Raw) + ParseBytes 两次复制）。
	return upstreamFinancialFailureEnvelopeText(unsafeFinancialEventString(body))
}

func upstreamFinancialFailureEnvelopeText(text string) bool {
	// 便宜门：下面每个分支的触发词只有 "error" 与 "failed" 两个
	// （errors / error / response.error / "type":"error" / "status":"failed"）。
	// 两者都不出现时所有分支都不可能成立，直接返回——省掉整份解析。
	//
	// 这是本函数最大的一笔：SSE 每个事件都会调用它，而正常响应里
	// 绝大多数帧既不含 "error" 也不含 "failed"。
	if !strings.Contains(text, "error") && !strings.Contains(text, "failed") {
		return false
	}

	node := gjson.Parse(text)
	if node.IsArray() {
		for _, item := range node.Array() {
			if upstreamFinancialFailureEnvelopeText(item.Raw) {
				return true
			}
		}
		return false
	}
	if node.Get("errors").IsArray() || node.Get("error").Exists() && node.Get("error").Type != gjson.Null || node.Get("response.error").Exists() && node.Get("response.error").Type != gjson.Null || node.Get("type").String() == "error" || node.Get("status").String() == "failed" {
		// 只在确实命中候选的罕见路径上才付一次 string→[]byte 的复制。
		return IsUpstreamFinancialError(0, []byte(text))
	}
	return false
}
