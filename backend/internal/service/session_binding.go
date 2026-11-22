package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// ErrSessionBindingMismatch 会话绑定的 IP/UA 发生变化，会话已失效。
var ErrSessionBindingMismatch = infraerrors.Unauthorized("SESSION_BINDING_MISMATCH", "session network fingerprint changed, please login again")

// SessionBinding 会话指纹：登录时的客户端 IP 与 User-Agent。
// 会话绑定开启时，指纹变化即导致会话失效（防止凭证被盗后异地重放）。
// IP 的比较粒度由 security.session_binding_mode 控制：
//   - strict: 完整 IP + UA；
//   - loose: IPv4 按 /24、IPv6 按 /64 归一后 + UA（默认，容忍同网段 IP 漂移）；
//   - off: 不计算绑定（返回空哈希，等价于关闭）。
type SessionBinding struct {
	IP        string
	UserAgent string
}

// Hash 计算严格模式的绑定指纹哈希（完整 IP + UA）。
func (b *SessionBinding) Hash() string {
	return b.HashForMode(config.SessionBindingModeStrict)
}

// HashForMode 按指定模式计算绑定指纹哈希。
// mode 为空或未知时按 strict 处理，保持旧数据/旧测试的行为。
// 归一化后的 IP 与 UA 都为空时返回空串（无法计算绑定，调用方应放行）。
func (b *SessionBinding) HashForMode(mode string) string {
	if b == nil {
		return ""
	}
	if mode == config.SessionBindingModeOff {
		return ""
	}
	ip := strings.TrimSpace(b.IP)
	if mode == config.SessionBindingModeLoose {
		ip = sessionBindingNetworkPrefix(ip)
	}
	ua := strings.TrimSpace(b.UserAgent)
	if ip == "" && ua == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(ip + "\n" + ua))
	return hex.EncodeToString(sum[:16])
}

// sessionBindingNetworkPrefix 把 IP 归一为网段表示：IPv4 保留 /24，
// IPv6 保留 /64；无法解析时退回原始字符串（保守，仍参与哈希）。
func sessionBindingNetworkPrefix(raw string) string {
	ip := net.ParseIP(strings.TrimSpace(raw))
	if ip == nil {
		return strings.TrimSpace(raw)
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.Mask(net.CIDRMask(24, 32)).String() + "/24"
	}
	return ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
}

type sessionBindingCtxKey struct{}

// WithSessionBinding 将会话指纹注入 context（由 HTTP 入口中间件调用）。
func WithSessionBinding(ctx context.Context, binding *SessionBinding) context.Context {
	if binding == nil {
		return ctx
	}
	return context.WithValue(ctx, sessionBindingCtxKey{}, binding)
}

// SessionBindingFromContext 从 context 提取会话指纹；不存在时返回 nil。
func SessionBindingFromContext(ctx context.Context) *SessionBinding {
	if ctx == nil {
		return nil
	}
	binding, _ := ctx.Value(sessionBindingCtxKey{}).(*SessionBinding)
	return binding
}
