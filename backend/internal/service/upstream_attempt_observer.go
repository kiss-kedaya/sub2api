package service

import (
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
