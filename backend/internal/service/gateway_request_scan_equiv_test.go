package service

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
	"unsafe"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// legacyParse mirrors the pre-optimization gjson hot path exactly, so the new
// single-pass scanner can be compared field by field against it.
func legacyParse(parsed *ParsedRequest, protocol string) error {
	bodyBytes := parsed.Body.Bytes()
	if !gjson.ValidBytes(bodyBytes) {
		return DescribeInvalidJSON(bodyBytes)
	}
	jsonStr := *(*string)(unsafe.Pointer(&bodyBytes))
	if hasDuplicateTopLevelKey(jsonStr, "model") {
		return ErrDuplicateModelField
	}
	clearGatewayRequestDerivedState(parsed)
	parsed.protocol = protocol

	modelResult := gjson.Get(jsonStr, "model")
	if modelResult.Exists() {
		if modelResult.Type != gjson.String {
			return fmt.Errorf("invalid model field type")
		}
		parsed.Model = modelResult.String()
		if protocol == domain.PlatformAnthropic {
			normalizedModel := normalizeClaudeCodeLongContextModel(parsed.Model)
			if normalizedModel != parsed.Model {
				normalizedBody, err := sjson.SetBytes(bodyBytes, "model", normalizedModel)
				if err != nil {
					return fmt.Errorf("normalize model field: %w", err)
				}
				parsed.Body.Replace(normalizedBody)
				bodyBytes = normalizedBody
				jsonStr = *(*string)(unsafe.Pointer(&bodyBytes))
				parsed.Model = normalizedModel
			}
		}
	}

	streamResult := gjson.Get(jsonStr, "stream")
	if streamResult.Exists() {
		if streamResult.Type != gjson.True && streamResult.Type != gjson.False {
			return fmt.Errorf("invalid stream field type")
		}
		parsed.Stream = streamResult.Bool()
	}

	parsed.MetadataUserID = gjson.Get(jsonStr, "metadata.user_id").String()
	thinkingType := gjson.Get(jsonStr, "thinking.type").String()
	parsed.ThinkingEnabled = thinkingType == "enabled" || thinkingType == "adaptive"
	parsed.OutputEffort = strings.TrimSpace(gjson.Get(jsonStr, "output_config.effort").String())
	if protocol == domain.PlatformAnthropic {
		parsed.Speed = strings.ToLower(strings.TrimSpace(gjson.Get(jsonStr, "speed").String()))
	}

	maxTokensResult := gjson.Get(jsonStr, "max_tokens")
	if maxTokensResult.Exists() && maxTokensResult.Type == gjson.Number {
		f := maxTokensResult.Float()
		if !math.IsNaN(f) && !math.IsInf(f, 0) && f == math.Trunc(f) &&
			f <= float64(math.MaxInt) && f >= float64(math.MinInt) {
			parsed.MaxTokens = int(f)
		}
	}

	setGatewayRequestRanges(parsed, protocol, jsonStr)
	return nil
}

