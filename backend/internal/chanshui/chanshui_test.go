package chanshui

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func policyData(t *testing.T, raw string) map[string]any {
	t.Helper()
	var d map[string]any
	require.NoError(t, json.Unmarshal([]byte(raw), &d))
	return d
}

func normalizeConfig(t *testing.T, modelID string, c Config) Config {
	t.Helper()
	require.NoError(t, c.Normalize(modelID))
	return c
}

func TestDefaultFingerprintDecision(t *testing.T) {
	cfg := normalizeConfig(t, "gpt-6-astra", Config{})
	require.Equal(t, []string{"fingerprint"}, cfg.Sections)
	for _, tc := range []struct{ name, body, want string }{
		{"match despite unrelated incomplete IQ", `{"verdict":{"fingerprint":{"top_model":"gpt-6-astra"},"total":{"score":56},"iq":{"complete":false}},"probes":[{"id":"Q1","status":"queued"}]}`, "audit_pass"},
		{"mismatch is immediate", `{"verdict":{"fingerprint":{"top_model":"gpt-other"}}}`, "audit_fail"},
		{"same family is not identical", `{"verdict":{"fingerprint":{"top_model":"gpt-6-astra-mini"}}}`, "audit_fail"},
		{"missing is unknown", `{"verdict":{"fingerprint":null}}`, "unknown"},
		{"unfinished is unknown", `{"verdict":{"fingerprint":{"top_model":"gpt-other"}},"probes":[{"id":"fingerprint","status":"queued"}]}`, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, EvaluateStopCondition("gpt-6-astra", cfg, policyData(t, tc.body)).Status)
		})
	}
}

func TestCustomStopConditions(t *testing.T) {
	threshold := 80.0
	fp := StopRule{Type: "fingerprint_mismatch"}
	score := StopRule{Type: "total_score_below", Threshold: &threshold}
	for _, tc := range []struct {
		name, match, body, want string
		rules                   []StopRule
	}{
		{"below threshold", "any", `{"verdict":{"total":{"score":79,"max":100}}}`, "audit_fail", []StopRule{score}},
		{"at threshold passes", "any", `{"verdict":{"total":{"score":80,"max":100}}}`, "audit_pass", []StopRule{score}},
		{"unfinished total unknown", "any", `{"verdict":{"total":{"score":56},"iq":{"complete":false}}}`, "unknown", []StopRule{score}},
		{"any definite mismatch suffices", "any", `{"verdict":{"fingerprint":{"top_model":"other"}}}`, "audit_fail", []StopRule{fp, score}},
		{"all requires all evidence", "all", `{"verdict":{"fingerprint":{"top_model":"other"}}}`, "unknown", []StopRule{fp, score}},
		{"all false with complete evidence passes", "all", `{"verdict":{"fingerprint":{"top_model":"other"},"total":{"score":100}}}`, "audit_pass", []StopRule{fp, score}},
		{"literal section status", "any", `{"verdict":{"hidden":{"status":"injected"}}}`, "audit_fail", []StopRule{{Type: "section_status", Section: "hidden", Status: "injected"}}},
		{"unknown is not success", "any", `{"verdict":{"hidden":{"status":"unknown"}}}`, "unknown", []StopRule{{Type: "section_status", Section: "hidden", Status: "injected"}}},
		{"explicit unknown condition allowed", "any", `{"verdict":{"cache":{"status":"unknown"}}}`, "audit_fail", []StopRule{{Type: "section_status", Section: "cache", Status: "unknown"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := normalizeConfig(t, "gpt-test", Config{Sections: []string{}, StopCondition: &StopCondition{Match: tc.match, Rules: tc.rules}})
			require.Equal(t, tc.want, EvaluateStopCondition("gpt-test", cfg, policyData(t, tc.body)).Status)
		})
	}
}

func TestConfigurationAndPollOriginValidation(t *testing.T) {
	for _, config := range []Config{
		{BaseURL: "http://audit.example"}, {BaseURL: "https://127.0.0.1"}, {BaseURL: "https://user:secret@audit.example"}, {BaseURL: "https://audit.example?api_key=secret"}, {Timeout: 601}, {Timeout: 9}, {Protocol: "guess"}, {Sections: []string{"unknown"}}, {Sections: []string{"thinking"}},
	} {
		c := config
		require.Error(t, c.Normalize("gpt-test"))
	}
	c := Config{Sections: []string{"thinking"}, StopCondition: &StopCondition{Match: "any", Rules: []StopRule{{Type: "total_score_below", Threshold: func() *float64 { v := 50.0; return &v }()}}}}
	require.NoError(t, c.Normalize("claude-test"))
	require.Equal(t, DefaultBaseURL, c.BaseURL)
	for _, poll := range []string{"https://evil.example/api/v1/audits/audit-1", "//evil.example/api/v1/audits/audit-1", "/api/v1/audits/other", "/api/v1/audits/audit-1?token=secret", "http://audit.example/api/v1/audits/audit-1"} {
		_, err := PollURL("https://audit.example", poll, "audit-1")
		require.Error(t, err)
	}
	_, err := PollURL("https://audit.example", "/api/v1/audits/audit-1", "audit-1")
	require.NoError(t, err)
}

func TestRetryAfterAndRedaction(t *testing.T) {
	require.Equal(t, 48*time.Hour, RetryAfter("172800"))
	require.Equal(t, time.Minute, RetryAfter(""))
	require.Equal(t, time.Minute, RetryAfter("0"))
	secret := "fixture-upstream-key"
	fingerprint := Fingerprint(secret)
	raw := map[string]any{
		"api_key": secret,
		"echo":    "value is " + secret + " inline",
		"nested":  map[string]any{"authorization": "Bearer " + secret},
		"list":    []any{"sk-abcdefgh11223344"},
		"model":   "gpt-6-astra",
		"same_fp": fingerprint,
	}
	safe := Redact(raw, secret, fingerprint)
	encoded, err := json.Marshal(safe)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), secret)
	require.NotContains(t, string(encoded), "sk-abcdefgh11223344")
	require.Contains(t, string(encoded), "gpt-6-astra")
	require.True(t, LooksLikeKey("sk-abcdefgh"))
}
