//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func credentialGuardDatabaseFixture(t *testing.T) (*credentialGuardRepository, int64) {
	t.Helper()
	ctx := context.Background()
	var id int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO accounts(name,platform,type,status,schedulable,credentials) VALUES($1,'openai','oauth','active',TRUE,'{"access_token":"old-token"}') RETURNING id`, "guard-"+uuid.NewString()).Scan(&id))
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM scheduler_outbox WHERE account_id=$1`, id)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE id=$1`, id)
	})
	return &credentialGuardRepository{db: integrationDB}, id
}
func TestCredentialGuardRepositoryClaimsBudgetAndCAS(t *testing.T) {
	r, id := credentialGuardDatabaseFixture(t)
	ctx := context.Background()
	cfg := service.DefaultCredentialGuardConfig()
	cfg.Enabled = true
	cfg.AutoRelogin = true
	cfg.ReloginEndpoint = "https://login.example.com/relogin"
	cfg.AllowedOrigin = "https://login.example.com"
	cfg.LoginEmail = "operator@example.com"
	row := &service.CredentialGuardRecord{AccountID: id, Config: cfg, Revision: uuid.NewString(), SecretCipher: "encrypted-fixture"}
	require.NoError(t, r.Save(ctx, row, "", false))
	claim, err := r.Claim(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, claim)
	require.Equal(t, row.Revision, claim.Revision)
	busy, err := r.Claim(ctx, id)
	require.NoError(t, err)
	require.Nil(t, busy, "only one replica may inspect an account")
	require.Error(t, r.Save(ctx, &service.CredentialGuardRecord{AccountID: id, Config: cfg, Revision: uuid.NewString()}, row.Revision, false), "config changes cannot race an active login")
	reserved, err := r.ReserveRelogin(ctx, claim)
	require.NoError(t, err)
	require.True(t, reserved)
	reserved, err = r.ReserveRelogin(ctx, claim)
	require.NoError(t, err)
	require.False(t, reserved, "cooldown survives replicas")
	a := &service.Account{ID: id, Credentials: map[string]any{"access_token": "old-token"}}
	applied, err := r.PersistCredentials(ctx, claim, a, map[string]any{"access_token": "new-token"})
	require.NoError(t, err)
	require.True(t, applied)
	applied, err = r.PersistCredentials(ctx, claim, a, map[string]any{"access_token": "stale-token"})
	require.NoError(t, err)
	require.False(t, applied, "stale login must not overwrite a rotated credential")
	var raw []byte
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT credentials FROM accounts WHERE id=$1`, id).Scan(&raw))
	var actual map[string]any
	require.NoError(t, json.Unmarshal(raw, &actual))
	require.Equal(t, "new-token", actual["access_token"])
	a.Credentials = actual
	_, err = integrationDB.ExecContext(ctx, `UPDATE accounts SET schedulable=FALSE WHERE id=$1`, id)
	require.NoError(t, err)
	applied, err = r.PersistCredentials(ctx, claim, a, map[string]any{"access_token": "forbidden-token"})
	require.NoError(t, err)
	require.False(t, applied, "a manually disabled account remains disabled")
	require.NoError(t, r.Complete(ctx, claim, service.CredentialGuardCompletion{State: "manual_required", Code: "security_verification_required", Blocked: true, Next: time.Now().Add(time.Hour)}))
	stored, err := r.Get(ctx, id)
	require.NoError(t, err)
	require.True(t, stored.Blocked)
	require.Equal(t, 1, stored.Failures)
	require.Empty(t, stored.Lease)
	var eventCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM credential_guard_events WHERE account_id=$1`, id).Scan(&eventCount))
	require.Equal(t, 1, eventCount)
	require.NoError(t, r.Complete(ctx, claim, service.CredentialGuardCompletion{State: "healthy", Code: "stale_success", Next: time.Now()}))
	stored, err = r.Get(ctx, id)
	require.NoError(t, err)
	require.True(t, stored.Blocked)
	require.Equal(t, "security_verification_required", stored.Code)
}
