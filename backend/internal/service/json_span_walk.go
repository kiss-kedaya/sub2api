package service

import (
	"strings"
	"unsafe"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

func isJSONSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// jsonSkipValueStrict advances past a single JSON value starting at b[i] (no
// leading whitespace). Unlike jsonSkipValue, it validates primitive literals and
// numbers, rejects '(' / ')' outside strings (to match gjson.parseSquash), and
// verifies a valid JSON delimiter follows primitives.
func jsonSkipValueStrict(b []byte, i int) (int, bool) {
	if i >= len(b) {
		return i, false
	}
	s := jsonView(b)
	switch b[i] {
	case '"':
		end, _, ok := jsonSkipString(s, i)
		return end, ok
	case '{', '[':
		depth := 0
		for i < len(b) {
			switch b[i] {
			case '"':
				var ok bool
				i, _, ok = jsonSkipString(s, i)
				if !ok {
					return len(b), false
				}
				continue
			case '{', '[':
				depth++
			case '}', ']':
				depth--
				if depth == 0 {
					return i + 1, true
				}
			case '(', ')':
				return i, false
			}
			i++
		}
		return len(b), false
	case 't':
		if !validJSONLiteral(b, i, "true") {
			return i, false
		}
		i += 4
	case 'f':
		if !validJSONLiteral(b, i, "false") {
			return i, false
		}
		i += 5
	case 'n':
		if !validJSONLiteral(b, i, "null") {
			return i, false
		}
		i += 4
	case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		var ok bool
		i, ok = validJSONNumber(b, i)
		if !ok {
			return i, false
		}
	default:
		return i, false
	}
	if i >= len(b) {
		return i, false
	}
	switch b[i] {
	case ' ', '\t', '\n', '\r', ',', '}', ']':
		return i, true
	default:
		return i, false
	}
}

// jsonForEachElementSpan walks a JSON array at b[arrStart:arrEnd] without
// allocation, passing each element's [elemStart, elemEnd) offsets in b.
// Returns false on any malformed syntax or unclosed bracket; early stop via
// fn returning false still returns true.
func jsonForEachElementSpan(b []byte, arrStart, arrEnd int, fn func(elemStart, elemEnd int) bool) bool {
	if arrStart < 0 || arrEnd > len(b) || arrEnd-arrStart < 2 || b[arrStart] != '[' || b[arrEnd-1] != ']' {
		return false
	}
	i := skipJSONWhitespace(b[:arrEnd], arrStart+1)
	if i == arrEnd-1 {
		return true
	}
	for {
		elemStart := i
		elemEnd, ok := jsonSkipValueStrict(b, elemStart)
		if !ok || elemEnd >= arrEnd {
			return false
		}
		if !fn(elemStart, elemEnd) {
			return true
		}
		i = skipJSONWhitespace(b[:arrEnd], elemEnd)
		if i == arrEnd-1 {
			return true
		}
		if i >= arrEnd-1 || b[i] != ',' {
			return false
		}
		i = skipJSONWhitespace(b[:arrEnd], i+1)
		if i >= arrEnd-1 {
			return false
		}
	}
}

// jsonForEachMemberSpan walks a JSON object at b[objStart:objEnd] without
// allocation, passing zero-copy key and [valStart, valEnd), [memberStart,
// memberEnd) byte offsets in b. Any escaped key ('\\') or malformed syntax
// aborts with false so callers can fall back to gjson.
func jsonForEachMemberSpan(b []byte, objStart, objEnd int, fn func(key string, valStart, valEnd, memberStart, memberEnd int) bool) bool {
	if objStart < 0 || objEnd > len(b) || objEnd-objStart < 2 || b[objStart] != '{' || b[objEnd-1] != '}' {
		return false
	}
	i := skipJSONWhitespace(b[:objEnd], objStart+1)
	if i == objEnd-1 {
		return true
	}
	s := jsonView(b)
	for {
		if i >= objEnd-1 || b[i] != '"' {
			return false
		}
		memberStart := i
		keyEnd, esc, ok := jsonSkipString(s, i)
		if !ok || esc || keyEnd >= objEnd {
			return false
		}
		key := s[i+1 : keyEnd-1]
		i = skipJSONWhitespace(b[:objEnd], keyEnd)
		if i >= objEnd-1 || b[i] != ':' {
			return false
		}
		i = skipJSONWhitespace(b[:objEnd], i+1)
		if i >= objEnd-1 {
			return false
		}
		valStart := i
		valEnd, ok := jsonSkipValueStrict(b, valStart)
		if !ok || valEnd >= objEnd {
			return false
		}
		if !fn(key, valStart, valEnd, memberStart, valEnd) {
			return true
		}
		i = skipJSONWhitespace(b[:objEnd], valEnd)
		if i == objEnd-1 {
			return true
		}
		if i >= objEnd-1 || b[i] != ',' {
			return false
		}
		i = skipJSONWhitespace(b[:objEnd], i+1)
	}
}

// locateJSONInputArraySpan finds the top-level "input" array byte span [start, end)
// in body, reusing RequestBodyTop when available and falling back to gjson when
// keys are escaped or top-level framing is non-standard.
func locateJSONInputArraySpan(c *gin.Context, body []byte) (start, end int, isArray, ok bool) {
	if len(body) == 0 {
		return 0, 0, false, true
	}
	if c != nil {
		top := RequestBodyTop(c, body)
		if top.idx != nil {
			res, handled := top.idx.get("input")
			if handled {
				if !res.Exists() || res.Type != gjson.JSON || len(res.Raw) == 0 || res.Raw[0] != '[' {
					return 0, 0, false, true
				}
				if res.Index < 0 || res.Index+len(res.Raw) > len(body) ||
					unsafe.StringData(res.Raw) != unsafe.SliceData(body[res.Index:]) {
					return 0, 0, false, false
				}
				return res.Index, res.Index + len(res.Raw), true, true
			}
		}
	}
	i := skipJSONWhitespace(body, 0)
	if i >= len(body) {
		return 0, 0, false, true
	}
	if body[i] == '{' {
		endWs := len(body)
		for endWs > i && isJSONSpace(body[endWs-1]) {
			endWs--
		}
		if endWs > i+1 && body[endWs-1] == '}' {
			var foundStart, foundEnd int
			var found bool
			walkOK := jsonForEachMemberSpan(body, i, endWs, func(key string, valStart, valEnd, _, _ int) bool {
				if key == "input" {
					foundStart, foundEnd = valStart, valEnd
					found = true
					return false
				}
				return true
			})
			if walkOK {
				if !found {
					return 0, 0, false, true
				}
				if body[foundStart] != '[' {
					return 0, 0, false, true
				}
				return foundStart, foundEnd, true, true
			}
		}
	}
	input := parseRawJSONView(body).Get("input")
	if !input.IsArray() {
		return 0, 0, false, true
	}
	if input.Index < 0 || input.Index+len(input.Raw) > len(body) ||
		unsafe.StringData(input.Raw) != unsafe.SliceData(body[input.Index:]) {
		return 0, 0, false, false
	}
	return input.Index, input.Index + len(input.Raw), true, true
}

func asciiEqualFoldStr(a, lower string) bool {
	if len(a) != len(lower) {
		return false
	}
	for i := 0; i < len(lower); i++ {
		c := a[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != lower[i] {
			return false
		}
	}
	return true
}

// containsASCIIFoldAdditionalTools checks whether s contains "additional_tools"
// ASCII case-insensitively by anchoring on SIMD IndexByte('_').
func containsASCIIFoldAdditionalTools(s string) bool {
	const prefixLen = len("additional") // 10
	const totalLen = len("additional_tools")
	i := 0
	for i < len(s) {
		k := strings.IndexByte(s[i:], '_')
		if k < 0 {
			return false
		}
		pos := i + k
		if pos >= prefixLen && pos-prefixLen+totalLen <= len(s) {
			if asciiEqualFoldStr(s[pos-prefixLen:pos-prefixLen+totalLen], "additional_tools") {
				return true
			}
		}
		i = pos + 1
	}
	return false
}

// extractJSONMemberString reads a raw JSON value span the way gjson.Result.String()
// does, returning an independent (body-detached) string when clone is true.
func extractJSONMemberString(raw string, clone bool) string {
	if raw == "" || raw == "null" {
		return ""
	}
	if raw[0] == '"' && len(raw) >= 2 && raw[len(raw)-1] == '"' && strings.IndexByte(raw[1:len(raw)-1], '\\') < 0 {
		s := raw[1 : len(raw)-1]
		if clone {
			return strings.Clone(s)
		}
		return s
	}
	s := gjson.Parse(raw).String()
	if clone {
		return strings.Clone(s)
	}
	return s
}
