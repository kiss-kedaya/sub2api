package service

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ssefixture"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// refRewriteRequestedModelResponse and refRewriteSSEFrame are the
// implementations before the cheap-skip rewrite, kept as the oracle.
func refRewriteRequestedModelResponse(payload []byte, protocol, model string) []byte {
	if model == "" || len(payload) == 0 || !gjson.ValidBytes(payload) {
		return payload
	}
	root := gjson.ParseBytes(payload)
	if value := root.Get("error"); value.Exists() && value.Type != gjson.Null {
		return payload
	}
	if value := root.Get("response.error"); value.Exists() && value.Type != gjson.Null {
		return payload
	}
	event := root.Get("type").Str
	if event == "error" || event == "response.failed" {
		return payload
	}
	var paths []string
	switch protocol {
	case "openai":
		paths = []string{"model", "response.model"}
	case "anthropic":
		paths = []string{"model"}
		if event == "message_start" {
			paths = append(paths, "message.model")
		}
	case "realtime":
		paths = []string{"model", "response.model"}
		if event == "session.created" || event == "session.updated" {
			paths = append(paths, "session.model")
		}
	case "gemini":
		paths = []string{"modelVersion"}
		if root.IsArray() {
			paths = nil
			for index, item := range root.Array() {
				if item.Get("error").Type != gjson.Null {
					continue
				}
				paths = append(paths, strconv.Itoa(index)+".modelVersion")
			}
		}
	default:
		return payload
	}
	updated := payload
	for _, path := range paths {
		value := root.Get(path)
		if value.Type != gjson.String || value.Str == model {
			continue
		}
		if next, err := sjson.SetBytes(updated, path, model); err == nil {
			updated = next
		}
	}
	return updated
}

func refRewriteSSEFrame(frame []byte, protocol, model string) []byte {
	lines := bytes.SplitAfter(frame, []byte("\n"))
	type dataLine struct{ index, start, end int }
	var dataLines []dataLine
	var payload []byte
	for index, line := range lines {
		content := bytes.TrimRight(line, "\r\n")
		if bytes.HasPrefix(content, []byte("event:")) {
			event := strings.TrimSpace(string(content[len("event:"):]))
			if event == "error" || event == "response.failed" {
				return frame
			}
		}
		if !bytes.HasPrefix(content, []byte("data:")) {
			continue
		}
		start := len("data:")
		if start < len(content) && content[start] == ' ' {
			start++
		}
		if len(dataLines) > 0 {
			payload = append(payload, '\n')
		}
		payload = append(payload, content[start:]...)
		dataLines = append(dataLines, dataLine{index: index, start: start, end: len(content)})
	}
	updated := refRewriteRequestedModelResponse(payload, protocol, model)
	if bytes.Equal(updated, payload) {
		return frame
	}
	segments := bytes.Split(updated, []byte("\n"))
	if len(segments) != len(dataLines) {
		return frame
	}
	for index, position := range dataLines {
		line := lines[position.index]
		next := make([]byte, 0, position.start+len(segments[index])+len(line)-position.end)
		next = append(next, line[:position.start]...)
		next = append(next, segments[index]...)
		lines[position.index] = append(next, line[position.end:]...)
	}
	return bytes.Join(lines, nil)
}

var requestedModelTestProtocols = []string{"openai", "anthropic", "realtime", "gemini", "", "other"}

func requestedModelTestPayload(g *ssefixture.Gen) string {
	doc := g.Event()
	switch g.Intn(8) {
	case 0:
		doc = g.Corrupt(doc)
	case 1:
		// Event shapes whose model sits where only some protocols read it.
		doc = g.Pick(
			`{"type":"message_start","message":{"model":"model-b","usage":{}}}`,
			`{"type":"session.updated","session":{"model":"model-b"}}`,
			`{"type":"response.created","response":{"model":"model-b","error":null}}`,
			`{"type":"response.created","response":{"model":"model-b","error":{"m":1}}}`,
			`{"model":"model-b","error":{"message":"x"}}`,
			`[{"modelVersion":"model-b"},{"error":{"a":1},"modelVersion":"model-c"},{"modelVersion":"model-a"}]`,
			`{"modelVersion":"model-b","candidates":[]}`,
			`{"model":"model-b","model":"model-a"}`,
			`{"model":5,"response":{"model":null}}`,
			`{"response":"x","response":{"model":"model-b"}}`,
			"{\"mod\x5cu0065l\":\"model-b\"}",
			`  {"model":"model-b"}`,
		)
	}
	return doc
}

func TestRewriteRequestedModelResponseMatchesReference(t *testing.T) {
	iterations := 60000
	if testing.Short() {
		iterations = 5000
	}
	changed := 0
	for seed := 1; seed <= iterations; seed++ {
		g := ssefixture.NewGen(int64(seed))
		payload := requestedModelTestPayload(g)
		protocol := requestedModelTestProtocols[g.Intn(len(requestedModelTestProtocols))]
		model := g.Pick("client-model", "model-a", "model-b", "", "x")
		input := []byte(payload)
		got := RewriteRequestedModelResponse(append([]byte(nil), input...), protocol, model)
		want := refRewriteRequestedModelResponse(append([]byte(nil), input...), protocol, model)
		if !bytes.Equal(got, want) {
			t.Fatalf("seed %d protocol %q model %q\npayload %q\n got  %q\n want %q", seed, protocol, model, payload, got, want)
		}
		if !bytes.Equal(got, input) {
			changed++
		}
	}
	require.Greater(t, changed, iterations/20, "the generator must exercise real rewrites")
}

