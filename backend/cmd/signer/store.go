package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "github.com/lib/pq"
)

// Only the vault and audit use the shared database. Legacy policy/state keys
// remain readable for diagnostics and migration, never for authorization.
const (
	settingVault  = "crypto.signer.vault"
	settingPolicy = "crypto.signer.policy"
	settingState  = "crypto.signer.state"
)

// store is the signer's persistence: settings for config and a dedicated
// table for the audit trail.
type store struct {
	db *sql.DB
}

func newStore(dsn string) (*store, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(30 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return &store{db: db}, nil
}

func (s *store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// getSetting returns "" when the key has never been written.
func (s *store) getSetting(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx,
		`SELECT value FROM settings WHERE key = $1`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read setting %s: %w", key, err)
	}
	return value, nil
}

func (s *store) setSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO settings (key, value, updated_at) VALUES ($1, $2, NOW())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`,
		key, value)
	if err != nil {
		return fmt.Errorf("write setting %s: %w", key, err)
	}
	return nil
}

// writeAudit appends one signature event. Failure to record is reported to
// the caller: an unaudited signature is a security gap, not a warning.
func (s *store) writeAudit(ctx context.Context, e auditEntry) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO crypto_signer_audit
		  (op, kind, derivation_index, chain_id, to_address, token, amount, result, reason)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		e.Op, e.Kind, e.Index, e.ChainID, e.To, e.Token, e.Amount, e.Result, e.Reason)
	if err != nil {
		return fmt.Errorf("write signer audit: %w", err)
	}
	return nil
}

// loadPolicyFromDB is LEGACY/DIAGNOSTIC ONLY. Its result cannot enable signing.
func (s *store) loadPolicyFromDB(ctx context.Context) (*Policy, error) {
	raw, err := s.getSetting(ctx, settingPolicy)
	if err != nil {
		return nil, err
	}
	if raw == "" {
		return nil, fmt.Errorf("signer policy is not configured (settings key %s)", settingPolicy)
	}
	var p Policy
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return nil, fmt.Errorf("parse signer policy: %w", err)
	}
	if len(p.Targets) == 0 {
		return nil, fmt.Errorf("signer policy: at least one target is required")
	}
	return &p, nil
}
