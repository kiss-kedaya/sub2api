package sweeper

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

var (
	errGasPayerPending = errors.New("gas payer has an unresolved transaction; funding deferred")
	// errGasBudgetInsufficient marks a top-up the operator's native budget
	// cannot cover right now. Waiting for a cheaper quote or a raised budget is
	// not a failed payment, so the caller defers instead of spending an attempt.
	errGasBudgetInsufficient = errors.New("insufficient native gas budget to fund this sweep; it will be retried when the gas budget allows")
	// errGasPayerUnderfunded means the gas wallet cannot pay for the top-up plus
	// its own transfer fee. Signing anyway produced bytes no node accepts
	// ("insufficient funds for gas"); the row then stayed gas_pending with a
	// hash and blocked every other address on the chain, because only a
	// confirmation releases a payer and an unfundable transaction never
	// confirms.
	errGasPayerUnderfunded = errors.New("gas wallet native balance cannot cover another top-up; fund the gas wallet and the sweep will resume")
	// errGasQuoteExceedsBudget is a temporary market condition, not a failure.
	errGasQuoteExceedsBudget = errors.New("RPC gas quote exceeds the configured native funding budget")
)

func (e *Executor) prepareGas(ctx context.Context, pool *rpc.Pool, j *sweepJob, amount, price *big.Int) error {
	// A zero (or negative) top-up cannot help the child and would only produce a
	// signature that validation must reject. Treat it as unfunded, not failed.
	if amount == nil || amount.Sign() <= 0 || price == nil || price.Sign() <= 0 {
		return errGasBudgetInsufficient
	}
	if e.wallet == nil {
		return fmt.Errorf("wallet is not initialized")
	}
	hot, err := e.wallet.HotAddresses(ctx)
	if err != nil {
		return err
	}
	if !common.IsHexAddress(hot.EVM) || isZeroAddress(hot.EVM) {
		return fmt.Errorf("wallet returned invalid gas payer")
	}
	busy, err := e.gasPayerBusy(ctx, j, hot.EVM)
	if err != nil {
		return &persistenceError{err}
	}
	if busy {
		return errGasPayerPending
	}
	// The payer also pays the 21000-gas fee of this transfer. Check it here,
	// before anything is signed or persisted, so a drained gas wallet defers
	// instead of leaving an unmineable reservation behind.
	balance, err := pool.Balance(ctx, hot.EVM)
	if err != nil {
		return err
	}
	required := new(big.Int).Add(amount, new(big.Int).Mul(price, big.NewInt(21000)))
	if balance.Cmp(required) < 0 {
		slog.Warn("[cryptopay] gas wallet cannot cover the top-up",
			"job_id", j.ID, "chain", j.Chain,
			"shortfall_wei", new(big.Int).Sub(required, balance).String())
		return errGasPayerUnderfunded
	}
	// The receiving address may be a cold wallet. Its nonce is irrelevant.
	nonce, err := pool.Nonce(ctx, hot.EVM)
	if err != nil {
		return err
	}
	idx := j.DerivationIndex
	raw, err := e.wallet.SignEVM(ctx, EVMSignRequest{Index: hot.Index, Kind: "gas", GasIndex: &idx,
		ChainID: ChainIDs[j.Chain], Token: "native", To: j.Address, Amount: amount.String(),
		Nonce: nonce, GasPrice: price.String(), GasLimit: 21000})
	if err != nil {
		return err
	}
	hash, err := validateSigned(raw, j.Chain, hot.EVM, j.Address, "native", amount, nonce, 21000, price)
	if err != nil {
		return err
	}
	j.GasPayer, j.GasAmount, j.GasNonce = hot.EVM, amount.String(), strconv.FormatUint(nonce, 10)
	j.GasRaw, j.GasHash, j.Status, j.Error = raw, hash, "gas_pending", ""
	return e.saveJob(ctx, j) // autocommit; broadcast is only in settle
}

