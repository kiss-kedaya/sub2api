package requestcapture

import "unsafe"

// OutcomeObserver exposes the bounded protocol parser for metadata-only
// diagnostics. It retains no response text and never interprets nested tool
// arguments as protocol events.
type OutcomeObserver struct{ observer responseObserver }

func NewOutcomeObserver(contentType string) *OutcomeObserver {
	return &OutcomeObserver{observer: responseObserver{framing: newBodyFraming(contentType)}}
}

func (o *OutcomeObserver) Write(p []byte) { o.observer.write(p) }

// WriteString observes s without allocating a temporary []byte copy;
// responseObserver.write never retains or mutates its input slice.
func (o *OutcomeObserver) WriteString(s string) {
	if len(s) == 0 {
		return
	}
	o.observer.write(unsafe.Slice(unsafe.StringData(s), len(s)))
}

// TextDeltaSeen reports a complete SSE Responses text delta with nonempty text.
func (o *OutcomeObserver) TextDeltaSeen() bool { return o.observer.textDeltaSeen }

func (o *OutcomeObserver) Finish() (terminal string) {
	o.observer.end()
	if o.observer.failed {
		return "failed"
	}
	if o.observer.successful() {
		return "completed"
	}
	if o.observer.expectsTerminal {
		return "incomplete"
	}
	return ""
}
