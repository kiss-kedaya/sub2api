package requestcapture

import (
	"bytes"
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
	textDeltaSeen   bool
}

func (o *responseObserver) write(p []byte) {
	prefix, rest := o.framing.detect(p)
	o.writeDetected(prefix)
	o.writeDetected(rest)
}

func (o *responseObserver) writeDetected(p []byte) {
	if !o.framing.sse {
		o.json.feed(p)
		return
	}
	for i := 0; i < len(p); {
		b := p[i]
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
			i++
			continue
		}
		if o.data || o.skipLine {
			// Everything up to the next newline belongs to the current line:
			// hand it over in one piece instead of byte by byte.
			n := bytes.IndexByte(p[i:], '\n')
			if n < 0 {
				n = len(p) - i
			}
			if o.data {
				o.feedData(p[i : i+n])
			}
			i += n
			continue
		}
		i++
		if b == '\r' {
			continue
		}
		o.line[o.lineLen] = b
		o.lineLen++
		if string(o.line[:o.lineLen]) != "data:"[:o.lineLen] {
			o.skipLine = true
		} else if o.lineLen == len(o.line) {
			o.data, o.hasData = true, true
		}
	}
}

// feedData processes bytes of an SSE "data:" field (no newline inside).
func (o *responseObserver) feedData(seg []byte) {
	// The marker only ever needs the first few non-blank bytes ("[DONE]");
	// once it holds more than len(marker) of them nothing can change it.
	for len(seg) > 0 && o.markerN <= len(o.marker) {
		b := seg[0]
		seg = seg[1:]
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
	}
	o.json.feed(seg)
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
		if !failed && o.json.event == "response.output_text.delta" && o.json.nonemptyDelta {
			o.textDeltaSeen = true
		}
		o.expectsTerminal = o.expectsTerminal || strings.HasPrefix(o.json.event, "response.") || o.json.event == "message_start" || o.json.event == "message_delta" || o.json.event == "message_stop"
		if event != "" {
			o.terminal = event
		}
		o.failed = o.failed || failed
	}
	o.json.reset()
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
	// Track whether the top-level delta string contained bytes. Do not buffer the text.
	nonemptyDelta bool
	event         string
	status        string
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
	if !quoted && state == 0 && scope == 1 && field == "delta" && b != ' ' && b != '\t' && b != '\r' && b != '\n' {
		j.nonemptyDelta = false
	}
	if quoted && !j.keyToken && j.scope == 1 && j.field == "delta" && (b != '"' || j.v.escape) {
		j.nonemptyDelta = true
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
		j.finishToken()
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

// feed is write applied to every byte of p. The content of a quoted string
// (the bulk of any response) changes no parser state beyond two flags, and the
// inside of an object or array nobody looks at (tool schemas, echoed
// instructions, usage details) changes nothing but the validator, so both are
// consumed without the per-byte bookkeeping.
func (j *responseJSON) feed(p []byte) {
	for len(p) > 0 && !j.invalid {
		if j.v.quoted {
			if !j.v.escape && j.v.unicode == 0 {
				if n := plainStringRun(p); n > 0 {
					j.stringRun(p[:n])
					p = p[n:]
					continue
				}
			}
		} else if depth := len(j.v.stack); depth > 0 && j.scopes[depth-1] == 0 {
			p = j.feedUnwatched(p)
			continue
		}
		j.write(p[0])
		p = p[1:]
	}
}

// feedUnwatched is write for bytes outside any string whose innermost container
// has scope 0, that is: not the root, response or error object. It stops
// when a string opens, the container ends or the input is exhausted, and
// always consumes at least one byte.
func (j *responseJSON) feedUnwatched(p []byte) []byte {
	v := &j.v
	for len(p) > 0 {
		depth := len(v.stack)
		if depth == 0 || j.scopes[depth-1] != 0 {
			return p
		}
		b := p[0]
		p = p[1:]
		if b == '"' {
			// What write records at the opening quote; with scope 0 nothing is captured.
			state := v.stack[depth-1]
			j.keyToken = state == 1 || state == 5
			j.capture = false
			j.tokenN, j.scope, j.field = 0, 0, ""
		}
		if !v.accept(b) {
			j.invalid = true
			return p
		}
		if b == '{' || b == '[' {
			j.scopes[depth] = 0
		}
		if v.quoted {
			return p
		}
	}
	return p
}

// plainStringRun counts the leading bytes of p that a JSON string accepts
// without any state change: not a quote, a backslash or a control character.
func plainStringRun(p []byte) int {
	for i, b := range p {
		if b < 0x20 || b == '"' || b == '\\' {
			return i
		}
	}
	return len(p)
}

// stringRun is write for a run of plain string bytes; run is never empty.
func (j *responseJSON) stringRun(run []byte) {
	if !j.keyToken && j.scope == 1 && j.field == "delta" {
		j.nonemptyDelta = true
	}
	if j.capture {
		if j.tokenN < len(j.token) {
			copy(j.token[j.tokenN:], run)
		}
		j.tokenN = min(j.tokenN+len(run), len(j.token)+1)
	}
}

// reset readies the parser for the next event, keeping the validator buffers.
func (j *responseJSON) reset() {
	stack, kinds, primitive := j.v.stack[:0], j.v.kinds[:0], j.v.primitive[:0]
	*j = responseJSON{}
	j.v.stack, j.v.kinds, j.v.primitive = stack, kinds, primitive
}

// finishToken interprets the captured string token (quotes included). Only the
// protocol vocabulary is ever compared, so every other name collapses to "".
func (j *responseJSON) finishToken() {
	overflow := j.tokenN > len(j.token)
	var text []byte
	if !overflow {
		tok := j.token[:j.tokenN]
		if inner := tok[1 : len(tok)-1]; isPlainJSONText(inner) {
			text = inner
		} else {
			var value string
			_ = json.Unmarshal(tok, &value)
			text = []byte(value)
		}
	}
	if j.keyToken {
		j.keys[j.scope] = canonicalKey(text)
		return
	}
	switch j.field {
	case "type":
		j.event = canonicalEvent(text)
	case "status":
		j.status = canonicalStatus(text)
	case "error":
		j.hasError = j.hasError || len(text) > 0 || overflow
	}
}

// isPlainJSONText reports whether the raw string content decodes to itself.
func isPlainJSONText(b []byte) bool {
	for _, c := range b {
		if c == '\\' || c >= 0x80 {
			return false
		}
	}
	return true
}

func canonicalKey(b []byte) string {
	switch string(b) {
	case "type":
		return "type"
	case "delta":
		return "delta"
	case "status":
		return "status"
	case "error":
		return "error"
	case "response":
		return "response"
	}
	return ""
}

// canonicalEvent keeps the event names the observer reacts to; any other
// "response.*" name is reduced to the bare prefix, anything else to "".
func canonicalEvent(b []byte) string {
	switch string(b) {
	case "response.output_text.delta":
		return "response.output_text.delta"
	case "response.completed":
		return "response.completed"
	case "response.done":
		return "response.done"
	case "response.failed":
		return "response.failed"
	case "response.incomplete":
		return "response.incomplete"
	case "response.cancelled":
		return "response.cancelled"
	case "response.canceled":
		return "response.canceled"
	case "message_start":
		return "message_start"
	case "message_delta":
		return "message_delta"
	case "message_stop":
		return "message_stop"
	case "error":
		return "error"
	}
	if bytes.HasPrefix(b, []byte("response.")) {
		return "response."
	}
	return ""
}

func canonicalStatus(b []byte) string {
	switch string(b) {
	case "completed":
		return "completed"
	case "failed":
		return "failed"
	case "incomplete":
		return "incomplete"
	case "cancelled":
		return "cancelled"
	case "canceled":
		return "canceled"
	}
	return ""
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
