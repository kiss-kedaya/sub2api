//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"mime/multipart"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDeepSeekStandaloneSearchAndFetchDefaultTo5Per1kNotCodex10Per1k(t *testing.T) {
	for _, tc := range []struct {
		name, label string
		rate        float64
		independent bool
		toolRate    float64
		wantTotal   float64
		wantActual  float64
		wantLogRate float64
	}{
		{"search/follow-0.05x", DeepSeekToolsLabelSearch, 0.05, false, 1.0, 0.005, 0.00025, 0.05},
		{"fetch/follow-0.05x", DeepSeekToolsLabelFetch, 0.05, false, 1.0, 0.005, 0.00025, 0.05},
		{"search/follow-0.30x", DeepSeekToolsLabelSearch, 0.30, false, 1.0, 0.005, 0.0015, 0.30},
		{"fetch/independent-0.5x", DeepSeekToolsLabelFetch, 0.30, true, 0.5, 0.005, 0.0025, 0.5},
		{"search/independent-0-free", DeepSeekToolsLabelSearch, 0.30, true, 0.0, 0.005, 0.0, 0.0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs, settlement := &openAIRecordUsageLogRepoStub{inserted: true}, &openAIRecordUsageBillingRepoStub{}
			svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(logs, settlement, nil, nil, nil)
			// All tool prices nil -> must use official DeepSeek 5/1k (0.005/call),
			// never Codex $0.01/call!
			group := &Group{
				ID: 116, Platform: PlatformDeepseek, RateMultiplier: tc.rate,
				ToolRateIndependent: tc.independent, ToolRateMultiplier: tc.toolRate,
			}
			key := &APIKey{ID: 7, GroupID: &group.ID, Group: group}
			require.NoError(t, svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
				Result: &OpenAIForwardResult{
					RequestID: "ds-" + tc.name, Model: tc.label,
					WebSearchCalls: 1, FixedToolBilling: true,
				},
				APIKey: key, User: &User{ID: 8},
				Account: &Account{ID: 9, Platform: PlatformDeepseek, Type: AccountTypeAPIKey},
			}))
			require.Equal(t, 1, logs.calls)
			require.Equal(t, 1, settlement.calls)
			require.InDelta(t, tc.wantTotal, logs.lastLog.TotalCost, 1e-12)
			require.InDelta(t, QuantizeUsageBillingAmount(tc.wantActual), logs.lastLog.ActualCost, 1e-12)
			require.InDelta(t, logs.lastLog.ActualCost, settlement.lastCmd.BalanceCost, 1e-12)
			require.Equal(t, tc.wantLogRate, logs.lastLog.RateMultiplier)
			require.NotNil(t, logs.lastLog.BillingMode)
			require.Equal(t, string(BillingModePerRequest), *logs.lastLog.BillingMode)
		})
	}
}

