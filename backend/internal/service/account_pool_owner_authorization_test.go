package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConfiguredPoolOwnerAuthorization(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ctx     context.Context
		target  int64
		blocked bool
	}{
		{"worker unchanged", context.Background(), 7, false},
		{"admin under another configured owner", WithAccountOwnerScope(context.Background(), 8, 7), 7, true},
		{"owner global key", WithAccountOwnerScope(context.Background(), 7, 7), 0, false},
		{"owner self", WithAccountOwnerScope(context.Background(), 7, 7), 7, false},
		{"other admin global key", WithAccountOwnerScope(context.Background(), 8, 7), 0, true},
		{"other admin owner identity", WithAccountOwnerScope(context.Background(), 8, 7), 7, true},
		{"other admin other user", WithAccountOwnerScope(context.Background(), 8, 7), 9, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := RequireConfiguredPoolOwner(tc.ctx, tc.target)
			if tc.blocked {
				require.ErrorIs(t, err, ErrAccountPoolOwnerRequired)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestAdminCannotTakeOverConfiguredPoolOwner(t *testing.T) {
	ctx := WithAccountOwnerScope(context.Background(), 8, 7)
	for _, tc := range []struct {
		name string
		call func(*adminServiceImpl) error
	}{
		{"change password", func(s *adminServiceImpl) error {
			_, err := s.UpdateUser(ctx, 7, &UpdateUserInput{Password: "replacement-test-password"})
			return err
		}},
		{"change email", func(s *adminServiceImpl) error {
			_, err := s.UpdateUser(ctx, 7, &UpdateUserInput{Email: "attacker@example.test"})
			return err
		}},
		{"change role", func(s *adminServiceImpl) error {
			_, err := s.UpdateUser(ctx, 7, &UpdateUserInput{Role: RoleUser})
			return err
		}},
		{"delete", func(s *adminServiceImpl) error { return s.DeleteUser(ctx, 7) }},
		{"bind login identity", func(s *adminServiceImpl) error {
			_, err := s.BindUserAuthIdentity(ctx, 7, AdminBindAuthIdentityInput{ProviderType: "oidc", ProviderKey: "https://example.test", ProviderSubject: "other-login"})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Any dependency access would panic: denial must happen before reads,
			// password writes, auth identity binding or deletion side effects.
			require.ErrorIs(t, tc.call(&adminServiceImpl{}), ErrAccountPoolOwnerRequired)
		})
	}
}
