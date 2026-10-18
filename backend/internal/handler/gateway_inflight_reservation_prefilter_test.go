package handler

import (
	"testing"

	"github.com/tidwall/gjson"
	"github.com/stretchr/testify/require"
)

// legacyRequestMaxOutputTokens is the pre-optimization implementation.
func legacyRequestMaxOutputTokens(body []byte) int {
	for _, path := range []string{"max_tokens", "max_completion_tokens", "max_output_tokens", "generationConfig.maxOutputTokens", "generation_config.max_output_tokens"} {
		if v := gjson.GetBytes(body, path); v.Exists() && v.Type == gjson.Number && v.Int() > 0 {
			return int(v.Int())
		}
	}
	return 0
}

func TestRequestMaxOutputTokensMatchesLegacy(t *testing.T) {
	cases := []string{
		`{"max_tokens":1024}`,
		`{"max_completion_tokens":2048}`,
		`{"max_output_tokens":4096}`,
		`{"generationConfig":{"maxOutputTokens":512}}`,
		`{"generation_config":{"max_output_tokens":768}}`,
		`{"max_tokens":"1024"}`,
		`{"max_tokens":0}`,
		`{"max_tokens":-5}`,
		`{"max_tokens":1.9}`,
		`{}`,
		`{"max_tokens":1024,"max_output_tokens":4096}`,
		`{"generation_config":{"max_output_tokens":768},"max_tokens":9}`,
		`{"input":[{"content":"max_tokens"}]}`,
		`{"prompt":"say max_output_tokens 99"}`,
		`{"\u006dax_tokens":123}`,
		`{"generationConfig":{"maxOutputTokens":"512"}}`,
		`{"max_tokens":null}`,
		`[{"max_tokens":7}]`,
	}
	for _, body := range cases {
		want := legacyRequestMaxOutputTokens([]byte(body))
		got := requestMaxOutputTokens([]byte(body))
		require.Equalf(t, want, got, "body=%s", body)
	}
}