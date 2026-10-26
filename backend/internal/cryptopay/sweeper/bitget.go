package sweeper

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/bitget"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/wallet"
	"github.com/ethereum/go-ethereum/common"
)

type gaslessProvider interface {
	Prepare(context.Context, bitget.Request) (*bitget.Order, error)
	Submit(context.Context, string, string) error
	Status(context.Context, string) (*bitget.OrderStatus, error)
	ValidateSigning(*bitget.Order, bitget.Intent, time.Time) (*bitget.Validated, error)
}
type gaslessWallet interface {
	SignBitgetOrder(context.Context, int64, *bitget.Order, bitget.Intent) (*wallet.BitgetSignedOrder, error)
	BindBitgetTransaction(context.Context, int64, string, string) error
	GuardSweepBroadcast(context.Context, func() (string, error)) (string, error)
}

// EnrichGaslessPlan performs unsigned provider quotes only. Unavailable quotes
// remain visible failures; dry-run never invokes the wallet signer or submit.
func (e *Executor) EnrichGaslessPlan(ctx context.Context, plan *Plan) {
	if plan == nil || e.config.Provider != bitget.Provider {
		return
	}
	for i := range plan.Entries {
		entry := &plan.Entries[i]
		entry.Gasless = true
		entry.NeedsGasTopUp = false
		entry.GasTopUpAmount = nil
		_, in, err := e.quoteBitget(ctx, *entry)
		if err != nil {
			entry.QuoteError = rpc.SafeError(err)
			continue
		}
		net, _ := bitget.Uint(in.Net)
		fee, _ := bitget.Uint(in.Fee)
		entry.NetAmount = amountString(net, in.Decimals)
		entry.FeeAmount = amountString(fee, in.Decimals)
	}
}

func (e *Executor) bitgetProvider() gaslessProvider {
	if e.gasless != nil {
		return e.gasless
	}
	return bitget.NewClient()
}

func (e *Executor) quoteBitget(ctx context.Context, entry PlanEntry) (*bitget.Order, *bitget.Intent, error) {
	chain, id := bitget.Chain(entry.Chain)
	if id == 0 {
		return nil, nil, bitget.ErrUnsupportedChain
	}
	in := &bitget.Intent{Chain: entry.Chain, ChainID: id, Index: entry.DerivationIndex, From: entry.Address, To: e.config.HotWalletEVM,
		Token: entry.Token, Decimals: entry.Decimals, Gross: entry.RawAmount.String(), MaxFee: e.config.BitgetMaxFee, MaxFeeBPS: e.config.BitgetMaxFeeBPS}
	net := new(big.Int).Set(entry.RawAmount)
	// The fee is quoted in the same token. Request the final net transfer,
	// re-quote after subtracting its fee, and require an exact fixed point.
	for attempt := 0; attempt < 4; attempt++ {
		req := bitget.Request{Chain: chain, From: in.From, To: in.To, Contract: in.Token, Amount: amountString(net, in.Decimals), NoGas: true, PayToken: in.Token}
		o, err := e.bitgetProvider().Prepare(ctx, req)
		if err != nil {
			return nil, nil, err
		}
		fee, err := bitget.RawAmount(o.NoGas.PayAmount, in.Decimals)
		if err != nil {
			return nil, nil, err
		}
		want := new(big.Int).Sub(entry.RawAmount, fee)
		if want.Sign() <= 0 {
			return nil, nil, bitget.ErrUnsafeOrder
		}
		in.Net, in.Fee = net.String(), fee.String()
		if want.Cmp(net) == 0 {
			if err = bitget.ValidateQuote(o, *in); err != nil {
				return nil, nil, err
			}
			return o, in, nil
		}
		net = want
	}
	return nil, nil, bitget.ErrUnsafeOrder
}

