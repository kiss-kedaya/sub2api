package requestcapture

import (
	"encoding/json"
	"strings"
)

// Missing Content-Type is common on OAuth SSE responses. Retain at most one
// field prefix while detecting framing, never a whole line or response.
type bodyFraming struct {
	pending bool
	sse     bool
	prefix  []byte
}

func newBodyFraming(ct string) bodyFraming {
	ct = strings.ToLower(strings.TrimSpace(ct))
	return bodyFraming{pending: ct == "", sse: strings.Contains(ct, "text/event-stream")}
}

func (f *bodyFraming) detect(p []byte) (prefix, rest []byte) {
	if !f.pending {
		return nil, p
	}
	for len(p) > 0 {
		b := p[0]
		p = p[1:]
		if len(f.prefix) == 0 && (b == ' ' || b == '\n' || b == '\r' || b == '\t') {
			continue
		}
		f.prefix = append(f.prefix, b)
		prefix := string(f.prefix)
		if prefix == "\xef\xbb\xbf" {
			f.prefix = f.prefix[:0]
			continue
		}
		possible := strings.HasPrefix("\xef\xbb\xbf", prefix)
		for _, field := range []string{"data:", "event:", "id:", "retry:", ":"} {
			if prefix == field {
				f.sse = true
				f.pending = false
				break
			}
			possible = possible || strings.HasPrefix(field, prefix)
		}
		if f.pending && possible {
			continue
		}
		f.pending = false
		out := f.prefix
		f.prefix = nil
		return out, p
	}
	return nil, nil
}

// responseObserver validates JSON incrementally and extracts only shallow
// protocol fields. Large output strings never enter a diagnostic buffer.
// Its bounded token and depth storage use a separate 4 KiB stream charge.
type responseObserver struct {
	sawEvent        bool
	framing         bodyFraming
	json            responseJSON
	line            [5]byte
	lineLen         int
	data            bool
	skipLine        bool
	hasData         bool
	marker          [6]byte
	markerN         int
	markerSpace     bool
	terminal        string
	failed          bool
	expectsTerminal bool
}

func (o *responseObserver) write(p []byte) {
	prefix, rest := o.framing.detect(p)
	o.writeDetected(prefix)
	o.writeDetected(rest)
}

func (o *responseObserver) writeDetected(p []byte) {
	if !o.framing.sse {
		for _, b := range p {
			o.json.write(b)
		}
		return
	}
	for _, b := range p {
		if b == '\n' {
			if o.data {
				o.json.write('\n')
				if o.markerN > 0 {
					o.markerSpace = true
				}
			} else if o.lineLen == 0 && !o.skipLine {
				o.endEvent()
			}
			o.lineLen, o.data, o.skipLine = 0, false, false
			continue
		}
		if o.data {
			o.json.write(b)
			if b != ' ' && b != '\t' && b != '\r' {
				if o.markerSpace {
					o.markerN = len(o.marker) + 1
				}
				if o.markerN < len(o.marker) {
					o.marker[o.markerN] = b
				}
				if o.markerN <= len(o.marker) {
					o.markerN++
				}
			} else if o.markerN > 0 {
				o.markerSpace = true
			}
			continue
		}
		if o.skipLine || b == '\r' {
			continue
		}
		o.line[o.lineLen] = b
		o.lineLen++
		if string(o.line[:o.lineLen]) == "data:" {
			o.data, o.hasData = true, true
		} else if !strings.HasPrefix("data:", string(o.line[:o.lineLen])) {
			o.skipLine = true
		}
	}
}

const responseObserverCharge = 4 << 10

