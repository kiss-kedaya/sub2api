//go:build unit

package service

import (
	"bytes"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

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
	require.Equal(t, estimateTokensForText(text), estimateRequestInputTokens(gemini))

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
// estimator cannot read.
func TestEstimateBalancePreauthorizationInputTokens_KeepsTheByteFallback(t *testing.T) {
	unknown := []byte(`{"model":"x","prompt":"hello there"}`)
	require.Equal(t, len(unknown), estimateBalancePreauthorizationInputTokens(unknown))
	require.Zero(t, estimateBalancePreauthorizationInputTokens(nil))
	known := []byte(`{"model":"gpt-5.4","input":"hello there"}`)
	require.Equal(t, estimateRequestInputTokens(known), estimateBalancePreauthorizationInputTokens(known))
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

	// The Anthropic/Gemini families keep their historical heuristic.
	require.Equal(t, 95, splitIncompleteInput(100).CacheRead)
}

func TestEstimateIncompleteResponsesInput_ExactParityUpTo512KiB(t *testing.T) {
	// Mix of instructions, escaped newlines, Unicode, items, content parts, tools, and tool_choice
	// staying right under the 512 KiB exact threshold.
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
	require.LessOrEqual(t, len(body), incompleteInputExactEstimateLimit)

	exact, err := EstimateOpenAIResponsesInputTokens(body)
	require.NoError(t, err)
	got, err := estimateIncompleteResponsesInput(body)
	require.NoError(t, err)
	require.Equal(t, exact, got)
	require.Equal(t, exact, estimateRequestInputTokens(body))

	// Anthropic <= 512 KiB exact parity
	antBody, err := json.Marshal(map[string]any{
		"model":      "claude-sonnet-4",
		"max_tokens": 64,
		"system":     "System prompt.",
		"messages": []any{
			map[string]any{"role": "user", "content": largeText},
		},
	})
	require.NoError(t, err)
	require.LessOrEqual(t, len(antBody), incompleteInputExactEstimateLimit)
	antExact, err := EstimateAnthropicCountTokens(antBody)
	require.NoError(t, err)
	require.Equal(t, antExact, estimateRequestInputTokens(antBody))
}

func TestEstimateIncompleteResponsesInput_BoundedProportionalAndLarge16MiB(t *testing.T) {
	t.Run("600KiB_proportional_accuracy", func(t *testing.T) {
		text := strings.Repeat("token-word ", 55_000) // ~605 KiB (> 512 KiB)
		body := []byte(`{"model":"gpt-5.4","stream":true,"input":"` + text + `"}`)
		require.Greater(t, len(body), incompleteInputExactEstimateLimit)

		exact, err := EstimateOpenAIResponsesInputTokens(body)
		require.NoError(t, err)
		got, err := estimateIncompleteResponsesInput(body)
		require.NoError(t, err)
		diff := got - exact
		if diff < 0 {
			diff = -diff
		}
		require.LessOrEqual(t, float64(diff)/float64(exact), 0.05, "bounded estimate=%d exact=%d", got, exact)
	})

	t.Run("600KiB_mostly_whitespace_exact", func(t *testing.T) {
		body := append([]byte(`{"model":"gpt-5.4","input":"hello world"`), bytes.Repeat([]byte(" "), 560<<10)...)
		body = append(body, '}')
		exact, err := EstimateOpenAIResponsesInputTokens(body)
		require.NoError(t, err)
		got, err := estimateIncompleteResponsesInput(body)
		require.NoError(t, err)
		require.Equal(t, exact, got)
	})

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
			// Warm codec cache before timing/alloc measurement.
			_, _ = openAIInputTokensCodecForModel("gpt-5.4")

			runtime.GC()
			var mBefore, mAfter, mAfterGC runtime.MemStats
			runtime.ReadMemStats(&mBefore)
			start := time.Now()
			got := estimateRequestInputTokens(tc.body)
			elapsed := time.Since(start)
			runtime.ReadMemStats(&mAfter)
			runtime.GC()
			runtime.ReadMemStats(&mAfterGC)

			require.Equal(t, maxIncompleteInputTokens, got)
			require.Less(t, elapsed, 2*time.Second, "16 MiB estimation must be bounded, took %s", elapsed)
			// Verify no 16 MiB string/slice copies were allocated or retained during estimation.
			allocDelta := mAfter.TotalAlloc - mBefore.TotalAlloc
			require.Less(t, allocDelta, uint64(15<<20), "16 MiB estimation allocated too much: %d bytes (elapsed=%s)", allocDelta, elapsed)
			var retainedDelta uint64
			if mAfterGC.HeapAlloc > mBefore.HeapAlloc {
				retainedDelta = mAfterGC.HeapAlloc - mBefore.HeapAlloc
			}
			require.Less(t, retainedDelta, uint64(2<<20), "16 MiB estimation retained too much heap: %d bytes", retainedDelta)
		})
	}

	t.Run("large_invalid_json_or_missing_model_errors", func(t *testing.T) {
		badJSON := append([]byte(`{"model":"gpt-5.4","input":"`+strings.Repeat("a ", 300_000)), []byte(`"`)...)
		_, err := estimateIncompleteResponsesInput(badJSON)
		require.Error(t, err)

		noModel := []byte(`{"input":"` + strings.Repeat("hello ", 100_000) + `"}`)
		_, err = estimateIncompleteResponsesInput(noModel)
		require.Error(t, err)
	})
}

func TestBoundedTokenEstimator_EscapedWhitespacePrefixAndRemainUnderRuneLen(t *testing.T) {
	t.Run("escaped_66KiB_newline_prefix_followed_by_520KiB_real_text", func(t *testing.T) {
		prefix := strings.Repeat(`\n`, 66<<10)
		tail := strings.Repeat("billing-token ", 38_000) // ~532 KiB
		body := []byte(`{"model":"gpt-5.4","input":[{"content":[{"type":"input_text","text":"` + prefix + tail + `"}]}]}`)
		require.Greater(t, len(body), incompleteInputExactEstimateLimit)

		exact, err := EstimateOpenAIResponsesInputTokens(body)
		require.NoError(t, err)
		got, err := estimateOpenAIResponsesInputTokensBounded(body)
		require.NoError(t, err)

		diff := got - exact
		if diff < 0 {
			diff = -diff
		}
		require.LessOrEqual(t, float64(diff)/float64(exact), 0.05, "exact=%d bounded=%d", exact, got)
	})

	t.Run("escaped_all_whitespace_66KiB_and_trailing_whitespace", func(t *testing.T) {
		allWS := []byte(`{"model":"gpt-5.4","input":[{"content":[{"type":"input_text","text":"` + strings.Repeat(`\n`, 66<<10) + `"}]}],"padding":"` + strings.Repeat("p", 520<<10) + `"}`)
		require.Greater(t, len(allWS), incompleteInputExactEstimateLimit)
		exactWS, err := EstimateOpenAIResponsesInputTokens(allWS)
		require.NoError(t, err)
		gotWS, err := estimateOpenAIResponsesInputTokensBounded(allWS)
		require.NoError(t, err)
		require.Equal(t, exactWS, gotWS)

		// Also verify trailing escaped whitespace does not inflate bounded count.
		tailText := strings.Repeat("billing-token ", 38_000)
		trailingWS := []byte(`{"model":"gpt-5.4","input":[{"content":[{"type":"input_text","text":"` + tailText + strings.Repeat(`\n`, 66<<10) + `"}]}]}`)
		exactTrail, err := EstimateOpenAIResponsesInputTokens(trailingWS)
		require.NoError(t, err)
		gotTrail, err := estimateOpenAIResponsesInputTokensBounded(trailingWS)
		require.NoError(t, err)
		diff := gotTrail - exactTrail
		if diff < 0 {
			diff = -diff
		}
		require.LessOrEqual(t, float64(diff)/float64(exactTrail), 0.05, "exact=%d bounded=%d", exactTrail, gotTrail)
	})

	t.Run("remain_less_than_utf8_rune_len_with_540KiB_escaped_CJK_role_omitted", func(t *testing.T) {
		// Item 0 consumes 65534 of 65536 sampleBudget ("user" + 65530 bytes), leaving remain=2.
		// Item 1 omits "role" and starts directly with 3-byte escaped CJK (\u4e2d).
		item0Text := strings.Repeat("abcd ", 13105) + "abcdx" // 65530 bytes
		cjk540KiB := strings.Repeat(`\u4e2d`, 180_000)        // 540,000 decoded UTF-8 bytes
		for _, tc := range []struct {
			name   string
			prefix string
		}{
			{name: "cjk_after_newline", prefix: `\n`},
			{name: "cjk_at_byte_0", prefix: ``},
		} {
			t.Run(tc.name, func(t *testing.T) {
				body := []byte(`{"model":"gpt-5.4","input":[{"role":"user","content":[{"type":"input_text","text":"` + item0Text + `"}]},{"content":[{"type":"input_text","text":"` + tc.prefix + cjk540KiB + `"}]}]}`)
				require.Greater(t, len(body), incompleteInputExactEstimateLimit)

				got, err := estimateOpenAIResponsesInputTokensBounded(body)
				require.NoError(t, err)
				require.Greater(t, got, 100_000, "escaped CJK item must not be dropped when remain < utf8.RuneLen (bounded=%d)", got)
			})
		}
	})

	t.Run("plain_vs_escaped_remain_under_rune_len_parity", func(t *testing.T) {
		codec, err := openAIInputTokensCodecForModel("gpt-5.4")
		require.NoError(t, err)

		runEstimator := func(fieldLiteral string) int {
			est := newBoundedTokenEstimator(codec)
			// Leave remain == 2 (< 3-byte CJK rune len).
			seed := `"` + strings.Repeat("a", int(est.sampleBudget)-2) + `"`
			ok, err := est.observeJSONString([]byte(seed), 0, len(seed), true)
			require.NoError(t, err)
			require.True(t, ok)
			ok, err = est.observeJSONString([]byte(fieldLiteral), 0, len(fieldLiteral), true)
			require.NoError(t, err)
			require.True(t, ok)
			return est.result()
		}

		plainLit := `"` + strings.Repeat("中", 2_000) + `"`
		escapedLit := `"` + strings.Repeat(`\u4e2d`, 2_000) + `"`
		gotPlain := runEstimator(plainLit)
		gotEscaped := runEstimator(escapedLit)
		require.Equal(t, gotPlain, gotEscaped)
		require.Greater(t, gotEscaped, 8_500)
	})
}

