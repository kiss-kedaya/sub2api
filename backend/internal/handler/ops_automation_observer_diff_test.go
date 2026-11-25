package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
	"unicode"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ssefixture"
	"github.com/stretchr/testify/require"
)

// refAutomationVerdict is observeLine as it was before the prefilter: decode
// every data line in full.
func refAutomationVerdict(line []byte) (failed, terminal bool) {
	line = bytes.TrimSpace(line)
	if bytes.HasPrefix(line, []byte("data:")) {
		line = bytes.TrimSpace(line[5:])
	} else {
		return
	}
	if bytes.Equal(line, []byte("[DONE]")) {
		return
	}
	var item struct {
		Type     string          `json:"type"`
		Error    json.RawMessage `json:"error"`
		Response struct {
			Status string `json:"status"`
		} `json:"response"`
		Choices []struct {
			Finish *string `json:"finish_reason"`
		} `json:"choices"`
		Candidates []struct {
			Finish string `json:"finishReason"`
		} `json:"candidates"`
	}
	if json.Unmarshal(line, &item) != nil {
		return
	}
	if len(item.Error) > 0 && string(item.Error) != "null" {
		failed = true
	}
	switch item.Type {
	case "error", "response.failed", "response.incomplete":
		failed = true
	case "message_stop":
		terminal = true
	case "response.completed":
		if item.Response.Status == "completed" {
			terminal = true
		}
	}
	for _, v := range item.Choices {
		if v.Finish != nil && (*v.Finish == "stop" || *v.Finish == "length" || *v.Finish == "tool_calls" || *v.Finish == "function_call") {
			terminal = true
		}
	}
	for _, v := range item.Candidates {
		if v.Finish == "STOP" || v.Finish == "MAX_TOKENS" {
			terminal = true
		}
	}
	return
}

func checkAutomationLine(t testing.TB, raw string) {
	t.Helper()
	line := []byte(raw)
	wantFailed, wantTerminal := refAutomationVerdict(line)
	w := &automationObserverWriter{}
	w.observeLine(line)
	if w.failed != wantFailed || w.terminal != wantTerminal {
		t.Fatalf("verdict differs for %q: got failed=%v terminal=%v, want failed=%v terminal=%v", raw, w.failed, w.terminal, wantFailed, wantTerminal)
	}
	// Soundness of the filter itself: a skipped line must have no effect.
	payload := bytes.TrimSpace(bytes.TrimPrefix(bytes.TrimSpace(line), []byte("data:")))
	if !automationLineMayMatter(payload) && (wantFailed || wantTerminal) {
		t.Fatalf("prefilter skipped an effective line %q", raw)
	}
}

func TestAutomationObserverPrefilterKeepsEveryVerdict(t *testing.T) {
	for _, doc := range []string{
		`{"type":"response.completed","response":{"status":"completed"}}`,
		`{"type":"response.completed","response":{"status":"in_progress"}}`,
		`{"TYPE":"error"}`, `{"Type":"response.failed"}`, `{"type":"response.incomplete"}`,
		`{"ErRoR":{"x":1}}`, `{"ERROR":"x"}`, `{"error":null}`, `{"error":{}}`, `{"error":false}`, `{"error" : [] }`,
		`{"type":"message_stop"}`, `{"type":"error"}`, `{"type":"response.failed"}`, `{"choices":[{"finish_reason":"stop"}]}`,
		`{"type":"message_stop"}`, `{"type":"message_stop2"}`, `{"type":"Message_Stop"}`,
		`{"choices":[{"finish_reason":"stop"}]}`, `{"choices":[{"FINISH_REASON":"length"}]}`, `{"Choices":[{"Finish_Reason":"tool_calls"}]}`,
		`{"choices":[{"finish_reason":"function_call"}]}`, `{"choices":[{"finish_reason":null}]}`, `{"choices":[{"finish_reason":"content_filter"}]}`,
		`{"choices":[{"finish_reason":" stop"}]}`, `{"choices":[{"finish_reason":5}]}`, `{"choices":{"finish_reason":"stop"}}`,
		`{"candidates":[{"finishReason":"STOP"}]}`, `{"candidates":[{"FINISHREASON":"MAX_TOKENS"}]}`, `{"candidates":[{"finishReason":"SAFETY"}]}`,
		`{"choices":[{"delta":{"content":"hello"},"finish_reason":null}]}`,
		`{"type":"response.output_text.delta","delta":"error: something"}`,
		`{"type":"response.output_text.delta","delta":"Stop \"stop\" here"}`,
		`{"type":"response.output_text.delta","delta":"\"type\":\"error\""}`,
		`{"type":"x","type":"error"}`, `{"type":"error","type":"x"}`, `{"a":{"type":"error"}}`, `[{"type":"error"}]`, `"error"`, `error`, `{`, ``, `null`, `5`,
		`{"type":"error"} trailing`, `{"type":"error"}{"type":"error"}`, `{"type":5,"error":{"a":1}}`, `{"type":"error","choices":"x"}`,
		`{"ſtatus":"completed","type":"response.completed"}`, "{\"type\":\"response.completed\",\"response\":{\"ſtatus\":\"completed\"}}",
	} {
		for _, prefix := range []string{"data: ", "data:", "  data:  ", "event: x", "id: 4 data: ", ": comment "} {
			checkAutomationLine(t, prefix+doc)
		}
	}
	for _, line := range []string{"data: [DONE]", "data:[DONE]", "data:    ", "", "data", "data: {", "DATA: {\"type\":\"error\"}"} {
		checkAutomationLine(t, line)
	}
}