func (e *Executor) prepareSweep(ctx context.Context, pool *rpc.Pool, j *sweepJob, price *big.Int) error {
	if e.wallet == nil {
		return fmt.Errorf("wallet is not initialized")
	}
	nonce, err := pool.Nonce(ctx, j.Address)
	if err != nil {
		return err
	}
	raw, err := e.wallet.SignEVM(ctx, EVMSignRequest{Index: j.DerivationIndex, Kind: "sweep",
		ChainID: ChainIDs[j.Chain], Token: j.Token, To: j.Destination, Amount: j.RawAmount.String(),
		Nonce: nonce, GasPrice: price.String(), GasLimit: transferGasLimit})
	if err != nil {
		return err
	}
	hash, err := validateSigned(raw, j.Chain, j.Address, j.Destination, j.Token,
		j.RawAmount, nonce, transferGasLimit, price)
	if err != nil {
		return err
	}
	j.SweepNonce, j.SweepRaw, j.SweepHash = strconv.FormatUint(nonce, 10), raw, hash
	j.Status, j.Error = "sweep_pending", ""
	return e.saveJob(ctx, j)
}

// Decode and verify the signed bytes, rather than trusting a wallet/RPC hash.
// This also detects seed/index changes before any funds can be spent.
func validateSigned(raw, chain, from, to, token string, amount *big.Int, nonce, gas uint64, price *big.Int) (string, error) {
	encoded, err := hex.DecodeString(strings.TrimPrefix(raw, "0x"))
	if err != nil {
		return "", fmt.Errorf("invalid signed transaction encoding: %w", err)
	}
	var tx types.Transaction
	if err := tx.UnmarshalBinary(encoded); err != nil {
		return "", fmt.Errorf("invalid signed transaction: %w", err)
	}
	chainID := ChainIDs[chain]
	if chainID <= 0 || tx.ChainId().Cmp(big.NewInt(chainID)) != 0 ||
		tx.Nonce() != nonce || !gasLimitMatches(tx.Gas(), gas) || tx.To() == nil ||
		!common.IsHexAddress(from) || !common.IsHexAddress(to) || amount == nil || amount.Sign() <= 0 ||
		(tx.Type() != types.DynamicFeeTxType && tx.Type() != types.LegacyTxType) {
		return "", fmt.Errorf("signed transaction does not match reserved chain/nonce/gas")
	}
	sender, err := types.Sender(types.LatestSignerForChainID(big.NewInt(chainID)), &tx)
	if err != nil || sender != common.HexToAddress(from) {
		return "", fmt.Errorf("signed transaction sender does not match reserved payer")
	}
	if tx.GasFeeCap().Sign() <= 0 || tx.GasTipCap().Cmp(tx.GasFeeCap()) > 0 ||
		(price != nil && tx.GasFeeCap().Cmp(price) != 0) {
		return "", fmt.Errorf("signed transaction fee does not match request")
	}
	if token == "native" {
		if *tx.To() != common.HexToAddress(to) || tx.Value().Cmp(amount) != 0 || len(tx.Data()) != 0 {
			return "", fmt.Errorf("signed gas payment differs from reserved payment")
		}
	} else {
		if !common.IsHexAddress(token) || amount.BitLen() > 256 {
			return "", fmt.Errorf("invalid reserved token/amount")
		}
		data := common.FromHex("0xa9059cbb")
		data = append(data, common.LeftPadBytes(common.HexToAddress(to).Bytes(), 32)...)
		data = append(data, common.LeftPadBytes(amount.Bytes(), 32)...)
		if *tx.To() != common.HexToAddress(token) || tx.Value().Sign() != 0 || !bytes.Equal(tx.Data(), data) {
			return "", fmt.Errorf("signed token transfer differs from immutable plan")
		}
	}
	return tx.Hash().Hex(), nil
}

type receiptState int

const (
	receiptAbsent receiptState = iota
	receiptWaiting
	receiptConfirmed
	receiptReverted
)

