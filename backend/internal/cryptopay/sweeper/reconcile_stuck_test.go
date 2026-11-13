package sweeper

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

// A fixture hash, never a real transaction. All-zero would be indistinguishable
// from a missing value in the fixture maps, so a repeated byte is used instead.
const stuckFixtureHash = "0x" + "abababababababababababababababababababababababababababababababab"

// stuckJob is the record shape from the 2026-11-13 BSC incident: the chain has
// the transaction, the row still says sweep_pending and carries the redacted
// generic sentence because the real cause was unclassifiable.
func stuckJob() *sweepJob {
	return &sweepJob{
		PlanEntry: PlanEntry{Chain: "bsc", Address: "0x3333333333333333333333333333333333333333",
			DerivationIndex: 42, Token: testToken, RawAmount: big.NewInt(10_000_000), Decimals: 6},
		ID: 560, Version: 1, Destination: testDestination,
		Status:        "sweep_pending",
		Error:         "Payment operation failed; reconcile transaction status before retrying",
		Confirmations: 2, SweepNonce: "0", SweepRaw: "0x02", SweepHash: stuckFixtureHash,
	}
}

// expectMinedRecoveryCandidates is the read half of one recovery pass: the
// candidate query and the record itself, with no write expectation. Any write
// attempt then fails the test through an unexpected sqlmock call, so a clean
// return proves the record was left alone.
func expectMinedRecoveryCandidates(m sqlmock.Sqlmock, job *sweepJob) {
	m.ExpectQuery("SELECT id FROM sweep_records").
		WithArgs(int64(defaultMinedRecoveryAge/time.Second), minedRecoveryLimit).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(job.ID))
	m.ExpectQuery("SELECT id, chain, from_address").WillReturnRows(jobRows(job))
}

// expectMinedRecoveryRun wires one recovery pass over exactly one record whose
// conditional write reports affected.
func expectMinedRecoveryRun(m sqlmock.Sqlmock, job *sweepJob, affected int64) {
	expectMinedRecoveryCandidates(m, job)
	m.ExpectExec("UPDATE sweep_records").
		WithArgs(job.ID, job.SweepHash).
		WillReturnResult(sqlmock.NewResult(0, affected))
}

// blindRPCServer answers the chain id only: it knows no receipts and no blocks.
// It models the endpoint that made the rotating check stall - not broken, just
// behind (or answering JSON null) while a peer has the data.
func blindRPCServer(t *testing.T, chainID string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var result any
		if req.Method == "eth_chainId" {
			result = chainID
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": result})
	}))
	t.Cleanup(server.Close)
	return server
}

// A node that cannot serve the finality tag must not stop confirmation: the
// receipt, the canonical block and the configured depth are already proven, and
// the pending record is advanced to swept by the next run without an operator.
func TestMinedSweepRecoversWhenFinalityTagIsUnsupported(t *testing.T) {
	e, m, chain, _ := setupExecutor(t)
	chain.mined = true
	chain.head = 110
	chain.canonical = testBlockHash
	chain.finalityErr = true // the whole bug: a tag rejection used to fail the check
	chain.txs = map[string]bool{stuckFixtureHash: true}
	chain.blocks = map[string]uint64{stuckFixtureHash: 100} // depth 10, required 2
	job := stuckJob()
	expectMinedRecoveryRun(m, job, 1)

	recovered, err := e.recoverMinedSweeps(t.Context())
	if err != nil {
		t.Fatalf("recovery returned an error for a final transaction: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("recovered = %d, want 1: an unsupported finality tag must fall back to depth", recovered)
	}
}

// Two endpoints disagree: one has the receipt and the block, the other knows
// neither. The disagreement must not keep the record pending for ever.
func TestMinedSweepRecoversWhenEndpointsDisagree(t *testing.T) {
	for _, order := range []string{"healthy first", "blind first"} {
		t.Run(order, func(t *testing.T) {
			e, m, chain, _ := setupExecutor(t)
			chain.mined = true
			chain.head = 110
			chain.canonical = testBlockHash
			chain.txs = map[string]bool{stuckFixtureHash: true}
			chain.blocks = map[string]uint64{stuckFixtureHash: 100}
			healthy := e.pools["bsc"].Endpoints()[0]
			blind := blindRPCServer(t, "0x38")
			endpoints := healthy + "," + blind.URL
			if order == "blind first" {
				endpoints = blind.URL + "," + healthy
			}
			e.pools["bsc"] = rpc.NewPoolWithHTTPClient(endpoints, blind.Client())
			job := stuckJob()
			expectMinedRecoveryRun(m, job, 1)

			recovered, err := e.recoverMinedSweeps(t.Context())
			if err != nil {
				t.Fatalf("recovery returned an error: %v", err)
			}
			if recovered != 1 {
				t.Fatalf("recovered = %d, want 1: one endpoint's null must not veto the other's receipt", recovered)
			}
		})
	}
}

// The check must not turn a missing receipt or a short depth into a success.
func TestMinedSweepStaysPendingWithoutReceiptOrDepth(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*localChain)
	}{
		{"receipt absent", func(c *localChain) { c.mined = false }},
		{"depth short", func(c *localChain) { c.head = 101 }}, // one confirmation, two required
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, m, chain, _ := setupExecutor(t)
			chain.mined = true
			chain.head = 110
			chain.canonical = testBlockHash
			chain.txs = map[string]bool{stuckFixtureHash: true}
			chain.blocks = map[string]uint64{stuckFixtureHash: 100}
			tc.setup(chain)

			// No UPDATE expectation: any write attempt fails the test through an
			// unexpected sqlmock call, so err == nil proves nothing was written.
			expectMinedRecoveryCandidates(m, stuckJob())
			recovered, err := e.recoverMinedSweeps(t.Context())
			if err != nil || recovered != 0 {
				t.Fatalf("unproven transaction recovered: recovered=%d err=%v", recovered, err)
			}
			state, checkErr := checkReceipt(t.Context(), e.pools["bsc"], "bsc", stuckFixtureHash, 2)
			if checkErr != nil {
				t.Fatalf("observation failed: %v", checkErr)
			}
			if state != receiptWaiting && state != receiptAbsent {
				t.Fatalf("state = %v, want waiting or absent", state)
			}
		})
	}
}

