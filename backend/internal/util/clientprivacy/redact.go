// Package clientprivacy removes private upstream diagnostics at client-facing
// error boundaries. It must never be applied to successful model/tool content.
package clientprivacy

import (
	"bytes"
	"encoding/json"
	"html"
	"io"
	"net"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
)

const (
	redacted      = "[redacted]"
	maxDepth      = 32
	maxInputBytes = 2 << 20
	fallbackJSON  = `{"error":{"type":"upstream_error","message":"Upstream error details redacted"}}`
)

var (
	privateKeyPattern        = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?(?:-----END [A-Z0-9 ]*PRIVATE KEY-----|$)`)
	urlPattern               = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s<>"'\\]+`)
	credentialPattern        = regexp.MustCompile(`(?i)\b(?:bearer|basic)\s+[a-z0-9_./+=:-]+`)
	tokenPattern             = regexp.MustCompile(`\b(?:(?:sk|rk|pk|ghp|gho|github_pat)[-_][A-Za-z0-9_-]{12,}|AIza[A-Za-z0-9_-]{30,}|GOCSPX-[A-Za-z0-9_-]{20,}|eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+)\b`)
	emailPattern             = regexp.MustCompile(`(?i)\b[a-z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]+@[a-z0-9.-]+\.[a-z]{2,63}\b`)
	domainPattern            = regexp.MustCompile(`(?i)\b(?:[a-z0-9_](?:[a-z0-9_-]{0,61}[a-z0-9_])?\.)+[a-z_]{2,63}\b(?::[0-9]{1,5})?`)
	ipv4Pattern              = regexp.MustCompile(`\b(?:[0-9]{1,3}\.){3}[0-9]{1,3}\b(?::[0-9]{1,5})?`)
	ipv6Pattern              = regexp.MustCompile(`[0-9A-Fa-f:.]*:[0-9A-Fa-f:.]+(?:%[0-9A-Za-z_.-]+)?`)
	assignmentPattern        = regexp.MustCompile(`(?i)(\b(?:api[_-]?key|access[_-]?token|refresh[_-]?token|id[_-]?token|client[_-]?secret|secret|password|passwd|authorization|cookie|credential(?:s)?|private[_-]?key|mnemonic|seed|account(?:[_-]?(?:id|name|email))?|upstream(?:[_-]?(?:url|host|endpoint|account|name))?|proxy(?:[_-]?(?:url|host|name))?|base[_-]?url|endpoint|hostname|server[_-]?ip|token|key)\b["']?\s*[:=]\s*)(?:"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|[^\r\n,;}&<>]+)`)
	unicodeEscapePattern     = regexp.MustCompile(`\\u[0-9a-fA-F]{4}`)
	credentialContextPattern = regexp.MustCompile(`(?i)(\b(?:(?:incorrect|invalid|revoked|expired)\s+(?:api[ _-]?key|access[ _-]?token|token|credential|authorization)(?:\s+(?:provided|supplied|used|received))?|(?:api[ _-]?key|access[ _-]?token|token|credential)\s+(?:provided|supplied|used|received))\s*[:=]\s*)(?:"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|[^\s,;<>]+)`)
	accountContextPattern    = regexp.MustCompile(`(?i)\b(?:account|proxy)\s+(?:id\s*[:=#]?\s*)?#?[0-9]+(?:\s*\([^\r\n)]*\))?`)
)