func TestAppendCompactedJSONBounded_MidRuneUTF8Boundary(t *testing.T) {
	// Cut inside 2-byte (é), 3-byte (中), and 4-byte (🚀) UTF-8 sequences.
	cases := []struct {
		name string
		raw  string
	}{
		{name: "2byte_latin", raw: `{"k":"` + strings.Repeat("é", 40) + `"}`},
		{name: "3byte_cjk", raw: `{"k":"` + strings.Repeat("中", 40) + `"}`},
		{name: "4byte_emoji", raw: `{"k":"` + strings.Repeat("🚀", 40) + `"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for maxBytes := 1; maxBytes <= 24; maxBytes++ {
				sample, total := appendCompactedJSONBounded(nil, []byte(tc.raw), maxBytes)
				require.Equal(t, len(tc.raw), total, "maxBytes=%d", maxBytes)
				require.LessOrEqual(t, len(sample), maxBytes, "maxBytes=%d", maxBytes)
				require.True(t, utf8.Valid(sample), "maxBytes=%d sample=%x", maxBytes, sample)
			}
		})
	}
}

func TestEstimateAnthropicInputTokensBounded_ErrorAndNullParity(t *testing.T) {
	pad := strings.Repeat("padding-token ", 40_000) // > 512 KiB

	t.Run("system_null_parity", func(t *testing.T) {
		body := []byte(`{"model":"claude-sonnet-4","max_tokens":64,"system":null,"messages":[{"role":"user","content":"` + pad + `"}]}`)
		require.Greater(t, len(body), incompleteInputExactEstimateLimit)

		exact, err := EstimateAnthropicCountTokens(body)
		require.NoError(t, err)
		got, err := estimateAnthropicInputTokensBounded(body)
		require.NoError(t, err)
		diff := got - exact
		if diff < 0 {
			diff = -diff
		}
		require.LessOrEqual(t, float64(diff)/float64(exact), 0.05, "exact=%d bounded=%d", exact, got)
	})

	t.Run("explicit_message_content_null_parity", func(t *testing.T) {
		body := []byte(`{"model":"claude-sonnet-4","max_tokens":64,"messages":[{"role":"user","content":null},{"role":"user","content":"` + pad + `"}]}`)
		require.Greater(t, len(body), incompleteInputExactEstimateLimit)

		exact, err := EstimateAnthropicCountTokens(body)
		require.NoError(t, err)
		got, err := estimateAnthropicInputTokensBounded(body)
		require.NoError(t, err)
		diff := got - exact
		if diff < 0 {
			diff = -diff
		}
		require.LessOrEqual(t, float64(diff)/float64(exact), 0.05, "exact=%d bounded=%d", exact, got)
	})

	t.Run("tool_result_content_valid_non_block_and_deep_nesting_parity", func(t *testing.T) {
		// convertToolResultOutput catches inner []AnthropicContentBlock unmarshal failures
		// and falls back to "(empty)" with err == nil, and never recurses past depth 1.
		deepOpen := strings.Repeat(`{"type":"tool_result","tool_use_id":"u","content":[`, 200)
		deepClose := strings.Repeat(`]}`, 200)
		deepBody := []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":[` + deepOpen + `{"type":"text","text":"` + pad + `"}` + deepClose + `]}]}`)

		validShapes := []struct {
			name string
			body []byte
		}{
			{
				name: "tool_result_content_array_primitive",
				body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":["x",123,"` + pad + `"]}]}]}`),
			},
			{
				name: "tool_result_content_object",
				body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":{"bad":"` + pad + `"}}]}]}`),
			},
			{
				name: "tool_result_content_deep_nesting_200",
				body: deepBody,
			},
		}
		for _, tc := range validShapes {
			t.Run(tc.name, func(t *testing.T) {
				require.Greater(t, len(tc.body), incompleteInputExactEstimateLimit)
				exact, exactErr := EstimateAnthropicCountTokens(tc.body)
				require.NoError(t, exactErr)
				got, boundedErr := estimateAnthropicInputTokensBounded(tc.body)
				require.NoError(t, boundedErr)
				require.Greater(t, exact, 0)
				require.Greater(t, got, 0)
			})
		}
	})

	errCases := []struct {
		name string
		body []byte
	}{
		{
			name: "invalid_escape_in_system_string",
			body: []byte(`{"model":"claude-sonnet-4","system":"bad\q","messages":[{"role":"user","content":"` + pad + `"}]}`),
		},
		{
			name: "invalid_escape_in_system_block_text",
			body: []byte(`{"model":"claude-sonnet-4","system":[{"type":"text","text":"bad\q"}],"messages":[{"role":"user","content":"` + pad + `"}]}`),
		},
		{
			name: "invalid_system_type_number",
			body: []byte(`{"model":"claude-sonnet-4","system":123,"messages":[{"role":"user","content":"` + pad + `"}]}`),
		},
		{
			name: "invalid_system_type_object",
			body: []byte(`{"model":"claude-sonnet-4","system":{"text":"hi"},"messages":[{"role":"user","content":"` + pad + `"}]}`),
		},
		{
			name: "invalid_system_type_bool",
			body: []byte(`{"model":"claude-sonnet-4","system":true,"messages":[{"role":"user","content":"` + pad + `"}]}`),
		},
		{
			name: "malformed_system_array",
			body: []byte(`{"model":"claude-sonnet-4","system":[invalid],"messages":[{"role":"user","content":"` + pad + `"}]}`),
		},
		{
			name: "malformed_tools_array",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"` + pad + `"}],"tools":[invalid]}`),
		},
		{
			name: "invalid_tools_type_object",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"` + pad + `"}],"tools":{"name":"x"}}`),
		},
		{
			name: "invalid_escape_in_message_role_string",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"us\qr","content":"` + pad + `"}]}`),
		},
		{
			name: "invalid_escape_in_message_content_string",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"` + pad + `\q"}]}`),
		},
		{
			name: "malformed_message_content_array",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":[{"type":"text","text":"` + pad + `"},invalid]}]}`),
		},
		{
			name: "invalid_escape_in_block_text_string",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":[{"type":"text","text":"` + pad + `\q"}]}]}`),
		},
		{
			name: "invalid_escape_in_nested_tool_result_content_string",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"` + pad + `\q"}]}]}`),
		},
		{
			name: "malformed_nested_tool_result_content_array",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"text","text":"` + pad + `"},invalid]}]}]}`),
		},
		{
			name: "raw_control_char_in_system",
			body: []byte("{\"model\":\"claude-sonnet-4\",\"system\":\"hi\x01\",\"messages\":[{\"role\":\"user\",\"content\":\"" + pad + "\"}]}"),
		},
		{
			name: "raw_control_char_in_message_content",
			body: []byte("{\"model\":\"claude-sonnet-4\",\"messages\":[{\"role\":\"user\",\"content\":\"" + pad + "\x01\"}]}"),
		},
		{
			name: "null_element_in_messages",
			body: []byte(`{"model":"claude-sonnet-4","messages":[null,{"role":"user","content":"` + pad + `"}]}`),
		},
		{
			name: "missing_content_in_message",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user"},{"role":"user","content":"` + pad + `"}]}`),
		},
		{
			name: "missing_content_in_message_with_escaped_key",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"\u0072ole":"user"},{"role":"user","content":"` + pad + `"}]}`),
		},
		{
			name: "invalid_system_block_non_string_type",
			body: []byte(`{"model":"claude-sonnet-4","system":[{"type":123,"text":"hi"}],"messages":[{"role":"user","content":"` + pad + `"}]}`),
		},
		{
			name: "invalid_system_block_extra_malformed_field",
			body: []byte(`{"model":"claude-sonnet-4","system":[{"type":"text","text":"hi","cache_control":{invalid}}],"messages":[{"role":"user","content":"` + pad + `"}]}`),
		},
		{
			name: "invalid_tool_result_content_array_string_escape",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":["bad\q","` + pad + `"]}]}]}`),
		},
		{
			name: "invalid_message_content_array_primitive",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":["x",123,"` + pad + `"]}]}`),
		},
		{
			name: "invalid_tool_result_content_object",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":{"bad":"x\q","pad":"` + pad + `"}}]}]}`),
		},
		{
			name: "invalid_unknown_top_field",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"` + pad + `"}],"metadata":{"bad":"x\q"}}`),
		},
		{
			name: "depth_exceeding_json_max_depth",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"` + pad + `"}],"metadata":` + strings.Repeat("[", 10002) + strings.Repeat("]", 10002) + `}`),
		},
	}

	for _, tc := range errCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Greater(t, len(tc.body), incompleteInputExactEstimateLimit)
			_, exactErr := EstimateAnthropicCountTokens(tc.body)
			require.Error(t, exactErr, "expected EstimateAnthropicCountTokens to reject %s", tc.name)

			_, boundedErr := estimateAnthropicInputTokensBounded(tc.body)
			require.Error(t, boundedErr, "expected estimateAnthropicInputTokensBounded to reject %s", tc.name)
		})
	}
}

func TestEstimateOpenAIResponsesInputTokensBounded_ErrorAndShapeParity(t *testing.T) {
	pad := strings.Repeat("padding-token ", 40_000) // > 512 KiB

	validCases := []struct {
		name string
		body []byte
	}{
		{
			name: "tools_null",
			body: []byte(`{"model":"gpt-5.4","input":"` + pad + `","tools":null}`),
		},
		{
			name: "tools_string_shorthand_and_null",
			body: []byte(`{"model":"gpt-5.4","input":"` + pad + `","tools":["apply_patch",null]}`),
		},
		{
			name: "item_role_number",
			body: []byte(`{"model":"gpt-5.4","input":[{"role":123,"content":[{"type":"input_text","text":"` + pad + `"}]}]}`),
		},
	}
	for _, tc := range validCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Greater(t, len(tc.body), incompleteInputExactEstimateLimit)
			exact, exactErr := EstimateOpenAIResponsesInputTokens(tc.body)
			require.NoError(t, exactErr)
			got, boundedErr := estimateOpenAIResponsesInputTokensBounded(tc.body)
			require.NoError(t, boundedErr)
			require.Greater(t, exact, 0)
			require.Greater(t, got, 0)
		})
	}

	errCases := []struct {
		name string
		body []byte
	}{
		{
			name: "invalid_escape_in_model",
			body: []byte(`{"model":"gpt-5.4\q","input":"` + pad + `"}`),
		},
		{
			name: "raw_control_char_in_model",
			body: []byte("{\"model\":\"gpt-5.4\x01\",\"input\":\"" + pad + "\"}"),
		},
		{
			name: "raw_control_char_in_instructions",
			body: []byte("{\"model\":\"gpt-5.4\",\"instructions\":\"hi\x01\",\"input\":\"" + pad + "\"}"),
		},
		{
			name: "raw_control_char_in_input_string",
			body: []byte("{\"model\":\"gpt-5.4\",\"input\":\"" + pad + "\x01\"}"),
		},
		{
			name: "invalid_escape_in_content_part_text",
			body: []byte(`{"model":"gpt-5.4","input":[{"role":"user","content":[{"type":"input_text","text":"` + pad + `\q"}]}]}`),
		},
		{
			name: "malformed_content_parts_array",
			body: []byte(`{"model":"gpt-5.4","input":[{"role":"user","content":[{"type":"input_text","text":"` + pad + `"},invalid]}]}`),
		},
		{
			name: "invalid_tools_type_object",
			body: []byte(`{"model":"gpt-5.4","input":"` + pad + `","tools":{"name":"x"}}`),
		},
		{
			name: "invalid_tools_element_number",
			body: []byte(`{"model":"gpt-5.4","input":"` + pad + `","tools":[123]}`),
		},
		{
			name: "invalid_tools_element_array",
			body: []byte(`{"model":"gpt-5.4","input":"` + pad + `","tools":[[]]}`),
		},
		{
			name: "invalid_tools_element_escape",
			body: []byte(`{"model":"gpt-5.4","input":"` + pad + `","tools":[{"type":"function","name":"bad\q"}]}`),
		},
		{
			name: "invalid_tool_choice_object",
			body: []byte(`{"model":"gpt-5.4","input":"` + pad + `","tool_choice":{"type":"bad\q"}}`),
		},
		{
			name: "invalid_output_object_escape",
			body: []byte(`{"model":"gpt-5.4","input":[{"type":"function_call_output","call_id":"call_1","output":{"bad":"x\q"}},{"role":"user","content":"` + pad + `"}]}`),
		},
		{
			name: "invalid_unknown_top_field_escape",
			body: []byte(`{"model":"gpt-5.4","input":"` + pad + `","metadata":{"bad":"x\q"}}`),
		},
		{
			name: "invalid_input_non_array_object",
			body: []byte(`{"model":"gpt-5.4","input":{"bad":"` + pad + `\q"}}`),
		},
		{
			name: "depth_exceeding_json_max_depth",
			body: []byte(`{"model":"gpt-5.4","input":"` + pad + `","tool_choice":` + strings.Repeat("[", 10002) + strings.Repeat("]", 10002) + `}`),
		},
	}

	for _, tc := range errCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Greater(t, len(tc.body), incompleteInputExactEstimateLimit)
			_, exactErr := EstimateOpenAIResponsesInputTokens(tc.body)
			require.Error(t, exactErr, "expected EstimateOpenAIResponsesInputTokens to reject %s", tc.name)

			_, boundedErr := estimateOpenAIResponsesInputTokensBounded(tc.body)
			require.Error(t, boundedErr, "expected estimateOpenAIResponsesInputTokensBounded to reject %s", tc.name)
		})
	}
}

func TestEstimateOpenAIResponsesInputTokensBounded_T5R3Parity(t *testing.T) {
	pad := strings.Repeat("padding-token ", 40_000) // > 512 KiB (~80,000 tokens)

	validCases := []struct {
		name string
		body []byte
	}{
		{
			name: "input_array_with_string_element",
			body: []byte(`{"model":"gpt-5.4","input":["` + pad + `","extra"]}`),
		},
		{
			name: "input_array_with_number_element",
			body: []byte(`{"model":"gpt-5.4","input":[{"role":"user","content":"` + pad + `"},12345]}`),
		},
		{
			name: "input_array_with_array_element",
			body: []byte(`{"model":"gpt-5.4","input":[{"role":"user","content":"` + pad + `"},["nested",1]]}`),
		},
		{
			name: "model_duplicate_second_null",
			body: []byte(`{"model":"gpt-5.4","model":null,"input":"` + pad + `"}`),
		},
		{
			name: "model_uppercase_key",
			body: []byte(`{"MODEL":"gpt-5.4","input":"` + pad + `"}`),
		},
		{
			name: "instructions_duplicate_second_null",
			body: []byte(`{"model":"gpt-5.4","instructions":"` + pad + `","instructions":null,"input":"hi"}`),
		},
		{
			name: "model_escaped_key_and_instructions_casefold",
			body: []byte(`{"\u006dodel":"gpt-5.4","INSTRUCTIONS":"` + pad + `","input":"hi"}`),
		},
		{
			name: "item_role_number_raw_fallback_parity",
			body: []byte(`{"model":"gpt-5.4","input":[{"role":123,"content":[{"type":"input_text","text":"` + pad + `"}]}]}`),
		},
	}

	for _, tc := range validCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Greater(t, len(tc.body), incompleteInputExactEstimateLimit)
			exact, exactErr := EstimateOpenAIResponsesInputTokens(tc.body)
			require.NoError(t, exactErr)
			got, boundedErr := estimateOpenAIResponsesInputTokensBounded(tc.body)
			require.NoError(t, boundedErr)
			require.Greater(t, exact, 80_000)
			diff := got - exact
			if diff < 0 {
				diff = -diff
			}
			require.LessOrEqual(t, float64(diff)/float64(exact), 0.05, "exact=%d bounded=%d", exact, got)
		})
	}

	errCases := []struct {
		name string
		body []byte
	}{
		{
			name: "tools_element_object_bad_type_field",
			body: []byte(`{"model":"gpt-5.4","input":"` + pad + `","tools":[{"type":123,"name":"fn"}]}`),
		},
		{
			name: "tools_element_object_bad_name_field",
			body: []byte(`{"model":"gpt-5.4","input":"` + pad + `","tools":[{"type":"function","name":123}]}`),
		},
		{
			name: "tools_element_object_bad_strict_field",
			body: []byte(`{"model":"gpt-5.4","input":"` + pad + `","tools":[{"type":"function","name":"fn","strict":"true"}]}`),
		},
		{
			name: "large_tools_element_bad_strict_field",
			body: []byte(`{"model":"gpt-5.4","input":"hi","tools":[{"type":"function","name":"fn","description":"` + pad + `","strict":"true"}]}`),
		},
	}

	for _, tc := range errCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Greater(t, len(tc.body), incompleteInputExactEstimateLimit)
			_, exactErr := EstimateOpenAIResponsesInputTokens(tc.body)
			require.Error(t, exactErr, "expected EstimateOpenAIResponsesInputTokens to reject %s", tc.name)

			_, boundedErr := estimateOpenAIResponsesInputTokensBounded(tc.body)
			require.Error(t, boundedErr, "expected estimateOpenAIResponsesInputTokensBounded to reject %s", tc.name)
		})
	}
}

