package service

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCodexQuotaOverdraftAccountHasQuotaEvidence(t *testing.T) {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	future := now.Add(24 * time.Hour).Format(time.RFC3339)
	past := now.Add(-time.Minute).Format(time.RFC3339)

	newAccount := func(extra map[string]any) *Account {
		return &Account{
			ID:       29092,
			Platform: PlatformOpenAI,
			Type:     AccountTypeOAuth,
			Extra:    extra,
		}
	}

	tests := []struct {
		name string
		acct *Account
		want bool
	}{
		{
			name: "five hour threshold with future reset",
			acct: newAccount(map[string]any{
				"codex_5h_used_percent": 95.0,
				"codex_5h_reset_at":     future,
			}),
			want: true,
		},
		{
			name: "seven day threshold with future reset",
			acct: newAccount(map[string]any{
				"codex_7d_used_percent": 100.0,
				"codex_7d_reset_at":     future,
			}),
			want: true,
		},
		{
			name: "ordinary future 429 has no quota evidence",
			acct: newAccount(map[string]any{
				"codex_5h_used_percent": 40.0,
				"codex_5h_reset_at":     future,
			}),
			want: false,
		},
		{
			name: "below prearm threshold",
			acct: newAccount(map[string]any{
				"codex_5h_used_percent": 94.99,
				"codex_5h_reset_at":     future,
			}),
			want: false,
		},
		{
			name: "non finite quota value",
			acct: newAccount(map[string]any{
				"codex_5h_used_percent": math.NaN(),
				"codex_5h_reset_at":     future,
			}),
			want: false,
		},
		{
			name: "missing quota reset",
			acct: newAccount(map[string]any{
				"codex_5h_used_percent": 100.0,
			}),
			want: false,
		},
		{
			name: "relative reset requires server snapshot timestamp",
			acct: newAccount(map[string]any{
				"codex_5h_used_percent":        100.0,
				"codex_5h_reset_after_seconds": 3600,
			}),
			want: false,
		},
		{
			name: "quota reset already elapsed",
			acct: newAccount(map[string]any{
				"codex_7d_used_percent": 100.0,
				"codex_7d_reset_at":     past,
			}),
			want: false,
		},
		{
			name: "malformed quota reset",
			acct: newAccount(map[string]any{
				"codex_5h_used_percent": 100.0,
				"codex_5h_reset_at":     "not-a-timestamp",
			}),
			want: false,
		},
		{
			name: "non OAuth account",
			acct: &Account{
				ID:       29092,
				Platform: PlatformOpenAI,
				Type:     AccountTypeAPIKey,
				Extra: map[string]any{
					"codex_5h_used_percent": 100.0,
					"codex_5h_reset_at":     future,
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, codexQuotaOverdraftAccountHasQuotaEvidence(tt.acct, now))
		})
	}
}

func TestAccountTestCodexQuotaOverdraftPreparationPreservesMessageInput(t *testing.T) {
	previousEnabled := CodexQuotaOverdraftEnabled()
	previousBusinessInjection := CodexQuotaOverdraftBusinessInjectionEnabled()
	SetCodexQuotaOverdraftEnabled(true)
	SetCodexQuotaOverdraftBusinessInjectionEnabled(true)
	t.Cleanup(func() {
		SetCodexQuotaOverdraftEnabled(previousEnabled)
		SetCodexQuotaOverdraftBusinessInjectionEnabled(previousBusinessInjection)
	})

	now := time.Now().UTC()
	account := &Account{
		ID:       29092,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Extra: map[string]any{
			"codex_5h_used_percent": 100.0,
			"codex_5h_reset_at":     now.Add(time.Hour).Format(time.RFC3339),
		},
	}
	body := []byte(`{"model":"gpt-5.4","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}]}`)

	svc := &AccountTestService{}
	ctx, prepared, injected := svc.prepareCodexQuotaOverdraftTestRequest(context.Background(), account, body)
	require.True(t, injected)
	require.True(t, CodexQuotaOverdraftSchedulingEnabled(ctx))
	require.True(t, codexQuotaOverdraftBusinessInjectionEnabledForContext(ctx))
	require.True(t, codexQuotaOverdraftWasInjected(ctx, account.ID))

	var document struct {
		Input []map[string]any `json:"input"`
	}
	require.NoError(t, json.Unmarshal(prepared, &document))
	require.Len(t, document.Input, 3)
	require.Equal(t, "message", document.Input[0]["type"])
	require.Equal(t, "custom_tool_call", document.Input[1]["type"])
	require.Equal(t, "custom_tool_call_output", document.Input[2]["type"])
}

func TestCodexQuotaOverdraftNormalizationBypassesOnlyQuotaRateLimitOnClone(t *testing.T) {
	now := time.Now().UTC()
	future := now.Add(time.Hour)
	account := &Account{
		ID:               29092,
		Platform:         PlatformOpenAI,
		Type:             AccountTypeOAuth,
		Status:           StatusActive,
		Schedulable:      true,
		RateLimitedAt:    codexPtrTime(now.Add(-time.Minute)),
		RateLimitResetAt: &future,
		Extra: map[string]any{
			"codex_7d_used_percent": 100.0,
			"codex_7d_reset_at":     future.Format(time.RFC3339),
		},
	}
	ctx := WithCodexQuotaOverdraftSchedulingSnapshot(context.Background(), true, false)
	normalized := normalizeCodexQuotaOverdraftAccountForScheduling(ctx, account)
	require.NotSame(t, account, normalized)
	require.Nil(t, normalized.RateLimitResetAt)
	require.Nil(t, normalized.RateLimitedAt)
	require.Same(t, &future, account.RateLimitResetAt)

	marked := WithCodexQuotaOverdraftSchedulingSnapshot(context.Background(), true, false)
	markCodexQuotaOverdraftCandidates(marked, []Account{*account})
	hydrated, ok := normalizeCodexQuotaOverdraftHydratedAccount(marked, account, now)
	require.True(t, ok)
	require.NotSame(t, account, hydrated)
	require.Nil(t, hydrated.RateLimitResetAt)
}

func codexPtrTime(value time.Time) *time.Time { return &value }
