package sweeper

import (
	"context"
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
		return fmt.Errorf("current credited sweep balance or address assignment is invalid")
	}
	if toDecimal(balance.Raw, job.Decimals) < e.config.MinPerAddress[job.Chain] {
		return fmt.Errorf("current credited balance is below the per-address sweep minimum")
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