func TestEstimateAnthropicInputTokensBounded_T5R3Parity(t *testing.T) {
	pad := strings.Repeat("padding-token ", 40_000) // > 512 KiB (~80,000 tokens)

	validCases := []struct {
		name string
		body []byte
	}{
		{
			name: "model_duplicate_second_null",
			body: []byte(`{"model":"claude-sonnet-4","model":null,"messages":[{"role":"user","content":"` + pad + `"}]}`),
		},
		{
			name: "model_uppercase_key",
			body: []byte(`{"MODEL":"claude-sonnet-4","messages":[{"role":"user","content":"` + pad + `"}]}`),
		},
		{
			name: "escaped_block_text_key_valid",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":[{"type":"text","\u0074ext":"` + pad + `"}]}]}`),
		},
		{
			name: "large_valid_tool_choice_object",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"` + pad + `"}],"tool_choice":{"type":"tool","name":"fn","extra":"` + strings.Repeat("x", 5000) + `"}}`),
		},
	}

	for _, tc := range validCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Greater(t, len(tc.body), incompleteInputExactEstimateLimit)
			exact, exactErr := EstimateAnthropicCountTokens(tc.body)
			require.NoError(t, exactErr)
			got, boundedErr := estimateAnthropicInputTokensBounded(tc.body)
			require.NoError(t, boundedErr)
			require.Greater(t, exact, 80_000)
			diff := got - exact
			if diff < 0 {
				diff = -diff
			}
			require.LessOrEqual(t, float64(diff)/float64(exact), 0.05, "exact=%d bounded=%d", exact, got)
		})
	}

	errCases := []struct {
		name string
		body []byte
	}{
		{
			name: "tool_choice_string_auto",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"` + pad + `"}],"tool_choice":"auto"}`),
		},
		{
			name: "tool_choice_number",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"` + pad + `"}],"tool_choice":123}`),
		},
		{
			name: "tool_choice_array",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"` + pad + `"}],"tool_choice":["auto"]}`),
		},
		{
			name: "tool_choice_bad_type_field",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"` + pad + `"}],"tool_choice":{"type":123}}`),
		},
		{
			name: "tool_choice_bad_name_field",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"` + pad + `"}],"tool_choice":{"type":"tool","name":123}}`),
		},
		{
			name: "tools_bad_name_type",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"` + pad + `"}],"tools":[{"name":123}]}`),
		},
		{
			name: "tools_bad_type_type",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"` + pad + `"}],"tools":[{"type":123,"name":"fn"}]}`),
		},
		{
			name: "tools_bad_description_type",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"` + pad + `"}],"tools":[{"name":"fn","description":123}]}`),
		},
		{
			name: "tools_bad_cache_control_type",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"` + pad + `"}],"tools":[{"name":"fn","cache_control":"bad"}]}`),
		},
		{
			name: "system_block_bad_cache_control",
			body: []byte(`{"model":"claude-sonnet-4","system":[{"type":"text","text":"sys","cache_control":"bad"}],"messages":[{"role":"user","content":"` + pad + `"}]}`),
		},
		{
			name: "system_block_bad_is_error",
			body: []byte(`{"model":"claude-sonnet-4","system":[{"type":"text","text":"sys","is_error":"true"}],"messages":[{"role":"user","content":"` + pad + `"}]}`),
		},
		{
			name: "message_content_block_bad_signature",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":[{"type":"thinking","thinking":"` + pad + `","signature":123}]}]}`),
		},
		{
			name: "message_content_block_bad_is_error",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":[{"type":"text","text":"` + pad + `","is_error":"true"}]}]}`),
		},
		{
			name: "message_content_block_bad_cache_control",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":[{"type":"text","text":"` + pad + `","cache_control":"bad"}]}]}`),
		},
		{
			name: "message_escaped_key_bad_role",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"\u0072ole":123,"content":"` + pad + `"}]}`),
		},
		{
			name: "message_escaped_key_bad_content",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"\u0072ole":"user","cont\u0065nt":123},{"role":"user","content":"` + pad + `"}]}`),
		},
		{
			name: "message_content_block_escaped_key_bad_text",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":[{"type":"text","\u0074ext":123},{"type":"text","text":"` + pad + `"}]}]}`),
		},
		{
			name: "top_max_tokens_string",
			body: []byte(`{"model":"claude-sonnet-4","max_tokens":"64","messages":[{"role":"user","content":"` + pad + `"}]}`),
		},
		{
			name: "top_stream_string",
			body: []byte(`{"model":"claude-sonnet-4","stream":"true","messages":[{"role":"user","content":"` + pad + `"}]}`),
		},
	}

	for _, tc := range errCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Greater(t, len(tc.body), incompleteInputExactEstimateLimit)
			_, exactErr := EstimateAnthropicCountTokens(tc.body)
			require.Error(t, exactErr, "expected EstimateAnthropicCountTokens to reject %s", tc.name)

			_, boundedErr := estimateAnthropicInputTokensBounded(tc.body)
			require.Error(t, boundedErr, "expected estimateAnthropicInputTokensBounded to reject %s", tc.name)
		})
	}
}

func TestEstimateRequestInputTokens_LiveBillingOver512KiBParity(t *testing.T) {
	pad := strings.Repeat("billing-token ", 40_000) // > 512 KiB (> 80,000 tokens)

	cases := []struct {
		name string
		body []byte
	}{
		{
			name: "openai_input_array_strings",
			body: []byte(`{"model":"gpt-5.4","input":["` + pad + `","tail"]}`),
		},
		{
			name: "openai_input_array_numbers",
			body: []byte(`{"model":"gpt-5.4","input":[{"role":"user","content":"` + pad + `"},42]}`),
		},
		{
			name: "openai_input_array_arrays",
			body: []byte(`{"model":"gpt-5.4","input":[{"role":"user","content":"` + pad + `"},["nested"]]}`),
		},
		{
			name: "openai_uppercase_model",
			body: []byte(`{"MODEL":"gpt-5.4","input":"` + pad + `"}`),
		},
		{
			name: "openai_duplicate_model_null",
			body: []byte(`{"model":"gpt-5.4","model":null,"input":"` + pad + `"}`),
		},
		{
			name: "openai_duplicate_instructions_null",
			body: []byte(`{"model":"gpt-5.4","instructions":"` + pad + `","instructions":null,"input":"hi"}`),
		},
		{
			name: "anthropic_uppercase_model",
			body: []byte(`{"MODEL":"claude-sonnet-4","messages":[{"role":"user","content":"` + pad + `"}]}`),
		},
		{
			name: "anthropic_duplicate_model_null",
			body: []byte(`{"model":"claude-sonnet-4","model":null,"messages":[{"role":"user","content":"` + pad + `"}]}`),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Greater(t, len(tc.body), incompleteInputExactEstimateLimit)
			raw := estimateRequestInputTokensRaw(tc.body)
			bounded := estimateRequestInputTokens(tc.body)
			require.Greater(t, raw, 80_000, "raw estimate too small for %s", tc.name)
			require.Greater(t, bounded, 0, "bounded live billing estimate must never undercharge to 0 for %s", tc.name)
			diff := bounded - raw
			if diff < 0 {
				diff = -diff
			}
			require.LessOrEqual(t, float64(diff)/float64(raw), 0.05, "raw=%d bounded=%d", raw, bounded)
		})
	}
}

func TestEstimateAnthropicInputTokensBounded_T5R4Parity(t *testing.T) {
	pad := strings.Repeat("padding-token ", 40_000) // > 512 KiB (~80,000 tokens)
	over4KNonBase64 := strings.Repeat("word ", 1_000)

	// Gap 1: Superseded > 4 KiB duplicate messages / system / tool_choice must not fail valid requests.
	dupValidCases := []struct {
		name string
		body []byte
	}{
		{
			name: "dup_messages_over_4k_missing_content_then_MESSAGES_valid",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","extra":"` + over4KNonBase64 + `"}],"MESSAGES":[{"role":"user","content":"` + pad + `"}]}`),
		},
		{
			name: "dup_messages_over_4k_null_elem_then_MESSAGES_valid",
			body: []byte(`{"model":"claude-sonnet-4","messages":[null,{"role":"user","extra":"` + over4KNonBase64 + `"}],"MESSAGES":[{"role":"user","content":"` + pad + `"}]}`),
		},
		{
			name: "dup_system_over_4k_invalid_block_then_SYSTEM_string",
			body: []byte(`{"model":"claude-sonnet-4","system":[{"type":123,"text":"` + over4KNonBase64 + `"}],"SYSTEM":"sys","messages":[{"role":"user","content":"` + pad + `"}]}`),
		},
		{
			name: "dup_tool_choice_over_4k_bad_type_then_TOOL_CHOICE_auto",
			body: []byte(`{"model":"claude-sonnet-4","tool_choice":{"type":123,"extra":"` + over4KNonBase64 + `"},"TOOL_CHOICE":{"type":"auto"},"messages":[{"role":"user","content":"` + pad + `"}]}`),
		},
		{
			name: "huge_thinking_over_4k_gpt61sol_enabled_valid",
			body: []byte(`{"model":"gpt-6.1-sol","thinking":{"type":"enabled","budget_tokens":1024,"extra":"` + over4KNonBase64 + `"},"messages":[{"role":"user","content":"` + pad + `"}]}`),
		},
	}
	for _, tc := range dupValidCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Greater(t, len(tc.body), incompleteInputExactEstimateLimit)
			exact, exactErr := EstimateAnthropicCountTokens(tc.body)
			require.NoError(t, exactErr)
			got, boundedErr := estimateAnthropicInputTokensBounded(tc.body)
			require.NoError(t, boundedErr)
			require.Greater(t, exact, 80_000)
			diff := got - exact
			if diff < 0 {
				diff = -diff
			}
			require.LessOrEqual(t, float64(diff)/float64(exact), 0.05, "exact=%d bounded=%d", exact, got)
		})
	}

	// Gap 2 + superseded bad role error parity: > 4 KiB thinking/output_config on gpt-6.1-sol must be rejected.
	errCases := []struct {
		name string
		body []byte
	}{
		{
			name: "huge_output_config_over_4k_gpt61sol_minimal",
			body: []byte(`{"model":"gpt-6.1-sol","output_config":{"effort":"minimal","extra":"` + over4KNonBase64 + `"},"messages":[{"role":"user","content":"` + pad + `"}]}`),
		},
		{
			name: "huge_output_config_over_4k_gpt61sol_none",
			body: []byte(`{"model":"gpt-6.1-sol","output_config":{"effort":"none","extra":"` + over4KNonBase64 + `"},"messages":[{"role":"user","content":"` + pad + `"}]}`),
		},
		{
			name: "huge_output_config_over_4k_space_padded_effort_gpt61sol_minimal",
			body: []byte(`{"model":"gpt-6.1-sol","output_config":{"effort":"` + strings.Repeat(" ", 5000) + `minimal"},"messages":[{"role":"user","content":"` + pad + `"}]}`),
		},
		{
			name: "huge_thinking_over_4k_gpt61sol_disabled",
			body: []byte(`{"model":"gpt-6.1-sol","thinking":{"type":"disabled","extra":"` + over4KNonBase64 + `"},"messages":[{"role":"user","content":"` + pad + `"}]}`),
		},
		{
			name: "dup_messages_over_4k_bad_role_then_MESSAGES_valid",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":123,"content":"` + over4KNonBase64 + `"}],"MESSAGES":[{"role":"user","content":"` + pad + `"}]}`),
		},
		{
			name: "dup_tool_choice_over_4k_valid_then_TOOL_CHOICE_bad",
			body: []byte(`{"model":"claude-sonnet-4","tool_choice":{"type":"auto"},"TOOL_CHOICE":{"type":123,"extra":"` + over4KNonBase64 + `"},"messages":[{"role":"user","content":"` + pad + `"}]}`),
		},
	}
	for _, tc := range errCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Greater(t, len(tc.body), incompleteInputExactEstimateLimit)
			_, exactErr := EstimateAnthropicCountTokens(tc.body)
			require.Error(t, exactErr, "expected EstimateAnthropicCountTokens to reject %s", tc.name)

			_, boundedErr := estimateAnthropicInputTokensBounded(tc.body)
			require.Error(t, boundedErr, "expected estimateAnthropicInputTokensBounded to reject %s", tc.name)
		})
	}

	// Gap 3: Billing-header system blocks, thinking blocks, and invalid-inner tool_result arrays must not overcount.
	filteredCases := []struct {
		name string
		body []byte
	}{
		{
			name: "system_array_billing_header_filtered",
			body: []byte(`{"model":"claude-sonnet-4","system":[{"type":"text","text":"x-anthropic-billing-header: ` + pad + `"}],"messages":[{"role":"user","content":"hi"}]}`),
		},
		{
			name: "system_array_billing_header_escaped_filtered",
			body: []byte(`{"model":"claude-sonnet-4","system":[{"type":"text","text":"\u0078-anthropic-billing-header:\u0020` + pad + `"}],"messages":[{"role":"user","content":"hi"}]}`),
		},
		{
			name: "system_string_billing_header_filtered",
			body: []byte(`{"model":"claude-sonnet-4","system":"x-anthropic-billing-header: ` + pad + `","messages":[{"role":"user","content":"hi"}]}`),
		},
		{
			name: "thinking_without_signature_over_512k",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"` + pad + `"}]},{"role":"user","content":"hi"}]}`),
		},
		{
			name: "thinking_gAAAA_signature_over_512k",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"` + pad + `","signature":"gAAAAexample"}]},{"role":"user","content":"hi"}]}`),
		},
		{
			name: "thinking_valid_signature_over_512k",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"` + pad + `","signature":"EqQBCgIYAhIMvalid"}]},{"role":"user","content":"hi"}]}`),
		},
		{
			name: "tool_result_inner_valid_then_invalid",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"text","text":"` + pad + `"},{"type":"text","text":123}]}]}]}`),
		},
		{
			name: "tool_result_inner_valid_then_primitive",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"text","text":"` + pad + `"},123]}]}]}`),
		},
		{
			name: "tool_result_inner_invalid_then_valid",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"text","text":123},{"type":"text","text":"` + pad + `"}]}]}]}`),
		},
	}
	for _, tc := range filteredCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Greater(t, len(tc.body), incompleteInputExactEstimateLimit)
			exact, exactErr := EstimateAnthropicCountTokens(tc.body)
			require.NoError(t, exactErr)
			got, boundedErr := estimateAnthropicInputTokensBounded(tc.body)
			require.NoError(t, boundedErr)
			require.LessOrEqual(t, exact, 20)
			require.LessOrEqual(t, got, 20, "exact=%d bounded=%d", exact, got)
			require.Greater(t, got, 0)
		})
	}
}

