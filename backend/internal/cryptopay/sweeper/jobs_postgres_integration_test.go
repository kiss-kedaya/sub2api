//go:build integration

package sweeper

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
)

// These tests require an explicitly created disposable local cluster. Never
// fall back to the application's database or to ambient PG* environment vars.
func isolatedSweepPostgres(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("CRYPTO_SWEEP_TEST_DSN")
	if dsn == "" {
		t.Skip("set CRYPTO_SWEEP_TEST_DSN for the disposable local review cluster")
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Hostname() != "127.0.0.1" || u.Port() != "15439" ||
		u.User == nil || u.User.Username() != "crypto_review" || u.Path != "/postgres" ||
		u.Query().Get("sslmode") != "disable" || len(u.Query()) != 1 {
		t.Fatal("integration DSN must name the disposable 127.0.0.1:15439 crypto_review cluster")
	}
	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "crypto_sweep_it_" + strings.ToLower(strings.ReplaceAll(t.Name(), "/", "_"))
	// Test names exceed PostgreSQL's identifier limit; a unique timestamp also
	// allows independent test processes without touching one another's tables.
	schema = schema[:20] + time.Now().Format("20060102150405.000000000")
	quoted := pq.QuoteIdentifier(schema)
	if _, err := admin.ExecContext(t.Context(), "CREATE SCHEMA "+quoted); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	query := u.Query()
	query.Set("search_path", schema)
	u.RawQuery = query.Encode()
	db, err := sql.Open("postgres", u.String())
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(5)
	t.Cleanup(func() {
		_ = db.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(ctx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error(err)
		}
		_ = admin.Close()
	})
	applySweepMigration(t, db, "261_crypto_deposit.sql")
	applySweepMigration(t, db, "266_crypto_sweep_jobs.sql")
	applySweepMigration(t, db, "266_crypto_sweep_jobs.sql")
	return db
}

func applySweepMigration(t *testing.T, db *sql.DB, name string) {
	t.Helper()
	script, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", name))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), string(script)); err != nil {
		t.Fatalf("migration %s: %v", name, err)
	}
}

func TestPostgresSweepPersistsExactAmountBeforeBroadcast(t *testing.T) {
	db := isolatedSweepPostgres(t)
	e, _, chain, entry := setupExecutor(t)
	e.db = db
	e.config.MinPerAddress = map[string]float64{"bsc": 1}
	e.reader = &fakeReader{balances: map[string][]AddressBalance{"bsc": {{
		Chain: entry.Chain, Address: entry.Address, DerivationIndex: entry.DerivationIndex,
		Token: entry.Token, Raw: entry.RawAmount, Decimals: entry.Decimals,
	}}}}
	chain.native = 0 // exercise both durable gas and token transactions
	var observedGas, observedSweep bool
	chain.beforeSend = func() {
		var status, gasRaw, sweepRaw string
		if err := db.QueryRowContext(t.Context(), `SELECT status, gas_raw_tx, sweep_raw_tx FROM sweep_records`).
			Scan(&status, &gasRaw, &sweepRaw); err != nil {
			t.Error(err)
			return
		}
		switch status {
		case "gas_pending":
			observedGas = gasRaw != "" && sweepRaw == ""
		case "sweep_pending":
			observedSweep = gasRaw != "" && sweepRaw != ""
		default:
			t.Errorf("broadcast without a committed pending job: %s", status)
		}
	}
	summary, err := e.Run(t.Context(), false)
	if err != nil || summary.Swept != 1 || summary.Pending != 0 {
		t.Fatalf("summary=%+v err=%v", summary, err)
	}
	var status, rawAmount, amount, gasNonce string
	var gasConfirmed bool
	if err := db.QueryRowContext(t.Context(), `SELECT status,raw_amount,amount::text,gas_nonce,gas_confirmed FROM sweep_records`).
		Scan(&status, &rawAmount, &amount, &gasNonce, &gasConfirmed); err != nil {
		t.Fatal(err)
	}
	if !observedGas || !observedSweep || status != "swept" || rawAmount != "10000000" ||
		amount != "10.000000000000000000" || gasNonce != "7" || !gasConfirmed {
		t.Fatalf("durability/ledger mismatch: gas=%v sweep=%v status=%s raw=%s amount=%s nonce=%s confirmed=%v",
			observedGas, observedSweep, status, rawAmount, amount, gasNonce, gasConfirmed)
	}
	chain.mu.Lock()
	defer chain.mu.Unlock()
	if len(chain.signs) != 2 || chain.signs[0].Index != 9 || chain.signs[1].Amount != "10000000" {
		t.Fatalf("unexpected signatures: %+v", chain.signs)
	}
}