func TestDeepSeekToolPricingPrecedenceAndChannelOverrides(t *testing.T) {
	groupID := int64(99)
	chSearchPrice := 0.004
	chFetchPrice := 0.002
	chOCRPrice := 0.001
	chTTSPrice := 200.0
	chASRPrice := 0.50
	zero := 0.0

	makeResolver := func(chPricing []ChannelModelPricing) *ModelPricingResolver {
		ch := Channel{
			ID:           1,
			Status:       StatusActive,
			GroupIDs:     []int64{groupID},
			ModelPricing: chPricing,
		}
		cs := newTestChannelService(makeStandardRepo(ch, map[int64]string{groupID: PlatformDeepseek}))
		return NewModelPricingResolver(cs, newTestBillingService())
	}

	t.Run("channel per_request overrides official defaults for all 5 tools", func(t *testing.T) {
		resolver := makeResolver([]ChannelModelPricing{
			{Platform: PlatformDeepseek, Models: []string{DeepSeekToolsLabelSearch}, BillingMode: BillingModePerRequest, PerRequestPrice: &chSearchPrice},
			{Platform: PlatformDeepseek, Models: []string{DeepSeekToolsLabelFetch}, BillingMode: BillingModePerRequest, PerRequestPrice: &chFetchPrice},
			{Platform: PlatformDeepseek, Models: []string{DeepSeekToolsLabelOCR}, BillingMode: BillingModePerRequest, PerRequestPrice: &chOCRPrice},
			{Platform: PlatformDeepseek, Models: []string{DeepSeekToolsLabelTTS}, BillingMode: BillingModePerRequest, PerRequestPrice: &chTTSPrice},
			{Platform: PlatformDeepseek, Models: []string{DeepSeekToolsLabelASR}, BillingMode: BillingModePerRequest, PerRequestPrice: &chASRPrice},
		})
		g := &Group{ID: groupID, Platform: PlatformDeepseek, RateMultiplier: 0.5}
		for _, tc := range []struct {
			model string
			want  float64
		}{
			{DeepSeekToolsLabelSearch, chSearchPrice},
			{DeepSeekToolsLabelFetch, chFetchPrice},
			{DeepSeekToolsLabelOCR, chOCRPrice},
			{DeepSeekToolsLabelTTS, chTTSPrice},
			{DeepSeekToolsLabelASR, chASRPrice},
		} {
			got, ok := ResolveDeepSeekToolUnitPrice(context.Background(), resolver, g, tc.model)
			require.True(t, ok)
			require.InDelta(t, tc.want, got, 1e-12, tc.model)
		}
	})

	t.Run("channel explicit 0 means free and stops fallback", func(t *testing.T) {
		resolver := makeResolver([]ChannelModelPricing{
			{Platform: PlatformDeepseek, Models: []string{DeepSeekToolsLabelFetch}, BillingMode: BillingModePerRequest, PerRequestPrice: &zero},
			{Platform: PlatformDeepseek, Models: []string{DeepSeekToolsLabelOCR}, BillingMode: BillingModePerRequest, PerRequestPrice: &zero},
		})
		searchPrice := 6.0
		g := &Group{ID: groupID, Platform: PlatformDeepseek, SearchPricePer1k: &searchPrice}
		gotFetch, ok := ResolveDeepSeekToolUnitPrice(context.Background(), resolver, g, DeepSeekToolsLabelFetch)
		require.True(t, ok)
		require.Zero(t, gotFetch, "explicit 0 channel fetch card must override search fallback")

		gotOCR, ok := ResolveDeepSeekToolUnitPrice(context.Background(), resolver, g, DeepSeekToolsLabelOCR)
		require.True(t, ok)
		require.Zero(t, gotOCR)
	})

	t.Run("group dedicated fields beat channel cards and group per_request cards beat group columns", func(t *testing.T) {
		resolver := makeResolver([]ChannelModelPricing{
			{Platform: PlatformDeepseek, Models: []string{DeepSeekToolsLabelSearch}, BillingMode: BillingModePerRequest, PerRequestPrice: &chSearchPrice},
			{Platform: PlatformDeepseek, Models: []string{DeepSeekToolsLabelFetch}, BillingMode: BillingModePerRequest, PerRequestPrice: &chFetchPrice},
		})
		groupSearch1k := 3.0
		groupFetch1k := 1.2
		g := &Group{
			ID: groupID, Platform: PlatformDeepseek,
			SearchPricePer1k: &groupSearch1k,
			FetchPricePer1k:  &groupFetch1k,
		}
		gotSearch, _ := ResolveDeepSeekToolUnitPrice(context.Background(), resolver, g, DeepSeekToolsLabelSearch)
		require.InDelta(t, 0.003, gotSearch, 1e-12)
		gotFetch, _ := ResolveDeepSeekToolUnitPrice(context.Background(), resolver, g, DeepSeekToolsLabelFetch)
		require.InDelta(t, 0.0012, gotFetch, 1e-12)

		// Group per_request card beats dedicated column.
		cardPrice := 0.0007
		g.ModelPricing = []ChannelModelPricing{
			{Platform: PlatformDeepseek, Models: []string{DeepSeekToolsLabelFetch}, BillingMode: BillingModePerRequest, PerRequestPrice: &cardPrice},
		}
		gotFetch, _ = ResolveDeepSeekToolUnitPrice(context.Background(), resolver, g, DeepSeekToolsLabelFetch)
		require.InDelta(t, cardPrice, gotFetch, 1e-12)
	})

	t.Run("fetch falls back to search when fetch pricing is unset", func(t *testing.T) {
		resolver := makeResolver([]ChannelModelPricing{
			{Platform: PlatformDeepseek, Models: []string{DeepSeekToolsLabelSearch}, BillingMode: BillingModePerRequest, PerRequestPrice: &chSearchPrice},
		})
		g := &Group{ID: groupID, Platform: PlatformDeepseek}
		gotFetch, _ := ResolveDeepSeekToolUnitPrice(context.Background(), resolver, g, DeepSeekToolsLabelFetch)
		require.InDelta(t, chSearchPrice, gotFetch, 1e-12, "unset fetch inherits channel search card")

		groupSearch1k := 2.5
		g.SearchPricePer1k = &groupSearch1k
		gotFetch, _ = ResolveDeepSeekToolUnitPrice(context.Background(), resolver, g, DeepSeekToolsLabelFetch)
		require.InDelta(t, 0.0025, gotFetch, 1e-12, "unset fetch inherits group search_price_per_1k")

		g.SearchPricePer1k = &zero
		gotFetch, _ = ResolveDeepSeekToolUnitPrice(context.Background(), resolver, g, DeepSeekToolsLabelFetch)
		require.Zero(t, gotFetch, "unset fetch inherits explicit 0 group search_price_per_1k")
	})
}

