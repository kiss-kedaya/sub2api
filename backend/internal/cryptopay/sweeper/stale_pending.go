package sweeper

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

// A signed transaction that no configured node can see, at a nonce the account
// has already moved past, can never be mined. Keeping its reservation blocks
// every later sweep of the same address, which is exactly how one BSC job
// (nonce 2, tx hash 0x5112…) held its address for hours after the nonce had
// been consumed by another transaction.
const (
	// stalePendingAge is how long a job may sit at a nonce that is already
	// taken in a node's pool before the original bytes are replayed and the
	// record is concluded to be dead. Config may override it in tests.
	defaultStalePendingAge = 45 * time.Minute
	// staleRebroadcasts is how many times the exact persisted bytes are sent
	// again before a job is released on the pool-consumed path.
	staleRebroadcasts = 3
	// defaultStaleRebroadcastDelay spaces those replays; zero disables the wait.
	defaultStaleRebroadcastDelay = 2 * time.Second
	// staleReleasePrefix marks a failed record whose reservation has been
	// deliberately released. reservedPredicate excludes this prefix so the
	// address is truly free for a new job while the dead hash stays visible.
	staleReleasePrefix = "stale pending released: "
)

func (e *Executor) stalePendingAge() time.Duration {
	if e.config.StalePendingAge > 0 {
		return e.config.StalePendingAge
	}
	return defaultStalePendingAge
}

func (e *Executor) staleRebroadcastDelay() time.Duration {
	if e.config.StalePendingRebroadcastDelay > 0 {
		return e.config.StalePendingRebroadcastDelay
	}
	return defaultStaleRebroadcastDelay
}

// jobAgeMinutes reads how long the record has existed in its pending shape.
// It deliberately uses created_at, not updated_at: every resume that finds the
// receipt absent rewrites the error sentence, and an updated_at-based age would
// therefore never reach the threshold. It is only asked for an already-absent
// transaction, so the extra query never runs on the normal resume path.
func (e *Executor) jobAgeMinutes(ctx context.Context, id int64) (int64, error) {
	var age int64
	err := e.queries().QueryRowContext(ctx, `SELECT GREATEST(0, EXTRACT(EPOCH FROM (NOW()-created_at))/60)::bigint
		FROM sweep_records WHERE id=$1`, id).Scan(&age)
	return age, err
}

// releaseStaleTransaction concludes that the job's persisted transaction can
// never be mined and moves the job to failed with a readable reason. It returns
// true only after the release has been persisted. allowRebroadcast is false for
// the operator reconcile path, which promises to be read-only; such a call can
// still release a nonce the chain has already mined past.
//
// Safety rules, in order:
//   - an unreadable chain, or a transaction a node can still see, never releases;
//   - a nonce below the account's latest (mined) nonce is consumed for good;
//   - a nonce that is only taken in the pending pool is released only after the
//     record is older than the threshold AND the exact bytes were replayed
//     staleRebroadcasts times and still cannot be found;
//   - a nonce that is still free in the pool never releases: the transaction
//     may simply be waiting in a mempool.
func (e *Executor) releaseStaleTransaction(ctx context.Context, pool *rpc.Pool, j *sweepJob, gas, allowRebroadcast bool) (bool, error) {
	hash, raw, nonceStr, address := j.SweepHash, j.SweepRaw, j.SweepNonce, j.Address
	if gas {
		hash, raw, nonceStr, address = j.GasHash, j.GasRaw, j.GasNonce, j.GasPayer
	}
	if hash == "" || raw == "" || address == "" {
		return false, nil
	}
	nonce, err := strconv.ParseUint(nonceStr, 10, 64)
	if err != nil {
		return false, nil
	}
	present, err := pool.TransactionByHash(ctx, hash)
	if err != nil || present {
		// Transport/timeout: keep the reservation. Present: it will confirm.
		return false, nil
	}
	latest, err := pool.ConfirmedNonce(ctx, address)
	if err != nil {
		return false, nil
	}
	if latest > nonce {
		reason := fmt.Sprintf("%stx absent on chain; nonce %d already consumed (latest nonce %d); released",
			staleReleasePrefix, nonce, latest)
		return true, e.markStaleReleased(ctx, j, gas, reason)
	}
	pending, err := pool.Nonce(ctx, address)
	if err != nil {
		return false, nil
	}
	if pending <= nonce {
		// The slot is still free: the original transaction may be waiting in a
		// mempool we cannot query. Releasing here would allow a second sweep.
		return false, nil
	}
	if !allowRebroadcast {
		// The reconcile view is read-only. A later sweep pass replays the exact
		// bytes and releases this record if it is genuinely dead.
		return false, nil
	}
	age, err := e.jobAgeMinutes(ctx, j.ID)
	if err != nil {
		return false, nil
	}
	if time.Duration(age)*time.Minute < e.stalePendingAge() {
		return false, nil
	}
	delay := e.staleRebroadcastDelay()
	for i := 0; i < staleRebroadcasts; i++ {
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return false, ctx.Err()
			case <-timer.C:
			}
		}
		// Reuse the exact persisted bytes; never sign a replacement here.
		if _, sendErr := pool.SendRawTransaction(ctx, raw); sendErr != nil {
			if rpc.IsAlreadyKnown(sendErr) {
				return false, nil // a node holds these bytes: it can still land
			}
			if !rpc.IsRejectedKnown(sendErr) {
				// Unreadable chain: leave the record untouched.
				return false, nil
			}
		}
		if present, err := pool.TransactionByHash(ctx, hash); err != nil || present {
			return false, nil
		}
	}
	latest, err = pool.ConfirmedNonce(ctx, address)
	if err != nil {
		return false, nil
	}
	pending, err = pool.Nonce(ctx, address)
	if err != nil {
		return false, nil
	}
	if latest <= nonce && pending <= nonce {
		return false, nil // the slot came back: never release a live nonce
	}
	reason := fmt.Sprintf("%stx absent after %d replays; nonce %d is taken in the pending pool; released",
		staleReleasePrefix, staleRebroadcasts, nonce)
	return true, e.markStaleReleased(ctx, j, gas, reason)
}

func (e *Executor) markStaleReleased(ctx context.Context, j *sweepJob, gas bool, reason string) error {
	slog.Error("[cryptopay] stale sweep transaction released",
		"job_id", j.ID, "chain", j.Chain, "gas", gas, "reason", reason)
	j.Status, j.Error = "failed", reason
	return e.saveJob(ctx, j)
}
