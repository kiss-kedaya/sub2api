//go:build unit

package service

import (
	"encoding/base64"
	"encoding/json"
	"math/rand"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func randomBase64(seed int64, rawBytes int) string {
	raw := make([]byte, rawBytes)
	_, _ = rand.New(rand.NewSource(seed)).Read(raw)
	return base64.StdEncoding.EncodeToString(raw)
}

func TestInlineOpaqueBlobFolding_Boundaries(t *testing.T) {
	long := randomBase64(7, 3000) // 4000 base64 characters
	run := func(n int) string { return strings.Repeat("A", n) }
	countStr := func(rawJSONString string) int {
		var c fastTokenCounter
		b := []byte(rawJSONString)
		c.observeJSONString(b, 0, len(b))
		return c.tokens()
	}

	// Just under opaqueBlobMinRun stays counted as text; at/above threshold folds to ~2 tokens.
	under := countStr(`"` + run(opaqueBlobMinRun-1) + `"`)
	atThreshold := countStr(`"` + run(opaqueBlobMinRun) + `"`)
	require.Greater(t, under, 200, "run just under threshold must still count as text")
	require.Equal(t, 2, atThreshold, "run at threshold must fold inline to blob placeholder")

	// Data URI keeps header (~10 tokens) and folds base64 payload.
	dataURI := countStr(`"data:image/png;base64,` + long + `"`)
	require.Less(t, dataURI, 20)

	// Escaped slashes (\/) belong to the base64 run; other escapes (\n) break the run.
	escapedSlashes := countStr(`"` + strings.Repeat(`ab\/`, 400) + `"`)
	require.Equal(t, 2, escapedSlashes)
	brokenRun := countStr(`"` + strings.Repeat(run(600)+`\n`, 3) + `"`)
	require.Greater(t, brokenRun, 300)

	// Two blobs separated by prose fold independently while keeping prose counted.
	twoBlobs := countStr(`"` + long + ` hello world ` + long + `"`)
	require.Greater(t, twoBlobs, 4)
	require.Less(t, twoBlobs, 20)

	// Odd / truncated / non-JSON inputs never panic.
	for _, in := range []string{`"` + long, `{"x":"` + long, `{"x":"` + long + `\`, strings.Repeat(`"`, 5000), strings.Repeat(`\`, 5000), long} {
		require.NotPanics(t, func() { _ = estimateRequestInputTokens([]byte(in)) }, in[:min(len(in), 20)])
	}
}

// The bug this guards: a few screenshots in a request were counted as text, giving
// 9 to 12 million input tokens for a session that completed at 170 to 230 thousand.
func TestEstimateRequestInputTokens_BlobsAreNotText(t *testing.T) {
	blob := randomBase64(9, 300_000) // 400 KB of base64
	text := strings.Repeat("The quick brown fox jumps over the lazy dog. ", 100)
	mk := func(input any) []byte {
		b, _ := json.Marshal(map[string]any{"model": "gpt-5.4", "input": input})
		return b
	}
	image := "data:image/png;base64," + blob
	control := mk([]any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": text}}}})
	shapes := map[string][]byte{
		"function_call_output string": mk([]any{map[string]any{"type": "function_call_output", "call_id": "c1", "output": image}}),
		"function_call_output array":  mk([]any{map[string]any{"type": "function_call_output", "call_id": "c1", "output": []any{map[string]any{"type": "input_image", "image_url": image}}}}),
		"image_url object":            mk([]any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_image", "image_url": map[string]any{"url": image}}}}}),
	}
	for name, body := range shapes {
		t.Run(name, func(t *testing.T) {
			raw := estimateRequestInputTokensRaw(body)
			require.Greater(t, raw, 100_000, "the unfolded estimate is the bug: base64 counted as text")
			got := estimateRequestInputTokens(body)
			require.Less(t, got, 200, "a folded screenshot costs a few tokens, not hundreds of thousands")
			n, err := estimateResponsesInputTokensForBilling(body)
			require.NoError(t, err)
			require.Equal(t, got, n)
		})
	}
	t.Run("a body without blobs stays calibrated to exact tiktoken", func(t *testing.T) {
		raw := estimateRequestInputTokensRaw(control)
		got := estimateRequestInputTokens(control)
		require.Greater(t, raw, 500)
		require.InDelta(t, float64(raw), float64(got), float64(raw)*0.20, "fast=%d raw=%d", got, raw)
	})
	t.Run("text next to a screenshot still counts", func(t *testing.T) {
		both := mk([]any{
			map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": text}}},
			map[string]any{"type": "function_call_output", "call_id": "c1", "output": image},
		})
		require.GreaterOrEqual(t, estimateRequestInputTokens(both), estimateRequestInputTokens(control))
	})
	t.Run("anthropic image block", func(t *testing.T) {
		b, _ := json.Marshal(map[string]any{"model": "claude-sonnet-4", "max_tokens": 64, "messages": []any{map[string]any{
			"role": "user", "content": []any{
				map[string]any{"type": "text", "text": "describe this"},
				map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": blob}},
			}}}})
		// Image base64 data must never inflate the fast fallback estimate.
		require.Less(t, estimateRequestInputTokensRaw(b), 200)
		require.Greater(t, estimateRequestInputTokens(b), 0)
		require.Less(t, estimateRequestInputTokens(b), 200)
	})
}

func TestClampIncompleteInputTokens(t *testing.T) {
	require.Equal(t, 0, clampIncompleteInputTokens(0))
	require.Equal(t, 1_700_000, clampIncompleteInputTokens(1_700_000))
	require.Equal(t, maxIncompleteInputTokens, clampIncompleteInputTokens(maxIncompleteInputTokens))
	require.Equal(t, maxIncompleteInputTokens, clampIncompleteInputTokens(12_319_222))
}

// A request carrying screenshots that is cut off is billed for its text, not for the
// base64 of its images; the preauthorization reserve keeps the raw upper bound.
func TestFillIncompleteStreamUsage_FoldsBlobsAndPreauthKeepsTheRawBound(t *testing.T) {
	image := "data:image/png;base64," + randomBase64(10, 300_000)
	body, _ := json.Marshal(map[string]any{"model": "gpt-5.4", "input": []any{
		map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": "look at this"}}},
		map[string]any{"type": "function_call_output", "call_id": "c1", "output": image},
	}})
	usage := &OpenAIUsage{}
	_, in := fillIncompleteStreamUsage(usage, 0, body)
	require.Greater(t, in, 0)
	require.Less(t, in, 200)
	require.Equal(t, in, usage.InputTokens)
	require.True(t, usage.CacheReadEstimated)
	require.Greater(t, estimateBalancePreauthorizationInputTokens(body), 100_000, "the reserve is meant to stay conservative")
}

// A Responses body with no input field (only instructions and a previous_response_id)
// is not placed by the shape dispatcher; the Responses estimator still counts its text.
func TestEstimateIncompleteResponsesInput(t *testing.T) {
	onlyInstructions := []byte(`{"model":"gpt-5.4","previous_response_id":"resp_1","instructions":"` + strings.Repeat("Answer briefly. ", 40) + `"}`)
	require.Zero(t, estimateRequestInputTokens(onlyInstructions))
	got, err := estimateIncompleteResponsesInput(onlyInstructions)
	require.NoError(t, err)
	require.Greater(t, got, 50)

	withInput := []byte(`{"model":"gpt-5.4","input":"` + strings.Repeat("hello there ", 50) + `"}`)
	got, err = estimateIncompleteResponsesInput(withInput)
	require.NoError(t, err)
	require.Equal(t, estimateRequestInputTokens(withInput), got)

	_, err = estimateIncompleteResponsesInput([]byte("not json"))
	require.Error(t, err)
}
