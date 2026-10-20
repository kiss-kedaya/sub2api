package scanner

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

// This in-process driver models PostgreSQL SESSION locks, including reentrant
// acquisition and automatic release on physical disconnect. Separate sql.DB
// pools share only the mock server, just as separate instances share PostgreSQL.
// No DSN, environment variable, network database or production config is used.
type scannerMockPG struct {
	mu               sync.Mutex
	nextSession      int
	holder           int
	depth            int
	events           []scannerPGEvent
	closed           []int
	acquire, release *scannerPGReply
	beforeWork       func(context.Context, string) error
	openErr          error
}

type scannerPGReply struct {
	value driver.Value
	err   error
	empty bool
}

type scannerPGEvent struct {
	step, query   string
	session       int
	ctxErr        error
	cleanupBudget time.Duration
}

func (p *scannerMockPG) Connect(ctx context.Context) (driver.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.openErr != nil {
		return nil, p.openErr
	}
	p.nextSession++
	return &scannerPGConn{pg: p, session: p.nextSession}, nil
}

func (p *scannerMockPG) Driver() driver.Driver { return scannerPGDriver{pg: p} }

type scannerPGDriver struct{ pg *scannerMockPG }

func (d scannerPGDriver) Open(string) (driver.Conn, error) {
	return d.pg.Connect(context.Background())
}

type scannerPGConn struct {
	pg      *scannerMockPG
	session int
}

func (c *scannerPGConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected Prepare")
}

func (c *scannerPGConn) Begin() (driver.Tx, error) {
	return nil, errors.New("session lock must not use a transaction")
}

func (c *scannerPGConn) Close() error {
	c.pg.mu.Lock()
	defer c.pg.mu.Unlock()
	c.pg.closed = append(c.pg.closed, c.session)
	if c.pg.holder == c.session {
		c.pg.holder, c.pg.depth = 0, 0
	}
	return nil
}

func (c *scannerPGConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.Contains(query, "pg_try_advisory_lock") || strings.Contains(query, "pg_advisory_unlock") {
		// Keep this literal independent of scanLockKey to catch a wrong key.
		if len(args) != 1 || args[0].Value != int64(0x43525950544f5343) {
			return nil, fmt.Errorf("unexpected scanner advisory key: %v", args)
		}
		p := c.pg
		p.mu.Lock()
		defer p.mu.Unlock()
		event := scannerPGEvent{query: query, session: c.session, ctxErr: ctx.Err()}
		var reply *scannerPGReply
		var value bool
		if strings.Contains(query, "pg_try_advisory_lock") {
			event.step = "acquire"
			value = p.holder == 0 || p.holder == c.session
			if value {
				p.holder, p.depth = c.session, p.depth+1
			}
			reply = p.acquire
		} else {
			event.step = "release"
			if deadline, ok := ctx.Deadline(); ok {
				event.cleanupBudget = time.Until(deadline)
			}
			value = p.holder == c.session
			reply = p.release
			if value && reply == nil {
				p.depth--
				if p.depth == 0 {
					p.holder = 0
				}
			}
		}
		p.events = append(p.events, event)
		if reply != nil {
			// An acquire failure is injected AFTER the server takes the lock:
			// pooling this session would leave a real, reentrant orphan lock.
			if reply.err != nil {
				return nil, reply.err
			}
			if reply.empty {
				return &scannerPGRows{columns: []string{"locked"}}, nil
			}
			return scannerOneRow("locked", reply.value), nil
		}
		return scannerOneRow("locked", value), nil
	}
	var step string
	switch {
	case strings.Contains(query, "status = 'pending'"):
		step = "pending"
	case strings.Contains(query, "d.status = 'credited'"):
		step = "recheck"
	case strings.Contains(query, "SELECT last_block"):
		step = "load cursor"
	default:
		return nil, fmt.Errorf("unexpected query: %s", query)
	}
	if err := c.work(ctx, step, query); err != nil {
		return nil, err
	}
	if step == "load cursor" {
		return scannerOneRow("last_block", int64(100)), nil
	}
	return &scannerPGRows{columns: []string{"chain", "tx_hash", "log_index", "from_address", "to_address",
		"token", "amount", "block_number", "block_hash", "reason"}}, nil
}

func (c *scannerPGConn) ExecContext(ctx context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	if !strings.Contains(query, "INSERT INTO crypto_scan_cursors") {
		return nil, fmt.Errorf("unexpected exec: %s", query)
	}
	if err := c.work(ctx, "save cursor", query); err != nil {
		return nil, err
	}
	return driver.RowsAffected(1), nil
}

func (c *scannerPGConn) work(ctx context.Context, step, query string) error {
	p := c.pg
	p.mu.Lock()
	p.events = append(p.events, scannerPGEvent{step: step, query: query, session: c.session, ctxErr: ctx.Err()})
	protected := p.holder != 0 && p.holder != c.session && p.depth == 1
	p.mu.Unlock()
	if !protected {
		return fmt.Errorf("%s ran without a lock on a separate session", step)
	}
	if p.beforeWork != nil {
		if err := p.beforeWork(ctx, step); err != nil {
			return err
		}
	}
	return ctx.Err()
}

