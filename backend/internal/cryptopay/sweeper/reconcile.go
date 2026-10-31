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
// without ever signing anything new:
//
//	sweep tx mined + confirmed -> swept
//	gas tx mined + confirmed   -> gas_confirmed (next run signs the token transfer)
//	tx reverted                -> failed (address released, needs a human look)
//	tx absent                  -> failed (dropped broadcast, address released)
//
// The address is only released when no transaction of this job can still land,
// which is exactly the "absent or reverted" case.
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
	if job.Status == "swept" || job.Status == "failed" {
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
			return run.finishReconcile(ctx, job, out, "failed",
				"confirmed token transfer reverted; address released after manual review",
				ReconcileReleased, "")
		case receiptWaiting:
			out.Action = ReconcileObserved
			out.Message = "token transfer is mined but still short of confirmations"
			return out, nil
		}
		return run.releaseAbsent(ctx, job, out, "token transfer")
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
			return run.finishReconcile(ctx, job, out, "failed",
				"gas top-up reverted; address released after manual review",
				ReconcileReleased, "")
		case receiptWaiting:
			out.Action = ReconcileObserved
			out.Message = "gas top-up is mined but still short of confirmations"
			return out, nil
		}
		return run.releaseAbsent(ctx, job, out, "gas top-up")
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

// releaseAbsent clears a job whose reserved transaction is not on chain.
// Nothing was broadcast from these bytes, so the nonce stays free and the
// address can be planned again on the next run.
func (e *Executor) releaseAbsent(ctx context.Context, job *sweepJob, out *ReconcileResult,
	label string) (*ReconcileResult, error) {
	msg := label + ": transaction not found on chain; released, no funds moved"
	return e.finishReconcile(ctx, job, out, "failed", msg, ReconcileReleased, msg)
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
		FROM sweep_records WHERE job_version=1 AND status NOT IN ('swept','failed')
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
