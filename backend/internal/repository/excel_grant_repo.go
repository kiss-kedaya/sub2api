package repository

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type excelGrantRepository struct{ db *sql.DB }

func NewExcelGrantRepository(db *sql.DB) service.ExcelGrantRepository {
	return &excelGrantRepository{db}
}

const excelGrantColumns = `g.account_id,a.name,g.config,g.revision,g.credentials_cipher,g.expires_at`

func scanExcelGrant(row scannable) (*service.ExcelGrantRecord, error) {
	r := &service.ExcelGrantRecord{}
	var config []byte
	err := row.Scan(&r.AccountID, &r.AccountName, &config, &r.Revision, &r.Cipher, &r.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(config, &r.Config); err != nil {
		return nil, err
	}
	r.Configured = r.Cipher != ""
	return r, nil
}
func (r *excelGrantRepository) Get(ctx context.Context, id int64) (*service.ExcelGrantRecord, error) {
	return scanExcelGrant(r.db.QueryRowContext(ctx, `SELECT `+excelGrantColumns+` FROM excel_grants g JOIN accounts a ON a.id=g.account_id AND a.deleted_at IS NULL WHERE g.account_id=$1`, id))
}
func (r *excelGrantRepository) List(ctx context.Context) ([]service.ExcelGrantRecord, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+excelGrantColumns+` FROM excel_grants g JOIN accounts a ON a.id=g.account_id AND a.deleted_at IS NULL ORDER BY g.account_id LIMIT 1000`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []service.ExcelGrantRecord{}
	for rows.Next() {
		row, e := scanExcelGrant(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *row)
	}
	return out, rows.Err()
}
func (r *excelGrantRepository) SaveConfig(ctx context.Context, row *service.ExcelGrantRecord, expected string) error {
	raw, err := json.Marshal(row.Config)
	if err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var result sql.Result
	if expected == "" {
		result, err = tx.ExecContext(ctx, `INSERT INTO excel_grants(account_id,config,revision) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, row.AccountID, string(raw), row.Revision)
	} else {
		result, err = tx.ExecContext(ctx, `UPDATE excel_grants SET config=$2,revision=$3,updated_at=NOW() WHERE account_id=$1 AND revision=$4`, row.AccountID, string(raw), row.Revision, expected)
	}
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("BPS settings changed; refresh and retry")
	}
	models, err := json.Marshal(row.Config.Models)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE accounts SET extra=jsonb_set(jsonb_set(COALESCE(extra,'{}'::jsonb),'{excel_bps_enabled}',to_jsonb($2::boolean),TRUE),'{excel_bps_models}',$3::jsonb,TRUE),updated_at=NOW() WHERE id=$1 AND deleted_at IS NULL`, row.AccountID, row.Config.Enabled, string(models))
	if err != nil {
		return err
	}
	if err = enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &row.AccountID, nil, nil); err != nil {
		return err
	}
	return tx.Commit()
}
func (r *excelGrantRepository) CreateSession(ctx context.Context, s service.ExcelGrantSession) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO excel_oauth_sessions(id,account_id,state_hash,verifier_cipher,config_revision,primary_hash,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, s.ID, s.AccountID, s.StateHash, s.VerifierCipher, s.Revision, s.PrimaryHash, s.ExpiresAt)
	return err
}
func (r *excelGrantRepository) ConsumeSession(ctx context.Context, id int64, session, hash string) (*service.ExcelGrantSession, error) {
	s := &service.ExcelGrantSession{}
	err := r.db.QueryRowContext(ctx, `DELETE FROM excel_oauth_sessions WHERE id=$1 AND account_id=$2 AND state_hash=$3 AND expires_at>NOW() RETURNING id,account_id,state_hash,verifier_cipher,config_revision,primary_hash,expires_at`, session, id, hash).Scan(&s.ID, &s.AccountID, &s.StateHash, &s.VerifierCipher, &s.Revision, &s.PrimaryHash, &s.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return s, err
}
func (r *excelGrantRepository) StoreGrant(ctx context.Context, id int64, revision, expectedCipher, primaryHash, cipher string, expires time.Time) (bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	a := &service.Account{}
	var credentials []byte
	err = tx.QueryRowContext(ctx, `SELECT credentials,proxy_id FROM accounts WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, id).Scan(&credentials, &a.ProxyID)
	if err != nil {
		return false, err
	}
	if err = json.Unmarshal(credentials, &a.Credentials); err != nil {
		return false, err
	}
	if service.ExcelGrantPrimaryHash(a) != primaryHash {
		return false, nil
	}
	result, err := tx.ExecContext(ctx, `UPDATE excel_grants SET credentials_cipher=$5,expires_at=$6,updated_at=NOW() WHERE account_id=$1 AND revision=$2 AND credentials_cipher=$3 AND EXISTS(SELECT 1 FROM accounts WHERE id=$1 AND deleted_at IS NULL AND platform='openai' AND type='oauth') AND $4<>''`, id, revision, expectedCipher, primaryHash, cipher, expires)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if n != 1 {
		return false, nil
	}
	if err = enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &id, nil, nil); err != nil {
		return false, err
	}
	return true, tx.Commit()
}
func (r *excelGrantRepository) WithAccountLock(ctx context.Context, id int64, run func() error) error {
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	key := "excel-grant:" + strconv.FormatInt(id, 10)
	for {
		var locked bool
		err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1,0))`, key).Scan(&locked)
		if err != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
			return err
		}
		if locked {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		if _, err := conn.ExecContext(cleanup, `SELECT pg_advisory_unlock(hashtextextended($1,0))`, key); err != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()
	return run()
}
