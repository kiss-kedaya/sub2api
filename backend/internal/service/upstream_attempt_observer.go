package service

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"syscall"
)

// UpstreamAttemptObserver 观察每一次对上游账号发起的 HTTP 尝试的结果（重试的每一次都算一次）。
// statusCode 为 0 表示没有拿到响应（err 非空）。实现必须极轻：它在每个上游请求的热路径上被调用。
type UpstreamAttemptObserver interface {
	ObserveUpstreamAttempt(ctx context.Context, accountID int64, statusCode int, err error)
}

// UpstreamAttemptBodyPeekLimit 是传输层为改判而预读的响应体上限（字节）。
const UpstreamAttemptBodyPeekLimit = 8 << 10

// UpstreamAttemptBodyObserver 是观察者的可选扩展：个别状态码要看响应体前缀才能分对类
// （Grok 的内容安全 403、Antigravity 的容量不足 503）。传输层只在 WantsAttemptBody 返回 true 时
// 才预读响应体（最多 UpstreamAttemptBodyPeekLimit 字节，预读的内容会原样还给调用方），其余请求不多读一个字节。
type UpstreamAttemptBodyObserver interface {
	UpstreamAttemptObserver
	WantsAttemptBody(ctx context.Context, accountID int64, statusCode int) bool
	ObserveUpstreamAttemptBody(ctx context.Context, accountID int64, statusCode int, err error, bodyPrefix []byte)
}

// UpstreamAttemptObservable 由可以挂观察者的 HTTPUpstream 实现（repository 里的真实传输层）。
// 用可选接口而不是装饰器：NewHTTPUpstream 的返回类型不变，断言具体类型的测试不受影响。
type UpstreamAttemptObservable interface {
	SetUpstreamAttemptObserver(UpstreamAttemptObserver)
}

type attemptOutcome uint8

const (
	// attemptIgnored 不计入窗口：客户端主动断开、本地 ctx 取消/超时、本机资源耗尽。
	// 这些都不是「这个上游账号坏了」的证据；计入的话，本机过载会让所有账号一起被熔断，
	// 把部分故障放大成全站故障。
	attemptIgnored attemptOutcome = iota
	// attemptOK 上游活着：2xx/3xx，以及上游 4xx 里属于请求自身问题的（400/404/405/413/422 等）。
	attemptOK
	// attemptFailed 账号对我们不可用：连接/传输错误、上游响应超时、5xx、429、401/402/403/407。
	attemptFailed
)

// classifyUpstreamAttempt 是失败口径的唯一出处。
//
//	ctxErr     请求 ctx 此刻的 Err()（客户端断开 / 本地超时 / 取消都会让它非空）
//	statusCode 上游响应状态码（拿到响应时 > 0）
//	err        传输层错误（未拿到响应时非空）
func classifyUpstreamAttempt(ctxErr error, statusCode int, err error) attemptOutcome {
	if err != nil {
		if ctxErr != nil || errors.Is(err, context.Canceled) || isLocalResourceError(err) {
			return attemptIgnored
		}
		// 拨号/握手/读写失败、代理故障、上游响应头超时（net.Error 超时，不是本地 ctx 超时）：失败。
		return attemptFailed
	}
	if statusCode <= 0 {
		return attemptIgnored
	}
	if isUpstreamFailureStatus(statusCode) {
		return attemptFailed
	}
	return attemptOK
}

// attemptBodyRefinable 报告「这个平台上的这个状态码」是否可能被响应体改判。
// 平台标签未知（空）时按可能处理：宁可多读一次小响应体，也不漏改判。
func attemptBodyRefinable(platform string, statusCode int) bool {
	switch statusCode {
	case http.StatusForbidden:
		return platform == "" || platform == PlatformGrok
	case http.StatusServiceUnavailable:
		return platform == "" || platform == PlatformAntigravity
	}
	return false
}

// refineFailedAttempt 用响应体前缀把「状态码看起来像账号故障、实际不是」的尝试改判：
//   - Grok 403 里的内容安全拒绝（提示词/素材被拒，换账号也一样）：账号是好的，算「上游活着」；
//   - Antigravity 503 MODEL_CAPACITY_EXHAUSTED：所有账号共享同一个模型容量池，不是这个账号的问题，不计入。
//
// 响应体读不到、被截断到认不出来、或者经过压缩时保持「失败」：宁可多计一次，也不放过真实的封号/故障。
func refineFailedAttempt(platform string, statusCode int, body []byte) attemptOutcome {
	switch statusCode {
	case http.StatusForbidden:
		if (platform == "" || platform == PlatformGrok) && isGrokContentPolicyRejection(statusCode, body) {
			return attemptOK
		}
	case http.StatusServiceUnavailable:
		if (platform == "" || platform == PlatformAntigravity) && bytes.Contains(body, []byte(googleRPCReasonModelCapacityExhausted)) {
			return attemptIgnored
		}
	}
	return attemptFailed
}

// isUpstreamFailureStatus 判断上游状态码是否说明「这个账号现在不能用」。
//
//   - 401/402/403：上游认证失败/欠费/封禁；407：账号的代理认证失败。
//   - 429：上游限流（生产里 429 冷却可能是关的，由断路器兜底）。
//   - 5xx：500/502/503/504、Cloudflare 520–526/530、Anthropic 529 等。
//     501（上游不支持该接口）和 505（HTTP 版本不支持）是请求/端点自身的问题，不算。
//   - 其余 4xx（400/404/405/413/422…）：上游活着、是请求不对，不算。
func isUpstreamFailureStatus(status int) bool {
	switch status {
	case http.StatusUnauthorized, http.StatusPaymentRequired, http.StatusForbidden,
		http.StatusProxyAuthRequired, http.StatusTooManyRequests:
		return true
	case http.StatusNotImplemented, http.StatusHTTPVersionNotSupported:
		return false
	}
	return status >= 500 && status <= 599
}

// isLocalResourceError 识别「本机资源耗尽」类错误：文件描述符/临时端口/缓冲区/内存用完。
// 它们影响所有账号，不是某个上游的问题。
func isLocalResourceError(err error) bool {
	return errors.Is(err, syscall.EMFILE) ||
		errors.Is(err, syscall.ENFILE) ||
		errors.Is(err, syscall.ENOBUFS) ||
		errors.Is(err, syscall.EADDRNOTAVAIL) ||
		errors.Is(err, syscall.ENOMEM)
}