func TestEstimateRequestInputTokens_LiveBillingRound4Parity(t *testing.T) {
	pad := strings.Repeat("billing-token ", 40_000) // > 512 KiB (> 80,000 tokens)
	over4KNonBase64 := strings.Repeat("word ", 1_000)

	highTokenCases := []struct {
		name string
		body []byte
	}{
		{
			name: "dup_messages_over_4k_missing_content_then_MESSAGES_valid",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","extra":"` + over4KNonBase64 + `"}],"MESSAGES":[{"role":"user","content":"` + pad + `"}]}`),
		},
		{
			name: "dup_system_over_4k_invalid_block_then_SYSTEM_string",
			body: []byte(`{"model":"claude-sonnet-4","system":[{"type":123,"text":"` + over4KNonBase64 + `"}],"SYSTEM":"sys","messages":[{"role":"user","content":"` + pad + `"}]}`),
		},
		{
			name: "dup_tool_choice_over_4k_bad_type_then_TOOL_CHOICE_auto",
			body: []byte(`{"model":"claude-sonnet-4","tool_choice":{"type":123,"extra":"` + over4KNonBase64 + `"},"TOOL_CHOICE":{"type":"auto"},"messages":[{"role":"user","content":"` + pad + `"}]}`),
		},
		{
			name: "tool_result_inner_valid_null_then_text",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":[null,{"type":"text","text":"` + pad + `"}]}]}]}`),
		},
	}
	for _, tc := range highTokenCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Greater(t, len(tc.body), incompleteInputExactEstimateLimit)
			raw := estimateRequestInputTokensRaw(tc.body)
			bounded := estimateRequestInputTokens(tc.body)
			require.Greater(t, raw, 80_000)
			require.Greater(t, bounded, 0)
			diff := bounded - raw
			if diff < 0 {
				diff = -diff
			}
			require.LessOrEqual(t, float64(diff)/float64(raw), 0.05, "raw=%d bounded=%d", raw, bounded)
		})
	}

	lowTokenCases := []struct {
		name string
		body []byte
	}{
		{
			name: "system_array_billing_header_filtered",
			body: []byte(`{"model":"claude-sonnet-4","system":[{"type":"text","text":"x-anthropic-billing-header: ` + pad + `"}],"messages":[{"role":"user","content":"hi"}]}`),
		},
		{
			name: "thinking_without_signature_over_512k",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"` + pad + `"}]},{"role":"user","content":"hi"}]}`),
		},
		{
			name: "thinking_gAAAA_signature_over_512k",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"` + pad + `","signature":"gAAAAexample"}]},{"role":"user","content":"hi"}]}`),
		},
		{
			name: "thinking_valid_signature_over_512k",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"` + pad + `","signature":"EqQBCgIYAhIMvalid"}]},{"role":"user","content":"hi"}]}`),
		},
		{
			name: "tool_result_inner_valid_then_invalid",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"text","text":"` + pad + `"},{"type":"text","text":123}]}]}]}`),
		},
	}
	for _, tc := range lowTokenCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Greater(t, len(tc.body), incompleteInputExactEstimateLimit)
			raw := estimateRequestInputTokensRaw(tc.body)
			bounded := estimateRequestInputTokens(tc.body)
			require.LessOrEqual(t, raw, 20)
			require.LessOrEqual(t, bounded, 20, "raw=%d bounded=%d", raw, bounded)
			require.Greater(t, bounded, 0)
		})
	}
}

func TestEstimateInputTokensBounded_T5R5Parity(t *testing.T) {
	pad := strings.Repeat("padding-token ", 40_000) // > 512 KiB (~80,000 tokens)
	over4KNonBase64 := strings.Repeat("word ", 1_000)
	over512KNonBase64 := strings.Repeat("extra ", 90_000)

	t.Run("scratch_aliasing_nested_large_fields", func(t *testing.T) {
		anthropicCases := []struct {
			name string
			body []byte
		}{
			{
				name: "anthropic_block_cap64_then_over4k_cache_control",
				body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"tool_a","input":{"a":1}},{"type":"text","text":"` + pad + `","cache_control":{"type":"ephemeral","extra":"` + over4KNonBase64 + `"}}]}]}`),
			},
			{
				name: "anthropic_block_cap64_then_over4k_source",
				body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"tool_a","input":{"a":1}},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + over4KNonBase64 + `"}}]},{"role":"user","content":"` + pad + `"}]}`),
			},
			{
				name: "anthropic_tool_cap64_then_over4k_cache_control",
				body: []byte(`{"model":"claude-sonnet-4","tools":[{"name":"tool_first","description":"first tool description exceeding sixty four bytes for scratch","input_schema":{"type":"object"}},{"name":"tool_second","description":"` + over4KNonBase64 + `","cache_control":{"type":"ephemeral","extra":"` + over4KNonBase64 + `"},"input_schema":{"type":"object"}}],"messages":[{"role":"user","content":"` + pad + `"}]}`),
			},
		}
		for _, tc := range anthropicCases {
			t.Run(tc.name, func(t *testing.T) {
				require.Greater(t, len(tc.body), incompleteInputExactEstimateLimit)
				exact, exactErr := EstimateAnthropicCountTokens(tc.body)
				require.NoError(t, exactErr)
				got, boundedErr := estimateAnthropicInputTokensBounded(tc.body)
				require.NoError(t, boundedErr)
				diff := got - exact
				if diff < 0 {
					diff = -diff
				}
				require.LessOrEqual(t, float64(diff)/float64(exact), 0.05, "exact=%d bounded=%d", exact, got)
			})
		}

		responsesCases := []struct {
			name string
			body []byte
		}{
			{
				name: "responses_item_cap128_then_over4k_summary",
				body: []byte(`{"model":"gpt-5.4","input":[{"type":"function_call","call_id":"call_1","name":"run_step_with_long_name_for_scratch","arguments":"{\"alpha\":1,\"beta\":2}"},{"type":"reasoning","encrypted_content":"enc","summary":[{"type":"summary_text","text":"` + over4KNonBase64 + `"}]},{"role":"user","content":[{"type":"input_text","text":"` + pad + `"}]}]}`),
			},
			{
				name: "responses_part_cap128_then_over4k_cache_control",
				body: []byte(`{"model":"gpt-5.4","input":[{"type":"function_call","call_id":"call_1","name":"run_step_with_long_name_for_scratch","arguments":"{\"alpha\":1,\"beta\":2}"},{"role":"user","content":[{"type":"input_text","text":"` + pad + `","cache_control":{"type":"ephemeral","extra":"` + over4KNonBase64 + `"}}]}]}`),
			},
			{
				name: "responses_tool_cap128_then_over4k_cache_control_and_nested_tools",
				body: []byte(`{"model":"gpt-5.4","tools":[{"type":"function","name":"tool_a_long_name_for_scratch_growth","description":"grow metaScratch above 128 bytes","parameters":{"type":"object"}},{"type":"namespace","name":"ns_outer","description":"` + over4KNonBase64 + `","cache_control":{"type":"ephemeral","extra":"` + over4KNonBase64 + `"},"tools":[{"type":"function","name":"child_tool","description":"` + over4KNonBase64 + `","cache_control":{"type":"ephemeral","extra":"` + over4KNonBase64 + `"},"parameters":{"type":"object"}}]}],"input":"` + pad + `"}`),
			},
		}
		for _, tc := range responsesCases {
			t.Run(tc.name, func(t *testing.T) {
				require.Greater(t, len(tc.body), incompleteInputExactEstimateLimit)
				exact, exactErr := EstimateOpenAIResponsesInputTokens(tc.body)
				require.NoError(t, exactErr)
				got, boundedErr := estimateOpenAIResponsesInputTokensBounded(tc.body)
				require.NoError(t, boundedErr)
				diff := got - exact
				if diff < 0 {
					diff = -diff
				}
				require.LessOrEqual(t, float64(diff)/float64(exact), 0.05, "exact=%d bounded=%d", exact, got)
			})
		}
	})

	t.Run("anthropic_tool_choice_extra_fields", func(t *testing.T) {
		cases := []struct {
			name string
			body []byte
			low  bool
		}{
			{
				name: "tool_choice_auto_extra",
				body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hi"}],"tool_choice":{"type":"auto","extra":"` + over512KNonBase64 + `"}}`),
				low:  true,
			},
			{
				name: "tool_choice_any_extra",
				body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hi"}],"tool_choice":{"type":"any","extra":"` + over512KNonBase64 + `"}}`),
				low:  true,
			},
			{
				name: "tool_choice_none_extra",
				body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hi"}],"tool_choice":{"type":"none","extra":"` + over512KNonBase64 + `"}}`),
				low:  true,
			},
			{
				name: "tool_choice_tool_extra",
				body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hi"}],"tool_choice":{"type":"tool","name":"fn","extra":"` + over512KNonBase64 + `"}}`),
				low:  true,
			},
			{
				name: "tool_choice_tool_huge_name",
				body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hi"}],"tool_choice":{"type":"tool","name":"` + over512KNonBase64 + `","extra":"ignored"}}`),
				low:  false,
			},
			{
				name: "tool_choice_unknown_type_passthrough",
				body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hi"}],"tool_choice":{"type":"custom_passthrough","extra":"` + over512KNonBase64 + `"}}`),
				low:  false,
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				require.Greater(t, len(tc.body), incompleteInputExactEstimateLimit)
				exact, exactErr := EstimateAnthropicCountTokens(tc.body)
				require.NoError(t, exactErr)
				got, boundedErr := estimateAnthropicInputTokensBounded(tc.body)
				require.NoError(t, boundedErr)
				if tc.low {
					require.Equal(t, exact, got, "exact=%d bounded=%d", exact, got)
				} else {
					diff := got - exact
					if diff < 0 {
						diff = -diff
					}
					require.LessOrEqual(t, float64(diff)/float64(exact), 0.05, "exact=%d bounded=%d", exact, got)
				}
			})
		}
	})

	t.Run("tools_web_search_unknown_and_known_fields", func(t *testing.T) {
		anthropicCases := []struct {
			name  string
			body  []byte
			exact bool
		}{
			{
				name:  "anthropic_tool_web_search_large_schema",
				body:  []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"web_search_20250305","name":"web_search","description":"desc","input_schema":{"extra":"` + over512KNonBase64 + `"}}]}`),
				exact: true,
			},
			{
				name:  "anthropic_tool_unknown_extra_field",
				body:  []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hi"}],"tools":[{"name":"fn","description":"desc","input_schema":{"type":"object"},"cache_control":{"type":"ephemeral"},"extra":"` + over512KNonBase64 + `"}]}`),
				exact: true,
			},
			{
				name:  "anthropic_tool_known_large_description_and_unknown",
				body:  []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hi"}],"tools":[{"name":"fn","description":"` + over512KNonBase64 + `","input_schema":{"type":"object"},"extra":"ignored"}]}`),
				exact: false,
			},
		}
		for _, tc := range anthropicCases {
			t.Run(tc.name, func(t *testing.T) {
				require.Greater(t, len(tc.body), incompleteInputExactEstimateLimit)
				exact, exactErr := EstimateAnthropicCountTokens(tc.body)
				require.NoError(t, exactErr)
				got, boundedErr := estimateAnthropicInputTokensBounded(tc.body)
				require.NoError(t, boundedErr)
				if tc.exact {
					require.Equal(t, exact, got, "exact=%d bounded=%d", exact, got)
				} else {
					diff := got - exact
					if diff < 0 {
						diff = -diff
					}
					require.LessOrEqual(t, float64(diff)/float64(exact), 0.05, "exact=%d bounded=%d", exact, got)
				}
			})
		}

		openAICases := []struct {
			name  string
			body  []byte
			exact bool
		}{
			{
				name:  "openai_tool_unknown_extra",
				body:  []byte(`{"model":"gpt-5.4","input":"hi","tools":[{"type":"function","name":"fn","extra":"` + over512KNonBase64 + `"}]}`),
				exact: true,
			},
			{
				name:  "openai_tool_all_known_fields_retained_with_unknown_extra",
				body:  []byte(`{"model":"gpt-5.4","input":"hi","tools":[{"cache_control":{"type":"ephemeral","ttl":"5m"},"type":"x_search","name":"xs","description":"search x","parameters":{"type":"object","properties":{"q":{"type":"string"}}},"strict":true,"allowed_x_handles":["a","b"],"excluded_x_handles":["c"],"from_date":"2026-01-01","to_date":"2026-12-31","enable_image_understanding":true,"enable_video_understanding":false,"extra":"` + over512KNonBase64 + `"},{"type":"namespace","name":"ns","tools":[{"type":"function","name":"sub1","description":"d1","extra":"` + over4KNonBase64 + `"}],"children":[{"type":"function","name":"sub2"}],"extra":"` + over4KNonBase64 + `"}]}`),
				exact: true,
			},
			{
				name:  "openai_tool_known_large_description_streaming",
				body:  []byte(`{"model":"gpt-5.4","input":"hi","tools":[{"cache_control":{"type":"ephemeral","extra":"` + over4KNonBase64 + `"},"type":"x_search","name":"xs","description":"` + over512KNonBase64 + `","parameters":{"type":"object"},"strict":true,"allowed_x_handles":["a"],"excluded_x_handles":["b"],"from_date":"2026-01-01","to_date":"2026-12-31","enable_image_understanding":true,"enable_video_understanding":false,"tools":[{"type":"function","name":"child","description":"` + over4KNonBase64 + `","extra":"` + over4KNonBase64 + `"}],"extra":"` + over4KNonBase64 + `"}]}`),
				exact: false,
			},
		}
		for _, tc := range openAICases {
			t.Run(tc.name, func(t *testing.T) {
				require.Greater(t, len(tc.body), incompleteInputExactEstimateLimit)
				exact, exactErr := EstimateOpenAIResponsesInputTokens(tc.body)
				require.NoError(t, exactErr)
				got, boundedErr := estimateOpenAIResponsesInputTokensBounded(tc.body)
				require.NoError(t, boundedErr)
				if tc.exact {
					require.Equal(t, exact, got, "exact=%d bounded=%d", exact, got)
				} else {
					diff := got - exact
					if diff < 0 {
						diff = -diff
					}
					require.LessOrEqual(t, float64(diff)/float64(exact), 0.05, "exact=%d bounded=%d", exact, got)
				}
			})
		}
	})

	t.Run("openai_large_escaped_item_part_type_and_image_url", func(t *testing.T) {
		escapedType := strings.Repeat(`tok\n`, 140_000)
		escapedURL := `data:image\/png;base64,` + strings.Repeat(`a\/b`, 140_000)
		cases := []struct {
			name string
			body []byte
			low  bool
		}{
			{
				name: "huge_escaped_item_type",
				body: []byte(`{"model":"gpt-5.4","input":[{"type":"` + escapedType + `","content":"hi"}]}`),
				low:  false,
			},
			{
				name: "huge_escaped_part_type",
				body: []byte(`{"model":"gpt-5.4","input":[{"role":"user","content":[{"type":"` + escapedType + `","text":"ignored"}]}]}`),
				low:  false,
			},
			{
				name: "huge_escaped_data_image_url",
				body: []byte(`{"model":"gpt-5.4","input":[{"role":"user","content":[{"type":"input_image","image_url":"` + escapedURL + `"}]}]}`),
				low:  true,
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				require.Greater(t, len(tc.body), incompleteInputExactEstimateLimit)
				exact, exactErr := EstimateOpenAIResponsesInputTokens(tc.body)
				require.NoError(t, exactErr)
				got, boundedErr := estimateOpenAIResponsesInputTokensBounded(tc.body)
				require.NoError(t, boundedErr)
				if tc.low {
					require.Equal(t, exact, got, "exact=%d bounded=%d", exact, got)
				} else {
					diff := got - exact
					if diff < 0 {
						diff = -diff
					}
					require.LessOrEqual(t, float64(diff)/float64(exact), 0.05, "exact=%d bounded=%d", exact, got)
				}
			})
		}
	})
}

