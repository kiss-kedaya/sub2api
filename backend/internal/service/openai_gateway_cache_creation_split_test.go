//go:build unit

package service

import (
	"context"
	"encoding/json"
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
