package sweeper

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

// What a reconciliation did to a job.
const (
	ReconcileConfirmed = "confirmed"
	ReconcileReleased  = "released"
	ReconcileObserved  = "observed"
)

// ReconcileResult reports the on-chain truth for one job.
type ReconcileResult struct {
	JobID   int64  `json:"job_id"`
	Chain   string `json:"chain"`
	Status  string `json:"status"`
	Action  string `json:"action"`
	Message string `json:"message"`
}

// Reconcile inspects a stuck job against the chain and moves it forward
// without ever signing or broadcasting anything:
//
//	sweep tx mined + confirmed -> swept
//	gas tx mined + confirmed   -> gas_confirmed (next run signs the token transfer)
//	tx reverted                -> manual_review (nonce consumed; no replacement)
//	tx absent                  -> pending (retain the reservation and exact bytes)
//	swept tx rolled back       -> failed (address released, the next run plans it again)
func (e *Executor) Reconcile(ctx context.Context, jobID int64) (*ReconcileResult, error) {
	if err := mode.CheckWritable(); err != nil {
		return nil, err
	}
	conn, release, err := e.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = release() }()
	run := *e
	run.store = conn

	job, err := run.loadJob(ctx, jobID)
	if err != nil {
		return nil, err
	}
	out := &ReconcileResult{JobID: job.ID, Chain: job.Chain, Status: job.Status}
	hasTransaction := job.GasHash != "" || job.SweepHash != ""
	// saveJob only writes version 1 rows, so a reconcile of anything else could
	// read the chain but never record what it found. Say so up front.
	if job.Version != 1 {
		// A legacy swept row cannot be reclassified either, so keep the old
		// "already final" answer instead of an error.
		if job.Status == "swept" || !hasTransaction {
			out.Action = ReconcileObserved
			out.Message = "job already final; nothing to reconcile"
			return out, nil
		}
		return nil, ErrLegacySweepRecord
	}
	pool := e.pools[job.Chain]
	if pool == nil {
		return nil, fmt.Errorf("no RPC pool for chain %s", job.Chain)
	}
	pool, err = pool.ForChain(ctx, uint64(ChainIDs[job.Chain]))
	if err != nil {
		return nil, err
	}
	if job.Status == "swept" {
		// "swept" is a claim about the chain, not the chain itself: re-read the
		// receipt and its canonical block. A proven rollback turns the record
		// into failed so the address can be planned again.
		observation, reason, checkErr := run.checkSwept(ctx, pool, job)
		if checkErr != nil {
			return nil, checkErr
		}
		switch observation {
		case sweptStillConfirmed:
			out.Action = ReconcileObserved
			out.Message = "swept transaction is still canonical on chain; nothing to reconcile"
			return out, nil
		case sweptMissingReceipt:
			out.Action = ReconcileObserved
			out.Message = "swept receipt is not visible to this node; record retained for the periodic audit"
			return out, nil
		}
		changed, markErr := run.markSweptRolledBack(ctx, job.ID, reason)
		if markErr != nil {
			return nil, markErr
		}
		if !changed {
			out.Action = ReconcileObserved
			out.Message = "sweep record changed during reconcile; re-read it"
			return out, nil
		}
		out.Action, out.Status = ReconcileReleased, "failed"
		out.Message = "swept transaction rolled back (" + reason + "); address released for a new plan"
		return out, nil
	}
	if job.Status == "failed" && !hasTransaction {
		out.Action = ReconcileObserved
		out.Message = "job already final; nothing to reconcile"
		return out, nil
	}
	required := e.config.Confirmations[job.Chain]
	if required == 0 {
		required = 1
	}
	// A record released as stale was deliberately unreserved: the sweeper
	// proved its bytes could never be mined and a later run may already hold a
	// new reservation for the same address. Observing it still matters (a late
	// confirmation corrects the record), but turning it back into
	// `sweep_pending`/`manual_review` would re-reserve an address the sweeper
	// already handed to a newer job.
	if job.Status == "failed" && strings.HasPrefix(job.Error, staleReleasePrefix) {
		return run.observeReleased(ctx, pool, job, out, required)
	}

	// A signed token transfer is authoritative once it is mined.
	if job.SweepHash != "" {
		state, err := checkReceipt(ctx, pool, job.Chain, job.SweepHash, required)
		if err != nil {
			return nil, err
		}
		switch state {
		case receiptConfirmed:
			return run.finishReconcile(ctx, job, out, "swept", "",
				ReconcileConfirmed, "token transfer confirmed on chain")
		case receiptReverted:
			return run.finishReconcile(ctx, job, out, "manual_review",
				"confirmed transaction reverted; automatic replacement/funding refused",
				ReconcileObserved, "confirmed token transfer reverted; manual review required")
		case receiptWaiting:
			return run.retainUnconfirmed(ctx, job, out, false, "token transfer is mined but still short of confirmations")
		}
		if job.Status == "sweep_pending" {
			released, releaseErr := run.releaseStaleTransaction(ctx, pool, job, false, false)
			if releaseErr != nil {
				return nil, releaseErr
			}
			if released {
				out.Action, out.Status = ReconcileReleased, job.Status
				out.Message = job.Error
				return out, nil
			}
		}
		return run.retainUnconfirmed(ctx, job, out, false, "token transfer has no canonical receipt yet")
	}

	if job.GasHash != "" {
		state, err := checkReceipt(ctx, pool, job.Chain, job.GasHash, required)
		if err != nil {
			return nil, err
		}
		switch state {
		case receiptConfirmed:
			job.GasConfirmed = true
			return run.finishReconcile(ctx, job, out, "gas_confirmed", "",
				ReconcileConfirmed, "gas top-up confirmed; the next run signs the token transfer")
		case receiptReverted:
			return run.finishReconcile(ctx, job, out, "manual_review",
				"confirmed gas top-up reverted; automatic replacement/funding refused",
				ReconcileObserved, "gas top-up reverted; manual review required")
		case receiptWaiting:
			return run.retainUnconfirmed(ctx, job, out, true, "gas top-up is mined but still short of confirmations")
		}
		if job.Status == "gas_pending" {
			released, releaseErr := run.releaseStaleTransaction(ctx, pool, job, true, false)
			if releaseErr != nil {
				return nil, releaseErr
			}
			if released {
				out.Action, out.Status = ReconcileReleased, job.Status
				out.Message = job.Error
				return out, nil
			}
		}
		return run.retainUnconfirmed(ctx, job, out, true, "gas top-up has no canonical receipt yet")
	}

	out.Action = ReconcileObserved
	out.Message = "no signed transaction reserved; the next run plans this address normally"
	return out, nil
}