func TestEstimateRequestInputTokens_LiveBillingRound5Parity(t *testing.T) {
	pad := strings.Repeat("billing-token ", 40_000)
	over4KNonBase64 := strings.Repeat("word ", 1_000)
	over512KNonBase64 := strings.Repeat("extra ", 90_000)

	highTokenCases := []struct {
		name string
		body []byte
	}{
		{
			name: "anthropic_block_cap64_then_over4k_cache_control",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"tool_a","input":{"a":1}},{"type":"text","text":"` + pad + `","cache_control":{"type":"ephemeral","extra":"` + over4KNonBase64 + `"}}]}]}`),
		},
		{
			name: "responses_item_cap128_then_over4k_summary",
			body: []byte(`{"model":"gpt-5.4","input":[{"type":"function_call","call_id":"call_1","name":"run_step_with_long_name_for_scratch","arguments":"{\"alpha\":1,\"beta\":2}"},{"type":"reasoning","encrypted_content":"enc","summary":[{"type":"summary_text","text":"` + over4KNonBase64 + `"}]},{"role":"user","content":[{"type":"input_text","text":"` + pad + `"}]}]}`),
		},
		{
			name: "anthropic_tool_choice_tool_huge_name",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hi"}],"tool_choice":{"type":"tool","name":"` + over512KNonBase64 + `","extra":"ignored"}}`),
		},
		{
			name: "openai_huge_escaped_item_type",
			body: []byte(`{"model":"gpt-5.4","input":[{"type":"` + strings.Repeat(`tok\n`, 140_000) + `","content":"hi"}]}`),
		},
	}
	for _, tc := range highTokenCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Greater(t, len(tc.body), incompleteInputExactEstimateLimit)
			raw := estimateRequestInputTokensRaw(tc.body)
			bounded := estimateRequestInputTokens(tc.body)
			require.Greater(t, raw, 50_000)
			require.Greater(t, bounded, 0)
			diff := bounded - raw
			if diff < 0 {
				diff = -diff
			}
			require.LessOrEqual(t, float64(diff)/float64(raw), 0.05, "raw=%d bounded=%d", raw, bounded)
		})
	}

	lowTokenCases := []struct {
		name string
		body []byte
	}{
		{
			name: "tool_choice_auto_extra",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hi"}],"tool_choice":{"type":"auto","extra":"` + over512KNonBase64 + `"}}`),
		},
		{
			name: "tool_choice_any_extra",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hi"}],"tool_choice":{"type":"any","extra":"` + over512KNonBase64 + `"}}`),
		},
		{
			name: "tool_choice_none_extra",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hi"}],"tool_choice":{"type":"none","extra":"` + over512KNonBase64 + `"}}`),
		},
		{
			name: "tool_choice_tool_extra",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hi"}],"tool_choice":{"type":"tool","name":"fn","extra":"` + over512KNonBase64 + `"}}`),
		},
		{
			name: "anthropic_tool_web_search_large_schema",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"web_search_20250305","name":"web_search","description":"desc","input_schema":{"extra":"` + over512KNonBase64 + `"}}]}`),
		},
		{
			name: "anthropic_tool_unknown_extra_field",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hi"}],"tools":[{"name":"fn","description":"desc","input_schema":{"type":"object"},"cache_control":{"type":"ephemeral"},"extra":"` + over512KNonBase64 + `"}]}`),
		},
		{
			name: "openai_tool_unknown_extra",
			body: []byte(`{"model":"gpt-5.4","input":"hi","tools":[{"type":"function","name":"fn","extra":"` + over512KNonBase64 + `"}]}`),
		},
	}
	for _, tc := range lowTokenCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Greater(t, len(tc.body), incompleteInputExactEstimateLimit)
			raw := estimateRequestInputTokensRaw(tc.body)
			bounded := estimateRequestInputTokens(tc.body)
			require.Equal(t, raw, bounded, "raw=%d bounded=%d", raw, bounded)
		})
	}
}

func BenchmarkBoundedTokenEstimator_Anthropic_1MiB_vs_16MiB(b *testing.B) {
	sizes := []struct {
		name string
		reps int
	}{
		{name: "1MiB", reps: 75_000},
		{name: "16MiB", reps: 1_200_000},
	}
	for _, sz := range sizes {
		body := []byte(`{"model":"claude-sonnet-4","system":[{"type":"text","text":"sys"}],"messages":[{"role":"user","content":"` + strings.Repeat("billing-token ", sz.reps) + `"}]}`)
		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				got, err := estimateAnthropicInputTokensBounded(body)
				if err != nil || got <= 0 {
					b.Fatalf("unexpected result: got=%d err=%v", got, err)
				}
			}
		})
	}
}

func BenchmarkBoundedTokenEstimator_OpenAI_HugeEscapedItemType_1MiB_vs_16MiB(b *testing.B) {
	sizes := []struct {
		name string
		reps int
	}{
		{name: "1MiB", reps: 180_000},
		{name: "16MiB", reps: 2_880_000},
	}
	for _, sz := range sizes {
		body := []byte(`{"model":"gpt-5.4","input":[{"type":"` + strings.Repeat(`tok\n`, sz.reps) + `","content":"hi"}]}`)
		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				got, err := estimateOpenAIResponsesInputTokensBounded(body)
				if err != nil || got <= 0 {
					b.Fatalf("unexpected result: got=%d err=%v", got, err)
				}
			}
		})
	}
}

func TestEstimateInputTokensBounded_T5R6Parity(t *testing.T) {
	pad := strings.Repeat("padding-token ", 40_000) // > 512 KiB (~80,000 tokens)
	over4KNonBase64 := strings.Repeat("word ", 1_000)

	t.Run("anthropic_messages_role_normalization", func(t *testing.T) {
		over4KEscapedRole := strings.Repeat(`custom\\role\n`, 350) // > 4 KiB escaped
		over512KRole := strings.Repeat("custom_role ", 46_000)     // > 512 KiB

		cases := []struct {
			name string
			body []byte
			low  bool
		}{
			{
				name: "message_role_over512k",
				body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"` + over512KRole + `","content":"hi"}]}`),
				low:  true,
			},
			{
				name: "message_role_over4k_escaped",
				body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"` + over4KEscapedRole + `","content":"` + pad + `"}]}`),
				low:  false,
			},
			{
				name: "message_role_escaped_assistant",
				body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"\u0061ssistant","content":[{"type":"tool_use","id":"toolu_1","name":"fn","input":{"a":1}},{"type":"text","text":"` + pad + `"}]}]}`),
				low:  false,
			},
			{
				name: "message_role_omitted_and_null",
				body: []byte(`{"model":"claude-sonnet-4","messages":[{"content":"hi"},{"role":null,"content":"` + pad + `"}]}`),
				low:  false,
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				require.Greater(t, len(tc.body), incompleteInputExactEstimateLimit)
				exact, exactErr := EstimateAnthropicCountTokens(tc.body)
				require.NoError(t, exactErr)
				got, boundedErr := estimateAnthropicInputTokensBounded(tc.body)
				require.NoError(t, boundedErr)
				if tc.low {
					require.Equal(t, exact, got, "exact=%d bounded=%d", exact, got)
				} else {
					diff := got - exact
					if diff < 0 {
						diff = -diff
					}
					require.LessOrEqual(t, float64(diff)/float64(exact), 0.05, "exact=%d bounded=%d", exact, got)
				}
			})
		}
	})

	t.Run("openai_input_image_long_header_data_uri", func(t *testing.T) {
		longHeader := `data:text\/plain;param=` + strings.Repeat("p", 600)
		escapedBase64 := strings.Repeat(`a\/b`, 140_000)
		escapedNonBase64 := strings.Repeat(`hello%20\/%20world `, 30_000)
		over64KHeader := `data:text\/plain;param=` + strings.Repeat("p ", 50_000)

		cases := []struct {
			name string
			body []byte
			low  bool
		}{
			{
				name: "long_header_data_uri",
				body: []byte(`{"model":"gpt-5.4","input":[{"role":"user","content":[{"type":"input_image","image_url":"` + longHeader + `,` + escapedBase64 + `"}]}]}`),
				low:  true,
			},
			{
				name: "long_header_data_uri_escaped_nonbase64",
				body: []byte(`{"model":"gpt-5.4","input":[{"role":"user","content":[{"type":"input_image","image_url":"` + longHeader + `,` + escapedNonBase64 + `"}]}]}`),
				low:  true,
			},
			{
				name: "long_header_data_uri_over_64k_header",
				body: []byte(`{"model":"gpt-5.4","input":[{"role":"user","content":[{"type":"input_image","image_url":"` + over64KHeader + `,` + escapedNonBase64 + `"}]}]}`),
				low:  false,
			},
			{
				name: "escaped_data_uri_no_comma_over512k",
				body: []byte(`{"model":"gpt-5.4","input":[{"role":"user","content":[{"type":"input_image","image_url":"data:text\/plain;param=` + escapedNonBase64 + `"}]}]}`),
				low:  false,
			},
			{
				name: "unescaped_non_data_uri_over512k",
				body: []byte(`{"model":"gpt-5.4","input":[{"role":"user","content":[{"type":"input_image","image_url":"https://example.com/` + strings.Repeat("path ", 110_000) + `"}]}]}`),
				low:  false,
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				require.Greater(t, len(tc.body), incompleteInputExactEstimateLimit)
				exact, exactErr := EstimateOpenAIResponsesInputTokens(tc.body)
				require.NoError(t, exactErr)
				got, boundedErr := estimateOpenAIResponsesInputTokensBounded(tc.body)
				require.NoError(t, boundedErr)
				if tc.low {
					require.Equal(t, exact, got, "exact=%d bounded=%d", exact, got)
				} else {
					diff := got - exact
					if diff < 0 {
						diff = -diff
					}
					require.LessOrEqual(t, float64(diff)/float64(exact), 0.05, "exact=%d bounded=%d", exact, got)
				}
			})
		}
	})

	t.Run("escaped_wire_format_tool_choice_and_tools", func(t *testing.T) {
		tokN := strings.Repeat(`tok\n`, 140_000)
		newlines := strings.Repeat(`\n`, 300_000)
		mixed := strings.Repeat(`a\n\r\t\"\\\/\u003c\u0001\u4e2d`, 30_000)

		anthropicCases := []struct {
			name string
			body []byte
		}{
			{
				name: "anthropic_tool_choice_huge_escaped_name_tok_n",
				body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hi"}],"tool_choice":{"type":"tool","name":"` + tokN + `"}}`),
			},
			{
				name: "anthropic_tool_choice_huge_escaped_name_newlines",
				body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hi"}],"tool_choice":{"type":"tool","name":"` + newlines + `"}}`),
			},
			{
				name: "anthropic_tool_choice_huge_escaped_name_mixed",
				body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hi"}],"tool_choice":{"type":"tool","name":"` + mixed + `"}}`),
			},
			{
				name: "anthropic_tools_huge_escaped_name_newlines",
				body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hi"}],"tools":[{"name":"` + newlines + `","description":"d","input_schema":{"type":"object"}}]}`),
			},
			{
				name: "anthropic_tools_huge_escaped_desc_tok_n",
				body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hi"}],"tools":[{"name":"fn","description":"` + tokN + `","input_schema":{"type":"object"}}]}`),
			},
			{
				name: "anthropic_tools_huge_escaped_desc_newlines",
				body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hi"}],"tools":[{"name":"fn","description":"` + newlines + `","input_schema":{"type":"object"}}]}`),
			},
			{
				name: "anthropic_tools_huge_escaped_desc_mixed",
				body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hi"}],"tools":[{"name":"fn","description":"` + mixed + `","input_schema":{"type":"object"}}]}`),
			},
		}
		for _, tc := range anthropicCases {
			t.Run(tc.name, func(t *testing.T) {
				require.Greater(t, len(tc.body), incompleteInputExactEstimateLimit)
				exact, exactErr := EstimateAnthropicCountTokens(tc.body)
				require.NoError(t, exactErr)
				got, boundedErr := estimateAnthropicInputTokensBounded(tc.body)
				require.NoError(t, boundedErr)
				diff := got - exact
				if diff < 0 {
					diff = -diff
				}
				require.LessOrEqual(t, float64(diff)/float64(exact), 0.05, "exact=%d bounded=%d", exact, got)
			})
		}

		openAICases := []struct {
			name string
			body []byte
		}{
			{
				name: "openai_tools_huge_escaped_name_newlines",
				body: []byte(`{"model":"gpt-5.4","input":"hi","tools":[{"type":"function","name":"` + newlines + `","parameters":{"type":"object"}}]}`),
			},
			{
				name: "openai_tools_huge_escaped_desc_tok_n",
				body: []byte(`{"model":"gpt-5.4","input":"hi","tools":[{"type":"function","name":"fn","description":"` + tokN + `","parameters":{"type":"object"}}]}`),
			},
			{
				name: "openai_tools_huge_escaped_desc_newlines",
				body: []byte(`{"model":"gpt-5.4","input":"hi","tools":[{"type":"function","name":"fn","description":"` + newlines + `","parameters":{"type":"object"}}]}`),
			},
			{
				name: "openai_tools_huge_escaped_desc_mixed",
				body: []byte(`{"model":"gpt-5.4","input":"hi","tools":[{"type":"function","name":"fn","description":"` + mixed + `","parameters":{"type":"object"}}]}`),
			},
		}
		for _, tc := range openAICases {
			t.Run(tc.name, func(t *testing.T) {
				require.Greater(t, len(tc.body), incompleteInputExactEstimateLimit)
				exact, exactErr := EstimateOpenAIResponsesInputTokens(tc.body)
				require.NoError(t, exactErr)
				got, boundedErr := estimateOpenAIResponsesInputTokensBounded(tc.body)
				require.NoError(t, boundedErr)
				diff := got - exact
				if diff < 0 {
					diff = -diff
				}
				require.LessOrEqual(t, float64(diff)/float64(exact), 0.05, "exact=%d bounded=%d", exact, got)
			})
		}
	})

	t.Run("openai_namespace_depth_and_whitespace_x_handles", func(t *testing.T) {
		buildDeepNamespaceTool := func(levels int, leafDesc string) string {
			var sb strings.Builder
			for i := 0; i < levels; i++ {
				sb.WriteString(`{"type":"namespace","name":"ns","tools":[`)
			}
			sb.WriteString(`{"type":"function","name":"leaf","description":"` + leafDesc + `","parameters":{"type":"object"}}`)
			for i := 0; i < levels; i++ {
				sb.WriteString(`]}`)
			}
			return sb.String()
		}

		// 35 levels via streaming (> 64 KiB nested description) and via inline filter.
		deepStreaming := []byte(`{"model":"gpt-5.4","input":"hi","tools":[` + buildDeepNamespaceTool(35, strings.Repeat("deep ", 110_000)) + `]}`)
		require.Greater(t, len(deepStreaming), incompleteInputExactEstimateLimit)
		exactDeep, err := EstimateOpenAIResponsesInputTokens(deepStreaming)
		require.NoError(t, err)
		gotDeep, err := estimateOpenAIResponsesInputTokensBounded(deepStreaming)
		require.NoError(t, err)
		diff := gotDeep - exactDeep
		if diff < 0 {
			diff = -diff
		}
		require.LessOrEqual(t, float64(diff)/float64(exactDeep), 0.05, "exact=%d bounded=%d", exactDeep, gotDeep)

		// 35 levels with 65 KiB leaf + extra unknown field (still forces streaming).
		deepSmallLeaf := []byte(`{"model":"gpt-5.4","input":"hi","tools":[` + buildDeepNamespaceTool(35, strings.Repeat("leaf ", 14_000)) + `],"padding":"` + pad + `"}`)
		require.Greater(t, len(deepSmallLeaf), incompleteInputExactEstimateLimit)
		exactSmallLeaf, err := EstimateOpenAIResponsesInputTokens(deepSmallLeaf)
		require.NoError(t, err)
		gotSmallLeaf, err := estimateOpenAIResponsesInputTokensBounded(deepSmallLeaf)
		require.NoError(t, err)
		diffLeaf := gotSmallLeaf - exactSmallLeaf
		if diffLeaf < 0 {
			diffLeaf = -diffLeaf
		}
		require.LessOrEqual(t, float64(diffLeaf)/float64(exactSmallLeaf), 0.05, "exact=%d bounded=%d", exactSmallLeaf, gotSmallLeaf)

		// Exceeding maxBoundedResponsesToolDepth (258 levels) must stay safe and not emit malformed trailing commas.
		deep258 := []byte(`{"model":"gpt-5.4","input":"` + pad + `","tools":[` + buildDeepNamespaceTool(258, over4KNonBase64) + `]}`)
		require.Greater(t, len(deep258), incompleteInputExactEstimateLimit)
		got258, err := estimateOpenAIResponsesInputTokensBounded(deep258)
		require.NoError(t, err)
		require.Greater(t, got258, 80_000)

		// Whitespace-only empty allowed_x_handles / excluded_x_handles in streaming > 64 KiB tool must be omitted like omitempty.
		wsHandles := []byte(`{"model":"gpt-5.4","input":"hi","tools":[{"type":"x_search","name":"xs","description":"` + strings.Repeat("x ", 270_000) + `","allowed_x_handles":[   ],"excluded_x_handles":[` + "\n\t " + `],"extra":"` + over4KNonBase64 + `"}]}`)
		require.Greater(t, len(wsHandles), incompleteInputExactEstimateLimit)
		exactWS, err := EstimateOpenAIResponsesInputTokens(wsHandles)
		require.NoError(t, err)
		gotWS, err := estimateOpenAIResponsesInputTokensBounded(wsHandles)
		require.NoError(t, err)
		diffWS := gotWS - exactWS
		if diffWS < 0 {
			diffWS = -diffWS
		}
		require.LessOrEqual(t, float64(diffWS)/float64(exactWS), 0.05, "exact=%d bounded=%d", exactWS, gotWS)

		// Also verify whitespace-only > 64 KiB allowed_x_handles / excluded_x_handles produces the exact same estimate as omitting them
		// both in filtered JSON path and in streaming fallback path.
		wsHugeHandles := []byte(`{"model":"gpt-5.4","input":"hi","tools":[{"type":"x_search","name":"xs","allowed_x_handles":[` + strings.Repeat(" ", 70_000) + `],"excluded_x_handles":[` + strings.Repeat(" ", 500_000) + `]}]}`)
		require.Greater(t, len(wsHugeHandles), incompleteInputExactEstimateLimit)
		exactHugeWS, err := EstimateOpenAIResponsesInputTokens(wsHugeHandles)
		require.NoError(t, err)
		gotHugeWS, err := estimateOpenAIResponsesInputTokensBounded(wsHugeHandles)
		require.NoError(t, err)
		require.Equal(t, exactHugeWS, gotHugeWS)

		hugeDesc := strings.Repeat("d ", 35_000)
		wsStreamWithHandles := []byte(`{"model":"gpt-5.4","input":"hi","tools":[{"type":"x_search","name":"xs","description":"` + hugeDesc + `","allowed_x_handles":[   ],"excluded_x_handles":[` + strings.Repeat(" ", 500_000) + `]}]}`)
		wsStreamNoHandles := []byte(`{"model":"gpt-5.4","input":"hi","tools":[{"type":"x_search","name":"xs","description":"` + hugeDesc + `","extra":"` + strings.Repeat(" ", 500_000) + `"}]}`)
		require.Greater(t, len(wsStreamWithHandles), incompleteInputExactEstimateLimit)
		gotStreamWithHandles, err := estimateOpenAIResponsesInputTokensBounded(wsStreamWithHandles)
		require.NoError(t, err)
		gotStreamNoHandles, err := estimateOpenAIResponsesInputTokensBounded(wsStreamNoHandles)
		require.NoError(t, err)
		require.Equal(t, gotStreamNoHandles, gotStreamWithHandles)
	})
}

