package service

import (
	"context"
	"errors"
	"time"
)

// ErrRefreshTokenNotFound is returned when a refresh token is not found in cache.
// This is used to abstract away the underlying cache implementation (e.g., redis.Nil).
var ErrRefreshTokenNotFound = errors.New("refresh token not found")

// RefreshTokenData 存储在Redis中的Refresh Token数据
type RefreshTokenData struct {
	UserID       int64  `json:"user_id"`
	TokenVersion int64  `json:"token_version"`          // 用于检测密码更改后的Token失效
	FamilyID     string `json:"family_id"`              // Token家族ID，用于防重放攻击
	BindingHash  string `json:"binding_hash,omitempty"` // 会话指纹哈希（IP+UA），会话绑定开启时校验；墓碑上是「消费它的那个客户端」的指纹
	// BindingCoarse 是粗粒度指纹（浏览器家族|系统家族|IP 家族|网段短哈希），和 BindingHash 并存：
	// BindingHash 保持旧算法，新旧版本混跑时旧实例仍能正确比对；loose 模式下新代码优先用它比较
	// （忽略浏览器/系统版本号，IPv4<->IPv6 切换不算不一致）。旧记录没有该字段，按 BindingHash 比较。
	BindingCoarse string `json:"binding_coarse,omitempty"`
	// SessionStartedAt 是该登录会话首次签发的时间。每次轮换都继承同一个值，
	// 会话绝对寿命上限 = SessionStartedAt + jwt.session_max_lifetime_days；
	// 超过上限必须重新登录。refresh token 本身的有效期是滑动的（每次轮换从「现在」起算，
	// 但不会超过这个上限）。旧数据为空时视为以 CreatedAt 为起点（升级兼容，不延长也不会缩短既有会话）。
	SessionStartedAt time.Time `json:"session_started_at,omitempty"`
	// Consumed 标记该 Token 已因轮换被消费。轮换时保留一条短 TTL 墓碑，
	// 用于再次收到同一 Token 时识别重放并撤销整个 family。
	Consumed bool `json:"consumed,omitempty"`
	// ConsumedAt 是该 Token 被轮换消费的时间（墓碑上的字段）。已消费 Token 在
	// jwt.refresh_token_reuse_leeway_seconds 内、由「消费它的那个客户端」再次出示，
	// 视为响应丢失后的合法重试。旧墓碑没有该字段（零值），按原行为处理：任何重放都撤销整个 family。
	ConsumedAt time.Time `json:"consumed_at,omitempty"`
	// LeewayReissues 是该已消费 Token 在宽限期内被「重发」新令牌的次数。它只由
	// RefreshTokenAtomicStore.ReserveLeewayReissue 在 Redis 里原子地累加（先占名额再发新令牌），
	// 服务层不会读改写它；达到上限后不再重发，按重放处理。
	LeewayReissues int       `json:"leeway_reissues,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	ExpiresAt      time.Time `json:"expires_at"`
}

// RefreshConsumeOutcome 是 RefreshTokenAtomicStore.ConsumeRefreshToken 的结果。
type RefreshConsumeOutcome int

const (
	// RefreshConsumeMissing：令牌记录已不存在——会话被撤销（登出/撤销全部/家族撤销）或令牌已过期。
	RefreshConsumeMissing RefreshConsumeOutcome = iota
	// RefreshConsumeWon：这次调用把令牌改写成了墓碑。
	RefreshConsumeWon
	// RefreshConsumeAlready：令牌已被另一个并发请求抢先消费。
	RefreshConsumeAlready
)

// RefreshLeewayOutcome 是 RefreshTokenAtomicStore.ReserveLeewayReissue 的结果。
type RefreshLeewayOutcome int

const (
	// RefreshLeewayMissing：墓碑已不存在——会话被撤销或墓碑已过期。
	RefreshLeewayMissing RefreshLeewayOutcome = iota
	// RefreshLeewayReserved：占到了一个重发名额（计数已 +1）。
	RefreshLeewayReserved
	// RefreshLeewayCapped：重发名额已用完。
	RefreshLeewayCapped
	// RefreshLeewayNotConsumed：记录存在但不是墓碑（仍是可用令牌），不该走宽限路径。
	RefreshLeewayNotConsumed
)

// RefreshTokenAtomicStore 是 refresh token 存储的可选能力（Redis 实现提供，服务里类型断言；
// 故意不放进 RefreshTokenCache，免得波及所有测试替身）。三类竞态要靠它原子地解决：
//
//   - 撤销落在「新令牌已存好、旧令牌还没标已消费」之间：旧令牌的墓碑不能被盲写回去
//     （写回去并重新计时，宽限就会把它变回凭证，让已登出的会话复活）；
//   - 同一枚已消费令牌被并发出示：重发次数上限必须按原子计数执行，而不是读改写；
//   - 撤销之后的任何写入都必须失败，而不是悄悄复活墓碑。
//
// 缓存没有实现它时，服务层不开重试宽限（任何重放都撤销家族，安全方向），首次轮换的消费退回尽力而为的写入。
type RefreshTokenAtomicStore interface {
	// ConsumeRefreshToken 仅当令牌记录仍存在且尚未被消费时，原子地把它改写成 tombstone（TTL = ttl）。
	ConsumeRefreshToken(ctx context.Context, tokenHash string, tombstone *RefreshTokenData, ttl time.Duration) (RefreshConsumeOutcome, error)
	// ReserveLeewayReissue 原子地占用一个宽限重发名额：墓碑存在、确实已消费、计数 < maxReissues 时计数 +1，
	// 返回新计数；墓碑不存在（已撤销）时失败，不会写出任何东西。
	ReserveLeewayReissue(ctx context.Context, tokenHash string, maxReissues int) (RefreshLeewayOutcome, int, error)
}

// SessionStart 返回会话首次签发时间，兼容升级前没有 session_started_at 的旧数据。
func (d *RefreshTokenData) SessionStart() time.Time {
	if d == nil {
		return time.Time{}
	}
	if !d.SessionStartedAt.IsZero() {
		return d.SessionStartedAt
	}
	return d.CreatedAt
}

// RefreshTokenCache 管理Refresh Token的Redis缓存
// 用于JWT Token刷新机制，支持Token轮转和防重放攻击
//
// Key 格式:
//   - refresh_token:{token_hash}     -> RefreshTokenData (JSON)
//   - user_refresh_tokens:{user_id}  -> Set<token_hash>
//   - token_family:{family_id}       -> Set<token_hash>
type RefreshTokenCache interface {
	// StoreRefreshToken 存储Refresh Token
	// tokenHash: Token的SHA256哈希值（不存储原始Token）
	// data: Token关联的数据
	// ttl: Token过期时间
	StoreRefreshToken(ctx context.Context, tokenHash string, data *RefreshTokenData, ttl time.Duration) error

	// GetRefreshToken 获取Refresh Token数据
	// 返回 (data, nil) 如果Token存在
	// 返回 (nil, ErrRefreshTokenNotFound) 如果Token不存在
	// 返回 (nil, err) 如果发生其他错误
	GetRefreshToken(ctx context.Context, tokenHash string) (*RefreshTokenData, error)

	// DeleteRefreshToken 删除单个Refresh Token
	// 用于Token轮转时使旧Token失效
	DeleteRefreshToken(ctx context.Context, tokenHash string) error

	// DeleteUserRefreshTokens 删除用户的所有Refresh Token
	// 用于密码更改或用户主动登出所有设备
	DeleteUserRefreshTokens(ctx context.Context, userID int64) error

	// DeleteTokenFamily 删除整个Token家族
	// 用于检测到Token重放攻击时，撤销整个会话链
	DeleteTokenFamily(ctx context.Context, familyID string) error

	// AddToUserTokenSet 将Token添加到用户的Token集合
	// 用于跟踪用户的所有活跃Refresh Token
	AddToUserTokenSet(ctx context.Context, userID int64, tokenHash string, ttl time.Duration) error

	// AddToFamilyTokenSet 将Token添加到家族Token集合
	// 用于跟踪同一登录会话的所有Token
	AddToFamilyTokenSet(ctx context.Context, familyID string, tokenHash string, ttl time.Duration) error

	// GetUserTokenHashes 获取用户的所有Token哈希
	// 用于批量删除用户Token
	GetUserTokenHashes(ctx context.Context, userID int64) ([]string, error)

	// GetFamilyTokenHashes 获取家族的所有Token哈希
	// 用于批量删除家族Token
	GetFamilyTokenHashes(ctx context.Context, familyID string) ([]string, error)

	// IsTokenInFamily 检查Token是否属于指定家族
	// 用于验证Token家族关系
	IsTokenInFamily(ctx context.Context, familyID string, tokenHash string) (bool, error)
}
