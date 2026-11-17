//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func recordCacheCreationSplitForTest(t *testing.T, usage OpenAIUsage) *UsageLog {
	t.Helper()
	repo := &openAIRecordUsageLogRepoStub{inserted: true}
	svc := newOpenAIRecordUsageServiceForTest(repo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
	svc.billingService = newTestBillingServiceWithPrices(map[string]*ModelPricing{
		"claude-sonnet-4": {CacheCreationPricePerToken: 2.5e-6, CacheCreation5mPrice: 2.5e-6, CacheCreation1hPrice: 4e-6, SupportsCacheBreakdown: true},
	})
	require.NoError(t, svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{Model: "claude-sonnet-4", Usage: usage},
		APIKey: &APIKey{ID: 2}, User: &User{ID: 1}, Account: &Account{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
	}))
	require.NotNil(t, repo.lastLog)
	return repo.lastLog
}

func TestOpenAICacheCreationSplitPartialBuckets(t *testing.T) {
	for _, tc := range []struct {
		name               string
		five, hour, want5m int
		want               float64
	}{
		{"5m only", 80, 0, 200, 0.00050000},
		{"1h only", 0, 120, 80, 0.00068000},
		{"both", 50, 100, 100, 0.00065000},
		{"no split", 0, 0, 0, 0.00050000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := recordCacheCreationSplitForTest(t, OpenAIUsage{CacheCreationInputTokens: 200, CacheCreation5mTokens: tc.five, CacheCreation1hTokens: tc.hour})
			t.Logf("cache cost=%.8f expected=%.8f", log.CacheCreationCost, tc.want)
			require.InDelta(t, tc.want, log.CacheCreationCost, 1e-12)
			require.Equal(t, tc.want5m, log.CacheCreation5mTokens)
			require.Equal(t, tc.hour, log.CacheCreation1hTokens)
			// Account statistics receive the same normalized tokens as billing.
			stats := calculateTokenStatsCost(&ChannelModelPricing{CacheWritePrice: testPtrFloat64(2.5e-6), CacheWrite1hPrice: testPtrFloat64(4e-6)}, UsageTokens{CacheCreationTokens: 200, CacheCreation5mTokens: log.CacheCreation5mTokens, CacheCreation1hTokens: log.CacheCreation1hTokens})
			require.NotNil(t, stats)
			require.InDelta(t, tc.want, *stats, 1e-12)
		})
	}
}

func TestOpenAICacheCreationSplitPerTurnTotals(t *testing.T) {
	for _, hosted := range []bool{false, true} {
		for _, total := range []int{0, -1, 100} {
			t.Run(fmt.Sprintf("hosted=%t/total=%d", hosted, total), func(t *testing.T) {
				first := OpenAIUsage{CacheCreationInputTokens: total, CacheCreation1hTokens: 200}
				second := OpenAIUsage{CacheCreationInputTokens: 200, CacheCreation5mTokens: 200}
				var combined OpenAIUsage
				if hosted {
					state := &deepSeekHostedSearchState{}
					state.addResult(&OpenAIForwardResult{Usage: first})
					state.addResult(&OpenAIForwardResult{Usage: second})
					combined = state.result.Usage
				} else {
					addOpenAIUsage(&combined, first)
					addOpenAIUsage(&combined, second)
				}
				want := recordCacheCreationSplitForTest(t, first).CacheCreationCost + recordCacheCreationSplitForTest(t, second).CacheCreationCost
				actual := recordCacheCreationSplitForTest(t, combined)
				t.Logf("combined=%.8f separate=%.8f", actual.CacheCreationCost, want)
				require.InDelta(t, want, actual.CacheCreationCost, 1e-12)
			})
		}
	}
}

