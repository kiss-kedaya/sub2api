package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// OAuthLoginCodeTTL 是一次性 OAuth 登录码的有效期。码只用于把浏览器从
// OAuth 回调页无缝送到令牌签发接口，因此窗口很短：即使泄漏，攻击者也
// 必须在 60 秒内抢在浏览器前兑换，且兑换即失效。
const OAuthLoginCodeTTL = 60 * time.Second

// oauthLoginCodeMaxLength 限制兑换请求里的 code 长度，避免异常超长输入。
const oauthLoginCodeMaxLength = 256

// ErrOAuthLoginCodeNotFound 表示一次性登录码不存在、已过期或已被消费。
var ErrOAuthLoginCodeNotFound = infraerrors.Unauthorized(
	"OAUTH_CODE_INVALID",
	"oauth login code is invalid or expired, please login again",
)

// OAuthLoginCodeData 是 Redis 中一次性登录码关联的数据。
type OAuthLoginCodeData struct {
	UserID     int64     `json:"user_id"`
	Provider   string    `json:"provider,omitempty"`
	RedirectTo string    `json:"redirect_to,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// OAuthLoginCodeStore 是一次性 OAuth 登录码的存储抽象（Redis 实现）。
// 与 RefreshTokenCache 分开定义，避免把登录码语义混进会话存储；仓库层的
// Redis 缓存同时实现两者，AuthService 用类型断言取得该能力。
type OAuthLoginCodeStore interface {
	// StoreOAuthLoginCode 写入一次性码（key 为码的哈希），ttl 后自动过期。
	StoreOAuthLoginCode(ctx context.Context, codeHash string, data *OAuthLoginCodeData, ttl time.Duration) error
	// ConsumeOAuthLoginCode 原子地读取并删除一次性码：同一码只能兑换一次。
	ConsumeOAuthLoginCode(ctx context.Context, codeHash string) (*OAuthLoginCodeData, error)
}

func (s *AuthService) oauthLoginCodeStore() (OAuthLoginCodeStore, bool) {
	if s == nil || s.refreshTokenCache == nil {
		return nil, false
	}
	store, ok := s.refreshTokenCache.(OAuthLoginCodeStore)
	return store, ok
}

// OAuthCodeExchangeAvailable 报告当前服务是否具备一次性 code 交换能力
// （Redis 未配置时返回 false，调用方应退回旧的 fragment 流程）。
func (s *AuthService) OAuthCodeExchangeAvailable() bool {
	_, ok := s.oauthLoginCodeStore()
	return ok
}

// CreateOAuthLoginCode 为已通过 OAuth 校验的账号签发一次性登录码。
// 码本身不携带任何令牌；兑换时才按当前请求的会话指纹签发 access/refresh token。
func (s *AuthService) CreateOAuthLoginCode(ctx context.Context, userID int64, provider, redirectTo string) (string, error) {
	store, ok := s.oauthLoginCodeStore()
	if !ok {
		return "", errors.New("oauth login code store is not configured")
	}
	if userID <= 0 {
		return "", errors.New("invalid oauth login user")
	}
	codeBytes := make([]byte, 32)
	if _, err := rand.Read(codeBytes); err != nil {
		return "", fmt.Errorf("generate oauth login code: %w", err)
	}
	rawCode := hex.EncodeToString(codeBytes)
	data := &OAuthLoginCodeData{
		UserID:     userID,
		Provider:   strings.TrimSpace(provider),
		RedirectTo: strings.TrimSpace(redirectTo),
		CreatedAt:  time.Now().UTC(),
	}
	if err := store.StoreOAuthLoginCode(ctx, hashToken(rawCode), data, OAuthLoginCodeTTL); err != nil {
		return "", fmt.Errorf("store oauth login code: %w", err)
	}
	return rawCode, nil
}

// ConsumeOAuthLoginCode 原子消费一次性登录码并返回关联数据；不存在/过期/
// 已消费统一返回 ErrOAuthLoginCodeNotFound，不区分原因（避免探测）。
func (s *AuthService) ConsumeOAuthLoginCode(ctx context.Context, code string) (*OAuthLoginCodeData, error) {
	code = strings.TrimSpace(code)
	if code == "" || len(code) > oauthLoginCodeMaxLength {
		return nil, ErrOAuthLoginCodeNotFound
	}
	store, ok := s.oauthLoginCodeStore()
	if !ok {
		return nil, ErrOAuthLoginCodeNotFound
	}
	data, err := store.ConsumeOAuthLoginCode(ctx, hashToken(code))
	if err != nil {
		if errors.Is(err, ErrOAuthLoginCodeNotFound) {
			return nil, ErrOAuthLoginCodeNotFound
		}
		return nil, err
	}
	if data == nil || data.UserID <= 0 {
		return nil, ErrOAuthLoginCodeNotFound
	}
	return data, nil
}

// GetActiveUserForLogin 加载并校验用户状态，供 OAuth code 兑换等无密码登录路径复用。
func (s *AuthService) GetActiveUserForLogin(ctx context.Context, userID int64) (*User, error) {
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return nil, ErrInvalidToken
		}
		return nil, ErrServiceUnavailable
	}
	if !user.IsActive() {
		return nil, ErrUserNotActive
	}
	return user, nil
}
