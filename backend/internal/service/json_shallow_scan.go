package service

import (
	"encoding/binary"
	"strings"
	"unicode/utf8"
	"unsafe"
)

// Zero-allocation helpers for request pre-processing that only needs to know
// whether a body has to be rewritten at all.
//
// The rewrite passes in this package decode the whole request body into a
// map[string]any, which costs several times the body size in garbage, even though
// almost every request leaves the body untouched. The probes built on these
// helpers answer "can this possibly change?" with a single pass that skips
// large strings and containers instead of materializing them.
//
// The contract is one-sided: a probe may say "maybe" for a body that turns out to
// be a no-op (the caller then runs the original decoder, which is always
// correct), but it must never say "no change" for a body the decoder would
// rewrite. Everything here therefore gives up (ok=false) on constructs it does
// not interpret itself: malformed text, object keys or interpreted string
// values with escapes or non-ASCII bytes, and duplicate keys.

// jsonView returns b as a string without copying. The result is read-only and
// must not outlive the next write to b.
func jsonView(b []byte) string {
	return unsafe.String(unsafe.SliceData(b), len(b))
}

func jsonSkipSpace(s string, i int) int {
	for i < len(s) {
		switch s[i] {
		case ' ', '\t', '\n', '\r':
			i++
		default:
			return i
		}
	}
	return i
}

// jsonSkipString skips the string whose opening quote is s[i]. It returns the
// index just past the closing quote and whether the content holds a backslash.
// Long strings are crossed with IndexByte instead of a byte loop.
func jsonSkipString(s string, i int) (end int, esc bool, ok bool) {
	start := i + 1
	j := start
	for {
		k := strings.IndexByte(s[j:], '"')
		if k < 0 {
			return len(s), esc, false
		}
		q := j + k
		backslashes := 0
		for p := q - 1; p >= start && s[p] == '\\'; p-- {
			backslashes++
		}
		if backslashes&1 == 0 {
			if !esc {
				esc = strings.IndexByte(s[start:q], '\\') >= 0
			}
			return q + 1, esc, true
		}
		esc = true
		j = q + 1
	}
}

// jsonSkipValue returns the end of the value starting at or after s[i].
func jsonSkipValue(s string, i int) (end int, ok bool) {
	i = jsonSkipSpace(s, i)
	if i >= len(s) {
		return i, false
	}
	switch s[i] {
	case '"':
		end, _, ok = jsonSkipString(s, i)
		return end, ok
	case '{', '[':
		depth := 0
		for i < len(s) {
			switch s[i] {
			case '"':
				end, _, ok = jsonSkipString(s, i)
				if !ok {
					return end, false
				}
				i = end
				continue
			case '{', '[':
				depth++
			case '}', ']':
				depth--
				if depth == 0 {
					return i + 1, true
				}
			}
			i++
		}
		return i, false
	}
	j := i
	for j < len(s) {
		switch s[j] {
		case ',', '}', ']', ' ', '\t', '\n', '\r':
			return j, j > i
		}
		j++
	}
	return j, j > i
}

// jsonForEachElement calls fn with the raw text of every element of the array s.
// It reports false when s is not an array it can walk. fn returning false stops
// the walk early (still reported as true).
func jsonForEachElement(s string, fn func(elem string) bool) bool {
	i := jsonSkipSpace(s, 0)
	if i >= len(s) || s[i] != '[' {
		return false
	}
	i++
	for {
		i = jsonSkipSpace(s, i)
		if i >= len(s) {
			return false
		}
		switch s[i] {
		case ']':
			return true
		case ',':
			i++
			continue
		}
		end, ok := jsonSkipValue(s, i)
		if !ok {
			return false
		}
		if !fn(s[i:end]) {
			return true
		}
		i = end
	}
}

