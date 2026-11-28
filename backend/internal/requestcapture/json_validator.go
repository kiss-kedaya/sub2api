package requestcapture

// This scanner validates structure without allocating strings or whole events.
// States: value, object key/end, colon, comma/end, array value/end, object key.
type jsonValidator struct {
	stack               [128]byte
	kinds               [128]byte
	depth               uint8
	root                byte
	quoted, key, escape bool
	unicode             int
	primitive           [128]byte
	primLen             uint8
}

// validPrimitive is json.Valid for the number/true/false/null tokens collected
// in primitive, with zero allocations.
func validPrimitive(p []byte) bool {
	switch string(p) {
	case "true", "false", "null":
		return true
	}
	return validJSONNumber(p)
}

func validJSONNumber(p []byte) bool {
	n := len(p)
	if n == 0 {
		return false
	}
	i := 0
	if p[i] == '-' {
		i++
		if i >= n {
			return false
		}
	}
	switch {
	case p[i] == '0':
		i++
	case p[i] >= '1' && p[i] <= '9':
		for i < n && p[i] >= '0' && p[i] <= '9' {
			i++
		}
	default:
		return false
	}
	if i < n && p[i] == '.' {
		i++
		if i >= n || p[i] < '0' || p[i] > '9' {
			return false
		}
		for i < n && p[i] >= '0' && p[i] <= '9' {
			i++
		}
	}
	if i < n && (p[i] == 'e' || p[i] == 'E') {
		i++
		if i < n && (p[i] == '+' || p[i] == '-') {
			i++
		}
		if i >= n || p[i] < '0' || p[i] > '9' {
			return false
		}
		for i < n && p[i] >= '0' && p[i] <= '9' {
			i++
		}
	}
	return i == n
}

func (v *jsonValidator) state() byte {
	if v.depth == 0 {
		return v.root
	}
	return v.stack[v.depth-1]
}
func (v *jsonValidator) set(s byte) {
	if v.depth == 0 {
		v.root = s
	} else {
		v.stack[v.depth-1] = s
	}
}
func (v *jsonValidator) complete() bool {
	if v.primLen > 0 {
		if !validPrimitive(v.primitive[:v.primLen]) {
			return false
		}
		v.primLen = 0
		v.set(3)
	}
	return v.depth == 0 && v.root == 3 && !v.quoted
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
	if v.primLen > 0 {
		if b != ',' && b != '}' && b != ']' && !space {
			if v.primLen >= 128 || ((b < '0' || b > '9') && b != '.' && b != '-' && b != '+' && b != 'e' && b != 'E' && b != 'r' && b != 'u' && b != 'l' && b != 's' && b != 'a' && b != 't' && b != 'f' && b != 'n') {
				return false
			}
			v.primitive[v.primLen] = b
			v.primLen++
			return true
		}
		if !validPrimitive(v.primitive[:v.primLen]) {
			return false
		}
		v.primLen = 0
		v.set(3)
	}
	if space {
		return true
	}
	s := v.state()
	if b == '}' || b == ']' {
		if v.depth == 0 {
			return false
		}
		kind := v.kinds[v.depth-1]
		if b == '}' && (kind != '{' || s != 1 && s != 3) || b == ']' && (kind != '[' || s != 4 && s != 3) {
			return false
		}
		v.depth--
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
		if b != ',' || v.depth == 0 {
			return false
		}
		if v.kinds[v.depth-1] == '{' {
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
		if v.depth >= 128 {
			return false
		}
		next := byte(1)
		if b == '[' {
			next = 4
		}
		v.stack[v.depth] = next
		v.kinds[v.depth] = b
		v.depth++
		return true
	}
	if b == '-' || b >= '0' && b <= '9' || b == 't' || b == 'f' || b == 'n' {
		v.primitive[v.primLen] = b
		v.primLen++
		return true
	}
	return false
}