func TestAutomationObserverPrefilterRandomLines(t *testing.T) {
	iterations := 120000
	if testing.Short() {
		iterations = 10000
	}
	skipped := 0
	for seed := 1; seed <= iterations; seed++ {
		g := ssefixture.NewGen(int64(seed))
		doc := g.Event()
		if g.Chance(0.2) {
			doc = g.Corrupt(doc)
		}
		checkAutomationLine(t, "data: "+doc)
		if !automationLineMayMatter([]byte(doc)) {
			skipped++
		}
	}
	// The filter has to be selective to be worth anything.
	require.Greater(t, skipped, iterations/4, "prefilter skips too little: %d of %d", skipped, iterations)
}

func TestAutomationObserverPrefilterTypicalStreams(t *testing.T) {
	for name, frames := range map[string][][]byte{
		"responses": ssefixture.ResponsesStream(300),
		"chat":      ssefixture.ChatStream(300),
		"anthropic": ssefixture.AnthropicStream(300),
	} {
		stream := ssefixture.Join(frames)
		var want, got automationObserverWriter
		for _, line := range bytes.Split(stream, []byte("\n")) {
			f, tm := refAutomationVerdict(line)
			want.failed, want.terminal = want.failed || f, want.terminal || tm
			got.observeLine(line)
		}
		require.Equal(t, want.failed, got.failed, name)
		require.Equal(t, want.terminal, got.terminal, name)
		require.True(t, got.terminal, name+": every typical stream ends with a terminal")
		// Splitting the stream arbitrarily must not matter either.
		var split automationObserverWriter
		for start := 0; start < len(stream); start += 97 {
			split.observe(stream[start:min(start+97, len(stream))])
		}
		require.Equal(t, got.terminal, split.terminal, name)
		require.Equal(t, got.failed, split.failed, name)
	}
}

// The prefilter never searches keys apart from "error" because no rune but
// ASCII folds onto e, r or o; pin that so a Unicode table change cannot
// silently invalidate the argument.
func TestNoNonASCIIRuneFoldsOntoError(t *testing.T) {
	for _, letter := range "eroERO" {
		for r := unicode.SimpleFold(letter); r != letter; r = unicode.SimpleFold(r) {
			require.Less(t, r, rune(0x80), fmt.Sprintf("%q folds with non-ASCII %U", letter, r))
		}
	}
}

func TestAutomationLineMayMatter(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{`{"a":"error"}`, true}, {`{"ERROR":1}`, true}, {`{"eRrOr":1}`, true}, {`erro`, false}, {`err or`, false}, {``, false}, {`xxErrorxx`, false}, {`{"delta":"an error occurred"}`, false}, {`{"a":"error "}`, false}, {`3rror`, false}, {`{"Error":{}}`, true},
		{`{"finish_reason":"stop"}`, true}, {`{"finish_reason":"stopx"}`, false}, {`{"finish_reason":"stop`, false}, {`{"finish_reason":"Stop"}`, false},
		{`{"a":"length"}`, true}, {`{"a":"tool_calls"}`, true}, {`{"a":"function_call"}`, true}, {`{"a":"function_calls"}`, false},
		{`{"a":"STOP"}`, true}, {`{"a":"MAX_TOKENS"}`, true}, {`{"a":"MAX_TOKEN"}`, false},
		{`{"type":"message_stop"}`, true}, {`{"type":"message_stops"}`, false}, {`{"type":"response.completed"}`, true}, {`{"type":"response.failed"}`, true},
		{`{"type":"response.incomplete"}`, true}, {`{"type":"response.created"}`, false}, {`{"type":"response.output_text.delta","delta":"hi"}`, false},
		{"{\"a\":\"\x5cu0065\"}", true}, {"{\"a\":\"a\x5c\x5cu0065\"}", true}, {`{"a":"x\ny"}`, false}, {"{\"a\":\"你好\"}", false},
		{`{"id":"chatcmpl-1","choices":[{"delta":{"content":"hi"},"finish_reason":null}]}`, false},
	} {
		require.Equal(t, tc.want, automationLineMayMatter([]byte(tc.in)), tc.in)
	}
}

func benchmarkAutomationObserver(b *testing.B, frames [][]byte, observe func(line []byte)) {
	var lines [][]byte
	total := 0
	for _, f := range frames {
		for _, l := range bytes.Split(bytes.TrimSuffix(f, []byte("\n")), []byte("\n")) {
			lines = append(lines, l)
			total += len(l) + 1
		}
	}
	b.SetBytes(int64(total))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, l := range lines {
			observe(l)
		}
	}
}

func BenchmarkAutomationObserver(b *testing.B) {
	for name, frames := range map[string][][]byte{
		"responses_1000": ssefixture.ResponsesStream(1000),
		"chat_1000":      ssefixture.ChatStream(1000),
		"anthropic_1000": ssefixture.AnthropicStream(1000),
	} {
		b.Run(name+"/new", func(b *testing.B) {
			var w automationObserverWriter
			benchmarkAutomationObserver(b, frames, w.observeLine)
		})
		b.Run(name+"/reference", func(b *testing.B) {
			var failed, terminal bool
			benchmarkAutomationObserver(b, frames, func(line []byte) {
				f, t := refAutomationVerdict(line)
				failed, terminal = failed || f, terminal || t
			})
			_, _ = failed, terminal
		})
	}
}
