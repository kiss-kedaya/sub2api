package sweeper

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/wallet"
)

// captureStore records the statements a job writes, so a test can assert on the
// SQL itself: which columns a failure may touch is the whole point.
type captureStore struct {
	mu    sync.Mutex
	execs []capturedStatement
}

type capturedStatement struct {
	query string
	args  []any
}

func (s *captureStore) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.execs = append(s.execs, capturedStatement{query: query, args: args})
	return driver.RowsAffected(1), nil
}

func (s *captureStore) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, errors.New("captureStore: unexpected query")
}

func (s *captureStore) QueryRowContext(context.Context, string, ...any) *sql.Row { return nil }

// captureLogs routes slog to a buffer at ERROR level, which is what
// LOG_LEVEL=error leaves in production: a line that is not in the buffer would
// not have survived.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelError})))
	t.Cleanup(func() {
		slog.SetDefault(previous)
		// SetDefault also redirects the standard log package; put that back too.
		log.SetOutput(os.Stderr)
		log.SetFlags(log.LstdFlags)
	})
	return &buf
}

func logLines(t *testing.T, buf *bytes.Buffer, msg string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var fields map[string]any
		if err := json.Unmarshal([]byte(line), &fields); err != nil {
			t.Fatalf("log line is not JSON: %q", line)
		}
		if fields["msg"] == msg {
			out = append(out, fields)
		}
	}
	return out
}

// resetFailureLog gives the test its own limiter and clock.
func resetFailureLog(t *testing.T, now func() time.Time) {
	t.Helper()
	previous := jobFailureLog
	jobFailureLog = &failureLimiter{seen: map[string]time.Time{}, now: now}
	t.Cleanup(func() { jobFailureLog = previous })
}

func failedJob(status string, revision int64) *sweepJob {
	return &sweepJob{PlanEntry: PlanEntry{Chain: "polygon", Address: testDestination, Token: testToken},
		ID: 1338, Version: 1, Revision: revision, Status: status}
}

