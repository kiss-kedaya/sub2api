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
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

// 旧版全权管理员 API Key（外部系统集成用的 x-api-key）的哈希化存储。
//
// 历史实现把明文 key 直接写进 settings("admin_api_key")，库被读走即等于拿到
// 一个永不触发 2FA 的管理面后门。改造后：
//   - 新建/轮换只落 sha256 哈希（settings "admin_api_key_hash"）与展示前缀
//     （settings "admin_api_key_hint"），明文只在生成时返回一次；
//   - 鉴权用哈希等值比较（常数时间），未配置时与错误 key 返回完全一致的 401；
//   - 历史明文行在启动/状态读取时一次性迁移为哈希并删除明文；
//   - 每次成功调用记录 last_used_at（限流写），由认证中间件异步补记。
const (
	// SettingKeyAdminAPIKeyHash 是旧版全权管理员 API Key 的 SHA-256 摘要。
	SettingKeyAdminAPIKeyHash = "admin_api_key_hash"
	// SettingKeyAdminAPIKeyHint 是旧版密钥的展示前缀（绝不含完整 key）。
	SettingKeyAdminAPIKeyHint = "admin_api_key_hint"
	// SettingKeyAdminAPIKeyLastUsedAt 是旧版密钥最近一次成功使用的时间。
	SettingKeyAdminAPIKeyLastUsedAt = "admin_api_key_last_used_at"
	// adminAPIKeyHintLen 是展示前缀保留的明文字符数（"admin-" + 7 hex）。
	adminAPIKeyHintLen = 12
	// adminAPIKeyLastUsedWriteInterval 是 last_used_at 的最小落库间隔，
	// 避免自动化高频调用把 settings 表写成热点。
	adminAPIKeyLastUsedWriteInterval = time.Minute
)

// HashAdminAPIKey 返回旧版管理员 API Key 明文的 SHA-256 小写 hex 摘要。
// 与用户 API Key 相同的取舍：key 本体是 256 bit CSPRNG 随机值，
// 不存在口令空间，无需加盐；鉴权路径必须可做等值比较。
func HashAdminAPIKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// AdminAPIKeyHint 返回可安全展示的 key 前缀，只用于识别"是哪一把 key"。
func AdminAPIKeyHint(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	if len(key) <= adminAPIKeyHintLen {
		return key[:adminAPIKeyMinInt(len(key), 4)] + strings.Repeat("*", adminAPIKeyMaxInt(len(key)-4, 0))
	}
	return key[:adminAPIKeyHintLen]
}

func adminAPIKeyMinInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func adminAPIKeyMaxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// verifyAdminAPIKeyAgainstHash 常数时间比较明文与落库哈希。
func verifyAdminAPIKeyAgainstHash(key, storedHash string) bool {
	storedHash = strings.TrimSpace(storedHash)
	if storedHash == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(HashAdminAPIKey(key)), []byte(storedHash)) == 1
}

// verifyAdminAPIKeyAgainstPlaintext 只在历史明文迁移完成前使用。
func verifyAdminAPIKeyAgainstPlaintext(key, storedKey string) bool {
	if storedKey == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(key), []byte(storedKey)) == 1
}

// VerifyResult 描述一次旧版管理员 API Key 鉴权的结果。
type VerifyResult struct {
	Verified bool
	// Hint 是展示前缀（可入审计日志），不含完整 key。
	Hint string
	// HashStorage 表示该 key 已经以哈希形式存储（可安全写 last_used_at）。
	HashStorage bool
	// PlaintextLegacyStorage 表示仍命中历史明文存储，需要迁移。
	PlaintextLegacyStorage bool
}

