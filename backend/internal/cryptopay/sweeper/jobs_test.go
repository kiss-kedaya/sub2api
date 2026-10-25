package sweeper

import (
	"context"
	"crypto/ecdsa"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

const testToken = "0x1111111111111111111111111111111111111111"
const testDestination = "0x2222222222222222222222222222222222222222"
const testBlockHash = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type fixtureWallet struct{ chain *localChain }

func (w *fixtureWallet) HotAddresses(ctx context.Context) (*HotAddress, error) {
	return &HotAddress{Index: 9, EVM: w.chain.payer}, nil
}
func (w *fixtureWallet) SignEVM(ctx context.Context, req EVMSignRequest) (string, error) {
	c := w.chain
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.beforeSign != nil {
		c.beforeSign()
	}
	c.signs = append(c.signs, req)
	return c.signed(req), nil
}

// Every RPC and signing request goes through this local HTTP server. Keys
// are deterministic test fixtures, never a vault or a real-chain account.
type localChain struct {
	mu           sync.Mutex
	child, hot   *ecdsa.PrivateKey
	address      string
	payer        string
	live         int64
	native       int64
	mined        bool
	head         uint64
	status       string
	canonical    string
	sendUnknown  bool
	receiptError bool
	wrongAmount  bool
	txs          map[string]bool
	blocks       map[string]uint64
	raws         []string
	signs        []EVMSignRequest
	nonceReads   []string
	receiptReads int
	chainID      string
	gasPrice     string
	beforeSign   func()
	beforeSend   func()
}

func (c *localChain) signed(req EVMSignRequest) string {
	key := c.child
	to, value := common.HexToAddress(req.To), new(big.Int)
	amount, _ := new(big.Int).SetString(req.Amount, 10)
	var data []byte
	if req.Kind == "gas" {
		key, value = c.hot, amount
	} else {
		to = common.HexToAddress(req.Token)
		data = common.FromHex("0xa9059cbb")
		data = append(data, common.LeftPadBytes(common.HexToAddress(req.To).Bytes(), 32)...)
		if c.wrongAmount {
			amount.Add(amount, big.NewInt(1))
		}
		data = append(data, common.LeftPadBytes(amount.Bytes(), 32)...)
	}
	price, _ := new(big.Int).SetString(req.GasPrice, 10)
	tx := types.NewTx(&types.DynamicFeeTx{ChainID: big.NewInt(req.ChainID), Nonce: req.Nonce,
		GasTipCap: price, GasFeeCap: price, Gas: req.GasLimit, To: &to, Value: value, Data: data})
	signed, _ := types.SignTx(tx, types.LatestSignerForChainID(big.NewInt(req.ChainID)), key)
	raw, _ := signed.MarshalBinary()
	return "0x" + hex.EncodeToString(raw)
}

func rawHash(raw string) string {
	var tx types.Transaction
	_ = tx.UnmarshalBinary(common.FromHex(raw))
	return tx.Hash().Hex()
}

func (c *localChain) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/hot-address" {
		_ = json.NewEncoder(w).Encode(HotAddress{Index: 9, EVM: c.payer})
		return
	}
	if r.URL.Path == "/sign/evm" {
		var req EVMSignRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.beforeSign != nil {
			c.beforeSign()
		}
		c.signs = append(c.signs, req)
		_ = json.NewEncoder(w).Encode(map[string]string{"signed_tx": c.signed(req)})
		return
	}
	var req struct {
		Method string            `json:"method"`
		Params []json.RawMessage `json:"params"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	c.mu.Lock()
	defer c.mu.Unlock()
	var result any
	param := func(i int) string { var v string; _ = json.Unmarshal(req.Params[i], &v); return v }
	switch req.Method {
	case "eth_chainId":
		result = "0x1"
		if c.chainID != "" {
			result = c.chainID
		}
	case "eth_call":
		result = fmt.Sprintf("0x%x", c.live)
	case "eth_gasPrice":
		result = "0x1"
		if c.gasPrice != "" {
			result = c.gasPrice
		}
	case "eth_getBalance":
		result = fmt.Sprintf("0x%x", c.native)
	case "eth_getTransactionCount":
		address := param(0)
		c.nonceReads = append(c.nonceReads, address)
		result = "0x3"
		if strings.EqualFold(address, c.payer) {
			result = "0x7"
		}
	case "eth_sendRawTransaction":
		if c.beforeSend != nil {
			c.beforeSend()
		}
		raw := param(0)
		hash := rawHash(raw)
		c.raws = append(c.raws, raw)
		c.txs[hash] = true
		var tx types.Transaction
		_ = tx.UnmarshalBinary(common.FromHex(raw))
		if tx.Value().Sign() > 0 && c.mined {
			c.native = 1_000_000_000
		}
		if c.sendUnknown {
			w.WriteHeader(http.StatusGatewayTimeout) // accepted; response lost
			return
		}
		result = hash
	case "eth_getTransactionReceipt":
		c.receiptReads++
		if c.receiptError {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		hash := param(0)
		if c.mined && c.txs[hash] {
			block := uint64(100)
			if c.blocks[hash] != 0 {
				block = c.blocks[hash]
			}
			result = map[string]any{"transactionHash": hash, "status": c.status,
				"blockNumber": fmt.Sprintf("0x%x", block), "blockHash": testBlockHash}
		}
	case "eth_getBlockByNumber":
		result = map[string]string{"hash": c.canonical, "number": param(0)}
	case "eth_blockNumber":
		result = fmt.Sprintf("0x%x", c.head)
	default:
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": result})
}

func setupExecutor(t *testing.T) (*Executor, sqlmock.Sqlmock, *localChain, PlanEntry) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
		_ = db.Close()
	})
	child, _ := crypto.HexToECDSA(strings.Repeat("0", 63) + "1")
	hot, _ := crypto.HexToECDSA(strings.Repeat("0", 63) + "2")
	c := &localChain{child: child, hot: hot, address: crypto.PubkeyToAddress(child.PublicKey).Hex(),
		payer: crypto.PubkeyToAddress(hot.PublicKey).Hex(), live: 15_000_000, native: 1_000_000_000,
		mined: true, head: 103, status: "0x1", canonical: testBlockHash, txs: map[string]bool{}, chainID: "0x38"}
	server := httptest.NewServer(http.HandlerFunc(c.serve))
	t.Cleanup(server.Close)
	wallet := &fixtureWallet{chain: c}
	pool := rpc.NewPoolWithHTTPClient(server.URL+"/rpc", server.Client())
	config := Config{HotWalletEVM: testDestination, Confirmations: map[string]uint64{"bsc": 2},
		GasTopUp: map[string]float64{"bsc": 0.002}}
	entry := PlanEntry{Chain: "bsc", Address: c.address, DerivationIndex: 42,
		Token: testToken, RawAmount: big.NewInt(10_000_000), Decimals: 6}
	e := NewExecutor(db, map[string]*rpc.Pool{"bsc": pool}, wallet, config,
		map[string]TokenSpec{"bsc|" + testToken: {Contract: testToken, Decimals: 6}},
		&fakeReader{}, 123) // deliberately different from wallet's real hot index
	return e, mock, c, entry
}

type captureArg struct{ match func(driver.Value) bool }

func (a captureArg) Match(v driver.Value) bool { return a.match(v) }

func expectCreate(m sqlmock.Sqlmock, entry PlanEntry, err error, persisted *atomic.Bool) {
	first := driver.Value(entry.Chain)
	if persisted != nil {
		first = captureArg{func(v driver.Value) bool { persisted.Store(true); return v == entry.Chain }}
	}
	q := m.ExpectQuery("INSERT INTO sweep_records").WithArgs(first, entry.Address, testDestination,
		entry.Token, "10", "10000000", 6, int64(42), "2")
	if err != nil {
		q.WillReturnError(err)
	} else {
		q.WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	}
}

func expectAddressClear(m sqlmock.Sqlmock, entry PlanEntry) {
	m.ExpectQuery(`SELECT EXISTS .*lower\(from_address\)`).WithArgs(entry.Chain, entry.Address, int64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
}

func expectWrite(m sqlmock.Sqlmock, status string, err error, durable *atomic.Bool) {
	expectWriteID(m, 1, status, err, durable)
}

func expectWriteID(m sqlmock.Sqlmock, id int64, status string, err error, durable *atomic.Bool) {
	args := []driver.Value{id, status, sqlmock.AnyArg(), "2"}
	for i := 4; i < 14; i++ {
		args = append(args, sqlmock.AnyArg())
	}
	if durable != nil {
		index := 11 // sweep_raw_tx
		if status == "gas_pending" {
			index = 7
		}
		args[index] = captureArg{func(v driver.Value) bool {
			raw, ok := v.(string)
			if ok && strings.HasPrefix(raw, "0x02") {
				durable.Store(true)
				return true
			}
			return false
		}}
	}
	x := m.ExpectExec("UPDATE sweep_records SET").WithArgs(args...)
	if err != nil {
		x.WillReturnError(err)
	} else {
		x.WillReturnResult(sqlmock.NewResult(0, 1))
	}
}

func signedJob(c *localChain, entry PlanEntry, gas bool) *sweepJob {
	j := &sweepJob{PlanEntry: entry, ID: 1, Version: 1, Revision: 1,
		Destination: testDestination, Status: "sweep_pending", Confirmations: 2}
	req := EVMSignRequest{Index: 42, Kind: "sweep", ChainID: 56, Token: entry.Token,
		To: testDestination, Amount: "10000000", Nonce: 3, GasPrice: "1", GasLimit: transferGasLimit}
	if gas {
		req.Kind, req.Token, req.To, req.Amount, req.Nonce, req.GasLimit = "gas", "native", entry.Address, "195000", 7, 21000
		j.GasPayer, j.GasNonce, j.GasAmount = c.payer, "7", req.Amount
		j.GasRaw = c.signed(req)
		j.GasHash, j.Status = rawHash(j.GasRaw), "gas_pending"
	} else {
		j.SweepNonce, j.SweepRaw = "3", c.signed(req)
		j.SweepHash = rawHash(j.SweepRaw)
	}
	return j
}

func jobRows(jobs ...*sweepJob) *sqlmock.Rows {
	columns := strings.Split(strings.ReplaceAll(strings.ReplaceAll(jobColumns, "\n", ""), "\t", ""), ",")
	for i := range columns {
		columns[i] = strings.TrimSpace(columns[i])
	}
	rows := sqlmock.NewRows(columns)
	for _, j := range jobs {
		raw := ""
		if j.RawAmount != nil {
			raw = j.RawAmount.String()
		}
		rows.AddRow(j.ID, j.Chain, j.Address, j.Destination, j.Token, raw, j.Decimals,
			int64(j.DerivationIndex), j.Status, j.Error, j.Version, j.Revision,
			strconv.FormatUint(j.Confirmations, 10), j.GasPayer, j.GasAmount, j.GasNonce,
			j.GasRaw, j.GasHash, j.GasConfirmed, j.SweepNonce, j.SweepRaw, j.SweepHash)
	}
	return rows
}

func expectRunStart(m sqlmock.Sqlmock, jobs ...*sweepJob) {
	m.ExpectQuery(`SELECT pg_try_advisory_lock`).WithArgs(sweepLockKey).
		WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(true))
	m.ExpectQuery("SELECT id, chain, from_address").WillReturnRows(jobRows(jobs...))
}

func expectUnlock(m sqlmock.Sqlmock) {
	m.ExpectQuery(`SELECT pg_advisory_unlock`).WithArgs(sweepLockKey).
		WillReturnRows(sqlmock.NewRows([]string{"unlocked"}).AddRow(true))
}

func TestSweepTransfersOnlyPlanAndPersistsBeforeBroadcast(t *testing.T) {
	e, m, c, entry := setupExecutor(t)
	var planned, durable atomic.Bool
	c.beforeSign = func() {
		if !planned.Load() {
			t.Error("signed before planned job was persisted")
		}
	}
	c.beforeSend = func() {
		if !durable.Load() {
			t.Error("broadcast before raw transaction was persisted")
		}
	}
	expectCreate(m, entry, nil, &planned)
	expectAddressClear(m, entry)
	expectWrite(m, "sweep_pending", nil, &durable)
	expectWrite(m, "swept", nil, nil)
	out, err := e.sweepOne(t.Context(), entry, false)
	if err != nil || out.Status != "swept" || out.Amount != "10000000" {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	if len(c.signs) != 1 || c.signs[0].Amount != "10000000" || len(c.raws) != 1 {
		t.Fatalf("uncredited excess was signed or repeated: %+v", c.signs)
	}
	var tx types.Transaction
	_ = tx.UnmarshalBinary(common.FromHex(c.raws[0]))
	if new(big.Int).SetBytes(tx.Data()[36:]).Cmp(entry.RawAmount) != 0 {
		t.Fatal("actual transfer data differs from plan")
	}
}

func TestSweepInsufficientLiveBalanceNeverSignsOrFunds(t *testing.T) {
	for _, live := range []int64{0, 9_000_000} {
		t.Run(strconv.FormatInt(live, 10), func(t *testing.T) {
			e, m, c, entry := setupExecutor(t)
			c.live, c.native = live, 0
			expectCreate(m, entry, nil, nil)
			expectWrite(m, "planned", nil, nil)
			out, err := e.sweepOne(t.Context(), entry, false)
			if err == nil || out.Status == "swept" || len(c.signs) != 0 || len(c.raws) != 0 {
				t.Fatalf("out=%+v err=%v", out, err)
			}
		})
	}
}

func TestSweepDatabaseFailureNeverReportsSuccess(t *testing.T) {
	for _, phase := range []string{"planned", "signed", "confirmed"} {
		t.Run(phase, func(t *testing.T) {
			e, m, c, entry := setupExecutor(t)
			diskFull := errors.New("disk full")
			if phase == "planned" {
				expectCreate(m, entry, diskFull, nil)
			} else {
				expectCreate(m, entry, nil, nil)
				expectAddressClear(m, entry)
				if phase == "signed" {
					expectWrite(m, "sweep_pending", diskFull, nil)
				} else {
					expectWrite(m, "sweep_pending", nil, nil)
					expectWrite(m, "swept", diskFull, nil)
				}
			}
			out, err := e.sweepOne(t.Context(), entry, false)
			if !errors.Is(err, diskFull) || out.Status == "swept" || out.Error == "" {
				t.Fatalf("out=%+v err=%v", out, err)
			}
			wantBroadcasts := 0
			if phase == "confirmed" {
				wantBroadcasts = 1
			}
			if len(c.raws) != wantBroadcasts {
				t.Fatalf("broadcast count=%d", len(c.raws))
			}
		})
	}
}

func TestSweepUnknownBroadcastRecoversWithoutNewSignature(t *testing.T) {
	e, m, c, entry := setupExecutor(t)
	c.sendUnknown = true
	expectCreate(m, entry, nil, nil)
	expectAddressClear(m, entry)
	expectWrite(m, "sweep_pending", nil, nil)
	expectWrite(m, "sweep_pending", nil, nil)
	out, err := e.sweepOne(t.Context(), entry, false)
	if err == nil || out.Status != "pending" {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	j := signedJob(c, entry, false)
	j.Revision = 2
	// Simulate a restarted executor, no plan, live balance zero, destination
	// and token config removed. The old immutable job must still reconcile.
	c.live = 0
	e.config.MinPerAddress, e.config.HotWalletEVM, e.tokens = nil, "", nil
	expectRunStart(m, j)
	expectWrite(m, "swept", nil, nil)
	expectUnlock(m)
	summary, err := e.Run(t.Context(), false)
	if err != nil || summary.Swept != 1 || len(c.signs) != 1 || len(c.raws) != 1 {
		t.Fatalf("summary=%+v signs=%d sends=%d err=%v", summary, len(c.signs), len(c.raws), err)
	}
}

func TestSweepRecoveryRebroadcastsIdenticalBytes(t *testing.T) {
	e, m, c, entry := setupExecutor(t)
	c.mined = false
	j := signedJob(c, entry, false)
	for range 2 {
		expectRunStart(m, j)
		expectWrite(m, "sweep_pending", nil, nil)
		expectUnlock(m)
		summary, err := e.Run(t.Context(), false)
		if err != nil || summary.Pending != 1 || summary.Swept != 0 {
			t.Fatalf("%+v %v", summary, err)
		}
		j.Revision++
	}
	if len(c.signs) != 0 || len(c.raws) != 2 || c.raws[0] != j.SweepRaw || c.raws[1] != j.SweepRaw {
		t.Fatal("recovery generated a replacement transaction")
	}
}

func TestSweepWaitsForConfiguredDepthAndCanonicalBlock(t *testing.T) {
	for _, state := range []string{"shallow", "reorg", "receipt_error", "reverted"} {
		t.Run(state, func(t *testing.T) {
			e, m, c, entry := setupExecutor(t)
			j := signedJob(c, entry, false)
			c.txs[j.SweepHash] = true
			status := "sweep_pending"
			switch state {
			case "shallow":
				c.head = 101
			case "reorg":
				c.canonical = "0x" + strings.Repeat("b", 64)
			case "receipt_error":
				c.receiptError = true
			case "reverted":
				c.status, status = "0x0", "manual_review"
			}
			expectRunStart(m, j)
			expectWrite(m, status, nil, nil)
			expectUnlock(m)
			summary, _ := e.Run(t.Context(), false)
			if summary.Swept != 0 || len(c.raws) != 0 || len(c.signs) != 0 {
				t.Fatalf("unsafe success/replacement: %+v", summary)
			}
		})
	}
}

func TestSweepMissingConfirmationPolicyObservesButCannotSendOrFinalize(t *testing.T) {
	e, m, c, entry := setupExecutor(t)
	j := signedJob(c, entry, false)
	c.txs[j.SweepHash] = true
	e.config.Confirmations = nil
	expectRunStart(m, j)
	// Persisting policy error uses the job's original requirement, not zero.
	expectWrite(m, "sweep_pending", nil, nil)
	expectUnlock(m)
	summary, err := e.Run(t.Context(), false)
	if err == nil || summary.Swept != 0 || c.receiptReads != 1 || len(c.raws) != 0 {
		t.Fatalf("summary=%+v reads=%d err=%v", summary, c.receiptReads, err)
	}
}

func TestSweepGasNonceUsesWalletHotSeedAddress(t *testing.T) {
	e, m, c, entry := setupExecutor(t)
	c.native = 0
	var durable atomic.Bool
	c.beforeSend = func() {
		if !durable.Load() {
			t.Error("broadcast before durable signed job")
		}
	}
	expectCreate(m, entry, nil, nil)
	expectAddressClear(m, entry)
	m.ExpectQuery(`SELECT EXISTS .*gas_payer`).WithArgs("bsc", int64(1), c.payer).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	expectWrite(m, "gas_pending", nil, &durable)
	expectWrite(m, "gas_confirmed", nil, nil)
	expectWrite(m, "sweep_pending", nil, nil)
	expectWrite(m, "swept", nil, nil)
	out, err := e.sweepOne(t.Context(), entry, false)
	if err != nil || out.Status != "swept" {
		t.Fatalf("%+v %v", out, err)
	}
	if len(c.signs) != 2 || c.signs[0].Kind != "gas" || c.signs[0].Index != 9 || c.signs[0].Nonce != 7 ||
		len(c.nonceReads) != 2 || c.nonceReads[0] != c.payer || c.nonceReads[1] != entry.Address {
		t.Fatalf("wrong gas payer/nonce: signs=%+v reads=%+v", c.signs, c.nonceReads)
	}
}

func TestSweepPendingGasNeverFundsAgainOrSignsSweep(t *testing.T) {
	e, m, c, entry := setupExecutor(t)
	c.native, c.mined = 0, false
	j := signedJob(c, entry, true)
	expectRunStart(m, j)
	expectWrite(m, "gas_pending", nil, nil)
	expectUnlock(m)
	summary, err := e.Run(t.Context(), false)
	if err != nil || summary.Pending != 1 || len(c.signs) != 0 || len(c.raws) != 1 || c.raws[0] != j.GasRaw {
		t.Fatalf("summary=%+v signs=%+v sends=%d err=%v", summary, c.signs, len(c.raws), err)
	}
}

func TestSweepLegacyHashBlocksReplanning(t *testing.T) {
	e, m, c, entry := setupExecutor(t)
	j := signedJob(c, entry, false)
	j.Version, j.Status, j.RawAmount = 0, "manual_review", nil
	j.SweepRaw, j.SweepNonce = "", ""
	e.config.MinPerAddress = map[string]float64{"bsc": 1}
	e.reader = &fakeReader{balances: map[string][]AddressBalance{"bsc": {
		{Chain: "bsc", Address: entry.Address, Token: entry.Token, DerivationIndex: 42, Raw: entry.RawAmount, Decimals: 6},
	}}}
	expectRunStart(m, j)
	expectUnlock(m)
	summary, err := e.Run(t.Context(), false)
	if err != nil || summary.Failed != 1 || summary.Entries[0].Status != "manual_review" || len(c.signs) != 0 || len(c.raws) != 0 {
		t.Fatalf("summary=%+v err=%v", summary, err)
	}
}

func TestSweepRejectsWalletAmountMismatchBeforeBroadcast(t *testing.T) {
	e, m, c, entry := setupExecutor(t)
	c.wrongAmount = true
	expectCreate(m, entry, nil, nil)
	expectAddressClear(m, entry)
	expectWrite(m, "planned", nil, nil)
	out, err := e.sweepOne(t.Context(), entry, false)
	if err == nil || out.Status == "swept" || len(c.raws) != 0 {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}

func TestSweepCrossInstanceLockDeniesRunBeforeAnyRPC(t *testing.T) {
	e, m, c, _ := setupExecutor(t)
	m.ExpectQuery(`SELECT pg_try_advisory_lock`).WithArgs(sweepLockKey).
		WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(false))
	_, err := e.Run(context.Background(), false)
	if err == nil || len(c.signs) != 0 || len(c.raws) != 0 || c.receiptReads != 0 {
		t.Fatalf("lock contention did not stop executor: %v", err)
	}
}

func TestSweepLockReleasedOnPersistenceFailure(t *testing.T) {
	e, m, _, _ := setupExecutor(t)
	m.ExpectQuery(`SELECT pg_try_advisory_lock`).WithArgs(sweepLockKey).
		WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(true))
	m.ExpectQuery("SELECT id, chain, from_address").WillReturnError(errors.New("database offline"))
	expectUnlock(m)
	_, err := e.Run(t.Context(), false)
	if err == nil {
		t.Fatal("load error was hidden")
	}
	// The released connection can be reused to acquire exactly one lock.
	expectRunStart(m)
	expectUnlock(m)
	if _, err := e.Run(t.Context(), false); err != nil {
		t.Fatal(err)
	}
}

func TestSweepFinalWriteFailureRecoversWithoutDuplicateTransfer(t *testing.T) {
	e, m, c, entry := setupExecutor(t)
	e.config.MinPerAddress = map[string]float64{"bsc": 1}
	e.reader = &fakeReader{balances: map[string][]AddressBalance{"bsc": {
		{Chain: "bsc", Address: entry.Address, Token: entry.Token, DerivationIndex: 42, Raw: entry.RawAmount, Decimals: 6},
	}}}
	diskFull := errors.New("disk full during final record")
	expectRunStart(m)
	expectCreate(m, entry, nil, nil)
	expectAddressClear(m, entry)
	expectWrite(m, "sweep_pending", nil, nil)
	expectWrite(m, "swept", diskFull, nil)
	expectUnlock(m)
	summary, err := e.Run(t.Context(), false)
	if !errors.Is(err, diskFull) || summary.Swept != 0 || summary.Pending != 1 {
		t.Fatalf("final-write failure reported success: %+v %v", summary, err)
	}
	j := signedJob(c, entry, false) // DB kept the signed pending job, revision 1
	c.live = 0
	e.reader = &fakeReader{}
	expectRunStart(m, j)
	expectWrite(m, "swept", nil, nil)
	expectUnlock(m)
	summary, err = e.Run(t.Context(), false)
	if err != nil || summary.Swept != 1 || len(c.signs) != 1 || len(c.raws) != 1 {
		t.Fatalf("retry duplicated spend: %+v signs=%d sends=%d %v", summary, len(c.signs), len(c.raws), err)
	}
}

func TestSweepAllUnfinishedJobsRecoveredWithoutWaitingForFirst(t *testing.T) {
	for _, first := range []string{"shallow", "broadcast_unknown"} {
		t.Run(first, func(t *testing.T) {
			e, m, c, entry := setupExecutor(t)
			j1 := signedJob(c, entry, false)
			secondEntry := entry
			secondEntry.Token = "0x3333333333333333333333333333333333333333"
			j2 := signedJob(c, secondEntry, false)
			j2.ID = 2
			// Same address, different token. Recovery must visit every job;
			// an unresolved address blocks new planning, not existing hashes.
			c.txs[j2.SweepHash] = true
			if first == "shallow" {
				c.txs[j1.SweepHash] = true
				c.blocks = map[string]uint64{j1.SweepHash: 102}
			} else {
				c.sendUnknown = true
			}
			expectRunStart(m, j1, j2)
			expectWrite(m, "sweep_pending", nil, nil)
			expectWriteID(m, 2, "swept", nil, nil)
			expectUnlock(m)
			summary, err := e.Run(t.Context(), false)
			if summary.Pending != 1 || summary.Swept != 1 || len(summary.Entries) != 2 || len(c.signs) != 0 {
				t.Fatalf("first job blocked the group: %+v %v", summary, err)
			}
			if (err != nil) != (first == "broadcast_unknown") {
				t.Fatalf("error propagation differs from outcome: %v", err)
			}
		})
	}
}

func TestSweepMissingRPCStillSurfacesUnfinishedJob(t *testing.T) {
	e, m, c, entry := setupExecutor(t)
	j := signedJob(c, entry, false)
	e.pools, e.config.MinPerAddress = nil, nil
	expectRunStart(m, j)
	expectWrite(m, "sweep_pending", nil, nil)
	expectUnlock(m)
	summary, err := e.Run(t.Context(), false)
	if err == nil || summary.Pending != 1 || len(summary.Entries) != 1 || len(c.raws) != 0 {
		t.Fatalf("unfinished job disappeared after chain removal: %+v %v", summary, err)
	}
}

func TestSweepGasPersistenceFailuresNeverAllowUnrecordedSpending(t *testing.T) {
	for _, phase := range []string{"signed", "confirmed"} {
		t.Run(phase, func(t *testing.T) {
			e, m, c, entry := setupExecutor(t)
			c.native = 0
			diskFull := errors.New("gas record disk full")
			expectCreate(m, entry, nil, nil)
			expectAddressClear(m, entry)
			m.ExpectQuery(`SELECT EXISTS .*gas_payer`).WithArgs("bsc", int64(1), c.payer).
				WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
			if phase == "signed" {
				expectWrite(m, "gas_pending", diskFull, nil)
			} else {
				expectWrite(m, "gas_pending", nil, nil)
				expectWrite(m, "gas_confirmed", diskFull, nil)
			}
			out, err := e.sweepOne(t.Context(), entry, false)
			if !errors.Is(err, diskFull) || out.Status == "swept" || len(c.signs) != 1 {
				t.Fatalf("gas write failure continued sweep: %+v %v", out, err)
			}
			if phase == "signed" && len(c.raws) != 0 {
				t.Fatal("unpersisted gas broadcast")
			}
			if phase == "confirmed" && len(c.raws) != 1 {
				t.Fatal("expected exactly the persisted gas broadcast")
			}
		})
	}
}

func TestSweepConfirmedGasRecoveryDoesNotTopUpAgain(t *testing.T) {
	e, m, c, entry := setupExecutor(t)
	j := signedJob(c, entry, true)
	c.txs[j.GasHash] = true
	// The gas receipt is already confirmed at restart; native balance was
	// credited on-chain, so only a single child token signature is needed.
	expectRunStart(m, j)
	expectWrite(m, "gas_confirmed", nil, nil)
	expectAddressClear(m, entry)
	expectWrite(m, "sweep_pending", nil, nil)
	expectWrite(m, "swept", nil, nil)
	expectUnlock(m)
	summary, err := e.Run(t.Context(), false)
	if err != nil || summary.Swept != 1 || len(c.raws) != 1 || len(c.signs) != 1 || c.signs[0].Kind != "sweep" {
		t.Fatalf("recovery resent gas: %+v signs=%+v %v", summary, c.signs, err)
	}
}

func TestSweepUnresolvedHotNonceBlocksNewFunding(t *testing.T) {
	e, m, c, entry := setupExecutor(t)
	c.native = 0
	expectCreate(m, entry, nil, nil)
	expectAddressClear(m, entry)
	m.ExpectQuery(`SELECT EXISTS .*gas_payer`).WithArgs("bsc", int64(1), c.payer).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	expectWrite(m, "planned", nil, nil)
	out, err := e.sweepOne(t.Context(), entry, false)
	if err != nil || out.Status != "pending" || out.Error != "awaiting gas payer transaction confirmation" || len(c.nonceReads) != 0 || len(c.signs) != 0 || len(c.raws) != 0 {
		t.Fatalf("unresolved nonce was reused: %+v %v", out, err)
	}
}

func TestSweepMissingRawBytesRequireManualReview(t *testing.T) {
	e, m, c, entry := setupExecutor(t)
	j := signedJob(c, entry, false)
	j.SweepRaw = ""
	expectRunStart(m, j)
	expectWrite(m, "manual_review", nil, nil)
	expectUnlock(m)
	summary, err := e.Run(t.Context(), false)
	if err == nil || summary.Failed != 1 || len(c.signs) != 0 || len(c.raws) != 0 {
		t.Fatalf("missing bytes triggered replacement: %+v %v", summary, err)
	}
}

func TestSweepUnknownOutcomeWriteFailurePropagates(t *testing.T) {
	e, m, c, entry := setupExecutor(t)
	c.sendUnknown = true
	expectCreate(m, entry, nil, nil)
	expectAddressClear(m, entry)
	expectWrite(m, "sweep_pending", nil, nil)
	diskFull := errors.New("pending record disk full")
	expectWrite(m, "sweep_pending", diskFull, nil)
	out, err := e.sweepOne(t.Context(), entry, false)
	if !errors.Is(err, diskFull) || out.Status != "pending" {
		t.Fatalf("%+v %v", out, err)
	}
}

func TestSweepRevisionConflictDoesNotBroadcast(t *testing.T) {
	e, m, c, entry := setupExecutor(t)
	expectCreate(m, entry, nil, nil)
	expectAddressClear(m, entry)
	m.ExpectExec("UPDATE sweep_records SET").WillReturnResult(sqlmock.NewResult(0, 0))
	out, err := e.sweepOne(t.Context(), entry, false)
	if err == nil || out.Status == "swept" || len(c.raws) != 0 {
		t.Fatalf("%+v %v", out, err)
	}
}

func TestSweepLockCleanupIgnoresCanceledRunContext(t *testing.T) {
	e, m, _, _ := setupExecutor(t)
	ctx, cancel := context.WithCancel(t.Context())
	m.ExpectQuery(`SELECT pg_try_advisory_lock`).WithArgs(sweepLockKey).
		WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(true))
	_, release, err := e.lock(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	expectUnlock(m)
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if e.db.Stats().InUse != 0 {
		t.Fatal("lock connection leaked")
	}
}

func TestSweepFailedUnlockDiscardsConnection(t *testing.T) {
	e, m, _, _ := setupExecutor(t)
	expectRunStart(m)
	unlockErr := errors.New("lost unlock reply")
	m.ExpectQuery(`SELECT pg_advisory_unlock`).WithArgs(sweepLockKey).WillReturnError(unlockErr)
	m.ExpectClose()
	_, err := e.Run(t.Context(), false)
	if !errors.Is(err, unlockErr) || e.db.Stats().OpenConnections != 0 {
		t.Fatalf("possibly locked connection returned to pool: stats=%+v err=%v", e.db.Stats(), err)
	}
}
