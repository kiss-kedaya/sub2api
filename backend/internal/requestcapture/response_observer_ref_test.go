package requestcapture

// Frozen copy of the byte-at-a-time response observer as it was before the
// bulk-skipping rewrite. It is the oracle for the differential tests: the
// production observer must reach exactly the same state after every write.

import (
	"encoding/json"
	"strings"
)

type refValidator struct {
	stack               []byte
	kinds               []byte
	root                byte
	quoted, key, escape bool
	unicode             int
	primitive           []byte
}

func (v *refValidator) state() byte {
	if len(v.stack) == 0 {
		return v.root
	}
	return v.stack[len(v.stack)-1]
}
func (v *refValidator) set(s byte) {
	if len(v.stack) == 0 {
		v.root = s
	} else {
		v.stack[len(v.stack)-1] = s
	}
}
func (v *refValidator) complete() bool {
	if len(v.primitive) > 0 {
		if !json.Valid(v.primitive) {
			return false
		}
		v.primitive = nil
		v.set(3)
	}
	return len(v.stack) == 0 && v.root == 3 && !v.quoted
}
func (v *refValidator) accept(b byte) bool {
	if v.quoted {
		if v.unicode > 0 {
			if (b < '0' || b > '9') && (b < 'a' || b > 'f') && (b < 'A' || b > 'F') {
				return false
			}
			v.unicode--
			return true
		}
		if v.escape {
			v.escape = false
			if b == 'u' {
				v.unicode = 4
				return true
			}
			return b == '"' || b == 92 || b == '/' || b == 'b' || b == 'f' || b == 'n' || b == 'r' || b == 't'
		}
		if b < 32 {
			return false
		}
		switch b {
		case 92:
			v.escape = true
		case '"':
			v.quoted = false
			if v.key {
				v.set(2)
			} else {
				v.set(3)
			}
		}
		return true
	}
	space := b == ' ' || b == '\n' || b == '\r' || b == '\t'
	if len(v.primitive) > 0 {
		if b != ',' && b != '}' && b != ']' && !space {
			if len(v.primitive) >= 128 || ((b < '0' || b > '9') && b != '.' && b != '-' && b != '+' && b != 'e' && b != 'E' && b != 'r' && b != 'u' && b != 'l' && b != 's' && b != 'a' && b != 't' && b != 'f' && b != 'n') {
				return false
			}
			v.primitive = append(v.primitive, b)
			return true
		}
		if !json.Valid(v.primitive) {
			return false
		}
		v.primitive = nil
		v.set(3)
	}
	if space {
		return true
	}
	s := v.state()
	if b == '}' || b == ']' {
		if len(v.stack) == 0 {
			return false
		}
		kind := v.kinds[len(v.kinds)-1]
		if b == '}' && (kind != '{' || s != 1 && s != 3) || b == ']' && (kind != '[' || s != 4 && s != 3) {
			return false
		}
		v.stack = v.stack[:len(v.stack)-1]
		v.kinds = v.kinds[:len(v.kinds)-1]
		v.set(3)
		return true
	}
	if s == 1 || s == 5 {
		if b != '"' {
			return false
		}
		v.quoted = true
		v.key = true
		return true
	}
	if s == 2 {
		if b != ':' {
			return false
		}
		v.set(0)
		return true
	}
	if s == 3 {
		if b != ',' || len(v.stack) == 0 {
			return false
		}
		if v.kinds[len(v.kinds)-1] == '{' {
			v.set(5)
		} else {
			v.set(0)
		}
		return true
	}
	if b == '"' {
		v.quoted = true
		v.key = false
		return true
	}
	if b == '{' || b == '[' {
		if len(v.stack) >= 128 {
			return false
		}
		next := byte(1)
		if b == '[' {
			next = 4
		}
		v.stack = append(v.stack, next)
		v.kinds = append(v.kinds, b)
		return true
	}
	if b == '-' || b >= '0' && b <= '9' || b == 't' || b == 'f' || b == 'n' {
		v.primitive = append(v.primitive, b)
		return true
	}
	return false
}

type refObserver struct {
	sawEvent        bool
	framing         bodyFraming
	json            refJSON
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

func (o *refObserver) write(p []byte) {
	prefix, rest := o.framing.detect(p)
	o.writeDetected(prefix)
	o.writeDetected(rest)
}

func (o *refObserver) writeDetected(p []byte) {
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

func (o *refObserver) endEvent() {
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
	o.json = refJSON{}
	o.hasData, o.markerN = false, 0
	o.markerSpace = false
}

func (o *refObserver) end() {
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

func (o *refObserver) successful() bool {
	return !o.failed && (o.terminal == "response.completed" || o.terminal == "response.done" || o.terminal == "message_stop" || o.terminal == "[DONE]" || o.terminal == "json")
}

type refJSON struct {
	v        refValidator
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

func (j *refJSON) write(b byte) {
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

func (j *refJSON) result() (event string, failed, valid bool) {
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
