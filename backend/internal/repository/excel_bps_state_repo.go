package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/service/basispoints"
)

type excelBPSStateRepository struct {
	db        *sql.DB
	encryptor service.SecretEncryptor
}

func NewExcelBPSStateRepository(db *sql.DB, encryptor service.SecretEncryptor) basispoints.StateStore {
	return &excelBPSStateRepository{db, encryptor}
}
func excelBPSStateHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func (r *excelBPSStateRepository) LoadBPSState(ctx context.Context, scope, key string) ([]byte, uint64, error) {
	if scope == "" || key == "" || r.encryptor == nil {
		return nil, 0, basispoints.ErrStateUnavailable
	}
	var cipher string
	var revision uint64
	err := r.db.QueryRowContext(ctx, `UPDATE excel_bps_session_states SET expires_at=NOW()+INTERVAL '2 hours' WHERE scope=$1 AND state_key=$2 AND expires_at>NOW() RETURNING payload_cipher,revision`, excelBPSStateHash(scope), excelBPSStateHash(key)).Scan(&cipher, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, basispoints.ErrStateUnavailable
	}
	plain, err := r.encryptor.Decrypt(cipher)
	if err != nil {
		return nil, 0, basispoints.ErrStateUnavailable
	}
	return []byte(plain), revision, nil
}
func (r *excelBPSStateRepository) SaveBPSState(ctx context.Context, scope, key string, expected *uint64, raw []byte) (bool, error) {
	if scope == "" || key == "" || r.encryptor == nil {
		return false, basispoints.ErrStateUnavailable
	}
	if len(raw) > 2<<20 {
		return false, basispoints.ErrStateCapacity
	}
	cipher, err := r.encryptor.Encrypt(string(raw))
	if err != nil {
		return false, basispoints.ErrStateUnavailable
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	scopeHash, keyHash := excelBPSStateHash(scope), excelBPSStateHash(key)
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, scopeHash); err != nil {
		return false, err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM excel_bps_session_states WHERE scope=$1 AND expires_at<=NOW()`, scopeHash)
	if err != nil {
		return false, err
	}
	var current uint64
	err = tx.QueryRowContext(ctx, `SELECT revision FROM excel_bps_session_states WHERE scope=$1 AND state_key=$2`, scopeHash, keyHash).Scan(&current)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if expected != nil && current != *expected {
		return false, nil
	}
	{
		var count int
		var bytes int64
		err = tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(octet_length(payload_cipher)),0) FROM excel_bps_session_states WHERE scope=$1 AND state_key<>$2`, scopeHash, keyHash).Scan(&count, &bytes)
		if err != nil {
			return false, err
		}
		if count >= 512 || bytes+int64(len(cipher)) > 32<<20 {
			return false, basispoints.ErrStateCapacity
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO excel_bps_session_states(scope,state_key,revision,payload_cipher,expires_at) VALUES($1,$2,1,$3,NOW()+INTERVAL '2 hours') ON CONFLICT(scope,state_key) DO UPDATE SET revision=excel_bps_session_states.revision+1,payload_cipher=EXCLUDED.payload_cipher,expires_at=EXCLUDED.expires_at`, scopeHash, keyHash, cipher)
	if err != nil {
		return false, err
	}
	return true, tx.Commit()
}