func TestEstimateRequestInputTokens_LiveBillingRound6Parity(t *testing.T) {
	pad := strings.Repeat("billing-token ", 40_000)
	over4KEscapedRole := strings.Repeat(`custom\\role\n`, 350)
	over512KRole := strings.Repeat("custom_role ", 46_000)
	longHeader := `data:text\/plain;param=` + strings.Repeat("p", 600)
	escapedNonBase64 := strings.Repeat(`hello%20\/%20world `, 30_000)
	newlines := strings.Repeat(`\n`, 300_000)

	var deepSB strings.Builder
	for i := 0; i < 35; i++ {
		deepSB.WriteString(`{"type":"namespace","name":"ns","tools":[`)
	}
	deepSB.WriteString(`{"type":"function","name":"leaf","description":"` + strings.Repeat("deep ", 110_000) + `","parameters":{"type":"object"}}`)
	for i := 0; i < 35; i++ {
		deepSB.WriteString(`]}`)
	}

	lowTokenCases := []struct {
		name string
		body []byte
	}{
		{
			name: "anthropic_message_role_over512k",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"` + over512KRole + `","content":"hi"}]}`),
		},
		{
			name: "openai_long_header_data_uri_escaped_nonbase64",
			body: []byte(`{"model":"gpt-5.4","input":[{"role":"user","content":[{"type":"input_image","image_url":"` + longHeader + `,` + escapedNonBase64 + `"}]}]}`),
		},
	}
	for _, tc := range lowTokenCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Greater(t, len(tc.body), incompleteInputExactEstimateLimit)
			raw := estimateRequestInputTokensRaw(tc.body)
			bounded := estimateRequestInputTokens(tc.body)
			require.Equal(t, raw, bounded, "raw=%d bounded=%d", raw, bounded)
		})
	}

	highTokenCases := []struct {
		name string
		body []byte
	}{
		{
			name: "anthropic_message_role_over4k_escaped",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"` + over4KEscapedRole + `","content":"` + pad + `"}]}`),
		},
		{
			name: "anthropic_tool_choice_huge_escaped_name_newlines",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hi"}],"tool_choice":{"type":"tool","name":"` + newlines + `"}}`),
		},
		{
			name: "anthropic_tool_huge_escaped_desc_newlines",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hi"}],"tools":[{"name":"fn","description":"` + newlines + `","input_schema":{"type":"object"}}]}`),
		},
		{
			name: "openai_tool_huge_escaped_desc_newlines",
			body: []byte(`{"model":"gpt-5.4","input":"hi","tools":[{"type":"function","name":"fn","description":"` + newlines + `","parameters":{"type":"object"}}]}`),
		},
		{
			name: "openai_tools_deep_namespace_35_levels",
			body: []byte(`{"model":"gpt-5.4","input":"hi","tools":[` + deepSB.String() + `]}`),
		},
	}
	for _, tc := range highTokenCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Greater(t, len(tc.body), incompleteInputExactEstimateLimit)
			raw := estimateRequestInputTokensRaw(tc.body)
			bounded := estimateRequestInputTokens(tc.body)
			require.Greater(t, raw, 50_000)
			require.Greater(t, bounded, 0)
			diff := bounded - raw
			if diff < 0 {
				diff = -diff
			}
			require.LessOrEqual(t, float64(diff)/float64(raw), 0.05, "raw=%d bounded=%d", raw, bounded)
		})
	}
}

func BenchmarkBoundedTokenEstimator_OpenAI_EscapedToolDesc_1MiB_vs_16MiB(b *testing.B) {
	sizes := []struct {
		name string
		reps int
	}{
		{name: "1MiB", reps: 180_000},
		{name: "16MiB", reps: 2_880_000},
	}
	for _, sz := range sizes {
		body := []byte(`{"model":"gpt-5.4","input":"hi","tools":[{"type":"function","name":"fn","description":"` + strings.Repeat(`tok\n`, sz.reps) + `","parameters":{"type":"object"}}]}`)
		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				got, err := estimateOpenAIResponsesInputTokensBounded(body)
				if err != nil || got <= 0 {
					b.Fatalf("unexpected result: got=%d err=%v", got, err)
				}
			}
		})
	}
}

func BenchmarkBoundedTokenEstimator_OpenAI_LongDataURIHeader_1MiB_vs_16MiB(b *testing.B) {
	sizes := []struct {
		name string
		reps int
	}{
		{name: "1MiB", reps: 450_000},
		{name: "16MiB", reps: 7_500_000},
	}
	for _, sz := range sizes {
		body := []byte(`{"model":"gpt-5.4","input":[{"role":"user","content":[{"type":"input_image","image_url":"data:text\/plain;param=` + strings.Repeat("p ", sz.reps) + `,hello%20\/%20world"}]}]}`)
		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				got, err := estimateOpenAIResponsesInputTokensBounded(body)
				if err != nil || got <= 0 {
					b.Fatalf("unexpected result: got=%d err=%v", got, err)
				}
			}
		})
	}
}

func TestExtractBoundedDataURIHeader_TrailingWhitespaceBoundaries(t *testing.T) {
	cases := []struct {
		name           string
		inner          string
		maxSampleBytes int
		wantOK         bool
		wantDst        string
		wantTotal      int64
	}{
		{
			name:           "trailing_spaces_exceed_max_sample",
			inner:          `data:image\/png;base64` + strings.Repeat(" ", 64) + `,AAAA`,
			maxSampleBytes: 30,
			wantOK:         true,
			wantDst:        "data:image/png;base64",
			wantTotal:      21,
		},
		{
			name:           "internal_spaces_preserved_when_followed_by_nonspace_past_cap",
			inner:          `data:image\/png;   abc` + strings.Repeat(" ", 40) + `,AAAA`,
			maxSampleBytes: 17, // "data:image/png;  " (17 bytes, stops inside internal spaces before 'abc')
			wantOK:         true,
			wantDst:        "data:image/png;  ",
			wantTotal:      21, // len("data:image/png;   abc")
		},
		{
			name:           "unicode_space_and_escaped_comma",
			inner:          `  \u0020data:text\/plain\n\t\u00a0 \u002cAAAA`,
			maxSampleBytes: 64,
			wantOK:         true,
			wantDst:        "data:text/plain",
			wantTotal:      15,
		},
		{
			name:           "only_spaces_after_scheme_before_comma",
			inner:          `data:   \n\t ,AAAA`,
			maxSampleBytes: 64,
			wantOK:         true,
			wantDst:        "data:",
			wantTotal:      5,
		},
		{
			name:           "zero_sample_budget",
			inner:          `data:image\/png;base64   \n ,AAAA`,
			maxSampleBytes: 0,
			wantOK:         true,
			wantDst:        "",
			wantTotal:      21,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotDst, gotTotal, ok := extractBoundedDataURIHeader(nil, []byte(tc.inner), tc.maxSampleBytes)
			require.Equal(t, tc.wantOK, ok)
			require.Equal(t, tc.wantDst, string(gotDst))
			require.Equal(t, tc.wantTotal, gotTotal)
		})
	}
}