// A reverted receipt is still a definitive observation, and it must be reported
// as reverted rather than confirmed so the record is parked, never swept.
func TestMinedSweepRevertedIsNotConfirmed(t *testing.T) {
	e, m, chain, _ := setupExecutor(t)
	chain.mined = true
	chain.head = 110
	chain.canonical = testBlockHash
	chain.status = "0x0"
	chain.txs = map[string]bool{stuckFixtureHash: true}
	chain.blocks = map[string]uint64{stuckFixtureHash: 100}

	state, err := checkReceipt(t.Context(), e.pools["bsc"], "bsc", stuckFixtureHash, 2)
	if err != nil || state != receiptReverted {
		t.Fatalf("reverted receipt: state=%v err=%v", state, err)
	}
	expectMinedRecoveryCandidates(m, stuckJob())
	recovered, err := e.recoverMinedSweeps(t.Context())
	if err != nil || recovered != 0 {
		t.Fatalf("a reverted sweep must not be recovered as swept: recovered=%d err=%v", recovered, err)
	}
}

// The log must name the reason, and it must never carry the endpoint URL or a
// credential embedded in it. The durable record stays the fixed safe sentence.
func TestReceiptObservationLogsReasonWithoutSecrets(t *testing.T) {
	const secret = "TOP-SECRET-API-KEY"
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	e, _, chain, _ := setupExecutor(t)
	chain.mined = true
	chain.head = 110
	chain.canonical = testBlockHash
	chain.finalityErr = true
	chain.txs = map[string]bool{stuckFixtureHash: true}
	chain.blocks = map[string]uint64{stuckFixtureHash: 100}
	// The configured endpoint carries the credential in its path.
	pool := rpc.NewPoolWithHTTPClient(e.pools["bsc"].Endpoints()[0]+"/"+secret, http.DefaultClient)
	state, err := checkReceipt(t.Context(), pool, "bsc", stuckFixtureHash, 2)
	if err != nil || state != receiptConfirmed {
		t.Fatalf("state=%v err=%v", state, err)
	}
	logged := buf.String()
	if !strings.Contains(logged, reasonFinalityMissing) {
		t.Fatalf("log does not name the unavailable finality tag: %s", logged)
	}
	if !strings.Contains(logged, reasonReceiptConfirmed) {
		t.Fatalf("log does not record the confirmation: %s", logged)
	}
	if strings.Contains(logged, secret) || strings.Contains(logged, "://") {
		t.Fatalf("log leaked the endpoint URL or its credential: %s", logged)
	}
}

// An unreadable endpoint must still yield a diagnosable reason in the log and
// a classified failure for the record, never the generic operation sentence.
func TestReceiptObservationRPCFailureStaysDiagnosable(t *testing.T) {
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	e, _, chain, _ := setupExecutor(t)
	chain.receiptError = true
	state, err := checkReceipt(t.Context(), e.pools["bsc"], "bsc", stuckFixtureHash, 2)
	if err == nil {
		t.Fatalf("an unreadable endpoint must not look like a verdict: state=%v", state)
	}
	logged := buf.String()
	if !strings.Contains(logged, reasonRPCFailure) {
		t.Fatalf("log does not name the RPC failure: %s", logged)
	}
	if strings.Contains(logged, "://") {
		t.Fatalf("log leaked an endpoint URL: %s", logged)
	}
	recorded := rpc.SafeRecordedError(rpc.SafeError(err))
	if recorded == "Payment operation failed; reconcile transaction status before retrying" {
		t.Fatalf("the real cause was redacted into the generic sentence: %q", recorded)
	}
}

// A row that moved on while the pass read the chain must keep its newer state:
// the write is conditional on the status and the hash it observed.
func TestMinedSweepRecoveryWriteIsConditional(t *testing.T) {
	e, m, chain, _ := setupExecutor(t)
	chain.mined = true
	chain.head = 110
	chain.canonical = testBlockHash
	chain.txs = map[string]bool{stuckFixtureHash: true}
	chain.blocks = map[string]uint64{stuckFixtureHash: 100}
	job := stuckJob()
	// Another writer already reconciled the row before this write: zero rows.
	expectMinedRecoveryRun(m, job, 0)

	recovered, err := e.recoverMinedSweeps(t.Context())
	if err != nil || recovered != 0 {
		t.Fatalf("a conditional write that matched nothing must not count: recovered=%d err=%v", recovered, err)
	}
}
