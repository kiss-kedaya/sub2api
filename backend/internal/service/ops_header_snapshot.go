package service

import (
	"context"
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
)

// 请求头 / 响应头的可观测快照。
//
// 背景：出错时最常被追问的三件事是「客户端到底发了什么头」「我们发给上游的头是什么」
// 「上游回了什么头」，而 ops_error_logs 此前只存了 User-Agent 一个头。
// 本文件把三份头快照挂到 gin context 上，由 ops_error_logger 落库、
// 由后台错误详情页展示。
//
// 三份快照：
//
//	ops_request_headers          客户端 -> 网关（入站）
//	ops_upstream_request_headers 网关   -> 上游（出站）
//	ops_upstream_response_headers 上游  -> 网关（回程）
//
// 安全约束（必须遵守）：
//   - 凭据类头一律不进快照：authorization / x-api-key / api-key / cookie /
//     set-cookie / proxy-authorization / x-goog-api-key。命中即丢弃整条，
//     而不是打码后保留——避免任何形式的凭据片段入库。
//   - 其余头的值按 opsHeaderSnapshotMaxValueBytes 截断，条数按
//     opsHeaderSnapshotMaxEntries 截断，避免大头把一次错误记录撑爆。
//   - 键名统一小写并排序，保证同一请求在不同时间的快照可比对。
const (
	opsRequestHeadersKey         = "ops_request_headers"
	opsUpstreamRequestHeadersKey = "ops_upstream_request_headers"
	opsUpstreamRespHeadersKey    = "ops_upstream_response_headers"

	opsHeaderSnapshotMaxEntries     = 64
	opsHeaderSnapshotMaxValueBytes  = 512
	opsHeaderSnapshotMaxTotalBytes  = 8192
	opsHeaderSnapshotTruncatedValue = "...(truncated)"
)

// opsHeaderCredentialDenylist 是绝不入快照的头（小写）。
var opsHeaderCredentialDenylist = map[string]bool{
	"authorization":        true,
	"proxy-authorization":  true,
	"x-api-key":            true,
	"api-key":              true,
	"apikey":               true,
	"x-goog-api-key":       true,
	"cookie":               true,
	"set-cookie":           true,
	"x-amz-security-token": true,
}

// OpsHeaderSnapshot 是三份头快照的公开形态（键已小写、已排序、已截断）。
type OpsHeaderSnapshot map[string]string

