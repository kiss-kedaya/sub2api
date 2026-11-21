//go:build unit

package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// adminKeyMemorySettingRepo 是支持读写的 settings 内存实现，供哈希化测试使用。
type adminKeyMemorySettingRepo struct {
	values map[string]string
}

func (r *adminKeyMemorySettingRepo) Get(context.Context, string) (*Setting, error) {
	return nil, ErrSettingNotFound
}
func (r *adminKeyMemorySettingRepo) GetValue(_ context.Context, key string) (string, error) {
	if v, ok := r.values[key]; ok {
		return v, nil
	}
	return "", ErrSettingNotFound
}
func (r *adminKeyMemorySettingRepo) Set(_ context.Context, key, value string) error {
	r.values[key] = value
	return nil
}
func (r *adminKeyMemorySettingRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	out := map[string]string{}
	for _, key := range keys {
		if v, ok := r.values[key]; ok {
			out[key] = v
		}
	}
	return out, nil
}
func (r *adminKeyMemorySettingRepo) SetMultiple(_ context.Context, values map[string]string) error {
	for key, value := range values {
		r.values[key] = value
	}
	return nil
}
func (r *adminKeyMemorySettingRepo) GetAll(context.Context) (map[string]string, error) {
	return r.values, nil
}
func (r *adminKeyMemorySettingRepo) Delete(_ context.Context, key string) error {
	delete(r.values, key)
	return nil
}

func newAdminKeySettingService(values map[string]string) (*SettingService, *adminKeyMemorySettingRepo) {
	repo := &adminKeyMemorySettingRepo{values: values}
	return NewSettingService(repo, &config.Config{}), repo
}

// 生成/轮换只落哈希与前缀：settings 里不能出现明文，明文只返回一次。
func TestGenerateAdminAPIKeyHashedStoresOnlyHash(t *testing.T) {
	svc, repo := newAdminKeySettingService(map[string]string{})
	plaintext, err := svc.GenerateAdminAPIKeyHashed(context.Background())
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(plaintext, AdminAPIKeyPrefix))
	require.NotContains(t, repo.values, SettingKeyAdminAPIKey, "不得回写明文")
	require.Equal(t, HashAdminAPIKey(plaintext), repo.values[SettingKeyAdminAPIKeyHash])
	require.Equal(t, AdminAPIKeyHint(plaintext), repo.values[SettingKeyAdminAPIKeyHint])
	require.Less(t, len(repo.values[SettingKeyAdminAPIKeyHint]), len(plaintext))

	result, err := svc.VerifyAdminAPIKey(context.Background(), plaintext)
	require.NoError(t, err)
	require.True(t, result.Verified)
	require.True(t, result.HashStorage)
	require.Equal(t, AdminAPIKeyHint(plaintext), result.Hint)

	wrong, err := svc.VerifyAdminAPIKey(context.Background(), plaintext+"x")
	require.NoError(t, err)
	require.False(t, wrong.Verified)
}

// 历史明文行仍可鉴权（只读兼容），迁移后明文被删除，只剩哈希。
func TestLegacyPlaintextAdminAPIKeyMigratesToHash(t *testing.T) {
	const plaintext = "admin-0123456789abcdef0123456789abcdef"
	svc, repo := newAdminKeySettingService(map[string]string{SettingKeyAdminAPIKey: plaintext})
	ctx := context.Background()

	result, err := svc.VerifyAdminAPIKey(ctx, plaintext)
	require.NoError(t, err)
	require.True(t, result.Verified)
	require.True(t, result.PlaintextLegacyStorage)
	require.False(t, result.HashStorage)
	require.Equal(t, AdminAPIKeyHint(plaintext), result.Hint)

	migrated, err := svc.MigrateAdminAPIKeyStorage(ctx)
	require.NoError(t, err)
	require.True(t, migrated)
	require.NotContains(t, repo.values, SettingKeyAdminAPIKey, "迁移后必须删除明文")
	require.Equal(t, HashAdminAPIKey(plaintext), repo.values[SettingKeyAdminAPIKeyHash])

	result, err = svc.VerifyAdminAPIKey(ctx, plaintext)
	require.NoError(t, err)
	require.True(t, result.Verified)
	require.True(t, result.HashStorage, "迁移后必须走哈希校验")
}

// 未配置时：鉴权失败且不返回错误，调用方统一 401，不能区分"未配置/错误"。
func TestVerifyAdminAPIKeyUnconfiguredFailsClosed(t *testing.T) {
	svc, _ := newAdminKeySettingService(map[string]string{})
	result, err := svc.VerifyAdminAPIKey(context.Background(), "admin-whatever")
	require.NoError(t, err)
	require.False(t, result.Verified)
	require.False(t, result.HashStorage)
	require.False(t, result.PlaintextLegacyStorage)
	require.Empty(t, result.Hint)

	configured, err := svc.AdminAPIKeyConfigured(context.Background())
	require.NoError(t, err)
	require.False(t, configured)
}

// 状态接口只能返回前缀，绝不回显完整 key。
func TestGetAdminAPIKeyStatusReturnsPrefixOnly(t *testing.T) {
	const plaintext = "admin-0123456789abcdef0123456789abcdef"
	svc, _ := newAdminKeySettingService(map[string]string{SettingKeyAdminAPIKey: plaintext})
	masked, exists, err := svc.GetAdminAPIKeyStatus(context.Background())
	require.NoError(t, err)
	require.True(t, exists)
	require.Contains(t, masked, AdminAPIKeyHint(plaintext))
	require.NotContains(t, masked, plaintext[len(AdminAPIKeyHint(plaintext)):], "不能回显 key 中后段")
}

// last_used_at 限流落库，不会每次调用都写。
func TestTouchAdminAPIKeyLastUsedThrottlesWrites(t *testing.T) {
	svc, repo := newAdminKeySettingService(map[string]string{})
	ctx := context.Background()
	require.NoError(t, svc.TouchAdminAPIKeyLastUsed(ctx, "admin-abc"))
	first := repo.values[SettingKeyAdminAPIKeyLastUsedAt]
	require.NotEmpty(t, first)
	require.NoError(t, svc.TouchAdminAPIKeyLastUsed(ctx, "admin-abc"))
	require.Equal(t, first, repo.values[SettingKeyAdminAPIKeyLastUsedAt], "一分钟内不重复写")
}

// 删除会清掉所有形态，避免只删明文却留下可用的哈希。
func TestDeleteAdminAPIKeyStorageRemovesAllForms(t *testing.T) {
	svc, repo := newAdminKeySettingService(map[string]string{
		SettingKeyAdminAPIKey:           "admin-plain",
		SettingKeyAdminAPIKeyHash:       HashAdminAPIKey("admin-plain"),
		SettingKeyAdminAPIKeyHint:       "admin-plain",
		SettingKeyAdminAPIKeyLastUsedAt: "2026-01-01T00:00:00Z",
	})
	require.NoError(t, svc.DeleteAdminAPIKeyStorage(context.Background()))
	require.Empty(t, repo.values)
}
