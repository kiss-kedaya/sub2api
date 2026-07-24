//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeEmailForAliasDedup(t *testing.T) {
	cases := []struct {
		name  string
		email string
		want  string
	}{
		{"plain", "user@example.com", "user@example.com"},
		{"uppercase and spaces", "  User@Example.COM ", "user@example.com"},
		{"plus alias stripped", "user+tag@example.com", "user@example.com"},
		{"gmail plus alias", "someone+bulk294@gmail.com", "someone@gmail.com"},
		{"gmail dots removed", "some.one@gmail.com", "someone@gmail.com"},
		{"gmail dots and plus", "s.o.m.e+x@gmail.com", "some@gmail.com"},
		{"googlemail folded to gmail", "user@googlemail.com", "user@gmail.com"},
		{"non-gmail keeps dots", "first.last@qq.com", "first.last@qq.com"},
		{"invalid keeps lowered raw", "not-an-email", "not-an-email"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, NormalizeEmailForAliasDedup(tc.email))
		})
	}
}

func TestAliasDedupCandidateDomains(t *testing.T) {
	require.ElementsMatch(t, []string{"gmail.com", "googlemail.com"}, aliasDedupCandidateDomains("user@gmail.com"))
	require.ElementsMatch(t, []string{"gmail.com", "googlemail.com"}, aliasDedupCandidateDomains("user@googlemail.com"))
	require.Equal(t, []string{"qq.com"}, aliasDedupCandidateDomains("user@qq.com"))
	require.Nil(t, aliasDedupCandidateDomains("not-an-email"))
}

// aliasDedupRepoStub implements only the methods alias dedup uses; other
// UserRepository methods come from the embedded nil interface (a wrong call
// would panic, failing the test).
type aliasDedupRepoStub struct {
	UserRepository
	exists    bool
	existsErr error
	emails    []string
	listErr   error
	scanned   [][]string
}

func (s *aliasDedupRepoStub) ExistsByEmail(context.Context, string) (bool, error) {
	return s.exists, s.existsErr
}

func (s *aliasDedupRepoStub) ListEmailsByDomains(_ context.Context, domains []string) ([]string, error) {
	s.scanned = append(s.scanned, domains)
	return s.emails, s.listErr
}

// exactOnlyRepoStub only supports the exact check (no alias-lookup capability).
type exactOnlyRepoStub struct {
	UserRepository
	exists bool
}

func (s *exactOnlyRepoStub) ExistsByEmail(context.Context, string) (bool, error) {
	return s.exists, nil
}

func TestExistsByEmailOrAlias(t *testing.T) {
	ctx := context.Background()

	t.Run("exact duplicate short-circuits", func(t *testing.T) {
		repo := &aliasDedupRepoStub{exists: true}
		svc := &AuthService{userRepo: repo}
		got, err := svc.existsByEmailOrAlias(ctx, "user@gmail.com")
		require.NoError(t, err)
		require.True(t, got)
		require.Empty(t, repo.scanned, "no alias scan expected after exact hit")
	})

	t.Run("plus alias variant detected", func(t *testing.T) {
		repo := &aliasDedupRepoStub{emails: []string{"someone+bulk294@gmail.com"}}
		svc := &AuthService{userRepo: repo}
		got, err := svc.existsByEmailOrAlias(ctx, "Someone@gmail.com")
		require.NoError(t, err)
		require.True(t, got)
	})

	t.Run("gmail dot variant detected", func(t *testing.T) {
		repo := &aliasDedupRepoStub{emails: []string{"some.one@gmail.com"}}
		svc := &AuthService{userRepo: repo}
		got, err := svc.existsByEmailOrAlias(ctx, "someone@gmail.com")
		require.NoError(t, err)
		require.True(t, got)
	})

	t.Run("gmail scans both gmail-family domains", func(t *testing.T) {
		repo := &aliasDedupRepoStub{}
		svc := &AuthService{userRepo: repo}
		_, err := svc.existsByEmailOrAlias(ctx, "user@googlemail.com")
		require.NoError(t, err)
		require.Len(t, repo.scanned, 1)
		require.ElementsMatch(t, []string{"gmail.com", "googlemail.com"}, repo.scanned[0])
	})

	t.Run("different inbox allowed", func(t *testing.T) {
		repo := &aliasDedupRepoStub{emails: []string{"other@gmail.com"}}
		svc := &AuthService{userRepo: repo}
		got, err := svc.existsByEmailOrAlias(ctx, "user@gmail.com")
		require.NoError(t, err)
		require.False(t, got)
	})

	t.Run("list error fails closed", func(t *testing.T) {
		repo := &aliasDedupRepoStub{listErr: errors.New("db down")}
		svc := &AuthService{userRepo: repo}
		_, err := svc.existsByEmailOrAlias(ctx, "user@gmail.com")
		require.Error(t, err)
	})

	t.Run("exact check error propagates", func(t *testing.T) {
		repo := &aliasDedupRepoStub{existsErr: errors.New("db down")}
		svc := &AuthService{userRepo: repo}
		_, err := svc.existsByEmailOrAlias(ctx, "user@gmail.com")
		require.Error(t, err)
	})

	t.Run("repo without capability falls back to exact check", func(t *testing.T) {
		svc := &AuthService{userRepo: &exactOnlyRepoStub{exists: false}}
		got, err := svc.existsByEmailOrAlias(ctx, "user@gmail.com")
		require.NoError(t, err)
		require.False(t, got)
	})
}
