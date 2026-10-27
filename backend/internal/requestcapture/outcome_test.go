package requestcapture

import "testing"

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
