package service

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func ptrTierFloat(v float64) *float64 { return &v }

func TestNormalizeGroupOpenAITierMultiplier(t *testing.T) {
	cases := []struct {
		name    string
		in      *float64
		want    *float64
		wantErr bool
	}{
		{"omitted", nil, nil, false},
		{"zero clears the override", ptrTierFloat(0), nil, false},
		{"one means no surcharge", ptrTierFloat(1), ptrTierFloat(1), false},
		{"rounded to four decimals", ptrTierFloat(1.234567), ptrTierFloat(1.2346), false},
		{"upper bound", ptrTierFloat(100), ptrTierFloat(100), false},
		{"below the smallest step is rejected", ptrTierFloat(0.00004), nil, true},
		{"negative", ptrTierFloat(-1), nil, true},
		{"too large", ptrTierFloat(100.01), nil, true},
		{"NaN", ptrTierFloat(math.NaN()), nil, true},
		{"infinity", ptrTierFloat(math.Inf(1)), nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeGroupOpenAITierMultiplier("openai_fast_multiplier", tc.in)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if tc.want == nil {
				require.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			require.InDelta(t, *tc.want, *got, 1e-12)
		})
	}
}

func TestOpenAITierBillingPolicyMultiplierFor(t *testing.T) {
	openAI := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	grok := &Account{Platform: PlatformGrok, Type: AccountTypeAPIKey}

	t.Run("no policy and no override leave the official price", func(t *testing.T) {
		_, ok := (*OpenAITierBillingPolicy)(nil).multiplierFor(openAI, "priority")
		require.False(t, ok)
		policy := openAITierBillingPolicyFromGroup(&Group{Platform: PlatformOpenAI})
		for _, tier := range []string{"priority", "fast", "ultrafast", "flex", "", "default"} {
			_, ok := policy.multiplierFor(openAI, tier)
			require.False(t, ok, tier)
		}
	})
	t.Run("explicit multipliers apply to their own tier only", func(t *testing.T) {
		policy := openAITierBillingPolicyFromGroup(&Group{Platform: PlatformOpenAI, OpenAIFastMultiplier: ptrTierFloat(1.5), OpenAIUltrafastMultiplier: ptrTierFloat(3)})
		m, ok := policy.multiplierFor(openAI, "priority")
		require.True(t, ok)
		require.Equal(t, 1.5, m)
		m, ok = policy.multiplierFor(openAI, " Fast ")
		require.True(t, ok)
		require.Equal(t, 1.5, m, "the client alias for Fast is the same tier")
		m, ok = policy.multiplierFor(openAI, "ultrafast")
		require.True(t, ok)
		require.Equal(t, 3.0, m)
		_, ok = policy.multiplierFor(openAI, "flex")
		require.False(t, ok, "Flex is not a premium tier")
	})
	t.Run("free Fast is a Fast multiplier of 1 and wins over an explicit one", func(t *testing.T) {
		policy := openAITierBillingPolicyFromGroup(&Group{Platform: PlatformOpenAI, FreeOpenAIFast: true, OpenAIFastMultiplier: ptrTierFloat(2)})
		m, ok := policy.multiplierFor(openAI, "priority")
		require.True(t, ok)
		require.Equal(t, 1.0, m)
		_, ok = policy.multiplierFor(openAI, "ultrafast")
		require.False(t, ok, "free Fast does not cover UltraFast")
	})
	t.Run("a stored non-positive value is ignored", func(t *testing.T) {
		policy := openAITierBillingPolicyFromGroup(&Group{Platform: PlatformOpenAI, OpenAIFastMultiplier: ptrTierFloat(0)})
		_, ok := policy.multiplierFor(openAI, "priority")
		require.False(t, ok)
	})
	t.Run("only OpenAI accounts serve these tiers", func(t *testing.T) {
		policy := openAITierBillingPolicyFromGroup(&Group{Platform: PlatformOpenAI, FreeOpenAIFast: true})
		_, ok := policy.multiplierFor(grok, "priority")
		require.False(t, ok)
		_, ok = policy.multiplierFor(nil, "priority")
		require.False(t, ok)
	})
	t.Run("groups that cannot serve Fast have no policy", func(t *testing.T) {
		require.Nil(t, openAITierBillingPolicyFromGroup(&Group{Platform: PlatformAnthropic, FreeOpenAIFast: true}))
		require.Nil(t, openAITierBillingPolicyFromGroup(nil))
	})
}

