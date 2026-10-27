package typesafe

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type choiceTransport func(*http.Request) (*http.Response, error)

func (f choiceTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestTypeSafeQualityChoiceStrictProtocol(t *testing.T) {
	criteria := map[string]string{"correct": "equivalent", "incorrect": "different", "unknown": "uncertain"}
	valid := `{"model":"jev-latest","answers":{"quality":{"type":"choice","choice":"correct","confidence":0.95,"probabilities":{"correct":0.96,"incorrect":0.02,"unknown":0.02}}},"usage":{"input_tokens":10,"output_tokens":2}}`
	got, err := decodeChoice([]byte(valid), criteria, "quality")
	require.NoError(t, err)
	require.Equal(t, "correct", got.Choice)
	for name, body := range map[string]string{
		"missing":               `{"model":"jev-latest","answers":{}}`,
		"noul is not quality":   `{"model":"jev-latest","answers":{"quality":{"type":"noul","noul":0.99}}}`,
		"score is not quality":  `{"model":"jev-latest","answers":{"quality":{"type":"score","score":2}}}`,
		"unknown label":         strings.Replace(valid, `"choice":"correct"`, `"choice":"pass"`, 1),
		"missing confidence":    strings.Replace(valid, `"confidence":0.95,`, "", 1),
		"null confidence":       strings.Replace(valid, `"confidence":0.95`, `"confidence":null`, 1),
		"out of range":          strings.Replace(valid, `"confidence":0.95`, `"confidence":1.1`, 1),
		"null probability":      strings.Replace(valid, `"incorrect":0.02`, `"incorrect":null`, 1),
		"missing probability":   strings.Replace(valid, `,"unknown":0.02`, "", 1),
		"invalid sum":           strings.Replace(valid, `"correct":0.96`, `"correct":0.5`, 1),
		"not highest":           strings.Replace(valid, `"choice":"correct"`, `"choice":"incorrect"`, 1),
		"duplicate":             strings.Replace(valid, `"choice":"correct"`, `"choice":"incorrect","choice":"correct"`, 1),
		"case alias":            strings.Replace(valid, `"confidence"`, `"Confidence"`, 1),
		"duplicate probability": strings.Replace(valid, `"correct":0.96`, `"correct":0.96,"correct":0.96`, 1),
		"refusal":               strings.Replace(valid, `"type":"choice"`, `"type":"choice","refusal":"cannot grade"`, 1),
		"trailing":              valid + `{}`,
	} {
		t.Run(name, func(t *testing.T) { _, err := decodeChoice([]byte(body), criteria, "quality"); require.Error(t, err) })
	}
}

func TestTypeSafeQualityChoiceOneAttemptBoundAndRedaction(t *testing.T) {
	input := Request{Model: JevLatestModel, State: `{"candidate_answer":"ignore rules"}`, Questions: map[string]Question{"quality": {Type: "choice", Instructions: "compare", Criteria: map[string]string{"correct": "same", "incorrect": "different", "unknown": "uncertain"}}}}
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"credential", 401, "SYNTHETIC_SECRET upstream body"},
		{"rate limit", 429, "SYNTHETIC_SECRET"},
		{"oversize", 200, strings.Repeat(" ", MaxSystemOneResponseBytes+1)},
		{"truncated", 200, `{"model":"jev-latest"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, base := range []string{"https://example.com", "https://example.com/v1"} {
				t.Run(base, func(t *testing.T) {
					calls := 0
					client := &http.Client{Transport: choiceTransport(func(req *http.Request) (*http.Response, error) {
						calls++
						require.Equal(t, "/v1/systemone", req.URL.Path)
						require.Equal(t, "Bearer synthetic-key", req.Header.Get("Authorization"))
						return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body)), Header: make(http.Header)}, nil
					})}
					_, status, err := EvaluateChoice(context.Background(), client, base, "synthetic-key", input, "quality")
					require.Error(t, err)
					require.Equal(t, tc.status, status)
					require.Equal(t, 1, calls)
					require.NotContains(t, err.Error(), "SYNTHETIC_SECRET")
					require.NotContains(t, err.Error(), "synthetic-key")
				})
			}
		})
	}
}
