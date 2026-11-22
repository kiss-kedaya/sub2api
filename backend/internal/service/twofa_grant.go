package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

// 五分钟 2FA 凭证（two-factor grant）。
//
// 需求：一次动态码校验（登录二次验证 / 敏感操作 step-up / 密码校验入口）通过后，
// 服务端下发一个**不透明随机凭证**，5 分钟内同一账号在同一客户端（IP+UA）上
// 再次登录或校验当前密码时不再要求动态码。
//
// 安全约束（实现即契约）：
//   - Cookie 里只有 32 字节 crypto/rand 的 base64url 随机串：不放 JWT、TOTP
//     种子、密码、user id 或任何可读信息；响应 body 也不回显。
//   - 服务端 Redis 只存 SHA-256(token) 作为 key（`auth:2fa_grant:<hex>`），
//     value 是绑定记录；数据库/日志/审计里都不出现 token 明文。
//   - 绑定 user_id + 客户端 IP + User-Agent 的 SHA-256 前 16 字节；任一不匹配
//     即视为无效并 fail-closed（要求重新输动态码）。
//   - 固定窗口：自签发起 5 分钟，绝对过期，不做无限滑动。使用成功最多重签
//     一次（新凭证与旧凭证共享同一个绝对到期时间），防止无限续期。
//   - Redis 不可用/记录损坏/读失败：一律按「无有效凭证」处理，绝不放行。
const (
	// TwoFactorGrantCookieName 承载不透明凭证的 HttpOnly Cookie 名。
	TwoFactorGrantCookieName = "sub2_2fa_grant"
	// TwoFactorGrantTTL 凭证的固定有效期：自签发起 5 分钟。
	TwoFactorGrantTTL = 5 * time.Minute

	// TwoFactorGrantScopeLogin / TwoFactorGrantScopePassword 记录凭证的签发入口。
	// 两种 scope 都可跨入口复用：老板要的是「一次 2FA，5 分钟内这个账号都免」。
	TwoFactorGrantScopeLogin    = "login"
	TwoFactorGrantScopePassword = "password"

	// 凭证校验的每用户/IP 限速：60 秒滚动窗口最多 10 次。
	twoFactorGrantVerifyLimit  = 10
	twoFactorGrantVerifyWindow = time.Minute

	twoFactorGrantTokenBytes  = 32
	twoFactorGrantMaxTokenLen = 128
)

var (
	// ErrTwoFactorGrantUnavailable 表示凭证存储不可用（Redis 故障/装配缺失）。
	// 调用方必须 fail-closed：按「没有有效凭证」处理，要求重新输动态码。
	ErrTwoFactorGrantUnavailable = infraerrors.ServiceUnavailable(
		"TWO_FACTOR_GRANT_UNAVAILABLE", "two-factor verification service unavailable")
	// ErrPasswordTwoFactorRequired 表示密码校验入口需要先完成动态码校验。
	// 复用 STEP_UP_REQUIRED 机器码：前端已有的全局 TOTP 对话框会自动接管并重试。
	ErrPasswordTwoFactorRequired = infraerrors.Forbidden(
		"STEP_UP_REQUIRED", "two-factor verification is required to verify the account password")

	// 内部拒绝原因（不外发，仅用于审计枚举；对外统一「需要重新验证」）。
	errTwoFactorGrantExpired     = errors.New("two factor grant expired")
	errTwoFactorGrantUser        = errors.New("two factor grant user mismatch")
	errTwoFactorGrantBinding     = errors.New("two factor grant ip/ua mismatch")
	errTwoFactorGrantUnknown     = errors.New("two factor grant unknown")
	errTwoFactorGrantRateLimited = errors.New("two factor grant rate limited")
)

// 审计里使用的拒绝原因枚举（与设计文档一致）。
const (
	TwoFactorGrantReasonExpired          = "expired"
	TwoFactorGrantReasonUserMismatch     = "mismatch_user"
	TwoFactorGrantReasonBindingMismatch  = "mismatch_ipua"
	TwoFactorGrantReasonUnknown          = "unknown"
	TwoFactorGrantReasonRedisUnavailable = "redis_unavailable"
	TwoFactorGrantReasonRateLimited      = "rate_limited"
)

