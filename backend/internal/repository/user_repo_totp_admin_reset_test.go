package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// ResetTotpForAdmin 是管理端破玻璃重置 2FA 唯一真正写库的函数。服务层测试用桩仓储，
// 这里在真实的 ent + SQL 上钉住它的写入内容与乐观前置条件：
// 「把 totp_enabled=true 的前置条件翻成 false」「重置后 totp_enabled 仍为 true」这类变异
// 都必须让这些用例变红。

type totpRow struct {
	enabled   bool
	secret    *string
	enabledAt *time.Time
}

func readTotpRow(t *testing.T, repo *userRepository, id int64) totpRow {
	t.Helper()
	// 原生 SQL：不经 ent 的软删除过滤，已删除的行也能读到。
	var (
		row       totpRow
		secret    *string
		enabledAt *time.Time
	)
	rows, err := repo.sql.QueryContext(context.Background(), "SELECT totp_enabled, totp_secret_encrypted, totp_enabled_at FROM users WHERE id = ?", id)
	require.NoError(t, err)
	defer func() { require.NoError(t, rows.Close()) }()
	require.True(t, rows.Next(), "user %d must exist", id)
	require.NoError(t, rows.Scan(&row.enabled, &secret, &enabledAt))
	require.NoError(t, rows.Err())
	row.secret, row.enabledAt = secret, enabledAt
	return row
}

func TestUserRepositoryResetTotpForAdminClearsEveryTotpField(t *testing.T) {
	for _, role := range []string{service.RoleUser, service.RoleAdmin} {
		t.Run(role, func(t *testing.T) {
			repo, client := newUserEntRepo(t)
			ctx := context.Background()
			enabledAt := time.Date(2026, 5, 1, 8, 0, 0, 0, time.UTC)
			target, err := client.User.Create().SetEmail("reset-target@example.test").SetPasswordHash("password-hash").
				SetRole(role).SetStatus(service.StatusActive).
				SetTotpEnabled(true).SetTotpSecretEncrypted("v1:encrypted-secret").SetTotpEnabledAt(enabledAt).Save(ctx)
			require.NoError(t, err)

			require.NoError(t, repo.ResetTotpForAdmin(ctx, target.ID))

			row := readTotpRow(t, repo, target.ID)
			require.False(t, row.enabled, "totp_enabled must be false after the reset")
			require.Nil(t, row.secret, "the secret must be cleared")
			require.Nil(t, row.enabledAt, "the enabled-at stamp must be cleared")
			// 只动 TOTP 三列：密码、角色、状态原样。
			got, err := client.User.Get(ctx, target.ID)
			require.NoError(t, err)
			require.Equal(t, "password-hash", got.PasswordHash)
			require.Equal(t, role, got.Role)
			require.Equal(t, service.StatusActive, got.Status)
		})
	}
}

// 乐观前置条件：目标当前没有启用 TOTP（并发被用户自己关掉了）→ 影响 0 行 →
// ErrUserSecurityStateChanged，而不是假装成功；残留列原样，不被误清。
func TestUserRepositoryResetTotpForAdminRefusesWhenTotpIsNotEnabled(t *testing.T) {
	repo, client := newUserEntRepo(t)
	ctx := context.Background()
	enabledAt := time.Date(2026, 5, 1, 8, 0, 0, 0, time.UTC)
	target, err := client.User.Create().SetEmail("not-enabled@example.test").SetPasswordHash("hash").
		SetStatus(service.StatusActive).
		SetTotpEnabled(false).SetTotpSecretEncrypted("leftover-secret").SetTotpEnabledAt(enabledAt).Save(ctx)
	require.NoError(t, err)

	require.ErrorIs(t, repo.ResetTotpForAdmin(ctx, target.ID), service.ErrUserSecurityStateChanged)

	row := readTotpRow(t, repo, target.ID)
	require.False(t, row.enabled)
	require.NotNil(t, row.secret, "a refused reset must leave the row untouched")
	require.Equal(t, "leftover-secret", *row.secret)
	require.NotNil(t, row.enabledAt)
}

func TestUserRepositoryResetTotpForAdminRefusesSoftDeletedUser(t *testing.T) {
	repo, client := newUserEntRepo(t)
	ctx := context.Background()
	target, err := client.User.Create().SetEmail("deleted@example.test").SetPasswordHash("hash").
		SetStatus(service.StatusActive).SetTotpEnabled(true).SetTotpSecretEncrypted("v1:encrypted-secret").
		SetDeletedAt(time.Now()).Save(ctx)
	require.NoError(t, err)

	require.ErrorIs(t, repo.ResetTotpForAdmin(ctx, target.ID), service.ErrUserSecurityStateChanged)

	row := readTotpRow(t, repo, target.ID)
	require.True(t, row.enabled, "a soft-deleted account must not be modified")
	require.NotNil(t, row.secret)
}

// 只重置目标本人，别的用户的 TOTP 原样。
func TestUserRepositoryResetTotpForAdminOnlyTouchesTheTarget(t *testing.T) {
	repo, client := newUserEntRepo(t)
	ctx := context.Background()
	create := func(email string) int64 {
		user, err := client.User.Create().SetEmail(email).SetPasswordHash("hash").SetStatus(service.StatusActive).
			SetTotpEnabled(true).SetTotpSecretEncrypted("v1:" + email).SetTotpEnabledAt(time.Now()).Save(ctx)
		require.NoError(t, err)
		return user.ID
	}
	target, bystander := create("target@example.test"), create("bystander@example.test")

	require.NoError(t, repo.ResetTotpForAdmin(ctx, target))

	require.False(t, readTotpRow(t, repo, target).enabled)
	other := readTotpRow(t, repo, bystander)
	require.True(t, other.enabled)
	require.NotNil(t, other.secret)
	require.NotNil(t, other.enabledAt)
}

func TestUserRepositoryResetTotpForAdminRejectsInvalidAndUnknownIDs(t *testing.T) {
	repo, _ := newUserEntRepo(t)
	ctx := context.Background()

	require.ErrorIs(t, repo.ResetTotpForAdmin(ctx, 0), service.ErrUserNotFound)
	require.ErrorIs(t, repo.ResetTotpForAdmin(ctx, -7), service.ErrUserNotFound)
	// 不存在的用户：没有行满足前置条件，同样不是「成功」。
	require.ErrorIs(t, repo.ResetTotpForAdmin(ctx, 987654), service.ErrUserSecurityStateChanged)
}
