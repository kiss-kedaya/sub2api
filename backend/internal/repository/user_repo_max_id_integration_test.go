//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// MaxUserID 只给登录首步伪造 token 的用户 ID 段估算取值上界（service.UserMaxIDReader）：
// 新建用户后返回值不小于其中最大的 ID，软删的用户不参与。
func TestUserRepo_MaxUserID(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()
	repo, ok := NewUserRepository(client, integrationDB).(service.UserMaxIDReader)
	require.True(t, ok, "userRepository 必须实现 service.UserMaxIDReader")

	first := mustCreateUser(t, client, &service.User{Email: "max-id-first@test.com"})
	second := mustCreateUser(t, client, &service.User{Email: "max-id-second@test.com"})
	require.Greater(t, second.ID, first.ID)

	got, err := repo.MaxUserID(ctx)
	require.NoError(t, err)
	require.GreaterOrEqual(t, got, second.ID)

	// 软删最大的那个：不再参与（返回值不会比刚建的 first 更小，也不会还是 second）。
	require.NoError(t, client.User.DeleteOneID(second.ID).Exec(ctx))
	got, err = repo.MaxUserID(ctx)
	require.NoError(t, err)
	require.GreaterOrEqual(t, got, first.ID)
	require.NotEqual(t, second.ID, got)
}
