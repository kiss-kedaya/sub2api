//go:build unit

package service

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type memoryPersonalAPIKeyRepo struct {
	mu          sync.Mutex
	byUser      map[int64]*UserPersonalAPIKey
	byHash      map[string]*UserPersonalAPIKey
	touchCalls  int
	lastTouched *UserPersonalAPIKey
	touchDoneCh chan struct{}
}

func newMemoryPersonalAPIKeyRepo() *memoryPersonalAPIKeyRepo {
	return &memoryPersonalAPIKeyRepo{
		byUser:      make(map[int64]*UserPersonalAPIKey),
		byHash:      make(map[string]*UserPersonalAPIKey),
		touchDoneCh: make(chan struct{}, 8),
	}
}

func (r *memoryPersonalAPIKeyRepo) GetByUserID(_ context.Context, userID int64) (*UserPersonalAPIKey, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	item, ok := r.byUser[userID]
	if !ok {
		return nil, ErrUserPersonalAPIKeyNotFound
	}
	cp := *item
	return &cp, nil
}

func (r *memoryPersonalAPIKeyRepo) GetByHash(_ context.Context, keyHash string) (*UserPersonalAPIKey, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	item, ok := r.byHash[keyHash]
	if !ok {
		return nil, ErrUserPersonalAPIKeyNotFound
	}
	cp := *item
	return &cp, nil
}

func (r *memoryPersonalAPIKeyRepo) Upsert(_ context.Context, key *UserPersonalAPIKey) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	old, existed := r.byUser[key.UserID]
	if existed {
		delete(r.byHash, old.KeyHash)
	}
	cp := *key
	r.byUser[key.UserID] = &cp
	r.byHash[key.KeyHash] = &cp
	return existed, nil
}

func (r *memoryPersonalAPIKeyRepo) DeleteByUserID(_ context.Context, userID int64) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	old, existed := r.byUser[userID]
	if !existed {
		return false, nil
	}
	delete(r.byUser, userID)
	delete(r.byHash, old.KeyHash)
	return true, nil
}

func (r *memoryPersonalAPIKeyRepo) TouchLastUsed(_ context.Context, userID int64, expectedHash, clientIP string, usedAt time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	item, ok := r.byUser[userID]
	if !ok || item.KeyHash != expectedHash {
		return nil
	}
	t := usedAt.UTC()
	item.LastUsedAt = &t
	item.LastUsedIP = clientIP
	r.touchCalls++
	cp := *item
	r.lastTouched = &cp
	select {
	case r.touchDoneCh <- struct{}{}:
	default:
	}
	return nil
}

func TestUserPersonalAPIKeyLifecycleAndHashAuth(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryPersonalAPIKeyRepo()
	svc := NewUserPersonalAPIKeyService(repo, nil)

	// 初始状态不存在，且兜底限流始终生效。
	status, err := svc.GetStatus(ctx, 42)
	require.NoError(t, err)
	require.False(t, status.Exists)
	require.Empty(t, status.KeyPrefix)
	require.Equal(t, UserPersonalAPIKeyFallbackReadRPM, status.RateLimitRPM)
	require.Equal(t, UserPersonalAPIKeyFallbackWriteRPM, status.WriteRateLimitRPM)

	// 创建密钥：返回一次明文，库内只存 SHA-256 哈希与脱敏前缀。
	plain1, status1, replaced1, err := svc.CreateOrRegenerate(ctx, 42)
	require.NoError(t, err)
	require.False(t, replaced1)
	require.True(t, IsUserPersonalAPIKeyFormat(plain1))
	require.True(t, strings.HasPrefix(plain1, UserPersonalAPIKeyPrefix))
	require.Len(t, plain1, UserPersonalAPIKeyLength)
	require.True(t, status1.Exists)
	require.NotEqual(t, plain1, status1.KeyPrefix)
	require.True(t, strings.Contains(status1.KeyPrefix, "..."))

	stored, err := repo.GetByUserID(ctx, 42)
	require.NoError(t, err)
	require.Equal(t, HashUserPersonalAPIKey(plain1), stored.KeyHash)
	require.NotEqual(t, plain1, stored.KeyHash)

	// GetStatus 永远不返回明文。
	statusAgain, err := svc.GetStatus(ctx, 42)
	require.NoError(t, err)
	require.True(t, statusAgain.Exists)
	require.Equal(t, status1.KeyPrefix, statusAgain.KeyPrefix)

	// 哈希鉴权成功，并异步去抖记录 last_used_at / last_used_ip。
	authRecord, err := svc.Authenticate(ctx, plain1, "203.0.113.9")
	require.NoError(t, err)
	require.Equal(t, int64(42), authRecord.UserID)

	select {
	case <-repo.touchDoneCh:
	case <-time.After(2 * time.Second):
		t.Fatal("expected async TouchLastUsed to run")
	}
	repo.mu.Lock()
	require.Equal(t, 1, repo.touchCalls)
	require.Equal(t, "203.0.113.9", repo.lastTouched.LastUsedIP)
	repo.mu.Unlock()

	// 60 秒内重复鉴权不应再次写库（防止热行锁车队）。
	_, err = svc.Authenticate(ctx, plain1, "203.0.113.10")
	require.NoError(t, err)
	time.Sleep(50 * time.Millisecond)
	repo.mu.Lock()
	require.Equal(t, 1, repo.touchCalls)
	repo.mu.Unlock()

	// 重置密钥后旧密钥立即失效，新密钥立即生效。
	plain2, status2, replaced2, err := svc.CreateOrRegenerate(ctx, 42)
	require.NoError(t, err)
	require.True(t, replaced2)
	require.NotEqual(t, plain1, plain2)
	require.NotEqual(t, status1.KeyPrefix, status2.KeyPrefix)

	_, err = svc.Authenticate(ctx, plain1, "203.0.113.9")
	require.ErrorIs(t, err, ErrUserPersonalAPIKeyInvalid)

	authRecord2, err := svc.Authenticate(ctx, plain2, "198.51.100.7")
	require.NoError(t, err)
	require.Equal(t, int64(42), authRecord2.UserID)

	select {
	case <-repo.touchDoneCh:
	case <-time.After(2 * time.Second):
		t.Fatal("expected async TouchLastUsed to run after key regeneration")
	}

	// 删除密钥后立即失效。
	deleted, err := svc.Delete(ctx, 42)
	require.NoError(t, err)
	require.True(t, deleted)

	_, err = svc.Authenticate(ctx, plain2, "198.51.100.7")
	require.ErrorIs(t, err, ErrUserPersonalAPIKeyInvalid)

	deletedAgain, err := svc.Delete(ctx, 42)
	require.NoError(t, err)
	require.False(t, deletedAgain)
}

func TestIsUserPersonalAPIKeyFormat(t *testing.T) {
	valid := UserPersonalAPIKeyPrefix + strings.Repeat("a1", 32)
	require.True(t, IsUserPersonalAPIKeyFormat(valid))
	require.False(t, IsUserPersonalAPIKeyFormat(""))
	require.False(t, IsUserPersonalAPIKeyFormat("adm_sc_"+strings.Repeat("a1", 32)))
	require.False(t, IsUserPersonalAPIKeyFormat("uak_short"))
	require.False(t, IsUserPersonalAPIKeyFormat(UserPersonalAPIKeyPrefix+strings.Repeat("A1", 32)))
	require.False(t, IsUserPersonalAPIKeyFormat(UserPersonalAPIKeyPrefix+strings.Repeat("zz", 32)))
}