// jsonForEachMember calls fn with the raw key text (without quotes) and the raw
// value text of every member of the object s, in document order, duplicates
// included. It reports false when s is not an object it can walk, including any
// key that contains a backslash: such a key could spell a name the caller is
// looking for in a form a plain comparison would miss.
func jsonForEachMember(s string, fn func(key, val string) bool) bool {
	i := jsonSkipSpace(s, 0)
	if i >= len(s) || s[i] != '{' {
		return false
	}
	i++
	for {
		i = jsonSkipSpace(s, i)
		if i >= len(s) {
			return false
		}
		switch s[i] {
		case '}':
			return true
		case ',':
			i++
			continue
		case '"':
		default:
			return false
		}
		end, esc, ok := jsonSkipString(s, i)
		if !ok || esc {
			return false
		}
		key := s[i+1 : end-1]
		i = jsonSkipSpace(s, end)
		if i >= len(s) || s[i] != ':' {
			return false
		}
		i = jsonSkipSpace(s, i+1)
		vend, ok := jsonSkipValue(s, i)
		if !ok {
			return false
		}
		if !fn(key, s[i:vend]) {
			return true
		}
		i = vend
	}
}

// jsonPlainString interprets a raw value the way stringValue(any) does: a
// non-string reads as the empty string. A string is only returned when its
// bytes are the decoded text, i.e. pure ASCII without escapes; anything else
// reports ok=false so the caller defers to the real decoder.
func jsonPlainString(val string) (s string, ok bool) {
	if val == "" || val[0] != '"' {
		return "", true
	}
	if len(val) < 2 || val[len(val)-1] != '"' {
		return "", false
	}
	s = val[1 : len(val)-1]
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' || s[i] >= utf8.RuneSelf {
			return "", false
		}
	}
	return s, true
}

// jsonMaxDepth is encoding/json's nesting limit: a document nested deeper is
// rejected by json.Valid and json.Unmarshal.
const jsonMaxDepth = 10000

// jsonStringClass classifies bytes inside a JSON string for validJSON.
var jsonStringClass = func() (t [256]uint8) {
	for i := 0; i < 0x20; i++ {
		t[i] = 3 // control characters are not allowed raw
	}
	t['"'] = 1
	t['\\'] = 2
	return
}()

// validJSON is json.Valid without the per-byte state-machine dispatch.
// It accepts exactly the documents json.Valid accepts (RFC 8259 text, nesting up
// to jsonMaxDepth, no UTF-8 check), at several times the speed.
func validJSON(b []byte) bool {
	n := len(b)
	var stackBuf [64]byte
	stack := stackBuf[:0]
	const (
		sValue = iota
		sObjFirst
		sKey
		sColon
		sArrFirst
		sAfter
	)
	state := sValue
	i := 0
	for {
		for i < n {
			c := b[i]
			if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
				break
			}
			i++
		}
		if i >= n {
			return state == sAfter && len(stack) == 0
		}
		c := b[i]
		switch state {
		case sValue:
			switch {
			case c == '{':
				if len(stack) >= jsonMaxDepth {
					return false
				}
				stack = append(stack, '{')
				i++
				state = sObjFirst
			case c == '[':
				if len(stack) >= jsonMaxDepth {
					return false
				}
				stack = append(stack, '[')
				i++
				state = sArrFirst
			case c == '"':
				var ok bool
				if i, ok = validJSONString(b, i+1); !ok {
					return false
				}
				state = sAfter
			case c == '-' || (c >= '0' && c <= '9'):
				var ok bool
				if i, ok = validJSONNumber(b, i); !ok {
					return false
				}
				state = sAfter
			case c == 't':
				if !validJSONLiteral(b, i, "true") {
					return false
				}
				i += 4
				state = sAfter
			case c == 'f':
				if !validJSONLiteral(b, i, "false") {
					return false
				}
				i += 5
				state = sAfter
			case c == 'n':
				if !validJSONLiteral(b, i, "null") {
					return false
				}
				i += 4
				state = sAfter
			default:
				return false
			}
		case sObjFirst:
			switch c {
			case '}':
				stack = stack[:len(stack)-1]
				i++
				state = sAfter
			case '"':
				var ok bool
				if i, ok = validJSONString(b, i+1); !ok {
					return false
				}
				state = sColon
			default:
				return false
			}
		case sKey:
			if c != '"' {
				return false
			}
			var ok bool
			if i, ok = validJSONString(b, i+1); !ok {
				return false
			}
			state = sColon
		case sColon:
			if c != ':' {
				return false
			}
			i++
			state = sValue
		case sArrFirst:
			if c == ']' {
				stack = stack[:len(stack)-1]
				i++
				state = sAfter
			} else {
				state = sValue
			}
		case sAfter:
			if len(stack) == 0 {
				return false
			}
			if stack[len(stack)-1] == '{' {
				switch c {
				case ',':
					i++
					state = sKey
				case '}':
					stack = stack[:len(stack)-1]
					i++
				default:
					return false
				}
			} else {
				switch c {
				case ',':
					i++
					state = sValue
				case ']':
					stack = stack[:len(stack)-1]
					i++
				default:
					return false
				}
			}
		}
	}
}

