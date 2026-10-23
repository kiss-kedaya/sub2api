package sweeper

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"time"
)

type jobDB interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// Shared by every executor instance. Do not acquire this lock in service.
const sweepLockKey int64 = 0x43525950544f5357

func joinErrors(a, b error) error { return errors.Join(a, b) }

// The connection holds a SESSION lock. All writes on it are autocommitted;
// holding an uncommitted transaction across broadcasts would lose the job on
// a crash. The planner's BalanceReader needs a second pool connection.
func (e *Executor) lock(ctx context.Context) (*sql.Conn, func() error, error) {
	if e.db == nil {
		return nil, nil, fmt.Errorf("sweep database is not configured")
	}
	if e.db.Stats().MaxOpenConnections == 1 {
		return nil, nil, fmt.Errorf("sweep requires at least two database connections (lock and balance reader)")
	}
	conn, err := e.db.Conn(ctx)
	if err != nil {
		return nil, nil, err
	}
	discard := func() { _ = conn.Raw(func(any) error { return driver.ErrBadConn }) }
	var locked bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, sweepLockKey).Scan(&locked); err != nil {
		discard() // acquisition may have succeeded with a lost response
		_ = conn.Close()
		return nil, nil, fmt.Errorf("acquire sweep lock: %w", err)
	}
	if !locked {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("sweep already running in another instance")
	}
	release := func() error {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var unlocked bool
		err := conn.QueryRowContext(cleanup, `SELECT pg_advisory_unlock($1)`, sweepLockKey).Scan(&unlocked)
		if err != nil || !unlocked {
			discard() // never return a possibly locked session to the pool
			if err == nil {
				err = fmt.Errorf("sweep advisory lock was not held")
			}
		}
		return joinErrors(err, conn.Close())
	}
	return conn, release, nil
}

type persistenceError struct{ cause error }

func (e *persistenceError) Error() string { return "persist sweep job: " + e.cause.Error() }
func (e *persistenceError) Unwrap() error { return e.cause }

func (e *Executor) queries() jobDB {
	if e.store != nil {
		return e.store
	}
	return e.db
}

type sweepJob struct {
	PlanEntry
	ID, Revision  int64
	Version       int
	Destination   string
	Status, Error string
	Confirmations uint64
	GasPayer      string
	GasAmount     string
	GasNonce      string
	GasRaw        string
	GasHash       string
	GasConfirmed  bool
	SweepNonce    string
	SweepRaw      string
	SweepHash     string
}

func (j *sweepJob) outcome() SweepEntry {
	amount := ""
	if j.RawAmount != nil {
		amount = j.RawAmount.String()
	}
	status := "pending"
	if j.Status == "swept" || j.Status == "manual_review" {
		status = j.Status
	}
	return SweepEntry{Chain: j.Chain, Address: j.Address, Amount: amount,
		GasTxHash: j.GasHash, SweepTxHash: j.SweepHash, Status: status, Error: j.Error}
}

// Legacy rows can contain duplicate attempts and inaccurate amounts. They
// remain outside the new partial unique index and must never be auto-resigned.
const unfinishedPredicate = `status NOT IN ('swept','failed') AND job_version = 1
	OR job_version = 0 AND status <> 'swept' AND
	(status IN ('manual_review','gas_sent') OR gas_tx_hash <> '' OR sweep_tx_hash <> '')`

const jobColumns = `id, chain, from_address, to_address, token, raw_amount,
	token_decimals, derivation_index, status, error, job_version, revision,
	confirmations_required, gas_payer, gas_amount, gas_nonce, gas_raw_tx,
	gas_tx_hash, gas_confirmed, sweep_nonce, sweep_raw_tx, sweep_tx_hash`