// Job #1338 in production sat at gas_confirmed with an EMPTY error column and an
// unchanged revision: a transient failure was designed not to be persisted at
// all, because saveJob bumps revision and revision is the attempt counter. The
// reason is now written, by a statement that touches neither revision nor
// updated_at nor status.
func TestTransientFailureRecordsItsReasonWithoutSpendingAnAttempt(t *testing.T) {
	resetFailureLog(t, time.Now)
	store := &captureStore{}
	e := &Executor{store: store}
	job := failedJob("gas_confirmed", 3)
	cause := atStep(StepBalanceRead, rpc.NewTransportFailure(errors.New("fixture")))

	out, err := e.jobError(t.Context(), job, cause)

	if err == nil || out.Status != "pending" {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	if out.Step != StepBalanceRead || out.Category != rpc.CategoryTransport || out.Error != "RPC connection failed" {
		t.Fatalf("the entry does not carry the reason code: %+v", out)
	}
	if len(store.execs) != 1 {
		t.Fatalf("expected exactly the reason write, got %d statements", len(store.execs))
	}
	statement := store.execs[0]
	for _, forbidden := range []string{"revision=revision", "updated_at", "status=", "gas_", "sweep_raw", "sweep_tx_hash"} {
		if strings.Contains(statement.query, forbidden) {
			t.Fatalf("the reason write must not touch %q: %s", forbidden, statement.query)
		}
	}
	for _, required := range []string{"SET error=$2", "revision=$3", "job_version=1", "NOT IN ('swept','failed')"} {
		if !strings.Contains(statement.query, required) {
			t.Fatalf("the reason write must be guarded by %q: %s", required, statement.query)
		}
	}
	if len(statement.args) != 3 || statement.args[0] != int64(1338) ||
		statement.args[1] != "balance_read: RPC connection failed" || statement.args[2] != int64(3) {
		t.Fatalf("args = %#v", statement.args)
	}
	if job.Revision != 3 || job.Status != "gas_confirmed" {
		t.Fatalf("the job state moved: revision=%d status=%s", job.Revision, job.Status)
	}
	// The same failure again writes nothing: the reason is already there.
	if _, err := e.jobError(t.Context(), job, cause); err == nil || len(store.execs) != 1 {
		t.Fatalf("a repeat of the same reason wrote again: %d statements", len(store.execs))
	}
	// And the text the record now holds survives the read-side sanitiser.
	if got := rpc.SafeRecordedError(job.Error); got != job.Error {
		t.Fatalf("the stored reason is rewritten on read: %q", got)
	}
}

// A locked signer says nothing about the payment either, and fails every job the
// same way: the reason is recorded and no attempt is spent.
func TestLockedSignerIsRecordedNotCountedAsAnAttempt(t *testing.T) {
	resetFailureLog(t, time.Now)
	store := &captureStore{}
	e := &Executor{store: store}
	job := failedJob("gas_confirmed", 7)
	out, err := e.jobError(t.Context(), job, atStep(StepSign, wallet.ErrSignerShareRequired))

	if !IsFatalRunError(err) {
		t.Fatalf("the run must be told this is fatal: %v", err)
	}
	if out.Step != StepSign || out.Category != CategoryWallet {
		t.Fatalf("out=%+v", out)
	}
	if len(store.execs) != 1 || strings.Contains(store.execs[0].query, "revision=revision") {
		t.Fatalf("a locked signer spent an attempt: %+v", store.execs)
	}
	if got := store.execs[0].args[1]; got != "sign: wallet signer is locked; a recovery shard is required" {
		t.Fatalf("the operator is told %q", got)
	}
}

// ForChain reports "no endpoint verified" as a plain error, so it used to be
// persisted as a failed attempt with the generic sentence on every pass.
func TestUnverifiedChainEndpointsDoNotSpendAnAttempt(t *testing.T) {
	resetFailureLog(t, time.Now)
	store := &captureStore{}
	e := &Executor{store: store}
	job := failedJob("planned", 0)
	out, err := e.jobError(t.Context(), job, atStep(StepValidate, errChainUnverified))
	if err == nil || out.Step != StepValidate || out.Category != CategoryEndpoints || out.Error != errChainUnverified.Error() {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	if len(store.execs) != 1 || strings.Contains(store.execs[0].query, "revision=revision") {
		t.Fatalf("an unreachable chain spent an attempt: %+v", store.execs)
	}
	if got := store.execs[0].args[1]; got != "validate: no RPC endpoint could be verified for the chain" {
		t.Fatalf("recorded %q", got)
	}
}

// The other half of the contract: a failure that IS a verdict on the payment
// still spends an attempt, still persists through saveJob, and still gives up.
func TestAFailureThatIsAVerdictStillSpendsAnAttempt(t *testing.T) {
	resetFailureLog(t, time.Now)
	store := &captureStore{}
	e := &Executor{store: store}
	job := failedJob("gas_confirmed", 3)
	out, err := e.jobError(t.Context(), job, atStep(StepValidate, errors.New("invalid sweep plan")))
	if err == nil || out.Step != StepValidate || out.Category != CategoryOther {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	if len(store.execs) != 1 || !strings.Contains(store.execs[0].query, "revision=revision+1") {
		t.Fatalf("a real failure must go through saveJob: %+v", store.execs)
	}
	if job.Revision != 4 {
		t.Fatalf("revision = %d, want 4", job.Revision)
	}
}

// Failures are logged at ERROR, with the reason code and the failing host, and
// never with a URL, a credential or the node's own words.
func TestJobFailureLogSurvivesLogLevelErrorAndLeaksNothing(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	resetFailureLog(t, func() time.Time { return now })
	buf := captureLogs(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("fixture-node-secret-body"))
	}))
	t.Cleanup(server.Close)
	pool := rpc.NewPoolWithHTTPClient(server.URL+"/fixture-secret-path?api_key=fixture-secret-key", server.Client())
	_, failure := pool.Nonce(t.Context(), testDestination)
	if failure == nil {
		t.Fatal("the fixture node must fail")
	}
	cause := atStep(StepNonce, failure)

	e := &Executor{store: &captureStore{}}
	job := failedJob("gas_confirmed", 3)
	for i := 0; i < 3; i++ {
		_, _ = e.jobError(t.Context(), job, cause)
	}
	lines := logLines(t, buf, "[cryptopay] sweep job could not advance")
	if len(lines) != 1 {
		t.Fatalf("a retry loop must log once per window, got %d lines:\n%s", len(lines), buf.String())
	}
	line := lines[0]
	if line["level"] != "ERROR" || line["job_id"] != float64(1338) || line["chain"] != "polygon" ||
		line["step"] != StepNonce || line["category"] != rpc.CategoryTransport ||
		line["endpoint_host"] != "127.0.0.1" || line["status"] != "gas_confirmed" {
		t.Fatalf("log line lacks the reason code: %v", line)
	}
	for _, secret := range []string{"fixture-secret-path", "fixture-secret-key", "fixture-node-secret-body", server.URL, "api_key"} {
		if strings.Contains(buf.String(), secret) {
			t.Fatalf("the log leaked %q:\n%s", secret, buf.String())
		}
	}
	// Once the window has passed, the still-failing job is logged again.
	now = now.Add(failureLogEvery + time.Second)
	_, _ = e.jobError(t.Context(), job, cause)
	if got := len(logLines(t, buf, "[cryptopay] sweep job could not advance")); got != 2 {
		t.Fatalf("a failure that is still happening must be logged again, got %d lines", got)
	}
}