type scannerPGRows struct {
	columns []string
	values  [][]driver.Value
}

func scannerOneRow(column string, value driver.Value) *scannerPGRows {
	return &scannerPGRows{columns: []string{column}, values: [][]driver.Value{{value}}}
}

func (r *scannerPGRows) Columns() []string { return r.columns }
func (r *scannerPGRows) Close() error      { return nil }
func (r *scannerPGRows) Next(dest []driver.Value) error {
	if len(r.values) == 0 {
		return io.EOF
	}
	copy(dest, r.values[0])
	r.values = r.values[1:]
	return nil
}

func (p *scannerMockPG) snapshot() (int, int, []scannerPGEvent, []int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.holder, p.depth, append([]scannerPGEvent(nil), p.events...), append([]int(nil), p.closed...)
}

func scannerLockFixture(t *testing.T, pg *scannerMockPG) (*Worker, *rpcFixture) {
	t.Helper()
	db := sql.OpenDB(pg)
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(2)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	tr := testTransfer()
	f := newRPCFixture(tr)
	f.logs = nil
	w := NewWorker(db, map[string]*rpc.Pool{"bsc": f.pool(t)},
		[]ChainConfig{{Chain: "bsc", Tokens: []string{tr.Token}, Confirmations: 5}},
		NewCursorStore(db), &recordingHandler{},
		func(context.Context, string) ([]string, error) { return []string{tr.To}, nil }, 0, 0)
	return w, f
}

func scannerAssertSteps(t *testing.T, pg *scannerMockPG, want ...string) {
	t.Helper()
	_, _, events, _ := pg.snapshot()
	var got []string
	for _, e := range events {
		got = append(got, e.step)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("steps=%v want=%v", got, want)
	}
}

func scannerAssertIdle(t *testing.T, w *Worker, pg *scannerMockPG, discarded bool) {
	t.Helper()
	holder, depth, _, closed := pg.snapshot()
	stats := w.db.Stats()
	if holder != 0 || depth != 0 || stats.InUse != 0 {
		t.Fatalf("leaked session lock: holder=%d depth=%d stats=%+v", holder, depth, stats)
	}
	if discarded && (len(closed) != 1 || closed[0] != 1 || stats.OpenConnections != stats.Idle) {
		t.Fatalf("uncertain lock session was not discarded: closed=%v stats=%+v", closed, stats)
	}
	if !discarded && len(closed) != 0 {
		t.Fatalf("healthy session was discarded: %v", closed)
	}
}

func TestScannerScanAllHoldsDedicatedSessionThroughCursor(t *testing.T) {
	pg := &scannerMockPG{}
	w, f := scannerLockFixture(t, pg)
	w.scanAll(t.Context())
	scannerAssertSteps(t, pg, "acquire", "pending", "recheck", "load cursor", "save cursor", "release")
	scannerAssertIdle(t, w, pg, false)
	_, _, events, _ := pg.snapshot()
	if events[0].session != events[len(events)-1].session || w.db.Stats().OpenConnections != 2 {
		t.Fatalf("lock was not pinned to its own session: events=%+v stats=%+v", events, w.db.Stats())
	}
	if f.callCount("eth_getLogs") != 1 {
		t.Fatal("the protected pass did not scan the chain")
	}
}

func TestScannerScanAllSkipsWhenAcquireFalse(t *testing.T) {
	// A different PostgreSQL session already owns the global lock.
	pg := &scannerMockPG{holder: -1, depth: 1}
	w, f := scannerLockFixture(t, pg)
	w.scanAll(t.Context())
	scannerAssertSteps(t, pg, "acquire")
	holder, depth, _, closed := pg.snapshot()
	if holder != -1 || depth != 1 || len(closed) != 0 || w.db.Stats().InUse != 0 || f.callCount("eth_chainId") != 0 {
		t.Fatalf("contender performed work or changed the owner's lock: holder=%d depth=%d closed=%v", holder, depth, closed)
	}
}

func TestScannerScanAllDiscardsUnknownAcquireSession(t *testing.T) {
	for _, tt := range []struct {
		name  string
		reply scannerPGReply
	}{
		{"lost response", scannerPGReply{err: errors.New("lost acquire response")}},
		{"cancelled query", scannerPGReply{err: context.Canceled}},
		{"NULL response", scannerPGReply{value: nil}},
		{"invalid response", scannerPGReply{value: "unknown"}},
		{"missing response", scannerPGReply{empty: true}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pg := &scannerMockPG{acquire: &tt.reply}
			w, f := scannerLockFixture(t, pg)
			w.scanAll(t.Context())
			scannerAssertSteps(t, pg, "acquire")
			scannerAssertIdle(t, w, pg, true)
			if w.db.Stats().OpenConnections != 0 || f.callCount("eth_chainId") != 0 {
				t.Fatal("failed acquire pooled the session or started scanning")
			}
		})
	}
}