func (e *Executor) finishReconcile(ctx context.Context, job *sweepJob, out *ReconcileResult,
	status, jobErr, action, message string) (*ReconcileResult, error) {
	job.Status, job.Error = status, jobErr
	if err := e.saveJob(ctx, job); err != nil {
		return nil, err
	}
	out.Action, out.Status = action, status
	if message != "" {
		out.Message = message
	} else {
		out.Message = jobErr
	}
	return out, nil
}

// observeReleased inspects a record whose reservation was deliberately released
// as stale. A late confirmation is a genuine state correction and is recorded;
// anything else leaves the row exactly as it is, because re-reserving it would
// collide with any newer job for the same address.
func (e *Executor) observeReleased(ctx context.Context, pool *rpc.Pool, job *sweepJob,
	out *ReconcileResult, required uint64) (*ReconcileResult, error) {
	hash := job.SweepHash
	if hash == "" {
		// A confirmed gas top-up does not re-open a released record either: the
		// address is already free and its next plan must win.
		hash = job.GasHash
	}
	if hash != "" {
		state, err := checkReceipt(ctx, pool, hash, required)
		if err != nil {
			return nil, err
		}
		if state == receiptConfirmed && job.SweepHash != "" {
			return e.finishReconcile(ctx, job, out, "swept", "",
				ReconcileConfirmed, "released token transfer confirmed on chain")
		}
	}
	out.Action = ReconcileObserved
	out.Status = job.Status
	out.Message = "job was released as stale; it stays failed unless its recorded token transfer confirms on chain"
	return out, nil
}