func TestEstimateInputTokensBounded_T5R7Parity(t *testing.T) {
	pad := strings.Repeat("padding-token ", 40_000)

	t.Run("openai_input_image_data_uri_trailing_whitespace", func(t *testing.T) {
		cases := []struct {
			name string
			body []byte
			low  bool
		}{
			{
				name: "unescaped_600k_spaces_before_comma",
				body: []byte(`{"model":"gpt-5.4","input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64` + strings.Repeat(" ", 600_000) + `,AAAA"}]}]}`),
				low:  true,
			},
			{
				name: "escaped_600k_spaces_before_comma",
				body: []byte(`{"model":"gpt-5.4","input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image\/png;base64` + strings.Repeat(" ", 600_000) + `,AAAA"}]}]}`),
				low:  true,
			},
			{
				name: "escaped_300k_newlines_before_comma",
				body: []byte(`{"model":"gpt-5.4","input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image\/png;base64` + strings.Repeat(`\n`, 300_000) + `,AAAA"}]}]}`),
				low:  true,
			},
			{
				name: "escaped_leading_and_trailing_spaces_before_comma",
				body: []byte(`{"model":"gpt-5.4","input":[{"role":"user","content":[{"type":"input_image","image_url":"  \u0020data:\n\t \u00a0 ,` + strings.Repeat("A", 600_000) + `"}]}]}`),
				low:  true,
			},
			{
				name: "inline_escaped_trailing_spaces_before_comma",
				body: []byte(`{"model":"gpt-5.4","input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image\/png;base64   \n\t ,AAAA"},{"type":"input_text","text":"` + pad + `"}]}]}`),
				low:  false,
			},
			{
				name: "escaped_header_over64k_with_trailing_spaces_and_escaped_comma",
				body: []byte(`{"model":"gpt-5.4","input":[{"role":"user","content":[{"type":"input_image","image_url":"data:text\/plain;param=` + strings.Repeat("p ", 150_000) + strings.Repeat(`\n `, 100_000) + `\u002cAAAA"}]}]}`),
				low:  false,
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				require.Greater(t, len(tc.body), incompleteInputExactEstimateLimit)
				exact, exactErr := EstimateOpenAIResponsesInputTokens(tc.body)
				require.NoError(t, exactErr)
				got, boundedErr := estimateOpenAIResponsesInputTokensBounded(tc.body)
				require.NoError(t, boundedErr)
				if tc.low {
					require.Equal(t, exact, got, "exact=%d bounded=%d", exact, got)
				} else {
					diff := got - exact
					if diff < 0 {
						diff = -diff
					}
					require.LessOrEqual(t, float64(diff)/float64(exact), 0.05, "exact=%d bounded=%d", exact, got)
				}
			})
		}
	})

	t.Run("anthropic_messages_content_part_and_item_overhead", func(t *testing.T) {
		ignoredThinking := strings.Repeat(`,{"type":"thinking","thinking":"x"}`, 20_000)
		ignoredNullAndUnknown := strings.Repeat(`,null,{"type":"custom_unknown","text":"ignored"}`, 12_000)
		manyAssistantTexts := strings.Repeat(`,{"type":"text","text":"x"}`, 20_000)
		manyWhitespaceTexts := strings.Repeat(`,{"type":"text","text":"   "}`, 20_000)
		manyEmptyTexts := strings.Repeat(`,{"type":"text","text":""}`, 22_000)
		manyToolResultNulls := strings.Repeat(`,null,{"type":"thinking","thinking":"y"}`, 14_000)

		cases := []struct {
			name string
			body []byte
			low  bool
		}{
			{
				name: "assistant_20001_ignored_thinking_blocks",
				body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"x"}` + ignoredThinking + `]}]}`),
				low:  true,
			},
			{
				name: "user_20001_ignored_thinking_and_null_blocks",
				body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":[{"type":"thinking","thinking":"x"}` + ignoredNullAndUnknown + `]}]}`),
				low:  true,
			},
			{
				name: "assistant_mixed_gAAAA_and_valid_signature_thinking",
				body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"x","signature":"gAAAAskip"},{"type":"thinking","thinking":"y","signature":"EqQBCgIYAhIMvalid"}` + ignoredThinking + `]}]}`),
				low:  true,
			},
			{
				name: "assistant_multiple_text_blocks_single_output_text_part",
				body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"assistant","content":[{"type":"text","text":"x"}` + manyAssistantTexts + `]}]}`),
				low:  false,
			},
			{
				name: "assistant_20001_whitespace_only_text_blocks",
				body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"assistant","content":[{"type":"text","text":"   "}` + manyWhitespaceTexts + `]}]}`),
				low:  true,
			},
			{
				name: "user_20001_empty_text_blocks",
				body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":[{"type":"text","text":""}` + manyEmptyTexts + `]}]}`),
				low:  true,
			},
			{
				name: "user_tool_result_blocks_with_many_ignored_inner_and_image",
				body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"text","text":"ok"}` + manyToolResultNulls + `,{"type":"image","source":{"type":"base64","media_type":"image/jpeg","data":"AAAA"}}]}]}` + `]}`),
				low:  true,
			},
			{
				name: "user_image_blocks_valid_and_empty_data",
				body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + strings.Repeat("A", 550_000) + `"}},{"type":"image","source":{"type":"base64","media_type":"image/png","data":""}}]}]}`),
				low:  true,
			},
			{
				name: "assistant_tool_use_omitted_and_null_input",
				body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"fn"},{"type":"tool_use","id":"toolu_2","name":"fn2","input":null}` + ignoredThinking + `]}]}`),
				low:  true,
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				require.Greater(t, len(tc.body), incompleteInputExactEstimateLimit)
				exact, exactErr := EstimateAnthropicCountTokens(tc.body)
				require.NoError(t, exactErr)
				got, boundedErr := estimateAnthropicInputTokensBounded(tc.body)
				require.NoError(t, boundedErr)
				if tc.low {
					require.Equal(t, exact, got, "exact=%d bounded=%d", exact, got)
				} else {
					diff := got - exact
					if diff < 0 {
						diff = -diff
					}
					require.LessOrEqual(t, float64(diff)/float64(exact), 0.05, "exact=%d bounded=%d", exact, got)
				}
			})
		}
	})

	t.Run("openai_streaming_tool_escaped_x_handles", func(t *testing.T) {
		escapedHandle := strings.Repeat(`\u0041`, 10) + `\/` + strings.Repeat(`\u0042`, 10)
		manyEscapedHandles := `"` + escapedHandle + `"` + strings.Repeat(`,"`+escapedHandle+`"`, 4_000)
		hugeEscapedHandles := `"` + escapedHandle + `"` + strings.Repeat(`,"`+escapedHandle+`"`, 5_000)
		htmlHandles := `"\u003ctag\u003e\u0026handle\/1"` + strings.Repeat(`,"\u003ctag\u003e\u0026handle\/1"`, 15_000)
		streamingExtra := strings.Repeat("extra ", 12_000) // > 64 KiB unknown field forces observeResponsesToolSpanStreaming

		cases := []struct {
			name string
			body []byte
		}{
			{
				name: "allowed_x_handles_escaped_streaming",
				body: []byte(`{"model":"gpt-5.4","input":"hi","tools":[{"type":"x_search","name":"xs","extra":"` + streamingExtra + `","allowed_x_handles":[` + manyEscapedHandles + `]}]}`),
			},
			{
				name: "excluded_x_handles_escaped_with_null_streaming",
				body: []byte(`{"model":"gpt-5.4","input":"hi","tools":[{"type":"x_search","name":"xs","extra":"` + streamingExtra + `","excluded_x_handles":[null,` + manyEscapedHandles + `]}]}`),
			},
			{
				name: "huge_allowed_x_handles_escaped_over512k",
				body: []byte(`{"model":"gpt-5.4","input":"hi","tools":[{"type":"x_search","name":"xs","allowed_x_handles":[` + hugeEscapedHandles + `]}]}`),
			},
			{
				name: "excluded_x_handles_html_chars_no_escape_html",
				body: []byte(`{"model":"gpt-5.4","input":"hi","tools":[{"type":"x_search","name":"xs","extra":"` + streamingExtra + `","excluded_x_handles":[` + htmlHandles + `]}]}`),
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				require.Greater(t, len(tc.body), incompleteInputExactEstimateLimit)
				exact, exactErr := EstimateOpenAIResponsesInputTokens(tc.body)
				require.NoError(t, exactErr)
				got, boundedErr := estimateOpenAIResponsesInputTokensBounded(tc.body)
				require.NoError(t, boundedErr)
				diff := got - exact
				if diff < 0 {
					diff = -diff
				}
				require.LessOrEqual(t, float64(diff)/float64(exact), 0.05, "exact=%d bounded=%d", exact, got)
			})
		}
	})
}

func TestEstimateRequestInputTokens_LiveBillingRound7Parity(t *testing.T) {
	ignoredThinking := strings.Repeat(`,{"type":"thinking","thinking":"x"}`, 20_000)
	ignoredNullAndUnknown := strings.Repeat(`,null,{"type":"custom_unknown","text":"ignored"}`, 12_000)
	escapedHandle := strings.Repeat(`\u0041`, 10) + `\/` + strings.Repeat(`\u0042`, 10)
	manyEscapedHandles := `"` + escapedHandle + `"` + strings.Repeat(`,"`+escapedHandle+`"`, 4_000)
	streamingExtra := strings.Repeat("extra ", 12_000)

	lowTokenCases := []struct {
		name string
		body []byte
	}{
		{
			name: "unescaped_600k_spaces_before_comma",
			body: []byte(`{"model":"gpt-5.4","input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64` + strings.Repeat(" ", 600_000) + `,AAAA"}]}]}`),
		},
		{
			name: "escaped_600k_spaces_before_comma",
			body: []byte(`{"model":"gpt-5.4","input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image\/png;base64` + strings.Repeat(" ", 600_000) + `,AAAA"}]}]}`),
		},
		{
			name: "escaped_300k_newlines_before_comma",
			body: []byte(`{"model":"gpt-5.4","input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image\/png;base64` + strings.Repeat(`\n`, 300_000) + `,AAAA"}]}]}`),
		},
		{
			name: "anthropic_assistant_20001_ignored_thinking_blocks",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"x"}` + ignoredThinking + `]}]}`),
		},
		{
			name: "anthropic_user_20001_ignored_thinking_and_null_blocks",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":[{"type":"thinking","thinking":"x"}` + ignoredNullAndUnknown + `]}]}`),
		},
	}
	for _, tc := range lowTokenCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Greater(t, len(tc.body), incompleteInputExactEstimateLimit)
			raw := estimateRequestInputTokensRaw(tc.body)
			bounded := estimateRequestInputTokens(tc.body)
			require.Equal(t, raw, bounded, "raw=%d bounded=%d", raw, bounded)
		})
	}

	highTokenCases := []struct {
		name string
		body []byte
	}{
		{
			name: "openai_streaming_tool_escaped_allowed_x_handles",
			body: []byte(`{"model":"gpt-5.4","input":"hi","tools":[{"type":"x_search","name":"xs","extra":"` + streamingExtra + `","allowed_x_handles":[` + manyEscapedHandles + `]}]}`),
		},
		{
			name: "openai_streaming_tool_escaped_excluded_x_handles",
			body: []byte(`{"model":"gpt-5.4","input":"hi","tools":[{"type":"x_search","name":"xs","extra":"` + streamingExtra + `","excluded_x_handles":[` + manyEscapedHandles + `]}]}`),
		},
	}
	for _, tc := range highTokenCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Greater(t, len(tc.body), incompleteInputExactEstimateLimit)
			raw := estimateRequestInputTokensRaw(tc.body)
			bounded := estimateRequestInputTokens(tc.body)
			require.Greater(t, raw, 20_000)
			require.Greater(t, bounded, 0)
			diff := bounded - raw
			if diff < 0 {
				diff = -diff
			}
			require.LessOrEqual(t, float64(diff)/float64(raw), 0.05, "raw=%d bounded=%d", raw, bounded)
		})
	}
}

func BenchmarkBoundedTokenEstimator_OpenAI_EscapedXHandles_1MiB_vs_16MiB(b *testing.B) {
	escapedHandle := strings.Repeat(`\u0041`, 10) + `\/`
	sizes := []struct {
		name string
		reps int
	}{
		{name: "1MiB", reps: 15_000},
		{name: "16MiB", reps: 240_000},
	}
	for _, sz := range sizes {
		handles := `"` + escapedHandle + `"` + strings.Repeat(`,"`+escapedHandle+`"`, sz.reps)
		body := []byte(`{"model":"gpt-5.4","input":"hi","tools":[{"type":"x_search","name":"xs","allowed_x_handles":[` + handles + `]}]}`)
		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				got, err := estimateOpenAIResponsesInputTokensBounded(body)
				if err != nil || got <= 0 {
					b.Fatalf("unexpected result: got=%d err=%v", got, err)
				}
			}
		})
	}
}

func BenchmarkBoundedTokenEstimator_Anthropic_IgnoredThinking_1MiB_vs_16MiB(b *testing.B) {
	sizes := []struct {
		name string
		reps int
	}{
		{name: "1MiB", reps: 1 << 20},
		{name: "16MiB", reps: 16 << 20},
	}
	for _, sz := range sizes {
		body := []byte(`{"model":"claude-sonnet-4","messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"` + strings.Repeat("x", sz.reps) + `"}]}]}`)
		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				got, err := estimateAnthropicInputTokensBounded(body)
				if err != nil || got != 1 {
					b.Fatalf("unexpected result: got=%d err=%v", got, err)
				}
			}
		})
	}
}

func BenchmarkBoundedTokenEstimator_OpenAI_LongDataURIHeaderTrailingSpace_1MiB_vs_16MiB(b *testing.B) {
	sizes := []struct {
		name string
		reps int
	}{
		{name: "1MiB", reps: 500_000},
		{name: "16MiB", reps: 8_000_000},
	}
	for _, sz := range sizes {
		body := []byte(`{"model":"gpt-5.4","input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image\/png;base64` + strings.Repeat(`\n`, sz.reps) + `,AAAA"}]}]}`)
		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				got, err := estimateOpenAIResponsesInputTokensBounded(body)
				if err != nil || got != 10 {
					b.Fatalf("unexpected result: got=%d err=%v", got, err)
				}
			}
		})
	}
}