func (e *Executor) createBitgetJob(ctx context.Context, entry PlanEntry) (*sweepJob, error) {
	j := &sweepJob{PlanEntry: entry, Version: 2, Destination: e.config.HotWalletEVM, Status: "planned", Confirmations: e.config.Confirmations[entry.Chain]}
	err := e.queries().QueryRowContext(ctx, `INSERT INTO sweep_records
	 (chain,from_address,to_address,token,amount,raw_amount,token_decimals,derivation_index,status,job_version,confirmations_required)
	 SELECT $1,$2,$3,$4,$5,$6,$7,$8,'planned',2,$9 WHERE NOT EXISTS
	 (SELECT 1 FROM sweep_records WHERE chain=$1 AND lower(from_address)=lower($2) AND (`+unfinishedPredicate+`)) RETURNING id`,
		entry.Chain, entry.Address, j.Destination, entry.Token, amountString(entry.RawAmount, entry.Decimals), entry.RawAmount.String(), entry.Decimals, int64(entry.DerivationIndex), strconv.FormatUint(j.Confirmations, 10)).Scan(&j.ID)
	if err != nil {
		return nil, &persistenceError{err}
	}
	return j, nil
}

func (e *Executor) saveBitgetState(ctx context.Context, j *sweepJob) error {
	result, err := e.queries().ExecContext(ctx, `UPDATE sweep_records SET status=$2,error=$3,sweep_tx_hash=$4,revision=revision+1,updated_at=NOW() WHERE id=$1 AND job_version=2 AND revision=$5`, j.ID, j.Status, j.Error, j.SweepHash, j.Revision)
	if err == nil {
		var n int64
		n, err = result.RowsAffected()
		if err == nil && n != 1 {
			err = bitget.ErrUnsafeOrder
		}
	}
	if err != nil {
		return &persistenceError{err}
	}
	j.Revision++
	return nil
}

