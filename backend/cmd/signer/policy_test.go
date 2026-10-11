package main

import (
	"testing"
	"time"
)

func testPolicy() *Policy {
	return &Policy{
		Targets:       []string{"0x1111111111111111111111111111111111111111"},
		MaxPerTx:      map[string]string{"native": "1000", "usdt": "500"},
		MaxDaily:      map[string]string{"native": "5000"},
		RatePerMinute: 2,
	}
}

func TestPolicyAllowsTarget(t *testing.T) {
	p := testPolicy()
	if !p.AllowsTarget("0x1111111111111111111111111111111111111111") {
		t.Fatal("exact target should be allowed")
	}
	if !p.AllowsTarget("0x1111111111111111111111111111111111111111") {
		t.Fatal("case-insensitive target should be allowed")
	}
	if p.AllowsTarget("0x2222222222222222222222222222222222222222") {
		t.Fatal("unknown target must be denied")
	}
	if p.AllowsTarget("") {
		t.Fatal("empty target must be denied")
	}
}

func TestLimiterPerTxLimit(t *testing.T) {
	l, err := newLimiter(t.TempDir() + "/state.json")
	if err != nil {
		t.Fatal(err)
	}
	p := testPolicy()
	reason, err := l.Allow(p, "native", 2000, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if reason != "per_tx_limit" {
		t.Fatalf("reason = %q, want per_tx_limit", reason)
	}
}

func TestLimiterDailyLimit(t *testing.T) {
	l, err := newLimiter(t.TempDir() + "/state.json")
	if err != nil {
		t.Fatal(err)
	}
	p := testPolicy()
	p.RatePerMinute = 0
	now := time.Now()
	for i := 0; i < 5; i++ {
		reason, err := l.Allow(p, "native", 1000, now)
		if err != nil {
			t.Fatal(err)
		}
		if reason != "" {
			t.Fatalf("call %d denied: %s", i, reason)
		}
	}
	reason, err := l.Allow(p, "native", 1, now)
	if err != nil {
		t.Fatal(err)
	}
	if reason != "daily_limit" {
		t.Fatalf("reason = %q, want daily_limit", reason)
	}
}

func TestLimiterRateLimit(t *testing.T) {
	l, err := newLimiter(t.TempDir() + "/state.json")
	if err != nil {
		t.Fatal(err)
	}
	p := testPolicy()
	p.MaxPerTx = map[string]string{"native": "1000"}
	p.MaxDaily = nil
	now := time.Now()
	for i := 0; i < 2; i++ {
		reason, err := l.Allow(p, "native", 1, now)
		if err != nil {
			t.Fatal(err)
		}
		if reason != "" {
			t.Fatalf("call %d denied: %s", i, reason)
		}
	}
	reason, err := l.Allow(p, "native", 1, now)
	if err != nil {
		t.Fatal(err)
	}
	if reason != "rate_limit" {
		t.Fatalf("reason = %q, want rate_limit", reason)
	}
}

func TestLimiterMissingPerTxDenies(t *testing.T) {
	l, err := newLimiter(t.TempDir() + "/state.json")
	if err != nil {
		t.Fatal(err)
	}
	p := testPolicy()
	reason, err := l.Allow(p, "unknown_token", 1, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if reason != "no_per_tx_limit_configured" {
		t.Fatalf("reason = %q, want no_per_tx_limit_configured", reason)
	}
}
