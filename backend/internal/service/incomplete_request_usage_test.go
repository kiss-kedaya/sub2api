//go:build unit

package service

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// legacyIncompleteInputCacheReadPercent and its wrappers stay in unit tests
// (golangci-lint runs without -tags=unit) for historical 95%-split comparisons.
const legacyIncompleteInputCacheReadPercent = 95

func splitIncompleteInput(total int) incompleteInputEstimate {
	return splitIncompleteInputWithPercent(total, legacyIncompleteInputCacheReadPercent)
}

func incompleteCacheReadTokens(total int) int {
	return splitIncompleteInput(total).CacheRead
}

func TestSplitIncompleteInput(t *testing.T) {
	for _, tc := range []struct{ total, cacheRead, fresh int }{
		{-5, 0, 0}, {0, 0, 0}, {1, 0, 1}, {19, 18, 1}, {20, 19, 1}, {100, 95, 5}, {1000, 950, 50}, {123456, 117283, 6173},
	} {
		got := splitIncompleteInput(tc.total)
		if tc.total <= 0 {
			require.Equal(t, incompleteInputEstimate{}, got, "total=%d", tc.total)
			continue
		}
		require.Equal(t, tc.total, got.Total, "total=%d", tc.total)
		require.Equal(t, tc.cacheRead, got.CacheRead, "total=%d", tc.total)
		require.Equal(t, tc.fresh, got.Fresh, "total=%d", tc.total)
		require.Equal(t, got.Total, got.CacheRead+got.Fresh, "the split must add up")
		require.Equal(t, tc.cacheRead, incompleteCacheReadTokens(tc.total))
	}
}

func TestEstimateRequestInputTokens_PicksTheEstimatorByRequestShape(t *testing.T) {
	text := "Write a long story about a fox who learns to code, with a twist at the end."
	responses := []byte(`{"model":"gpt-5.4","input":"` + text + `","stream":true}`)
	anthropic := []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"` + text + `"}],"max_tokens":64}`)
	gemini := []byte(`{"contents":[{"role":"user","parts":[{"text":"` + text + `"}]}]}`)

	require.Greater(t, estimateRequestInputTokens(responses), 0)
	require.Greater(t, estimateRequestInputTokens(anthropic), 0)
	geminiGot := estimateRequestInputTokens(gemini)
	geminiExact := estimateTokensForText(text)
	require.InDelta(t, float64(geminiExact), float64(geminiGot), float64(geminiExact)*0.25, "gemini fast=%d exact=%d", geminiGot, geminiExact)

	for name, body := range map[string][]byte{
		"empty":         nil,
		"not json":      []byte("not json"),
		"unknown shape": []byte(`{"model":"x","prompt":"` + text + `"}`),
		"empty input":   []byte(`{"model":"x","input":""}`),
	} {
		require.Zero(t, estimateRequestInputTokens(body), name)
	}
}

func TestSplitIncompleteInput_OfARequestEstimate(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4","input":"Summarise this paragraph about gateways and billing in two sentences."}`)
	got := splitIncompleteInput(estimateRequestInputTokens(body))
	require.Greater(t, got.Total, 0)
	require.Equal(t, got.Total*95/100, got.CacheRead)
	require.Equal(t, got.Total, got.CacheRead+got.Fresh)
	require.Equal(t, incompleteInputEstimate{}, splitIncompleteInput(estimateRequestInputTokens([]byte(`{"x":1}`))))
}

// The preauthorization reserve keeps its byte-length floor for shapes the shared
// estimator cannot read, and its raw tiktoken bound for known shapes.
func TestEstimateBalancePreauthorizationInputTokens_KeepsTheByteFallback(t *testing.T) {
	unknown := []byte(`{"model":"x","prompt":"hello there"}`)
	require.Equal(t, len(unknown), estimateBalancePreauthorizationInputTokens(unknown))
	require.Zero(t, estimateBalancePreauthorizationInputTokens(nil))
	known := []byte(`{"model":"gpt-5.4","input":"hello there"}`)
	require.Equal(t, estimateRequestInputTokensRaw(known), estimateBalancePreauthorizationInputTokens(known))
}

