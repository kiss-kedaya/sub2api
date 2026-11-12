package sweeper

import (
	"context"
	"log/slog"
	"math/big"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

// A sweep signed below the chain's gas price can never be mined. Re-sending
// those bytes cannot help, and because the nonce stays taken in every pool that
// holds them, releasing the record leaves the address unable to move at all:
// the next sweep signs at the higher pending nonce and queues behind a
// transaction that will never land.
//
// The 2026-11-13 BSC incident is exactly that case: sweep_records.id=560 signed
// gasPrice 0x1 (1 wei) at nonce 0x0 while the network asked 0x2faf080
// (50,000,000 wei). This is the sweep counterpart of refreshStuckGas: the same
// payment, the same nonce, a higher fee.

// refreshUnderpricedSweep re-signs an underpriced sweep transaction at the same
// nonce, keeping chain, destination, amount and gas limit and raising only the
// fee. It returns nil when there is nothing to do, which includes every case it
// must not act on:
//
//   - a receipt exists (mined or reverted) or the chain is unreadable: settle
//     owns that decision, and this function never replaces a landable payment;
//   - the nonce is already mined past: the old bytes are dead, and the record is
//     handled by the stale-release path instead;
//   - the signed fee is not below today's quote: nothing is underpriced;
//   - the address cannot pay the higher fee from the balance it is sweeping.
func (e *Executor) refreshUnderpricedSweep(ctx context.Context, pool *rpc.Pool, j *sweepJob) error {
	if j.SweepRaw == "" || j.SweepHash == "" || j.SweepNonce == "" || j.Address == "" {
		return nil
	}
	if j.RawAmount == nil || j.RawAmount.Sign() <= 0 {
		return nil
	}
	nonce, err := strconv.ParseUint(j.SweepNonce, 10, 64)
	if err != nil {
		return nil
	}
	// Anything other than a clean "no receipt" (mined, reverted, unreadable) is
	// settle's business.
	if state, err := checkReceipt(ctx, pool, j.Chain, j.SweepHash, 1); err != nil || state != receiptAbsent {
		return nil
	}
	confirmed, err := pool.ConfirmedNonce(ctx, j.Address)
	if err != nil || confirmed > nonce {
		return nil
	}
	quote, err := pool.GasPrice(ctx)
	if err != nil {
		return nil
	}
	oldCap, ok := feeCapOf(j.SweepRaw)
	if !ok || oldCap.Cmp(quote) >= 0 {
		return nil
	}
	price := replacementPrice(oldCap, quote)
	// The pool already clamps to the chain's floor; repeating it here keeps a
	// pool that could not prove its chain from signing a dead fee.
	if floor := rpc.GasPriceFloor(uint64(ChainIDs[j.Chain])); price.Cmp(floor) < 0 {
		price = floor
	}
	// Keep the exact limit the old bytes were signed with: raising it as well
	// would make the fee comparison meaningless and could exceed the balance.
	gasLimit := signedGasLimit(j.SweepRaw)
	if gasLimit == 0 {
		gasLimit = e.sweepGasLimitFor(j.Chain, j.Token)
	}
	// The deposit address pays this fee out of the balance the sweep empties, so
	// the higher fee has to fit too. Signing bytes it cannot fund would replace
	// one unmineable transaction with another.
	needed := new(big.Int).Mul(price, new(big.Int).SetUint64(gasLimit))
	if e.isNativeSweep(j.Chain, j.Token) {
		needed.Add(needed, j.RawAmount)
	}
	balance, err := pool.Balance(ctx, j.Address)
	if err != nil {
		return nil
	}
	if balance.Cmp(needed) < 0 {
		slog.Warn("[cryptopay] underpriced sweep not replaced: the address cannot cover the higher fee",
			"job_id", j.ID, "chain", j.Chain, "nonce", nonce,
			"shortfall_wei", new(big.Int).Sub(needed, balance).String())
		return nil
	}
	if e.wallet == nil {
		return nil
	}
	raw, err := e.wallet.SignEVM(ctx, EVMSignRequest{Index: j.DerivationIndex, Kind: "sweep",
		ChainID: ChainIDs[j.Chain], Token: j.Token, To: j.Destination, Amount: j.RawAmount.String(),
		Nonce: nonce, GasPrice: price.String(), GasLimit: gasLimit})
	if err != nil {
		return err
	}
	hash, err := validateSigned(raw, j.Chain, j.Address, j.Destination, j.Token, j.RawAmount, nonce, gasLimit, price)
	if err != nil {
		return err
	}
	// Persist before anything is sent, like every other signed transaction.
	oldRaw, oldHash := j.SweepRaw, j.SweepHash
	j.SweepRaw, j.SweepHash = raw, hash
	if err := e.saveJob(ctx, j); err != nil {
		j.SweepRaw, j.SweepHash = oldRaw, oldHash
		return err
	}
	slog.Error("[cryptopay] stuck sweep transaction replaced with a higher fee at the same nonce",
		"job_id", j.ID, "chain", j.Chain, "nonce", nonce,
		"old_fee_cap_wei", oldCap.String(), "new_fee_cap_wei", price.String())
	return nil
}

// sweepNonce picks the nonce for a new sweep. It is the pool's pending nonce,
// except when this address still has an un-mined transaction at a lower nonce
// that this executor can prove can never be mined (a released record whose
// signed fee is below the chain's floor). Signing at the pending nonce would
// queue the new payment behind that dead transaction forever, so the current
// plan is signed at the lower nonce instead: one nonce, one winner, and the
// properly priced replacement is what lands.
func (e *Executor) sweepNonce(ctx context.Context, pool *rpc.Pool, j *sweepJob) (uint64, error) {
	nonce, err := pool.Nonce(ctx, j.Address)
	if err != nil {
		return 0, err
	}
	confirmed, err := pool.ConfirmedNonce(ctx, j.Address)
	if err != nil || nonce <= confirmed {
		return nonce, nil
	}
	if !e.releasedUnderpricedSweep(ctx, j.Chain, j.Address, confirmed) {
		return nonce, nil
	}
	slog.Error("[cryptopay] sweep reuses the nonce of a released underpriced transaction",
		"job_id", j.ID, "chain", j.Chain, "nonce", confirmed,
		"pending_nonce", nonce)
	return confirmed, nil
}

// releasedUnderpricedSweep reports whether this address has a record that was
// released as stale whose persisted bytes at exactly nonce were signed below the
// chain's gas price floor. Only that provably dead shape is reused: any other
// pending transaction may still be mined, and replacing it is not this
// function's decision.
func (e *Executor) releasedUnderpricedSweep(ctx context.Context, chain, address string, nonce uint64) bool {
	var raw string
	err := e.queries().QueryRowContext(ctx, `SELECT sweep_raw_tx FROM sweep_records
		WHERE chain=$1 AND lower(from_address)=lower($2) AND sweep_nonce=$3
		  AND status='failed' AND COALESCE(error,'') LIKE $4 AND sweep_raw_tx <> ''
		ORDER BY id DESC LIMIT 1`,
		chain, address, strconv.FormatUint(nonce, 10), staleReleasePrefix+"%").Scan(&raw)
	if err != nil || raw == "" {
		return false
	}
	cap, ok := feeCapOf(raw)
	if !ok {
		return false
	}
	return cap.Cmp(chainGasPriceFloor(chain)) < 0
}