func TestOpenAICacheCreationSplitProgressivePresence(t *testing.T) {
	for _, ws := range []bool{false, true} {
		for _, tc := range []struct {
			name, update   string
			want5m, want1h int
			wantCost       float64
		}{
			{"explicit zero", `"cache_creation":{"ephemeral_5m_input_tokens":200,"ephemeral_1h_input_tokens":0}`, 200, 0, 0.00050000},
			{"missing bucket", `"output_tokens":1`, 0, 200, 0.00080000},
			{"missing 1h retains it", `"cache_creation":{"ephemeral_5m_input_tokens":0}`, 0, 200, 0.00080000},
			{"explicit total zero", `"cache_creation_input_tokens":0`, 0, 0, 0},
		} {
			t.Run(fmt.Sprintf("ws=%t/%s", ws, tc.name), func(t *testing.T) {
				var usage OpenAIUsage
				parse := func(raw string) {
					if ws {
						parseOpenAIWSResponseUsageFromCompletedEvent([]byte(raw), &usage)
					} else {
						(&OpenAIGatewayService{}).parseSSEUsage(raw, &usage)
					}
				}
				parse(`{"type":"response.in_progress","response":{"usage":{"input_tokens":1000,"cache_creation_input_tokens":200,"cache_creation":{"ephemeral_1h_input_tokens":200}}}}`)
				parse(`{"type":"response.in_progress","response":{"usage":{` + tc.update + `}}}`)
				parse(`{"type":"response.completed","response":{"id":"resp_cache","output":[]}}`)
				log := recordCacheCreationSplitForTest(t, usage)
				t.Logf("cache cost=%.8f expected=%.8f", log.CacheCreationCost, tc.wantCost)
				require.Equal(t, tc.want5m, log.CacheCreation5mTokens)
				require.Equal(t, tc.want1h, log.CacheCreation1hTokens)
				require.InDelta(t, tc.wantCost, log.CacheCreationCost, 1e-12)
			})
		}
	}
}

func TestOpenAICacheCreationSplitTerminalPresence(t *testing.T) {
	for _, ws := range []bool{false, true} {
		for _, tc := range []struct {
			name, final string
			want1h      int
			wantCost    float64
		}{
			{"missing cache fields", `"input_tokens":1000,"output_tokens":2`, 200, 0.00080000},
			{"missing total with 5m correction", `"input_tokens":1000,"output_tokens":2,"cache_creation":{"ephemeral_5m_input_tokens":200,"ephemeral_1h_input_tokens":0}`, 0, 0.00050000},
			{"explicit total zero", `"cache_creation_input_tokens":0`, 0, 0},
		} {
			t.Run(fmt.Sprintf("ws=%t/%s", ws, tc.name), func(t *testing.T) {
				var usage OpenAIUsage
				parse := func(raw string) {
					if ws {
						parseOpenAIWSResponseUsageFromCompletedEvent([]byte(raw), &usage)
					} else {
						(&OpenAIGatewayService{}).parseSSEUsage(raw, &usage)
					}
				}
				parse(`{"type":"response.in_progress","usage":{"input_tokens":1000,"cache_creation_input_tokens":200,"cache_creation":{"ephemeral_1h_input_tokens":200}}}`)
				parse(`{"type":"response.completed","usage":{` + tc.final + `}}`)
				log := recordCacheCreationSplitForTest(t, usage)
				require.Equal(t, tc.want1h, log.CacheCreation1hTokens)
				require.InDelta(t, tc.wantCost, log.CacheCreationCost, 1e-12)
			})
		}
	}
}

