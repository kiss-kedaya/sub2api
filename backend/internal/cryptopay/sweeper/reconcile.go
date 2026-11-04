package sweeper

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"

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
	if job.Status == "swept" || (job.Status == "failed" && !hasTransaction) {
		out.Action = ReconcileObserved
		out.Message = "job already final; nothing to reconcile"
		return out, nil
	}
	pool := e.pools[job.Chain]
	if pool == nil {
		return nil, fmt.Errorf("no RPC pool for chain %s", job.Chain)
	}
	pool, err = pool.ForChain(ctx, uint64(ChainIDs[job.Chain]))
	if err != nil {
		return nil, err
	}
	required := e.config.Confirmations[job.Chain]
	if required == 0 {
		required = 1
	}

	// A signed token transfer is authoritative once it is mined.
	if job.SweepHash != "" {
		state, err := checkReceipt(ctx, pool, job.SweepHash, required)
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
		return run.retainUnconfirmed(ctx, job, out, false, "token transfer has no canonical receipt yet")
	}

	if job.GasHash != "" {
		state, err := checkReceipt(ctx, pool, job.GasHash, required)
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
		GREATEST(0, EXTRACT(EPOCH FROM (NOW()-updated_at))/60)::bigint
		FROM sweep_records WHERE status <> 'swept'
		AND (status <> 'failed' OR gas_tx_hash <> '' OR sweep_tx_hash <> '')
		AND (gas_tx_hash<>'' OR sweep_tx_hash<>'')
		ORDER BY updated_at ASC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []BlockedJob
	for rows.Next() {
		var b BlockedJob
		if err := rows.Scan(&b.JobID, &b.Chain, &b.Address, &b.Status, &b.Error, &b.AgeMin); err != nil {
			return nil, err
		}
		b.Error = rpc.SafeRecordedError(b.Error)
		b.CanResolve = true
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
		if amt, ok := new(big.Int).SetString(raw, 10); !ok || amt.Sign() <= 0 {
			return nil, fmt.Errorf("invalid immutable amount for sweep job %d", jobID)
		}
	}
	return j, nil
}
