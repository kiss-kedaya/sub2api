package service

import (
	"fmt"
	"strconv"
	"unsafe"

	"github.com/tidwall/gjson"
)

// gatewayTopLevelScan is the single-pass top-level scan of a request body.
//
// Before this, parseGatewayRequestCurrentBody ran gjson.ValidBytes plus nine
// gjson.Get calls plus three range lookups against the same body; every gjson
// lookup rescans from the start, and an online profile showed
// gjson.parseSquash at 27% of CPU. This walks the byte stream once and grabs
// every hot-path scalar and raw range in that pass.
//
// Semantics match the gjson version it replaces:
//   - field lookup is case sensitive (gjson.Get default);
//   - duplicate keys keep the first occurrence (gjson.Get returns on the first match);
//   - the model duplicate check counts case-insensitive keys separately,
//     matching the previous hasDuplicateTopLevelKey behaviour;
//   - ranges are absolute byte offsets: start points at the first byte of the
//     value, end is one past it.
type gatewayTopLevelScan struct {
	modelRaw  []byte
	modelType byte
	modelSet  bool
	// modelKeyCount counts top-level keys matching "model" case-insensitively.
	modelKeyCount int

	streamRaw []byte
	streamSet bool

	metadataRaw     []byte
	thinkingRaw     []byte
	outputConfigRaw []byte
	speedRaw        []byte
	maxTokensRaw    []byte

	systemRaw      jsonRange
	hasSystem      bool
	messagesRaw    jsonRange
	inputRaw       jsonRange
	geminiPartsRaw jsonRange
	contentsRaw    jsonRange
}

// scanGatewayRequestTopLevel parses the top-level object once and collects the
// gateway hot-path fields. A non-object top level returns an empty scan, which
// matches gjson.Get finding nothing.
func scanGatewayRequestTopLevel(body []byte) (gatewayTopLevelScan, error) {
	var scan gatewayTopLevelScan
	scan.systemRaw = missingJSONRange()
	scan.messagesRaw = missingJSONRange()
	scan.inputRaw = missingJSONRange()
	scan.geminiPartsRaw = missingJSONRange()
	scan.contentsRaw = missingJSONRange()

	i := skipJSONWhitespace(body, 0)
	if i >= len(body) {
		return scan, fmt.Errorf("empty request body")
	}
	if body[i] != '{' {
		return scan, nil
	}
	i++

	for {
		i = skipJSONWhitespace(body, i)
		if i >= len(body) {
			return scan, fmt.Errorf("unterminated top-level object")
		}
		if body[i] == '}' {
			return scan, nil
		}
		if body[i] != '"' {
			return scan, fmt.Errorf("invalid top-level key at byte %d", i)
		}

		keyStart := i
		keyEnd, err := scanJSONStringEnd(body, i)
		if err != nil {
			return scan, err
		}
		key := body[keyStart+1 : keyEnd-1]
		keyEscaped := stringHasBackslash(key)
		i = skipJSONWhitespace(body, keyEnd)
		if i >= len(body) || body[i] != ':' {
			return scan, fmt.Errorf("missing colon at byte %d", i)
		}
		i = skipJSONWhitespace(body, i+1)
		if i >= len(body) {
			return scan, fmt.Errorf("missing value at byte %d", i)
		}

		valueStart := i
		valueEnd, err := scanJSONValueEnd(body, i)
		if err != nil {
			return scan, err
		}
		raw := body[valueStart:valueEnd]
		valueKind := body[valueStart]

		// The duplicate check is case-insensitive and independent of lookup.
		if keyEqualFold(key, keyEscaped, "model") {
			scan.modelKeyCount++
		}

		// Every lookup below is case sensitive and keeps the first hit.
		if !scan.modelSet && keyExact(key, keyEscaped, "model") {
			scan.modelSet = true
			scan.modelRaw = raw
			scan.modelType = valueKind
		}
		if !scan.streamSet && keyExact(key, keyEscaped, "stream") {
			scan.streamSet = true
			scan.streamRaw = raw
		}
		if scan.metadataRaw == nil && keyExact(key, keyEscaped, "metadata") {
			scan.metadataRaw = raw
		}
		if scan.thinkingRaw == nil && keyExact(key, keyEscaped, "thinking") {
			scan.thinkingRaw = raw
		}
		if scan.outputConfigRaw == nil && keyExact(key, keyEscaped, "output_config") {
			scan.outputConfigRaw = raw
		}
		if scan.speedRaw == nil && keyExact(key, keyEscaped, "speed") {
			scan.speedRaw = raw
		}
		if scan.maxTokensRaw == nil && keyExact(key, keyEscaped, "max_tokens") {
			scan.maxTokensRaw = raw
		}
		if !scan.geminiPartsRaw.exists() && keyExact(key, keyEscaped, "systemInstruction") {
			// Gemini: range of systemInstruction.parts (one small level down).
			if parts, ok := directJSONField(body, valueStart, valueEnd, "parts"); ok && body[parts.start] == '[' {
				scan.geminiPartsRaw = parts
			}
		}
		if !scan.hasSystem && keyExact(key, keyEscaped, "system") {
			scan.hasSystem = true
			scan.systemRaw = jsonRange{start: valueStart, end: valueEnd, kind: jsonKindFromByte(valueKind)}
		}
		if !scan.messagesRaw.exists() && keyExact(key, keyEscaped, "messages") && valueKind == '[' {
			scan.messagesRaw = jsonRange{start: valueStart, end: valueEnd, kind: gjson.JSON}
		}
		if !scan.inputRaw.exists() && keyExact(key, keyEscaped, "input") {
			scan.inputRaw = jsonRange{start: valueStart, end: valueEnd, kind: jsonKindFromByte(valueKind)}
		}
		if !scan.contentsRaw.exists() && keyExact(key, keyEscaped, "contents") && valueKind == '[' {
			scan.contentsRaw = jsonRange{start: valueStart, end: valueEnd, kind: gjson.JSON}
		}

		i = skipJSONWhitespace(body, valueEnd)
		if i >= len(body) {
			return scan, fmt.Errorf("unterminated top-level object")
		}
		if body[i] == ',' {
			i++
			continue
		}
		if body[i] == '}' {
			return scan, nil
		}
		return scan, fmt.Errorf("unexpected byte %q at %d", body[i], i)
	}
}