func TestOpenAICacheCreationSplitTTLOverrides(t *testing.T) {
	previous := gatewayForwardingCache.Load()
	if previous == nil {
		previous = &cachedGatewayForwardingSettings{}
	}
	t.Cleanup(func() { gatewayForwardingCache.Store(previous) })
	for _, tc := range []struct {
		name, keyMode      string
		global             bool
		extra              map[string]any
		upstream1h, want1h int
		overridden         bool
	}{
		{"global injection", AnthropicCacheTTLModeInherit, true, nil, 200, 0, true},
		{"account 5m", AnthropicCacheTTLModeInherit, false, map[string]any{"cache_ttl_override_enabled": true, "cache_ttl_override_target": "5m"}, 200, 0, true},
		{"key 5m", AnthropicCacheTTLMode5m, false, nil, 200, 0, true},
		{"key off", AnthropicCacheTTLModeOff, true, nil, 200, 200, false},
		{"key 1h without explicit 1h", AnthropicCacheTTLMode1h, false, nil, 0, 0, false},
		{"key 1h keeps explicit 1h", AnthropicCacheTTLMode1h, false, nil, 120, 120, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &openAIRecordUsageLogRepoStub{inserted: true}
			svc := newOpenAIRecordUsageServiceForTest(repo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
			svc.billingService = newTestBillingServiceWithPrices(map[string]*ModelPricing{"claude-sonnet-4": {CacheCreationPricePerToken: 2.5e-6, CacheCreation5mPrice: 2.5e-6, CacheCreation1hPrice: 4e-6, SupportsCacheBreakdown: true}})
			value := "false"
			if tc.global {
				value = "true"
			}
			svc.settingService = NewSettingService(&gatewayTTLSettingRepo{data: map[string]string{SettingKeyEnableAnthropicCacheTTL1hInjection: value}}, &config.Config{})
			gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{})
			account := &Account{ID: 3, Platform: PlatformAnthropic, Type: AccountTypeOAuth, Extra: tc.extra}
			key := &APIKey{ID: 2, AnthropicCacheTTLMode: tc.keyMode}
			usage := OpenAIUsage{CacheCreationInputTokens: 200, CacheCreation1hTokens: tc.upstream1h}
			require.NoError(t, svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{Result: &OpenAIForwardResult{Model: "claude-sonnet-4", Usage: usage}, APIKey: key, User: &User{ID: 1}, Account: account}))
			want := float64(200-tc.want1h)*2.5e-6 + float64(tc.want1h)*4e-6
			t.Logf("cache cost=%.8f expected=%.8f", repo.lastLog.CacheCreationCost, want)
			require.InDelta(t, want, repo.lastLog.CacheCreationCost, 1e-12)
			require.Equal(t, tc.want1h, repo.lastLog.CacheCreation1hTokens)
			require.Equal(t, tc.overridden, repo.lastLog.CacheTTLOverridden)
		})
	}
}

