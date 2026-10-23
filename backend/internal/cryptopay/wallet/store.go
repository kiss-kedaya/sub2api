package wallet

import (
	"context"
	"database/sql"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/addresses"
)

const settingVault = "crypto.signer.vault"

// Conflicts with the sweeper's session advisory lock as well as other wallet
// transactions. Transaction-scoped ownership cannot leak back into the pool.
const vaultSweepLockKey int64 = 0x43525950544f5357

// Keep identical to sweeper's unfinishedPredicate, including ambiguous v0 jobs.
const unfinishedSweepPredicate = `status NOT IN ('swept','failed') AND job_version = 1
	OR job_version = 0 AND status <> 'swept' AND
	(status IN ('manual_review','gas_sent') OR gas_tx_hash <> '' OR sweep_tx_hash <> '')`

const auditSQL = `INSERT INTO crypto_signer_audit
	(op, kind, derivation_index, chain_id, to_address, token, amount, result, reason)
	VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`

type auditEntry struct {
	op, kind                          string
	index                             uint32
	chainID                           int64
	to, token, amount, result, reason string
}

type auditWriter interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func writeAudit(ctx context.Context, w auditWriter, e auditEntry) error {
	if err := mode.CheckWritable(); err != nil {
		return err
	}
	if _, err := w.ExecContext(ctx, auditSQL, e.op, e.kind, e.index, e.chainID,
		e.to, e.token, e.amount, e.result, e.reason); err != nil {
		// Drivers can include SQL arguments in errors. Never propagate them.
		return ErrAuditUnavailable
	}
	return nil
}

func (m *Manager) audit(ctx context.Context, e auditEntry) error {
	if m.db == nil {
		return ErrAuditUnavailable
	}
	return writeAudit(ctx, m.db, e)
}

// Denials use a bounded independent context, so cancellation cannot suppress
// the audit attempt. Only fixed operation/reason strings reach this path.
func (m *Manager) denied(op string, cause error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := m.audit(ctx, auditEntry{op: op, result: "denied"}); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

func (m *Manager) beginVaultTx(ctx context.Context) (*sql.Tx, error) {
	if m.db == nil {
		return nil, ErrVaultUnavailable
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, ErrVaultUnavailable
	}
	var locked bool
	if err := tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock($1)`, vaultSweepLockKey).Scan(&locked); err != nil {
		_ = tx.Rollback()
		return nil, ErrVaultUnavailable
	}
	if !locked {
		_ = tx.Rollback()
		return nil, ErrSweepBusy
	}
	return tx, nil
}

type vaultReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readVault(ctx context.Context, r vaultReader, forUpdate bool) (string, error) {
	query := `SELECT value FROM settings WHERE key = $1`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	var raw string
	err := r.QueryRowContext(ctx, query, settingVault).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil || len(raw) > 32<<10 {
		return "", ErrVaultUnavailable
	}
	if raw != "" && strings.TrimSpace(raw) == "" {
		return "", ErrVaultUnavailable
	}
	return raw, nil
}

// Validate BOTH allocated addresses and unassigned pool entries before seeds
// enter memory. No status filter: disabled/used addresses still own deposits.
func validateDepositAddresses(ctx context.Context, tx *sql.Tx, d *Deriver) error {
	rows, err := tx.QueryContext(ctx, `SELECT chain_family, derivation_index, address FROM crypto_address_pool
		UNION SELECT CASE
			WHEN chain IN ('bsc','polygon','plasma','arbitrum','base','xlayer') THEN 'evm'
			WHEN chain = 'solana' THEN 'solana'
			ELSE chain END AS chain_family, derivation_index, address FROM deposit_addresses`)
	if err != nil {
		return ErrVaultUnavailable
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var family, address string
		var index int64
		if rows.Scan(&family, &index, &address) != nil {
			return ErrVaultUnavailable
		}
		if index < 0 || index >= 1<<31 {
			return ErrDepositSeedMismatch
		}
		switch family {
		case addresses.FamilyEVM:
			_, derived, err := d.EVMKey(uint32(index))
			if err != nil {
				return ErrDepositSeedMismatch
			}
			if !strings.EqualFold(address, derived.Hex()) {
				return ErrDepositSeedMismatch
			}
		case addresses.FamilySolana:
			key, derived, err := d.SolanaKey(uint32(index))
			clear(key)
			if err != nil || address != derived.String() {
				return ErrDepositSeedMismatch
			}
		default:
			return ErrDepositSeedMismatch
		}
	}
	if rows.Err() != nil {
		return ErrVaultUnavailable
	}
	return nil
}
