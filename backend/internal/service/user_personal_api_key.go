package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	// UserPersonalAPIKeyPrefix 是用户个人接口密钥的固定前缀（User Access Key）。
	// 与网关推理密钥（sk-）和管理员密钥（adm_sc_ / admin-）彻底隔离，避免跨通道误用。
	UserPersonalAPIKeyPrefix = "uak_"
	// userPersonalAPIKeyRandomBytes 为 256-bit CSPRNG 随机熵。
	userPersonalAPIKeyRandomBytes = 32
	// UserPersonalAPIKeyLength 是合法个人接口密钥的固定长度（"uak_" + 64 hex = 68）。
	UserPersonalAPIKeyLength = len(UserPersonalAPIKeyPrefix) + userPersonalAPIKeyRandomBytes*2

	// UserPersonalAPIKeyFallbackReadRPM / WriteRPM 是全局面板限流关闭时的强制兜底限流，
	// 保证个人接口密钥始终处于限流保护下，不会被自动化脚本打爆数据库。
	UserPersonalAPIKeyFallbackReadRPM  = 120
	UserPersonalAPIKeyFallbackWriteRPM = 30

	// UserPersonalAPIKeyInvalidIPMaxAttempts 是单客户端 IP 在滚动窗口内允许的无效个人密钥尝试次数。
	UserPersonalAPIKeyInvalidIPMaxAttempts = 20
	// UserPersonalAPIKeyInvalidIPWindow 是无效个人密钥尝试的滚动窗口。
	UserPersonalAPIKeyInvalidIPWindow = 60 * time.Second

	userPersonalAPIKeyTouchInterval = time.Minute
	userPersonalAPIKeyTouchTimeout  = 2 * time.Second
	userPersonalAPIKeyTouchMaxConc  = 16
	userPersonalAPIKeyTouchMaxCache = 10000
	userPersonalAPIKeyMaxIPLen      = 64
)

var (
	// ErrUserPersonalAPIKeyNotFound 表示该用户尚未生成个人接口密钥。
	ErrUserPersonalAPIKeyNotFound = infraerrors.NotFound("PERSONAL_API_KEY_NOT_FOUND", "Personal API key not found")
	// ErrUserPersonalAPIKeyInvalid 表示个人接口密钥无效或已吊销（统一 401，不区分不存在与错误）。
	ErrUserPersonalAPIKeyInvalid = infraerrors.Unauthorized("INVALID_PERSONAL_API_KEY", "Invalid personal API key")
	// ErrUserPersonalAPIKeyUnavailable 表示个人接口密钥存储未装配或暂时不可用。
	ErrUserPersonalAPIKeyUnavailable = infraerrors.ServiceUnavailable("PERSONAL_API_KEY_UNAVAILABLE", "Personal API key service is unavailable")
	// ErrUserPersonalAPIKeyForbidden 表示个人接口密钥试图访问仅限交互式会话的账户安全/凭证管理接口。
	ErrUserPersonalAPIKeyForbidden = infraerrors.Forbidden("PERSONAL_API_KEY_FORBIDDEN", "Personal API key cannot access account security or credential management endpoints")
)

