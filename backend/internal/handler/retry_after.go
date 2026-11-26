package handler

import (
	"math/rand/v2"
	"strconv"

	"github.com/gin-gonic/gin"
)

// 网关自己返回的 503 / 429（没有可用账号、排队已满、并发/限流…）统一带上 Retry-After，
// 让 SDK 的重试循环按这个秒数退避，而不是立刻重试把风暴放大。只加头，不改状态码和错误体。
//
// 不碰这几类：
//   - 已经带了 Retry-After 的（计费类错误、上游透传的 Retry-After、容量冷却的 30 秒）；
//   - 上游错误的透传（failover 耗尽后把上游的 429/503 原样交给客户端）：那是上游的状态，
//     不是网关自己的决定，由 markUpstreamPassthroughError 标记后跳过；
//   - 响应头已经提交（SSE 流中途）的错误：头发不出去。

const upstreamPassthroughErrorKey = "gateway.upstream_passthrough_error"

// markUpstreamPassthroughError 标记「本次错误响应是上游错误的透传」，gatewayRetryAfter 随后不再加头。
func markUpstreamPassthroughError(c *gin.Context) {
	if c != nil {
		c.Set(upstreamPassthroughErrorKey, true)
	}
}

// gatewayRetryAfterSeconds 在 [retry_after_min_seconds, retry_after_max_seconds] 内取随机秒数。
// 抖动让被同时拒绝的一批客户端不会在同一秒一起回来。
func gatewayRetryAfterSeconds() int {
	s := currentRetryGuard()
	lo, hi := s.retryAfterMinSeconds, s.retryAfterMaxSeconds
	if hi <= lo {
		return lo
	}
	return lo + rand.IntN(hi-lo+1) //nolint:gosec // G404: Retry-After 抖动不需要密码学随机
}

// setGatewayRetryAfter 在网关自己生成的 429/503 响应上补 Retry-After（见文件头说明）。
// 必须在写响应体之前调用（gin 在第一次 Write 时才发送响应头）。
func setGatewayRetryAfter(c *gin.Context, status int) {
	if c == nil || (status != 429 && status != 503) {
		return
	}
	if v, ok := c.Get(upstreamPassthroughErrorKey); ok {
		if passthrough, _ := v.(bool); passthrough {
			return
		}
	}
	if c.Writer.Written() || c.Writer.Header().Get("Retry-After") != "" {
		return
	}
	c.Header("Retry-After", strconv.Itoa(gatewayRetryAfterSeconds()))
}
