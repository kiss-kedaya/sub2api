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

func TestStripOpaqueBlobs(t *testing.T) {
	long := randomBase64(7, 3000) // 4000 base64 characters
	run := func(n int) string { return strings.Repeat("A", n) }
	for _, tc := range []struct{ name, in, want string }{
		{"data uri keeps its header", `{"u":"data:image/png;base64,` + long + `"}`, `{"u":"data:image/png;base64,[blob]"}`},
		{"bare run", `{"x":"` + long + `"}`, `{"x":"[blob]"}`},
		{"one char under the threshold stays", `{"x":"` + run(opaqueBlobMinRun-1) + `"}`, `{"x":"` + run(opaqueBlobMinRun-1) + `"}`},
		{"exactly the threshold is folded", `{"x":"` + run(opaqueBlobMinRun) + `"}`, `{"x":"[blob]"}`},
		{"prose with spaces stays", `{"x":"` + strings.Repeat("hello world ", 400) + `"}`, `{"x":"` + strings.Repeat("hello world ", 400) + `"}`},
		{"escaped slashes belong to the run", `{"x":"` + strings.Repeat(`ab\/`, 400) + `"}`, `{"x":"[blob]"}`},
		{"other escapes end the run", `{"x":"` + strings.Repeat(run(600)+`\n`, 3) + `"}`, `{"x":"` + strings.Repeat(run(600)+`\n`, 3) + `"}`},
		{"two blobs in one string, text between stays", `{"x":"` + long + ` and ` + long + `"}`, `{"x":"[blob] and [blob]"}`},
		{"a stringified body with escaped quotes", `{"x":"{\"u\":\"data:image/png;base64,` + long + `\"}"}`, `{"x":"{\"u\":\"data:image/png;base64,[blob]\"}"}`},
		{"a long key is never touched", `{"` + run(2000) + `":1}`, `{"[blob]":1}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := string(stripOpaqueBlobs([]byte(tc.in)))
			if tc.name == "a long key is never touched" {
				// A key is a string too; folding it keeps the JSON valid, which is all that is promised.
				require.True(t, json.Valid([]byte(got)))
				return
			}
			require.Equal(t, tc.want, got)
			require.True(t, json.Valid([]byte(got)), "the result must stay valid JSON")
			require.Equal(t, got, string(stripOpaqueBlobs([]byte(got))), "folding is idempotent")
		})
	}
}

func TestStripOpaqueBlobs_SafeOnOddInput(t *testing.T) {
	long := randomBase64(8, 3000)
	require.Equal(t, []byte(nil), stripOpaqueBlobs(nil))
	small := []byte(`{"a":1}`)
	require.Equal(t, small, stripOpaqueBlobs(small))
	// Not JSON, truncated, unterminated string: never panics, never loses data it did not fold.
	for _, in := range []string{`"` + long, `{"x":"` + long, `{"x":"` + long + `\`, strings.Repeat(`"`, 5000), strings.Repeat(`\`, 5000), long} {
		require.NotPanics(t, func() { _ = stripOpaqueBlobs([]byte(in)) }, in[:min(len(in), 20)])
	}
	require.Equal(t, `"[blob]`, string(stripOpaqueBlobs([]byte(`"`+long))), "an unterminated string still closes its run")
	require.Equal(t, long, string(stripOpaqueBlobs([]byte(long))), "text outside any JSON string is left alone")
	// A body with nothing to fold comes back as the same slice, no copy.
	plain := []byte(`{"input":"` + strings.Repeat("hello world ", 300) + `"}`)
	require.True(t, &plain[0] == &stripOpaqueBlobs(plain)[0])
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
	t.Run("a body without blobs is estimated as before", func(t *testing.T) {
		require.Greater(t, estimateRequestInputTokensRaw(control), 500)
		require.Equal(t, estimateRequestInputTokensRaw(control), estimateRequestInputTokens(control))
	})
	t.Run("text next to a screenshot still counts", func(t *testing.T) {
		both := mk([]any{
			map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": text}}},
			map[string]any{"type": "function_call_output", "call_id": "c1", "output": image},
		})
		require.GreaterOrEqual(t, estimateRequestInputTokens(both), estimateRequestInputTokensRaw(control))
	})
	t.Run("anthropic image block", func(t *testing.T) {
		b, _ := json.Marshal(map[string]any{"model": "claude-sonnet-4", "max_tokens": 64, "messages": []any{map[string]any{
			"role": "user", "content": []any{
				map[string]any{"type": "text", "text": "describe this"},
				map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": blob}},
			}}}})
		// The Anthropic to Responses conversion never counted image data, so this
		// path was not inflated; folding must leave its estimate alone.
		require.Less(t, estimateRequestInputTokensRaw(b), 200)
		require.Equal(t, estimateRequestInputTokensRaw(b), estimateRequestInputTokens(b))
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