func TestAddOpenAIUsage_KeepsTheEstimatedCacheReadMark(t *testing.T) {
	dst := OpenAIUsage{InputTokens: 10, CacheReadInputTokens: 2}
	addOpenAIUsage(&dst, OpenAIUsage{InputTokens: 100, CacheReadInputTokens: 95, CacheReadEstimated: true})
	require.Equal(t, 110, dst.InputTokens)
	require.Equal(t, 97, dst.CacheReadInputTokens)
	require.True(t, dst.CacheReadEstimated, "a later estimated round must keep the mark")

	clean := OpenAIUsage{}
	addOpenAIUsage(&clean, OpenAIUsage{InputTokens: 5})
	require.False(t, clean.CacheReadEstimated)
}

// withOpenAIIncompleteInputCacheReadPercent sets the OpenAI-family estimate policy
// for one test and restores it afterwards.
func withOpenAIIncompleteInputCacheReadPercent(t *testing.T, percent int) {
	t.Helper()
	previous := OpenAIIncompleteInputCacheReadPercent()
	SetOpenAIIncompleteInputCacheReadPercent(percent)
	t.Cleanup(func() { SetOpenAIIncompleteInputCacheReadPercent(previous) })
}

// withAnthropicIncompleteInputCacheReadPercent sets the Anthropic-family estimate policy
// for one test and restores it afterwards.
func withAnthropicIncompleteInputCacheReadPercent(t *testing.T, percent int) {
	t.Helper()
	previous := AnthropicIncompleteInputCacheReadPercent()
	SetAnthropicIncompleteInputCacheReadPercent(percent)
	t.Cleanup(func() { SetAnthropicIncompleteInputCacheReadPercent(previous) })
}

// withGeminiIncompleteInputCacheReadPercent sets the Gemini/Antigravity-family estimate policy
// for one test and restores it afterwards.
func withGeminiIncompleteInputCacheReadPercent(t *testing.T, percent int) {
	t.Helper()
	previous := GeminiIncompleteInputCacheReadPercent()
	SetGeminiIncompleteInputCacheReadPercent(percent)
	t.Cleanup(func() { SetGeminiIncompleteInputCacheReadPercent(previous) })
}