func validJSONLiteral(b []byte, i int, lit string) bool {
	return len(b)-i >= len(lit) && string(b[i:i+len(lit)]) == lit
}

const (
	jsonStringSWARLo = uint64(0x0101010101010101)
	jsonStringSWARHi = uint64(0x8080808080808080)
)

// skipSafeJSONStringRun advances i across bytes that are valid unescaped JSON
// string content (0x20..0xff excluding '"' and '\\'), 8 bytes at a time via SWAR.
func skipSafeJSONStringRun(b []byte, i int) int {
	n := len(b)
	if i < n && jsonStringClass[b[i]] == 0 {
		for i+8 <= n {
			w := binary.LittleEndian.Uint64(b[i:])
			ctrl := (w - 0x20*jsonStringSWARLo) &^ w & jsonStringSWARHi
			q := w ^ (0x22 * jsonStringSWARLo)
			quote := (q - jsonStringSWARLo) &^ q & jsonStringSWARHi
			e := w ^ (0x5c * jsonStringSWARLo)
			esc := (e - jsonStringSWARLo) &^ e & jsonStringSWARHi
			if (ctrl | quote | esc) != 0 {
				break
			}
			i += 8
		}
		for i < n && jsonStringClass[b[i]] == 0 {
			i++
		}
	}
	return i
}

// validJSONString scans a string whose opening quote precedes b[i]; it returns
// the index after the closing quote.
func validJSONString(b []byte, i int) (int, bool) {
	n := len(b)
	for i < n {
		i = skipSafeJSONStringRun(b, i)
		if i >= n {
			return i, false
		}
		switch jsonStringClass[b[i]] {
		case 1:
			return i + 1, true
		case 2:
			i++
			if i >= n {
				return i, false
			}
			switch b[i] {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				i++
			case 'u':
				if n-i < 5 {
					return i, false
				}
				for k := 1; k <= 4; k++ {
					h := b[i+k]
					if (h < '0' || h > '9') && (h < 'a' || h > 'f') && (h < 'A' || h > 'F') {
						return i, false
					}
				}
				i += 5
			default:
				return i, false
			}
		default:
			return i, false
		}
	}
	return i, false
}

// validJSONNumber scans -?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)? at b[i].
func validJSONNumber(b []byte, i int) (int, bool) {
	n := len(b)
	if b[i] == '-' {
		i++
		if i >= n {
			return i, false
		}
	}
	switch {
	case b[i] == '0':
		i++
	case b[i] >= '1' && b[i] <= '9':
		for i < n && b[i] >= '0' && b[i] <= '9' {
			i++
		}
	default:
		return i, false
	}
	if i < n && b[i] == '.' {
		i++
		if i >= n || b[i] < '0' || b[i] > '9' {
			return i, false
		}
		for i < n && b[i] >= '0' && b[i] <= '9' {
			i++
		}
	}
	if i < n && (b[i] == 'e' || b[i] == 'E') {
		i++
		if i < n && (b[i] == '+' || b[i] == '-') {
			i++
		}
		if i >= n || b[i] < '0' || b[i] > '9' {
			return i, false
		}
		for i < n && b[i] >= '0' && b[i] <= '9' {
			i++
		}
	}
	return i, true
}