func TestOpenAICacheCreationSplitAnthropicGroupKey5m(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const request = `{"model":"claude-sonnet-4","max_tokens":32,"messages":[{"role":"user","content":"hi"}]}`
	account := nativeAnthropicTestAccount()
	key := &APIKey{ID: 2, GroupID: i64p(10), Group: &Group{ID: 10, Platform: PlatformAnthropic, RateMultiplier: 1, ModelPricing: []ChannelModelPricing{{Models: []string{"claude-sonnet-4"}, CacheWritePrice: testPtrFloat64(2.5e-6), CacheWrite1hPrice: testPtrFloat64(4e-6)}}}, AnthropicCacheTTLMode: AnthropicCacheTTLMode5m}
	c := adaptiveProtocolTestContext("/v1/messages", []byte(request))
	c.Set("api_key", key)
	repo := &openAIRecordUsageLogRepoStub{inserted: true}
	svc := newOpenAIRecordUsageServiceForTest(repo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
	svc.StopOpenAICodexTicketHarvester()
	svc.cfg = rawChatCompletionsTestConfig()
	svc.httpUpstream = &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"msg_cache","type":"message","role":"assistant","model":"claude-sonnet-4","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":700,"output_tokens":50,"cache_creation_input_tokens":200,"cache_creation":{"ephemeral_1h_input_tokens":200}}}`))}}
	svc.billingService = newTestBillingServiceWithPrices(map[string]*ModelPricing{"claude-sonnet-4": {CacheCreationPricePerToken: 2.5e-6, CacheCreation5mPrice: 2.5e-6, CacheCreation1hPrice: 4e-6, SupportsCacheBreakdown: true}})
	svc.resolver = NewModelPricingResolver(nil, svc.billingService)
	result, err := svc.ForwardAsAnthropic(context.Background(), c, account, []byte(request), "", "")
	require.NoError(t, err)
	require.NoError(t, svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{Result: result, APIKey: key, User: &User{ID: 1}, Account: account}))
	require.InDelta(t, 0.00050000, repo.lastLog.CacheCreationCost, 1e-12)
	require.True(t, repo.lastLog.CacheTTLOverridden)
	require.Zero(t, repo.lastLog.CacheCreation1hTokens)
}

func TestOpenAICacheCreationSplitPassthroughWebSocket(t *testing.T) {
	gin.SetMode(gin.TestMode)
	controlCtx, cancel := context.WithCancelCause(context.Background())
	defer cancel(context.Canceled)
	upstream := newStagedPassthroughConn()
	const payload = `{"type":"response.completed","response":{"id":"resp_cache_ws","model":"claude-sonnet-4","usage":{"input_tokens":1000,"output_tokens":50,"cache_creation_input_tokens":200,"cache_creation":{"ephemeral_5m_input_tokens":80,"ephemeral_1h_input_tokens":120}}}}`
	upstream.Send(payload)
	results := make(chan *OpenAIForwardResult, 2)
	hooks := &OpenAIWSIngressHooks{AfterTurn: func(_ int, result *OpenAIForwardResult, _ error) { results <- result }}
	server, serverErr := startPassthroughHookRecordingServer(t, controlCtx, newPassthroughLifecycleService(passthroughLifecycleConfig(), upstream), passthroughLifecycleAccount(), hooks)
	defer server.Close()
	client := dialPassthroughLifecycleClientWithPayload(t, server, `{"type":"response.create","model":"claude-sonnet-4","stream":false}`)
	defer func() {
		if err := client.CloseNow(); err != nil && !strings.Contains(err.Error(), "closed network connection") {
			t.Error(err)
		}
	}()
	frame, err := readPassthroughLifecycleFrame(t, client, 3*time.Second)
	require.NoError(t, err)
	require.Equal(t, payload, string(frame), "billing must not change client frames")
	var result *OpenAIForwardResult
	select {
	case result = <-results:
	case <-time.After(3 * time.Second):
		t.Fatal("turn result not received")
	}
	require.NotNil(t, result)
	log := recordCacheCreationSplitForTest(t, result.Usage)
	require.NoError(t, client.CloseNow())
	cancel(context.Canceled)
	select {
	case <-serverErr:
	case <-time.After(3 * time.Second):
		t.Fatal("relay did not stop")
	}
	t.Logf("cache cost=%.8f expected=0.00068000", log.CacheCreationCost)
	require.Equal(t, 120, result.Usage.CacheCreation1hTokens)
	require.InDelta(t, 0.00068000, log.CacheCreationCost, 1e-12)
}

func TestOpenAICacheCreationSplitRecordUsage(t *testing.T) {
	for _, tc := range []struct {
		name, raw      string
		native         bool
		want5m, want1h int
	}{
		{"total only stays 5m", `{"input_tokens":1000,"output_tokens":50,"cache_creation_input_tokens":200,"cache_read_input_tokens":100}`, false, 0, 0},
		{"upstream 1h", `{"input_tokens":1000,"output_tokens":50,"cache_creation_input_tokens":200,"cache_read_input_tokens":100,"cache_creation":{"ephemeral_1h_input_tokens":200}}`, false, 0, 200},
		{"mixed upstream buckets", `{"input_tokens":1000,"output_tokens":50,"cache_creation_input_tokens":200,"cache_read_input_tokens":100,"cache_creation":{"ephemeral_5m_input_tokens":80,"ephemeral_1h_input_tokens":120}}`, false, 80, 120},
		{"native conversion", `{"input_tokens":700,"output_tokens":50,"cache_creation_input_tokens":200,"cache_read_input_tokens":100,"cache_creation":{"ephemeral_5m_input_tokens":80,"ephemeral_1h_input_tokens":120}}`, true, 80, 120},
		{"contradictory upstream split capped", `{"input_tokens":1000,"output_tokens":50,"cache_creation_input_tokens":200,"cache_read_input_tokens":100,"cache_creation":{"ephemeral_5m_input_tokens":180,"ephemeral_1h_input_tokens":120}}`, false, 120, 80},
		{"negative split ignored", `{"input_tokens":1000,"output_tokens":50,"cache_creation_input_tokens":200,"cache_read_input_tokens":100,"cache_creation":{"ephemeral_5m_input_tokens":-20,"ephemeral_1h_input_tokens":-1}}`, false, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var usage OpenAIUsage
			if tc.native {
				usage = claudeUsageToOpenAIUsage(parseClaudeUsageFromResponseBody([]byte(`{"usage":` + tc.raw + `}`)))
			} else {
				var ok bool
				usage, ok = extractOpenAIUsageFromJSONBytes([]byte(`{"usage":` + tc.raw + `}`))
				require.True(t, ok)
			}
			repo := &openAIRecordUsageLogRepoStub{inserted: true}
			userRepo := &openAIRecordUsageUserRepoStub{}
			svc := newOpenAIRecordUsageServiceForTest(repo, userRepo, &openAIRecordUsageSubRepoStub{}, nil)
			svc.billingService = newTestBillingServiceWithPrices(map[string]*ModelPricing{
				"claude-sonnet-4": {
					InputPricePerToken: 2e-6, OutputPricePerToken: 10e-6,
					CacheCreationPricePerToken: 2.5e-6, CacheReadPricePerToken: 0.2e-6,
					SupportsCacheBreakdown: true, CacheCreation5mPrice: 2.5e-6, CacheCreation1hPrice: 4e-6,
				},
			})
			err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
				Result:  &OpenAIForwardResult{Model: "claude-sonnet-4", Usage: usage},
				APIKey:  &APIKey{ID: 2},
				User:    &User{ID: 1},
				Account: &Account{ID: 3, Platform: PlatformAnthropic, Type: AccountTypeAPIKey},
			})
			require.NoError(t, err)
			require.NotNil(t, repo.lastLog)
			require.Equal(t, 700, repo.lastLog.InputTokens)
			require.Equal(t, 200, repo.lastLog.CacheCreationTokens)
			require.Equal(t, tc.want5m, repo.lastLog.CacheCreation5mTokens)
			require.Equal(t, tc.want1h, repo.lastLog.CacheCreation1hTokens)
			wantCacheCost := 200 * 2.5e-6
			if tc.want5m+tc.want1h > 0 {
				wantCacheCost = float64(tc.want5m)*2.5e-6 + float64(tc.want1h)*4e-6
			}
			require.InDelta(t, wantCacheCost, repo.lastLog.CacheCreationCost, 1e-12)
			require.InDelta(t, (700*2e-6+50*10e-6+100*0.2e-6+wantCacheCost)*1.1, userRepo.lastAmount, 1e-8)
			require.False(t, repo.lastLog.CacheTTLOverridden, "preserving upstream details is not a TTL pricing override")
		})
	}
}

func TestOpenAICacheCreationSplitExplicitZeroTotal(t *testing.T) {
	repo := &openAIRecordUsageLogRepoStub{inserted: true}
	svc := newOpenAIRecordUsageServiceForTest(repo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
	svc.billingService = newTestBillingServiceWithPrices(map[string]*ModelPricing{
		"claude-sonnet-4": {SupportsCacheBreakdown: true, CacheCreation5mPrice: 2.5e-6, CacheCreation1hPrice: 4e-6},
	})
	usage, ok := extractOpenAIUsageFromJSONBytes([]byte(`{"usage":{"input_tokens":1000,"input_tokens_details":{"cache_write_tokens":0},"cache_creation_input_tokens":200,"cache_creation":{"ephemeral_1h_input_tokens":200}}}`))
	require.True(t, ok)
	require.NoError(t, svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{Model: "claude-sonnet-4", Usage: usage},
		APIKey: &APIKey{ID: 2}, User: &User{ID: 1}, Account: &Account{ID: 3},
	}))
	require.Zero(t, repo.lastLog.CacheCreationCost, "an authoritative zero total must not acquire a cache write charge")
}

func TestOpenAICacheCreationSplitConversions(t *testing.T) {
	t.Run("summed usage retains both buckets", func(t *testing.T) {
		dst := OpenAIUsage{CacheCreationInputTokens: 200, CacheCreation5mTokens: 80, CacheCreation1hTokens: 120}
		addOpenAIUsage(&dst, OpenAIUsage{CacheCreationInputTokens: 100, CacheCreation5mTokens: 40, CacheCreation1hTokens: 60})
		require.Equal(t, 120, dst.CacheCreation5mTokens)
		require.Equal(t, 180, dst.CacheCreation1hTokens)
		state := &deepSeekHostedSearchState{}
		state.addResult(&OpenAIForwardResult{Usage: OpenAIUsage{CacheCreationInputTokens: 200, CacheCreation5mTokens: 80, CacheCreation1hTokens: 120}})
		state.addResult(&OpenAIForwardResult{Usage: OpenAIUsage{CacheCreationInputTokens: 100, CacheCreation5mTokens: 40, CacheCreation1hTokens: 60}})
		require.Equal(t, dst.CacheCreation5mTokens, state.result.Usage.CacheCreation5mTokens)
		require.Equal(t, dst.CacheCreation1hTokens, state.result.Usage.CacheCreation1hTokens)
	})
	for _, tc := range []struct {
		name, raw      string
		want5m, want1h int
	}{
		{"both", `{"ephemeral_5m_input_tokens":80,"ephemeral_1h_input_tokens":120}`, 80, 120},
		{"one hour only", `{"ephemeral_1h_input_tokens":200}`, 0, 200},
		{"explicit zero", `{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0}`, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := `{"input_tokens":1000,"output_tokens":50,"cache_creation_input_tokens":200,"cache_read_input_tokens":100,"cache_creation":` + tc.raw + `}`
			var responses apicompat.ResponsesUsage
			require.NoError(t, json.Unmarshal([]byte(raw), &responses))
			copied := copyOpenAIUsageFromResponsesUsage(&responses)
			require.Equal(t, tc.want5m, copied.CacheCreation5mTokens)
			require.Equal(t, tc.want1h, copied.CacheCreation1hTokens)
			var progressive OpenAIUsage
			svc := &OpenAIGatewayService{}
			svc.parseSSEUsage(`{"type":"response.in_progress","response":{"usage":`+raw+`}}`, &progressive)
			require.Equal(t, tc.want5m, progressive.CacheCreation5mTokens)
			require.Equal(t, tc.want1h, progressive.CacheCreation1hTokens)
			svc.parseSSEUsage(`{"type":"response.completed","response":{"usage":{"input_tokens":1000,"output_tokens":60,"cache_creation_input_tokens":0}}}`, &progressive)
			require.Zero(t, progressive.CacheCreation5mTokens, "terminal usage replaces provisional buckets")
			require.Zero(t, progressive.CacheCreation1hTokens)
		})
	}
}

func TestOpenAICacheCreationSplitChatHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, split, header5m, header1h string
		want5m, want1h                  int
	}{
		{"header fallback", "", "80", "120", 80, 120},
		{"one hour header", "", "", "200", 0, 200},
		{"body wins", `,"cache_creation":{"ephemeral_5m_input_tokens":200,"ephemeral_1h_input_tokens":0}`, "0", "200", 200, 0},
		{"body zero wins", `,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0}`, "0", "200", 0, 0},
		{"no split", "", "", "", 0, 0},
		{"invalid header", "", "-1", "200", 0, 0},
	} {
		for _, mode := range []string{"raw buffered", "raw stream", "fallback buffered", "fallback stream"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				rawUsage := `{"prompt_tokens":1000,"completion_tokens":50,"prompt_tokens_details":{"cached_tokens":100,"cache_write_tokens":200}` + tc.split + `}`
				body := `{"id":"chat_cache","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":` + rawUsage + `}`
				if strings.HasSuffix(mode, "stream") {
					body = "data: " + `{"id":"chat_cache","choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}` + "\n\ndata: " + `{"choices":[],"usage":` + rawUsage + `}` + "\n\ndata: [DONE]\n\n"
				}
				response := &http.Response{StatusCode: http.StatusOK, Header: mshHeaders(tc.header5m, tc.header1h), Body: io.NopCloser(strings.NewReader(body))}
				defer func() { require.NoError(t, response.Body.Close()) }()
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
				svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}}
				var usage OpenAIUsage
				switch mode {
				case "raw buffered", "raw stream":
					var result *OpenAIForwardResult
					var err error
					if mode == "raw stream" {
						result, err = svc.streamRawChatCompletions(c, response, &Account{}, "claude-sonnet-4", "claude-sonnet-4", "claude-sonnet-4", nil, nil, time.Now(), 0)
					} else {
						result, err = svc.bufferRawChatCompletions(c, response, &Account{}, "claude-sonnet-4", "claude-sonnet-4", "claude-sonnet-4", nil, nil, time.Now())
					}
					require.NoError(t, err)
					usage = result.Usage
				case "fallback buffered":
					wire, parsed, err := svc.readCCUpstreamJSONResponse(c, response, writeResponsesError)
					require.NoError(t, err)
					require.Equal(t, tc.split != "", wire.Usage.CacheCreation != nil, "header enrichment must not change the converted client response")
					usage = parsed
				case "fallback stream":
					state := svc.scanCCStream(c, response, "cache split test", "", time.Now(), func(chunk *apicompat.ChatCompletionsChunk) {
						if chunk.Usage != nil {
							require.Equal(t, tc.split != "", chunk.Usage.CacheCreation != nil, "header enrichment is internal only")
						}
					})
					require.NoError(t, state.Err)
					usage = state.Usage
				}
				require.Equal(t, 200, usage.CacheCreationInputTokens)
				require.Equal(t, tc.want5m, usage.CacheCreation5mTokens)
				require.Equal(t, tc.want1h, usage.CacheCreation1hTokens)
			})
		}
	}
}