func TestDeepSeekHostedMixedSearchAndFetchDistinctPricingAndPeakExclusion(t *testing.T) {
	billing := newTestBillingService()
	search1k, fetch1k := 4.0, 2.0
	group := &Group{
		ID: 99, Platform: PlatformDeepseek,
		SubscriptionType:   SubscriptionTypeSubscription,
		RateMultiplier:     0.5,
		PeakRateEnabled:    true,
		PeakStart:          "00:00",
		PeakEnd:            "23:59",
		PeakRateMultiplier: 2.0, // peak applies to tokens, NEVER tools
		SearchPricePer1k:   &search1k,
		FetchPricePer1k:    &fetch1k,
	}
	key := &APIKey{ID: 7, Group: group, GroupID: &group.ID}
	tokens := UsageTokens{InputTokens: 1000, OutputTokens: 500}
	tokenPeak, err := billing.CalculateCost("claude-sonnet-4", tokens, 1.0) // 0.5 * 2.0 peak
	require.NoError(t, err)

	// 2 searches @ 4/1k = 0.008; 3 fetches @ 2/1k = 0.006 -> base 0.014, actual @ 0.5x (no peak!) = 0.007.
	wantToolTotal := 2*0.004 + 3*0.002
	wantToolActual := wantToolTotal * 0.5

	openAI := &OpenAIGatewayService{billingService: billing}
	cost, err := openAI.calculateOpenAIRecordUsageCost(
		context.Background(),
		&OpenAIForwardResult{SearchCount: 2, FetchCount: 3, FixedToolBilling: true},
		key, []string{"claude-sonnet-4"},
		1.0, 0.5, 0.5, 0.5, tokens, "", boolPtr(false), time.Now(),
	)
	require.NoError(t, err)
	require.InDelta(t, tokenPeak.TotalCost+wantToolTotal, cost.TotalCost, 1e-12)
	require.InDelta(t, tokenPeak.ActualCost+wantToolActual, cost.ActualCost, 1e-12)

	gateway := &GatewayService{billingService: billing}
	anthropic := gateway.calculateRecordUsageCostWithToolMultiplier(
		context.Background(),
		&ForwardResult{
			Model: "claude-sonnet-4", FixedToolBilling: true, SearchCount: 2, FetchCount: 3,
			Usage: ClaudeUsage{InputTokens: 1000, OutputTokens: 500},
		},
		key, "claude-sonnet-4", 1.0, 0.5, 0.5, time.Now(),
	)
	require.InDelta(t, tokenPeak.TotalCost+wantToolTotal, anthropic.TotalCost, 1e-12)
	require.InDelta(t, tokenPeak.ActualCost+wantToolActual, anthropic.ActualCost, 1e-12)
}