func TestPostgresManualSweepWaitCompletesAfterDelayedGasAndTokenReceipts(t *testing.T) {
	db := isolatedSweepPostgres(t)
	e, _, chain, entry := setupExecutor(t)
	e.db = db
	e.config.MinPerAddress = map[string]float64{"bsc": 1}
	e.reader = &fakeReader{balances: map[string][]AddressBalance{"bsc": {{
		Chain: entry.Chain, Address: entry.Address, DerivationIndex: entry.DerivationIndex,
		Token: entry.Token, Raw: entry.RawAmount, Decimals: entry.Decimals,
	}}}}
	chain.native, chain.mined = 0, false
	chain.beforeSend = func() {
		chain.mined = false
		go func() {
			time.Sleep(20 * time.Millisecond)
			chain.mu.Lock()
			defer chain.mu.Unlock()
			chain.mined, chain.native = true, 1_000_000_000
		}()
	}
	summary, err := e.RunAndWait(t.Context(), false)
	if err != nil || summary.Swept != 1 || summary.Pending != 0 || summary.Failed != 0 {
		t.Fatalf("manual operation stopped before confirming gas/token: %+v %v", summary, err)
	}
	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM sweep_records
		WHERE status='swept' AND gas_confirmed AND gas_raw_tx<>'' AND sweep_raw_tx<>''
		AND raw_amount='10000000' AND amount=10`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("missing exact confirmed sweep: count=%d err=%v", count, err)
	}
	chain.mu.Lock()
	defer chain.mu.Unlock()
	if len(chain.signs) != 2 || len(chain.raws) != 2 {
		t.Fatalf("duplicate spending: signatures=%d broadcasts=%d", len(chain.signs), len(chain.raws))
	}
}

func TestPostgresUnknownBroadcastResumesIdenticalTransaction(t *testing.T) {
	db := isolatedSweepPostgres(t)
	e, _, chain, entry := setupExecutor(t)
	e.db = db
	chain.sendUnknown, chain.mined = true, false
	if out, err := e.sweepOne(t.Context(), entry, false); err == nil || out.Status != "pending" {
		t.Fatalf("unknown broadcast must remain pending: %+v %v", out, err)
	}
	chain.mu.Lock()
	chain.sendUnknown = false
	chain.mu.Unlock()
	restarted := *e
	restarted.wallet = nil // a persisted job must never need another signature
	summary, err := restarted.Run(t.Context(), false)
	if err != nil || summary.Pending != 1 || summary.Swept != 0 {
		t.Fatalf("pending recovery: %+v %v", summary, err)
	}
	chain.mu.Lock()
	if len(chain.signs) != 1 || len(chain.raws) != 2 || chain.raws[0] != chain.raws[1] {
		t.Errorf("restart replaced signed bytes: signs=%d broadcasts=%d", len(chain.signs), len(chain.raws))
	}
	chain.mined = true
	chain.mu.Unlock()
	summary, err = restarted.Run(t.Context(), false)
	if err != nil || summary.Swept != 1 || summary.Pending != 0 {
		t.Fatalf("confirmed recovery: %+v %v", summary, err)
	}
	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM sweep_records WHERE status='swept'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("completed jobs=%d err=%v", count, err)
	}
}

func TestPostgresSweepLockExcludesSecondExecutorAndReleasesOnCancel(t *testing.T) {
	db := isolatedSweepPostgres(t)
	e := &Executor{db: db}
	ctx, cancel := context.WithCancel(t.Context())
	_, release, err := e.lock(ctx)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	defer func() {
		if release != nil {
			_ = release()
		}
	}()
	second := &Executor{db: db}
	if _, unexpectedRelease, err := second.lock(t.Context()); err == nil {
		_ = unexpectedRelease()
		t.Fatal("second executor acquired an already held database lock")
	}
	cancel()
	if err := release(); err != nil {
		t.Fatal(err)
	}
	release = nil
	_, nextRelease, err := second.lock(t.Context())
	if err != nil {
		t.Fatalf("cancelled owner leaked advisory lock: %v", err)
	}
	if err := nextRelease(); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresSweepConstraintsAndLegacyUpgrade(t *testing.T) {
	db := isolatedSweepPostgres(t)
	if _, err := db.ExecContext(t.Context(), `INSERT INTO sweep_records(chain,from_address,to_address,token,amount,status,sweep_tx_hash)
		VALUES ('bsc','legacy-pending','target','token',10,'failed','known-hash'),
		       ('bsc','legacy-success','target','token',20,'swept','success-hash')`); err != nil {
		t.Fatal(err)
	}
	applySweepMigration(t, db, "266_crypto_sweep_jobs.sql")
	var pending, successful string
	if err := db.QueryRowContext(t.Context(), `SELECT status FROM sweep_records WHERE from_address='legacy-pending'`).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(t.Context(), `SELECT status FROM sweep_records WHERE from_address='legacy-success'`).Scan(&successful); err != nil {
		t.Fatal(err)
	}
	if pending != "manual_review" || successful != "swept" {
		t.Fatalf("legacy conversion changed accounting: pending=%s successful=%s", pending, successful)
	}
	e, _, _, entry := setupExecutor(t)
	e.db = db
	job, err := e.createJob(t.Context(), entry)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.createJob(t.Context(), entry); err == nil {
		t.Fatal("duplicate active job accepted")
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE sweep_records SET status='swept' WHERE id=$1`, job.ID); err == nil {
		t.Fatal("swept state without committed transaction accepted")
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE sweep_records SET raw_amount='0' WHERE id=$1`, job.ID); err == nil {
		t.Fatal("zero immutable amount accepted")
	}
}

func TestPostgresUnsignedTokenRefreshPreservesConfirmedGasTransaction(t *testing.T) {
	db := isolatedSweepPostgres(t)
	e, _, chain, entry := setupExecutor(t)
	e.db = db
	e.reader = currentReaderFor(entry, 12_000_000)
	created, err := e.createJob(t.Context(), entry)
	if err != nil {
		t.Fatal(err)
	}
	job := signedJob(chain, entry, true)
	job.ID, job.Revision = created.ID, created.Revision
	if err := e.saveJob(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	chain.txs[job.GasHash] = true // the previously authorized gas payment is mined
	originalGas := job.GasRaw
	summary, err := e.Run(t.Context(), false)
	if err != nil || summary.Swept != 1 || summary.Pending != 0 {
		t.Fatalf("could not resume the latest credited amount: %+v %v", summary, err)
	}
	var raw, amount, gasRaw, gasNonce string
	if err := db.QueryRowContext(t.Context(), `SELECT raw_amount,amount::text,gas_raw_tx,gas_nonce
		FROM sweep_records WHERE id=$1`, job.ID).Scan(&raw, &amount, &gasRaw, &gasNonce); err != nil {
		t.Fatal(err)
	}
	if raw != "12000000" || amount != "12.000000000000000000" || gasRaw != originalGas || gasNonce != "7" {
		t.Fatalf("amount/gas mismatch: raw=%s amount=%s gas unchanged=%v nonce=%s", raw, amount, gasRaw == originalGas, gasNonce)
	}
	if len(chain.signs) != 1 || chain.signs[0].Kind != "sweep" || chain.signs[0].Amount != "12000000" {
		t.Fatalf("old gas was repaid or token amount stayed stale: %+v", chain.signs)
	}
}

func TestPostgresAmountRefreshCannotOverwriteAConcurrentTokenSignature(t *testing.T) {
	db := isolatedSweepPostgres(t)
	e, _, chain, entry := setupExecutor(t)
	e.db = db
	e.reader = currentReaderFor(entry, 12_000_000)
	job, err := e.createJob(t.Context(), entry)
	if err != nil {
		t.Fatal(err)
	}
	signed := signedJob(chain, entry, false)
	// Leave the revision equal to the stale reader's value to exercise the
	// signed-payload guard independently of the ordinary revision check.
	_, err = db.ExecContext(t.Context(), `UPDATE sweep_records SET
		status='sweep_pending',sweep_raw_tx=$2,sweep_tx_hash=$3,sweep_nonce=$4 WHERE id=$1`,
		job.ID, signed.SweepRaw, signed.SweepHash, signed.SweepNonce)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.refreshUnsignedAmount(t.Context(), job); err == nil {
		t.Fatal("a concurrent token signature did not block the amount rewrite")
	}
	var raw, signedRaw string
	if err := db.QueryRowContext(t.Context(), `SELECT raw_amount,sweep_raw_tx FROM sweep_records WHERE id=$1`, job.ID).
		Scan(&raw, &signedRaw); err != nil {
		t.Fatal(err)
	}
	if raw != "10000000" || signedRaw != signed.SweepRaw || len(chain.signs) != 0 || len(chain.raws) != 0 {
		t.Fatal("amount refresh modified signed intent or sent a transaction")
	}
}
