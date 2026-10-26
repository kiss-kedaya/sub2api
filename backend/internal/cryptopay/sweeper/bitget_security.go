package sweeper

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/bitget"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

// AuthorizeGaslessObserved closes the race between the deposit security scan
// and the sweep caller polling its order. It never signs or submits; only an
// already signed durable order with this vendor-reported hash can be bound.
// Unavailable order evidence stops the scan, just as unavailable RPC evidence
// does, rather than misclassifying a legitimate relayed fee as theft.
func (e *Executor) AuthorizeGaslessObserved(ctx context.Context, pool *rpc.Pool, chain, hash, from string) (bool, error) {
	w, ok := e.wallet.(gaslessWallet)
	if !ok {
		return false, nil
	}
	rows, err := e.db.QueryContext(ctx, `SELECT b.job_id,b.order_id,b.intent,b.bound_tx_hash,b.signed_messages
	 FROM crypto_bitget_sweep_jobs b JOIN sweep_records s ON s.id=b.job_id
	 WHERE s.chain=$1 AND lower(s.from_address)=lower($2) AND s.job_version=2
	 AND b.signed_messages<>'' AND (b.bound_tx_hash='' OR lower(b.bound_tx_hash)=lower($3))
	 ORDER BY b.job_id DESC LIMIT 5`, chain, from, hash)
	if err != nil {
		return false, err
	}
	type pending struct {
		id     int64
		order  string
		in     bitget.Intent
		bound  string
		signed string
	}
	var jobs []pending
	for rows.Next() {
		var p pending
		var raw []byte
		if err = rows.Scan(&p.id, &p.order, &raw, &p.bound, &p.signed); err != nil {
			_ = rows.Close()
			return false, err
		}
		if json.Unmarshal(raw, &p.in) != nil {
			_ = rows.Close()
			return false, nil
		}
		jobs = append(jobs, p)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return false, err
	}
	var waiting bool
	for _, j := range jobs {
		if err = w.VerifyBitgetRecord(ctx, j.id, j.order, j.in, j.signed); err != nil {
			if errors.Is(err, bitget.ErrUnsafeOrder) {
				return false, nil
			}
			return false, err
		}
		// Even an unknown vendor task cannot indefinitely suppress a confirmed
		// unapproved outflow. After the short propagation window the existing
		// security incident path locks the wallet and alerts the operator.
		fresh := j.in.CreatedAt > 0 && time.Now().Unix() >= j.in.CreatedAt-30 && time.Now().Unix() < j.in.CreatedAt+300
		if j.bound != "" && strings.EqualFold(j.bound, hash) {
			return true, nil
		}
		status, e2 := e.bitgetProvider().Status(ctx, j.order)
		if e2 != nil {
			waiting = waiting || fresh
			continue
		}
		if status == nil || status.ID != j.order || (status.Status != "PENDING" && status.Status != "PROCESSING" && status.Status != "SUCCESS" && status.Status != "FAILED") {
			waiting = waiting || fresh
			continue
		}
		candidate := status.Hash
		if candidate == "" {
			candidate = status.TxID
		}
		if !bitget.Hash(candidate) {
			waiting = waiting || (fresh && (status.Status == "PENDING" || status.Status == "PROCESSING"))
			continue
		}
		if !strings.EqualFold(candidate, hash) {
			continue
		}
		if status.Status != "SUCCESS" {
			waiting = waiting || fresh
			continue
		}
		collector, e2 := e.verifyBitgetReceipt(ctx, pool, j.in, hash, 0, false)
		if e2 != nil {
			if errors.Is(e2, bitget.ErrUnsafeOrder) {
				return false, nil
			}
			return false, e2
		}
		if e2 = w.BindBitgetTransaction(ctx, j.id, hash, collector, j.in); e2 != nil {
			if errors.Is(e2, bitget.ErrUnsafeOrder) {
				return false, nil
			}
			return false, e2
		}
		return true, nil
	}
	if waiting {
		return false, bitget.ErrUnavailable
	}
	return false, nil
}
