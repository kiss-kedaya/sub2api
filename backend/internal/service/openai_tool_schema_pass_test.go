package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func sequentialToolSchemaPasses(body []byte, platform string) ([]byte, bool, error) {
	normalized := body
	changed := false
	if shouldRepairOpenAIResponsesNullToolSchemaType(platform) {
		next, repaired, err := sanitizeOpenAIResponsesToolParameterTypes(normalized)
		if err != nil {
			return body, false, fmt.Errorf("sanitize OpenAI Responses tool parameters: %w", err)
		}
		if repaired {
			normalized, changed = next, true
		}
	}
	if shouldSanitizeOpenAIResponsesToolSchemaPatterns(platform) {
		next, repaired, err := sanitizeOpenAIResponsesToolSchemaPatterns(normalized)
		if err != nil {
			return body, false, fmt.Errorf("sanitize OpenAI Responses tool schema patterns: %w", err)
		}
		if repaired {
			normalized, changed = next, true
		}
	}
	return normalized, changed, nil
}

func TestToolSchemaPlatformPassesPreserveWireContract(t *testing.T) {
	inputs := []string{
		``, `{}`, `null`, `[]`,
		`{"input":"say (hello)","prompt_cache_key":"cache-session","previous_response_id":"resp_prior"}`,
		`{"prompt_cache_key":"cache-session","tools":[{"type":"function","name":"lookup","parameters":{"type":null,"pattern":"(?=a)","properties":{"query":{"type":"string","pattern":"(?!b)","default":"(?=keep)"}},"required":["query"]}}],"input":[{"type":"reasoning","encrypted_content":"opaque-cache-tag"},{"type":"function_call_output","call_id":"c1","output":"(?=keep-output)"}],"metadata":{"cache_control":{"type":"ephemeral"}}}`,
		`{"tools":[{"function":{"parameters":{"pattern":"(?=a)","type":null}}}]}`,
		`{"tools":[{"parameters":{"pattern":"(?=a)","oneOf":[{"type":"object","properties":{"x":{"pattern":"(?<=x)"}}}]}}]}`,
		`{"tools":[{"parameters":{"type":null,"type":"string","pattern":"(?=a)","pattern":"(?!b)","title":"keep"}}]}`,
		`{"tools":[{"parameters":{"pattern":{"properties":{"x":{"type":null}}},"type":null,"default":{"pattern":"(?=keep)","type":null}}}]}`,
		`{"tools":[{"parameters":{"ty\u0070e":null,"pat\u0074ern":"\u0028?=a)"}}]}`,
		`{"input":[{"tools":[{"type":"function","parameters":{"pattern":"(?=a)","type":null}}],"type":"additional_tools"}],"usage":{"input_tokens_details":{"cached_tokens":128}}}`,
		`{"tools":[{"parameters":{"oneOf":[{"type":"object"}],"pattern":"(?=a)"}}]}`,
		`{"tools":[{"parameters":{"pattern":"(?=a)","anyOf":[{"type":"object"}]}}]}`,
		`{"tools":[{"parameters":{"type":null,"pattern":"(?=a)",}}]}`,
		`{"tools":[{"parameters":{"type":null,"pattern":"(?=a)"}}]} trailing`,
		`{"input":"\x","tools":[{"parameters":{"type":null}}]}`,
		`{"tools":[{"parameters":{"type":null,"pattern":"(?=a)"}}],"input":` + strings.Repeat(`[`, 130) + `0` + strings.Repeat(`]`, 130) + `}`,
	}
	for _, platform := range []string{PlatformOpenAI, PlatformGrok, PlatformAnthropic, PlatformKimi, PlatformGemini} {
		for i, input := range inputs {
			t.Run(fmt.Sprintf("%s/%d", platform, i), func(t *testing.T) {
				body := []byte(input)
				before := append([]byte(nil), body...)
				want, wantChanged, wantErr := sequentialToolSchemaPasses(body, platform)
				got, changed, err := sanitizeOpenAIResponsesToolSchemasForPlatform(body, platform)
				require.Equal(t, fmt.Sprint(wantErr), fmt.Sprint(err))
				require.Equal(t, wantChanged, changed)
				require.True(t, bytes.Equal(want, got), "wire bytes differ")
				require.True(t, bytes.Equal(before, body), "input body was mutated")
			})
		}
	}
}

func FuzzToolSchemaPlatformMatchesSequential(f *testing.F) {
	f.Add([]byte(`{"tools":[{"parameters":{"type":null,"pattern":"(?=a)"}}]}`))
	f.Add([]byte(`{"tools":[{"parameters":{"oneOf":[{"type":"object"}],"pattern":"(?=a)"}}]}`))
	f.Add([]byte(`{"input":[{"type":"additional_tools","tools":[{"parameters":{"type":null,"pattern":"(?=a)"}}]}]}`))
	f.Fuzz(func(t *testing.T, body []byte) {
		if len(body) > 65536 {
			t.Skip()
		}
		want, wantChanged, wantErr := sequentialToolSchemaPasses(body, PlatformOpenAI)
		got, changed, err := sanitizeOpenAIResponsesToolSchemasForPlatform(body, PlatformOpenAI)
		require.Equal(t, fmt.Sprint(wantErr), fmt.Sprint(err))
		require.Equal(t, wantChanged, changed)
		require.Equal(t, want, got)
	})
}

func BenchmarkToolSchemaPlatformPasses(b *testing.B) {
	for _, size := range []int{4096, 1 << 20} {
		for _, repair := range []bool{false, true} {
			parameterType := any("object")
			if repair {
				parameterType = nil
			}
			body, err := json.Marshal(map[string]any{
				"input":            strings.Repeat("plain (text) ", size/13),
				"prompt_cache_key": "cache-bench",
				"tools": []any{map[string]any{"type": "function", "parameters": map[string]any{
					"type": parameterType, "properties": map[string]any{"x": map[string]any{"type": "string", "pattern": "(?=prefix)"}},
				}}},
			})
			require.NoError(b, err)
			for _, optimized := range []bool{false, true} {
				b.Run(fmt.Sprintf("bytes_%d/repair_%t/optimized_%t", size, repair, optimized), func(b *testing.B) {
					b.SetBytes(int64(len(body)))
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						var err error
						if optimized {
							_, _, err = sanitizeOpenAIResponsesToolSchemasForPlatform(body, PlatformOpenAI)
						} else {
							_, _, err = sequentialToolSchemaPasses(body, PlatformOpenAI)
						}
						if err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		}
	}
}