// TwoFactorGrantRecord 是 Redis 里保存的凭证绑定记录。
//
// 这里没有 token、没有 TOTP 种子、没有密码：token 只以 SHA-256 摘要作为 key，
// 记录本身即使泄露也只能看出「某用户在某 IP/UA 指纹上于某时刻签了一个凭证」。
type TwoFactorGrantRecord struct {
	UserID    int64  `json:"user_id"`
	Scope     string `json:"scope"`
	IPHash    string `json:"ip_hash"`
	UAHash    string `json:"ua_hash"`
	IssuedAt  int64  `json:"issued_at"`
	ExpiresAt int64  `json:"expires_at"`
	// Reissued 标记该凭证已经重签过一次：再次使用时只校验、不再重签，避免
	// 借助重签链无限续期（绝对到期时间本来也不变，这里再加一道次数限制）。
	Reissued bool `json:"reissued,omitempty"`
}

// TwoFactorGrantStore 是不透明凭证的持久化端口，由 Redis 实现。
// 与 TotpCache 分开定义：现有测试替身不必实现它；拿不到实现时服务 fail-closed。
type TwoFactorGrantStore interface {
	SaveTwoFactorGrant(ctx context.Context, digest string, record *TwoFactorGrantRecord, ttl time.Duration) error
	// LoadTwoFactorGrant 在 key 不存在时返回 (nil, nil)。
	LoadTwoFactorGrant(ctx context.Context, digest string) (*TwoFactorGrantRecord, error)
	DeleteTwoFactorGrant(ctx context.Context, digest string) error
	// RevokeTwoFactorGrantsForUser 撤销该用户全部凭证（依赖用户维度索引集合，
	// 禁止在热路径 SCAN 整库）。返回删除的凭证数。
	RevokeTwoFactorGrantsForUser(ctx context.Context, userID int64) (int64, error)
	// IncrementTwoFactorGrantVerifyAttempt 做每用户/IP 的窗口计数（60s 滚动）。
	IncrementTwoFactorGrantVerifyAttempt(ctx context.Context, userID int64, ipHash string, window time.Duration) (int64, error)
}

// TwoFactorGrantBinding 是签发/校验时使用的客户端指纹材料。
// IP 必须来自 ip.GetClientIP（可信代理解析后的客户端地址），UA 取请求头。
type TwoFactorGrantBinding struct {
	IP        string
	UserAgent string
}

// TwoFactorGrant 是一次签发或校验成功的结果。Token 永远不会写日志/审计/响应体。
type TwoFactorGrant struct {
	// Token 是交给浏览器的 Cookie 值（签发时是新凭证；校验成功时可能是
	// 重签后的新凭证，也可能是继续有效的旧凭证）。
	Token     string
	Scope     string
	ExpiresAt time.Time
	Remaining time.Duration
}

// twoFactorGrantDigest 计算 token 的服务端存储摘要。Redis key 与索引都用它，
// 因此服务端从不保存 token 明文。
func twoFactorGrantDigest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// twoFactorGrantFingerprint 计算绑定指纹：SHA-256(kind||value) 的前 16 字节。
// kind 参与哈希，避免 IP 与 UA 使用相同原料时互相代入。
func twoFactorGrantFingerprint(kind, value string) string {
	sum := sha256.Sum256([]byte(kind + "\x00" + strings.TrimSpace(value)))
	return hex.EncodeToString(sum[:16])
}

func (b TwoFactorGrantBinding) ipHash() string {
	return twoFactorGrantFingerprint("ip", b.IP)
}

func (b TwoFactorGrantBinding) uaHash() string {
	return twoFactorGrantFingerprint("ua", b.UserAgent)
}

// AuditFingerprints 返回写入审计用的 IP/UA 指纹（与凭证绑定使用同一套摘要）。
// 审计里只落这两个不可逆指纹，不落客户端 IP/UA 原文以外的任何凭证材料。
func (b TwoFactorGrantBinding) AuditFingerprints() (ipHash, uaHash string) {
	return b.ipHash(), b.uaHash()
}

// TwoFactorGrantRejectReason 把内部错误映射成审计原因枚举。
func TwoFactorGrantRejectReason(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, errTwoFactorGrantExpired):
		return TwoFactorGrantReasonExpired
	case errors.Is(err, errTwoFactorGrantUser):
		return TwoFactorGrantReasonUserMismatch
	case errors.Is(err, errTwoFactorGrantBinding):
		return TwoFactorGrantReasonBindingMismatch
	case errors.Is(err, errTwoFactorGrantRateLimited):
		return TwoFactorGrantReasonRateLimited
	case errors.Is(err, ErrTwoFactorGrantUnavailable):
		return TwoFactorGrantReasonRedisUnavailable
	default:
		return TwoFactorGrantReasonUnknown
	}
}