// retainUnconfirmed never releases an address merely because one RPC cannot
// see a receipt. It retains immutable signed bytes for v1 jobs; old or
// incomplete jobs require manual review and stay blocked.
func (e *Executor) retainUnconfirmed(ctx context.Context, job *sweepJob, out *ReconcileResult,
	gas bool, message string) (*ReconcileResult, error) {
	if job.Version != 1 || (gas && job.GasRaw == "") || (!gas && job.SweepRaw == "") {
		return e.finishReconcile(ctx, job, out, "manual_review",
			"legacy or ambiguous transaction; reconcile hashes manually before releasing address",
			ReconcileObserved, message+"; reservation retained for manual review")
	}
	if gas {
		job.Status = "gas_pending"
	} else {
		job.Status = "sweep_pending"
	}
	job.Error = "awaiting canonical receipt and required confirmations"
	if err := e.saveJob(ctx, job); err != nil {
		return nil, err
	}
	out.Action, out.Status = ReconcileObserved, job.Status
	out.Message = message + "; address remains reserved and the existing signed transaction will be resumed without creating a new signature"
	return out, nil
}

// ErrLegacySweepRecord marks a record written before the current job format (or
// handled by hand). It cannot be updated by the sweeper, so it cannot be reconciled.
var ErrLegacySweepRecord = errors.New("legacy sweep record cannot be reconciled")

// BlockedJob explains why an address cannot be swept yet.
type BlockedJob struct {
	JobID      int64  `json:"job_id"`
	Chain      string `json:"chain"`
	Address    string `json:"address"`
	Status     string `json:"status"`
	Error      string `json:"error"`
	AgeMin     int64  `json:"age_minutes"`
	CanResolve bool   `json:"can_reconcile"`
}

// ListBlocked returns unfinished jobs that hold a reserved transaction, i.e.
// the ones that stop the chain from making progress.
func (e *Executor) ListBlocked(ctx context.Context, limit int) ([]BlockedJob, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := e.queries().QueryContext(ctx, `SELECT id, chain, from_address, status, error,
		GREATEST(0, EXTRACT(EPOCH FROM (NOW()-updated_at))/60)::bigint, job_version
		FROM sweep_records WHERE status <> 'swept'
		AND (status <> 'failed' OR gas_tx_hash <> '' OR sweep_tx_hash <> '')
		AND (gas_tx_hash<>'' OR sweep_tx_hash<>'')
		AND COALESCE(error,'') NOT LIKE 'stale pending released:%'
		AND COALESCE(error,'') NOT LIKE 'swept rollback released:%'
		ORDER BY updated_at ASC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []BlockedJob
	for rows.Next() {
		var b BlockedJob
		var version int
		if err := rows.Scan(&b.JobID, &b.Chain, &b.Address, &b.Status, &b.Error, &b.AgeMin, &version); err != nil {
			return nil, err
		}
		b.Error = rpc.SafeRecordedError(b.Error)
		// Reconcile can only record its findings on current-format rows.
		b.CanResolve = version == 1
		out = append(out, b)
	}
	return out, rows.Err()
}

// loadJob reads one job for an operator action.
func (e *Executor) loadJob(ctx context.Context, jobID int64) (*sweepJob, error) {
	row := e.queries().QueryRowContext(ctx, `SELECT `+jobColumns+` FROM sweep_records WHERE id=$1`, jobID)
	j := &sweepJob{}
	var raw string
	err := row.Scan(&j.ID, &j.Chain, &j.Address, &j.Destination, &j.Token, &raw,
		&j.Decimals, &j.DerivationIndex, &j.Status, &j.Error, &j.Version, &j.Revision,
		&j.Confirmations, &j.GasPayer, &j.GasAmount, &j.GasNonce, &j.GasRaw,
		&j.GasHash, &j.GasConfirmed, &j.SweepNonce, &j.SweepRaw, &j.SweepHash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("sweep job %d not found", jobID)
	}
	if err != nil {
		return nil, err
	}
	if j.Version == 1 {
		amount, ok := new(big.Int).SetString(raw, 10)
		if !ok || amount.Sign() <= 0 {
			return nil, fmt.Errorf("invalid immutable amount for sweep job %d", jobID)
		}
		// The sweep audit re-derives the recorded transfer from this amount; a
		// validated but discarded value would make every receipt look foreign.
		j.RawAmount = amount
	}
	return j, nil
}
