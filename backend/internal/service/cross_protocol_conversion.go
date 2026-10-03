package service

import "github.com/gin-gonic/gin"

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
	crossProtocolDisabledMessage = "This group does not allow cross-protocol conversion " +
		"(Anthropic Messages <-> OpenAI Responses/Chat Completions). " +
		"Ask the administrator to enable \"cross protocol conversion\" on this group, " +
		"or use a group whose upstream protocol matches your client protocol."

	// crossProtocolDisabledCode 是跨协议转换被关闭时的稳定错误码。
	crossProtocolDisabledCode = "cross_protocol_conversion_disabled"
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
