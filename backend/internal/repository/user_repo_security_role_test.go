package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUserRepositoryDisableTotpWithExpectedRolePreservesChangedIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, currentRole, status, expectedRole string
	}{
		{name: "promoted", currentRole: service.RoleAdmin, expectedRole: service.RoleUser, status: service.StatusActive},
		{name: "demoted", currentRole: service.RoleUser, expectedRole: service.RoleAdmin, status: service.StatusActive},
		{name: "disabled", currentRole: service.RoleUser, expectedRole: service.RoleUser, status: service.StatusDisabled},
		{name: "invalid_role", currentRole: service.RoleAdmin, expectedRole: "", status: service.StatusActive},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, client := newUserEntRepo(t)
			ctx := context.Background()
			user, err := client.User.Create().SetEmail("disable-race@example.com").SetPasswordHash("hash").
				SetRole(tc.currentRole).SetStatus(tc.status).SetTotpEnabled(true).SetTotpSecretEncrypted("encrypted-secret").Save(ctx)
			require.NoError(t, err)
			err = repo.DisableTotpWithExpectedRole(ctx, user.ID, tc.expectedRole, user.TotpSecretEncrypted)
			require.ErrorIs(t, err, service.ErrUserSecurityStateChanged)
			got, err := client.User.Get(ctx, user.ID)
			require.NoError(t, err)
			require.True(t, got.TotpEnabled)
			require.NotNil(t, got.TotpSecretEncrypted)
			require.Equal(t, "encrypted-secret", *got.TotpSecretEncrypted)
		})
	}
}

func TestUserRepositoryDisableTotpWithMatchingRoleSucceeds(t *testing.T) {
	for _, role := range []string{service.RoleUser, service.RoleAdmin} {
		t.Run(role, func(t *testing.T) {
			repo, client := newUserEntRepo(t)
			ctx := context.Background()
			user, err := client.User.Create().SetEmail("disable-match@example.com").SetPasswordHash("hash").
				SetRole(role).SetStatus(service.StatusActive).SetTotpEnabled(true).SetTotpSecretEncrypted("encrypted-secret").Save(ctx)
			require.NoError(t, err)
			require.NoError(t, repo.DisableTotpWithExpectedRole(ctx, user.ID, role, user.TotpSecretEncrypted))
			got, err := client.User.Get(ctx, user.ID)
			require.NoError(t, err)
			require.False(t, got.TotpEnabled)
			require.Nil(t, got.TotpSecretEncrypted)
			require.Nil(t, got.TotpEnabledAt)
		})
	}
}

func TestUserRepositoryUpdateRolePreconditionRejectsConcurrentPromotion(t *testing.T) {
	repo, client := newUserEntRepo(t)
	ctx := context.Background()
	user := &service.User{Email: "role-race@example.com", Username: "before", PasswordHash: "hash", Role: service.RoleUser, Status: service.StatusActive}
	require.NoError(t, repo.Create(ctx, user))
	stale, err := repo.GetByID(ctx, user.ID)
	require.NoError(t, err)
	_, err = client.User.UpdateOneID(user.ID).SetRole(service.RoleAdmin).Save(ctx)
	require.NoError(t, err)
	stale.Username = "must-not-write"
	err = repo.Update(ctx, stale, service.UserUpdateFields{Role: true, Username: true, ExpectedRole: service.RoleUser})
	require.ErrorIs(t, err, service.ErrUserSecurityStateChanged)
	got, err := client.User.Get(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, service.RoleAdmin, got.Role)
	require.Equal(t, "before", got.Username)
}

func TestUserRepositoryUpdateRolePreconditionAllowsMatchingRole(t *testing.T) {
	for _, role := range []string{service.RoleAdmin, service.RoleUser} {
		t.Run(role, func(t *testing.T) {
			repo, client := newUserEntRepo(t)
			ctx := context.Background()
			user := &service.User{Email: "role-match@example.com", Username: "before", PasswordHash: "hash", Role: role, Status: service.StatusActive}
			require.NoError(t, repo.Create(ctx, user))
			user.Role = service.RoleUser
			user.Username = "after"
			require.NoError(t, repo.Update(ctx, user, service.UserUpdateFields{Role: true, Username: true, ExpectedRole: role}))
			got, err := client.User.Get(ctx, user.ID)
			require.NoError(t, err)
			require.Equal(t, service.RoleUser, got.Role)
			require.Equal(t, "after", got.Username)
		})
	}
}
