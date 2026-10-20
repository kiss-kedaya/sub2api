package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/programs/token"
)

const regressionToken = "0x55d398326f99059ff775485246999027b3197955"

func regressionPolicy() *Policy {
	return &Policy{
		Targets:   []string{"0x1111111111111111111111111111111111111111"},
		HotIndex:  3,
		MaxPerTx:  map[string]string{"native": "100000", regressionToken: "100"},
		MaxDaily:  map[string]string{"native": "200000", regressionToken: "1000"},
		EVMChains: map[string]EVMChainPolicy{"56": {MaxGasLimit: 100000, MaxGasPrice: "10", MaxFee: "1000000"}},
	}
}

func writeTestPolicy(t *testing.T, p *Policy) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "policy.json")
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	return path
}

func testBudget(t *testing.T) (*limiter, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "budget.sqlite")
	if err := initializeBudget(path, time.Now()); err != nil {
		t.Fatal(err)
	}
	l, err := newPersistentLimiter(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.db.Close() })
	return l, path
}

func regressionServer(t *testing.T, p *Policy) *server {
	t.Helper()
	path := writeTestPolicy(t, p)
	approved, err := loadPolicyFile(path)
	if err != nil {
		t.Fatal(err)
	}
	d, err := NewDeriver(testDepositMnemonic)
	if err != nil {
		t.Fatal(err)
	}
	hot, err := NewDeriver(testHotMnemonic)
	if err != nil {
		t.Fatal(err)
	}
	l, _ := testBudget(t)
	return &server{deriver: d, hotDeriver: hot, policy: approved, policyPath: path, limiter: l}
}

func signRequest() evmSignRequest {
	return evmSignRequest{Kind: "sweep", ChainID: 56, Index: 7, To: regressionPolicy().Targets[0], Token: "native", Amount: "1", GasPrice: "1", GasLimit: 21000}
}

func requestJSON(t *testing.T, handler http.HandlerFunc, method string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handler(w, httptest.NewRequest(method, "/", bytes.NewReader(raw)))
	return w
}

