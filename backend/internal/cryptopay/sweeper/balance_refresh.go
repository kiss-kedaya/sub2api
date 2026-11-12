package sweeper

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

// Only the token transaction freezes the token amount. An old preview or a
// previously signed gas payment must not omit deposits credited before execution.
func (e *Executor) refreshUnsignedAmount(ctx context.Context, job *sweepJob) error {
	if job.Version != 1 || job.SweepRaw != "" || job.SweepHash != "" {
		return nil
	}
	reader, ok := e.reader.(CurrentBalanceReader)
	if !ok {
		return nil // readers without a fresh-ledger contract keep their fixed plan
	}
	balance, err := reader.CurrentBalance(ctx, job.Chain, job.Address, job.Token)
	if err != nil {
		return err
	}
	if balance == nil || balance.Chain != job.Chain ||
		!strings.EqualFold(balance.Address, job.Address) || !strings.EqualFold(balance.Token, job.Token) ||
		balance.Decimals != job.Decimals || balance.DerivationIndex != job.DerivationIndex ||
		balance.Raw == nil || balance.Raw.Sign() <= 0 || balance.Raw.BitLen() > 256 {
		return fmt.Errorf("%w: current credited balance or assignment is no longer valid", errStaleSweepPlan)
	}
	if toDecimal(balance.Raw, job.Decimals) < e.config.MinPerAddress[job.Chain] {
		return fmt.Errorf("%w: current credited balance is below the per-address sweep minimum", errStaleSweepPlan)
	}
	if balance.Raw.Cmp(job.RawAmount) == 0 {
		return nil
	}
	// Preserve every signed gas byte and nonce. The CAS also checks that a token
	// signature has not appeared since the job was read.
	result, err := e.queries().ExecContext(ctx, `UPDATE sweep_records SET
		amount=$2, raw_amount=$3, revision=revision+1, updated_at=NOW()
		WHERE id=$1 AND job_version=1 AND revision=$4
		AND status IN ('planned','gas_pending','gas_confirmed')
		AND sweep_raw_tx='' AND sweep_tx_hash=''`, job.ID,
		amountString(balance.Raw, job.Decimals), balance.Raw.String(), job.Revision)
	if err == nil {
		var changed int64
		changed, err = result.RowsAffected()
		if err == nil && changed != 1 {
			err = fmt.Errorf("unsigned sweep job changed before balance refresh")
		}
	}
	if err != nil {
		return &persistenceError{err}
	}
	job.RawAmount = new(big.Int).Set(balance.Raw)
	job.AmountDecimal = toDecimal(job.RawAmount, job.Decimals)
	job.Revision++
	return nil
}

func (e *Executor) checkCurrentSweepBalance(ctx context.Context, pool *rpc.Pool, job *sweepJob) error {
	if err := e.refreshUnsignedAmount(ctx, job); err != nil {
		return err
	}
	if e.isNativeSweep(job.Chain, job.Token) {
		// Native coin has no balanceOf: read the address balance directly. The
		// planned amount was already capped below that balance minus the gas
		// reserve, so this only has to prove the funds are still there.
		balance, err := pool.Balance(ctx, job.Address)
		if err != nil {
			return err
		}
		if balance.Cmp(job.RawAmount) < 0 {
			return fmt.Errorf("live native balance %s is below planned amount %s", balance, job.RawAmount)
		}
		return nil
	}
	// Read through the existing RPC pool rather than deriving the sweep amount
	// from uncredited on-chain funds.
	balance, err := e.tokenBalance(ctx, pool, job.Token, job.Address)
	if err != nil {
		return err
	}
	if balance.Cmp(job.RawAmount) < 0 {
		return fmt.Errorf("live balance %s is below planned amount %s", balance, job.RawAmount)
	}
	return nil
}

// errNativeSweepBelowGasReserve means the address holds no native coin beyond
// the fee reserve a native sweep must leave behind, so there is nothing that can
// be sent. It is a wait-for-funds state, never a failed payment.
var errNativeSweepBelowGasReserve = errors.New("native balance does not exceed the sweep gas reserve; nothing sweepable yet")

// capNativeSweepAmount lowers an unsigned native sweep to what the address can
// actually send while keeping nativeSweepGasReserve behind.
//
// The credited ledger balance stays the authorization ceiling: this only ever
// shrinks the amount, never grows it past what was credited. The leftover
// (credited minus swept) is the reserve itself, which the planner ignores
// because it is at or below that same reserve.
func (e *Executor) capNativeSweepAmount(ctx context.Context, pool *rpc.Pool, job *sweepJob, price *big.Int) error {
	live, err := pool.Balance(ctx, job.Address)
	if err != nil {
		return err
	}
	sweepable := new(big.Int).Sub(live, nativeSweepGasReserve(price))
	if sweepable.Sign() <= 0 {
		return errNativeSweepBelowGasReserve
	}
	if sweepable.Cmp(job.RawAmount) >= 0 {
		return nil
	}
	result, err := e.queries().ExecContext(ctx, `UPDATE sweep_records SET
		amount=$2, raw_amount=$3, revision=revision+1, updated_at=NOW()
		WHERE id=$1 AND job_version=1 AND revision=$4
		AND status IN ('planned','gas_pending','gas_confirmed')
		AND sweep_raw_tx='' AND sweep_tx_hash=''`, job.ID,
		amountString(sweepable, job.Decimals), sweepable.String(), job.Revision)
	if err == nil {
		var changed int64
		changed, err = result.RowsAffected()
		if err == nil && changed != 1 {
			err = fmt.Errorf("unsigned sweep job changed before native amount cap")
		}
	}
	if err != nil {
		return &persistenceError{err}
	}
	job.RawAmount = new(big.Int).Set(sweepable)
	job.AmountDecimal = toDecimal(job.RawAmount, job.Decimals)
	job.Revision++
	return nil
}

// errStaleSweepPlan means the job no longer matches the credited ledger or the
// address assignment. It is released rather than retried, so the planner can
// create a fresh job from the current balance in the same run.
var errStaleSweepPlan = errors.New("stale sweep plan")
