package service

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// 协议族边界与跨协议转换开关。
//
// 网关历史上允许「入站协议」与「上游协议」任意交叉。按协议族划分只有两类：
//
//	anthropic 族：POST /v1/messages（Anthropic Messages 线格式）
//	openai 族   ：POST /v1/responses、POST /v1/chat/completions
//
// 族内的 chat <-> responses 互转（Codex OAuth、国产 OpenAI 兼容上游的
// CC→Responses 归一）是上游既有设计，量大且稳定，**不受本开关影响**。
//
// 跨族转换（messages <-> responses / chat）需要整套 Anthropic 事件状态机与
// 回程重建，在 tool_use、thinking 回传、usage 分桶上反复出问题。本开关按分组
// 控制是否允许跨族转换，默认关闭。
//
// 关闭时的行为：不再静默转换，而是返回明确的 400，指出该分组不允许跨协议转换、
// 以及如何开启。这样用户侧看到的是可诊断的错误，而不是「转换漏了参数」这类
// 难以归因的表现。
const (
	// crossProtocolDisabledMessage 是跨协议转换被分组开关关闭时返回给客户端的说明。
	crossProtocolDisabledMessage = "该分组不允许 Anthropic 协议族与 OpenAI 协议族之间转换：" +
		"请使用与分组平台匹配的入口（Anthropic 平台用 /v1/messages；OpenAI 平台用 /v1/responses 或 /v1/chat/completions），" +
		"或让管理员在分组设置里打开「跨协议转换」。"

	// crossProtocolDisabledCode 是跨协议转换被关闭时的稳定错误码。
	// 与请求入口无关：所有「协议族不匹配且转换被关闭」的拒绝都用这个码，
	// 客户端据 code 即可判定这是配置问题，不要重试。
	crossProtocolDisabledCode = "PROTOCOL_MISMATCH"

	// ProtocolMismatchCode 是协议族不匹配的对外稳定错误码（导出给 handler 写响应体）。
	ProtocolMismatchCode = crossProtocolDisabledCode
)

// CrossProtocolConversionAllowed 报告当前请求是否允许跨协议族转换。
//
// 判定顺序：
//  1. 拿不到分组（内部调用、测试、无分组的 key）时放行——此时没有可依据的
//     配置来源，按「保持旧行为」处理，避免误拦。
//  2. 否则读分组级开关。
//
// 注意：本函数只回答「能不能跨族」。族内的 chat<->responses 互转不经过它。
func CrossProtocolConversionAllowed(apiKey *APIKey) bool {
	if apiKey == nil || apiKey.Group == nil {
		return true
	}
	return apiKey.Group.CrossProtocolConversionEnabled
}

// crossProtocolConversionAllowedFromContext 是 CrossProtocolConversionAllowed
// 的 gin 便捷封装，供转发热路径使用。
func crossProtocolConversionAllowedFromContext(c *gin.Context) bool {
	if c == nil {
		return true
	}
	return CrossProtocolConversionAllowed(getAPIKeyFromContext(c))
}

// CrossProtocolConversionError 是跨协议转换被关闭时返回的稳定错误。
type CrossProtocolConversionError struct{}

func (CrossProtocolConversionError) Error() string { return crossProtocolDisabledMessage }

// IsCrossProtocolConversionDisabled 判断错误是否为跨协议转换被分组开关关闭。
func IsCrossProtocolConversionDisabled(err error) bool {
	_, ok := err.(CrossProtocolConversionError)
	return ok
}

// IsOpenAIProtocolFamilyPath reports whether an inbound endpoint speaks the
// OpenAI protocol family (Responses / Chat Completions). The check is
// path-based on purpose: it runs before the body is parsed so a protocol
// mismatch can be rejected deterministically instead of falling through
// account selection and surfacing as a retryable 503.
func IsOpenAIProtocolFamilyPath(path string) bool {
	normalized := strings.ToLower(strings.TrimSpace(path))
	return strings.Contains(normalized, "/responses") ||
		strings.Contains(normalized, "/chat/completions")
}

// protocolFamilyLabel renders a platform value for client-facing messages.
func protocolFamilyLabel(platform string) string {
	switch platform {
	case PlatformAnthropic:
		return "Anthropic"
	case PlatformOpenAI:
		return "OpenAI"
	case PlatformComposite:
		return "composite"
	default:
		return strings.TrimSpace(platform)
	}
}

