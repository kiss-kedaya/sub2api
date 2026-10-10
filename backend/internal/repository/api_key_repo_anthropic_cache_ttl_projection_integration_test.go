//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// GetByKeyForAuth 是最小字段投影，网关热路径拿到的 apiKey 就来自这里。
// 密钥级 Anthropic cache TTL 覆盖（api_keys.anthropic_cache_ttl_mode）一旦漏出
// 这份投影，鉴权快照里该字段恒为空 -> 归一化成 inherit -> 「强制 1h 缓存」
// 整条注入链路静默失效，而且没有任何报错。这个测试就是守这一列。
func TestGetByKeyForAuthCarriesAnthropicCacheTTLMode(t *testing.T) {
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	group := mustCreateGroup(t, integrationEntClient, &service.Group{
		Name: fmt.Sprintf("ttl-proj-group-%d", suffix), Platform: service.PlatformAnthropic,
		RateMultiplier: 1,
	})
	user := mustCreateUser(t, integrationEntClient, &service.User{
		Email: fmt.Sprintf("ttl-proj-%d@example.com", suffix), Concurrency: 5,
	})
	groupID := group.ID
	keyValue := fmt.Sprintf("sk-ttl-proj-%d", suffix)
	apiKeyRepo := NewAPIKeyRepository(integrationEntClient, integrationDB)
	key := &service.APIKey{
		UserID: user.ID, GroupID: &groupID, Key: keyValue, Name: "ttl-proj", Status: service.StatusActive,
		AnthropicCacheTTLMode: service.AnthropicCacheTTLMode1h,
	}
	require.NoError(t, apiKeyRepo.Create(ctx, key))
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(ctx, "DELETE FROM auth_cache_invalidation_outbox WHERE cache_key = encode(sha256(convert_to($1, 'UTF8')), 'hex')", keyValue)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, "DELETE FROM api_keys WHERE id = $1", key.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, "DELETE FROM users WHERE id = $1", user.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, "DELETE FROM groups WHERE id = $1", group.ID)
		require.NoError(t, err)
	})

	got, err := apiKeyRepo.GetByKeyForAuth(ctx, keyValue)
	require.NoError(t, err)
	require.Equal(t, service.AnthropicCacheTTLMode1h, got.AnthropicCacheTTLMode,
		"GetByKeyForAuth 必须投影 anthropic_cache_ttl_mode，否则密钥级 TTL 覆盖在热路径上读不到")
}
