package typesafe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ChoiceResult follows the native systemone choice schema, not a chat response.
// Confidence and probabilities describe this question, never audit risk scores.
type ChoiceResult struct {
	Choice      string
	Confidence  float64
	Probability float64
}

// EvaluateChoice performs exactly one attempt using the existing request and
// bounded response helpers. It exposes neither raw body nor usage.
func EvaluateChoice(ctx context.Context, client *http.Client, baseURL, key string, input Request, id string) (*ChoiceResult, int, error) {
	body, err := json.Marshal(input)
	if err != nil {
		return nil, 0, errors.New("typesafe invalid request")
	}
	if _, err = ValidateSystemOneRequest(body); err != nil {
		return nil, 0, errors.New("typesafe invalid request")
	}
	req, err := NewSystemOneRequest(ctx, baseURL, key, body)
	if err != nil {
		return nil, 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, errors.New("typesafe transport unavailable")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp.StatusCode, errors.New("typesafe request rejected")
	}
	decoded, err := DecodeSystemOneResponse(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, errors.New("typesafe invalid response")
	}
	result, err := decodeChoice(decoded.Body, input.Questions[id].Criteria, id)
	return result, resp.StatusCode, err
}

func decodeChoice(body []byte, criteria map[string]string, id string) (*ChoiceResult, error) {
	invalid := errors.New("typesafe invalid choice response")
	if len(body) > MaxSystemOneResponseBytes || !utf8.Valid(body) || !validChoiceJSON(body) {
		return nil, invalid
	}
	var envelope struct {
		Model   string                     `json:"model"`
		Answers map[string]json.RawMessage `json:"answers"`
	}
	if json.Unmarshal(body, &envelope) != nil || envelope.Model == "" || len(envelope.Model) > 100 || len(envelope.Answers) != 1 {
		return nil, invalid
	}
	if checkSystemOneObjectKeys(body, "response", []string{"model", "answers", "usage", "id", "provider"}) != nil {
		return nil, invalid
	}
	var top map[string]json.RawMessage
	_ = json.Unmarshal(body, &top)
	for field, raw := range top {
		switch field {
		case "model", "answers", "usage":
		case "id", "provider":
			// The existing audit endpoint adds these transport identifiers. They
			// carry no grading semantics and are never returned or logged.
			limit := 128
			if field == "provider" {
				limit = 64
			}
			var value string
			if json.Unmarshal(raw, &value) != nil || strings.TrimSpace(value) == "" || len(value) > limit || strings.IndexFunc(value, unicode.IsControl) >= 0 {
				return nil, invalid
			}
		default:
			return nil, invalid
		}
	}
	if checkSystemOneObjectKeys(top["answers"], "answers", nil) != nil {
		return nil, invalid
	}
	raw := envelope.Answers[id]
	if checkSystemOneObjectKeys(raw, "answer", []string{"type", "choice", "confidence", "probabilities"}) != nil {
		return nil, invalid
	}
	var answer struct {
		Type          string             `json:"type"`
		Choice        string             `json:"choice"`
		Confidence    *float64           `json:"confidence"`
		Probabilities map[string]float64 `json:"probabilities"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&answer) != nil || answer.Type != "choice" || answer.Confidence == nil || !unitProbability(*answer.Confidence) {
		return nil, invalid
	}
	if _, ok := criteria[answer.Choice]; !ok || len(criteria) == 0 || len(answer.Probabilities) != len(criteria) {
		return nil, invalid
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	if checkSystemOneObjectKeys(fields["probabilities"], "probabilities", nil) != nil {
		return nil, invalid
	}
	var rawProbabilities map[string]json.RawMessage
	_ = json.Unmarshal(fields["probabilities"], &rawProbabilities)
	total := 0.0
	selected := answer.Probabilities[answer.Choice]
	for label := range criteria {
		p, ok := answer.Probabilities[label]
		// json null must not silently decode as zero.
		if !ok || bytes.Equal(bytes.TrimSpace(rawProbabilities[label]), []byte("null")) || !unitProbability(p) || p > selected+1e-9 {
			return nil, invalid
		}
		total += p
	}
	if math.Abs(total-1) > 0.001 {
		return nil, invalid
	}
	return &ChoiceResult{Choice: answer.Choice, Confidence: *answer.Confidence, Probability: selected}, nil
}

// Bound nesting before unmarshalling and reject duplicate keys at every level,
// including otherwise ignored usage. A metadata exception must not hide a second
// verdict, refusal, ambiguous key spelling or an unbounded nested value.
func validChoiceJSON(body []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(body))
	var value func(int) bool
	value = func(depth int) bool {
		token, err := decoder.Token()
		if err != nil {
			return false
		}
		delim, container := token.(json.Delim)
		if !container {
			return true
		}
		if depth >= 16 || (delim != '{' && delim != '[') {
			return false
		}
		seen := map[string]bool{}
		for decoder.More() {
			if delim == '{' {
				token, err := decoder.Token()
				key, ok := token.(string)
				if err != nil || !ok || seen[key] {
					return false
				}
				seen[key] = true
			}
			if !value(depth + 1) {
				return false
			}
		}
		end, err := decoder.Token()
		return err == nil && ((delim == '{' && end == json.Delim('}')) || (delim == '[' && end == json.Delim(']')))
	}
	if !value(0) {
		return false
	}
	_, err := decoder.Token()
	return err == io.EOF
}

func unitProbability(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 1
}