// Text sanitizes free-form error text without altering safe text or whitespace.
func Text(input string) string {
	if len(input) > maxInputBytes {
		return redacted
	}
	// Diagnostics can contain HTML entities or JSON escapes even when they are
	// embedded in a larger text message. Decode for detection, but preserve the
	// original encoding when it contained no sensitive content.
	decoded, ok := decodeDiagnosticText(input)
	if !ok {
		return redacted
	}
	out := privateKeyPattern.ReplaceAllString(decoded, redacted)
	out = urlPattern.ReplaceAllString(out, redacted)
	out = credentialPattern.ReplaceAllString(out, redacted)
	out = credentialContextPattern.ReplaceAllString(out, `${1}`+redacted)
	out = accountContextPattern.ReplaceAllString(out, redacted)
	out = tokenPattern.ReplaceAllString(out, redacted)
	out = emailPattern.ReplaceAllString(out, redacted)
	// IPv6 runs first so IPv4-mapped IPv6 addresses are removed as a whole.
	out = ipv6Pattern.ReplaceAllStringFunc(out, func(value string) string {
		if _, err := netip.ParseAddr(value); err == nil {
			return redacted
		}
		return value
	})
	out = ipv4Pattern.ReplaceAllStringFunc(out, func(value string) string {
		address := value
		if host, _, err := net.SplitHostPort(value); err == nil {
			address = host
		}
		if _, err := netip.ParseAddr(address); err == nil {
			return redacted
		}
		return value
	})
	out = domainPattern.ReplaceAllStringFunc(out, func(value string) string {
		if safeProtocolPath(value) {
			return value
		}
		return redacted
	})
	out = assignmentPattern.ReplaceAllString(out, `${1}`+redacted)
	if out == decoded {
		return input
	}
	return out
}

func decodeDiagnosticText(input string) (string, bool) {
	decoded := input
	for depth := 0; depth <= maxDepth; depth++ {
		next := html.UnescapeString(strings.ReplaceAll(decoded, `\/`, "/"))
		next = unicodeEscapePattern.ReplaceAllStringFunc(next, func(value string) string {
			if unquoted, err := strconv.Unquote(`"` + value + `"`); err == nil {
				return unquoted
			}
			return value
		})
		if next == decoded {
			return decoded, true
		}
		decoded = next
	}
	return "", false
}

