package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestOpenAIRequestParseAuditJSONPatternEscapes(t *testing.T) {
	for _, pattern := range []string{
		`(?=path)\/`,
		`(?!path)\/`,
		`(?<=path)\/`,
		`(?<!path)\/`,
		`(?=\ud83d\ude00)`,
		`\u0028?=\ud83d\ude00)\/`,
		`(?=\"quoted\")`,
		`(?=\\path)`,
		`(?=\b\f\n\r\t)`,
		`(?=\u0000\u007f\u0800\uffff)`,
		`(?=\uD83D\uDE00)`,
		`(?=\ud800)`,
		`(?=\udc00)`,
	} {
		t.Run(pattern, func(t *testing.T) {
			for _, template := range []string{
				`{"tools":[{"type":"function","parameters":%s}],"opaque":9007199254740993}`,
				`{"input":[{"parameters":%s,"type":"function"}],"opaque":9007199254740993}`,
				`{"input":[{"type":"additional_tools","tools":[{"parameters":%s}]}]}`,
			} {
				// Only the schema constraint is removed. Identical instance data survives.
				unchanged := `"default":{"pattern":"` + pattern + `"},"type":"string"`
				body := []byte(fmt.Sprintf(template, `{"pattern":"`+pattern+`",`+unchanged+`}`))
				want := []byte(fmt.Sprintf(template, `{`+unchanged+`}`))
				require.True(t, json.Valid(body))
				original := bytes.Clone(body)
				got, changed, err := sanitizeOpenAIResponsesToolSchemasForPlatform(body, PlatformOpenAI)
				require.NoError(t, err)
				require.True(t, changed)
				require.Equal(t, string(want), string(got))
				require.Equal(t, original, body)
				again, changed, err := sanitizeOpenAIResponsesToolSchemasForPlatform(got, PlatformOpenAI)
				require.NoError(t, err)
				require.False(t, changed)
				require.Equal(t, got, again)
			}
		})
	}
}

func TestOpenAIRequestParseAuditPortablePatterns(t *testing.T) {
	for _, pattern := range []string{`^(path\/name)$`, `^(\ud83d\ude00)$`, `^[a-z]+$`} {
		body := []byte(`{"tools":[{"parameters":{"type":"string","pattern":"` + pattern + `"}}]}`)
		got, changed, err := sanitizeOpenAIResponsesToolSchemasForPlatform(body, PlatformOpenAI)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, body, got)
	}
	body := []byte(`{"tools":[{"parameters":{"type":"string","pattern":"(?=path)\/"}}]}`)
	got, changed, err := sanitizeOpenAIResponsesToolSchemasForPlatform(body, PlatformGrok)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, body, got)
}

func TestOpenAIRequestParseAuditHistoryBoundaries(t *testing.T) {
	for _, item := range []string{
		`{"type":"message","content":[{"type":"input_text","text":"(opaque)"}],"parameters":{"type":null,"pattern":"(?=keep)"}}`,
		`{"content":{"tools":[{"parameters":{"type":null,"pattern":"(?=keep)"}}]},"ty\u0070e":"message"}`,
		`{"type":"reasoning","summary":[],"encrypted_content":"opaque","parameters":{"type":null}}`,
		`{"type":"function_call_output","output":"(result)","parameters":{"type":null}}`,
		`{"type":"function","type":"message","parameters":{"type":null}}`,
		`{"type":null,"parameters":{"type":null}}`,
		`{"parameters":{"type":null}}`,
	} {
		body := []byte(" \n{\"input\":[" + item + `,{"parameters":{"type":null},"type":"function"}],"tail":-0.00}` + "\t")
		want := bytes.Replace(body, []byte(`{"parameters":{"type":null},"type":"function"}`), []byte(`{"parameters":{"type":"object"},"type":"function"}`), 1)
		got, changed, err := sanitizeOpenAIResponsesToolSchemasForPlatform(body, PlatformOpenAI)
		require.NoError(t, err)
		require.True(t, changed)
		require.Equal(t, string(want), string(got))
	}
}

