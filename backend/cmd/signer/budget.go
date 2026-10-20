package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// dailyState lives ONLY in the signer-owned ledger. Shared web settings must
// never be used as the source of budget truth, even when they contain old data.
type dailyState struct {
	Day         string            `json:"day"`
	Totals      map[string]string `json:"totals"`
	Minute      int64             `json:"minute,omitempty"`
	MinuteCount int               `json:"minute_count,omitempty"`
}

type limiter struct {
	mu    sync.Mutex
	db    *sql.DB
	state dailyState // memory-only unit-test helper, never enabled by main
}

// Keep malformed transaction/build failures compatible with the existing HTTP
// 400 response, while persistence failures must surface as service errors.
type signBuildError struct{ error }

func utcDay(now time.Time) string { return now.UTC().Format("2006-01-02") }

func newLimiter(st *store) (*limiter, error) {
	if st != nil {
		return nil, fmt.Errorf("shared database budgets are disabled; use -state")
	}
	return &limiter{}, nil
}

func openBudgetDB(path string) (*sql.DB, error) {
	if !localAbsolutePath(path) {
		return nil, fmt.Errorf("state requires an absolute local path")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("state must already exist as a regular file; initialize offline with -init-state")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("state must be private to signer (0600)")
	}
	dir, err := os.Lstat(filepath.Dir(path))
	if err != nil || !dir.IsDir() || dir.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("state directory must be a real local directory")
	}
	if runtime.GOOS != "windows" && dir.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("state directory must be private to signer (0700)")
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	if runtime.GOOS == "windows" {
		u.Path = "/" + u.Path
	}
	q := url.Values{"mode": {"rw"}, "_txlock": {"immediate"},
		"_pragma": {"busy_timeout(5000)", "synchronous(FULL)"}}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	// All connections also get the same DSN pragmas. The bound avoids a local
	// writer deadlocking its own transaction while another request is queued.
	db.SetMaxOpenConns(1)
	return db, nil
}

// initializeBudget is an explicit offline action, never normal startup. It
// refuses to overwrite any existing file, including an empty/broken ledger.
// Existing installations must migrate reviewed totals or wait for a new UTC
// day with signing stopped; creating a zero ledger mid-day resets the budget.
func initializeBudget(path string, now time.Time) error {
	if !localAbsolutePath(path) {
		return fmt.Errorf("state requires an absolute local path")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create state: %w", err)
	}
	if err := f.Close(); err != nil {
		return err
	}
	db, err := openBudgetDB(path)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `CREATE TABLE signer_budget (
		singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
		version INTEGER NOT NULL CHECK (version = 1), state TEXT NOT NULL)`); err != nil {
		return err
	}
	raw, _ := json.Marshal(dailyState{Day: utcDay(now), Totals: map[string]string{}})
	if _, err := tx.ExecContext(ctx, `INSERT INTO signer_budget VALUES (1, 1, ?)`, string(raw)); err != nil {
		return err
	}
	return tx.Commit()
}

func decodeBudget(raw string) (dailyState, error) {
	var state dailyState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return state, fmt.Errorf("invalid budget state: %w", err)
	}
	if day, err := time.Parse("2006-01-02", state.Day); err != nil || utcDay(day) != state.Day || state.Totals == nil || state.Minute < 0 || state.MinuteCount < 0 {
		return state, fmt.Errorf("invalid budget state fields")
	}
	for key, raw := range state.Totals {
		v, err := parseBigInt(raw)
		if key == "" || raw == "" || err != nil || v.BitLen() > 256 {
			return state, fmt.Errorf("invalid persisted total")
		}
	}
	return state, nil
}

func newPersistentLimiter(path string) (*limiter, error) {
	db, err := openBudgetDB(path)
	if err != nil {
		return nil, err
	}
	l := &limiter{db: db}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var raw string
	err = db.QueryRowContext(ctx, `SELECT state FROM signer_budget WHERE singleton = 1 AND version = 1`).Scan(&raw)
	if err == nil {
		_, err = decodeBudget(raw)
	}
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("load budget (refusing to reset): %w", err)
	}
	return l, nil
}