// safeProtocolPath distinguishes documented event names and parameter paths
// from hostnames. Arbitrary dotted strings are deliberately not exempted.
func safeProtocolPath(value string) bool {
	for _, known := range []string{
		"response.failed", "response.completed", "response.created", "response.in_progress", "response.incomplete",
		"response.output_text.delta", "response.function_call_arguments.delta", "response.error.message",
		"error.message", "error.type", "error.code", "message.start", "message.stop",
		"io.EOF", "net.Error", "url.Error", "http.Client", "context.Canceled", "context.DeadlineExceeded",
	} {
		if value == known {
			return true
		}
	}
	parts := strings.Split(value, ".")
	if len(parts) < 2 {
		return false
	}
	if parts[0] == "response" && len(parts) >= 3 {
		suffix := strings.Join(parts[2:], ".")
		switch parts[1] {
		case "output_item", "content_part", "reasoning_summary_part":
			return suffix == "added" || suffix == "done"
		case "output_text":
			return suffix == "delta" || suffix == "done" || suffix == "annotation.added"
		case "refusal", "function_call_arguments", "reasoning_text", "reasoning_summary_text":
			return suffix == "delta" || suffix == "done"
		case "web_search_call", "file_search_call":
			return suffix == "in_progress" || suffix == "searching" || suffix == "completed"
		case "image_generation_call":
			return suffix == "in_progress" || suffix == "generating" || suffix == "partial_image" || suffix == "completed"
		case "error":
			return suffix == "message" || suffix == "type" || suffix == "code" || suffix == "param"
		}
	}
	switch parts[0] {
	case "messages", "tools", "input", "output", "choices", "content", "generation_config", "generationConfig", "response_format", "cache_control", "reasoning":
	default:
		return false
	}
	for index, part := range parts[1:] {
		switch part {
		case "content", "role", "type", "name", "arguments", "function", "parameters", "properties", "text", "input", "output", "items", "schema", "strict", "json_schema", "ttl", "effort", "temperature", "max_output_tokens", "response_mime_type", "response_schema", "top_p", "top_k", "thinking_config":
			continue
		}
		if index > 0 && parts[index] == "properties" && part != "" && !strings.ContainsAny(part, ":/@") {
			continue
		}
		if part == "" {
			return false
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

func privateField(key string) bool {
	key = strings.ToLower(strings.Map(func(r rune) rune {
		if r == '_' || r == '-' || r == ' ' {
			return -1
		}
		return r
	}, key))
	// Provider-specific aliases must not bypass the same boundary. Keep only
	// correlation/status fields under the upstream namespace.
	if strings.HasPrefix(key, "upstream") {
		switch key {
		case "upstreamstatus", "upstreamstatuscode", "upstreamrequestid", "upstreamerrorcode":
			return false
		default:
			return true
		}
	}
	if strings.HasPrefix(key, "proxy") || strings.HasPrefix(key, "account") {
		return true
	}
	for _, suffix := range []string{"apikey", "accesstoken", "refreshtoken", "clientsecret", "password", "privatekey"} {
		if strings.HasSuffix(key, suffix) {
			return true
		}
	}
	switch key {
	case "key", "apikey", "token", "accesstoken", "refreshtoken", "idtoken", "secret", "clientsecret", "password", "passwd",
		"authorization", "authorizationcode", "cookie", "setcookie", "credentials", "credential", "privatekey", "mnemonic", "seed",
		"account", "accountid", "accountname", "accountemail", "email", "useremail", "username", "userid",
		"upstream", "upstreamurl", "upstreamhost", "upstreamendpoint", "upstreamaccount", "upstreamaccountid", "upstreamname",
		"proxy", "proxyid", "proxyurl", "proxyhost", "proxyname", "baseurl", "endpoint", "url", "host", "hostname", "ip", "serverip",
		"headers", "requestheaders", "responseheaders", "stack", "stacktrace", "traceback", "debug", "debuginfo",
		"raw", "rawbody", "requestbody", "responsebody", "tenant", "tenantid", "organization", "organizationid":
		return true
	}
	return false
}

// JSON sanitizes an error document while preserving safe documents byte for
// byte. A token walk visits duplicate object keys too, so an overwritten secret
// cannot survive in the original bytes returned by the unchanged fast path.
// Malformed JSON is treated as free-form diagnostic text.
func JSON(input []byte) []byte {
	if len(input) > maxInputBytes {
		return []byte(fallbackJSON)
	}
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	value, changed, err := readJSONValue(decoder, 0)
	if err != nil {
		// JSON-valid but excessively nested diagnostics fail closed.
		trimmed := bytes.TrimSpace(input)
		if json.Valid(input) || len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
			return []byte(fallbackJSON)
		}
		return []byte(Text(string(input)))
	}
	if _, err := decoder.Token(); err != io.EOF {
		return []byte(fallbackJSON)
	}
	if !changed {
		return input
	}
	result, err := json.Marshal(value)
	if err != nil {
		return []byte(fallbackJSON)
	}
	return result
}

func readJSONValue(decoder *json.Decoder, depth int) (any, bool, error) {
	if depth > maxDepth {
		return nil, true, errTooDeep
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, false, err
	}
	switch value := token.(type) {
	case string:
		safe := Text(value)
		return safe, safe != value, nil
	case json.Delim:
		switch value {
		case '{':
			result := make(map[string]any)
			changed := false
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return nil, changed, err
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, changed, errInvalidJSON
				}
				item, itemChanged, err := readJSONValue(decoder, depth+1)
				if err != nil {
					return nil, changed, err
				}
				if privateField(key) {
					item, itemChanged = redacted, true
				}
				safeKey := Text(key)
				changed = changed || itemChanged || safeKey != key
				result[safeKey] = item
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim('}') {
				return nil, changed, errInvalidJSON
			}
			return result, changed, nil
		case '[':
			result := make([]any, 0)
			changed := false
			for decoder.More() {
				item, itemChanged, err := readJSONValue(decoder, depth+1)
				if err != nil {
					return nil, changed, err
				}
				changed = changed || itemChanged
				result = append(result, item)
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim(']') {
				return nil, changed, errInvalidJSON
			}
			return result, changed, nil
		}
		return nil, false, errInvalidJSON
	default:
		return token, false, nil
	}
}

type parseError string

func (e parseError) Error() string { return string(e) }

const errTooDeep parseError = "error payload exceeds privacy depth limit"
const errInvalidJSON parseError = "invalid error JSON payload"

// SSE sanitizes only error events in a mixed stream. Successful content,
// including generated URLs and search results, remains byte-for-byte intact.
func SSE(input string) string {
	return sanitizeSSE(input, false)
}

// ErrorSSE sanitizes an already identified error stream/event. Callers handling
// an HTTP non-2xx body or a protocol-confirmed error may use this variant when
// its payload does not itself contain a named error event.
func ErrorSSE(input string) string {
	return sanitizeSSE(input, true)
}

func sanitizeSSE(input string, knownError bool) string {
	if len(input) > maxInputBytes && knownError {
		return "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"upstream_error\",\"message\":\"Upstream error details redacted\"}}\n\n"
	}
	var output strings.Builder
	var event []string
	for _, line := range strings.SplitAfter(input, "\n") {
		event = append(event, line)
		if strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r") == "" && line != "" {
			_, _ = output.WriteString(sanitizeEvent(event, knownError))
			event = event[:0]
		}
	}
	if len(event) > 0 {
		_, _ = output.WriteString(sanitizeEvent(event, knownError))
	}
	return output.String()
}

func sanitizeEvent(lines []string, knownError bool) string {
	var data []string
	for _, line := range lines {
		content := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if value, ok := strings.CutPrefix(content, "data:"); ok {
			data = append(data, strings.TrimPrefix(value, " "))
		}
		if value, ok := strings.CutPrefix(content, "event:"); ok {
			switch strings.TrimSpace(value) {
			case "error", "response.failed":
				knownError = true
			}
		}
	}
	if !knownError {
		payload := strings.Join(data, "\n")
		if len(data) == 0 {
			payload = strings.TrimSpace(strings.Join(lines, ""))
		}
		var envelope map[string]json.RawMessage
		if json.Unmarshal([]byte(payload), &envelope) == nil {
			var eventType, status string
			_ = json.Unmarshal(envelope["type"], &eventType)
			_ = json.Unmarshal(envelope["status"], &status)
			errorData := bytes.TrimSpace(envelope["error"])
			knownError = eventType == "error" || eventType == "response.failed" || status == "failed" || len(errorData) > 0 && !bytes.Equal(errorData, []byte("null"))
		}
		if !knownError {
			return strings.Join(lines, "")
		}
	}
	if len(data) == 0 {
		joined := strings.Join(lines, "")
		trimmed := strings.TrimSpace(joined)
		safe := string(JSON([]byte(trimmed)))
		if safe != trimmed {
			return strings.Replace(joined, trimmed, safe, 1)
		}
		return joined
	}
	originalData := strings.Join(data, "\n")
	safeData := string(JSON([]byte(originalData)))
	var output strings.Builder
	wroteData := false
	for _, line := range lines {
		content := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if strings.HasPrefix(content, "data:") && safeData != originalData {
			if !wroteData {
				newline := strings.TrimPrefix(line, content)
				if newline == "" && len(data) > 1 {
					newline = "\n"
				}
				// Malformed data can contain real newlines; each remains an SSE
				// data line rather than becoming an injected event/header.
				_, _ = output.WriteString("data: " + strings.ReplaceAll(safeData, "\n", "\ndata: ") + newline)
				wroteData = true
			}
			continue
		}
		if strings.HasPrefix(content, "data:") {
			output.WriteString(line)
		} else {
			output.WriteString(Text(line))
		}
	}
	return output.String()
}