func TestOpenAITierBillingPolicyFromRequestPrefersTheForcingGroup(t *testing.T) {
	groupID := int64(77)
	// The key's routed group is a scheduler-cache copy that has not seen the edit yet.
	stale := &Group{ID: groupID, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true}
	fresh := &Group{ID: groupID, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, FreeOpenAIFast: true, OpenAIUltrafastMultiplier: ptrTierFloat(2)}
	apiKey := &APIKey{ID: 1, GroupID: &groupID, Group: stale}

	ctx := context.WithValue(context.Background(), ctxkey.Group, fresh)
	policy := OpenAITierBillingPolicyFromRequest(ctx, apiKey)
	require.NotNil(t, policy)
	require.Equal(t, groupID, policy.GroupID)
	require.True(t, policy.FreeFast)
	require.NotNil(t, policy.UltrafastMultiplier)
	require.Equal(t, 2.0, *policy.UltrafastMultiplier)

	// A context group for another group, or none at all, falls back to the key's group.
	other := &Group{ID: 78, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, FreeOpenAIFast: true}
	require.False(t, OpenAITierBillingPolicyFromRequest(context.WithValue(context.Background(), ctxkey.Group, other), apiKey).FreeFast)
	require.False(t, OpenAITierBillingPolicyFromRequest(context.Background(), apiKey).FreeFast)
	require.False(t, OpenAITierBillingPolicyFromRequest(nil, apiKey).FreeFast) //nolint:staticcheck // nil context is part of the contract
	require.Nil(t, OpenAITierBillingPolicyFromRequest(ctx, nil))
}

// ---- billing ----------------------------------------------------------------------------------------

type tierBillingFixture struct {
	svc      *OpenAIGatewayService
	usage    *openAIRecordUsageLogRepoStub
	groupID  int64
	standard float64 // token cost at the Standard price, before any multiplier
}

func newTierBillingFixture(t *testing.T, model string, group func(*Group)) (*tierBillingFixture, *APIKey) {
	t.Helper()
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
	svc.resolver = NewModelPricingResolver(nil, svc.billingService)

	groupID := int64(77)
	inputPrice, outputPrice := 0.001, 0.002
	fast, ultrafast := 3.0, 6.0
	g := &Group{
		ID: groupID, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, RateMultiplier: 0.5,
		ModelPricing: []ChannelModelPricing{{
			Models: []string{model}, BillingMode: BillingModeToken,
			InputPrice: &inputPrice, OutputPrice: &outputPrice, FastMultiplier: &fast, UltrafastMultiplier: &ultrafast,
		}},
	}
	if group != nil {
		group(g)
	}
	return &tierBillingFixture{svc: svc, usage: usageRepo, groupID: groupID, standard: 100*inputPrice + 50*outputPrice},
		&APIKey{ID: 1020, GroupID: &groupID, Group: g}
}