func TestEstimateDeepSeekOCRPages(t *testing.T) {
	wrapMultipart := func(filename string, content []byte) (string, []byte) {
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		part, err := w.CreateFormFile("file", filename)
		require.NoError(t, err)
		_, err = part.Write(content)
		require.NoError(t, err)
		require.NoError(t, w.Close())
		return w.FormDataContentType(), buf.Bytes()
	}

	t.Run("raster images reserve 1 page regardless of size", func(t *testing.T) {
		bigPayload := bytes.Repeat([]byte{0x42}, 512<<10)
		samples := []struct {
			name   string
			header []byte
		}{
			{"png", []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}},
			{"jpeg", []byte{0xff, 0xd8, 0xff, 0xe0}},
			{"webp", []byte("RIFF\x00\x00\x00\x00WEBP")},
			{"gif", []byte("GIF89a")},
			{"bmp", []byte("BM\x00\x00")},
		}
		for _, sample := range samples {
			ct, body := wrapMultipart("img."+sample.name, append(append([]byte(nil), sample.header...), bigPayload...))
			require.Equal(t, 1, EstimateDeepSeekOCRPages(ct, body), sample.name)
		}
	})

	t.Run("PDF counts /Type /Page while ignoring /Type /Pages and /Type /PageLabel", func(t *testing.T) {
		pdf := "%PDF-1.4\n1 0 obj << /Type /Pages /Count 3 >> endobj\n" +
			"2 0 obj << /Type /PageLabel >> endobj\n" +
			"3 0 obj << /Type /Page /Parent 1 0 R >> endobj\n" +
			"4 0 obj << /Type/Page /Parent 1 0 R >> endobj\n" +
			"5 0 obj << /Type\n/Page /Parent 1 0 R >> endobj\n"
		ct, body := wrapMultipart("doc.pdf", []byte(pdf))
		require.Equal(t, 3, EstimateDeepSeekOCRPages(ct, body))
	})

	t.Run("compressed PDF without plain /Type /Page falls back to size estimate clamped to [1,1000]", func(t *testing.T) {
		pdf := append([]byte("%PDF-1.7\n"), bytes.Repeat([]byte("Z"), 250<<10)...)
		ct, body := wrapMultipart("compressed.pdf", pdf)
		require.Equal(t, 3, EstimateDeepSeekOCRPages(ct, body))
	})

	t.Run("huge PDF is capped at 1000 pages", func(t *testing.T) {
		var b strings.Builder
		b.WriteString("%PDF-1.4\n")
		for i := range 1200 {
			_, _ = fmt.Fprintf(&b, "%d 0 obj << /Type /Page >> endobj\n", i+1)
		}
		ct, body := wrapMultipart("huge.pdf", []byte(b.String()))
		require.Equal(t, 1000, EstimateDeepSeekOCRPages(ct, body))
	})

	t.Run("empty or malformed upload returns 1", func(t *testing.T) {
		require.Equal(t, 1, EstimateDeepSeekOCRPages("", nil))
		require.Equal(t, 1, EstimateDeepSeekOCRPages("multipart/form-data; boundary=broken", []byte("not-multipart")))
	})
}

