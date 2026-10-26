package sweeper

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/bitget"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

// AuthorizeGaslessObserved closes the race between the deposit security scan
// and the sweep caller polling its order. It never signs or submits; only an
// already signed durable order with this vendor-reported hash can be bound.
// Unavailable order evidence stops the scan, just as unavailable RPC evidence
// does, rather than misclassifying a legitimate relayed fee as theft.
func (e *Executor) AuthorizeGaslessObserved(ctx context.Context, pool *rpc.Pool, chain, hash, from string) (bool, error) {
	if e.config.Provider != bitget.Provider {
		return false, nil
	}
	w, ok := e.wallet.(gaslessWallet)
	if !ok {
		return false, nil
	}
	rows, err := e.db.QueryContext(ctx, `SELECT b.job_id,b.order_id,b.intent,b.bound_tx_hash
	 FROM crypto_bitget_sweep_jobs b JOIN sweep_records s ON s.id=b.job_id
	 WHERE s.chain=$1 AND lower(s.from_address)=lower($2) AND s.job_version=2
	 AND b.signed_messages<>'' AND (b.bound_tx_hash='' OR lower(b.bound_tx_hash)=lower($3))
	 ORDER BY b.job_id DESC LIMIT 5`, chain, from, hash)
	if err != nil {
		return false, err
	}
	type pending struct {
		id    int64
		order string
		in    bitget.Intent
		bound string
	}
	var jobs []pending
	for rows.Next() {
		var p pending
		var raw []byte
		if err = rows.Scan(&p.id, &p.order, &raw, &p.bound); err != nil {
			_ = rows.Close()
			return false, err
		}
		if json.Unmarshal(raw, &p.in) != nil {
			_ = rows.Close()
			return false, bitget.ErrUnsafeOrder
		}
		jobs = append(jobs, p)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return false, err
	}
	for _, j := range jobs {
		if j.bound != "" && strings.EqualFold(j.bound, hash) {
			return true, nil
		}
		status, e2 := e.bitgetProvider().Status(ctx, j.order)
		if e2 != nil {
			return false, e2
		}
		candidate := status.Hash
		if candidate == "" {
			candidate = status.TxID
		}
		if !bitget.Hash(candidate) {
			return false, bitget.ErrUnavailable
		}
		if !strings.EqualFold(candidate, hash) {
			continue
		}
		collector, e2 := e.verifyBitgetReceipt(ctx, pool, j.in, hash, 0, false)
		if e2 != nil {
			return false, e2
		}
		if e2 = w.BindBitgetTransaction(ctx, j.id, hash, collector); e2 != nil {
			return false, e2
		}
		return true, nil
	}
	return false, nil
}