// A job that fails for real (a verdict) keeps its own ERROR line, now with the
// reason code as well.
func TestVerdictFailureLogCarriesTheReasonCode(t *testing.T) {
	resetFailureLog(t, time.Now)
	buf := captureLogs(t)
	e := &Executor{store: &captureStore{}}
	_, _ = e.jobError(t.Context(), failedJob("gas_confirmed", 1), atStep(StepSign, errors.New("signed transaction does not match reserved chain/nonce/gas")))
	lines := logLines(t, buf, "[cryptopay] sweep job failed")
	if len(lines) != 1 || lines[0]["step"] != StepSign || lines[0]["category"] != CategoryOther {
		t.Fatalf("lines=%v", lines)
	}
}

func twoChainExecutor(t *testing.T) (*Executor, sqlmock.Sqlmock, *localChain, PlanEntry) {
	t.Helper()
	e, m, c, entry := setupExecutor(t)
	// Polygon's endpoint is down: it answers 503 to everything.
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	t.Cleanup(down.Close)
	e.pools["polygon"] = rpc.NewPoolWithHTTPClient(down.URL, down.Client())
	e.config.Confirmations["polygon"] = 2
	return e, m, c, entry
}

// One chain's trouble must not stop another chain in the same pass, and the pass
// must say which job failed where.
func TestRunKeepsOtherChainsGoingAfterAJobFailure(t *testing.T) {
	resetFailureLog(t, time.Now)
	e, m, c, entry := twoChainExecutor(t)
	polygon := &sweepJob{PlanEntry: PlanEntry{Chain: "polygon", Address: testDestination, Token: testToken,
		RawAmount: entry.RawAmount, Decimals: 6}, ID: 1, Version: 1, Revision: 0, Destination: testDestination,
		Status: "planned", Confirmations: 2}
	bsc := signedJob(c, entry, false)
	bsc.ID = 2
	c.txs[bsc.SweepHash] = true

	expectRunStart(m, polygon, bsc)
	// Polygon's reason is recorded without a revision bump...
	m.ExpectExec(`UPDATE sweep_records SET error=\$2\s+WHERE id=\$1 AND job_version=1 AND revision=\$3`).
		WithArgs(int64(1), "validate: no RPC endpoint could be verified for the chain", int64(0)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	// ...and BSC, which comes after it, is swept in the same pass.
	expectWriteID(m, 2, "swept", nil, nil)
	expectUnlock(m)

	summary, err := e.Run(t.Context(), false)
	if err == nil {
		t.Fatal("the polygon failure must still be reported")
	}
	if IsFatalRunError(err) {
		t.Fatalf("an unreachable chain is not fatal: %v", err)
	}
	if summary.Swept != 1 || summary.Advanced != 1 || len(summary.Entries) != 2 {
		t.Fatalf("summary=%+v", summary)
	}
	first, second := summary.Entries[0], summary.Entries[1]
	if first.JobID != 1 || first.Status != "pending" || first.Step != StepValidate ||
		first.Category != CategoryEndpoints || !first.NeedsAttention() {
		t.Fatalf("polygon entry=%+v", first)
	}
	if second.JobID != 2 || second.Status != "swept" || second.Step != "" || second.NeedsAttention() {
		t.Fatalf("bsc entry=%+v", second)
	}
	if len(c.signs) != 0 || len(c.raws) != 0 {
		t.Fatalf("the already-broadcast sweep was signed or sent again: signs=%d sends=%d", len(c.signs), len(c.raws))
	}
}

// The end-to-end version of job #1338: a gas-confirmed job whose token balance
// read fails with an HTTP 503 now leaves its reason in the record, and the pass
// reports the same code.
func TestRunRecordsWhereAGasConfirmedJobStopped(t *testing.T) {
	resetFailureLog(t, time.Now)
	e, m, c, entry := setupExecutor(t)
	c.callError = true
	job := &sweepJob{PlanEntry: entry, ID: 1, Version: 1, Revision: 0, Destination: testDestination,
		Status: "gas_confirmed", Confirmations: 2, GasConfirmed: true}
	expectRunStart(m, job)
	m.ExpectQuery("SELECT updated_at").WithArgs(int64(1)).WillReturnRows(sqlmock.NewRows([]string{"updated_at"}).AddRow(time.Now()))
	m.ExpectExec(`UPDATE sweep_records SET error=\$2\s+WHERE id=\$1`).
		WithArgs(int64(1), "balance_read: RPC HTTP request failed (status 503)", int64(0)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	expectUnlock(m)

	summary, err := e.Run(t.Context(), false)
	if err == nil || summary.Advanced != 0 || summary.Pending != 1 {
		t.Fatalf("summary=%+v err=%v", summary, err)
	}
	got := summary.Entries[0]
	if got.Step != StepBalanceRead || got.Category != rpc.CategoryTransport || got.Error != "RPC HTTP request failed (status 503)" {
		t.Fatalf("entry=%+v", got)
	}
	if len(c.signs) != 0 || len(c.raws) != 0 {
		t.Fatal("a failed read must not sign or send anything")
	}
}

// A locked signer in the middle of a pass is reported as fatal so the drain
// stops, and the pass itself signs and sends nothing.
func TestRunReportsALockedSignerAsFatal(t *testing.T) {
	resetFailureLog(t, time.Now)
	e, m, c, entry := setupExecutor(t)
	fixture, ok := e.wallet.(*fixtureWallet)
	if !ok {
		t.Fatal("unexpected wallet fixture")
	}
	e.wallet = lockedWallet{fixture}
	job := &sweepJob{PlanEntry: entry, ID: 1, Version: 1, Revision: 0, Destination: testDestination,
		Status: "gas_confirmed", Confirmations: 2, GasConfirmed: true}
	expectRunStart(m, job)
	m.ExpectQuery("SELECT updated_at").WithArgs(int64(1)).WillReturnRows(sqlmock.NewRows([]string{"updated_at"}).AddRow(time.Now()))
	expectAddressClear(m, entry)
	m.ExpectExec(`UPDATE sweep_records SET error=\$2\s+WHERE id=\$1`).
		WithArgs(int64(1), "sign: wallet signer is locked; a recovery shard is required", int64(0)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	expectUnlock(m)

	summary, err := e.Run(t.Context(), false)
	if !IsFatalRunError(err) {
		t.Fatalf("err=%v", err)
	}
	if summary.Advanced != 0 || len(c.signs) != 0 || len(c.raws) != 0 {
		t.Fatalf("summary=%+v signs=%d sends=%d", summary, len(c.signs), len(c.raws))
	}
	if got := summary.Entries[0]; got.Step != StepSign || got.Category != CategoryWallet {
		t.Fatalf("entry=%+v", got)
	}
}

type lockedWallet struct{ *fixtureWallet }

func (lockedWallet) SignEVM(context.Context, EVMSignRequest) (string, error) {
	return "", wallet.ErrSignerShareRequired
}

// A job created from the plan in this pass counts as advanced as soon as its
// first transaction exists, even before anything confirms.
func TestRunCountsAJobCreatedFromThePlanAsAdvancedOnceItHasATransaction(t *testing.T) {
	e, m, c, entry := setupExecutor(t)
	c.native, c.mined = 0, false // the top-up is sent but never confirms
	e.reader = planFor(entry)
	e.config.MinPerAddress = map[string]float64{"bsc": 1}
	expectRunStart(m)
	expectInsertAccepted(m, 1)
	expectAddressClear(m, entry)
	m.ExpectQuery(`SELECT EXISTS .*gas_payer`).WithArgs("bsc", int64(1), c.payer).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	expectWrite(m, "gas_pending", nil, nil) // the signed top-up is persisted before it is sent
	expectWrite(m, "gas_pending", nil, nil) // and the wait for its receipt is recorded
	expectUnlock(m)

	summary, err := e.Run(t.Context(), false)
	if err != nil || summary.Advanced != 1 || summary.Swept != 0 || summary.Pending != 1 {
		t.Fatalf("summary=%+v err=%v", summary, err)
	}
	if got := summary.Entries[0]; got.GasTxHash == "" || got.NeedsAttention() || !got.Waiting() {
		t.Fatalf("a top-up waiting for its receipt is a wait, not a failure: %+v", got)
	}
}

// A pass counts a job as advanced only when something moved forward.
func TestRunCountsOnlyForwardMovesAsAdvanced(t *testing.T) {
	for _, tc := range []struct {
		name         string
		head         uint64
		wantStatus   string
		wantSwept    int
		wantAdvanced int
	}{
		{"still waiting for depth", 101, "sweep_pending", 0, 0},
		{"landed", 103, "swept", 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, m, c, entry := setupExecutor(t)
			c.head = tc.head
			j := signedJob(c, entry, false)
			c.txs[j.SweepHash] = true
			expectRunStart(m, j)
			expectWrite(m, tc.wantStatus, nil, nil)
			expectUnlock(m)
			summary, err := e.Run(t.Context(), false)
			if err != nil || summary.Swept != tc.wantSwept || summary.Advanced != tc.wantAdvanced {
				t.Fatalf("summary=%+v err=%v", summary, err)
			}
			if tc.wantAdvanced == 0 && (summary.Pending != 1 || summary.Entries[0].NeedsAttention()) {
				t.Fatalf("a wait for confirmations is pending and needs no attention: %+v", summary.Entries[0])
			}
		})
	}
}
