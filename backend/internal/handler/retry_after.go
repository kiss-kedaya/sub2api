package handler

import (
	"math"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// 网关自己返回的 503 / 429 按「客户端多久之后重试才有意义」分类给 Retry-After，让 SDK 的重试循环按这个秒数退避，
// 而不是立刻重试把风暴放大。只加头，不改状态码和错误体。
//
//   - 没有可用账号等容量类（503，以及「所有账号都被限流」的 429）：有熔断冷却信息时用「最早恢复的账号还要多久 + 1–5 秒」
//     （最多 30 秒）；拿不到时在 [retry_after_min_seconds, retry_after_max_seconds]（默认 5–15 秒）内随机；
//   - 用户 / Key 自己的并发上限、排队已满（429）：1–3 秒（槽位很快会释放）；
//   - 重试预算用尽后透传的上游 5xx：5–10 秒（网关替客户端做了「不再重试」的决定，客户端该退避一会儿再来）；
//   - 额度 / 余额 / Key 限流这类 429：不带 Retry-After——要等到额度重置，30 秒后重试没有意义；
//     有重置时间的由计费错误自己带头；
//   - 其余没认出来的 429：不带。
//
// 不碰：已经带了 Retry-After 的（计费类错误、上游透传的 Retry-After、容量冷却的 30 秒）；
// 上游错误的透传（上游自己的状态，不是网关的决定）；响应头已经提交（SSE 流中途）的错误。
// gateway.retry_guard.retry_after_enabled=false 时整个功能关闭，一律不带。

type retryAfterKind uint8

const (
	retryAfterNone retryAfterKind = iota
	retryAfterNoCapacity
	retryAfterUserLimit
	retryAfterUpstreamBudget
)

const (
	retryAfterUserLimitMin      = 1
	retryAfterUserLimitMax      = 3
	retryAfterBudgetMin         = 5
	retryAfterBudgetMax         = 10
	retryAfterCooldownJitterMin = 1
	retryAfterCooldownJitterMax = 5
	retryAfterCooldownCap       = 30
)

const upstreamPassthroughErrorKey = "gateway.upstream_passthrough_error"

// markUpstreamPassthroughError 标记「本次错误响应是上游错误的透传」，网关自己的 Retry-After 分类随后不再加头。
// 例外：重试预算用尽后返回的上游 5xx，网关替客户端做了「不再重试」的决定，补一个 5–10 秒的 Retry-After
// （上游自己带的 Retry-After 稍后由 copyFailoverRetryAfter 覆盖）。failoverErr 可以为 nil。
func markUpstreamPassthroughError(c *gin.Context, failoverErr *service.UpstreamFailoverError) {
	if c == nil {
		return
	}
	c.Set(upstreamPassthroughErrorKey, true)
	if failoverErr != nil && failoverErr.BudgetExhausted && failoverErr.StatusCode >= 500 && failoverErr.StatusCode <= 599 {
		setRetryAfterHeader(c, retryAfterUpstreamBudget)
	}
}

// circuitRecoveryHint 回答「本进程里最早结束冷却的熔断账号还要多久恢复」。
// 断路器本来就是进程级的，所以在 handler 构造时注册一次即可。
var circuitRecoveryHint atomic.Pointer[func() (time.Duration, bool)]

type circuitRecoveryHinter interface {
	CircuitRecoveryIn() (time.Duration, bool)
}

// registerCircuitRecoveryHint 记下能回答「最早什么时候有账号从熔断冷却里恢复」的服务。
func registerCircuitRecoveryHint(svc any) {
	if hinter, ok := svc.(circuitRecoveryHinter); ok {
		fn := hinter.CircuitRecoveryIn
		circuitRecoveryHint.Store(&fn)
	}
}

func circuitRecoveryIn() (time.Duration, bool) {
	if fn := circuitRecoveryHint.Load(); fn != nil {
		return (*fn)()
	}
	return 0, false
}

// isQuotaLimitMarker 判断是不是计费/额度/Key 限流类 429 的错误码：这类错误等多久都不会好，不带 Retry-After。
func isQuotaLimitMarker(v string) bool {
	switch v {
	case "rate_limit_exceeded", "API_KEY_QUOTA_EXHAUSTED", "insufficient_quota":
		return true
	}
	return false
}

// classifyGatewayRetryAfter 按状态码和网关错误的类型/码/文案判断 Retry-After 的类别。
func classifyGatewayRetryAfter(status int, errType, code, message string) retryAfterKind {
	switch status {
	case http.StatusServiceUnavailable:
		return retryAfterNoCapacity
	case http.StatusTooManyRequests:
		if isQuotaLimitMarker(errType) || isQuotaLimitMarker(code) {
			return retryAfterNone
		}
		switch code {
		case gatewayQueueFullCode, gatewayConcurrencyLimitCode, apiKeyQueueFullCode, apiKeyQueueTimeoutCode:
			return retryAfterUserLimit
		}
		if strings.HasPrefix(message, "No available accounts") || strings.Contains(message, "currently rate-limited") {
			return retryAfterNoCapacity
		}
		if strings.HasPrefix(message, "Too many pending requests") || errType == "rate_limit_error" {
			return retryAfterUserLimit
		}
	}
	return retryAfterNone
}

func randSeconds(lo, hi int) int {
	if hi <= lo {
		return lo
	}
	return lo + rand.IntN(hi-lo+1) //nolint:gosec // G404: Retry-After 抖动不需要密码学随机
}

// gatewayRetryAfterSeconds 返回某一类 Retry-After 的秒数（带随机抖动，被同时拒绝的一批客户端不会在同一秒一起回来）。
func gatewayRetryAfterSeconds(kind retryAfterKind) int {
	switch kind {
	case retryAfterUserLimit:
		return randSeconds(retryAfterUserLimitMin, retryAfterUserLimitMax)
	case retryAfterUpstreamBudget:
		return randSeconds(retryAfterBudgetMin, retryAfterBudgetMax)
	case retryAfterNoCapacity:
		s := currentRetryGuard()
		if remaining, ok := circuitRecoveryIn(); ok && remaining > 0 {
			secs := int(math.Ceil(remaining.Seconds())) + randSeconds(retryAfterCooldownJitterMin, retryAfterCooldownJitterMax)
			return min(secs, max(retryAfterCooldownCap, s.retryAfterMaxSeconds))
		}
		return randSeconds(s.retryAfterMinSeconds, s.retryAfterMaxSeconds)
	}
	return 0
}

// setRetryAfterHeader 补 Retry-After：功能被关闭、响应头已提交、或已经带了 Retry-After 时什么都不做。
// 必须在写响应体之前调用（gin 在第一次 Write 时才发送响应头）。
func setRetryAfterHeader(c *gin.Context, kind retryAfterKind) {
	if c == nil || kind == retryAfterNone || !currentRetryGuard().retryAfterEnabled {
		return
	}
	if c.Writer.Written() || c.Writer.Header().Get("Retry-After") != "" {
		return
	}
	if secs := gatewayRetryAfterSeconds(kind); secs > 0 {
		c.Header("Retry-After", strconv.Itoa(secs))
	}
}

// setGatewayRetryAfter 在网关自己生成的 429/503 响应上按错误类型补 Retry-After（见文件头说明）。
// errType / code / message 是这次错误响应的类型、机器码和文案，没有的传空字符串。
func setGatewayRetryAfter(c *gin.Context, status int, errType, code, message string) {
	if c == nil {
		return
	}
	kind := classifyGatewayRetryAfter(status, errType, code, message)
	if kind == retryAfterNone {
		return
	}
	if v, ok := c.Get(upstreamPassthroughErrorKey); ok {
		if passthrough, _ := v.(bool); passthrough {
			return
		}
	}
	setRetryAfterHeader(c, kind)
}