// authorizeAndSign holds a database write lock across the read/check/sign/write
// cycle. Failed signing rolls back all charges. Signed bytes are returned ONLY
// after the durable commit: write/commit errors deny signing. A crash after
// commit but before HTTP delivery conservatively consumes budget.
func (l *limiter) authorizeAndSign(ctx context.Context, p *Policy, charges map[string]*big.Int, now time.Time, sign func() (string, error)) (signed, reason string, err error) {
	if l == nil {
		return "", "", fmt.Errorf("budget unavailable")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	var tx *sql.Tx
	state := l.state
	if l.db != nil {
		tx, err = l.db.BeginTx(ctx, nil) // BEGIN IMMEDIATE, including across processes
		if err != nil {
			return "", "", fmt.Errorf("lock budget: %w", err)
		}
		defer tx.Rollback()
		var raw string
		if err := tx.QueryRowContext(ctx, `SELECT state FROM signer_budget WHERE singleton = 1 AND version = 1`).Scan(&raw); err != nil {
			return "", "", fmt.Errorf("read budget: %w", err)
		}
		if state, err = decodeBudget(raw); err != nil {
			return "", "", err
		}
	}
	if state.Day > utcDay(now) || state.Minute > now.Unix()/60 {
		return "", "", fmt.Errorf("clock moved backwards; refusing budget rollback")
	}
	if state.Day != utcDay(now) {
		state = dailyState{Day: utcDay(now), Totals: map[string]string{}}
	}
	if state.Minute != now.Unix()/60 {
		state.Minute, state.MinuteCount = now.Unix()/60, 0
	}
	if p.RatePerMinute > 0 && state.MinuteCount >= p.RatePerMinute {
		return "", "rate_limit", nil
	}
	totals := make(map[string]string, len(state.Totals)+len(charges))
	for key, total := range state.Totals {
		totals[key] = total
	}
	if len(charges) == 0 {
		return "", "", fmt.Errorf("empty budget charges")
	}
	for key, amount := range charges {
		if amount == nil || amount.Sign() <= 0 || amount.BitLen() > 256 {
			return "", "", fmt.Errorf("invalid budget amount")
		}
		maxTxRaw, ok := p.MaxPerTx[key]
		if !ok {
			return "", "no_per_tx_limit_configured", nil
		}
		maxTx, err := positiveUint(maxTxRaw, 256)
		if err != nil {
			return "", "", fmt.Errorf("invalid per-tx policy: %w", err)
		}
		if amount.Cmp(maxTx) > 0 {
			return "", "per_tx_limit", nil
		}
		maxDailyRaw, ok := p.MaxDaily[key]
		if !ok {
			return "", "no_daily_limit_configured", nil
		}
		maxDaily, err := positiveUint(maxDailyRaw, 256)
		if err != nil {
			return "", "", fmt.Errorf("invalid daily policy: %w", err)
		}
		current, err := parseBigInt(totals[key])
		if err != nil {
			return "", "", fmt.Errorf("invalid persisted total: %w", err)
		}
		total := new(big.Int).Add(current, amount)
		if total.Cmp(maxDaily) > 0 {
			return "", "daily_limit", nil
		}
		totals[key] = total.String()
	}
	signed, err = sign()
	if err != nil {
		return "", "", &signBuildError{err}
	}
	state.Totals, state.MinuteCount = totals, state.MinuteCount+1
	if tx != nil {
		raw, err := json.Marshal(state)
		if err != nil {
			return "", "", err
		}
		result, err := tx.ExecContext(ctx, `UPDATE signer_budget SET state = ? WHERE singleton = 1 AND version = 1`, string(raw))
		if err != nil {
			return "", "", fmt.Errorf("persist budget: %w", err)
		}
		if rows, err := result.RowsAffected(); err != nil || rows != 1 {
			return "", "", fmt.Errorf("budget row missing")
		}
		if err := tx.Commit(); err != nil {
			return "", "", fmt.Errorf("commit budget: %w", err)
		}
	} else {
		l.state = state
	}
	return signed, "", nil
}

// Allow is kept for the existing isolated limiter tests. HTTP signing always
// uses authorizeAndSign and requires a persistent ledger.
func (l *limiter) Allow(p *Policy, key string, amount *big.Int, now time.Time) (string, error) {
	_, reason, err := l.authorizeAndSign(context.Background(), p, map[string]*big.Int{key: amount}, now, func() (string, error) { return "", nil })
	return reason, err
}