// keyExact compares a key name exactly, matching gjson.Get case sensitivity.
func keyExact(key []byte, escaped bool, want string) bool {
	if escaped {
		// gjson unescapes keys before matching ("\u006dodel" == "model").
		return decodeJSONKey(key) == want
	}
	if len(key) != len(want) {
		return false
	}
	for i := 0; i < len(key); i++ {
		if key[i] != want[i] {
			return false
		}
	}
	return true
}

// keyEqualFold compares a key name ASCII case-insensitively, matching the
// previous hasDuplicateTopLevelKey behaviour.
func keyEqualFold(key []byte, escaped bool, want string) bool {
	if escaped {
		return equalFoldASCII(decodeJSONKey(key), want)
	}
	if len(key) != len(want) {
		return false
	}
	for i := 0; i < len(key); i++ {
		x, y := key[i], want[i]
		if x == y {
			continue
		}
		if x|0x20 == y|0x20 && y >= 'a' && y <= 'z' {
			continue
		}
		return false
	}
	return true
}

func decodeJSONKey(key []byte) string {
	return gjson.Parse(`"` + string(key) + `"`).String()
}

func equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		x, y := a[i], b[i]
		if x == y {
			continue
		}
		if x|0x20 == y|0x20 && x|0x20 >= 'a' && x|0x20 <= 'z' {
			continue
		}
		return false
	}
	return true
}

func stringHasBackslash(b []byte) bool {
	for _, c := range b {
		if c == '\\' {
			return true
		}
	}
	return false
}

func skipJSONWhitespace(body []byte, i int) int {
	for i < len(body) {
		switch body[i] {
		case ' ', '\t', '\n', '\r':
			i++
		default:
			return i
		}
	}
	return i
}

// scanJSONStringEnd returns the byte offset just past a string literal.
func scanJSONStringEnd(body []byte, i int) (int, error) {
	for j := i + 1; j < len(body); j++ {
		switch body[j] {
		case '\\':
			j++
		case '"':
			return j + 1, nil
		}
	}
	return 0, fmt.Errorf("unterminated string at byte %d", i)
}

// scanJSONValueEnd returns the byte offset just past any JSON value.
func scanJSONValueEnd(body []byte, i int) (int, error) {
	switch body[i] {
	case '"':
		return scanJSONStringEnd(body, i)
	case '{', '[':
		return scanJSONCompositeEnd(body, i)
	default:
		j := i
		for j < len(body) {
			switch body[j] {
			case ',', '}', ']', ' ', '\t', '\n', '\r':
				return j, nil
			}
			j++
		}
		return j, nil
	}
}