func (e *Executor) resumeBitget(ctx context.Context, j *sweepJob) (SweepEntry, error) {
	w, ok := e.wallet.(gaslessWallet)
	if !ok {
		return e.bitgetError(ctx, j, bitget.ErrUnsafeOrder)
	}
	pool := e.pools[j.Chain]
	if pool == nil {
		return e.bitgetError(ctx, j, bitget.ErrUnavailable)
	}
	_, chainID := bitget.Chain(j.Chain)
	if chainID == 0 {
		return e.bitgetError(ctx, j, bitget.ErrUnsupportedChain)
	}
	pool, err := pool.ForChain(ctx, uint64(chainID))
	if err != nil {
		return e.bitgetError(ctx, j, err)
	}
	var id, signed, authorization string
	var rawIntent, rawOrder []byte
	err = e.queries().QueryRowContext(ctx, `SELECT order_id,intent,prepared,signed_messages,authorization FROM crypto_bitget_sweep_jobs WHERE job_id=$1`, j.ID).Scan(&id, &rawIntent, &rawOrder, &signed, &authorization)
	var in bitget.Intent
	var order bitget.Order
	if errors.Is(err, sql.ErrNoRows) {
		if e.config.Provider != bitget.Provider {
			return e.bitgetError(ctx, j, bitget.ErrUnsafeOrder)
		}
		if _, _, err = e.validateEntry(j.PlanEntry); err != nil {
			return e.bitgetError(ctx, j, err)
		}
		live, e2 := e.tokenBalance(ctx, pool, j.Token, j.Address)
		if e2 != nil || live.Cmp(j.RawAmount) < 0 {
			return e.bitgetError(ctx, j, bitget.ErrUnsafeOrder)
		}
		o, intent, e2 := e.quoteBitget(ctx, j.PlanEntry)
		if e2 != nil {
			return e.bitgetError(ctx, j, e2)
		}
		intent.Nonce, e2 = pool.Nonce(ctx, j.Address)
		if e2 != nil {
			return e.bitgetError(ctx, j, e2)
		}
		intent.CurrentDelegate, e2 = currentDelegate(ctx, pool, j.Address)
		if e2 != nil {
			return e.bitgetError(ctx, j, e2)
		}
		// Unknown existing code is never replaced. A previously approved Bitget
		// delegation is accepted only from our local authenticated history.
		if intent.CurrentDelegate != "" {
			if guard, ok := e.wallet.(interface {
				CheckBitgetDelegate(context.Context, bitget.Intent) error
			}); !ok || guard.CheckBitgetDelegate(ctx, *intent) != nil {
				return e.bitgetError(ctx, j, bitget.ErrUnsafeOrder)
			}
		}
		if _, e2 = e.bitgetProvider().ValidateSigning(o, *intent, time.Now()); e2 != nil {
			return e.bitgetError(ctx, j, e2)
		}
		in, order = *intent, *o
		id = o.ID
		rawIntent, _ = json.Marshal(in)
		rawOrder, _ = json.Marshal(order)
		if _, err = e.queries().ExecContext(ctx, `INSERT INTO crypto_bitget_sweep_jobs(job_id,order_id,intent,prepared) VALUES($1,$2,$3::jsonb,$4::jsonb)`, j.ID, id, string(rawIntent), string(rawOrder)); err != nil {
			return pendingError(j, &persistenceError{err})
		}
	} else if err != nil {
		return pendingError(j, &persistenceError{err})
	} else if json.Unmarshal(rawIntent, &in) != nil || json.Unmarshal(rawOrder, &order) != nil || in.Gross != j.RawAmount.String() || in.Index != j.DerivationIndex || in.Chain != j.Chain || !strings.EqualFold(in.From, j.Address) || !strings.EqualFold(in.To, j.Destination) || !strings.EqualFold(in.Token, j.Token) || id != order.ID {
		return e.bitgetError(ctx, j, bitget.ErrUnsafeOrder)
	}
	if signed == "" {
		result, e2 := w.SignBitgetOrder(ctx, j.ID, &order, in)
		if e2 != nil {
			return e.bitgetError(ctx, j, e2)
		}
		signed, authorization = result.Messages, result.Authorization
		resultDB, e2 := e.queries().ExecContext(ctx, `UPDATE crypto_bitget_sweep_jobs SET signed_messages=$2,authorization=$3,updated_at=NOW() WHERE job_id=$1 AND signed_messages=''`, j.ID, signed, authorization)
		if e2 != nil {
			return pendingError(j, &persistenceError{e2})
		}
		n, e2 := resultDB.RowsAffected()
		if e2 != nil || n != 1 {
			return pendingError(j, &persistenceError{bitget.ErrUnsafeOrder})
		}
	}
	// Provider status, including timeouts, is not authority to create another
	// order. Any retry submits only this durable signature to the same order.
	state, err := e.bitgetProvider().Status(ctx, id)
	if err != nil {
		return e.bitgetError(ctx, j, err)
	}
	hash := state.Hash
	if hash == "" {
		hash = state.TxID
	}
	if hash != "" && bitget.Hash(hash) {
		collector, e2 := e.verifyBitgetReceipt(ctx, pool, in, hash, j.Confirmations, false)
		if e2 == nil {
			if e2 = w.BindBitgetTransaction(ctx, j.ID, hash, collector); e2 != nil {
				return e.bitgetError(ctx, j, e2)
			}
			delegate, e3 := currentDelegate(ctx, pool, j.Address)
			if e3 != nil {
				return e.bitgetError(ctx, j, e3)
			}
			if recorder, ok := e.wallet.(interface {
				RecordBitgetDelegate(context.Context, int64, string, string) error
			}); ok && delegate != "" {
				if e3 = recorder.RecordBitgetDelegate(ctx, j.ID, hash, delegate); e3 != nil {
					return e.bitgetError(ctx, j, e3)
				}
			}
			j.SweepHash = strings.ToLower(hash)
			_, e2 = e.verifyBitgetReceipt(ctx, pool, in, hash, j.Confirmations, true)
			if e2 == nil {
				j.Status = "swept"
				j.Error = ""
			} else {
				j.Status = "sweep_pending"
				j.Error = "awaiting canonical receipt and required confirmations"
			}
			if saveErr := e.saveBitgetState(ctx, j); saveErr != nil {
				return pendingError(j, saveErr)
			}
			return j.outcome(), nil
		}
		return e.bitgetError(ctx, j, e2)
	}
	if state.Status == "FAILED" {
		j.Status = "manual_review"
		return e.bitgetError(ctx, j, bitget.ErrUnavailable)
	}
	if state.Status == "PENDING" {
		_, err = w.GuardSweepBroadcast(ctx, func() (string, error) { return "", e.bitgetProvider().Submit(ctx, id, signed) })
		if err != nil {
			var remote *bitget.Error
			if !errors.As(err, &remote) || remote.Code != 30106 {
				return e.bitgetError(ctx, j, err)
			}
		}
	}
	j.Status = "sweep_pending"
	j.Error = "awaiting Bitget order and on-chain confirmation"
	if err = e.saveBitgetState(ctx, j); err != nil {
		return pendingError(j, err)
	}
	if !e.confirmationDeadline.IsZero() && time.Now().Before(e.confirmationDeadline) {
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return pendingError(j, ctx.Err())
		case <-timer.C:
			return e.resumeBitget(ctx, j)
		}
	}
	return j.outcome(), nil
}

