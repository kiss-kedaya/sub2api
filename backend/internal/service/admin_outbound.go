package service

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
)

// 管理面出站入口的公共错误文案：固定一句话，不区分 DNS 失败 / 解析到私网 /
// 字面量私网，避免把校验器变成内网存在性预言机。
var ErrAdminOutboundTargetRejected = errors.New("目标地址不被允许：仅允许公网可达地址")

// adminOutboundAllowPrivateForTest 只由单元测试打开：让管理面出站校验放行
// 127.0.0.1 上的 httptest 服务。生产恒为 false。
var adminOutboundAllowPrivateForTest atomic.Bool

// SetAdminOutboundAllowPrivateForTest 仅供单元测试放行本地 httptest 服务，
// 返回还原函数。
func SetAdminOutboundAllowPrivateForTest(allow bool) func() {
	previous := adminOutboundAllowPrivateForTest.Swap(allow)
	return func() { adminOutboundAllowPrivateForTest.Store(previous) }
}

// validateAdminOutboundTarget 校验管理员填写的出站 URL（字面量 + 解析）。
// 仅允许公网；返回规范化后的 URL。失败返回固定文案错误。
func validateAdminOutboundTarget(ctx context.Context, raw string) (string, error) {
	normalized, err := urlvalidator.ValidateOutboundURL(ctx, raw, urlvalidator.OutboundPolicy{
		// 与生产 ALLOW_INSECURE_HTTP=true 的现状一致：只拦内网，不额外收紧 scheme。
		AllowInsecureHTTP: true,
		AllowPrivate:      adminOutboundAllowPrivateForTest.Load(),
	})
	if err != nil {
		return "", ErrAdminOutboundTargetRejected
	}
	return normalized, nil
}

// newAdminOutboundHTTPClient 构造管理面出站校验用的安全客户端：
// 连接前按解析后的 IP 校验、重定向每一跳都校验。
func newAdminOutboundHTTPClient() *http.Client {
	return urlvalidator.NewSafeHTTPClient(urlvalidator.OutboundPolicy{
		AllowInsecureHTTP: true,
		AllowPrivate:      adminOutboundAllowPrivateForTest.Load(),
	}, urlvalidator.HTTPClientOptions{
		Timeout: 15 * time.Second,
	})
}

// 管理面探测入口（风控"测试 Key"等）通过 context 注入安全客户端：
// 只有管理面的测试调用需要强制校验，运行时业务路径沿用既有客户端。
type adminOutboundHTTPClientContextKey struct{}

// WithAdminOutboundHTTPClient 注入本次请求使用的安全出站客户端。
func WithAdminOutboundHTTPClient(ctx context.Context, client *http.Client) context.Context {
	if client == nil {
		return ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, adminOutboundHTTPClientContextKey{}, client)
}

// adminOutboundHTTPClientFromContext 取出注入的安全客户端；未注入返回 nil。
func adminOutboundHTTPClientFromContext(ctx context.Context) *http.Client {
	if ctx == nil {
		return nil
	}
	client, _ := ctx.Value(adminOutboundHTTPClientContextKey{}).(*http.Client)
	return client
}
