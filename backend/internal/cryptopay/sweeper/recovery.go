package sweeper

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

// A sweep record is a cache of a chain fact, and it can be stale in the safe
// direction: the transaction is mined and buried, but the row never advanced.
// The 2026-11-13 BSC incident is exactly that shape - sweep_records.id=560 held
// a successful 6000 USDT BSC transfer, finality included, for five hours while
// the record said sweep_pending.
//
// The sweep ticker already resumes unfinished rows, but a resumed row passes
// through the stale-release and underpriced-replacement checks before settle()
// observes it. Those checks exist to prove a transaction can never be mined;
// running them first on a row whose transaction is already mined is wasted work
// and, with an inconsistent endpoint, a chance to release a live payment. This
// pass instead proves the mined fact first, read-only, and writes the one
// transition it justifies. No operator action, no manual SQL.
const (
	// defaultMinedRecoveryAge keeps the pass away from a transaction that was
	// broadcast moments ago: a row this young is still the normal settle path's
	// business.
	defaultMinedRecoveryAge = 10 * time.Minute
	// minedRecoveryLimit bounds one run's extra RPC work.
	minedRecoveryLimit = 20
)

// minedRecoveryQuery lists records that may be mined but unrecorded, oldest
// first. Only rows with a persisted sweep hash are eligible: without one there
// is nothing to look up.
const minedRecoveryQuery = `SELECT id FROM sweep_records
	WHERE job_version = 1 AND status = 'sweep_pending' AND sweep_tx_hash <> ''
	  AND updated_at < NOW() - ($1 * INTERVAL '1 second')
	ORDER BY updated_at ASC LIMIT $2`

// recoverMinedSweeps promotes records whose signed sweep transaction already
// satisfies the configured depth (and the chain's finality tag when it serves
// one) to swept. It signs, broadcasts and releases nothing: a row that is not
// proven on chain is left exactly as it is.
func (e *Executor) recoverMinedSweeps(ctx context.Context) (int, error) {
	if e.db == nil {
		return 0, nil
	}
	ids, err := e.minedSweepCandidates(ctx)
	if err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	// One chain-id check per chain per pass, exactly like the swept audit: the
	// probe covers every endpoint and the window can hold many rows per chain.
	bound := make(map[string]*rpc.Pool)
	broken := make(map[string]bool)
	var errs []error
	recovered := 0
	for _, id := range ids {
		if ctx.Err() != nil {
			errs = append(errs, ctx.Err())
			break
		}
		job, loadErr := e.loadJob(ctx, id)
		if loadErr != nil {
			errs = append(errs, loadErr)
			continue
		}
		if job.SweepHash == "" || job.Status != "sweep_pending" {
			continue
		}
		if broken[job.Chain] {
			continue
		}
		pool, cached := bound[job.Chain]
		if !cached {
			configured := e.pools[job.Chain]
			if configured == nil {
				// A removed chain cannot corroborate anything: keep the record
				// and say why the pass skipped it instead of guessing.
				broken[job.Chain] = true
				slog.Warn("[cryptopay] mined sweep recovery skipped: no RPC pool",
					"job_id", job.ID, "chain", job.Chain)
				continue
			}
			verified, chainErr := configured.ForChain(ctx, uint64(ChainIDs[job.Chain]))
			if chainErr != nil {
				broken[job.Chain] = true
				errs = append(errs, fmt.Errorf("mined sweep recovery %d: %w", job.ID, chainErr))
				continue
			}
			bound[job.Chain] = verified
			pool = verified
		}
		required := e.config.Confirmations[job.Chain]
		if required < job.Confirmations {
			required = job.Confirmations
		}
		state, checkErr := checkReceipt(ctx, pool, job.Chain, job.SweepHash, required)
		if checkErr != nil {
			slog.Warn("[cryptopay] mined sweep recovery inconclusive",
				"job_id", job.ID, "chain", job.Chain,
				"category", rpc.CategoryOf(checkErr), "endpoint_host", rpc.HostOf(checkErr),
				"detail", rpc.DetailOf(checkErr), "error", rpc.SafeError(checkErr))
			errs = append(errs, fmt.Errorf("mined sweep recovery %d: %w", job.ID, checkErr))
			continue
		}
		if state != receiptConfirmed {
			slog.Info("[cryptopay] mined sweep recovery: record stays pending",
				"job_id", job.ID, "chain", job.Chain, "state", receiptStateName(state))
			continue
		}
		changed, markErr := e.markMinedSweepRecovered(ctx, job)
		if markErr != nil {
			errs = append(errs, markErr)
			continue
		}
		if !changed {
			slog.Info("[cryptopay] mined sweep recovery: record changed during recovery",
				"job_id", job.ID, "chain", job.Chain)
			continue
		}
		recovered++
		slog.Warn("[cryptopay] mined sweep record recovered to swept without an operator",
			"job_id", job.ID, "chain", job.Chain, "sweep_tx_hash", job.SweepHash)
	}
	return recovered, errors.Join(errs...)
}

// minedSweepCandidates drains the eligible ids before any RPC work, so a
// single-connection pool is never held by an open cursor.
func (e *Executor) minedSweepCandidates(ctx context.Context) ([]int64, error) {
	rows, err := e.queries().QueryContext(ctx, minedRecoveryQuery,
		int64(defaultMinedRecoveryAge/time.Second), minedRecoveryLimit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// markMinedSweepRecovered records the chain fact. The status and the hash are
// both part of the predicate: a row that moved on in the meantime keeps its
// newer state, and this pass can never overwrite a reconcile or a release.
func (e *Executor) markMinedSweepRecovered(ctx context.Context, job *sweepJob) (bool, error) {
	result, err := e.queries().ExecContext(ctx, `UPDATE sweep_records
		SET status = 'swept', error = '', revision = revision + 1, updated_at = NOW()
		WHERE id = $1 AND job_version = 1 AND status = 'sweep_pending' AND sweep_tx_hash = $2`,
		job.ID, job.SweepHash)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected == 1, nil
}

// receiptStateName is the fixed word used in logs for an observation result.
func receiptStateName(state receiptState) string {
	switch state {
	case receiptAbsent:
		return "absent"
	case receiptConfirmed:
		return "confirmed"
	case receiptReverted:
		return "reverted"
	}
	return "waiting"
}
