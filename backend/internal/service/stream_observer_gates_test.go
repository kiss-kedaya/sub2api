package service

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ssefixture"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Differential tests for the cheap gates in front of the per-line stream
// observers. The unfiltered implementations (or frozen copies of the previous
// code) are the oracle: a gate may only ever skip work whose answer is "no".

func gateTestPayload(g *ssefixture.Gen) string {
	var doc string
	switch g.Intn(4) {
	case 0:
		doc = g.Event()
	default:
		doc = g.ChatChunk()
	}
	switch g.Intn(10) {
	case 0:
		doc = g.Corrupt(doc)
	case 1:
		doc = " " + doc + " "
	}
	return doc
}

var gateTestPaths = []string{
	"model", "response.model", "message.model", "modelVersion", "service_tier", "response.service_tier", "usage", "response.usage",
	"choices", "type", "error", "error.code", "response.error", "status", "response.status", "delta", "0.modelVersion", "choices.0.finish_reason", "a.b.c", "",
}

func TestJSONPathMayExistNeverHidesAValue(t *testing.T) {
	skipped := 0
	for seed := 1; seed <= 40000; seed++ {
		g := ssefixture.NewGen(int64(seed))
		raw := gateTestPayload(g)
		path := gateTestPaths[g.Intn(len(gateTestPaths))]
		exists := gjson.Get(raw, path).Exists()
		if exists && !jsonPathMayExist(raw, path) {
			t.Fatalf("gate hid %q in %q", path, raw)
		}
		if exists && !jsonPathMayExistBytes([]byte(raw), path) {
			t.Fatalf("bytes gate hid %q in %q", path, raw)
		}
		require.Equal(t, jsonPathMayExist(raw, path), jsonPathMayExistBytes([]byte(raw), path))
		if !jsonPathMayExist(raw, path) {
			skipped++
		}
	}
	require.Greater(t, skipped, 2000, "the gate must actually skip")
	// Names that gjson compares after resolving escapes or truncating invalid ones.
	for _, tc := range []struct{ raw, path string }{
		{"{\"mod\x5cu0065l\":\"x\"}", "model"},
		{"{\"model\x5cq\":\"x\"}", "model"},
		{"{\"model\x01\x5c\x5c\":\"x\"}", "model"},
		{"[{\"modelVersion\":\"x\"}]", "0.modelVersion"},
		{"{\"response\":{\"model\":\"x\"}}", "response.model"},
	} {
		if gjson.Get(tc.raw, tc.path).Exists() {
			require.True(t, jsonPathMayExist(tc.raw, tc.path), "%q %q", tc.raw, tc.path)
		}
	}
}

func TestOpenAIExplicitUpstreamErrorGateMatchesUnfiltered(t *testing.T) {
	skipped := 0
	for seed := 1; seed <= 60000; seed++ {
		g := ssefixture.NewGen(int64(seed))
		raw := gateTestPayload(g)
		eventType := g.Pick("", "", "", "error", "response.failed", "response.completed", "x")
		root := gjson.Parse(raw)
		got, want := openAIExplicitUpstreamError(root, eventType), openAIExplicitUpstreamErrorUnfiltered(root, eventType)
		if got != want {
			t.Fatalf("event %q payload %q: got %v want %v", eventType, raw, got, want)
		}
		if eventType == "" && !jsonMayContainText(root.Raw, "error") && !jsonMayContainText(root.Raw, "failed") {
			skipped++
		}
	}
	require.Greater(t, skipped, 5000)
}

// ---------------------------------------------------------------------------
// frozen copies of the previous implementations

func refObserveDataLine(t *openAIRawStreamTerminalState, payload string) {
	t.sawDataLine = true
	if payload == "[DONE]" {
		t.sawDone = true
		return
	}
	if usage := gjson.Get(payload, "usage"); usage.Exists() && usage.IsObject() {
		t.sawUsage = true
	}
	if t.sawFinishReason {
		return
	}
	for _, choice := range gjson.Get(payload, "choices").Array() {
		if strings.TrimSpace(choice.Get("finish_reason").String()) != "" {
			t.sawFinishReason = true
			return
		}
	}
}