func TestDeepSeekToolPricingSurvivesAuthCacheSnapshotAndRollingGuard(t *testing.T) {
	svc := &APIKeyService{}
	fetchPrice := 2.5
	group := &Group{
		ID: 99, Platform: PlatformDeepseek, RateMultiplier: 0.3,
		ToolRateIndependent: true, ToolRateMultiplier: 0.8,
		FetchPricePer1k: &fetchPrice,
	}
	key := &APIKey{Group: group, GroupID: &group.ID, User: &User{ID: 1}}
	snapshot := svc.snapshotFromAPIKey(context.Background(), key)
	require.True(t, snapshot.Group.ToolPricingConfigLoaded)

	encoded, err := json.Marshal(snapshot)
	require.NoError(t, err)
	var decoded APIKeyAuthSnapshot
	require.NoError(t, json.Unmarshal(encoded, &decoded))

	loaded, used, err := svc.applyAuthCacheEntry("test-key-must-not-leak", &APIKeyAuthCacheEntry{Snapshot: &decoded})
	require.NoError(t, err)
	require.True(t, used)
	require.True(t, loaded.Group.ToolRateIndependent)
	require.Equal(t, 0.8, loaded.Group.ToolRateMultiplier)
	require.NotNil(t, loaded.Group.FetchPricePer1k)
	require.Equal(t, 2.5, *loaded.Group.FetchPricePer1k)

	// Rolling upgrade guard: non-DeepSeek snapshot without ToolPricingConfigLoaded stays valid.
	other := decoded
	otherGroup := *decoded.Group
	otherGroup.Platform = PlatformOpenAI
	otherGroup.ToolPricingConfigLoaded = false
	other.Group = &otherGroup
	_, used, err = svc.applyAuthCacheEntry("test-key-must-not-leak", &APIKeyAuthCacheEntry{Snapshot: &other})
	require.NoError(t, err)
	require.True(t, used)

	// Clones keep deep copies.
	for _, cp := range []*Group{cloneSchedulerGroup(group), cloneGroupForDuplicate(group, "op-1")} {
		require.True(t, cp.ToolRateIndependent)
		require.Equal(t, 0.8, cp.ToolRateMultiplier)
		require.NotNil(t, cp.FetchPricePer1k)
		require.Equal(t, 2.5, *cp.FetchPricePer1k)
		require.NotSame(t, group.FetchPricePer1k, cp.FetchPricePer1k)
	}
}