func TestScanGatewayRequestMatchesLegacy(t *testing.T) {
	cases := map[string]string{
		"anthropic-basic":        `{"model":"claude-sonnet-4-5","stream":true,"max_tokens":4096,"metadata":{"user_id":"u1"},"thinking":{"type":"enabled"},"system":"sys","messages":[{"role":"user","content":"hi"}],"output_config":{"effort":" high "},"speed":"Fast"}`,
		"anthropic-no-fields":    `{"messages":[]}`,
		"anthropic-stream-false": `{"model":"m","stream":false}`,
		"anthropic-1m-suffix":    `{"model":"claude-sonnet-4-5[1m]","stream":true}`,
		"anthropic-double-1m":    `{"model":"claude-sonnet-4-5[1m][1m]","stream":true}`,
		"responses-input":        `{"model":"gpt-5","input":[{"role":"user","content":"x"}],"stream":true}`,
		"responses-string-input": `{"model":"gpt-5","input":"hello"}`,
		"gemini":                 `{"model":"gemini-2.5-pro","contents":[{"role":"user","parts":[{"text":"x"}]}],"systemInstruction":{"parts":[{"text":"sys"}]}}`,
		"system-null":            `{"model":"m","system":null,"messages":[]}`,
		"max-tokens-string":      `{"model":"m","max_tokens":"4096"}`,
		"max-tokens-float":       `{"model":"m","max_tokens":4096.7}`,
		"thinking-adaptive":      `{"model":"m","thinking":{"type":"adaptive"}}`,
		"extra-whitespace":       "{\n  \"model\" : \"m\" ,\n \"stream\" : true ,\n \"messages\" : [ ]\n}",
		"nested-brackets":        `{"model":"m","messages":[{"content":"a}]b"}]}`,
		"unicode-escaped-key":    `{"\u006dodel":"m","stream":true}`,
		"duplicate-other-keys":   `{"model":"m","system":"a","system":"b","messages":[]}`,
		"empty-object":           `{}`,
		"uppercase-model-key":    `{"Model":"m","model":"n"}`,
		"speed-number":           `{"model":"m","speed":5}`,
	}
	protocols := []string{domain.PlatformAnthropic, "", domain.PlatformGemini, "responses"}

	for name, body := range cases {
		for _, protocol := range protocols {
			t.Run(name+"|"+protocol, func(t *testing.T) {
				want := &ParsedRequest{Body: NewRequestBodyRef([]byte(body))}
				wantErr := legacyParse(want, protocol)

				got, gotErr := ParseGatewayRequest(NewRequestBodyRef([]byte(body)), protocol)

				if (wantErr != nil) != (gotErr != nil) {
					t.Fatalf("error mismatch: legacy=%v new=%v", wantErr, gotErr)
				}
				if wantErr != nil {
					return
				}
				assertParsedEqual(t, want, got)
			})
		}
	}
}

func assertParsedEqual(t *testing.T, want, got *ParsedRequest) {
	t.Helper()
	if want.Model != got.Model {
		t.Fatalf("Model: legacy=%q new=%q", want.Model, got.Model)
	}
	if want.Stream != got.Stream {
		t.Fatalf("Stream: legacy=%v new=%v", want.Stream, got.Stream)
	}
	if want.MetadataUserID != got.MetadataUserID {
		t.Fatalf("MetadataUserID: legacy=%q new=%q", want.MetadataUserID, got.MetadataUserID)
	}
	if want.HasSystem != got.HasSystem {
		t.Fatalf("HasSystem: legacy=%v new=%v", want.HasSystem, got.HasSystem)
	}
	if want.ThinkingEnabled != got.ThinkingEnabled {
		t.Fatalf("ThinkingEnabled: legacy=%v new=%v", want.ThinkingEnabled, got.ThinkingEnabled)
	}
	if want.OutputEffort != got.OutputEffort {
		t.Fatalf("OutputEffort: legacy=%q new=%q", want.OutputEffort, got.OutputEffort)
	}
	if want.Speed != got.Speed {
		t.Fatalf("Speed: legacy=%q new=%q", want.Speed, got.Speed)
	}
	if want.MaxTokens != got.MaxTokens {
		t.Fatalf("MaxTokens: legacy=%d new=%d", want.MaxTokens, got.MaxTokens)
	}
	assertRawEqual(t, "SystemRaw", want.SystemRaw(), got.SystemRaw())
	assertRawEqual(t, "MessagesRaw", want.MessagesRaw(), got.MessagesRaw())
	assertRawEqual(t, "InputRaw", want.InputRaw(), got.InputRaw())
	if !jsonEqual(want.Body.Bytes(), got.Body.Bytes()) {
		t.Fatalf("body mismatch:\n legacy=%s\n new   =%s", want.Body.Bytes(), got.Body.Bytes())
	}
}

func assertRawEqual(t *testing.T, label string, want, got []byte) {
	t.Helper()
	if !jsonEqual(want, got) {
		t.Fatalf("%s mismatch:\n legacy=%s\n new   =%s", label, want, got)
	}
}

func jsonEqual(a, b []byte) bool {
	if len(a) == 0 || len(b) == 0 {
		return len(a) == len(b)
	}
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		return string(a) == string(b)
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		return string(a) == string(b)
	}
	ab, _ := json.Marshal(av)
	bb, _ := json.Marshal(bv)
	return string(ab) == string(bb)
}