// IsTwoFactorGrantExpiredOrMismatched 报告这次拒绝是否属于「浏览器里那条凭证
// 已经没用」的确定性失效（过期/用户或指纹不匹配/未知）。调用方据此清理
// Cookie；Redis 不可用与限速不清 Cookie，避免把抖动用成登出入口。
func IsTwoFactorGrantExpiredOrMismatched(err error) bool {
	return errors.Is(err, errTwoFactorGrantExpired) ||
		errors.Is(err, errTwoFactorGrantUser) ||
		errors.Is(err, errTwoFactorGrantBinding) ||
		errors.Is(err, errTwoFactorGrantUnknown)
}

// twoFactorGrantStore 取当前 TotpService 背后的凭证存储。测试替身没实现该端口
// 时返回 ErrTwoFactorGrantUnavailable，调用方据此 fail-closed。
func (s *TotpService) twoFactorGrantStore() (TwoFactorGrantStore, error) {
	if s == nil || s.cache == nil {
		return nil, ErrTwoFactorGrantUnavailable
	}
	store, ok := s.cache.(TwoFactorGrantStore)
	if !ok || store == nil {
		return nil, ErrTwoFactorGrantUnavailable
	}
	return store, nil
}

func validTwoFactorGrantScope(scope string) bool {
	return scope == TwoFactorGrantScopeLogin || scope == TwoFactorGrantScopePassword
}

// IssueTwoFactorGrant 在任一次动态码校验成功后签发不透明凭证。
//
// userID 必须来自刚刚完成 TOTP 校验的账号；binding 必须来自本次请求的可信客户端
// 指纹。返回的错误只用于日志/审计，不得回显细节给前端。
func (s *TotpService) IssueTwoFactorGrant(
	ctx context.Context,
	userID int64,
	scope string,
	binding TwoFactorGrantBinding,
) (*TwoFactorGrant, error) {
	if userID <= 0 || !validTwoFactorGrantScope(scope) {
		return nil, ErrTwoFactorGrantUnavailable
	}
	if strings.TrimSpace(binding.IP) == "" {
		// 没有可信客户端 IP 时无法绑定，宁可让用户重新输码也不签发弱凭证。
		return nil, ErrTwoFactorGrantUnavailable
	}
	store, err := s.twoFactorGrantStore()
	if err != nil {
		return nil, err
	}
	token, err := newTwoFactorGrantToken()
	if err != nil {
		return nil, ErrTwoFactorGrantUnavailable
	}
	now := time.Now()
	record := &TwoFactorGrantRecord{
		UserID:    userID,
		Scope:     scope,
		IPHash:    binding.ipHash(),
		UAHash:    binding.uaHash(),
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(TwoFactorGrantTTL).Unix(),
	}
	if err := store.SaveTwoFactorGrant(ctx, twoFactorGrantDigest(token), record, TwoFactorGrantTTL); err != nil {
		return nil, ErrTwoFactorGrantUnavailable
	}
	return &TwoFactorGrant{
		Token:     token,
		Scope:     record.Scope,
		ExpiresAt: time.Unix(record.ExpiresAt, 0),
		Remaining: time.Until(time.Unix(record.ExpiresAt, 0)),
	}, nil
}

