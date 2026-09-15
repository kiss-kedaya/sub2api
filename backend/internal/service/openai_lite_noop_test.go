package service

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func originalLiteParallelToolCalls(body []byte) ([]byte, bool, error) {
	var requestBody map[string]any
	if err := decodeOpenAIJSONUseNumber(body, &requestBody); err != nil {
		return body, false, fmt.Errorf("decode responses Lite request body: %w", err)
	}
	changed, err := ensureOpenAIResponsesLiteParallelToolCalls(requestBody, false)
	if err != nil || !changed {
		return body, false, err
	}
	rebuilt, err := marshalOpenAIUpstreamJSON(requestBody)
	if err != nil {
		return body, false, fmt.Errorf("encode responses Lite request body: %w", err)
	}
	return rebuilt, true, nil
}

func TestLiteParallelNoopPreservesOriginalContract(t *testing.T) {
	for i, input := range []string{
		`{"parallel_tool_calls":false,"prompt_cache_key":"cache-id","input":[{"type":"reasoning","encrypted_content":"opaque"},{"type":"message","content":[{"text":"hello","cache_control":{"type":"ephemeral"}}]}],"sequence":900719925474099312345}`,
		`{"parallel_tool_calls":true,"parallel_tool_calls":false}`,
		`{"parallel_tool_calls":false,"parallel_tool_calls":true}`,
		`{"parallel_tool_calls":false,"parallel_tool_calls":null}`,
		`{"parallel_tool_calls":false,"parallel_tool_calls":"false"}`,
		`{"parallel_tool_calls":false,"parallel_tool_calls":{}}`,
		`{"parallel_tool_calls":false,"parallel_tool_call\u0073":true}`,
		`{"parallel_tool_calls":true,"parallel_tool_call\u0073":false}`,
		`{"input":{"parallel_tool_calls":false}}`,
		`{"parallel_tool_calls":false,"input":1e999999}`,
		`{"parallel_tool_calls":false} {"another":"value"}`,
		`{"parallel_tool_calls":false,"input":"\x"}`,
		`{"parallel_tool_calls":false,"input":[}`, `[]`, `1`, ``,
	} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			body := []byte(input)
			original := append([]byte(nil), body...)
			want, wantChanged, wantErr := originalLiteParallelToolCalls(body)
			got, changed, err := normalizeOpenAIResponsesLiteParallelToolCallsPayload(body)
			require.Equal(t, fmt.Sprint(wantErr), fmt.Sprint(err))
			require.Equal(t, wantChanged, changed)
			require.Equal(t, want, got)
			require.True(t, bytes.Equal(original, body))
		})
	}
}

func BenchmarkLiteParallelNoop(b *testing.B) {
	body := []byte(`{"parallel_tool_calls":false,"input":"` + strings.Repeat("x", 1<<20) + `","prompt_cache_key":"cache-key"}`)
	for _, optimized := range []bool{false, true} {
		b.Run(fmt.Sprint(optimized), func(b *testing.B) {
			b.SetBytes(int64(len(body)))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				var err error
				if optimized {
					_, _, err = normalizeOpenAIResponsesLiteParallelToolCallsPayload(body)
				} else {
					_, _, err = originalLiteParallelToolCalls(body)
				}
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