func TestEstimateInputTokensBounded_T5R8Parity(t *testing.T) {
	pad := strings.Repeat("padding-token ", 40_000)

	t.Run("anthropic_system_and_system_array_overhead", func(t *testing.T) {
		wsBlocks25k := `{"type":"text","text":"   "}` + strings.Repeat(`,{"type":"text","text":"   "}`, 24_999)
		shortBlocks25k := `{"type":"text","text":"a"}` + strings.Repeat(`,{"type":"text","text":"a"}`, 24_999)

		cases := []struct {
			name string
			body []byte
			low  bool
		}{
			{
				name: "system_array_25k_whitespace_blocks",
				body: []byte(`{"model":"claude-sonnet-4","system":[` + wsBlocks25k + `],"messages":[{"role":"user","content":"hi"}]}`),
				low:  true,
			},
			{
				name: "system_array_25k_short_text_blocks",
				body: []byte(`{"model":"claude-sonnet-4","system":[` + shortBlocks25k + `],"messages":[{"role":"user","content":"hi"}]}`),
				low:  false,
			},
			{
				name: "system_array_empty_and_billing_header_only_over512k",
				body: []byte(`{"model":"claude-sonnet-4","system":[{"type":"text","text":""},{"type":"text","text":"x-anthropic-billing-header: ` + pad + `"},{"type":"text","text":"\u0078-anthropic-billing-header: escaped"},{"type":"thinking","thinking":"x"}],"messages":[{"role":"user","content":"hi"}]}`),
				low:  true,
			},
			{
				name: "system_array_billing_header_then_whitespace_over512k",
				body: []byte(`{"model":"claude-sonnet-4","system":[{"type":"text","text":"x-anthropic-billing-header: ` + pad + `"},{"type":"text","text":"   "}],"messages":[{"role":"user","content":"hi"}]}`),
				low:  true,
			},
			{
				name: "system_string_whitespace_over512k",
				body: []byte(`{"model":"claude-sonnet-4","system":"` + strings.Repeat(" ", 600_000) + `","messages":[{"role":"user","content":"hi"}]}`),
				low:  true,
			},
			{
				name: "system_string_billing_header_over512k",
				body: []byte(`{"model":"claude-sonnet-4","system":"x-anthropic-billing-header: ` + pad + `","messages":[{"role":"user","content":"hi"}]}`),
				low:  true,
			},
			{
				name: "system_string_empty_over512k",
				body: []byte(`{"model":"claude-sonnet-4","system":"","messages":[{"role":"user","content":"` + pad + `"}]}`),
				low:  false,
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				require.Greater(t, len(tc.body), incompleteInputExactEstimateLimit)
				exact, exactErr := EstimateAnthropicCountTokens(tc.body)
				require.NoError(t, exactErr)
				got, boundedErr := estimateAnthropicInputTokensBounded(tc.body)
				require.NoError(t, boundedErr)
				if tc.low {
					require.Equal(t, exact, got, "exact=%d bounded=%d", exact, got)
				} else {
					diff := got - exact
					if diff < 0 {
						diff = -diff
					}
					require.LessOrEqual(t, float64(diff)/float64(exact), 0.05, "exact=%d bounded=%d", exact, got)
				}
			})
		}
	})

	t.Run("anthropic_tool_use_pretty_printed_input", func(t *testing.T) {
		prettyEntry := "\n    {\n      \"k\": \"value\",\n      \"items\": [\n        1,\n        2,\n        3\n      ]\n    }"
		prettyArray := "[" + prettyEntry + strings.Repeat(","+prettyEntry, 7_500) + "\n  ]"
		prettyObject := "{\n  \"data\": " + prettyArray + "\n}"

		prettyPrefixArray := "[" + prettyEntry + strings.Repeat(","+prettyEntry, 1_100) + "\n  ]"
		prettyPrefixObject := "{\n  \"data\": " + prettyPrefixArray + "\n}"

		cases := []struct {
			name string
			body []byte
		}{
			{
				name: "pretty_printed_input_over512k",
				body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"fn","input":` + prettyObject + `}]}]}`),
			},
			{
				name: "pretty_printed_input_after_64k_sample_budget",
				body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"assistant","content":[{"type":"tool_use","id":"toolu_0","name":"fn0","input":` + prettyPrefixObject + `},{"type":"tool_use","id":"toolu_2","name":"fn","input":` + prettyObject + `}]}]}`),
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				require.Greater(t, len(tc.body), incompleteInputExactEstimateLimit)
				exact, exactErr := EstimateAnthropicCountTokens(tc.body)
				require.NoError(t, exactErr)
				got, boundedErr := estimateAnthropicInputTokensBounded(tc.body)
				require.NoError(t, boundedErr)
				diff := got - exact
				if diff < 0 {
					diff = -diff
				}
				require.LessOrEqual(t, float64(diff)/float64(exact), 0.05, "exact=%d bounded=%d", exact, got)
			})
		}
	})

	t.Run("openai_responses_nonstring_pretty_printed_output", func(t *testing.T) {
		prettyEntry := "\n  {\n    \"row\": \"alpha\",\n    \"vals\": [\n      10,\n      20,\n      30\n    ]\n  }"
		prettyArray := "[" + prettyEntry + strings.Repeat(","+prettyEntry, 8_000) + "\n]"
		prettyObject := "{\n  \"results\": " + prettyArray + "\n}"

		prettyPrefixArray := "[" + prettyEntry + strings.Repeat(","+prettyEntry, 1_100) + "\n]"

		cases := []struct {
			name string
			body []byte
		}{
			{
				name: "pretty_printed_array_output_over512k",
				body: []byte(`{"model":"gpt-5.4","input":[{"type":"function_call_output","call_id":"call_1","output":` + prettyArray + `}]}`),
			},
			{
				name: "pretty_printed_object_output_after_64k_sample_budget",
				body: []byte(`{"model":"gpt-5.4","input":[{"type":"function_call_output","call_id":"call_0","output":` + prettyPrefixArray + `},{"type":"function_call_output","call_id":"call_2","output":` + prettyObject + `}]}`),
			},
			{
				name: "nonstring_null_number_bool_empty_array_output_over512k",
				body: []byte(`{"model":"gpt-5.4","input":[{"type":"function_call_output","call_id":"call_n","output":null},{"type":"function_call_output","call_id":"call_num","output":12345},{"type":"function_call_output","call_id":"call_bool","output":true},{"type":"function_call_output","call_id":"call_arr","output":[]},{"role":"user","content":"` + pad + `"}]}`),
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				require.Greater(t, len(tc.body), incompleteInputExactEstimateLimit)
				exact, exactErr := EstimateOpenAIResponsesInputTokens(tc.body)
				require.NoError(t, exactErr)
				got, boundedErr := estimateOpenAIResponsesInputTokensBounded(tc.body)
				require.NoError(t, boundedErr)
				diff := got - exact
				if diff < 0 {
					diff = -diff
				}
				require.LessOrEqual(t, float64(diff)/float64(exact), 0.05, "exact=%d bounded=%d", exact, got)
			})
		}
	})

	t.Run("observeStringArrayFieldNormalized_budget_exhausted_len", func(t *testing.T) {
		codec, err := openAIInputTokensCodecForModel("gpt-5.4")
		require.NoError(t, err)

		arrJSON := []byte(`["alpha","beta"]`)
		estUnder := newBoundedTokenEstimator(codec)
		estUnder.sampledTextBytes = estUnder.sampleBudget - 128
		estUnder.totalTextBytes = estUnder.sampledTextBytes
		require.NoError(t, estUnder.observeStringArrayFieldNormalized(arrJSON, "allowed_x_handles", 0, len(arrJSON), false))

		estAt := newBoundedTokenEstimator(codec)
		estAt.sampledTextBytes = estAt.sampleBudget
		estAt.totalTextBytes = estAt.sampleBudget - 128
		require.NoError(t, estAt.observeStringArrayFieldNormalized(arrJSON, "allowed_x_handles", 0, len(arrJSON), false))

		require.Equal(t, estUnder.totalTextBytes, estAt.totalTextBytes)
	})

	t.Run("observeRawBytesSpan_preserves_whitespace_under_and_at_budget", func(t *testing.T) {
		codec, err := openAIInputTokensCodecForModel("gpt-5.4")
		require.NoError(t, err)

		raw := []byte("{\n  \"k\": \"v\",\n  \"arr\": [\n    1,\n    2\n  ]\n}")
		estUnder := newBoundedTokenEstimator(codec)
		require.NoError(t, estUnder.observeRawBytesSpan(raw, 0, len(raw)))
		require.Equal(t, int64(len(raw)), estUnder.totalTextBytes)

		estAt := newBoundedTokenEstimator(codec)
		estAt.sampledTextBytes = estAt.sampleBudget
		require.NoError(t, estAt.observeRawBytesSpan(raw, 0, len(raw)))
		require.Equal(t, int64(len(raw)), estAt.totalTextBytes)
	})
}

func TestEstimateRequestInputTokens_LiveBillingRound8Parity(t *testing.T) {
	pad := strings.Repeat("padding-token ", 40_000)
	wsBlocks25k := `{"type":"text","text":"   "}` + strings.Repeat(`,{"type":"text","text":"   "}`, 24_999)
	shortBlocks25k := `{"type":"text","text":"a"}` + strings.Repeat(`,{"type":"text","text":"a"}`, 24_999)
	prettyEntry := "\n    {\n      \"k\": \"value\",\n      \"items\": [\n        1,\n        2,\n        3\n      ]\n    }"
	prettyObject := "{\n  \"data\": [" + prettyEntry + strings.Repeat(","+prettyEntry, 7_500) + "\n  ]\n}"
	prettyOutArray := "[" + prettyEntry + strings.Repeat(","+prettyEntry, 7_500) + "\n]"

	lowCases := []struct {
		name string
		body []byte
	}{
		{
			name: "anthropic_system_25k_whitespace_blocks",
			body: []byte(`{"model":"claude-sonnet-4","system":[` + wsBlocks25k + `],"messages":[{"role":"user","content":"hi"}]}`),
		},
		{
			name: "anthropic_system_billing_header_only_over512k",
			body: []byte(`{"model":"claude-sonnet-4","system":[{"type":"text","text":"x-anthropic-billing-header: ` + pad + `"}],"messages":[{"role":"user","content":"hi"}]}`),
		},
	}
	for _, tc := range lowCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Greater(t, len(tc.body), incompleteInputExactEstimateLimit)
			raw := estimateRequestInputTokensRaw(tc.body)
			bounded := estimateRequestInputTokens(tc.body)
			require.Equal(t, raw, bounded, "raw=%d bounded=%d", raw, bounded)
		})
	}

	highCases := []struct {
		name string
		body []byte
	}{
		{
			name: "anthropic_system_25k_short_text_blocks",
			body: []byte(`{"model":"claude-sonnet-4","system":[` + shortBlocks25k + `],"messages":[{"role":"user","content":"hi"}]}`),
		},
		{
			name: "anthropic_tool_use_pretty_printed_input_over512k",
			body: []byte(`{"model":"claude-sonnet-4","messages":[{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"fn","input":` + prettyObject + `}]}]}`),
		},
		{
			name: "openai_responses_nonstring_pretty_printed_output_over512k",
			body: []byte(`{"model":"gpt-5.4","input":[{"type":"function_call_output","call_id":"call_1","output":` + prettyOutArray + `}]}`),
		},
	}
	for _, tc := range highCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Greater(t, len(tc.body), incompleteInputExactEstimateLimit)
			raw := estimateRequestInputTokensRaw(tc.body)
			bounded := estimateRequestInputTokens(tc.body)
			require.Greater(t, raw, 20_000)
			require.Greater(t, bounded, 0)
			diff := bounded - raw
			if diff < 0 {
				diff = -diff
			}
			require.LessOrEqual(t, float64(diff)/float64(raw), 0.05, "raw=%d bounded=%d", raw, bounded)
		})
	}
}

func BenchmarkBoundedTokenEstimator_Anthropic_SystemArray_1MiB_vs_16MiB(b *testing.B) {
	sizes := []struct {
		name string
		reps int
	}{
		{name: "1MiB", reps: 35_000},
		{name: "16MiB", reps: 560_000},
	}
	for _, sz := range sizes {
		blocks := `{"type":"text","text":"   "}` + strings.Repeat(`,{"type":"text","text":"   "}`, sz.reps)
		body := []byte(`{"model":"claude-sonnet-4","system":[` + blocks + `],"messages":[{"role":"user","content":"hi"}]}`)
		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				got, err := estimateAnthropicInputTokensBounded(body)
				if err != nil || got <= 0 {
					b.Fatalf("unexpected result: got=%d err=%v", got, err)
				}
			}
		})
	}
}

func BenchmarkBoundedTokenEstimator_Anthropic_PrettyToolUseInput_1MiB_vs_16MiB(b *testing.B) {
	prettyEntry := "\n    {\n      \"k\": \"value\",\n      \"items\": [\n        1,\n        2,\n        3\n      ]\n    }"
	sizes := []struct {
		name string
		reps int
	}{
		{name: "1MiB", reps: 14_000},
		{name: "16MiB", reps: 224_000},
	}
	for _, sz := range sizes {
		inputJSON := "{\n  \"data\": [" + prettyEntry + strings.Repeat(","+prettyEntry, sz.reps) + "\n  ]\n}"
		body := []byte(`{"model":"claude-sonnet-4","messages":[{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"fn","input":` + inputJSON + `}]}]}`)
		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				got, err := estimateAnthropicInputTokensBounded(body)
				if err != nil || got <= 0 {
					b.Fatalf("unexpected result: got=%d err=%v", got, err)
				}
			}
		})
	}
}

func BenchmarkBoundedTokenEstimator_OpenAI_NonStringPrettyOutput_1MiB_vs_16MiB(b *testing.B) {
	prettyEntry := "\n  {\n    \"row\": \"alpha\",\n    \"vals\": [\n      10,\n      20,\n      30\n    ]\n  }"
	sizes := []struct {
		name string
		reps int
	}{
		{name: "1MiB", reps: 14_000},
		{name: "16MiB", reps: 224_000},
	}
	for _, sz := range sizes {
		outputJSON := "[" + prettyEntry + strings.Repeat(","+prettyEntry, sz.reps) + "\n]"
		body := []byte(`{"model":"gpt-5.4","input":[{"type":"function_call_output","call_id":"call_1","output":` + outputJSON + `}]}`)
		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				got, err := estimateOpenAIResponsesInputTokensBounded(body)
				if err != nil || got <= 0 {
					b.Fatalf("unexpected result: got=%d err=%v", got, err)
				}
			}
		})
	}
}
