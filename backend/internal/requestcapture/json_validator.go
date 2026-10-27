package requestcapture

import "encoding/json"

// This scanner validates structure without allocating strings or whole events.
// States: value, object key/end, colon, comma/end, array value/end, object key.
type jsonValidator struct {
	stack               []byte
	kinds               []byte
	root                byte
	quoted, key, escape bool
	unicode             int
	primitive           []byte
}

func (v *jsonValidator) state() byte {
	if len(v.stack) == 0 {
		return v.root
	}
	return v.stack[len(v.stack)-1]
}
func (v *jsonValidator) set(s byte) {
	if len(v.stack) == 0 {
		v.root = s
	} else {
		v.stack[len(v.stack)-1] = s
	}
}
func (v *jsonValidator) complete() bool {
	if len(v.primitive) > 0 {
		if !json.Valid(v.primitive) {
			return false
		}
		v.primitive = nil
		v.set(3)
	}
	return len(v.stack) == 0 && v.root == 3 && !v.quoted
}
func (v *jsonValidator) accept(b byte) bool {
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
