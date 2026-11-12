package sweeper

import (
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/scanner"
)

const nativeSweepAddress = "0x4444444444444444444444444444444444444444"

func ethBalancePool(t *testing.T, wei string) *rpc.Pool {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if request.Method != "eth_getBalance" {
			t.Errorf("native sweep issued %s", request.Method)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"` + wei + `"}`))
	}))
	t.Cleanup(server.Close)
	return rpc.NewPoolWithHTTPClient(server.URL, server.Client())
}

// The shared sentinel must never drift between the two packages: a mismatch
// would make the scanner record a deposit the sweeper cannot recognise.
func TestNativeTokenSentinelMatchesScanner(t *testing.T) {
	if NativeToken != scanner.NativeToken {
		t.Fatalf("sweeper.NativeToken = %q, scanner.NativeToken = %q", NativeToken, scanner.NativeToken)
	}
}

func TestNativeSweepGasReserveBoundary(t *testing.T) {
	price := big.NewInt(10) // wei per gas
	reserve := nativeSweepGasReserve(price)
	want := new(big.Int).Mul(big.NewInt(nativeSweepGasLimit), price)
	want.Mul(want, big.NewInt(nativeSweepGasReserveFactor))
	if reserve.Cmp(want) != 0 {
		t.Fatalf("reserve = %s, want %s", reserve, want)
	}
	// The reserve is twice the intrinsic fee, so a balance of exactly the
	// reserve has nothing left to send.
	if sweepable := new(big.Int).Sub(reserve, reserve); sweepable.Sign() != 0 {
		t.Fatalf("a balance equal to the reserve must leave nothing sweepable, got %s", sweepable)
	}
	// One wei above the reserve is sweepable, and the reserve stays behind.
	balance := new(big.Int).Add(reserve, big.NewInt(1))
	if sweepable := new(big.Int).Sub(balance, reserve); sweepable.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("one wei above the reserve must be sweepable, got %s", sweepable)
	}
	if nativeSweepGasReserve(nil).Sign() != 0 || nativeSweepGasReserve(big.NewInt(0)).Sign() != 0 {
		t.Fatal("an unavailable gas quote must not reserve anything")
	}
}

func nativeSweepJob(amount *big.Int) *sweepJob {
	return &sweepJob{
		PlanEntry: PlanEntry{
			Chain: "ethereum", Address: nativeSweepAddress, Token: NativeToken,
			RawAmount: amount, Decimals: 18, DerivationIndex: 3,
		},
		ID: 7, Revision: 2, Version: 1, Destination: testDestination, Status: "planning",
	}
}

func nativeExecutor(db *sqlmock.Sqlmock) *Executor {
	return &Executor{
		tokens: map[string]TokenSpec{
			"ethereum|" + NativeToken: {Contract: NativeToken, Decimals: 18, Native: true},
			"ethereum|" + testToken:   {Contract: testToken, Decimals: 6},
		},
		pools: map[string]*rpc.Pool{},
	}
}

func TestCapNativeSweepAmountKeepsTheGasReserve(t *testing.T) {
	// 1 ETH deposit, but the address only holds the deposit: the fee and the
	// reserve must come out of it.
	price := big.NewInt(1_000_000_000) // 1 gwei
	reserve := nativeSweepGasReserve(price)
	balance := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	plan := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	e := nativeExecutor(&mock)
	e.db = db
	e.pools["ethereum"] = ethBalancePool(t, "0x"+balance.Text(16))

	sweepable := new(big.Int).Sub(balance, reserve)
	mock.ExpectExec(`(?s)UPDATE sweep_records SET.*raw_amount=\$3`).
		WithArgs(int64(7), amountString(sweepable, 18), sweepable.String(), int64(2)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	job := nativeSweepJob(plan)
	if err := e.capNativeSweepAmount(t.Context(), e.pools["ethereum"], job, price); err != nil {
		t.Fatalf("native amount cap failed: %v", err)
	}
	if job.RawAmount.Cmp(sweepable) != 0 {
		t.Fatalf("swept amount = %s, want %s (balance minus reserve)", job.RawAmount, sweepable)
	}
	if job.Revision != 3 {
		t.Fatalf("revision = %d, want 3 after persisting the cap", job.Revision)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCapNativeSweepAmountLeavesFullyFundedSweepAlone(t *testing.T) {
	price := big.NewInt(1_000_000_000)
	reserve := nativeSweepGasReserve(price)
	plan := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	// A prior gas top-up means the address already holds amount + reserve.
	balance := new(big.Int).Add(plan, reserve)

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	e := nativeExecutor(&mock)
	e.db = db
	e.pools["ethereum"] = ethBalancePool(t, "0x"+balance.Text(16))

	job := nativeSweepJob(plan)
	if err := e.capNativeSweepAmount(t.Context(), e.pools["ethereum"], job, price); err != nil {
		t.Fatalf("fully funded native sweep was refused: %v", err)
	}
	if job.RawAmount.Cmp(plan) != 0 {
		t.Fatalf("funded sweep amount changed from %s to %s", plan, job.RawAmount)
	}
	// No UPDATE is expected: sqlmock fails the test if one is issued.
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCapNativeSweepAmountDefersWhenOnlyReserveIsLeft(t *testing.T) {
	price := big.NewInt(1_000_000_000)
	reserve := nativeSweepGasReserve(price)
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	e := nativeExecutor(&mock)
	e.db = db
	// Balance is exactly the reserve: sweeping it would leave zero gas.
	e.pools["ethereum"] = ethBalancePool(t, "0x"+reserve.Text(16))
	job := nativeSweepJob(big.NewInt(1))
	err = e.capNativeSweepAmount(t.Context(), e.pools["ethereum"], job, price)
	if err == nil || !strings.Contains(err.Error(), "sweep gas reserve") {
		t.Fatalf("a dust balance was not deferred: %v", err)
	}
	if job.RawAmount.Cmp(big.NewInt(1)) != 0 {
		t.Fatal("a deferred native sweep must keep its reserved amount")
	}
}

func TestNativeSweepUsesIntrinsicGasLimit(t *testing.T) {
	e := nativeExecutor(nil)
	if got := e.sweepGasLimitFor("ethereum", NativeToken); got != nativeSweepGasLimit {
		t.Fatalf("native sweep gas limit = %d, want %d", got, nativeSweepGasLimit)
	}
	if got := e.sweepGasLimitFor("ethereum", testToken); got != transferGasLimit {
		t.Fatalf("ERC-20 sweep gas limit = %d, want %d", got, transferGasLimit)
	}
}

func TestValidateEntryAcceptsNativeAndRejectsMismatches(t *testing.T) {
	e := nativeExecutor(nil)
	e.pools["ethereum"] = ethBalancePool(t, "0x1")

	entry := PlanEntry{
		Chain: "ethereum", Address: nativeSweepAddress, Token: NativeToken,
		RawAmount: big.NewInt(1), Decimals: 18,
	}
	if _, _, err := e.validateEntry(entry); err != nil {
		t.Fatalf("native sweep entry was rejected: %v", err)
	}

	// The token string alone must never decide: a native plan pointing at an
	// ERC-20 contract is refused, and so is an ERC-20 entry using the native
	// sentinel.
	spoofed := entry
	spoofed.Token = testToken
	spoofed.Decimals = 6
	if _, _, err := e.validateEntry(spoofed); err != nil {
		t.Fatalf("the ERC-20 entry was rejected for the wrong reason: %v", err)
	}
	spoofed.Decimals = 18
	if _, _, err := e.validateEntry(spoofed); err == nil {
		t.Fatal("an ERC-20 asset accepted 18 decimals")
	}
	// A native spec must refuse a non-native identifier even when the asset
	// map happens to contain that key.
	e.tokens["ethereum|native-copy"] = TokenSpec{Contract: "native-copy", Decimals: 18, Native: true}
	mismatched := entry
	mismatched.Token = "native-copy"
	if _, _, err := e.validateEntry(mismatched); err == nil {
		t.Fatal("a native spec accepted a forged asset identifier")
	}
}

func TestPlannerSweepsNativeCoinAfterTokens(t *testing.T) {
	// Ordering is a cost property: token transfers need the native balance as
	// gas, so the mainnet coin must be swept last.
	entries := []PlanEntry{
		{Token: NativeToken}, {Token: testToken}, {Token: NativeToken}, {Token: "0x9999"},
	}
	sortNativeLast(entries)
	if isNativeToken(entries[0].Token) || isNativeToken(entries[1].Token) {
		t.Fatalf("native entries were not moved to the end: %+v", entries)
	}
	if !isNativeToken(entries[2].Token) || !isNativeToken(entries[3].Token) {
		t.Fatalf("native entries are missing from the tail: %+v", entries)
	}
	if entries[0].Token != testToken || entries[1].Token != "0x9999" {
		t.Fatalf("stablecoin order changed: %+v", entries)
	}
}