// An OpenAI-family estimate bills the whole input at the plain input price by
// default, matching new-api's abnormal-end fallback (issue #456); the share is
// configurable and clamped.
func TestOpenAIIncompleteInputCacheReadPolicy(t *testing.T) {
	require.Zero(t, OpenAIIncompleteInputCacheReadPercent(), "no cache discount is invented by default")
	require.Equal(t, incompleteInputEstimate{Total: 100, CacheRead: 0, Fresh: 100}, splitOpenAIIncompleteInput(100))
	require.Zero(t, openAIIncompleteCacheReadTokens(100))

	withOpenAIIncompleteInputCacheReadPercent(t, 95)
	require.Equal(t, 95, OpenAIIncompleteInputCacheReadPercent())
	require.Equal(t, incompleteInputEstimate{Total: 100, CacheRead: 95, Fresh: 5}, splitOpenAIIncompleteInput(100))
	require.Equal(t, 950, openAIIncompleteCacheReadTokens(1000))

	SetOpenAIIncompleteInputCacheReadPercent(-7)
	require.Equal(t, 0, OpenAIIncompleteInputCacheReadPercent(), "out-of-range values are clamped")
	SetOpenAIIncompleteInputCacheReadPercent(300)
	require.Equal(t, 100, OpenAIIncompleteInputCacheReadPercent(), "out-of-range values are clamped")
	SetOpenAIIncompleteInputCacheReadPercent(openAIIncompleteInputCacheReadPercentDefault)

	// Anthropic and Gemini families also default to 0% (plain input price) and are independently configurable.
	require.Zero(t, AnthropicIncompleteInputCacheReadPercent())
	require.Zero(t, GeminiIncompleteInputCacheReadPercent())
	require.Equal(t, incompleteInputEstimate{Total: 100, CacheRead: 0, Fresh: 100}, splitAnthropicIncompleteInput(100))
	require.Equal(t, incompleteInputEstimate{Total: 100, CacheRead: 0, Fresh: 100}, splitGeminiIncompleteInput(100))

	withAnthropicIncompleteInputCacheReadPercent(t, 80)
	require.Equal(t, incompleteInputEstimate{Total: 100, CacheRead: 80, Fresh: 20}, splitAnthropicIncompleteInput(100))
	require.Zero(t, splitGeminiIncompleteInput(100).CacheRead)

	withGeminiIncompleteInputCacheReadPercent(t, 95)
	require.Equal(t, incompleteInputEstimate{Total: 100, CacheRead: 95, Fresh: 5}, splitGeminiIncompleteInput(100))
	require.Equal(t, 950, geminiIncompleteCacheReadTokens(1000))

	SetAnthropicIncompleteInputCacheReadPercent(-1)
	require.Equal(t, 0, AnthropicIncompleteInputCacheReadPercent())
	SetAnthropicIncompleteInputCacheReadPercent(120)
	require.Equal(t, 100, AnthropicIncompleteInputCacheReadPercent())
	SetGeminiIncompleteInputCacheReadPercent(-5)
	require.Equal(t, 0, GeminiIncompleteInputCacheReadPercent())
	SetGeminiIncompleteInputCacheReadPercent(200)
	require.Equal(t, 100, GeminiIncompleteInputCacheReadPercent())

	// Legacy helper remains fixed at 95% for historical comparison tests.
	require.Equal(t, 95, splitIncompleteInput(100).CacheRead)
}