func TestDeepSeekToolPricingAdversarialRegressions(t *testing.T) {
	t.Run("non-DeepSeek groups ignore ToolRateIndependent and non-finite multipliers are sanitized", func(t *testing.T) {
		grok := &Group{ID: 7, Platform: PlatformGrok, ToolRateIndependent: true, ToolRateMultiplier: 0.2}
		require.InDelta(t, 0.5, ResolveGroupToolRateMultiplier(grok, 0.5), 1e-12)
		dsNaN := &Group{ID: 8, Platform: PlatformDeepseek, ToolRateIndependent: true, ToolRateMultiplier: math.NaN()}
		require.InDelta(t, 1.0, ResolveGroupToolRateMultiplier(dsNaN, 0.5), 1e-12)
		dsInf := &Group{ID: 9, Platform: PlatformDeepseek, ToolRateIndependent: true, ToolRateMultiplier: math.Inf(1)}
		require.InDelta(t, 1.0, ResolveGroupToolRateMultiplier(dsInf, 0.5), 1e-12)
		require.InDelta(t, 1.0, ResolveGroupToolRateMultiplier(nil, math.NaN()), 1e-12)
	})

	t.Run("wildcard per_request cards do not match heterogeneous DeepSeek tool units", func(t *testing.T) {
		wildcardPrice := 0.005
		exactOCRPrice := 0.002
		group := &Group{
			ID: 55, Platform: PlatformDeepseek, RateMultiplier: 1,
			ModelPricing: []ChannelModelPricing{{
				Platform:        PlatformDeepseek,
				Models:          []string{"deepseek-*", "deepseek-tools-*"},
				BillingMode:     BillingModePerRequest,
				PerRequestPrice: &wildcardPrice,
			}},
		}
		ch := Channel{
			ID: 1, Name: "ch", Status: StatusActive, GroupIDs: []int64{55},
			ModelPricing: []ChannelModelPricing{
				{
					Platform:        PlatformDeepseek,
					Models:          []string{"deepseek-*"},
					BillingMode:     BillingModePerRequest,
					PerRequestPrice: &wildcardPrice,
				},
				{
					Platform:        PlatformDeepseek,
					Models:          []string{DeepSeekToolsLabelOCR},
					BillingMode:     BillingModePerRequest,
					PerRequestPrice: &exactOCRPrice,
				},
			},
		}
		chSvc := newTestChannelService(makeStandardRepo(ch, map[int64]string{55: PlatformDeepseek}))
		resolver := NewModelPricingResolver(chSvc, newTestBillingService())

		// TTS must NOT be hijacked to 0.005 by wildcard deepseek-* card; keeps official 300.
		ttsPrice, ok := ResolveDeepSeekToolUnitPrice(context.Background(), resolver, group, DeepSeekToolsLabelTTS)
		require.True(t, ok)
		require.InDelta(t, 300.0, ttsPrice, 1e-12)
		// Exact OCR channel card still applies.
		ocrPrice, ok := ResolveDeepSeekToolUnitPrice(context.Background(), resolver, group, DeepSeekToolsLabelOCR)
		require.True(t, ok)
		require.InDelta(t, exactOCRPrice, ocrPrice, 1e-12)
	})

	t.Run("zero-token hosted DeepSeek search/fetch partial result records toolMultiplier on UsageLog", func(t *testing.T) {
		logs, settlement := &openAIRecordUsageLogRepoStub{inserted: true}, &openAIRecordUsageBillingRepoStub{}
		svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(logs, settlement, nil, nil, nil)
		groupID := int64(99)
		group := &Group{
			ID: groupID, Platform: PlatformDeepseek, RateMultiplier: 0.30,
			ToolRateIndependent: true, ToolRateMultiplier: 1.0,
		}
		key := &APIKey{ID: 1, UserID: 2, GroupID: &groupID, Group: group}
		err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
			Result: &OpenAIForwardResult{
				RequestID:        "req-ds-zero-token-hosted",
				Model:            "deepseek-chat",
				SearchCount:      1,
				FetchCount:       1,
				FixedToolBilling: true,
			},
			APIKey:  key,
			User:    &User{ID: 2},
			Account: &Account{ID: 3, Platform: PlatformDeepseek, Type: AccountTypeAPIKey},
		})
		require.NoError(t, err)
		require.Equal(t, 1, logs.calls)
		require.InDelta(t, 0.01, logs.lastLog.ActualCost, 1e-12)
		require.InDelta(t, 1.0, logs.lastLog.RateMultiplier, 1e-12, "zero-token hosted tool charge must record toolMultiplier")
	})

	t.Run("streaming OCR PDF scanner counts /Type /Page straddling the 32 KiB buffer boundary", func(t *testing.T) {
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		fw, err := w.CreateFormFile("file", "boundary.pdf")
		require.NoError(t, err)
		// Place `/Type /Page` right across the 32 KiB chunk boundary AND `/Type /Pages` right across 64 KiB boundary.
		var pdf bytes.Buffer
		pdf.WriteString("%PDF-1.4\n")
		// Pad so "/Type" starts at exact offset (deepSeekOCRStreamBufSize - deepSeekOCRPDFScanTailBytes - 2)
		pad1 := deepSeekOCRStreamBufSize - deepSeekOCRPDFScanTailBytes - 2 - pdf.Len()
		pdf.Write(bytes.Repeat([]byte("a"), pad1))
		pdf.WriteString("/Type /Page ")
		pad2 := 2*deepSeekOCRStreamBufSize - deepSeekOCRPDFScanTailBytes - 2 - pdf.Len()
		pdf.Write(bytes.Repeat([]byte("b"), pad2))
		pdf.WriteString("/Type /Pages ")
		pdf.WriteString("\n/Type /Page\n")
		_, err = fw.Write(pdf.Bytes())
		require.NoError(t, err)
		require.NoError(t, w.Close())

		require.Equal(t, 2, EstimateDeepSeekOCRPages(w.FormDataContentType(), buf.Bytes()))
	})

	t.Run("inflight reservation still reserves hosted DeepSeek search when user rate is 0 and tool_rate_independent is true", func(t *testing.T) {
		groupID := int64(99)
		group := &Group{
			ID: groupID, Platform: PlatformDeepseek, RateMultiplier: 0.30,
			ToolRateIndependent: true, ToolRateMultiplier: 1.0,
		}
		deps := inflightEstimateDeps{
			billing: newTestBillingService(),
			userGroupRate: func(_ context.Context, _ int64, _ int64, _ float64) float64 {
				return 0
			},
		}
		apiKey := &APIKey{ID: 1, UserID: 2, GroupID: &groupID, Group: group, User: &User{ID: 2}}
		cost, ok := deps.estimate(context.Background(), apiKey, InflightEstimateRequest{
			Model:       "deepseek-chat",
			BodyBytes:   1000,
			SearchCalls: 2,
		})
		require.True(t, ok)
		require.InDelta(t, 0.01, cost, 1e-12, "hosted search under independent tool multiplier must be reserved even when token rate is 0")
	})
}