func refIsOpenAIChatUsageOnlyStreamChunk(payload string) bool {
	if strings.TrimSpace(payload) == "" {
		return false
	}
	if !gjson.Get(payload, "usage").Exists() {
		return false
	}
	choices := gjson.Get(payload, "choices")
	return choices.Exists() && choices.IsArray() && len(choices.Array()) == 0
}

func refExtractCCStreamUsageFromRoot(root gjson.Result, headers http.Header) *OpenAIUsage {
	usageResult := root.Get("usage")
	if !usageResult.Exists() || !usageResult.IsObject() {
		return nil
	}
	u, ok := openAIUsageFromGJSON(usageResult)
	if !ok {
		return nil
	}
	if !usageResult.Get("cache_creation").IsObject() {
		if fiveMinute, oneHour, ok := ParseMSHCacheWriteBuckets(headers); ok {
			u.CacheCreation5mTokens = fiveMinute
			u.CacheCreation1hTokens = oneHour
		}
	}
	return &u
}

func refFirstValidTrimmedGJSONString(payload []byte, paths ...string) string {
	if len(payload) == 0 {
		return ""
	}
	for _, path := range paths {
		value := gjson.GetBytes(payload, path)
		if !value.Exists() || value.Type != gjson.String {
			continue
		}
		if text := strings.TrimSpace(value.String()); text != "" {
			if !gjson.ValidBytes(payload) {
				return ""
			}
			return text
		}
	}
	return ""
}

func refFirstTrimmedGJSONResultString(root gjson.Result, paths ...string) string {
	for _, path := range paths {
		value := root.Get(path)
		if !value.Exists() || value.Type != gjson.String {
			continue
		}
		if text := strings.TrimSpace(value.String()); text != "" {
			return text
		}
	}
	return ""
}

func refStripEmptyChatToolCallIdentityFromSSELine(line string) string {
	payload, ok := extractOpenAISSEDataLine(line)
	if !ok {
		return line
	}
	trimmed := strings.TrimSpace(payload)
	if trimmed == "" || trimmed == "[DONE]" {
		return line
	}
	rewritten, changed := stripEmptyChatToolCallIdentity([]byte(payload))
	if !changed {
		return line
	}
	prefixLen := len(line) - len(payload)
	if prefixLen < 0 {
		return line
	}
	return line[:prefixLen] + string(rewritten)
}

// refDetectorLine is ObserveSSELine without the decided() short cut.
func refDetectorLine(d *openAIChatSilentRefusalDetector, line string) {
	if d == nil || !d.enabled {
		return
	}
	if eventType, ok := extractOpenAISSEEventLine(line); ok {
		d.observeEventType(eventType)
		return
	}
	if payload, ok := extractOpenAISSEDataLine(line); ok {
		d.observePayload([]byte(payload))
	}
}