func TestEstimateRequestInputTokens_CalibratedAndZeroAlloc(t *testing.T) {
	t.Run("responses_mixed_code_cjk_tools", func(t *testing.T) {
		chunk := "func step(x int) int { return x + 1 } // 中文注释 🚀\n"
		largeText := strings.Repeat(chunk, 4000) // ~220 KiB
		body, err := json.Marshal(map[string]any{
			"model":        "gpt-5.4",
			"instructions": "Be concise.\nFollow rules.",
			"input": []any{
				map[string]any{
					"role":    "user",
					"type":    "message",
					"content": []any{map[string]any{"type": "input_text", "text": largeText}},
				},
				map[string]any{
					"type":      "function_call",
					"call_id":   "call_1",
					"name":      "run_step",
					"arguments": `{"n":42}`,
				},
				map[string]any{
					"type":    "function_call_output",
					"call_id": "call_1",
					"output":  `{"ok":true}`,
				},
			},
			"tools": []any{
				map[string]any{
					"type":        "function",
					"name":        "run_step",
					"description": "Execute step",
					"parameters":  map[string]any{"type": "object"},
				},
			},
			"tool_choice": "auto",
		})
		require.NoError(t, err)

		exact, err := EstimateOpenAIResponsesInputTokens(body)
		require.NoError(t, err)
		got, err := estimateIncompleteResponsesInput(body)
		require.NoError(t, err)
		require.Equal(t, got, estimateRequestInputTokens(body))
		require.InDelta(t, float64(exact), float64(got), float64(exact)*0.20, "fast=%d exact=%d", got, exact)
		require.Zero(t, testing.AllocsPerRun(20, func() {
			if estimateRequestInputTokens(body) != got {
				t.Fatal("non-deterministic estimate")
			}
		}), "hot-path fast estimator must allocate zero bytes")
	})

	t.Run("anthropic_messages_cjk_and_english", func(t *testing.T) {
		body, err := json.Marshal(map[string]any{
			"model":      "claude-sonnet-4",
			"max_tokens": 64,
			"system":     "You are a precise assistant helping review Go gateway code.",
			"messages": []any{
				map[string]any{"role": "user", "content": strings.Repeat("请检查网关计费和流式断开处理逻辑，确保不会重复扣费。 ", 400)},
			},
		})
		require.NoError(t, err)

		exact, err := EstimateAnthropicCountTokens(body)
		require.NoError(t, err)
		got := estimateRequestInputTokens(body)
		require.InDelta(t, float64(exact), float64(got), float64(exact)*0.25, "fast=%d exact=%d", got, exact)
		require.Zero(t, testing.AllocsPerRun(20, func() {
			if estimateRequestInputTokens(body) != got {
				t.Fatal("non-deterministic estimate")
			}
		}))
	})

	t.Run("smuggled_empty_foreign_shape_never_zeros_real_shape", func(t *testing.T) {
		body := []byte(`{"model":"claude-sonnet-4","contents":[],"messages":[{"role":"user","content":"` + strings.Repeat("hello world ", 100) + `"}]}`)
		require.Greater(t, estimateRequestInputTokens(body), 150)
	})

	t.Run("anthropic_tool_use_counts_input_keys_and_scalars", func(t *testing.T) {
		emptyInput := []byte(`{"model":"claude-sonnet-4","messages":[{"role":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"query_orders","input":{}}]}]}`)
		richInput := []byte(`{"model":"claude-sonnet-4","messages":[{"role":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"query_orders","input":{"limit":123456789,"status":"processing","include_archived":true}}]}]}`)
		require.Greater(t, estimateRequestInputTokens(richInput), estimateRequestInputTokens(emptyInput)+8)
	})

	t.Run("large_16MiB_clamped_deterministically", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			body []byte
		}{
			{
				name: "16MiB_plain_string_input",
				body: []byte(`{"model":"gpt-5.4","stream":true,"input":"` + strings.Repeat("token ", 2_800_000) + `"}`),
			},
			{
				name: "16MiB_escaped_code_items",
				body: []byte(`{"model":"gpt-5.4","stream":true,"input":[{"role":"user","content":[{"type":"input_text","text":"` + strings.Repeat(`func step(x int) int { return x + 1 }\n`, 430_000) + `"}]}]}`),
			},
			{
				name: "16MiB_anthropic_messages",
				body: []byte(`{"model":"claude-sonnet-4","stream":true,"max_tokens":64,"messages":[{"role":"user","content":"` + strings.Repeat("reasoning-and-context ", 800_000) + `"}]}`),
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				require.Greater(t, len(tc.body), 15<<20)
				require.Equal(t, maxIncompleteInputTokens, estimateRequestInputTokens(tc.body))
			})
		}
	})

	t.Run("invalid_json_or_missing_model_errors", func(t *testing.T) {
		badJSON := append([]byte(`{"model":"gpt-5.4","input":"`+strings.Repeat("a ", 300_000)), []byte(`"`)...)
		_, err := estimateIncompleteResponsesInput(badJSON)
		require.Error(t, err)

		noModel := []byte(`{"input":"` + strings.Repeat("hello ", 100_000) + `"}`)
		_, err = estimateIncompleteResponsesInput(noModel)
		require.Error(t, err)

		// Leading UTF-8 BOM + trailing whitespace must still parse cleanly.
		withBOM := append(append([]byte(nil), utf8BOM...), bytes.Repeat([]byte(" "), 16)...)
		withBOM = append(withBOM, []byte(`{"model":"gpt-5.4","input":"hello world"}   `)...)
		require.Greater(t, estimateRequestInputTokens(withBOM), 0)
	})
}

func BenchmarkEstimateRequestInputTokens_256KiB(b *testing.B) {
	chunk := "func step(x int) int { return x + 1 } // 中文注释\n"
	body, err := json.Marshal(map[string]any{
		"model":        "gpt-5.4",
		"instructions": "Be concise.",
		"input": []any{
			map[string]any{
				"role":    "user",
				"type":    "message",
				"content": []any{map[string]any{"type": "input_text", "text": strings.Repeat(chunk, 4500)}},
			},
		},
	})
	require.NoError(b, err)

	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if estimateRequestInputTokens(body) <= 0 {
			b.Fatal("expected positive estimate")
		}
	}
}
