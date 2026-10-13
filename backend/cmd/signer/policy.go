package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"
)

// Policy is the signer-side safety envelope: which targets may receive
// funds and how much may move per transaction and per day.
type Policy struct {
	// Targets that signatures may pay out to (hot wallets). Exact match;
	// EVM addresses compare case-insensitively.
	Targets []string `json:"targets"`

	// MaxPerTx caps one signature's raw amount, keyed by token contract
	// (lowercase 0x address) or "native". Missing key = deny.
	MaxPerTx map[string]string `json:"max_per_tx"`

	// MaxDaily caps the rolling UTC-day total per token key.
	MaxDaily map[string]string `json:"max_daily"`

	// RatePerMinute bounds signature requests per minute (0 = unlimited).
	RatePerMinute int `json:"rate_per_minute"`

	// HotIndex is the derivation index of the hot/gas wallet used for gas
	// top-ups (kind=gas). Defaults to 0 when unset.
	HotIndex uint32 `json:"hot_index"`
}

// AllowsTarget reports whether to is an allowed payout destination.
func (p *Policy) AllowsTarget(to string) bool {
	to = strings.TrimSpace(to)
	if to == "" {
		return false
	}
	for _, target := range p.Targets {
		if strings.EqualFold(target, to) {
			return true
		}
	}
	return false
}

// TokenKey normalizes the policy key for a transfer: token contract
// (lowercased) or "native".
func TokenKey(token string) string {
	token = strings.TrimSpace(strings.ToLower(token))
	if token == "" || token == "native" {
		return "native"
	}
	return token
}

// dailyState is the persisted rolling-day counter. The signer is a single
// process; a file-backed counter survives restarts.
type dailyState struct {
	Day    string            `json:"day"` // UTC YYYY-MM-DD
	Totals map[string]string `json:"totals"`
}

type limiter struct {
	mu          sync.Mutex
	store       *store
	state       dailyState
	minuteStart time.Time
	minuteCount int
}

func newLimiter(st *store) (*limiter, error) {
	l := &limiter{store: st}
	if st != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if raw, err := st.getSetting(ctx, settingState); err == nil && raw != "" {
			_ = json.Unmarshal([]byte(raw), &l.state)
		}
	}
	if l.state.Totals == nil {
		l.state.Totals = map[string]string{}
	}
	return l, nil
}

func (l *limiter) persistLocked() error {
	// A nil store means the caller only wants in-memory counting (tests);
	// production always has one, so limits survive a restart.
	if l.store == nil {
		return nil
	}
	data, err := json.Marshal(l.state)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return l.store.setSetting(ctx, settingState, string(data))
}

func utcDay(now time.Time) string {
	return now.UTC().Format("2006-01-02")
}

// Allow checks and records one signature against per-tx, daily and rate
// limits. It returns a denial reason (empty when allowed).
func (l *limiter) Allow(p *Policy, tokenKey string, amount *big.Int, now time.Time) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if p.RatePerMinute > 0 {
		if now.Sub(l.minuteStart) >= time.Minute {
			l.minuteStart = now
			l.minuteCount = 0
		}
		if l.minuteCount >= p.RatePerMinute {
			return "rate_limit", nil
		}
	}

	maxTxRaw, ok := p.MaxPerTx[tokenKey]
	if !ok {
		return "no_per_tx_limit_configured", nil
	}
	maxTx, err := parseBigInt(maxTxRaw)
	if err != nil {
		return "", fmt.Errorf("policy max_per_tx[%s]: %w", tokenKey, err)
	}
	if amount.Cmp(maxTx) > 0 {
		return "per_tx_limit", nil
	}

	day := utcDay(now)
	if l.state.Day != day {
		l.state.Day = day
		l.state.Totals = map[string]string{}
	}
	if maxDailyRaw, ok := p.MaxDaily[tokenKey]; ok {
		maxDaily, err := parseBigInt(maxDailyRaw)
		if err != nil {
			return "", fmt.Errorf("policy max_daily[%s]: %w", tokenKey, err)
		}
		current, err := parseBigInt(l.state.Totals[tokenKey])
		if err != nil {
			current = big.NewInt(0)
		}
		total := new(big.Int).Add(current, amount)
		if total.Cmp(maxDaily) > 0 {
			return "daily_limit", nil
		}
		l.state.Totals[tokenKey] = total.String()
		if err := l.persistLocked(); err != nil {
			return "", fmt.Errorf("persist daily state: %w", err)
		}
	}

	l.minuteCount++
	return "", nil
}