func (e *Executor) bitgetError(ctx context.Context, j *sweepJob, err error) (SweepEntry, error) {
	j.Error = rpc.SafeError(err)
	save := e.saveBitgetState(ctx, j)
	return pendingError(j, errors.Join(err, save))
}

func currentDelegate(ctx context.Context, pool *rpc.Pool, address string) (string, error) {
	raw, err := pool.Call(ctx, "eth_getCode", []any{address, "latest"})
	if err != nil {
		return "", err
	}
	var code string
	if json.Unmarshal(raw, &code) != nil {
		return "", bitget.ErrUnsafeOrder
	}
	if code == "0x" || code == "0x0" {
		return "", nil
	}
	if len(code) != 48 || !strings.HasPrefix(code, "0xef0100") || !bitget.ValidAddress("0x"+code[8:]) {
		return "", bitget.ErrUnsafeOrder
	}
	return "0x" + code[8:], nil
}

func (e *Executor) verifyBitgetReceipt(ctx context.Context, pool *rpc.Pool, in bitget.Intent, hash string, depth uint64, final bool) (string, error) {
	raw, err := pool.Call(ctx, "eth_getTransactionReceipt", []any{hash})
	if err != nil {
		return "", err
	}
	var receipt struct {
		Hash   string `json:"transactionHash"`
		Status string `json:"status"`
		Logs   []struct {
			Address string   `json:"address"`
			Topics  []string `json:"topics"`
			Data    string   `json:"data"`
		} `json:"logs"`
	}
	if json.Unmarshal(raw, &receipt) != nil || !strings.EqualFold(receipt.Hash, hash) || receipt.Status != "0x1" {
		return "", bitget.ErrUnavailable
	}
	net, _ := bitget.Uint(in.Net)
	fee, _ := bitget.Uint(in.Fee)
	gotNet, gotFee := false, fee.Sign() == 0
	collector := ""
	for _, log := range receipt.Logs {
		if len(log.Topics) != 3 || !strings.EqualFold(log.Topics[0], "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef") || !strings.EqualFold(common.HexToAddress(log.Topics[1]).Hex(), in.From) {
			continue
		}
		if !strings.EqualFold(log.Address, in.Token) {
			return "", bitget.ErrUnsafeOrder
		}
		amount, ok := new(big.Int).SetString(strings.TrimPrefix(log.Data, "0x"), 16)
		if !ok {
			return "", bitget.ErrUnsafeOrder
		}
		to := common.HexToAddress(log.Topics[2]).Hex()
		if !gotNet && strings.EqualFold(to, in.To) && amount.Cmp(net) == 0 {
			gotNet = true
			continue
		}
		if !gotFee && !strings.EqualFold(to, in.To) && !strings.EqualFold(to, in.From) && bitget.ValidAddress(to) && amount.Cmp(fee) == 0 {
			collector = to
			gotFee = true
			continue
		}
		return "", bitget.ErrUnsafeOrder
	}
	if !gotNet || !gotFee {
		return "", bitget.ErrUnsafeOrder
	}
	if final {
		state, e2 := checkReceipt(ctx, pool, hash, depth)
		if e2 != nil {
			return "", e2
		}
		if state != receiptConfirmed {
			return "", bitget.ErrUnavailable
		}
	}
	return collector, nil
}
