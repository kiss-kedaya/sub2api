package repository

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUserRepositoryTotpConcurrentBindingHasOnlyOneWinner(t *testing.T) {
	repo, client := newUserEntRepo(t)
	// SQLite's in-memory pool needs one connection for deterministic write
	// serialization; every contender still executes the conditional UPDATE.
	sqlDB, ok := repo.sql.(*sql.DB)
	require.True(t, ok)
	sqlDB.SetMaxOpenConns(1)
	ctx := context.Background()
	user, err := client.User.Create().SetEmail("bind-race@example.com").SetPasswordHash("hash").SetRole(service.RoleAdmin).SetStatus(service.StatusActive).Save(ctx)
	require.NoError(t, err)
	type result struct {
		secret string
		err    error
	}
	start := make(chan struct{})
	results := make(chan result, 8)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			secret := fmt.Sprintf("encrypted-secret-%d", i)
			results <- result{secret, repo.BindTotpIfDisabled(ctx, user.ID, secret)}
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	winners := 0
	winner := ""
	for outcome := range results {
		if outcome.err == nil {
			winners++
			winner = outcome.secret
		} else {
			require.ErrorIs(t, outcome.err, service.ErrUserSecurityStateChanged)
		}
	}
	require.Equal(t, 1, winners)
	got, err := client.User.Get(ctx, user.ID)
	require.NoError(t, err)
	require.True(t, got.TotpEnabled)
	require.NotNil(t, got.TotpEnabledAt)
	require.NotNil(t, got.TotpSecretEncrypted)
	require.Equal(t, winner, *got.TotpSecretEncrypted)
}

func TestUserRepositoryTotpBindingPreservesEnabledOrInactiveIdentity(t *testing.T) {
	for _, tc := range []struct {
		name    string
		enabled bool
		status  string
		deleted bool
	}{
		{name: "already_enabled", enabled: true, status: service.StatusActive},
		{name: "inactive", status: service.StatusDisabled},
		{name: "soft_deleted", status: service.StatusActive, deleted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, client := newUserEntRepo(t)
			ctx := context.Background()
			create := client.User.Create().SetEmail("bind-guard@example.com").SetPasswordHash("hash").SetRole(service.RoleAdmin).SetStatus(tc.status).SetTotpEnabled(tc.enabled).SetTotpSecretEncrypted("original-secret")
			if tc.deleted {
				create = create.SetDeletedAt(time.Now())
			}
			user, err := create.Save(ctx)
			require.NoError(t, err)
			require.ErrorIs(t, repo.BindTotpIfDisabled(ctx, user.ID, "replacement-secret"), service.ErrUserSecurityStateChanged)
			var secret string
			var enabled bool
			rows, err := repo.sql.QueryContext(ctx, "SELECT totp_enabled, totp_secret_encrypted FROM users WHERE id = ?", user.ID)
			require.NoError(t, err)
			defer func() { _ = rows.Close() }()
			require.True(t, rows.Next())
			require.NoError(t, rows.Scan(&enabled, &secret))
			require.NoError(t, rows.Err())
			require.Equal(t, tc.enabled, enabled)
			require.Equal(t, "original-secret", secret)
		})
	}
}

func TestUserRepositoryTotpBindingDatabaseFailureLeavesNoPartialBinding(t *testing.T) {
	repo, client := newUserEntRepo(t)
	ctx := context.Background()
	user, err := client.User.Create().SetEmail("bind-failure@example.com").SetPasswordHash("hash").SetStatus(service.StatusActive).Save(ctx)
	require.NoError(t, err)
	_, err = repo.sql.ExecContext(ctx, `CREATE TRIGGER reject_totp_enable BEFORE UPDATE ON users WHEN NEW.totp_enabled = 1 BEGIN SELECT RAISE(ABORT, 'simulated database write failure'); END`)
	require.NoError(t, err)
	require.Error(t, repo.BindTotpIfDisabled(ctx, user.ID, "must-not-persist"))
	got, err := client.User.Get(ctx, user.ID)
	require.NoError(t, err)
	require.False(t, got.TotpEnabled)
	require.Nil(t, got.TotpSecretEncrypted)
	require.Nil(t, got.TotpEnabledAt)
}

func TestUserRepositoryTotpDisableRejectsReboundSecret(t *testing.T) {
	repo, client := newUserEntRepo(t)
	ctx := context.Background()
	user, err := client.User.Create().SetEmail("disable-rebind@example.com").SetPasswordHash("hash").SetRole(service.RoleAdmin).SetStatus(service.StatusActive).SetTotpEnabled(true).SetTotpSecretEncrypted("verified-old-secret").Save(ctx)
	require.NoError(t, err)
	verifiedSecret := "verified-old-secret"
	_, err = client.User.UpdateOneID(user.ID).SetTotpSecretEncrypted("newly-bound-secret").Save(ctx)
	require.NoError(t, err)
	require.ErrorIs(t, repo.DisableTotpWithExpectedRole(ctx, user.ID, service.RoleAdmin, &verifiedSecret), service.ErrUserSecurityStateChanged)
	got, err := client.User.Get(ctx, user.ID)
	require.NoError(t, err)
	require.True(t, got.TotpEnabled)
	require.NotNil(t, got.TotpSecretEncrypted)
	require.Equal(t, "newly-bound-secret", *got.TotpSecretEncrypted)
}
