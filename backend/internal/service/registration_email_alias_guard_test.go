//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRegistrationEmailAliasSyntaxDoesNotConfuseDistinctNonGmailInboxes(t *testing.T) {
	for _, email := range []string{"u.ser@gmail.com", "user+tag@example.com", "USER@googlemail.com."} {
		require.True(t, IsRegistrationEmailAlias(email), email)
	}
	for _, email := range []string{"first.last@example.com", "first.last@qq.com", "user@gmail.com", "invalid"} {
		require.False(t, IsRegistrationEmailAlias(email), email)
	}
}

func TestRegistrationEmailAliasGuardRejectsDirectRegistrationButDefaultsOff(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		repo := &userRepoStub{nextID: 91}
		values := map[string]string{SettingKeyRegistrationEnabled: "true"}
		if enabled {
			values[SettingKeyRegistrationEmailAliasBlockEnabled] = "true"
		}
		svc := newAuthService(repo, values, nil, nil)
		_, user, err := svc.Register(context.Background(), "first.last+tag@gmail.com", "password")
		if enabled {
			require.ErrorIs(t, err, ErrEmailAliasNotAllowed)
			require.Empty(t, repo.created)
		} else {
			require.NoError(t, err)
			require.NotNil(t, user)
		}
	}
}

func TestRegistrationEmailAliasGuardCoversCodeAndOAuthSignupBeforeSending(t *testing.T) {
	svc := newAuthService(&userRepoStub{}, map[string]string{SettingKeyRegistrationEnabled: "true", SettingKeyRegistrationEmailAliasBlockEnabled: "true"}, nil, nil)
	email := "user+tag@example.com"
	require.ErrorIs(t, svc.SendVerifyCode(context.Background(), email), ErrEmailAliasNotAllowed)
	_, err := svc.SendVerifyCodeAsync(context.Background(), email)
	require.ErrorIs(t, err, ErrEmailAliasNotAllowed)
	// All pending OAuth creation paths share this policy function; login is a
	// separate exact-email lookup and intentionally does not call it.
	require.ErrorIs(t, svc.validateRegistrationEmailQuota(context.Background(), email), ErrEmailAliasNotAllowed)
	_, _, err = svc.RegisterVerifiedOAuthEmailAccount(context.Background(), email, "password", "", "google")
	require.ErrorIs(t, err, ErrEmailAliasNotAllowed)
}

func TestRegistrationEmailAliasGuardDoesNotDisableExistingAliasLogin(t *testing.T) {
	user := &User{ID: 91, Email: "first.last+legacy@gmail.com", Role: RoleUser, Status: StatusActive}
	require.NoError(t, user.SetPassword("fixture-password"))
	svc := newAuthService(&userRepoStub{user: user}, map[string]string{SettingKeyRegistrationEmailAliasBlockEnabled: "true"}, nil, nil)
	token, loggedIn, err := svc.Login(context.Background(), user.Email, "fixture-password")
	require.NoError(t, err)
	require.NotEmpty(t, token)
	require.Equal(t, user.ID, loggedIn.ID)
}

func TestRegistrationEmailAliasGuardFailsClosedOnPolicyReadFailure(t *testing.T) {
	repo := &settingRepoStub{err: errors.New("fixture unavailable")}
	svc := &AuthService{settingService: &SettingService{settingRepo: repo}}
	require.ErrorIs(t, svc.validateRegistrationEmailAlias(context.Background(), "user+tag@example.com"), ErrServiceUnavailable)
	require.NoError(t, svc.validateRegistrationEmailAlias(context.Background(), "first.last@example.com"))
}
