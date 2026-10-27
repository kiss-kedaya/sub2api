//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/service/basispoints"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestExcelGrantRepositoryConfigSessionAndPrimaryCAS(t *testing.T) {
	ctx := context.Background()
	var id int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO accounts(name,platform,type,status,schedulable,credentials) VALUES($1,'openai','oauth','active',TRUE,'{"access_token":"primary"}') RETURNING id`, "excel-"+uuid.NewString()).Scan(&id))
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM scheduler_outbox WHERE account_id=$1`, id)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE id=$1`, id)
	})
	r := &excelGrantRepository{db: integrationDB}
	row := &service.ExcelGrantRecord{AccountID: id, Revision: uuid.NewString(), Config: service.ExcelGrantConfig{Enabled: true, Models: []string{"gpt-fixture"}}}
	require.NoError(t, r.SaveConfig(ctx, row, ""))
	stored, err := r.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, row.Config, stored.Config)
	var enabled bool
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT (extra->>'excel_bps_enabled')::boolean FROM accounts WHERE id=$1`, id).Scan(&enabled))
	require.True(t, enabled)
	a := &service.Account{ID: id, Credentials: map[string]any{"access_token": "primary"}}
	session := service.ExcelGrantSession{ID: uuid.NewString(), AccountID: id, Revision: row.Revision, StateHash: "state-fixture", VerifierCipher: "encrypted-verifier", PrimaryHash: service.ExcelGrantPrimaryHash(a), ExpiresAt: time.Now().Add(time.Minute)}
	require.NoError(t, r.CreateSession(ctx, session))
	claimed, err := r.ConsumeSession(ctx, id, session.ID, "wrong")
	require.NoError(t, err)
	require.Nil(t, claimed)
	claimed, err = r.ConsumeSession(ctx, id, session.ID, session.StateHash)
	require.NoError(t, err)
	require.NotNil(t, claimed)
	claimed, err = r.ConsumeSession(ctx, id, session.ID, session.StateHash)
	require.NoError(t, err)
	require.Nil(t, claimed)
	applied, err := r.StoreGrant(ctx, id, row.Revision, "", service.ExcelGrantPrimaryHash(a), "encrypted-grant", time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.True(t, applied)
	applied, err = r.StoreGrant(ctx, id, row.Revision, "", service.ExcelGrantPrimaryHash(a), "stale-grant", time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.False(t, applied)
	_, err = integrationDB.ExecContext(ctx, `UPDATE accounts SET credentials='{"access_token":"new-primary"}' WHERE id=$1`, id)
	require.NoError(t, err)
	applied, err = r.StoreGrant(ctx, id, row.Revision, "encrypted-grant", service.ExcelGrantPrimaryHash(a), "wrong-primary-grant", time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.False(t, applied)
}
func TestExcelBPSStateEncryptedScopedAndRevisionProtected(t *testing.T) {
	ctx := context.Background()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	r := &excelBPSStateRepository{db: integrationDB, encryptor: &AESEncryptor{key: key}}
	scope := "fixture:" + uuid.NewString()
	other := scope + ":another-key"
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM excel_bps_session_states WHERE scope IN($1,$2)`, excelBPSStateHash(scope), excelBPSStateHash(other))
	})
	expected := uint64(0)
	saved, err := r.SaveBPSState(ctx, scope, "catalog", &expected, []byte(`{"private_tool":"fixture"}`))
	require.NoError(t, err)
	require.True(t, saved)
	raw, revision, err := r.LoadBPSState(ctx, scope, "catalog")
	require.NoError(t, err)
	require.Equal(t, uint64(1), revision)
	require.Contains(t, string(raw), "private_tool")
	var cipher string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT payload_cipher FROM excel_bps_session_states WHERE scope=$1 AND state_key=$2`, excelBPSStateHash(scope), excelBPSStateHash("catalog")).Scan(&cipher))
	require.NotContains(t, cipher, "private_tool")
	raw, _, err = r.LoadBPSState(ctx, other, "catalog")
	require.NoError(t, err)
	require.Empty(t, raw)
	saved, err = r.SaveBPSState(ctx, scope, "catalog", &expected, []byte(`{}`))
	require.NoError(t, err)
	require.False(t, saved)
	saved, err = r.SaveBPSState(ctx, scope, "catalog", &revision, []byte(`{"updated":true}`))
	require.NoError(t, err)
	require.True(t, saved)
	_, err = r.SaveBPSState(ctx, scope, "huge", nil, make([]byte, (2<<20)+1))
	require.ErrorIs(t, err, basispoints.ErrStateCapacity)
	_, err = integrationDB.ExecContext(ctx, `UPDATE excel_bps_session_states SET expires_at=NOW()-INTERVAL '1 second' WHERE scope=$1`, excelBPSStateHash(scope))
	require.NoError(t, err)
	raw, _, err = r.LoadBPSState(ctx, scope, "catalog")
	require.NoError(t, err)
	require.Empty(t, raw)
}
