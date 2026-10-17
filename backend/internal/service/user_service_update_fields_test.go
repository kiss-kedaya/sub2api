//go:build unit

package service

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 这些用例锁死"每个入口只声明自己真正要改的列"：
// 任何退回整行回写的改动都会让并发写入被陈旧快照覆盖，并在这里变红。

func TestUpdateProfile_OnlyDeclaresRequestedColumns(t *testing.T) {
	username := "renamed"
	tests := []struct {
		name string
		req  UpdateProfileRequest
		want UserUpdateFields
	}{
		{
			name: "username only",
			req:  UpdateProfileRequest{Username: &username},
			want: UserUpdateFields{Username: true},
		},
		{
			name: "notify settings only",
			req:  UpdateProfileRequest{BalanceNotifyEnabled: boolPtr(true)},
			want: UserUpdateFields{BalanceNotifySettings: true},
		},
		{
			name: "username and notify threshold",
			req:  UpdateProfileRequest{Username: &username, BalanceNotifyThreshold: float64Ptr(1.5)},
			want: UserUpdateFields{Username: true, BalanceNotifySettings: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &mockUserRepo{getByIDUser: &User{ID: 7, Balance: 0.30, Status: StatusActive}}
			svc := NewUserService(repo, nil, nil, nil)

			_, err := svc.UpdateProfile(context.Background(), 7, tt.req)
			require.NoError(t, err)
			require.Equal(t, []UserUpdateFields{tt.want}, repo.updateFields)
		})
	}
}

// 只改头像时用户行没有任何列要写，不应产生一次整行更新。
func TestUpdateProfile_AvatarOnlySkipsUserRowWrite(t *testing.T) {
	repo := &mockUserRepo{getByIDUser: &User{ID: 7, Balance: 0.30}}
	svc := NewUserService(repo, nil, nil, nil)

	avatar := "https://cdn.example.com/a.png"
	_, err := svc.UpdateProfile(context.Background(), 7, UpdateProfileRequest{AvatarURL: &avatar})
	require.NoError(t, err)
	require.Len(t, repo.upsertAvatarArgs, 1, "avatar must still be stored")
	require.Equal(t, []UserUpdateFields{{}}, repo.updateFields, "no user column should be declared")
}

func TestChangePassword_OnlyDeclaresPasswordHash(t *testing.T) {
	user := &User{ID: 7, Balance: 0.30}
	require.NoError(t, user.SetPassword("old-password"))
	repo := &mockUserRepo{getByIDUser: user}
	svc := NewUserService(repo, nil, nil, nil)

	err := svc.ChangePassword(context.Background(), 7, ChangePasswordRequest{
		CurrentPassword: "old-password",
		NewPassword:     "new-password",
	})
	require.NoError(t, err)
	require.Equal(t, []UserUpdateFields{{PasswordHash: true}}, repo.updateFields)
}

func TestUpdateStatus_OnlyDeclaresStatus(t *testing.T) {
	repo := &mockUserRepo{getByIDUser: &User{ID: 7, Balance: 0.30, Status: StatusActive}}
	svc := NewUserService(repo, nil, nil, nil)

	require.NoError(t, svc.UpdateStatus(context.Background(), 7, StatusDisabled))
	require.Equal(t, []UserUpdateFields{{Status: true}}, repo.updateFields)
}

// 生产事故复盘：外部用户通过 PUT /api/v1/user 写入 <svg onload=...> 用户名；
// 这里锁死该向量：标记字符必须 400，且不能落库。
func TestUpdateProfile_RejectsUnsafeUsername(t *testing.T) {
	repo := &mockUserRepo{getByIDUser: &User{ID: 7, Balance: 0.30, Status: StatusActive}}
	svc := NewUserService(repo, nil, nil, nil)

	xss := `<svg/onload=fetch('//evil.example')>`
	_, err := svc.UpdateProfile(context.Background(), 7, UpdateProfileRequest{Username: &xss})
	require.ErrorIs(t, err, ErrUsernameInvalid)
	require.Empty(t, repo.updateFields, "rejected username must not reach the repository")

	tooLong := strings.Repeat("名", MaxUsernameRunes+1)
	_, err = svc.UpdateProfile(context.Background(), 7, UpdateProfileRequest{Username: &tooLong})
	require.ErrorIs(t, err, ErrUsernameInvalid)
	require.Empty(t, repo.updateFields)
}

// 存量脏数据（历史用户名含被禁字符）回填自身时不拦截，避免历史用户无法保存其它字段。
func TestUpdateProfile_AllowsUnchangedLegacyUsername(t *testing.T) {
	legacy := "<legacy>"
	repo := &mockUserRepo{getByIDUser: &User{ID: 7, Balance: 0.30, Status: StatusActive, Username: legacy}}
	svc := NewUserService(repo, nil, nil, nil)

	_, err := svc.UpdateProfile(context.Background(), 7, UpdateProfileRequest{Username: &legacy})
	require.NoError(t, err)
	require.Equal(t, []UserUpdateFields{{Username: true}}, repo.updateFields)
}

func TestUpdateProfile_RejectsMalformedEmailChange(t *testing.T) {
	repo := &mockUserRepo{getByIDUser: &User{ID: 7, Balance: 0.30, Status: StatusActive, Email: "user@qq.com"}}
	svc := NewUserService(repo, nil, nil, nil)

	for _, email := range []string{"user name@example.com", "user@localhost", strings.Repeat("a", 65) + "@example.com"} {
		t.Run(email, func(t *testing.T) {
			_, err := svc.UpdateProfile(context.Background(), 7, UpdateProfileRequest{Email: &email})
			require.ErrorIs(t, err, ErrEmailFormatInvalid)
		})
	}
	require.Empty(t, repo.updateFields, "rejected email must not reach the repository")
}
