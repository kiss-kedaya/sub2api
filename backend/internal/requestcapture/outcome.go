package requestcapture

// OutcomeObserver exposes the bounded protocol parser for metadata-only
// diagnostics. It retains no response text and never interprets nested tool
// arguments as protocol events.
type OutcomeObserver struct{ observer responseObserver }

func NewOutcomeObserver(contentType string) *OutcomeObserver {
	return &OutcomeObserver{observer: responseObserver{framing: newBodyFraming(contentType)}}
}

func (o *OutcomeObserver) Write(p []byte) { o.observer.write(p) }

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