func TestRewriteSSEFrameMatchesReference(t *testing.T) {
	iterations := 60000
	if testing.Short() {
		iterations = 5000
	}
	changed, skipped := 0, 0
	for seed := 1; seed <= iterations; seed++ {
		g := ssefixture.NewGen(int64(seed))
		eol := g.Pick("\n", "\n", "\r\n")
		var frame strings.Builder
		if g.Chance(0.3) {
			frame.WriteString("event: " + g.Pick("response.created", "error", "response.failed", " error ", "message_start", "x") + eol)
		}
		if g.Chance(0.1) {
			frame.WriteString(": comment" + eol + "id: 7" + eol)
		}
		payload := requestedModelTestPayload(g)
		sep := g.Pick("data: ", "data:", "data:  ")
		if g.Chance(0.2) && strings.Contains(payload, ",") {
			for _, part := range strings.SplitAfter(payload, ",") {
				frame.WriteString(sep + part + eol)
			}
		} else {
			frame.WriteString(sep + payload + eol)
		}
		if g.Chance(0.1) {
			frame.WriteString(sep + g.Pick("[DONE]", "{}", "x") + eol)
		}
		frame.WriteString(eol)
		protocol := requestedModelTestProtocols[g.Intn(len(requestedModelTestProtocols))]
		model := g.Pick("client-model", "model-a", "model-b")
		w := &requestedModelResponseWriter{protocol: protocol, model: model}
		input := []byte(frame.String())
		got := w.rewriteSSEFrame(append([]byte(nil), input...))
		want := refRewriteSSEFrame(append([]byte(nil), input...), protocol, model)
		if !bytes.Equal(got, want) {
			t.Fatalf("seed %d protocol %q model %q\nframe %q\n got  %q\n want %q", seed, protocol, model, input, got, want)
		}
		if requestedModelCannotAppear(input) {
			skipped++
		}
		if !bytes.Equal(got, input) {
			changed++
		}
	}
	require.Greater(t, changed, iterations/20)
	require.Greater(t, skipped, iterations/10)
}

// The typical streams through the whole writer: bytes out must equal the
// reference rewrite frame by frame, however the stream is chopped up.
func TestRequestedModelWriterTypicalStreamsMatchReference(t *testing.T) {
	for name, tc := range map[string]struct {
		frames   [][]byte
		protocol string
	}{
		"responses": {ssefixture.ResponsesStream(120), "openai"},
		"chat":      {ssefixture.ChatStream(120), "openai"},
		"anthropic": {ssefixture.AnthropicStream(120), "anthropic"},
	} {
		var want bytes.Buffer
		for _, frame := range tc.frames {
			want.Write(refRewriteSSEFrame(frame, tc.protocol, "client-model"))
		}
		require.Contains(t, want.String(), "client-model", name)
		stream := ssefixture.Join(tc.frames)
		for _, size := range []int{len(stream), 1, 7, 64, 1000} {
			enabled := true
			c, recorder, finish := requestedModelWriterTestContext(t, tc.protocol, &enabled)
			c.Header("Content-Type", "text/event-stream")
			for start := 0; start < len(stream); start += size {
				_, err := c.Writer.WriteString(string(stream[start:min(start+size, len(stream))]))
				require.NoError(t, err)
			}
			finish()
			require.Equal(t, want.String(), recorder.Body.String(), fmt.Sprintf("%s chunk %d", name, size))
		}
	}
}

func BenchmarkRewriteSSEFrame(b *testing.B) {
	for name, tc := range map[string]struct {
		frames   [][]byte
		protocol string
		model    string
	}{
		"responses_1000":       {ssefixture.ResponsesStream(1000), "openai", "client-model"},
		"chat_1000":            {ssefixture.ChatStream(1000), "openai", "client-model"},
		"chat_1000_same_model": {ssefixture.ChatStream(1000), "openai", "model-a"},
		"anthropic_1000":       {ssefixture.AnthropicStream(1000), "anthropic", "client-model"},
	} {
		total := 0
		for _, f := range tc.frames {
			total += len(f)
		}
		w := &requestedModelResponseWriter{protocol: tc.protocol, model: tc.model}
		b.Run(name+"/new", func(b *testing.B) {
			b.SetBytes(int64(total))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				for _, f := range tc.frames {
					_ = w.rewriteSSEFrame(f)
				}
			}
		})
		b.Run(name+"/reference", func(b *testing.B) {
			b.SetBytes(int64(total))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				for _, f := range tc.frames {
					_ = refRewriteSSEFrame(f, tc.protocol, tc.model)
				}
			}
		})
	}
}