func TestOpenAICacheCreationSplitClientBytesUnchanged(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		for _, header := range []bool{false, true} {
			name := "buffered"
			if stream {
				name = "stream"
			}
			if header {
				name += "/MSH"
			} else {
				name += "/no MSH"
			}
			t.Run(name, func(t *testing.T) {
				account := ollamaCloudRawChatCompletionsTestAccount()
				headers := http.Header{}
				if header {
					headers = mshHeaders("80", "120")
				}
				const rawUsage = `{"prompt_tokens":1000,"completion_tokens":50,"prompt_tokens_details":{"cache_write_tokens":200}}`
				body := `{"id":"chat_bytes", "choices":[{"message":{"role":"assistant","content":"ok","reasoning":"thinking"},"finish_reason":"stop"}],"usage":` + rawUsage + `}`
				want := string(InjectChatCompletionCacheCreation(applyOllamaCloudRawChatCompletionsResponse(account, []byte(body)), headers))
				if stream {
					body = "event: message\ndata:  " + `{"id":"chat_bytes","choices":[{"delta":{"content":"ok","reasoning":"thinking","tool_calls":[{"index":0,"id":"","function":{"name":"","arguments":"{}"}}]},"finish_reason":"stop"}],"usage":` + rawUsage + `}` + "\n\ndata: [DONE]\n\n"
					lines := strings.Split(strings.TrimSuffix(body, "\n"), "\n")
					fiveMinute, oneHour, ok := ParseMSHCacheWriteBuckets(headers)
					for i, line := range lines {
						line = applyOllamaCloudRawChatCompletionsSSELine(account, line)
						line = stripEmptyChatToolCallIdentityFromSSELine(line)
						if ok {
							line = InjectChatCompletionCacheCreationIntoSSELine(line, fiveMinute, oneHour)
						}
						lines[i] = line
					}
					want = strings.Join(lines, "\n") + "\n"
				}
				response := &http.Response{StatusCode: http.StatusOK, Header: headers, Body: io.NopCloser(strings.NewReader(body))}
				defer func() { require.NoError(t, response.Body.Close()) }()
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
				svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}}
				var err error
				if stream {
					_, err = svc.streamRawChatCompletions(c, response, account, "claude-sonnet-4", "claude-sonnet-4", "claude-sonnet-4", nil, nil, time.Now(), 0)
				} else {
					_, err = svc.bufferRawChatCompletions(c, response, account, "claude-sonnet-4", "claude-sonnet-4", "claude-sonnet-4", nil, nil, time.Now())
				}
				require.NoError(t, err)
				require.Equal(t, []byte(want), rec.Body.Bytes(), "the pre-fix client rewrite order is byte-for-byte unchanged")
			})
		}
	}
}

