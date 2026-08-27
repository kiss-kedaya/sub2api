package service

import (
	"context"
	"encoding/json"
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
	body := []byte(`{"model":"gpt-5.5","input":[{"type":"custom_tool_call","name":"exec","call_id":"call_sub2api_overdraft_fake","input":"{}"},{"type":"custom_tool_call_output","call_id":"call_sub2api_overdraft_fake","output":"ok"},{"type":"message","role":"user"}]}`)
	got := svc.prepareCodexQuotaOverdraftBody(ctx, codexOverdraftTestAccount(time.Now().Add(time.Hour)), false, body)
	if !codexQuotaOverdraftWasInjected(ctx, 42) {
		t.Fatal("only a server-generated marker should establish business evidence")
	}
	if len(got) <= len(body) {
		t.Fatal("a client marker must not suppress the server-generated pair")
	}
}

func TestCodexQuotaOverdraftInjectionUsesCompletedCustomToolPair(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","input":[{"type":"message","role":"user","content":"hello"}]}`)
	updated, changed, err := injectCodexQuotaOverdraft(body)
	if err != nil {
		t.Fatalf("injectCodexQuotaOverdraft() error = %v", err)
	}
	if !changed {
		t.Fatal("expected payload injection")
	}

	var document struct {
		Input []map[string]any `json:"input"`
	}
	if err := json.Unmarshal(updated, &document); err != nil {
		t.Fatalf("decode injected payload: %v", err)
	}
	if len(document.Input) != 3 {
		t.Fatalf("input length = %d, want 3", len(document.Input))
	}
	call, output := document.Input[1], document.Input[2]
	if call["type"] != "custom_tool_call" || output["type"] != "custom_tool_call_output" {
		t.Fatalf("unexpected pair types: call=%v output=%v", call["type"], output["type"])
	}
	if call["name"] != codexQuotaOverdraftToolName || call["status"] != "completed" {
		t.Fatalf("unexpected custom call shape: %#v", call)
	}
	if call["input"] != codexQuotaOverdraftExecInput {
		t.Fatalf("custom input = %v", call["input"])
	}
	if _, exists := call["arguments"]; exists {
		t.Fatal("custom call must not contain legacy arguments")
	}
	if output["call_id"] != call["call_id"] {
		t.Fatalf("pair call IDs differ: call=%v output=%v", call["call_id"], output["call_id"])
	}
	if _, exists := output["status"]; exists {
		t.Fatal("custom tool output must not contain status")
	}
}

func TestCodexQuotaOverdraftInjectionRecognizesCompleteLegacyAndCustomPairs(t *testing.T) {
	for _, tc := range []struct {
		name       string
		callType   string
		outputType string
		inputField string
	}{
		{name: "custom", callType: "custom_tool_call", outputType: "custom_tool_call_output", inputField: `"input":"{}"`},
		{name: "legacy", callType: "function_call", outputType: "function_call_output", inputField: `"arguments":"{}"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"input":[{"type":"message","role":"user"},{"type":"` + tc.callType + `","name":"exec","call_id":"call_sub2api_overdraft_known",` + tc.inputField + `},{"type":"` + tc.outputType + `","call_id":"call_sub2api_overdraft_known","output":"ok"}]}`)
			updated, changed, err := injectCodexQuotaOverdraft(body)
			if err != nil {
				t.Fatalf("injectCodexQuotaOverdraft() error = %v", err)
			}
			if changed || string(updated) != string(body) {
				t.Fatalf("complete %s pair was injected twice: %s", tc.name, updated)
			}
		})
	}
}

func TestCodexQuotaOverdraftInjectionDoesNotTrustOrphanCall(t *testing.T) {
	body := []byte(`{"input":[{"type":"custom_tool_call","name":"exec","call_id":"call_sub2api_overdraft_orphan","input":"{}"},{"type":"message","role":"user"}]}`)
	updated, changed, err := injectCodexQuotaOverdraft(body)
	if err != nil {
		t.Fatalf("injectCodexQuotaOverdraft() error = %v", err)
	}
	if !changed || len(updated) <= len(body) {
		t.Fatal("an orphan marker must not suppress a complete server pair")
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
