package service

import (
	"context"
	"net/http"
)

// 对象存储（备份 / 图床）的出站客户端注入。
//
// S3 SDK 在 repository 层被构造，而「这次请求要不要做强制出站校验」由
// service 层的入口决定（例如管理后台的"测试连接"按钮必须拒绝私网目标，
// 而定时备份沿用部署既有配置）。工厂签名保持稳定，客户端通过 context
// 传递：仅测试入口会注入一个 urlvalidator 安全客户端，其它路径不注入。
type objectStoreHTTPClientContextKey struct{}

// WithObjectStoreHTTPClient 把这次对象存储操作要用的 HTTP 客户端放进 context。
// client 为 nil 时等价于不注入。
func WithObjectStoreHTTPClient(ctx context.Context, client *http.Client) context.Context {
	if client == nil {
		return ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, objectStoreHTTPClientContextKey{}, client)
}

// ObjectStoreHTTPClientFromContext 取出注入的 HTTP 客户端；未注入返回 nil。
// 只供 repository 层的 S3 客户端构造使用。
func ObjectStoreHTTPClientFromContext(ctx context.Context) *http.Client {
	if ctx == nil {
		return nil
	}
	client, _ := ctx.Value(objectStoreHTTPClientContextKey{}).(*http.Client)
	return client
}
