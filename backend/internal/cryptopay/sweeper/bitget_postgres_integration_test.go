//go:build integration

package sweeper

import (
	"encoding/json"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/bitget"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

func TestPostgresBitgetOrderPersistsBeforeSubmitAndResumesWithoutNewGasOrSignature(t *testing.T) {
	db := isolatedSweepPostgres(t)
	applySweepMigration(t, db, "267_crypto_bitget_sweeps.sql")
	applySweepMigration(t, db, "267_crypto_bitget_sweeps.sql")
	e, _, chain, entry := setupExecutor(t)
	e.db = db
	e.store = nil
	e.config.Provider = "bitget"
	e.config.BitgetMaxFee = "0.5"
	e.config.BitgetMaxFeeBPS = 100
	entry.Token = "0x55d398326f99059ff775485246999027b3197955"
	e.tokens["bsc|"+strings.ToLower(entry.Token)] = TokenSpec{Contract: entry.Token, Decimals: 18}
	entry.RawAmount = new(big.Int).Exp(big.NewInt(10), big.NewInt(19), nil)
	entry.Decimals = 18
	p := &fixtureGaslessProvider{status: bitget.OrderStatus{ID: "fixture-order", Status: "PENDING"}}
	w := &fixtureGaslessWallet{fixtureWallet: &fixtureWallet{chain: chain}}
	e.gasless = p
	e.wallet = w
	j, err := e.createBitgetJob(t.Context(), entry)
	require.NoError(t, err)
	// The existing localChain fixture has no eth_getCode route, so reserve a
	// prepared order explicitly. This exercises persisted state and recovery,
	// while quote/signature protocol tests independently cover authentication.
	o := &bitget.Order{ID: "fixture-order"}
	in := bitget.Intent{Chain: entry.Chain, ChainID: 56, Index: entry.DerivationIndex, From: entry.Address, To: j.Destination, Token: entry.Token, Gross: entry.RawAmount.String(), Net: "9950000000000000000", Fee: "50000000000000000", Decimals: 18, MaxFee: "0.5", MaxFeeBPS: 100, CreatedAt: time.Now().Unix(), Nonce: 3}
	rawI, err := json.Marshal(in)
	require.NoError(t, err)
	rawO, err := json.Marshal(o)
	require.NoError(t, err)
	_, err = db.ExecContext(t.Context(), `INSERT INTO crypto_bitget_sweep_jobs(job_id,order_id,intent,prepared) VALUES($1,$2,$3::jsonb,$4::jsonb)`, j.ID, o.ID, string(rawI), string(rawO))
	require.NoError(t, err)
	first, err := e.resumeBitget(t.Context(), j)
	require.NoError(t, err)
	require.Equal(t, "pending", first.Status)
	require.Equal(t, 1, w.signs)
	require.Equal(t, 1, p.submitted)
	require.Zero(t, p.prepared)
	require.Empty(t, chain.signs)
	require.Empty(t, chain.raws)
	jobs, err := e.loadUnfinished(t.Context())
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	require.Equal(t, 2, jobs[0].Version)
	p.status.Status = "PROCESSING"
	p.status.TxID = "getgas_task_fixture"
	_, err = e.resumeBitget(t.Context(), jobs[0])
	require.NoError(t, err)
	require.Equal(t, 1, w.signs)
	require.Equal(t, 1, p.submitted)
	// An ambiguous order cannot release its address or manufacture a second
	// order. The full address debit stays gross for future unswept accounting.
	var amount, gas, signed string
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT amount::text,gas_raw_tx FROM sweep_records WHERE id=$1`, j.ID).Scan(&amount, &gas))
	require.Equal(t, "10.000000000000000000", amount)
	require.Empty(t, gas)
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT signed_messages FROM crypto_bitget_sweep_jobs WHERE job_id=$1`, j.ID).Scan(&signed))
	require.NotEmpty(t, signed)
}

func TestPostgresBitgetConfirmedReceiptClosesGrossLedgerAndCannotReexecute(t *testing.T) {
	db := isolatedSweepPostgres(t)
	applySweepMigration(t, db, "267_crypto_bitget_sweeps.sql")
	e, _, chain, entry := setupExecutor(t)
	e.db = db
	e.store = nil
	e.config.Provider = "bitget"
	entry.Token = "0x55d398326f99059ff775485246999027b3197955"
	entry.Decimals = 18
	entry.RawAmount = new(big.Int).Exp(big.NewInt(10), big.NewInt(19), nil)
	j, err := e.createBitgetJob(t.Context(), entry)
	require.NoError(t, err)
	in := bitget.Intent{Chain: "bsc", ChainID: 56, Index: entry.DerivationIndex, From: entry.Address, To: j.Destination, Token: entry.Token, Decimals: 18, Gross: entry.RawAmount.String(), Net: "9950000000000000000", Fee: "50000000000000000", MaxFee: "0.5", MaxFeeBPS: 100, CreatedAt: time.Now().Unix(), CurrentDelegate: "0x3333333333333333333333333333333333333333", CurrentCodeHash: crypto.Keccak256Hash([]byte{0x60, 0x00}).Hex()}
	hash := "0x" + strings.Repeat("cd", 32)
	p := &fixtureGaslessProvider{status: bitget.OrderStatus{ID: "fixture-final", Status: "SUCCESS", Hash: hash}}
	w := &fixtureGaslessWallet{fixtureWallet: &fixtureWallet{chain: chain}}
	e.gasless = p
	e.wallet = w
	e.pools["bsc"] = bitgetReceiptPool(t, in, hash, 103, testBlockHash, nil)
	rawI, err := json.Marshal(in)
	require.NoError(t, err)
	rawO, err := json.Marshal(&bitget.Order{ID: p.status.ID})
	require.NoError(t, err)
	_, err = db.ExecContext(t.Context(), `INSERT INTO crypto_bitget_sweep_jobs(job_id,order_id,intent,prepared,signed_messages,authorization_mac) VALUES($1,$2,$3::jsonb,$4::jsonb,$5,'fixture')`, j.ID, p.status.ID, string(rawI), string(rawO), `[{"msgType":"call","hash":"0x00","sig":"fixture"}]`)
	require.NoError(t, err)
	// A stricter current policy must not be bypassed by the older stored depth.
	e.config.Confirmations["bsc"] = 4
	out, err := e.resumeBitget(t.Context(), j)
	require.NoError(t, err)
	require.Equal(t, "pending", out.Status)
	require.EqualValues(t, 4, j.Confirmations)
	require.Zero(t, w.delegates)
	e.pools["bsc"] = bitgetReceiptPool(t, in, hash, 105, testBlockHash, nil)
	out, err = e.resumeBitget(t.Context(), j)
	require.NoError(t, err)
	require.Equal(t, "swept", out.Status)
	require.Equal(t, 1, w.delegates)
	var amount, gas, status string
	var depth uint64
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT amount::text,gas_raw_tx,status,confirmations_required FROM sweep_records WHERE id=$1`, j.ID).Scan(&amount, &gas, &status, &depth))
	require.Equal(t, "10.000000000000000000", amount)
	require.Equal(t, "swept", status)
	require.Empty(t, gas)
	require.EqualValues(t, 4, depth)
	jobs, err := e.loadUnfinished(t.Context())
	require.NoError(t, err)
	require.Empty(t, jobs)
	_, err = e.resumeBitget(t.Context(), j)
	require.NoError(t, err)
	require.Zero(t, p.submitted)
	require.Zero(t, p.prepared)
	require.Zero(t, w.signs)
	require.Empty(t, chain.raws)
}