// ProtocolMismatchMessage is the stable user-facing sentence for a request
// whose protocol family cannot be served by the resolved group platform and
// whose group has cross-family conversion disabled.
func ProtocolMismatchMessage(path, groupPlatform string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "the requested endpoint"
	}
	if strings.TrimSpace(groupPlatform) == "" {
		return fmt.Sprintf("请求入口 %s 与分组平台不匹配，且该分组未开启「跨协议转换」", path)
	}
	return fmt.Sprintf("该分组平台是 %s（platform=%s），入站入口 %s 属于另一协议族，且该分组未开启「跨协议转换」",
		protocolFamilyLabel(groupPlatform), groupPlatform, path)
}

// ProtocolMismatchHint tells the caller exactly which endpoint to use (and how
// an operator can enable conversion instead).
func ProtocolMismatchHint(path, groupPlatform string) string {
	if IsOpenAIProtocolFamilyPath(path) {
		switch groupPlatform {
		case PlatformAnthropic:
			return "该分组是 Anthropic 平台：请调用 /v1/messages；或让管理员在分组设置中开启「跨协议转换」；或改用 OpenAI 兼容分组"
		default:
			return "请改用与分组平台匹配的入口；或让管理员在分组设置中开启「跨协议转换」；或改用 OpenAI 兼容分组"
		}
	}
	switch groupPlatform {
	case PlatformOpenAI, PlatformGrok, PlatformKimi, PlatformZhipu,
		PlatformDeepseek, PlatformMiniMax, PlatformCline, PlatformCommandCode:
		return "该分组是 OpenAI 兼容平台：请调用 /v1/responses 或 /v1/chat/completions；或让管理员在分组设置中开启「跨协议转换」；或改用 Anthropic 分组"
	default:
		return "请用与分组平台匹配的入口；或让管理员在分组设置中开启「跨协议转换」"
	}
}

// ProtocolMismatchPayload carries the machine-readable fields attached to a
// PROTOCOL_MISMATCH error body. Handlers marshal it next to the standard error
// object so clients can branch on path/group_platform without regexing text.
type ProtocolMismatchPayload struct {
	Code          string `json:"code"`
	Message       string `json:"message"`
	Path          string `json:"path"`
	GroupPlatform string `json:"group_platform"`
	Hint          string `json:"hint"`
}

// NewProtocolMismatchPayload builds the payload for the given inbound path and
// resolved group platform.
func NewProtocolMismatchPayload(path, groupPlatform string) ProtocolMismatchPayload {
	return ProtocolMismatchPayload{
		Code:          ProtocolMismatchCode,
		Message:       ProtocolMismatchMessage(path, groupPlatform),
		Path:          strings.TrimSpace(path),
		GroupPlatform: strings.TrimSpace(groupPlatform),
		Hint:          ProtocolMismatchHint(path, groupPlatform),
	}
}

// WriteProtocolMismatchError writes the canonical 400 PROTOCOL_MISMATCH body for
// an endpoint whose protocol family cannot be served by the resolved group
// platform because cross-family conversion is disabled.
//
// Both the ingress handlers and the conversion entry points use it so the shape
// stays identical no matter where the request is rejected: an OpenAI error
// envelope with type/code/message plus the machine-readable path,
// group_platform and hint fields.
func WriteProtocolMismatchError(c *gin.Context, path, groupPlatform string) {
	if c == nil {
		return
	}
	payload := NewProtocolMismatchPayload(path, groupPlatform)
	MarkResponseCommitted(c)
	c.JSON(http.StatusBadRequest, gin.H{
		"error": gin.H{
			"type":           "invalid_request_error",
			"code":           payload.Code,
			"message":        payload.Message,
			"path":           payload.Path,
			"group_platform": payload.GroupPlatform,
			"hint":           payload.Hint,
		},
	})
}

