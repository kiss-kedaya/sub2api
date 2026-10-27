package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

type credentialGuardRepository struct{ db *sql.DB }

func NewCredentialGuardRepository(db *sql.DB) service.CredentialGuardRepository {
	return &credentialGuardRepository{db}
}

const credentialGuardColumns = `g.account_id,a.name,g.config,g.revision,g.secret_cipher,g.state,g.code,g.http_status,g.failures,g.blocked,g.last_checked_at,g.next_check_at,g.lease`

func scanCredentialGuard(row scannable) (*service.CredentialGuardRecord, error) {
	r := &service.CredentialGuardRecord{}
	var cfg []byte
	err := row.Scan(&r.AccountID, &r.AccountName, &cfg, &r.Revision, &r.SecretCipher, &r.State, &r.Code, &r.HTTPStatus, &r.Failures, &r.Blocked, &r.LastCheckedAt, &r.NextCheckAt, &r.Lease)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(cfg, &r.Config); err != nil {
		return nil, err
	}
	r.SecretsConfigured = r.SecretCipher != ""
	return r, nil
}
func (r *credentialGuardRepository) Get(ctx context.Context, id int64) (*service.CredentialGuardRecord, error) {
	return scanCredentialGuard(r.db.QueryRowContext(ctx, `SELECT `+credentialGuardColumns+` FROM credential_guard_accounts g JOIN accounts a ON a.id=g.account_id AND a.deleted_at IS NULL WHERE g.account_id=$1`, id))
}
func (r *credentialGuardRepository) List(ctx context.Context) ([]service.CredentialGuardRecord, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+credentialGuardColumns+` FROM credential_guard_accounts g JOIN accounts a ON a.id=g.account_id AND a.deleted_at IS NULL ORDER BY g.account_id LIMIT 1000`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []service.CredentialGuardRecord{}
	for rows.Next() {
		row, e := scanCredentialGuard(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *row)
	}
	return out, rows.Err()
}
func (r *credentialGuardRepository) Save(ctx context.Context, row *service.CredentialGuardRecord, expected string, reset bool) error {
	cfg, err := json.Marshal(row.Config)
	if err != nil {
		return err
	}
	var result sql.Result
	if expected == "" {
		result, err = r.db.ExecContext(ctx, `INSERT INTO credential_guard_accounts(account_id,config,revision,secret_cipher,enabled) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, row.AccountID, string(cfg), row.Revision, row.SecretCipher, row.Config.Enabled)
	} else {
		result, err = r.db.ExecContext(ctx, `UPDATE credential_guard_accounts SET config=$2,revision=$3,secret_cipher=$4,enabled=$5,failures=CASE WHEN $7 THEN 0 ELSE failures END,blocked=CASE WHEN $7 THEN FALSE ELSE blocked END,next_relogin_at=CASE WHEN $7 THEN NULL ELSE next_relogin_at END,next_check_at=NOW(),updated_at=NOW() WHERE account_id=$1 AND revision=$6 AND (lease_until IS NULL OR lease_until<NOW())`, row.AccountID, string(cfg), row.Revision, row.SecretCipher, row.Config.Enabled, expected, reset)
	}
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("credential configuration changed or inspection is running")
	}
	return nil
}
func (r *credentialGuardRepository) Claim(ctx context.Context, id int64) (*service.CredentialGuardRecord, error) {
	lease := uuid.NewString()
	return scanCredentialGuard(r.db.QueryRowContext(ctx, `WITH candidate AS (SELECT g.account_id FROM credential_guard_accounts g JOIN accounts a ON a.id=g.account_id AND a.deleted_at IS NULL WHERE (lease_until IS NULL OR lease_until<NOW()) AND (($1::bigint=0 AND g.enabled AND NOT g.blocked AND g.next_check_at<=NOW()) OR g.account_id=$1) ORDER BY g.next_check_at FOR UPDATE OF g SKIP LOCKED LIMIT 1), claimed AS (UPDATE credential_guard_accounts g SET lease=$2,lease_until=NOW()+INTERVAL '3 minutes' FROM candidate c WHERE g.account_id=c.account_id RETURNING g.*) SELECT `+credentialGuardColumns+` FROM claimed g JOIN accounts a ON a.id=g.account_id`, id, lease))
}
func (r *credentialGuardRepository) ReserveRelogin(ctx context.Context, row *service.CredentialGuardRecord) (bool, error) {
	result, err := r.db.ExecContext(ctx, `UPDATE credential_guard_accounts SET failures=failures+1,next_relogin_at=NOW()+make_interval(secs=>$4),updated_at=NOW() WHERE account_id=$1 AND revision=$2 AND lease=$3 AND lease_until>NOW() AND NOT blocked AND failures<$5 AND (next_relogin_at IS NULL OR next_relogin_at<=NOW())`, row.AccountID, row.Revision, row.Lease, row.Config.CooldownSeconds, row.Config.MaxFailures)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}
func (r *credentialGuardRepository) Complete(ctx context.Context, row *service.CredentialGuardRecord, result service.CredentialGuardCompletion) error {
	_, err := r.db.ExecContext(ctx, `WITH completed AS (UPDATE credential_guard_accounts SET state=$4,code=$5,http_status=$6,blocked=$7,failures=CASE WHEN $4='healthy' THEN 0 ELSE failures END,last_checked_at=NOW(),next_check_at=$8,lease='',lease_until=NULL,updated_at=NOW() WHERE account_id=$1 AND revision=$2 AND lease=$3 RETURNING account_id) INSERT INTO credential_guard_events(account_id,state,code,http_status) SELECT account_id,$4,$5,$6 FROM completed`, row.AccountID, row.Revision, row.Lease, result.State, result.Code, result.HTTPStatus, result.Blocked, result.Next)
	return err
}
func (r *credentialGuardRepository) PersistCredentials(ctx context.Context, row *service.CredentialGuardRecord, a *service.Account, credentials map[string]any) (bool, error) {
	expected, err := json.Marshal(a.Credentials)
	if err != nil {
		return false, err
	}
	next, err := json.Marshal(credentials)
	if err != nil {
		return false, err
	}
	result, err := r.db.ExecContext(ctx, `WITH changed AS (UPDATE accounts a SET credentials=$4::jsonb,updated_at=NOW() WHERE a.id=$1 AND a.deleted_at IS NULL AND a.platform='openai' AND a.type='oauth' AND a.status='active' AND a.schedulable=TRUE AND a.credentials=$2::jsonb AND a.proxy_id IS NOT DISTINCT FROM $3 AND EXISTS(SELECT 1 FROM credential_guard_accounts g WHERE g.account_id=a.id AND g.revision=$5 AND g.lease=$6 AND g.lease_until>NOW()) RETURNING a.id) INSERT INTO scheduler_outbox(event_type,account_id,group_id,payload) SELECT $7,id,NULL,NULL FROM changed`, a.ID, string(expected), a.ProxyID, string(next), row.Revision, row.Lease, service.SchedulerOutboxEventAccountChanged)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}
