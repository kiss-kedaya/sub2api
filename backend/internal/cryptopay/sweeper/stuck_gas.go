package sweeper

import (
	"context"
	"encoding/hex"
	"log/slog"
	"math/big"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
	"github.com/ethereum/go-ethereum/core/types"
)

// supersededGasError is stored on a job whose gas top-up can no longer be mined.
// rpc.SafeRecordedError lists the same sentence; a test keeps the two in step.
const supersededGasError = "gas top-up superseded by another transaction at the same nonce; the address will be funded again"

// refreshStuckGas looks at a gas top-up that was signed earlier and has no
// receipt, before settle re-sends the same bytes. Re-sending cannot help when:
//
//   - the payer has since mined something else at that nonce: the old bytes are
//     dead, and the job goes back to "planned" so the next step funds it again;
//   - the fee in the signed bytes is below what the chain asks today: no
//     validator will include it, and because gasPayerBusy treats any unconfirmed
//     top-up as holding the payer, it stops every other address on the chain.
//     The same payment is signed again at the same nonce with a higher fee. Two
//     transactions cannot both be mined at one nonce, so this cannot pay twice.
//
// Any failure to read the chain leaves the job exactly as it was; only settle
// decides what an unreadable chain means.
func (e *Executor) refreshStuckGas(ctx context.Context, pool *rpc.Pool, j *sweepJob) error {
	if j.GasRaw == "" || j.GasHash == "" || j.GasConfirmed || j.GasPayer == "" || j.SweepRaw != "" || j.SweepHash != "" {
		return nil
	}
	nonce, err := strconv.ParseUint(j.GasNonce, 10, 64)
	if err != nil {
		return nil
	}
	// Anything other than a clean "no receipt" (mined, reverted, unreadable) is
	// settle's business.
	if state, err := checkReceipt(ctx, pool, j.GasHash, 1); err != nil || state != receiptAbsent {
		return nil
	}
	confirmed, err := pool.ConfirmedNonce(ctx, j.GasPayer)
	if err != nil {
		return nil
	}
	if confirmed > nonce {
		return e.releaseSupersededGas(ctx, j)
	}
	return e.replaceUnderpricedGas(ctx, pool, j, nonce)
}

func (e *Executor) releaseSupersededGas(ctx context.Context, j *sweepJob) error {
	slog.Error("[cryptopay] gas top-up can no longer be mined; funding the address again",
		"job_id", j.ID, "chain", j.Chain)
	j.GasPayer, j.GasAmount, j.GasNonce, j.GasRaw, j.GasHash = "", "", "", "", ""
	j.GasConfirmed = false
	j.Status, j.Error = "planned", supersededGasError
	return e.saveJob(ctx, j)
}

func (e *Executor) replaceUnderpricedGas(ctx context.Context, pool *rpc.Pool, j *sweepJob, nonce uint64) error {
	quote, err := pool.GasPrice(ctx)
	if err != nil {
		return nil
	}
	oldCap, ok := feeCapOf(j.GasRaw)
	if !ok || oldCap.Cmp(quote) >= 0 {
		return nil
	}
	price := replacementPrice(oldCap, quote)
	// The operator's native-coin budget bounds this fee like any other.
	if budget := configuredGasBudget(e.config, j.Chain); budget.Sign() <= 0 || transferGasBudget(price).Cmp(budget) > 0 {
		slog.Error("[cryptopay] stuck gas top-up not replaced: current gas quote exceeds the configured funding budget",
			"job_id", j.ID, "chain", j.Chain)
		return nil
	}
	if e.wallet == nil {
		return nil
	}
	hot, err := e.wallet.HotAddresses(ctx)
	if err != nil || !strings.EqualFold(hot.EVM, j.GasPayer) {
		return nil
	}
	amount, ok := new(big.Int).SetString(j.GasAmount, 10)
	if !ok || amount.Sign() <= 0 {
		return nil
	}
	// The replacement must be affordable too. Signing bytes the payer cannot
	// fund would leave another unmineable gas_pending row behind, which is the
	// state that blocks every other address on the chain.
	balance, err := pool.Balance(ctx, j.GasPayer)
	if err != nil {
		return nil
	}
	required := new(big.Int).Add(amount, new(big.Int).Mul(price, big.NewInt(21000)))
	if balance.Cmp(required) < 0 {
		slog.Warn("[cryptopay] stuck gas top-up not replaced: gas wallet cannot cover the higher fee",
			"job_id", j.ID, "chain", j.Chain)
		return nil
	}
	idx := j.DerivationIndex
	raw, err := e.wallet.SignEVM(ctx, EVMSignRequest{Index: hot.Index, Kind: "gas", GasIndex: &idx,
		ChainID: ChainIDs[j.Chain], Token: "native", To: j.Address, Amount: amount.String(),
		Nonce: nonce, GasPrice: price.String(), GasLimit: 21000})
	if err != nil {
		return err
	}
	hash, err := validateSigned(raw, j.Chain, j.GasPayer, j.Address, "native", amount, nonce, 21000, price)
	if err != nil {
		return err
	}
	// Persist before anything is sent, like every other signed transaction.
	oldRaw, oldHash := j.GasRaw, j.GasHash
	j.GasRaw, j.GasHash = raw, hash
	if err := e.saveJob(ctx, j); err != nil {
		j.GasRaw, j.GasHash = oldRaw, oldHash
		return err
	}
	slog.Error("[cryptopay] stuck gas top-up replaced with a higher fee at the same nonce",
		"job_id", j.ID, "chain", j.Chain, "old_fee_cap_wei", oldCap.String(), "new_fee_cap_wei", price.String())
	return nil
}

// replacementPrice is today's quote, but never less than the 12.5% (+1 wei) a
// node needs to see before it accepts a replacement for a transaction it already
// holds (nodes ask for at least 10% more, and the +1 covers tiny values).
func replacementPrice(oldCap, quote *big.Int) *big.Int {
	price := new(big.Int).Set(quote)
	bump := new(big.Int).Add(oldCap, new(big.Int).Div(oldCap, big.NewInt(8)))
	bump.Add(bump, big.NewInt(1))
	if bump.Cmp(price) > 0 {
		price = bump
	}
	return price
}

// feeCapOf reads the maximum fee per gas out of signed transaction bytes.
func feeCapOf(raw string) (*big.Int, bool) {
	encoded, err := hex.DecodeString(strings.TrimPrefix(raw, "0x"))
	if err != nil {
		return nil, false
	}
	var tx types.Transaction
	if err := tx.UnmarshalBinary(encoded); err != nil {
		return nil, false
	}
	return tx.GasFeeCap(), true
}
