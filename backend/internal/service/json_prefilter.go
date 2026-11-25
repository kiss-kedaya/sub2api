package service

import (
	"bytes"
	"strings"
)

// Conservative gates for the per-line stream observers. gjson and
// encoding/json compare a key with the text between its quotes after resolving
// escapes, and a string only decodes to letters by spelling them out or by a
// \uXXXX escape. So a path whose names do not occur in the raw JSON, in a
// payload without a \u escape, cannot exist: the lookup (a scan of the whole
// line) may be skipped without changing its answer. Whenever the gate cannot
// tell, it says "may exist" and the caller does the lookup as before.

const jsonPathSyntaxChars = `*?#@|\!<>=%[]{}()":,`

// jsonPathMayExist reports whether gjson.Get(raw, path) could find a value.
func jsonPathMayExist(raw, path string) bool {
	if strings.ContainsAny(path, jsonPathSyntaxChars) {
		return true
	}
	escapes := -1
	for rest := path; rest != ""; {
		part, next, _ := strings.Cut(rest, ".")
		rest = next
		if part == "" || isDecimalIndex(part) || strings.Contains(raw, part) {
			continue
		}
		if escapes < 0 {
			escapes = 0
			if strings.Contains(raw, `\u`) {
				escapes = 1
			}
		}
		if escapes == 0 {
			return false
		}
	}
	return true
}

// jsonPathMayExistBytes is jsonPathMayExist for a byte payload.
func jsonPathMayExistBytes(raw []byte, path string) bool {
	if strings.ContainsAny(path, jsonPathSyntaxChars) {
		return true
	}
	escapes := -1
	for rest := path; rest != ""; {
		part, next, _ := strings.Cut(rest, ".")
		rest = next
		if part == "" || isDecimalIndex(part) || bytes.Contains(raw, []byte(part)) {
			continue
		}
		if escapes < 0 {
			escapes = 0
			if bytes.Contains(raw, []byte(`\u`)) {
				escapes = 1
			}
		}
		if escapes == 0 {
			return false
		}
	}
	return true
}

func isDecimalIndex(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// jsonMayContainText reports whether raw could hold a key or string value
// spelled text: the text occurs, or a \u escape could spell it.
func jsonMayContainText(raw, text string) bool {
	return strings.Contains(raw, text) || strings.Contains(raw, `\u`)
}