func TestOpenAICacheCreationSplitNoTierPricing(t *testing.T) {
	for _, split := range []bool{false, true} {
		repo := &openAIRecordUsageLogRepoStub{inserted: true}
		svc := newOpenAIRecordUsageServiceForTest(repo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
		svc.billingService = newTestBillingServiceWithPrices(map[string]*ModelPricing{
			"claude-sonnet-4": {CacheCreationPricePerToken: 2.5e-6, SupportsCacheBreakdown: false, CacheCreation5mPrice: 2.5e-6, CacheCreation1hPrice: 4e-6},
		})
		usage := OpenAIUsage{InputTokens: 200, CacheCreationInputTokens: 200}
		if split {
			usage.CacheCreation1hTokens = 200
		}
		require.NoError(t, svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
			Result: &OpenAIForwardResult{Model: "claude-sonnet-4", Usage: usage},
			APIKey: &APIKey{ID: 2}, User: &User{ID: 1}, Account: &Account{ID: 3},
		}))
		require.InDelta(t, 200*2.5e-6, repo.lastLog.CacheCreationCost, 1e-12, "no tier pricing means no change to the charge")
	}
}

func TestOpenAICacheCreationSplitAccountStats(t *testing.T) {
	for _, gateway := range []string{"openai", "anthropic"} {
		t.Run(gateway, func(t *testing.T) {
			channel := &Channel{ID: 1, Status: StatusActive, AccountStatsPricingRules: []AccountStatsPricingRule{{
				GroupIDs: []int64{10},
				Pricing:  []ChannelModelPricing{{Models: []string{"claude-sonnet-4"}, CacheWritePrice: testPtrFloat64(2e-6), CacheWrite1hPrice: testPtrFloat64(4e-6)}},
			}}}
			repo := &openAIRecordUsageLogRepoStub{inserted: true}
			account := &Account{ID: 3, Platform: PlatformAnthropic, Type: AccountTypeAPIKey}
			key := &APIKey{ID: 2, GroupID: i64p(10), Group: &Group{ID: 10, Platform: PlatformAnthropic, RateMultiplier: 1}}
			usage := ClaudeUsage{CacheCreationInputTokens: 200, CacheCreation5mTokens: 80, CacheCreation1hTokens: 120}
			if gateway == "openai" {
				svc := newOpenAIRecordUsageServiceForTest(repo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
				svc.channelService = newTestChannelServiceForStats(t, channel, 10, PlatformAnthropic)
				require.NoError(t, svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
					Result: &OpenAIForwardResult{Model: "claude-sonnet-4", Usage: claudeUsageToOpenAIUsage(&usage)},
					APIKey: key, User: &User{ID: 1}, Account: account,
				}))
			} else {
				svc := newGatewayRecordUsageServiceForTest(repo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})
				svc.channelService = newTestChannelServiceForStats(t, channel, 10, PlatformAnthropic)
				require.NoError(t, svc.RecordUsage(context.Background(), &RecordUsageInput{
					Result: &ForwardResult{Model: "claude-sonnet-4", Usage: usage},
					APIKey: key, User: &User{ID: 1}, Account: account,
				}))
			}
			require.NotNil(t, repo.lastLog.AccountStatsCost)
			require.InDelta(t, 80*2e-6+120*4e-6, *repo.lastLog.AccountStatsCost, 1e-12)
		})
	}
}