// VerifyAdminAPIKey 校验旧版全权管理员 API Key。
//
// 未配置（哈希与历史明文都不存在）时返回 Verified=false 且 err=nil：调用方
// 统一回 401 INVALID_ADMIN_KEY，不能区分"没配置"与"key 错误"，避免探测。
func (s *SettingService) VerifyAdminAPIKey(ctx context.Context, presented string) (VerifyResult, error) {
	presented = strings.TrimSpace(presented)
	if presented == "" {
		return VerifyResult{}, nil
	}
	storedHash, err := s.settingRepo.GetValue(ctx, SettingKeyAdminAPIKeyHash)
	if err != nil && !errors.Is(err, ErrSettingNotFound) {
		return VerifyResult{}, err
	}
	if strings.TrimSpace(storedHash) != "" {
		hint, hintErr := s.settingRepo.GetValue(ctx, SettingKeyAdminAPIKeyHint)
		if hintErr != nil && !errors.Is(hintErr, ErrSettingNotFound) {
			return VerifyResult{}, hintErr
		}
		return VerifyResult{
			Verified:    verifyAdminAPIKeyAgainstHash(presented, storedHash),
			Hint:        strings.TrimSpace(hint),
			HashStorage: true,
		}, nil
	}

	// 历史明文兼容：只读校验，不在认证热路径上做迁移写入。
	legacy, legacyErr := s.settingRepo.GetValue(ctx, SettingKeyAdminAPIKey)
	if legacyErr != nil && !errors.Is(legacyErr, ErrSettingNotFound) {
		return VerifyResult{}, legacyErr
	}
	if strings.TrimSpace(legacy) == "" {
		return VerifyResult{}, nil
	}
	verified := verifyAdminAPIKeyAgainstPlaintext(presented, legacy)
	if !verified {
		return VerifyResult{}, nil
	}
	return VerifyResult{
		Verified:               true,
		Hint:                   AdminAPIKeyHint(legacy),
		PlaintextLegacyStorage: true,
	}, nil
}

// MigrateAdminAPIKeyStorage 把历史明文旧版 key 迁移为哈希存储并删除明文。
// 返回是否发生了迁移。未配置、已迁移或明文/哈希不一致时不改动数据。
func (s *SettingService) MigrateAdminAPIKeyStorage(ctx context.Context) (bool, error) {
	if _, err := s.settingRepo.GetValue(ctx, SettingKeyAdminAPIKeyHash); err == nil {
		return false, nil
	} else if !errors.Is(err, ErrSettingNotFound) {
		return false, err
	}
	legacy, err := s.settingRepo.GetValue(ctx, SettingKeyAdminAPIKey)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return false, nil
		}
		return false, err
	}
	legacy = strings.TrimSpace(legacy)
	if legacy == "" {
		return false, nil
	}
	if err := s.settingRepo.Set(ctx, SettingKeyAdminAPIKeyHash, HashAdminAPIKey(legacy)); err != nil {
		return false, fmt.Errorf("store admin api key hash: %w", err)
	}
	if err := s.settingRepo.Set(ctx, SettingKeyAdminAPIKeyHint, AdminAPIKeyHint(legacy)); err != nil {
		return false, fmt.Errorf("store admin api key hint: %w", err)
	}
	if err := s.settingRepo.Delete(ctx, SettingKeyAdminAPIKey); err != nil {
		return false, fmt.Errorf("delete plaintext admin api key: %w", err)
	}
	slog.Warn("legacy admin api key migrated to hashed storage; plaintext row deleted",
		"hint", AdminAPIKeyHint(legacy))
	return true, nil
}

// TouchAdminAPIKeyLastUsed 记录旧版 key 最近一次成功使用时间（限流落库）。
// 只在哈希存储模式下调用；调用失败不影响鉴权结果。
func (s *SettingService) TouchAdminAPIKeyLastUsed(ctx context.Context, hint string) error {
	if hint == "" {
		return nil
	}
	now := time.Now().UTC()
	if existing, err := s.settingRepo.GetValue(ctx, SettingKeyAdminAPIKeyLastUsedAt); err == nil {
		if last, parseErr := time.Parse(time.RFC3339, strings.TrimSpace(existing)); parseErr == nil &&
			now.Sub(last) < adminAPIKeyLastUsedWriteInterval {
			return nil
		}
	} else if !errors.Is(err, ErrSettingNotFound) {
		return err
	}
	if err := s.settingRepo.Set(ctx, SettingKeyAdminAPIKeyLastUsedAt, now.Format(time.RFC3339)); err != nil {
		return err
	}
	return nil
}