// SnapshotHeadersForOps 把 http.Header 转成可落库的快照。
// 返回 nil 表示没有可记录的头（全被过滤或入参为空）。
func SnapshotHeadersForOps(header http.Header) OpsHeaderSnapshot {
	if len(header) == 0 {
		return nil
	}
	out := make(OpsHeaderSnapshot, len(header))
	total := 0
	keys := make([]string, 0, len(header))
	for key := range header {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if len(out) >= opsHeaderSnapshotMaxEntries || total >= opsHeaderSnapshotMaxTotalBytes {
			break
		}
		lower := strings.ToLower(strings.TrimSpace(key))
		if lower == "" || opsHeaderCredentialDenylist[lower] {
			continue
		}
		values := header[key]
		if len(values) == 0 {
			continue
		}
		joined := strings.Join(values, ", ")
		joined = truncateHeaderSnapshotValue(joined)
		if joined == "" {
			continue
		}
		out[lower] = joined
		total += len(lower) + len(joined)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func truncateHeaderSnapshotValue(value string) string {
	value = strings.TrimSpace(strings.ToValidUTF8(value, ""))
	if len(value) <= opsHeaderSnapshotMaxValueBytes {
		return value
	}
	return value[:opsHeaderSnapshotMaxValueBytes] + opsHeaderSnapshotTruncatedValue
}

// SetOpsRequestHeaders 记录入站请求头快照（客户端 -> 网关）。
func SetOpsRequestHeaders(c *gin.Context, header http.Header) {
	setOpsHeaderSnapshot(c, opsRequestHeadersKey, header)
}

// SetOpsUpstreamRequestHeaders 记录出站请求头快照（网关 -> 上游）。
func SetOpsUpstreamRequestHeaders(c *gin.Context, header http.Header) {
	setOpsHeaderSnapshot(c, opsUpstreamRequestHeadersKey, header)
}

// SetOpsUpstreamResponseHeaders 记录上游回程响应头快照（上游 -> 网关）。
func SetOpsUpstreamResponseHeaders(c *gin.Context, header http.Header) {
	setOpsHeaderSnapshot(c, opsUpstreamRespHeadersKey, header)
}

// MergeOpsUpstreamResponseHeaders 在上游回程头之外补充网关自己追加的头
// （例如透传白名单处理后的最终集合）。已有同名键以新值为准。
func MergeOpsUpstreamResponseHeaders(c *gin.Context, header http.Header) {
	if c == nil {
		return
	}
	snapshot := SnapshotHeadersForOps(header)
	if snapshot == nil {
		return
	}
	merged := OpsHeaderSnapshot{}
	if v, ok := c.Get(opsUpstreamRespHeadersKey); ok {
		if existing, ok := v.(OpsHeaderSnapshot); ok {
			for key, value := range existing {
				merged[key] = value
			}
		}
	}
	for key, value := range snapshot {
		merged[key] = value
	}
	c.Set(opsUpstreamRespHeadersKey, merged)
}

func setOpsHeaderSnapshot(c *gin.Context, key string, header http.Header) {
	if c == nil {
		return
	}
	snapshot := SnapshotHeadersForOps(header)
	if snapshot == nil {
		return
	}
	c.Set(key, snapshot)
}

// GetOpsRequestHeaders / GetOpsUpstreamRequestHeaders / GetOpsUpstreamResponseHeaders
// 读取快照，供 ops_error_logger 落库。未记录时返回 nil。
func GetOpsRequestHeaders(c *gin.Context) OpsHeaderSnapshot {
	return getOpsHeaderSnapshot(c, opsRequestHeadersKey)
}

func GetOpsUpstreamRequestHeaders(c *gin.Context) OpsHeaderSnapshot {
	return getOpsHeaderSnapshot(c, opsUpstreamRequestHeadersKey)
}

func GetOpsUpstreamResponseHeaders(c *gin.Context) OpsHeaderSnapshot {
	return getOpsHeaderSnapshot(c, opsUpstreamRespHeadersKey)
}

// =========================
// gin context 载体
// =========================

// opsGinContextKey 把当前请求的 *gin.Context 挂到 http.Request 的 context 上。
//
// 为什么需要：上游调用点（doOpenAIUpstream / httpUpstream.DoWithTLS）只拿得到
// *http.Request，拿不到 gin.Context，但它们正是唯一能同时看到「出站请求头」和
// 「上游回程响应头」的地方。与其在二十多个调用点手工传 gin.Context，不如在入口
// 中间件挂一次，之后任何持有 request 的地方都能取回。
type opsGinContextKey struct{}

// WithOpsGinContext 把 gin.Context 绑到 ctx 上，供上游层回写头快照。
func WithOpsGinContext(ctx context.Context, c *gin.Context) context.Context {
	if ctx == nil || c == nil {
		return ctx
	}
	return context.WithValue(ctx, opsGinContextKey{}, c)
}

// OpsGinContextFrom 从 ctx 取回 gin.Context；取不到返回 nil。
func OpsGinContextFrom(ctx context.Context) *gin.Context {
	if ctx == nil {
		return nil
	}
	c, _ := ctx.Value(opsGinContextKey{}).(*gin.Context)
	return c
}

// CaptureOpsUpstreamHeaders 记录一次上游调用的出站请求头与回程响应头。
//
// req / resp 允许为 nil（传输失败时没有 resp）。函数对 nil 安全，
// 拿不到 gin.Context 时静默跳过——头快照是诊断增强，任何情况下都不得
// 影响转发与计费主链路。
func CaptureOpsUpstreamHeaders(ctx context.Context, req *http.Request, resp *http.Response) {
	c := OpsGinContextFrom(ctx)
	if c == nil {
		return
	}
	if req != nil {
		SetOpsUpstreamRequestHeaders(c, req.Header)
	}
	if resp != nil {
		SetOpsUpstreamResponseHeaders(c, resp.Header)
	}
}

func getOpsHeaderSnapshot(c *gin.Context, key string) OpsHeaderSnapshot {
	if c == nil {
		return nil
	}
	v, ok := c.Get(key)
	if !ok {
		return nil
	}
	snapshot, _ := v.(OpsHeaderSnapshot)
	return snapshot
}