// inboundProtocolMismatchCoordinates resolves the request path and the protocol
// platform that owns the mismatch. It prefers the live request path (clients may
// call an alias such as /responses or /openai/v1/responses) and the
// composite-resolved target platform, falling back to the group platform.
func inboundProtocolMismatchCoordinates(c *gin.Context, fallbackPath string) (string, string) {
	path := fallbackPath
	platform := ""
	if c != nil && c.Request != nil {
		if c.Request.URL != nil {
			if live := strings.TrimSpace(c.Request.URL.Path); live != "" {
				path = live
			}
		}
		if resolved, ok := ResolvedTargetPlatformFromContext(c.Request.Context()); ok {
			platform = resolved
		}
	}
	if platform == "" {
		if apiKey := getAPIKeyFromContext(c); apiKey != nil && apiKey.Group != nil {
			platform = apiKey.Group.Platform
		}
	}
	return path, platform
}

// IsOpenAIResponsesCompatibleGatewayPlatform reports whether a group platform is
// forwarded through the OpenAI-protocol gateway (OpenAI 兼容供应商、Grok 等）。
// Anthropic/Antigravity/Gemini/Composite 不走这里：它们的 /v1/responses 入站
// 由 Anthropic 网关做 Responses→Anthropic 转换（或按 target platform 解析）。
//
// 该判定同时是「协议族」的唯一入口：返回 true 的平台上，/v1/responses 与
// /v1/chat/completions 属于同族，不需要跨族转换开关。
func IsOpenAIResponsesCompatibleGatewayPlatform(platform string) bool {
	switch platform {
	case PlatformOpenAI, PlatformGrok,
		PlatformKimi, PlatformZhipu, PlatformDeepseek, PlatformMiniMax,
		PlatformCline, PlatformCommandCode:
		return true
	default:
		return false
	}
}

// InboundProtocolFamily labels the protocol family of an inbound gateway path:
//
//	"openai"    — /v1/responses, /v1/chat/completions（OpenAI 线格式）
//	"anthropic" — /v1/messages（Anthropic 线格式）
//	""          — 其它端点（图片/视频/model 等），协议族判定不适用
func InboundProtocolFamily(path string) string {
	normalized := strings.ToLower(strings.TrimSpace(path))
	switch {
	case IsOpenAIProtocolFamilyPath(normalized):
		return "openai"
	case strings.Contains(normalized, "/messages"):
		return "anthropic"
	default:
		return ""
	}
}

// SelectionFailureProtocolDiagnostics renders the protocol/platform context of a
// "no available accounts" failure so the 503 body can tell operators whether the
// request protocol and the group platform even match. It is deliberately cheap
// (no API calls) and stable enough for logs and client messages.
//
// Returned example:
//
//	inbound=/v1/responses, inbound_family=openai, group_platform=anthropic,
//	target_platform=anthropic, protocol_family_match=false,
//	cross_protocol_conversion=true, servable_via_conversion=true
func SelectionFailureProtocolDiagnostics(c *gin.Context, apiKey *APIKey, endpoint string) string {
	path := strings.TrimSpace(endpoint)
	groupPlatform := ""
	if apiKey != nil && apiKey.Group != nil {
		groupPlatform = apiKey.Group.Platform
	}
	targetPlatform := groupPlatform
	conversionAllowed := CrossProtocolConversionAllowed(apiKey)
	if c != nil && c.Request != nil {
		path = func() string {
			if endpoint != "" {
				return endpoint
			}
			if c.Request.URL != nil {
				return c.Request.URL.Path
			}
			return ""
		}()
		if resolved, ok := ResolvedTargetPlatformFromContext(c.Request.Context()); ok && resolved != "" {
			targetPlatform = resolved
		}
	}

	family := InboundProtocolFamily(path)
	familyMatch := true
	switch family {
	case "openai":
		familyMatch = IsOpenAIResponsesCompatibleGatewayPlatform(targetPlatform)
	case "anthropic":
		familyMatch = targetPlatform == PlatformAnthropic || targetPlatform == PlatformAntigravity ||
			targetPlatform == PlatformGemini || targetPlatform == PlatformComposite
	}
	return fmt.Sprintf(
		"inbound=%s, inbound_family=%s, group_platform=%s, target_platform=%s, protocol_family_match=%t, cross_protocol_conversion=%t, servable_via_conversion=%t",
		path, family, groupPlatform, targetPlatform, familyMatch, conversionAllowed, familyMatch || conversionAllowed,
	)
}