// AdminAPIKeyConfigured 报告是否存在任一形态的旧版全权 key（哈希或历史明文）。
func (s *SettingService) AdminAPIKeyConfigured(ctx context.Context) (bool, error) {
	if v, err := s.settingRepo.GetValue(ctx, SettingKeyAdminAPIKeyHash); err == nil {
		if strings.TrimSpace(v) != "" {
			return true, nil
		}
	} else if !errors.Is(err, ErrSettingNotFound) {
		return false, err
	}
	if v, err := s.settingRepo.GetValue(ctx, SettingKeyAdminAPIKey); err == nil {
		return strings.TrimSpace(v) != "", nil
	} else if !errors.Is(err, ErrSettingNotFound) {
		return false, err
	}
	return false, nil
}

// DeleteAdminAPIKeyStorage 删除旧版 key 的全部形态（哈希、前缀、last_used、历史明文）。
func (s *SettingService) DeleteAdminAPIKeyStorage(ctx context.Context) error {
	for _, key := range []string{
		SettingKeyAdminAPIKey,
		SettingKeyAdminAPIKeyHash,
		SettingKeyAdminAPIKeyHint,
		SettingKeyAdminAPIKeyLastUsedAt,
	} {
		if err := s.settingRepo.Delete(ctx, key); err != nil && !errors.Is(err, ErrSettingNotFound) {
			return err
		}
	}
	return nil
}

// GenerateAdminAPIKeyHashed 生成一把新的旧版全权 key，只落哈希与前缀，明文仅返回一次。
//
// Deprecated: 旧版全权密钥的新建/轮换入口已退役（handler 固定返回
// LEGACY_ADMIN_KEY_RETIRED）。保留该实现是为了让"只存哈希、只展示一次"的
// 存储契约有唯一落点，供未来若恢复受控轮换时直接复用。
func (s *SettingService) GenerateAdminAPIKeyHashed(ctx context.Context) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate random bytes: %w", err)
	}
	key := AdminAPIKeyPrefix + hex.EncodeToString(raw)
	if err := s.settingRepo.Set(ctx, SettingKeyAdminAPIKeyHash, HashAdminAPIKey(key)); err != nil {
		return "", fmt.Errorf("save admin api key hash: %w", err)
	}
	if err := s.settingRepo.Set(ctx, SettingKeyAdminAPIKeyHint, AdminAPIKeyHint(key)); err != nil {
		return "", fmt.Errorf("save admin api key hint: %w", err)
	}
	// 轮换后旧明文行必须一起清掉，否则旧 key 仍可通过历史明文路径继续使用。
	if err := s.settingRepo.Delete(ctx, SettingKeyAdminAPIKey); err != nil && !errors.Is(err, ErrSettingNotFound) {
		return "", fmt.Errorf("delete legacy plaintext admin api key: %w", err)
	}
	return key, nil
}

// TouchAdminAPIKeyLastUsedAsync 异步补记 last_used_at；不阻塞请求，也不影响鉴权结果。
func (s *SettingService) TouchAdminAPIKeyLastUsedAsync(ctx context.Context, hint string) {
	if s == nil || hint == "" {
		return
	}
	// 用独立 context，避免请求取消后丢写。
	bg := context.Background()
	if requestID, ok := ctx.Value(ctxkey.RequestID).(string); ok && requestID != "" {
		bg = context.WithValue(bg, ctxkey.RequestID, requestID)
	}
	go func() {
		defer func() {
			// 维护性写入不应拖垮进程；显式 recover 只捕获此后台任务。
			if recovered := recover(); recovered != nil {
				slog.Warn("admin api key last_used_at update panicked", "panic", recovered)
			}
		}()
		writeCtx, cancel := context.WithTimeout(bg, 5*time.Second)
		defer cancel()
		if err := s.TouchAdminAPIKeyLastUsed(writeCtx, hint); err != nil {
			slog.Warn("admin api key last_used_at update failed", "error", err)
		}
	}()
}