// UserPersonalAPIKey 是 user_personal_api_keys 表的领域模型。
type UserPersonalAPIKey struct {
	UserID     int64      `json:"user_id"`
	KeyPrefix  string     `json:"key_prefix"`
	KeyHash    string     `json:"-"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	LastUsedIP string     `json:"last_used_ip,omitempty"`
}

// UserPersonalAPIKeyStatus 是个人资料页展示的状态视图（永不含明文密钥）。
type UserPersonalAPIKeyStatus struct {
	Exists            bool       `json:"exists"`
	KeyPrefix         string     `json:"key_prefix,omitempty"`
	CreatedAt         *time.Time `json:"created_at,omitempty"`
	UpdatedAt         *time.Time `json:"updated_at,omitempty"`
	LastUsedAt        *time.Time `json:"last_used_at,omitempty"`
	LastUsedIP        string     `json:"last_used_ip,omitempty"`
	RateLimitRPM      int        `json:"rate_limit_rpm"`
	WriteRateLimitRPM int        `json:"write_rate_limit_rpm"`
}

// UserPersonalAPIKeyRepository 定义个人接口密钥的持久化端口。
type UserPersonalAPIKeyRepository interface {
	GetByUserID(ctx context.Context, userID int64) (*UserPersonalAPIKey, error)
	GetByHash(ctx context.Context, keyHash string) (*UserPersonalAPIKey, error)
	Upsert(ctx context.Context, key *UserPersonalAPIKey) (replaced bool, err error)
	DeleteByUserID(ctx context.Context, userID int64) (deleted bool, err error)
	TouchLastUsed(ctx context.Context, userID int64, expectedHash, clientIP string, usedAt time.Time) error
}

// IsUserPersonalAPIKeyPrefix 判断字符串是否以个人接口密钥前缀开头。
func IsUserPersonalAPIKeyPrefix(raw string) bool {
	return strings.HasPrefix(strings.TrimSpace(raw), UserPersonalAPIKeyPrefix)
}

// IsUserPersonalAPIKeyFormat 严格校验个人接口密钥格式（前缀 + 64 位十六进制），
// 用于在查库前快速丢弃畸形探针输入。
func IsUserPersonalAPIKeyFormat(raw string) bool {
	raw = strings.TrimSpace(raw)
	if len(raw) != UserPersonalAPIKeyLength || !strings.HasPrefix(raw, UserPersonalAPIKeyPrefix) {
		return false
	}
	body := raw[len(UserPersonalAPIKeyPrefix):]
	for i := 0; i < len(body); i++ {
		c := body[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// HashUserPersonalAPIKey 计算个人接口密钥明文的 SHA-256 小写 hex 摘要。
func HashUserPersonalAPIKey(raw string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(raw)))
	return hex.EncodeToString(sum[:])
}

// MaskUserPersonalAPIKey 生成可安全展示的脱敏前缀（uak_xxxxxxxx...yyyy）。
func MaskUserPersonalAPIKey(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if len(raw) <= 16 {
		return UserPersonalAPIKeyPrefix + "****"
	}
	return raw[:12] + "..." + raw[len(raw)-4:]
}

// UserPersonalAPIKeyService 管理用户个人接口密钥的生成、吊销、哈希鉴权与最近使用记录。
type UserPersonalAPIKeyService struct {
	repo           UserPersonalAPIKeyRepository
	settingService *SettingService

	touchMu   sync.Mutex
	lastTouch map[int64]time.Time
	touchSem  chan struct{}
}

// NewUserPersonalAPIKeyService 创建个人接口密钥服务。
func NewUserPersonalAPIKeyService(
	repo UserPersonalAPIKeyRepository,
	settingService *SettingService,
) *UserPersonalAPIKeyService {
	return &UserPersonalAPIKeyService{
		repo:           repo,
		settingService: settingService,
		lastTouch:      make(map[int64]time.Time),
		touchSem:       make(chan struct{}, userPersonalAPIKeyTouchMaxConc),
	}
}

// EffectiveRateLimits 返回个人接口密钥当前生效的读/写每分钟限流值。
// 即使全局面板限流开关被关闭，个人接口密钥也始终应用保底限流。
func (s *UserPersonalAPIKeyService) EffectiveRateLimits(ctx context.Context) (readRPM, writeRPM int) {
	readRPM = UserPersonalAPIKeyFallbackReadRPM
	writeRPM = UserPersonalAPIKeyFallbackWriteRPM
	if s != nil && s.settingService != nil {
		settings := s.settingService.GetPanelRateLimitSettingsCached(ctx)
		if settings.Enabled {
			if settings.UserRPM > 0 {
				readRPM = settings.UserRPM
			}
			if settings.HeavyRPM > 0 {
				writeRPM = settings.HeavyRPM
			}
		}
	}
	if writeRPM > readRPM {
		writeRPM = readRPM
	}
	return readRPM, writeRPM
}

// GetStatus 返回指定用户个人接口密钥的当前状态（永不返回明文）。
func (s *UserPersonalAPIKeyService) GetStatus(ctx context.Context, userID int64) (*UserPersonalAPIKeyStatus, error) {
	if s == nil || s.repo == nil || userID <= 0 {
		return nil, ErrUserPersonalAPIKeyUnavailable
	}
	readRPM, writeRPM := s.EffectiveRateLimits(ctx)
	record, err := s.repo.GetByUserID(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrUserPersonalAPIKeyNotFound) {
			return &UserPersonalAPIKeyStatus{
				Exists:            false,
				RateLimitRPM:      readRPM,
				WriteRateLimitRPM: writeRPM,
			}, nil
		}
		return nil, fmt.Errorf("get user personal api key status: %w", err)
	}
	return statusFromPersonalAPIKey(record, readRPM, writeRPM), nil
}

// CreateOrRegenerate 为用户创建或重置个人接口密钥。
// 数据库仅保存 SHA-256 哈希与脱敏前缀，明文只在本次返回值中出现一次。
func (s *UserPersonalAPIKeyService) CreateOrRegenerate(ctx context.Context, userID int64) (string, *UserPersonalAPIKeyStatus, bool, error) {
	if s == nil || s.repo == nil || userID <= 0 {
		return "", nil, false, ErrUserPersonalAPIKeyUnavailable
	}
	rawBytes := make([]byte, userPersonalAPIKeyRandomBytes)
	if _, err := rand.Read(rawBytes); err != nil {
		return "", nil, false, fmt.Errorf("generate personal api key random bytes: %w", err)
	}
	plaintext := UserPersonalAPIKeyPrefix + hex.EncodeToString(rawBytes)
	now := time.Now().UTC()
	record := &UserPersonalAPIKey{
		UserID:    userID,
		KeyPrefix: MaskUserPersonalAPIKey(plaintext),
		KeyHash:   HashUserPersonalAPIKey(plaintext),
		CreatedAt: now,
		UpdatedAt: now,
	}
	replaced, err := s.repo.Upsert(ctx, record)
	if err != nil {
		return "", nil, false, fmt.Errorf("upsert user personal api key: %w", err)
	}
	// 清掉去抖缓存，让新密钥首次调用即写入 last_used_at / last_used_ip。
	s.clearTouchDebounce(userID)
	readRPM, writeRPM := s.EffectiveRateLimits(ctx)
	return plaintext, statusFromPersonalAPIKey(record, readRPM, writeRPM), replaced, nil
}

// Delete 立即吊销并删除用户的个人接口密钥。
func (s *UserPersonalAPIKeyService) Delete(ctx context.Context, userID int64) (bool, error) {
	if s == nil || s.repo == nil || userID <= 0 {
		return false, ErrUserPersonalAPIKeyUnavailable
	}
	deleted, err := s.repo.DeleteByUserID(ctx, userID)
	if err != nil {
		return false, fmt.Errorf("delete user personal api key: %w", err)
	}
	s.clearTouchDebounce(userID)
	return deleted, nil
}

func (s *UserPersonalAPIKeyService) clearTouchDebounce(userID int64) {
	s.touchMu.Lock()
	delete(s.lastTouch, userID)
	s.touchMu.Unlock()
}

// Authenticate 按 SHA-256 哈希校验个人接口密钥并在常数时间内比对。
// 校验通过后异步去抖更新 last_used_at / last_used_ip，不阻塞热路径。
func (s *UserPersonalAPIKeyService) Authenticate(ctx context.Context, rawKey, clientIP string) (*UserPersonalAPIKey, error) {
	if s == nil || s.repo == nil {
		return nil, ErrUserPersonalAPIKeyUnavailable
	}
	rawKey = strings.TrimSpace(rawKey)
	if !IsUserPersonalAPIKeyFormat(rawKey) {
		return nil, ErrUserPersonalAPIKeyInvalid
	}
	computedHash := HashUserPersonalAPIKey(rawKey)
	record, err := s.repo.GetByHash(ctx, computedHash)
	if err != nil {
		if errors.Is(err, ErrUserPersonalAPIKeyNotFound) {
			return nil, ErrUserPersonalAPIKeyInvalid
		}
		return nil, fmt.Errorf("authenticate user personal api key: %w", err)
	}
	if record == nil || subtle.ConstantTimeCompare([]byte(computedHash), []byte(strings.TrimSpace(record.KeyHash))) != 1 {
		return nil, ErrUserPersonalAPIKeyInvalid
	}
	s.touchLastUsedAsync(record.UserID, strings.TrimSpace(record.KeyHash), clientIP)
	return record, nil
}

func (s *UserPersonalAPIKeyService) touchLastUsedAsync(userID int64, expectedHash, clientIP string) {
	if s == nil || s.repo == nil || userID <= 0 || expectedHash == "" || CryptoAdminTestMode() {
		return
	}
	now := time.Now().UTC()

	// 先抢占并发槽再在锁内原子更新去抖时间戳：
	// 1. 信号量满时直接放弃，不污染 60s 去抖窗口；
	// 2. 分钟边界的并发请求只会放行一条 DB UPDATE；
	// 3. 超过上限时清理过期条目，避免长期运行内存无界增长。
	select {
	case s.touchSem <- struct{}{}:
	default:
		return
	}

	s.touchMu.Lock()
	if prev, ok := s.lastTouch[userID]; ok && now.Sub(prev) < userPersonalAPIKeyTouchInterval {
		s.touchMu.Unlock()
		<-s.touchSem
		return
	}
	if len(s.lastTouch) >= userPersonalAPIKeyTouchMaxCache {
		cutoff := now.Add(-2 * userPersonalAPIKeyTouchInterval)
		for uid, ts := range s.lastTouch {
			if ts.Before(cutoff) {
				delete(s.lastTouch, uid)
			}
		}
		// 若极端高活跃场景下仍满，直接重置防 OOM。
		if len(s.lastTouch) >= userPersonalAPIKeyTouchMaxCache {
			clear(s.lastTouch)
		}
	}
	s.lastTouch[userID] = now
	s.touchMu.Unlock()

	ip := strings.TrimSpace(clientIP)
	if len(ip) > userPersonalAPIKeyMaxIPLen {
		ip = ip[:userPersonalAPIKeyMaxIPLen]
	}

	go func() {
		defer func() { <-s.touchSem }()
		touchCtx, cancel := context.WithTimeout(context.Background(), userPersonalAPIKeyTouchTimeout)
		defer cancel()
		if err := s.repo.TouchLastUsed(touchCtx, userID, expectedHash, ip, now); err != nil {
			slog.Warn("failed to touch user personal api key last_used", "user_id", userID, "error", err)
		}
	}()
}

func statusFromPersonalAPIKey(record *UserPersonalAPIKey, readRPM, writeRPM int) *UserPersonalAPIKeyStatus {
	if record == nil {
		return &UserPersonalAPIKeyStatus{
			Exists:            false,
			RateLimitRPM:      readRPM,
			WriteRateLimitRPM: writeRPM,
		}
	}
	createdAt := record.CreatedAt
	updatedAt := record.UpdatedAt
	return &UserPersonalAPIKeyStatus{
		Exists:            true,
		KeyPrefix:         record.KeyPrefix,
		CreatedAt:         &createdAt,
		UpdatedAt:         &updatedAt,
		LastUsedAt:        record.LastUsedAt,
		LastUsedIP:        record.LastUsedIP,
		RateLimitRPM:      readRPM,
		WriteRateLimitRPM: writeRPM,
	}
}