// No polling loops: a pending job returns control to the rest of the group.
// Missing confirmation policy permits observation, never finalization.
func checkReceipt(ctx context.Context, pool *rpc.Pool, chain, hash string, required uint64) (receiptState, error) {
	result, err := pool.Call(ctx, "eth_getTransactionReceipt", []any{hash})
	if err != nil {
		return receiptWaiting, err
	}
	if len(result) == 0 || string(result) == "null" {
		return receiptAbsent, nil
	}
	var r struct {
		Hash        string `json:"transactionHash"`
		Status      string `json:"status"`
		BlockNumber string `json:"blockNumber"`
		BlockHash   string `json:"blockHash"`
	}
	if err := json.Unmarshal(result, &r); err != nil {
		return receiptWaiting, err
	}
	if !strings.EqualFold(r.Hash, hash) || len(r.BlockHash) != 66 ||
		(r.Status != "0x1" && r.Status != "0x0") {
		return receiptWaiting, fmt.Errorf("incomplete or mismatched receipt")
	}
	block, err := strconv.ParseUint(strings.TrimPrefix(r.BlockNumber, "0x"), 16, 64)
	if err != nil {
		return receiptWaiting, err
	}
	canonical, err := pool.Call(ctx, "eth_getBlockByNumber", []any{r.BlockNumber, false})
	if err != nil {
		return receiptWaiting, err
	}
	var header struct {
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal(canonical, &header); err != nil {
		return receiptWaiting, err
	}
	if !strings.EqualFold(header.Hash, r.BlockHash) {
		return receiptWaiting, fmt.Errorf("receipt block is no longer canonical")
	}
	head, err := pool.MaxBlockNumber(ctx)
	if err != nil {
		return receiptWaiting, err
	}
	if required == 0 || head < block || head-block < required {
		return receiptWaiting, nil
	}
	// Depth is measured from a head that can itself be reorged, so it is not
	// finality. When the chain exposes a finality tag the mined block must also
	// be behind it; only chains that do not serve the tag keep depth alone.
	final, err := blockIsFinal(ctx, pool, chain, block)
	if err != nil {
		return receiptWaiting, err
	}
	if !final {
		return receiptWaiting, nil
	}
	if r.Status == "0x0" {
		return receiptReverted, nil
	}
	return receiptConfirmed, nil
}

// finalityTags lists the block tags that prove finality, strongest first. BSC's
// safe tag is only its justified head, so it is never used there.
func finalityTags(chain string) []string {
	if chain == "bsc" {
		return []string{"finalized"}
	}
	return []string{"finalized", "safe"}
}

// tagHeight reads one finality tag. ok is false when this node does not serve
// the tag at all (JSON null, or an explicit RPC rejection): the caller then
// keeps the configured depth rule that predates the tag check. Transport and
// malformed answers stay errors so an unreadable chain is never mistaken for a
// final one.
func tagHeight(ctx context.Context, pool *rpc.Pool, tag string) (uint64, bool, error) {
	result, err := pool.Call(ctx, "eth_getBlockByNumber", []any{tag, false})
	if err != nil {
		var failure *rpc.Failure
		// A node rejecting the tag itself (unknown tag, method not supported)
		// is "does not serve it". A rate limit is not: treating it as
		// unsupported would silently drop finality while the provider throttles.
		if errors.As(err, &failure) && failure.Kind == "remote" &&
			failure.RPCCode != 429 && failure.Reason != "rate limited" {
			return 0, false, nil
		}
		return 0, false, err
	}
	if bytes.Equal(bytes.TrimSpace(result), []byte("null")) {
		return 0, false, nil
	}
	var header struct {
		Number string `json:"number"`
	}
	if err := json.Unmarshal(result, &header); err != nil {
		return 0, false, err
	}
	number, err := strconv.ParseUint(strings.TrimPrefix(header.Number, "0x"), 16, 64)
	if err != nil {
		return 0, false, fmt.Errorf("invalid finality tag height")
	}
	return number, true, nil
}

// blockIsFinal reports whether the chain considers block final. Chains served
// by nodes that do not answer any finality tag fall back to the configured
// depth that already governed confirmation.
func blockIsFinal(ctx context.Context, pool *rpc.Pool, chain string, block uint64) (bool, error) {
	for _, tag := range finalityTags(chain) {
		height, ok, err := pool.MaxBlockTagHeight(ctx, tag)
		if err != nil {
			return false, err
		}
		if ok {
			return block <= height, nil
		}
	}
	return true, nil
}

// settle only rebroadcasts identical persisted bytes, and only after a
// successful receipt query says absent. RPC failures never authorize a new tx.
func (e *Executor) settle(ctx context.Context, pool *rpc.Pool, j *sweepJob, gas bool, required uint64) (bool, error) {
	raw, hash, nonceStr := j.SweepRaw, j.SweepHash, j.SweepNonce
	// A zero limit means "accept either the legacy or the current sweep gas
	// limit"; signed bytes from before the raise must stay verifiable.
	from, to, token, amount, limit := j.Address, j.Destination, j.Token, j.RawAmount, uint64(0)
	if gas {
		raw, hash, nonceStr = j.GasRaw, j.GasHash, j.GasNonce
		from, to, token, limit = j.GasPayer, j.Address, "native", 21000
		amount, _ = new(big.Int).SetString(j.GasAmount, 10)
	}
	nonce, err := strconv.ParseUint(nonceStr, 10, 64)
	if err == nil {
		var actual string
		actual, err = validateSigned(raw, j.Chain, from, to, token, amount, nonce, limit, nil)
		if err == nil && !strings.EqualFold(hash, actual) {
			err = fmt.Errorf("persisted transaction hash does not match signed bytes")
		}
	}
	if err != nil {
		j.Status = "manual_review"
		_, saveErr := e.jobError(ctx, j, err)
		return false, saveErr
	}
	state, err := checkReceipt(ctx, pool, j.Chain, hash, required)
	if err == nil && state == receiptAbsent {
		var returned string
		if guard, ok := e.wallet.(interface {
			GuardSweepBroadcast(context.Context, func() (string, error)) (string, error)
		}); ok {
			returned, err = guard.GuardSweepBroadcast(ctx, func() (string, error) { return pool.SendRawTransaction(ctx, raw) })
		} else {
			if guard, ok := e.wallet.(interface{ ValidateSweepAuthorization(context.Context) error }); ok {
				if guardErr := guard.ValidateSweepAuthorization(ctx); guardErr != nil {
					return false, guardErr
				}
			}
			returned, err = pool.SendRawTransaction(ctx, raw)
		}
		if rpc.IsAlreadyKnown(err) {
			// The node holds these exact bytes already: same outcome as a send.
			returned, err = hash, nil
		}
		if err == nil && !strings.EqualFold(returned, hash) {
			err = fmt.Errorf("broadcast returned a different transaction hash; outcome unknown")
		}
		if err == nil {
			state, err = checkReceipt(ctx, pool, j.Chain, hash, required)
		}
	}
	if err == nil {
		state, err = e.waitForReceipt(ctx, pool, j.Chain, hash, required, state)
	}
	if err != nil {
		j.Error = rpc.SafeError(err)
		// A timeout is persisted as an observation, never as a status change:
		// saveJob only bumps the revision here, so a transport failure cannot
		// move the job to failed by itself.
		return false, joinErrors(err, e.saveJob(ctx, j))
	}
	if state == receiptReverted {
		if shouldRetrySweepRevert(gas, j.SweepRaw) {
			// The legacy 65k limit is below what some stablecoin transfers
			// need. Re-sign once with the larger limit instead of parking the
			// job for manual review; the retried tx is distinguishable because
			// it carries the new gas limit.
			j.Status = "gas_confirmed"
			j.SweepRaw, j.SweepHash, j.SweepNonce = "", "", ""
			j.Error = "sweep reverted under the legacy gas limit; retrying with a larger limit"
			return false, e.saveJob(ctx, j)
		}
		j.Status, j.Error = "manual_review", "confirmed transaction reverted; automatic replacement/funding refused"
		return false, e.saveJob(ctx, j)
	}
	if state != receiptConfirmed {
		j.Error = "awaiting canonical receipt and required confirmations"
		return false, e.saveJob(ctx, j)
	}
	j.Error = ""
	if gas {
		j.GasConfirmed, j.Status = true, "gas_confirmed"
	} else {
		j.Status = "swept"
	}
	if err := e.saveJob(ctx, j); err != nil {
		return false, err
	}
	return true, nil
}

// gasLimitMatches validates the gas field of a signed transaction. A zero
// expected value accepts the legacy and current sweep limits (used for bytes
// signed before the limit was raised); every other caller keeps exact equality.
func gasLimitMatches(actual, expected uint64) bool {
	if expected == 0 {
		return actual == legacyTransferGasLimit || actual == transferGasLimit
	}
	return actual == expected
}

// signedGasLimit decodes the gas limit stored in persisted raw bytes.
func signedGasLimit(raw string) uint64 {
	encoded, err := hex.DecodeString(strings.TrimPrefix(raw, "0x"))
	if err != nil {
		return 0
	}
	var tx types.Transaction
	if err := tx.UnmarshalBinary(encoded); err != nil {
		return 0
	}
	return tx.Gas()
}

// shouldRetrySweepRevert reports whether a reverted token transfer was signed
// with the legacy gas limit and therefore deserves one automatic retry with the
// larger limit. Gas top-ups and retries that already used the new limit stay in
// manual review.
func shouldRetrySweepRevert(gas bool, raw string) bool {
	if gas {
		return false
	}
	limit := signedGasLimit(raw)
	return limit > 0 && limit <= legacyTransferGasLimit
}