func decodeEVM(t *testing.T, w *httptest.ResponseRecorder) *types.Transaction {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("signing status %d: %s", w.Code, w.Body)
	}
	var out struct {
		Signed string `json:"signed_tx"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	raw, err := hex.DecodeString(strings.TrimPrefix(out.Signed, "0x"))
	if err != nil {
		t.Fatal(err)
	}
	var tx types.Transaction
	if err := tx.UnmarshalBinary(raw); err != nil {
		t.Fatal(err)
	}
	return &tx
}

func TestNativeFeesCountTowardPerTxAndDailyAcrossRestart(t *testing.T) {
	p := regressionPolicy()
	p.MaxPerTx["native"] = "1000"
	s := regressionServer(t, p)
	if w := requestJSON(t, s.handleSignEVM, "POST", signRequest()); w.Code != 403 || !strings.Contains(w.Body.String(), "per_tx_limit") {
		t.Fatalf("fee bypass: %d %s", w.Code, w.Body)
	}
	// Exact cost: 1 value + 21000 gas * 1 fee cap.
	s.policy.MaxPerTx["native"], s.policy.MaxDaily["native"] = "21001", "42001"
	tx := decodeEVM(t, requestJSON(t, s.handleSignEVM, "POST", signRequest()))
	if got := tx.Cost().String(); got != "21001" || tx.GasFeeCap().String() != "1" {
		t.Fatalf("unexpected signed cost: %s", got)
	}
	var path string
	if err := s.limiter.db.QueryRow("PRAGMA database_list").Scan(new(int), new(string), &path); err != nil {
		t.Fatal(err)
	}
	_ = s.limiter.db.Close()
	l, err := newPersistentLimiter(path)
	if err != nil {
		t.Fatal("reopen ledger failed:", err)
	}
	s.limiter = l
	t.Cleanup(func() { _ = l.db.Close() })
	if w := requestJSON(t, s.handleSignEVM, "POST", signRequest()); w.Code != 403 || !strings.Contains(w.Body.String(), "daily_limit") {
		t.Fatalf("restart reused budget: %d %s", w.Code, w.Body)
	}
}

func TestERC20ChargesTokenAndNativeAtomically(t *testing.T) {
	p := regressionPolicy()
	p.MaxPerTx["native"], p.MaxDaily["native"] = "21000", "21000"
	s := regressionServer(t, p)
	req := signRequest()
	req.Token, req.Amount = regressionToken, "5"
	tx := decodeEVM(t, requestJSON(t, s.handleSignEVM, "POST", req))
	if tx.Value().Sign() != 0 || tx.To() == nil || *tx.To() != common.HexToAddress(regressionToken) || len(tx.Data()) != 68 || new(big.Int).SetBytes(tx.Data()[36:]).Int64() != 5 {
		t.Fatal("signed ERC20 calldata/value differs from budget")
	}
	w := requestJSON(t, s.handleSignEVM, "POST", req)
	if w.Code != 403 || !strings.Contains(w.Body.String(), "daily_limit") {
		t.Fatalf("ERC20 fee bypass: %d %s", w.Code, w.Body)
	}
	var raw string
	if err := s.limiter.db.QueryRow("SELECT state FROM signer_budget").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	state, err := decodeBudget(raw)
	if err != nil || state.Totals["native"] != "21000" || state.Totals[regressionToken] != "5" {
		t.Fatalf("non-atomic token/native budget: %s %v", raw, err)
	}
}

func TestMissingNativeDailyBudgetDeniesERC20(t *testing.T) {
	p := regressionPolicy()
	delete(p.MaxDaily, "native")
	s := regressionServer(t, p)
	req := signRequest()
	req.Token = regressionToken
	if w := requestJSON(t, s.handleSignEVM, "POST", req); w.Code != 403 {
		t.Fatalf("missing daily cap signed: %d %s", w.Code, w.Body)
	}
}

func TestChainFeeCapAndScopedLimitCannotFallBack(t *testing.T) {
	p := regressionPolicy()
	s := regressionServer(t, p)
	chain := s.policy.EVMChains["56"]
	chain.MaxFee = "20999"
	s.policy.EVMChains["56"] = chain
	if w := requestJSON(t, s.handleSignEVM, "POST", signRequest()); w.Code == 200 {
		t.Fatal("max_fee not enforced")
	}
	chain.MaxFee = "1000000"
	s.policy.EVMChains["56"] = chain
	// A scoped per-tx limit with no scoped daily limit must not fall back to
	// the larger legacy allowance.
	s.policy.MaxPerTx["evm:56:native"] = "21001"
	if w := requestJSON(t, s.handleSignEVM, "POST", signRequest()); w.Code != 403 || !strings.Contains(w.Body.String(), "no_daily_limit_configured") {
		t.Fatalf("partial scoped limits fell back: %d %s", w.Code, w.Body)
	}
	s.policy.MaxDaily["evm:56:native"] = "21001"
	decodeEVM(t, requestJSON(t, s.handleSignEVM, "POST", signRequest()))
}

func TestEVMChainGasAndIntegerBoundsAtHTTPBoundary(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*evmSignRequest)
	}{
		{"unknown_chain", func(r *evmSignRequest) { r.ChainID = 1 }},
		{"negative_chain", func(r *evmSignRequest) { r.ChainID = -56 }},
		{"zero_chain", func(r *evmSignRequest) { r.ChainID = 0 }},
		{"gas_limit", func(r *evmSignRequest) { r.GasLimit = 100001 }},
		{"gas_price", func(r *evmSignRequest) { r.GasPrice = "11" }},
		{"uint256_amount", func(r *evmSignRequest) { r.Amount = new(big.Int).Lsh(big.NewInt(1), 256).String() }},
		{"uint256_erc20", func(r *evmSignRequest) {
			r.Token = regressionToken
			r.Amount = new(big.Int).Lsh(big.NewInt(1), 256).String()
		}},
		{"uint256_price", func(r *evmSignRequest) { r.GasPrice = new(big.Int).Lsh(big.NewInt(1), 256).String() }},
		{"fee_product_overflow", func(r *evmSignRequest) { r.GasPrice = new(big.Int).Lsh(big.NewInt(1), 255).String() }},
		{"total_cost_overflow", func(r *evmSignRequest) {
			r.Amount = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1)).String()
		}},
		{"invalid_token", func(r *evmSignRequest) { r.Token = "0x1234" }},
		{"unknown_kind", func(r *evmSignRequest) { r.Kind = "withdraw" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := regressionServer(t, regressionPolicy())
			req := signRequest()
			tc.change(&req)
			if w := requestJSON(t, s.handleSignEVM, "POST", req); w.Code == 200 || strings.Contains(w.Body.String(), "signed_tx") {
				t.Fatalf("invalid request signed: %d %s", w.Code, w.Body)
			}
		})
	}
	s := regressionServer(t, regressionPolicy())
	for _, field := range []string{"gas_limit", "nonce", "index"} {
		raw, _ := json.Marshal(signRequest())
		var obj map[string]any
		_ = json.Unmarshal(raw, &obj)
		obj[field] = json.Number("18446744073709551616")
		if w := requestJSON(t, s.handleSignEVM, "POST", obj); w.Code != 400 {
			t.Fatalf("%s overflow accepted: %d %s", field, w.Code, w.Body)
		}
	}
}

func TestGasAlwaysSignsWithHotSeedAndApprovedIndex(t *testing.T) {
	s := regressionServer(t, regressionPolicy())
	index := uint32(9)
	_, target, _ := s.deriver.EVMKey(index)
	// This whitelist hit used to bypass the entire gas authorization branch.
	s.policy.Targets = append(s.policy.Targets, target.Hex())
	req := signRequest()
	req.Kind, req.GasIndex, req.To, req.Index = "gas", &index, target.Hex(), 999
	tx := decodeEVM(t, requestJSON(t, s.handleSignEVM, "POST", req))
	sender, err := types.Sender(types.LatestSignerForChainID(big.NewInt(56)), tx)
	_, expected, _ := s.hotDeriver.EVMKey(s.policy.HotIndex)
	if err != nil || sender != expected {
		t.Fatalf("gas sender=%s want hot[%d]=%s: %v", sender, s.policy.HotIndex, expected, err)
	}
	for _, tc := range []struct {
		name   string
		change func(*evmSignRequest)
	}{
		{"token", func(r *evmSignRequest) { r.Token = regressionToken }},
		{"no_index", func(r *evmSignRequest) { r.GasIndex = nil }},
		{"foreign_whitelisted", func(r *evmSignRequest) { r.To = regressionPolicy().Targets[0] }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := req
			tc.change(&r)
			if w := requestJSON(t, s.handleSignEVM, "POST", r); w.Code != 403 {
				t.Fatalf("gas authorization bypass: %d %s", w.Code, w.Body)
			}
		})
	}
}

func TestHotAddressContractAndIndependentPolicyBoundary(t *testing.T) {
	s := regressionServer(t, regressionPolicy())
	// There are no expected DB calls: even a present shared store cannot
	// alter policy or gas index on either signing or address lookup.
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s.store = &store{db: db}
	w := requestJSON(t, s.handleHotAddress, "GET", nil)
	var addresses map[string]json.RawMessage
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &addresses) != nil || len(addresses) != 3 {
		t.Fatalf("hot-address contract changed: %d %s", w.Code, w.Body)
	}
	_, expected, _ := s.hotDeriver.EVMKey(s.policy.HotIndex)
	var evm string
	_ = json.Unmarshal(addresses["evm"], &evm)
	if evm != expected.Hex() || addresses["index"] == nil || addresses["solana"] == nil {
		t.Fatal("wrong gas funding addresses")
	}
	decodeEVM(t, requestJSON(t, s.handleSignEVM, "POST", signRequest()))
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	// Loading even a valid legacy DB policy into memory does not enable signing.
	s.policyPath = ""
	if w := requestJSON(t, s.handleSignEVM, "POST", signRequest()); w.Code != 503 {
		t.Fatalf("DB policy authorized signing: %d", w.Code)
	}
	if w := requestJSON(t, s.handleHotAddress, "GET", nil); w.Code != 503 {
		t.Fatalf("DB policy chose gas address: %d", w.Code)
	}
}

func TestReadOnlyPolicyValidation(t *testing.T) {
	path := writeTestPolicy(t, regressionPolicy())
	if _, err := loadPolicyFile(path); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPolicyFile("relative-policy.json"); err == nil {
		t.Fatal("relative policy accepted")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPolicyFile(path); err == nil {
		t.Fatal("web-writable policy accepted")
	}
	for _, raw := range []string{`{"targets": []}`, `{"targets": [], "typo": 1}`, `{} {}`} {
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		_ = os.Chmod(path, 0o400)
		if _, err := loadPolicyFile(path); err == nil {
			t.Fatalf("invalid policy accepted: %s", raw)
		}
		_ = os.Chmod(path, 0o600)
	}
}

func TestSolanaInstructionUsesSourceThenMintAndPreservesBase58Case(t *testing.T) {
	d, _ := NewDeriver(testDepositMnemonic)
	_, from, _ := d.SolanaKey(2)
	_, to, _ := d.SolanaKey(3)
	mint := solana.MustPublicKeyFromBase58("EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v")
	req := solSignRequest{Index: 2, Mint: mint.String(), To: to.String(), Amount: "1234567", RecentBlockhash: solana.Hash{}.String()}
	signed, err := signSolTransfer(d, req)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(signed)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := solana.TransactionFromBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.VerifySignatures(); err != nil {
		t.Fatal(err)
	}
	if len(tx.Message.Instructions) != 2 {
		t.Fatal("expected idempotent ATA + transferChecked")
	}
	ix := tx.Message.Instructions[1]
	accounts, err := ix.ResolveInstructionAccounts(&tx.Message)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := token.DecodeInstruction(accounts, ix.Data)
	if err != nil {
		t.Fatal(err)
	}
	transfer, ok := decoded.Impl.(*token.TransferChecked)
	if !ok {
		t.Fatal("not transferChecked")
	}
	source, _, _ := solana.FindAssociatedTokenAddress(from, mint)
	dest, _, _ := solana.FindAssociatedTokenAddress(to, mint)
	if accounts[0].PublicKey != source || accounts[1].PublicKey != mint || accounts[2].PublicKey != dest || accounts[3].PublicKey != from || *transfer.Amount != 1234567 || *transfer.Decimals != 6 {
		t.Fatal("incorrect signed transferChecked accounts or data")
	}
	p := &Policy{Targets: []string{to.String()}}
	if !p.AllowsTarget(to.String()) || p.AllowsTarget(strings.ToLower(to.String())) {
		t.Fatal("Solana target case was folded")
	}
	if TokenKey(mint.String()) != mint.String() {
		t.Fatal("Solana mint case was folded")
	}
	p.Targets = []string{"0x" + strings.Repeat("Ab", 20)}
	if !p.AllowsTarget(strings.ToLower(p.Targets[0])) {
		t.Fatal("EVM case compatibility lost")
	}
	// Neither native SOL nor SPL is allowed to truncate an oversized uint64.
	for _, mint := range []string{"", mint.String()} {
		req.Mint, req.Amount = mint, "18446744073709551616"
		if signed, err := signSolTransfer(d, req); err == nil || signed != "" {
			t.Fatal("uint64 overflow signed")
		}
	}
}

func TestFailedSolanaSigningDoesNotConsumePersistentBudget(t *testing.T) {
	p := regressionPolicy()
	d, _ := NewDeriver(testDepositMnemonic)
	_, target, _ := d.SolanaKey(3)
	p.Targets = append(p.Targets, target.String())
	p.Solana = &SolanaPolicy{MaxFeeLamports: 5000, ATARentLamports: 2039280}
	p.MaxPerTx["solana:native"], p.MaxDaily["solana:native"] = "5001", "5001"
	s := regressionServer(t, p)
	req := solSignRequest{To: target.String(), Amount: "1", RecentBlockhash: "invalid"}
	if w := requestJSON(t, s.handleSignSol, "POST", req); w.Code != 400 {
		t.Fatalf("bad blockhash must retain HTTP 400: %d %s", w.Code, w.Body)
	}
	req.RecentBlockhash = solana.Hash{}.String()
	if w := requestJSON(t, s.handleSignSol, "POST", req); w.Code != 200 {
		t.Fatalf("failed signature consumed budget: %d %s", w.Code, w.Body)
	}
	if w := requestJSON(t, s.handleSignSol, "POST", req); w.Code != 403 {
		t.Fatalf("successful signature not charged: %d %s", w.Code, w.Body)
	}
}

func TestPersistentBudgetNeverSilentlyResets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.sqlite")
	if _, err := newPersistentLimiter(path); err == nil {
		t.Fatal("missing ledger auto-created")
	}
	l, path := testBudget(t)
	if err := initializeBudget(path, time.Now()); err == nil {
		t.Fatal("existing ledger overwritten")
	}
	if _, err := l.db.Exec("UPDATE signer_budget SET state = 'not-json'"); err != nil {
		t.Fatal(err)
	}
	if _, err := newPersistentLimiter(path); err == nil {
		t.Fatal("corrupt ledger reset at restart")
	}
	signed, _, err := l.authorizeAndSign(context.Background(), regressionPolicy(), map[string]*big.Int{"native": big.NewInt(1)}, time.Now(), func() (string, error) { return "must not escape", nil })
	if err == nil || signed != "" {
		t.Fatal("corrupt ledger authorized signing")
	}
}

func TestBudgetWriteAndCommitFailureReleaseNoSignature(t *testing.T) {
	for _, failure := range []string{"read", "write", "commit"} {
		t.Run(failure, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			l := &limiter{db: db}
			raw, _ := json.Marshal(dailyState{Day: utcDay(time.Now()), Totals: map[string]string{}})
			mock.ExpectBegin()
			if failure == "read" {
				mock.ExpectQuery("SELECT state").WillReturnError(errors.New("read failed"))
				mock.ExpectRollback()
			} else {
				mock.ExpectQuery("SELECT state").WillReturnRows(sqlmock.NewRows([]string{"state"}).AddRow(string(raw)))
				if failure == "write" {
					mock.ExpectExec("UPDATE signer_budget").WillReturnError(errors.New("disk full"))
					mock.ExpectRollback()
				} else {
					mock.ExpectExec("UPDATE signer_budget").WillReturnResult(sqlmock.NewResult(0, 1))
					mock.ExpectCommit().WillReturnError(errors.New("commit failed"))
				}
			}
			signed, _, err := l.authorizeAndSign(context.Background(), regressionPolicy(), map[string]*big.Int{"native": big.NewInt(1)}, time.Now(), func() (string, error) { return "signed bytes", nil })
			if err == nil || signed != "" {
				t.Fatal("persistence error leaked signature")
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBudgetClockRollbackRejected(t *testing.T) {
	l, _ := testBudget(t)
	now := time.Now()
	charges := map[string]*big.Int{"native": big.NewInt(1)}
	if _, reason, err := l.authorizeAndSign(context.Background(), regressionPolicy(), charges, now, func() (string, error) { return "ok", nil }); err != nil || reason != "" {
		t.Fatal(reason, err)
	}
	if signed, _, err := l.authorizeAndSign(context.Background(), regressionPolicy(), charges, now.Add(-24*time.Hour), func() (string, error) { return "bad", nil }); err == nil || signed != "" {
		t.Fatal("clock rollback reset budget")
	}
}

func TestRealLedgerWriteFailureRollsBackBudget(t *testing.T) {
	l, _ := testBudget(t)
	if _, err := l.db.Exec(`CREATE TRIGGER reject_budget BEFORE UPDATE ON signer_budget BEGIN SELECT RAISE(ABORT, 'disk failure'); END`); err != nil {
		t.Fatal(err)
	}
	p := regressionPolicy()
	p.MaxDaily["native"] = "1"
	charges := map[string]*big.Int{"native": big.NewInt(1)}
	sign := func() (string, error) { return "signed bytes", nil }
	if signed, _, err := l.authorizeAndSign(context.Background(), p, charges, time.Now(), sign); err == nil || signed != "" {
		t.Fatal("write failure returned a signature")
	}
	if _, err := l.db.Exec("DROP TRIGGER reject_budget"); err != nil {
		t.Fatal(err)
	}
	if signed, reason, err := l.authorizeAndSign(context.Background(), p, charges, time.Now(), sign); err != nil || reason != "" || signed == "" {
		t.Fatal("failed persistence consumed budget", reason, err)
	}
}

func TestRateLimitSurvivesRestart(t *testing.T) {
	l, path := testBudget(t)
	p := regressionPolicy()
	p.RatePerMinute = 1
	now := time.Now()
	sign := func() (string, error) { return "ok", nil }
	charges := map[string]*big.Int{"native": big.NewInt(1)}
	if _, reason, err := l.authorizeAndSign(context.Background(), p, charges, now, sign); err != nil || reason != "" {
		t.Fatal(reason, err)
	}
	_ = l.db.Close()
	l, err := newPersistentLimiter(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.db.Close()
	if signed, reason, err := l.authorizeAndSign(context.Background(), p, charges, now, sign); err != nil || reason != "rate_limit" || signed != "" {
		t.Fatal("restart reset minute allowance", reason, err)
	}
}

// Real subprocesses exercise SQLite's interprocess lock, not just a Go mutex.
func TestBudgetProcessesCannotReuseLastDailyAllowance(t *testing.T) {
	if path := os.Getenv("SIGNER_TEST_BUDGET_WORKER"); path != "" {
		l, err := newPersistentLimiter(path)
		if err != nil {
			t.Fatal(err)
		}
		defer l.db.Close()
		p := regressionPolicy()
		p.MaxDaily["native"] = "1"
		signed, reason, err := l.authorizeAndSign(context.Background(), p, map[string]*big.Int{"native": big.NewInt(1)}, time.Now(), func() (string, error) { return "ok", nil })
		if err != nil {
			t.Fatal(err)
		}
		if signed == "ok" {
			fmt.Println("BUDGET_SIGNED")
		} else if reason != "daily_limit" {
			t.Fatal(reason)
		}
		return
	}
	l, path := testBudget(t)
	_ = l.db.Close()
	const workers = 4
	var wg sync.WaitGroup
	outputs := make([][]byte, workers)
	errs := make([]error, workers)
	for i := range workers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cmd := exec.Command(os.Args[0], "-test.run=^TestBudgetProcessesCannotReuseLastDailyAllowance$", "-test.v")
			cmd.Env = append(os.Environ(), "SIGNER_TEST_BUDGET_WORKER="+path)
			outputs[i], errs[i] = cmd.CombinedOutput()
		}(i)
	}
	wg.Wait()
	signed := 0
	for i := range workers {
		if errs[i] != nil {
			t.Fatalf("worker: %v %s", errs[i], outputs[i])
		}
		signed += bytes.Count(outputs[i], []byte("BUDGET_SIGNED"))
	}
	if signed != 1 {
		t.Fatalf("%d processes reused the last allowance", signed)
	}
	// Reopening yet another process must see the exhausted allowance too.
	other, err := newPersistentLimiter(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.db.Close()
	var raw string
	if err := other.db.QueryRow("SELECT state FROM signer_budget").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	state, err := decodeBudget(raw)
	if err != nil || state.Totals["native"] != "1" {
		t.Fatal(raw, err)
	}
}
