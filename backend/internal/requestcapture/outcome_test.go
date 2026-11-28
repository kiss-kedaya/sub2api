package requestcapture

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ssefixture"
)

func TestOutcomeObserverPreservesStreamFailure(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"completed", "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n", "completed"},
		{"http200_error", "data: {\"type\":\"error\",\"error\":{\"message\":\"unavailable\"}}\n\n", "failed"},
		{"early_eof", "data: {\"type\":\"response.created\",\"response\":{\"status\":\"in_progress\"}}\n\n", "incomplete"},
		{"tool_text_not_error", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"error: response.failed\"}\n\ndata: [DONE]\n\n", "completed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := NewOutcomeObserver("text/event-stream")
			for _, b := range []byte(tc.body) {
				o.Write([]byte{b})
			}
			if got := o.Finish(); got != tc.want {
				t.Fatalf("outcome=%q want %q", got, tc.want)
			}
		})
	}
}

func TestOutcomeObserverWriteStringMatchesWrite(t *testing.T) {
	for name, frames := range map[string][][]byte{
		"responses": ssefixture.ResponsesStream(40),
		"chat":      ssefixture.ChatStream(40),
		"anthropic": ssefixture.AnthropicStream(40),
	} {
		lines := ssefixture.Lines(frames)
		t.Run(name, func(t *testing.T) {
			oWrite := NewOutcomeObserver("text/event-stream")
			oStr := NewOutcomeObserver("text/event-stream")
			for _, line := range lines {
				chunk := line + "\n"
				oWrite.Write([]byte(chunk))
				oStr.WriteString(chunk)
				if oWrite.TextDeltaSeen() != oStr.TextDeltaSeen() {
					t.Fatalf("TextDeltaSeen mismatch: write=%v writeString=%v", oWrite.TextDeltaSeen(), oStr.TextDeltaSeen())
				}
			}
			if got, want := oStr.Finish(), oWrite.Finish(); got != want {
				t.Fatalf("Finish mismatch: writeString=%q write=%q", got, want)
			}
		})
	}
}

func TestValidJSONNumberEdgeCases(t *testing.T) {
	cases := []string{
		"", "0", "-0", "1", "42", "-42", "0.0", "1.5", "-0.125",
		"1e5", "1E+5", "-2.5e-3", "01", "-01", "1.", ".5", "-", "1e", "1e+", "+1", "1e-0",
	}
	for _, s := range cases {
		got := validJSONNumber([]byte(s))
		want := json.Valid([]byte(s))
		if got != want {
			t.Fatalf("validJSONNumber(%q) = %v, want %v", s, got, want)
		}
	}
}