func (e *Executor) loadUnfinished(ctx context.Context) ([]*sweepJob, error) {
	rows, err := e.queries().QueryContext(ctx, `SELECT `+jobColumns+` FROM sweep_records
		WHERE (`+unfinishedPredicate+`) ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var jobs []*sweepJob
	for rows.Next() {
		j := &sweepJob{}
		var raw string
		if err := rows.Scan(&j.ID, &j.Chain, &j.Address, &j.Destination, &j.Token, &raw,
			&j.Decimals, &j.DerivationIndex, &j.Status, &j.Error, &j.Version, &j.Revision,
			&j.Confirmations, &j.GasPayer, &j.GasAmount, &j.GasNonce, &j.GasRaw,
			&j.GasHash, &j.GasConfirmed, &j.SweepNonce, &j.SweepRaw, &j.SweepHash); err != nil {
			return nil, err
		}
		if j.Version == 1 {
			var ok bool
			j.RawAmount, ok = new(big.Int).SetString(raw, 10)
			if !ok || j.RawAmount.Sign() <= 0 {
				return nil, fmt.Errorf("invalid immutable amount for sweep job %d", j.ID)
			}
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

func (e *Executor) createJob(ctx context.Context, entry PlanEntry) (*sweepJob, error) {
	j := &sweepJob{PlanEntry: entry, Version: 1, Destination: e.config.HotWalletEVM,
		Status: "planned", Confirmations: e.config.Confirmations[entry.Chain]}
	// The lock covers planning and this reservation. The unique index is a
	// second guard; legacy blockers are checked explicitly (no dedup of old rows).
	err := e.queries().QueryRowContext(ctx, `INSERT INTO sweep_records
		(chain, from_address, to_address, token, amount, raw_amount, token_decimals,
		 derivation_index, status, job_version, confirmations_required)
		SELECT $1,$2,$3,$4,$5,$6,$7,$8,'planned',1,$9
		WHERE NOT EXISTS (SELECT 1 FROM sweep_records WHERE chain=$1
		 AND lower(from_address)=lower($2) AND (`+unfinishedPredicate+`)) RETURNING id`,
		entry.Chain, entry.Address, j.Destination, entry.Token, amountString(entry.RawAmount, entry.Decimals),
		entry.RawAmount.String(), entry.Decimals, int64(entry.DerivationIndex),
		strconv.FormatUint(j.Confirmations, 10)).Scan(&j.ID)
	if err != nil {
		return nil, &persistenceError{err}
	}
	return j, nil
}

// Every state change checks its revision, including the final ledger write.
// Immutable plan fields and amount are never rewritten.
func (e *Executor) saveJob(ctx context.Context, j *sweepJob) error {
	result, err := e.queries().ExecContext(ctx, `UPDATE sweep_records SET
		status=$2, error=$3, confirmations_required=$4, gas_payer=$5, gas_amount=$6,
		gas_nonce=$7, gas_raw_tx=$8, gas_tx_hash=$9, gas_confirmed=$10,
		sweep_nonce=$11, sweep_raw_tx=$12, sweep_tx_hash=$13,
		revision=revision+1, updated_at=NOW() WHERE id=$1 AND job_version=1 AND revision=$14`,
		j.ID, j.Status, j.Error, strconv.FormatUint(j.Confirmations, 10), j.GasPayer, j.GasAmount,
		j.GasNonce, j.GasRaw, j.GasHash, j.GasConfirmed, j.SweepNonce, j.SweepRaw, j.SweepHash, j.Revision)
	if err == nil {
		var n int64
		n, err = result.RowsAffected()
		if err == nil && n != 1 {
			err = fmt.Errorf("job %d revision conflict or missing record", j.ID)
		}
	}
	if err != nil {
		return &persistenceError{err}
	}
	j.Revision++
	return nil
}

func (e *Executor) addressBusy(ctx context.Context, j *sweepJob) (bool, error) {
	var busy bool
	err := e.queries().QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM sweep_records
		WHERE chain=$1 AND lower(from_address)=lower($2) AND id<>$3
		AND (`+unfinishedPredicate+`) AND
		(gas_tx_hash<>'' OR sweep_tx_hash<>'' OR status='manual_review'))`, j.Chain, j.Address, j.ID).Scan(&busy)
	return busy, err
}

func (e *Executor) gasPayerBusy(ctx context.Context, j *sweepJob, payer string) (bool, error) {
	var busy bool
	err := e.queries().QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM sweep_records
		WHERE chain=$1 AND id<>$2 AND (`+unfinishedPredicate+`)
		AND gas_tx_hash<>'' AND NOT gas_confirmed
		AND (gas_payer='' OR lower(gas_payer)=lower($3)))`, j.Chain, j.ID, payer).Scan(&busy)
	return busy, err
}