// ConsumeTwoFactorGrant 校验浏览器带来的凭证，成功时按需重签一次并返回
// 应该写回 Cookie 的取新值。
//
// 校验顺序刻意固定：限速 → 摘要读取 → 用户绑定 → IP/UA 绑定 → 时间窗。
// 任何一步失败都返回错误，调用方必须 fail-closed（要求重新输动态码）。
func (s *TotpService) ConsumeTwoFactorGrant(
	ctx context.Context,
	token string,
	userID int64,
	binding TwoFactorGrantBinding,
) (*TwoFactorGrant, error) {
	if userID <= 0 {
		return nil, errTwoFactorGrantUnknown
	}
	token = strings.TrimSpace(token)
	if token == "" || len(token) > twoFactorGrantMaxTokenLen {
		return nil, errTwoFactorGrantUnknown
	}
	if strings.TrimSpace(binding.IP) == "" {
		return nil, ErrTwoFactorGrantUnavailable
	}
	store, err := s.twoFactorGrantStore()
	if err != nil {
		return nil, err
	}
	// 限速在读取记录之前：被限速时连 Redis GET 都不做，避免把凭证校验当成
	// 高频探测面。计数失败同样 fail-closed。
	attempts, err := store.IncrementTwoFactorGrantVerifyAttempt(ctx, userID, binding.ipHash(), twoFactorGrantVerifyWindow)
	if err != nil {
		return nil, ErrTwoFactorGrantUnavailable
	}
	if attempts > twoFactorGrantVerifyLimit {
		return nil, errTwoFactorGrantRateLimited
	}

	digest := twoFactorGrantDigest(token)
	record, err := store.LoadTwoFactorGrant(ctx, digest)
	if err != nil {
		return nil, ErrTwoFactorGrantUnavailable
	}
	if record == nil {
		return nil, errTwoFactorGrantUnknown
	}
	if record.UserID != userID {
		return nil, errTwoFactorGrantUser
	}
	if subtle.ConstantTimeCompare([]byte(record.IPHash), []byte(binding.ipHash())) != 1 ||
		subtle.ConstantTimeCompare([]byte(record.UAHash), []byte(binding.uaHash())) != 1 {
		return nil, errTwoFactorGrantBinding
	}
	now := time.Now()
	expiresAt := time.Unix(record.ExpiresAt, 0)
	if record.IssuedAt <= 0 || record.ExpiresAt <= record.IssuedAt ||
		record.ExpiresAt-record.IssuedAt > int64(TwoFactorGrantTTL/time.Second) ||
		record.IssuedAt > now.Unix() || !expiresAt.After(now) {
		return nil, errTwoFactorGrantExpired
	}
	if !validTwoFactorGrantScope(record.Scope) {
		return nil, errTwoFactorGrantUnknown
	}

	result := &TwoFactorGrant{
		Token:     token,
		Scope:     record.Scope,
		ExpiresAt: expiresAt,
		Remaining: time.Until(expiresAt),
	}
	if record.Reissued {
		// 已经重签过一次：不再续签，原凭证继续有效到绝对到期时间。
		return result, nil
	}

	// 重签一次：新 token 与旧记录共享同一个绝对到期时间，绝不延长窗口。
	rotatedToken, err := newTwoFactorGrantToken()
	if err != nil {
		return nil, ErrTwoFactorGrantUnavailable
	}
	rotated := *record
	rotated.Reissued = true
	ttl := time.Until(expiresAt)
	if ttl <= 0 {
		return nil, errTwoFactorGrantExpired
	}
	if err := store.SaveTwoFactorGrant(ctx, twoFactorGrantDigest(rotatedToken), &rotated, ttl); err != nil {
		return nil, ErrTwoFactorGrantUnavailable
	}
	// 旧摘要删除失败不影响安全（它本来就只活到同一个绝对到期时间），只记录。
	if err := store.DeleteTwoFactorGrant(ctx, digest); err != nil {
		logger.LegacyPrintf("service.totp", "delete rotated two-factor grant: user_id=%d err=%v", userID, err)
	}
	result.Token = rotatedToken
	return result, nil
}

// RevokeTwoFactorGrantsForUser 删除该用户全部未过期的 2FA 凭证。
// 密码修改、关闭/重置 2FA、账号禁用、强制下线、登出等场景调用。
func (s *TotpService) RevokeTwoFactorGrantsForUser(ctx context.Context, userID int64) error {
	if userID <= 0 {
		return nil
	}
	store, err := s.twoFactorGrantStore()
	if err != nil {
		return err
	}
	if _, err := store.RevokeTwoFactorGrantsForUser(ctx, userID); err != nil {
		return fmt.Errorf("revoke two-factor grants: %w", err)
	}
	return nil
}

// revokeTwoFactorGrantsQuietly 在安全状态已确认变更之后做尽力撤销：
// Redis 抖动不能把一次成功的密码修改/2FA 关闭变成失败响应，但必须留日志。
func (s *TotpService) revokeTwoFactorGrantsQuietly(ctx context.Context, userID int64) {
	if err := s.RevokeTwoFactorGrantsForUser(ctx, userID); err != nil {
		logger.LegacyPrintf("service.totp",
			"revoke two-factor grants after security change failed: user_id=%d err=%v", userID, err)
	}
}

// newTwoFactorGrantToken 生成 32 字节 crypto/rand 的 base64url 不透明串。
func newTwoFactorGrantToken() (string, error) {
	buf := make([]byte, twoFactorGrantTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// TwoFactorGrantCookieMaxAge 把剩余有效期换算成 Cookie Max-Age（秒，向上取整，
// 至少 1 秒）。Absolute expiry 不变，Cookie 只是跟随服务端记录。
func TwoFactorGrantCookieMaxAge(remaining time.Duration) int {
	seconds := int((remaining + time.Second - 1) / time.Second)
	if seconds < 1 {
		return 1
	}
	if seconds > int(TwoFactorGrantTTL/time.Second) {
		return int(TwoFactorGrantTTL / time.Second)
	}
	return seconds
}