func TestScannerScanAllDiscardsFailedReleaseSession(t *testing.T) {
	for _, tt := range []struct {
		name  string
		reply scannerPGReply
	}{
		{"lost response", scannerPGReply{err: errors.New("lost unlock response")}},
		{"cleanup timeout", scannerPGReply{err: context.DeadlineExceeded}},
		{"not held", scannerPGReply{value: false}},
		{"NULL response", scannerPGReply{value: nil}},
		{"invalid response", scannerPGReply{value: "unknown"}},
		{"missing response", scannerPGReply{empty: true}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pg := &scannerMockPG{release: &tt.reply}
			w, _ := scannerLockFixture(t, pg)
			w.scanAll(t.Context())
			scannerAssertSteps(t, pg, "acquire", "pending", "recheck", "load cursor", "save cursor", "release")
			scannerAssertIdle(t, w, pg, true)
			if w.db.Stats().OpenConnections != 1 {
				t.Fatal("lock session survived or business session was discarded")
			}
		})
	}
}

func TestScannerScanAllCancellationStillUnlocksWithFiveSecondContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	pg := &scannerMockPG{beforeWork: func(_ context.Context, step string) error {
		if step == "pending" {
			cancel()
		}
		return ctx.Err()
	}}
	w, _ := scannerLockFixture(t, pg)
	w.scanAll(ctx)
	scannerAssertSteps(t, pg, "acquire", "pending", "release")
	scannerAssertIdle(t, w, pg, false)
	_, _, events, _ := pg.snapshot()
	release := events[len(events)-1]
	if release.ctxErr != nil || release.cleanupBudget <= 4*time.Second || release.cleanupBudget > 5*time.Second {
		t.Fatalf("unlock must use an uncancelled independent 5s deadline: %+v", release)
	}
}

func TestScannerScanAllRejectsMissingDatabaseAndSingleConnectionPool(t *testing.T) {
	var nilWorker *Worker
	for _, w := range []*Worker{nilWorker, {}} {
		if release, err := w.tryScanLock(t.Context()); err == nil || release != nil {
			t.Fatal("missing database must fail closed")
		}
		w.scanAll(t.Context()) // must neither panic nor run unprotected work
	}
	pg := &scannerMockPG{}
	w, f := scannerLockFixture(t, pg)
	w.db.SetMaxOpenConns(1)
	w.scanAll(t.Context())
	scannerAssertSteps(t, pg)
	if w.db.Stats().OpenConnections != 0 || f.callCount("eth_chainId") != 0 {
		t.Fatal("one-connection pool was used despite deadlock risk")
	}
}

func TestScannerScanAllSkipsConnectionErrorsAndAlreadyCancelledContext(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancelled=%v", cancelled), func(t *testing.T) {
			pg := &scannerMockPG{openErr: errors.New("database unavailable")}
			w, f := scannerLockFixture(t, pg)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if cancelled {
				cancel()
			}
			w.scanAll(ctx)
			scannerAssertSteps(t, pg)
			if w.db.Stats().InUse != 0 || f.callCount("eth_chainId") != 0 {
				t.Fatal("connection failure started work or leaked a connection")
			}
		})
	}
}

func TestScannerScanAllMultipleWorkersOnlyOwnerPerformsWork(t *testing.T) {
	for _, blockedStep := range []string{"pending", "recheck", "load cursor", "chain scan", "save cursor"} {
		t.Run(blockedStep, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			entered, resume, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			block := func(ctx context.Context, step string) error {
				if step == blockedStep {
					once.Do(func() {
						close(entered)
						select {
						case <-resume:
						case <-ctx.Done():
						}
					})
				}
				return ctx.Err()
			}
			pg := &scannerMockPG{beforeWork: block}
			first, firstRPC := scannerLockFixture(t, pg)
			second, secondRPC := scannerLockFixture(t, pg) // independent connection pool
			addresses := first.addresses
			first.addresses = func(ctx context.Context, chain string) ([]string, error) {
				if err := block(ctx, "chain scan"); err != nil {
					return nil, err
				}
				return addresses(ctx, chain)
			}
			go func() {
				defer close(done)
				first.scanAll(ctx)
			}()
			t.Cleanup(func() {
				cancel()
				<-done
			})
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("owner did not reach the protected step")
			}
			second.scanAll(ctx)
			holder, depth, events, _ := pg.snapshot()
			if holder == 0 || depth != 1 || events[len(events)-1].step != "acquire" || secondRPC.callCount("eth_chainId") != 0 {
				t.Fatalf("contender performed work: holder=%d depth=%d events=%+v", holder, depth, events)
			}
			close(resume)
			select {
			case <-done:
			case <-ctx.Done():
				t.Fatal("owner did not release its lock")
			}
			if firstRPC.callCount("eth_getLogs") != 1 || secondRPC.callCount("eth_getLogs") != 0 {
				t.Fatal("multiple workers scanned during one ownership period")
			}
			scannerAssertIdle(t, first, pg, false)
			// After release, another instance must make progress on a new pass.
			second.scanAll(ctx)
			if secondRPC.callCount("eth_getLogs") != 1 {
				t.Fatal("released lock prevented the next instance from scanning")
			}
			scannerAssertIdle(t, second, pg, false)
		})
	}
}