// scanJSONCompositeEnd skips a nested array/object, including brackets that
// appear inside strings.
func scanJSONCompositeEnd(body []byte, i int) (int, error) {
	depth := 0
	for j := i; j < len(body); j++ {
		switch body[j] {
		case '"':
			end, err := scanJSONStringEnd(body, j)
			if err != nil {
				return 0, err
			}
			j = end - 1
		case '{', '[':
			depth++
		case '}', ']':
			depth--
			if depth == 0 {
				return j + 1, nil
			}
			if depth < 0 {
				return 0, fmt.Errorf("unbalanced bracket at byte %d", j)
			}
		}
	}
	return 0, fmt.Errorf("unterminated composite value at byte %d", i)
}

func jsonKindFromByte(b byte) gjson.Type {
	switch b {
	case '"':
		return gjson.String
	case '{', '[':
		return gjson.JSON
	case 't':
		return gjson.True
	case 'f':
		return gjson.False
	case 'n':
		return gjson.Null
	default:
		return gjson.Number
	}
}

// directJSONField returns the range of a single-level field inside a small
// object such as systemInstruction, avoiding a full rescan for it.
func directJSONField(body []byte, start, end int, want string) (jsonRange, bool) {
	if start >= end || body[start] != '{' {
		return missingJSONRange(), false
	}
	i := start + 1
	for {
		i = skipJSONWhitespace(body, i)
		if i >= end || body[i] == '}' {
			return missingJSONRange(), false
		}
		if body[i] != '"' {
			return missingJSONRange(), false
		}
		keyStart := i
		keyEnd, err := scanJSONStringEnd(body, i)
		if err != nil {
			return missingJSONRange(), false
		}
		key := body[keyStart+1 : keyEnd-1]
		escaped := stringHasBackslash(key)
		i = skipJSONWhitespace(body, keyEnd)
		if i >= end || body[i] != ':' {
			return missingJSONRange(), false
		}
		i = skipJSONWhitespace(body, i+1)
		if i >= end {
			return missingJSONRange(), false
		}
		valueStart := i
		valueEnd, err := scanJSONValueEnd(body, i)
		if err != nil {
			return missingJSONRange(), false
		}
		if keyExact(key, escaped, want) {
			return jsonRange{start: valueStart, end: valueEnd, kind: jsonKindFromByte(body[valueStart])}, true
		}
		i = skipJSONWhitespace(body, valueEnd)
		if i >= end {
			return missingJSONRange(), false
		}
		if body[i] == ',' {
			i++
			continue
		}
		return missingJSONRange(), false
	}
}

// rawJSONString decodes a JSON string literal (quotes included). It is
// zero-copy when the string has no escapes.
func rawJSONString(raw []byte) string {
	if len(raw) < 2 || raw[0] != '"' {
		return ""
	}
	inner := raw[1 : len(raw)-1]
	if !stringHasBackslash(inner) {
		return *(*string)(unsafe.Pointer(&inner))
	}
	return gjson.Parse(*(*string)(unsafe.Pointer(&raw))).String()
}

// rawJSONScalarString mirrors gjson's String(): non-string literals return
// their raw text. Only used for small scalars such as speed.
func rawJSONScalarString(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	if len(raw) >= 2 && raw[0] == '"' {
		return rawJSONString(raw)
	}
	return gjson.Parse(*(*string)(unsafe.Pointer(&raw))).String()
}

// rawJSONBool reports whether the literal is true.
func rawJSONBool(raw []byte) bool {
	return len(raw) == 4 && raw[0] == 't' && raw[1] == 'r' && raw[2] == 'u' && raw[3] == 'e'
}

// rawJSONFalse reports whether the literal is false.
func rawJSONFalse(raw []byte) bool {
	return len(raw) == 5 && raw[0] == 'f' && raw[1] == 'a' && raw[2] == 'l' && raw[3] == 's' && raw[4] == 'e'
}

// rawJSONNumber parses a number literal, mirroring gjson's Number type check
// so quoted strings never count as numbers.
func rawJSONNumber(raw []byte) (float64, bool) {
	if len(raw) == 0 || len(raw) > 64 {
		return 0, false
	}
	switch raw[0] {
	case '+', '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
	default:
		return 0, false
	}
	f, err := strconv.ParseFloat(*(*string)(unsafe.Pointer(&raw)), 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

// rawJSONSubString reads a single-level field from a small object
// (metadata/thinking/output_config) with gjson.String() semantics.
func rawJSONSubString(raw []byte, path string) string {
	if len(raw) == 0 {
		return ""
	}
	return gjson.GetBytes(raw, path).String()
}
