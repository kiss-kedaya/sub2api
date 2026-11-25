package requestcapture

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ssefixture"
)

// The production observer must reach the same state as the frozen byte-at-a-time
// reference (response_observer_ref_test.go) after every write, for any chunking
// of any input: valid streams, plain JSON bodies and corrupted variants alike.

type obsView struct {
	sawEvent, failed, expectsTerminal, textDeltaSeen, sse, pending bool
	terminal                                                       string
	prefix                                                         string
}

func viewNew(o *responseObserver) obsView {
	return obsView{o.sawEvent, o.failed, o.expectsTerminal, o.textDeltaSeen, o.framing.sse, o.framing.pending, o.terminal, string(o.framing.prefix)}
}

func viewRef(o *refObserver) obsView {
	return obsView{o.sawEvent, o.failed, o.expectsTerminal, o.textDeltaSeen, o.framing.sse, o.framing.pending, o.terminal, string(o.framing.prefix)}
}

func compareObservers(t testing.TB, label string, input []byte, cuts []int, contentType string) {
	t.Helper()
	n := responseObserver{framing: newBodyFraming(contentType)}
	r := refObserver{framing: newBodyFraming(contentType)}
	start := 0
	for step, cut := range append(append([]int(nil), cuts...), len(input)) {
		if cut > len(input) {
			cut = len(input)
		}
		if cut < start {
			continue
		}
		chunk := input[start:cut]
		start = cut
		n.write(append([]byte(nil), chunk...))
		r.write(append([]byte(nil), chunk...))
		if got, want := viewNew(&n), viewRef(&r); got != want {
			t.Fatalf("%s: state diverged after chunk %d (%d bytes consumed)\n got  %+v\n want %+v\ninput %q\ncuts %v", label, step, start, got, want, input, cuts)
		}
	}
	n.end()
	r.end()
	if got, want := viewNew(&n), viewRef(&r); got != want || n.successful() != r.successful() {
		t.Fatalf("%s: state diverged at end\n got  %+v %v\n want %+v %v\ninput %q\ncuts %v", label, got, n.successful(), want, r.successful(), input, cuts)
	}
}

func TestResponseObserverMatchesReference(t *testing.T) {
	iterations := 6000
	if testing.Short() {
		iterations = 500
	}
	for seed := 1; seed <= iterations; seed++ {
		g := ssefixture.NewGen(int64(seed))
		var input, contentType string
		switch g.Intn(6) {
		case 0:
			input, contentType = g.JSONBody(), "application/json"
		case 1:
			input, contentType = g.JSONBody(), ""
		case 2:
			input, contentType = g.SSEStream(), ""
		default:
			input, contentType = g.SSEStream(), "text/event-stream"
		}
		compareObservers(t, fmt.Sprintf("seed %d", seed), []byte(input), g.Cuts(len(input)), contentType)
	}
}

// The typical streams, fed frame by frame as the gateway writes them.
func TestResponseObserverMatchesReferenceOnTypicalStreams(t *testing.T) {
	for name, frames := range map[string][][]byte{
		"responses": ssefixture.ResponsesStream(200),
		"chat":      ssefixture.ChatStream(200),
		"anthropic": ssefixture.AnthropicStream(200),
	} {
		input := ssefixture.Join(frames)
		var cuts []int
		at := 0
		for _, f := range frames {
			at += len(f)
			cuts = append(cuts, at)
		}
		compareObservers(t, name+"/frames", input, cuts, "text/event-stream")
		compareObservers(t, name+"/whole", input, nil, "")
		compareObservers(t, name+"/odd", input, []int{3, 100, 101, 5000}, "text/event-stream")
	}
}

// Long strings: the bulk path must agree on the token buffer limit (258
// bytes) as well as on the delta flag.
func TestResponseObserverMatchesReferenceOnLongTokens(t *testing.T) {
	for _, n := range []int{0, 1, 2, 250, 255, 256, 257, 258, 259, 260, 300, 1000} {
		long := strings.Repeat("a", n)
		for _, doc := range []string{
			`{"type":"` + long + `"}`,
			`{"type":"response.completed","status":"` + long + `"}`,
			`{"` + long + `":1,"type":"response.completed"}`,
			`{"error":"` + long + `","type":"response.completed"}`,
			`{"delta":"` + long + `","type":"response.output_text.delta"}`,
			`{"response":{"error":"` + long + `","status":"completed"},"type":"response.completed"}`,
		} {
			for _, ct := range []string{"application/json", "text/event-stream"} {
				input := doc
				if ct == "text/event-stream" {
					input = "data: " + doc + "\n\n"
				}
				for _, cuts := range [][]int{nil, {len(input) / 2}, {5, 6, 7, 8, 9, 10, 11, 12}} {
					compareObservers(t, fmt.Sprintf("len %d %s %.40q", n, ct, doc), []byte(input), cuts, ct)
				}
			}
		}
	}
}

func TestValidPrimitiveMatchesJSONValid(t *testing.T) {
	alphabet := []byte("0123456789.-+eE" + "rulsatfn")
	var check func(prefix []byte, depth int)
	check = func(prefix []byte, depth int) {
		if len(prefix) > 0 && validPrimitive(prefix) != json.Valid(prefix) {
			t.Fatalf("validPrimitive(%q) = %v", prefix, validPrimitive(prefix))
		}
		if depth == 0 {
			return
		}
		for _, c := range alphabet {
			check(append(prefix[:len(prefix):len(prefix)], c), depth-1)
		}
	}
	check(nil, 5)
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 20000; i++ {
		n := 1 + r.Intn(24)
		b := make([]byte, n)
		for j := range b {
			b[j] = alphabet[r.Intn(len(alphabet))]
		}
		if bytes.Count(b, []byte("e"))+bytes.Count(b, []byte("E")) > 2 {
			continue
		}
		if validPrimitive(b) != json.Valid(b) {
			t.Fatalf("validPrimitive(%q) = %v", b, validPrimitive(b))
		}
	}
	for _, s := range []string{"true", "false", "null", "0", "-0", "1", "10", "19", "1.5", "1e5", "007", "-", "tru", "nulll", "123456789012345678901234567890"} {
		if validPrimitive([]byte(s)) != json.Valid([]byte(s)) {
			t.Fatalf("validPrimitive(%q)", s)
		}
	}
}

// ---------------------------------------------------------------------------
// Benchmarks: one frame per Write, as the gateway's writer chain delivers them.

func benchmarkObserver(b *testing.B, frames [][]byte, write func([][]byte)) {
	total := 0
	for _, f := range frames {
		total += len(f)
	}
	b.SetBytes(int64(total))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		write(frames)
	}
}

func BenchmarkOutcomeObserver(b *testing.B) {
	for name, frames := range map[string][][]byte{
		"responses_1000":     ssefixture.ResponsesStream(1000),
		"chat_1000":          ssefixture.ChatStream(1000),
		"anthropic_1000":     ssefixture.AnthropicStream(1000),
		"responses_echo_300": ssefixture.ResponsesStreamWithEcho(300, 20, 2000),
	} {
		b.Run(name+"/new", func(b *testing.B) {
			benchmarkObserver(b, frames, func(frames [][]byte) {
				o := NewOutcomeObserver("text/event-stream")
				for _, f := range frames {
					o.Write(f)
				}
				_ = o.Finish()
			})
		})
		b.Run(name+"/reference", func(b *testing.B) {
			benchmarkObserver(b, frames, func(frames [][]byte) {
				o := &refObserver{framing: newBodyFraming("text/event-stream")}
				for _, f := range frames {
					o.write(f)
				}
				o.end()
			})
		})
	}
}
