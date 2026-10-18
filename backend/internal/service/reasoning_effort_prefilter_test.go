package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// legacyExplicitReasoningEffort is the pre-optimization implementation.
func legacyExplicitReasoningEffort(body []byte) string {
	raw := strings.TrimSpace(gjson.GetBytes(body, "reasoning.effort").String())
	if raw == "" {
		raw = strings.TrimSpace(gjson.GetBytes(body, "reasoning_effort").String())
	}
	if raw == "" {
		raw = strings.TrimSpace(gjson.GetBytes(body, "output_config.effort").String())
	}
	return raw
}

func TestExplicitRequestedReasoningEffortMatchesLegacy(t *testing.T) {
	cases := []string{
		`{"reasoning":{"effort":"high"}}`,
		`{"reasoning_effort":"medium"}`,
		`{"output_config":{"effort":" low "}}`,
		`{}`,
		`{"reasoning":{"effort":""},"reasoning_effort":"xhigh"}`,
		`{"reasoning":{},"reasoning_effort":"high","output_config":{"effort":"low"}}`,
		`{"reasoning":{"effort":"  max  "}}`,
		`{"input":[{"content":"reasoning"}]}`,
		`{"prompt":"use reasoning effort high"}`,
		`{"\u0072easoning_effort":"max"}`,
		`{"reasoning":{"effort":null}}`,
		`{"reasoning":{"effort":5}}`,
		`{"output_config":{"effort":"high"},"reasoning_effort":""}`,
	}
	for _, body := range cases {
		want := legacyExplicitReasoningEffort([]byte(body))
		got := explicitRequestedReasoningEffortFromBody([]byte(body))
		require.Equalf(t, want, got, "body=%s", body)
	}
}