func TestRawChatObserverGatesMatchPreviousImplementations(t *testing.T) {
	headers := http.Header{}
	for seed := 1; seed <= 60000; seed++ {
		g := ssefixture.NewGen(int64(seed))
		raw := gateTestPayload(g)

		var gotState, wantState openAIRawStreamTerminalState
		for i := 0; i < 1+g.Intn(3); i++ {
			// Replays: states that are already set must stay set.
			(&gotState).ObserveDataLine(raw)
			refObserveDataLine(&wantState, raw)
		}
		require.Equal(t, wantState, gotState, "terminal state for %q", raw)

		require.Equal(t, refIsOpenAIChatUsageOnlyStreamChunk(raw), isOpenAIChatUsageOnlyStreamChunk(raw), "usage-only %q", raw)

		root := gjson.Parse(raw)
		require.Equal(t, refExtractCCStreamUsageFromRoot(root, headers), extractCCStreamUsageFromRoot(root, headers), "usage %q", raw)

		for _, paths := range [][]string{{"response.model", "model"}, {"response.service_tier", "service_tier"}, {"message.model", "model"}, {"modelVersion", "response.modelVersion", "response.response.modelVersion"}} {
			require.Equal(t, refFirstValidTrimmedGJSONString([]byte(raw), paths...), firstValidTrimmedGJSONString([]byte(raw), paths...), "%v %q", paths, raw)
			require.Equal(t, refFirstTrimmedGJSONResultString(root, paths...), firstTrimmedGJSONResultString(root, paths...), "%v %q", paths, raw)
		}

		line := g.Pick("data: ", "data:", "data:  ", "event: ") + raw
		require.Equal(t, refStripEmptyChatToolCallIdentityFromSSELine(line), stripEmptyChatToolCallIdentityFromSSELine(line), "%q", line)
		require.Equal(t, refStripEmptyChatToolCallIdentityFromSSELine(raw), stripEmptyChatToolCallIdentityFromSSELine(raw), "%q", raw)
	}
}

func TestSilentRefusalDetectorShortCutKeepsBothVerdicts(t *testing.T) {
	decided := 0
	for seed := 1; seed <= 20000; seed++ {
		g := ssefixture.NewGen(int64(seed))
		got, want := newOpenAIChatSilentRefusalDetector(openAISilentRefusalMinRequestBodyBytes), newOpenAIChatSilentRefusalDetector(openAISilentRefusalMinRequestBodyBytes)
		for i := 0; i < 1+g.Intn(12); i++ {
			var line string
			switch g.Intn(6) {
			case 0:
				line = "event: " + g.Pick("error", "response.failed", "response.output_text.delta", "reasoning", "x")
			case 1:
				line = ""
			case 2:
				line = "data: [DONE]"
			default:
				line = g.Pick("data: ", "data:") + gateTestPayload(g)
			}
			got.ObserveSSELine(line)
			refDetectorLine(want, line)
			if got.ShouldReleaseClientOutput() != want.ShouldReleaseClientOutput() || got.IsSilentRefusal() != want.IsSilentRefusal() {
				t.Fatalf("seed %d after %q: release %v/%v refusal %v/%v", seed, line, got.ShouldReleaseClientOutput(), want.ShouldReleaseClientOutput(), got.IsSilentRefusal(), want.IsSilentRefusal())
			}
			if payload, ok := extractOpenAISSEDataLine(line); ok && !got.decided() {
				// ObservePayload is the same entry point the chat handler uses.
				got.ObservePayload([]byte(payload))
				want.observePayload([]byte(payload))
				require.Equal(t, want.ShouldReleaseClientOutput(), got.ShouldReleaseClientOutput())
				require.Equal(t, want.IsSilentRefusal(), got.IsSilentRefusal())
			}
		}
		if got.decided() {
			decided++
		}
	}
	require.Greater(t, decided, 2000)
}

func TestSilentRefusalDetectorTypicalChatStream(t *testing.T) {
	d := newOpenAIChatSilentRefusalDetector(openAISilentRefusalMinRequestBodyBytes)
	for _, line := range ssefixture.Lines(ssefixture.ChatStream(50)) {
		d.ObserveSSELine(line)
	}
	require.True(t, d.ShouldReleaseClientOutput())
	require.False(t, d.IsSilentRefusal())
	// An empty completion with stop and no usage is still a refusal.
	empty := newOpenAIChatSilentRefusalDetector(openAISilentRefusalMinRequestBodyBytes)
	empty.ObserveSSELine(`data: {"choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`)
	empty.ObserveSSELine(`data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
	require.True(t, empty.IsSilentRefusal())
	require.False(t, empty.ShouldReleaseClientOutput())
}
