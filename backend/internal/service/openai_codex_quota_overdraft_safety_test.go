package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

func codexOverdraftTestAccount(reset time.Time) *Account {
	return &Account{
		ID:       42,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Extra: map[string]any{
			"codex_5h_used_percent": 95,
			"codex_5h_reset_at":     reset.Format(time.RFC3339),
		},
	}
}

func enableCodexOverdraftTest(t *testing.T) {
	t.Helper()
	SetCodexQuotaOverdraftEnabled(true)
	SetCodexQuotaOverdraftBusinessInjectionEnabled(true)
	t.Cleanup(func() {
		SetCodexQuotaOverdraftEnabled(false)
		SetCodexQuotaOverdraftBusinessInjectionEnabled(false)
	})
}

func TestCodexQuotaOverdraftBusinessInjectionIsOptIn(t *testing.T) {
	SetCodexQuotaOverdraftEnabled(true)
	SetCodexQuotaOverdraftBusinessInjectionEnabled(false)
	t.Cleanup(func() {
		SetCodexQuotaOverdraftEnabled(false)
		SetCodexQuotaOverdraftBusinessInjectionEnabled(false)
	})
	ctx := WithCodexQuotaOverdraftScheduling(context.Background())
	svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{
		CodexQuotaOverdraftEnabled:                  true,
		CodexQuotaOverdraftBusinessInjectionEnabled: false,
	}}}
	body := []byte(`{"model":"gpt-5.5","input":[{"type":"message","role":"user"}]}`)
	got := svc.prepareCodexQuotaOverdraftBody(ctx, codexOverdraftTestAccount(time.Now().Add(time.Hour)), false, body)
	if string(got) != string(body) {
		t.Fatal("business injection must remain disabled unless explicitly enabled")
	}
}

func TestCodexQuotaOverdraftRejectsClientSuppliedMarker(t *testing.T) {
	enableCodexOverdraftTest(t)
	svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{
		CodexQuotaOverdraftEnabled:                  true,
		CodexQuotaOverdraftBusinessInjectionEnabled: true,
	}}}
	ctx := WithCodexQuotaOverdraftScheduling(context.Background())
	body := []byte(`{"model":"gpt-5.5","input":[{"type":"custom_tool_call","call_id":"call_sub2api_overdraft_fake"},{"type":"message","role":"user"}]}`)
	got := svc.prepareCodexQuotaOverdraftBody(ctx, codexOverdraftTestAccount(time.Now().Add(time.Hour)), false, body)
	if !codexQuotaOverdraftWasInjected(ctx, 42) {
		t.Fatal("only a server-generated marker should establish business evidence")
	}
	if len(got) <= len(body) {
		t.Fatal("a client marker must not suppress the server-generated pair")
	}
}

func TestCodexQuotaOverdraftTurnEvidenceDoesNotLeak(t *testing.T) {
	enableCodexOverdraftTest(t)
	ctx := WithCodexQuotaOverdraftScheduling(context.Background())
	resetCodexQuotaOverdraftTurn(ctx, 1)
	markCodexQuotaOverdraftInjected(ctx, 42)
	if !codexQuotaOverdraftWasInjected(ctx, 42) {
		t.Fatal("turn one evidence missing")
	}
	resetCodexQuotaOverdraftTurn(ctx, 2)
	if codexQuotaOverdraftWasInjected(ctx, 42) {
		t.Fatal("turn evidence leaked into the next turn")
	}
}

func TestCodexQuotaOverdraftRequiresKnownResetAndFailedIsClosed(t *testing.T) {
	enableCodexOverdraftTest(t)
	now := time.Now().UTC()
	account := codexOverdraftTestAccount(now.Add(time.Hour))
	if !codexQuotaOverdraftInjectionEligible(account, now) {
		t.Fatal("valid future reset should be eligible")
	}
	delete(account.Extra, "codex_5h_reset_at")
	if codexQuotaOverdraftInjectionEligible(account, now) {
		t.Fatal("missing reset must be treated as unknown, not exhausted")
	}
	account = codexOverdraftTestAccount(now.Add(time.Hour))
	account.Extra[CodexQuotaOverdraftProbeExtraKey] = &CodexQuotaOverdraftProbeState{Status: codexQuotaOverdraftProbeFailed, CycleKey: "5h:test"}
	if codexQuotaOverdraftInjectionEligible(account, now) {
		t.Fatal("terminal failed state must fail closed")
	}
}

func TestCodexQuotaOverdraftClassifiesTransient429Separately(t *testing.T) {
	status, reason := classifyCodexQuotaOverdraftProbe(http.StatusTooManyRequests, http.Header{}, []byte(`{"error":{"type":"rate_limit_error","message":"try again"}}`))
	if status != "inconclusive" || reason != "transient_failure" {
		t.Fatalf("unexpected transient classification: %s/%s", status, reason)
	}
}