func TestOpenAIRequestParseAuditMalformedHistory(t *testing.T) {
	for _, content := range []string{
		"\"raw\x00nul\"", `"\x28"`, `"\u002"`, `"\q"`, `01`, `1e+`, `[1,]`, `{"x":1,}`, `{"x" 1}`,
	} {
		for _, template := range []string{
			`{"input":[{"type":"message","content":%s}]}`,
			`{"input":[{"content":%s,"type":"message"}]}`,
		} {
			body := []byte(fmt.Sprintf(template, content))
			require.False(t, json.Valid(body))
			_, changed, err := sanitizeOpenAIResponsesToolParameterTypes(body)
			require.Error(t, err)
			require.False(t, changed)
		}
	}
}

func TestOpenAIRequestParseAuditDuplicateHistoryType(t *testing.T) {
	for _, tc := range []struct {
		fields string
		repair bool
	}{
		{`"type":"function","type":"message"`, false},
		{`"type":"message","ty\u0070e":"function"`, true},
		// Preserve the existing probe's last-string rule, including non-string duplicates.
		{`"type":"function","type":null`, true},
		{`"type":"function","type":[]`, true},
		{`"type":"function","type":""`, false},
	} {
		body := []byte(`{"input":[{` + tc.fields + `,"parameters":{"type":null}}]}`)
		want := body
		if tc.repair {
			want = bytes.Replace(body, []byte(`"parameters":{"type":null}`), []byte(`"parameters":{"type":"object"}`), 1)
		}
		got, changed, err := sanitizeOpenAIResponsesToolSchemasForPlatform(body, PlatformOpenAI)
		require.NoError(t, err)
		require.Equal(t, tc.repair, changed)
		require.Equal(t, string(want), string(got))
	}
}

func TestOpenAIRequestParseAuditHistoryDepthGuard(t *testing.T) {
	for _, depth := range []int{125, 126} {
		body := []byte(`{"input":[{"type":"message","content":` + strings.Repeat("[", depth) +
			`null` + strings.Repeat("]", depth) + `}],"tools":[{"parameters":{"type":null}}]}`)
		got, changed, err := sanitizeOpenAIResponsesToolSchemasForPlatform(body, PlatformOpenAI)
		require.NoError(t, err)
		if depth == 125 {
			require.True(t, changed)
			require.Equal(t, bytes.Replace(body, []byte(`"type":null`), []byte(`"type":"object"`), 1), got)
		} else {
			require.False(t, changed)
			require.Same(t, &body[0], &got[0])
		}
	}
}

func FuzzOpenAIRequestParseAuditHistory(f *testing.F) {
	for _, seed := range []string{
		`null`, `"(text)"`, `{"tools":[{"parameters":{"type":null,"pattern":"(?=keep)"}}]}`,
		`["\ud83d\ude00","\/",9007199254740993]`, `{"same":1,"same":2}`, `{"bad":01}`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, content string) {
		if len(content) > 64<<10 {
			t.Skip()
		}
		body := []byte(`{"input":[{"content":` + content + `,"type":"message"}]}`)
		// Valid instance data must never be edited, including duplicate keys.
		if !utf8.ValidString(content) || !json.Valid([]byte(content)) {
			return
		}
		original := bytes.Clone(body)
		got, changed, err := sanitizeOpenAIResponsesToolSchemasForPlatform(body, PlatformOpenAI)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, original, got)
		require.Equal(t, original, body)
	})
}

func BenchmarkOpenAIRequestParseAuditHistory(b *testing.B) {
	for _, size := range []int{1024, 1 << 20, 8 << 20} {
		for _, tools := range []struct {
			name string
			raw  string
		}{
			{"NoTools", ""},
			{"Repair", `,"tools":[{"type":"function","parameters":{"type":null,"pattern":"(?=x)"}}]`},
		} {
			b.Run(fmt.Sprintf("%s/%d", tools.name, size), func(b *testing.B) {
				body := []byte(`{"input":[{"type":"message","role":"user","content":"` + strings.Repeat("x", size) + `"}]` + tools.raw + `}`)
				b.SetBytes(int64(len(body)))
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					_, changed, err := sanitizeOpenAIResponsesToolSchemasForPlatform(body, PlatformOpenAI)
					if err != nil || changed != (tools.raw != "") {
						b.Fatalf("changed=%v err=%v", changed, err)
					}
				}
			})
		}
	}
}