func (f *tierBillingFixture) record(t *testing.T, apiKey *APIKey, model, tier string, mutate func(*OpenAIRecordUsageInput)) *UsageLog {
	t.Helper()
	result := &OpenAIForwardResult{
		RequestID: "resp_tier_" + tier,
		Usage:     OpenAIUsage{InputTokens: 100, OutputTokens: 50},
		Model:     model,
		Duration:  time.Second,
	}
	if tier != "" {
		result.ServiceTier = &tier
	}
	in := &OpenAIRecordUsageInput{
		Result: result, APIKey: apiKey, User: &User{ID: 2020},
		Account: &Account{ID: 3020, Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
	}
	if mutate != nil {
		mutate(in)
	}
	require.NoError(t, f.svc.RecordUsage(context.Background(), in))
	require.NotNil(t, f.usage.lastLog)
	return f.usage.lastLog
}

func requireTierRow(t *testing.T, row *UsageLog, wantTier string, wantTotal, wantActual, wantMultiplier float64) {
	t.Helper()
	require.NotNil(t, row.ServiceTier)
	require.Equal(t, wantTier, *row.ServiceTier, "the log keeps the tier that was actually used")
	require.InDelta(t, wantTotal, row.TotalCost, 1e-9, "total cost is the real upstream tier price")
	require.InDelta(t, wantActual, row.ActualCost, 1e-9, "actual cost is what the customer pays")
	require.NotNil(t, row.ServiceTierMultiplier)
	require.InDelta(t, wantMultiplier, *row.ServiceTierMultiplier, 1e-9)
}

func TestRecordUsage_FastTierMultiplier(t *testing.T) {
	const model = "gpt-5.6-sol"
	rate := 0.5
	for _, tc := range []struct {
		name       string
		group      func(*Group)
		wantActual func(standard float64) float64
		wantMult   float64
	}{
		{"no override keeps the official Fast price", nil, func(s float64) float64 { return s * 3 * rate }, 3},
		{"group multiplier 1.5", func(g *Group) { g.OpenAIFastMultiplier = ptrTierFloat(1.5) }, func(s float64) float64 { return s * 1.5 * rate }, 1.5},
		{"group multiplier 1 is free Fast", func(g *Group) { g.OpenAIFastMultiplier = ptrTierFloat(1) }, func(s float64) float64 { return s * rate }, 1},
		{"legacy free Fast flag", func(g *Group) { g.FreeOpenAIFast = true }, func(s float64) float64 { return s * rate }, 1},
		{"free Fast wins over an explicit multiplier", func(g *Group) { g.FreeOpenAIFast = true; g.OpenAIFastMultiplier = ptrTierFloat(2) }, func(s float64) float64 { return s * rate }, 1},
		{"a multiplier above the official one", func(g *Group) { g.OpenAIFastMultiplier = ptrTierFloat(4) }, func(s float64) float64 { return s * 4 * rate }, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, apiKey := newTierBillingFixture(t, model, tc.group)
			row := f.record(t, apiKey, model, "priority", nil)
			requireTierRow(t, row, "priority", f.standard*3, tc.wantActual(f.standard), tc.wantMult)
			require.InDelta(t, rate, row.RateMultiplier, 1e-12, "the group rate stays the group rate")
		})
	}
}

func TestRecordUsage_UltrafastTierMultiplier(t *testing.T) {
	const model = "gpt-6.1-sol"
	rate := 0.5
	for _, tc := range []struct {
		name       string
		group      func(*Group)
		wantActual func(standard float64) float64
		wantMult   float64
	}{
		{"no override keeps the official UltraFast price", nil, func(s float64) float64 { return s * 6 * rate }, 6},
		{"free UltraFast", func(g *Group) { g.OpenAIUltrafastMultiplier = ptrTierFloat(1) }, func(s float64) float64 { return s * rate }, 1},
		{"UltraFast multiplier 3", func(g *Group) { g.OpenAIUltrafastMultiplier = ptrTierFloat(3) }, func(s float64) float64 { return s * 3 * rate }, 3},
		{"free Fast does not make UltraFast free", func(g *Group) { g.FreeOpenAIFast = true }, func(s float64) float64 { return s * 6 * rate }, 6},
		{"a Fast multiplier does not touch UltraFast", func(g *Group) { g.OpenAIFastMultiplier = ptrTierFloat(1) }, func(s float64) float64 { return s * 6 * rate }, 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, apiKey := newTierBillingFixture(t, model, tc.group)
			row := f.record(t, apiKey, model, "ultrafast", nil)
			requireTierRow(t, row, "ultrafast", f.standard*6, tc.wantActual(f.standard), tc.wantMult)
		})
	}
}