func (o *responseObserver) endEvent() {
	if !o.hasData {
		return
	}
	if o.markerN == 6 && string(o.marker[:]) == "[DONE]" {
		o.terminal = "[DONE]"
		o.expectsTerminal = true
	} else if event, failed, valid := o.json.result(); valid {
		o.sawEvent = true
		o.expectsTerminal = o.expectsTerminal || strings.HasPrefix(o.json.event, "response.") || o.json.event == "message_start" || o.json.event == "message_delta" || o.json.event == "message_stop"
		if event != "" {
			o.terminal = event
		}
		o.failed = o.failed || failed
	}
	o.json = responseJSON{}
	o.hasData, o.markerN = false, 0
	o.markerSpace = false
}

func (o *responseObserver) end() {
	if o.framing.sse {
		o.endEvent()
		return
	}
	if event, failed, valid := o.json.result(); valid {
		o.terminal, o.failed = event, o.failed || failed
		if o.terminal == "" && !o.failed {
			o.terminal = "json"
		}
	}
}

func (o *responseObserver) successful() bool {
	return !o.failed && (o.terminal == "response.completed" || o.terminal == "response.done" || o.terminal == "message_stop" || o.terminal == "[DONE]" || o.terminal == "json")
}

// Scopes: 1 = root object, 2 = response object, 3 = error object.
// Ignore nested output, tool arguments and quoted protocol-looking text.
type responseJSON struct {
	v        jsonValidator
	scopes   [128]byte
	keys     [3]string
	token    [258]byte
	tokenN   int
	capture  bool
	keyToken bool
	field    string
	scope    byte
	invalid  bool
	hasError bool
	event    string
	status   string
}

func (j *responseJSON) write(b byte) {
	if j.invalid {
		return
	}
	depth, state, quoted := len(j.v.stack), j.v.state(), j.v.quoted
	scope := byte(0)
	if depth > 0 {
		scope = j.scopes[depth-1]
	}
	field := ""
	if scope == 1 || scope == 2 {
		field = j.keys[scope]
	}
	if !quoted && b == '"' {
		j.keyToken = state == 1 || state == 5
		j.capture = (scope == 1 || scope == 2) && (j.keyToken || field == "status" || field == "error" || scope == 1 && field == "type")
		j.tokenN, j.scope, j.field = 0, scope, field
		if j.keyToken && scope == 3 {
			j.hasError = true
		}
	}
	if j.capture {
		if j.tokenN < len(j.token) {
			j.token[j.tokenN] = b
		}
		if j.tokenN <= len(j.token) {
			j.tokenN++
		}
	}
	if !j.v.accept(b) {
		j.invalid = true
		return
	}
	if quoted && !j.v.quoted && j.capture {
		var value string
		if j.tokenN <= len(j.token) {
			_ = json.Unmarshal(j.token[:j.tokenN], &value)
		}
		if j.keyToken {
			j.keys[j.scope] = value
		} else {
			switch j.field {
			case "type":
				j.event = value
			case "status":
				j.status = value
			case "error":
				j.hasError = j.hasError || value != "" || j.tokenN > len(j.token)
			}
		}
		j.capture = false
	}
	if !quoted && (b == '{' || b == '[') {
		next := byte(0)
		if b == '{' {
			switch {
			case depth == 0:
				next = 1
			case scope == 1 && field == "response":
				next = 2
			case (scope == 1 || scope == 2) && field == "error":
				next = 3
			}
		}
		j.scopes[depth] = next
	}
	if !quoted && state == 0 && (scope == 1 || scope == 2) && field == "error" && b == 't' {
		j.hasError = true
	}
}

func (j *responseJSON) result() (event string, failed, valid bool) {
	if j.invalid || !j.v.complete() {
		return "", false, false
	}
	switch j.event {
	case "response.completed", "response.done", "message_stop":
		event = j.event
	case "response.failed", "response.incomplete", "response.cancelled", "response.canceled", "error":
		event, failed = j.event, true
	}
	switch j.status {
	case "completed":
		if event == "" {
			event = "response.completed"
		}
	case "failed", "incomplete", "cancelled", "canceled":
		failed = true
	}
	return event, failed || j.hasError, true
}