// A request forced to Fast by a group edit must be billed by that same edit, even
// while the key's routed group still carries the policy from before it.
func TestRecordUsage_UsesThePolicyTheRequestWasForcedWith(t *testing.T) {
	const model = "gpt-5.6-sol"
	f, apiKey := newTierBillingFixture(t, model, nil) // routed group: no Free Fast yet
	require.False(t, apiKey.Group.FreeOpenAIFast)

	free := &OpenAITierBillingPolicy{GroupID: f.groupID, FreeFast: true}
	row := f.record(t, apiKey, model, "priority", func(in *OpenAIRecordUsageInput) { in.TierPolicy = free })
	requireTierRow(t, row, "priority", f.standard*3, f.standard*0.5, 1)

	// A policy captured for another group is not applied to this one.
	other := &OpenAITierBillingPolicy{GroupID: f.groupID + 1, FreeFast: true}
	row = f.record(t, apiKey, model, "priority", func(in *OpenAIRecordUsageInput) { in.TierPolicy = other })
	requireTierRow(t, row, "priority", f.standard*3, f.standard*3*0.5, 3)
}

func TestRecordUsage_TierPolicyLeavesOtherTrafficAlone(t *testing.T) {
	const model = "gpt-5.6-sol"
	f, apiKey := newTierBillingFixture(t, model, func(g *Group) {
		g.FreeOpenAIFast = true
		g.OpenAIFastMultiplier = ptrTierFloat(1)
		g.OpenAIUltrafastMultiplier = ptrTierFloat(1)
	})

	t.Run("standard tier", func(t *testing.T) {
		row := f.record(t, apiKey, model, "", nil)
		require.Nil(t, row.ServiceTier)
		require.Nil(t, row.ServiceTierMultiplier)
		require.InDelta(t, f.standard*0.5, row.ActualCost, 1e-9)
	})
	t.Run("flex tier", func(t *testing.T) {
		row := f.record(t, apiKey, model, "flex", nil)
		require.Nil(t, row.ServiceTierMultiplier)
		require.InDelta(t, row.TotalCost*0.5, row.ActualCost, 1e-9)
	})
	t.Run("a non-OpenAI account keeps the price it was computed with", func(t *testing.T) {
		row := f.record(t, apiKey, model, "priority", func(in *OpenAIRecordUsageInput) {
			in.Account = &Account{ID: 3021, Platform: PlatformGrok, Type: AccountTypeAPIKey}
		})
		require.InDelta(t, row.TotalCost*0.5, row.ActualCost, 1e-9)
	})
	t.Run("a rejected upstream request is not billed at all", func(t *testing.T) {
		row := f.record(t, apiKey, model, "priority", func(in *OpenAIRecordUsageInput) { in.Result.NonBillableUpstreamError = true })
		require.Zero(t, row.TotalCost)
		require.Zero(t, row.ActualCost)
		require.Nil(t, row.ServiceTierMultiplier)
	})
}

// Hosted search is a per-call add-on: a group's tier multiplier re-prices the token
// charge only, and the search charge is added once, unscaled.
func TestRecordUsage_TierMultiplierDoesNotScaleHostedSearch(t *testing.T) {
	const model = "gpt-5.6-sol"
	pricePer1k := 10.0
	f, apiKey := newTierBillingFixture(t, model, func(g *Group) {
		g.OpenAIFastMultiplier = ptrTierFloat(2)
		g.SearchPricePer1k = &pricePer1k
	})

	row := f.record(t, apiKey, model, "priority", func(in *OpenAIRecordUsageInput) { in.Result.SearchCount = 5 })

	search := f.svc.billingService.CalculateSearchCost(5, &pricePer1k, 0.5)
	require.Greater(t, search.ActualCost, 0.0)
	require.InDelta(t, f.standard*2*0.5+search.ActualCost, row.ActualCost, 1e-9,
		"token charge at the group's Fast multiplier, search charged once at its own price")
	require.NotNil(t, row.ServiceTierMultiplier)
	require.Equal(t, 2.0, *row.ServiceTierMultiplier)
}
