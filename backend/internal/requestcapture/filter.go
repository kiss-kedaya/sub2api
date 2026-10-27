package requestcapture

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"hash"
	"net/http"
	"net/url"
	"strings"
)

func sensitive(k string) bool {
	k = strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(k, "-", ""), "_", ""))
	switch k {
	case "authorization", "proxyauthorization", "cookie", "setcookie", "apikey", "xapikey", "xgoogapikey", "accesstoken", "refreshtoken", "idtoken", "token", "clientsecret", "secret", "password", "credentials", "privatekey", "signature", "sig", "key":
		return true
	}
	return strings.Contains(k, "credential") || strings.HasSuffix(k, "secret") || strings.HasSuffix(k, "token") || strings.HasSuffix(k, "accesskey") || strings.HasSuffix(k, "apikey")
}

func SafeURL(raw string) string {
	if len(raw) > 16384 {
		return "[URL omitted: too long]"
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "[invalid URL omitted]"
	}
	u.User = nil
	u.Fragment = ""
	q := u.Query()
	for k := range q {
		if sensitive(k) || strings.Contains(strings.ToLower(k), "signature") || strings.HasPrefix(strings.ToLower(k), "x-amz-") || strings.HasPrefix(strings.ToLower(k), "x-goog-") {
			q.Set(k, "[REDACTED]")
		}
	}
	u.RawQuery = q.Encode()
	if i := strings.Index(u.Path, "/api/bps-images/"); i >= 0 {
		u.Path = u.Path[:i] + "/api/bps-images/[REDACTED]"
		u.RawPath = ""
	}
	return u.String()
}

// Only diagnostic, bounded headers are retained. Unknown headers are excluded.
func SafeHeaders(h http.Header) map[string]string {
	out := map[string]string{}
	for _, k := range []string{"Content-Type", "Content-Encoding", "Content-Length", "X-Request-Id", "Request-Id", "X-Client-Request-Id", "Openai-Version", "Anthropic-Version", "Anthropic-Beta", "Retry-After"} {
		if v := h.Get(k); v != "" {
			if len(v) > 256 {
				v = v[:256]
			}
			out[k] = v
		}
	}
	return out
}

type containerState struct{ object, key, media bool }

// jsonFilter never buffers an entire string. Escapes and redaction survive chunks.
type jsonFilter struct {
	media                                           bool
	stack                                           []containerState
	started, invalid, quoted, escaped, isKey        bool
	key                                             string
	prefix                                          []byte
	mode                                            string
	digest                                          hash.Hash
	one                                             [1]byte
	validator                                       jsonValidator
	count                                           int64
	suppressDepth                                   int
	suppressString, suppressEscape, suppressLiteral bool
	redacted, omitted                               bool
}

func (f *jsonFilter) valueMode() string {
	if sensitive(f.key) {
		return "secret"
	}
	if !f.media {
		switch strings.ToLower(f.key) {
		case "b64_json", "file_data", "image_base64", "audio_data":
			return "media"
		}
	}
	if !f.media && strings.EqualFold(f.key, "data") && len(f.stack) > 0 && f.stack[len(f.stack)-1].media {
		return "media"
	}
	return "prefix"
}
func (f *jsonFilter) stringEnd(out *bytes.Buffer) {
	if f.isKey {
		var k string
		if json.Unmarshal(append(append([]byte{'"'}, f.prefix...), '"'), &k) != nil {
			f.invalid = true
			return
		}
		f.key = k
		_ = out.WriteByte('"')
		return
	}
	switch f.mode {
	case "secret":
		_, _ = out.WriteString("\"[REDACTED]\"")
		f.redacted = true
	case "media":
		v, _ := json.Marshal(map[string]any{"omitted": "media", "encoded_bytes": f.count, "sha256": hex.EncodeToString(f.digest.Sum(nil)), "digest_encoding": "json_string_bytes"})
		_, _ = out.Write(v)
		f.omitted = true
	case "url":
		var raw string
		if json.Unmarshal(append(append([]byte{'"'}, f.prefix...), '"'), &raw) != nil {
			_, _ = out.WriteString("\"[invalid URL omitted]\"")
			f.omitted = true
		} else {
			b, _ := json.Marshal(SafeURL(raw))
			_, _ = out.Write(b)
		}
	case "prefix":
		_ = out.WriteByte('"')
		_, _ = out.Write(f.prefix)
		_ = out.WriteByte('"')
	default:
		_ = out.WriteByte('"')
	}
}
func (f *jsonFilter) Write(p []byte) []byte {
	var out bytes.Buffer
	for _, b := range p {
		if f.invalid {
			continue
		}
		if !f.validator.accept(b) {
			f.invalid = true
			continue
		}
		if f.suppressDepth > 0 || f.suppressLiteral {
			if f.suppressString {
				if f.suppressEscape {
					f.suppressEscape = false
				} else if b == '\\' {
					f.suppressEscape = true
				} else if b == '"' {
					f.suppressString = false
				}
				continue
			}
			if f.suppressDepth > 0 {
				switch b {
				case '"':
					f.suppressString = true
				case '{', '[':
					f.suppressDepth++
				case '}', ']':
					f.suppressDepth--
				}
				continue
			}
			if b != ',' && b != '}' && b != ']' && b != ' ' && b != '\n' && b != '\r' && b != '\t' {
				continue
			}
			f.suppressLiteral = false
		}
		if f.quoted {
			closing := b == '"' && !f.escaped
			if closing {
				f.quoted = false
				f.stringEnd(&out)
				f.prefix = nil
				f.digest = nil
				continue
			}
			if f.escaped {
				f.escaped = false
			} else if b == '\\' {
				f.escaped = true
			}
			f.count++
			if f.isKey {
				if len(f.prefix) >= 256 {
					f.invalid = true
					f.omitted = true
					continue
				}
				f.prefix = append(f.prefix, b)
				_ = out.WriteByte(b)
				continue
			}
			switch f.mode {
			case "secret":
				continue
			case "media":
				f.one[0] = b
				_, _ = f.digest.Write(f.one[:])
			case "url":
				if len(f.prefix) >= 16384 {
					f.invalid = true
					f.omitted = true
					continue
				}
				f.prefix = append(f.prefix, b)
			case "prefix":
				f.prefix = append(f.prefix, b)
				var prefix string
				if json.Unmarshal(append(append([]byte{'"'}, f.prefix...), '"'), &prefix) != nil {
					if len(f.prefix) > 128 {
						f.invalid = true
					}
					continue
				}
				if !f.media && strings.HasPrefix(strings.ToLower(prefix), "data:") {
					f.mode = "media"
					f.digest = sha256.New()
					_, _ = f.digest.Write(f.prefix)
					f.prefix = nil
				} else if strings.HasPrefix(strings.ToLower(prefix), "https://") || strings.HasPrefix(strings.ToLower(prefix), "http://") || strings.HasPrefix(strings.ToLower(prefix), "wss://") || strings.HasPrefix(strings.ToLower(prefix), "ws://") {
					f.mode = "url"
				} else if len(prefix) >= 8 {
					f.mode = "text"
					_ = out.WriteByte('"')
					_, _ = out.Write(f.prefix)
					f.prefix = nil
				}
			default:
				_ = out.WriteByte(b)
			}
			continue
		}
		if !f.started {
			if b == ' ' || b == '\n' || b == '\r' || b == '\t' {
				continue
			}
			if b != '{' && b != '[' {
				f.invalid = true
				f.omitted = true
				continue
			}
			f.started = true
		}
		if sensitive(f.key) && b != '"' && b != ':' && b != ' ' && b != '\n' && b != '\r' && b != '\t' && b != ',' && b != '}' && b != ']' {
			_, _ = out.WriteString("\"[REDACTED]\"")
			f.redacted = true
			f.key = ""
			if b == '{' || b == '[' {
				f.suppressDepth = 1
			} else {
				f.suppressLiteral = true
			}
			continue
		}
		switch b {
		case '"':
			f.quoted = true
			f.escaped = false
			f.count = 0
			f.prefix = nil
			f.isKey = len(f.stack) > 0 && f.stack[len(f.stack)-1].object && f.stack[len(f.stack)-1].key
			if f.isKey {
				_ = out.WriteByte(b)
			} else {
				f.mode = f.valueMode()
				if f.mode == "media" {
					f.digest = sha256.New()
				}
			}
		case '{', '[':
			if len(f.stack) >= 128 {
				f.invalid = true
				f.omitted = true
				continue
			}
			media := false
			switch strings.ToLower(f.key) {
			case "source", "input_audio", "audio", "inline_data", "inlinedata", "image", "file":
				media = true
			}
			if len(f.stack) > 0 {
				media = media || f.stack[len(f.stack)-1].media
			}
			f.stack = append(f.stack, containerState{object: b == '{', key: b == '{', media: media})
			f.key = ""
			_ = out.WriteByte(b)
		case '}', ']':
			if len(f.stack) > 0 {
				f.stack = f.stack[:len(f.stack)-1]
			}
			f.key = ""
			_ = out.WriteByte(b)
		case ':':
			if len(f.stack) > 0 {
				f.stack[len(f.stack)-1].key = false
			}
			_ = out.WriteByte(b)
		case ',':
			if len(f.stack) > 0 && f.stack[len(f.stack)-1].object {
				f.stack[len(f.stack)-1].key = true
			}
			f.key = ""
			_ = out.WriteByte(b)
		default:
			_ = out.WriteByte(b)
		}
	}
	return out.Bytes()
}

type bodyFilter struct {
	json             jsonFilter
	framing          bodyFraming
	sse, unsupported bool
	linePrefix       []byte
	lineStarted      bool
	bytes            int64
	digest           hash.Hash
	omitted          bool
	unsupportedSSE   bool
	invalid          bool
	dataPrefix       []byte
	dataReady        bool
	knownMedia       bool
	contentType      string
	binary           bool
	binaryStarted    bool
	carry            []byte
}

func newBodyFilter(contentType string, media bool) *bodyFilter {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	knownMedia := strings.HasPrefix(ct, "image/") || strings.HasPrefix(ct, "audio/") || strings.HasPrefix(ct, "video/") || strings.HasPrefix(ct, "application/octet-stream")
	return &bodyFilter{framing: newBodyFraming(contentType), knownMedia: knownMedia, contentType: bounded(contentType, 256), json: jsonFilter{media: media}, sse: strings.Contains(ct, "text/event-stream"), binary: media && (strings.HasPrefix(ct, "image/") || strings.HasPrefix(ct, "audio/") || strings.HasPrefix(ct, "video/") || strings.HasPrefix(ct, "application/octet-stream")), unsupported: ct != "" && !strings.Contains(ct, "json") && !strings.Contains(ct, "text/event-stream"), digest: sha256.New()}
}
func (f *bodyFilter) Write(p []byte) []byte {
	f.bytes += int64(len(p))
	_, _ = f.digest.Write(p)
	if f.binary {
		var out []byte
		if !f.binaryStarted {
			out = append(out, []byte("{\"encoding\":\"base64\",\"data\":\"")...)
			f.binaryStarted = true
		}
		if len(f.carry) > 0 {
			for len(p) > 0 && len(f.carry) < 3 {
				f.carry = append(f.carry, p[0])
				p = p[1:]
			}
			if len(f.carry) == 3 {
				out = base64.StdEncoding.AppendEncode(out, f.carry)
				f.carry = nil
			}
		}
		count := len(p) / 3 * 3
		out = base64.StdEncoding.AppendEncode(out, p[:count])
		f.carry = append(f.carry, p[count:]...)
		return out
	}
	if f.unsupported {
		return nil
	}
	prefix, rest := f.framing.detect(p)
	f.sse = f.framing.sse
	if len(prefix) == 0 {
		return f.writeText(rest)
	}
	return append(f.writeText(prefix), f.writeText(rest)...)
}

func (f *bodyFilter) writeText(p []byte) []byte {
	if !f.sse {
		return f.json.Write(p)
	}
	var out bytes.Buffer
	for len(p) > 0 {
		if !f.lineStarted {
			b := p[0]
			p = p[1:]
			if b == '\n' {
				prefix := strings.TrimSpace(string(f.linePrefix))
				if prefix == "" {
					_ = out.WriteByte('\n')
					f.omitted = f.omitted || f.json.omitted
					f.invalid = f.invalid || (f.json.started && !f.json.validator.complete()) || f.json.invalid
					f.json = jsonFilter{media: f.json.media}
				} else if strings.HasPrefix(prefix, "event:") || strings.HasPrefix(prefix, "id:") || strings.HasPrefix(prefix, "retry:") {
					field, v, _ := strings.Cut(prefix, ":")
					v = strings.TrimSpace(v)
					if safeEventName(v) {
						_, _ = out.WriteString(field + ": " + v + "\n")
					} else {
						f.unsupportedSSE = true
					}
				} else if strings.HasPrefix(prefix, ":") {
					// Comments are heartbeats, not malformed SSE. Do not persist
					// arbitrary comment text, which has no JSON redaction boundary.
					_, _ = out.WriteString(": [comment omitted]\n")
				} else {
					f.unsupportedSSE = true
				}
				f.linePrefix = nil
				continue
			}
			if len(f.linePrefix) < 128 {
				f.linePrefix = append(f.linePrefix, b)
			}
			if bytes.Equal(f.linePrefix, []byte("data:")) {
				f.lineStarted = true
				f.dataPrefix = nil
				f.dataReady = false
				_, _ = out.WriteString("data:")
				f.linePrefix = nil
			}
			continue
		}
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			_, _ = out.Write(f.sseData(p, false))
			break
		}
		_, _ = out.Write(f.sseData(p[:i], true))
		_ = out.WriteByte('\n')
		p = p[i+1:]
		f.lineStarted = false
	}
	return out.Bytes()
}

// Recognize SSE's non-JSON terminal marker without relaxing JSON validation.
func (f *bodyFilter) sseData(p []byte, end bool) []byte {
	if f.dataReady {
		return f.json.Write(p)
	}
	var out []byte
	for len(p) > 0 && !f.dataReady {
		f.dataPrefix = append(f.dataPrefix, p[0])
		p = p[1:]
		trimmed := strings.TrimSpace(string(f.dataPrefix))
		if len(f.dataPrefix) < 16 && strings.HasPrefix("[DONE]", trimmed) {
			continue
		}
		f.dataReady = true
		out = append(out, f.json.Write(f.dataPrefix)...)
		f.dataPrefix = nil
	}
	if f.dataReady {
		out = append(out, f.json.Write(p)...)
	}
	if end && !f.dataReady {
		if strings.TrimSpace(string(f.dataPrefix)) == "[DONE]" {
			out = append(out, []byte(" [DONE]")...)
		} else {
			out = append(out, f.json.Write(f.dataPrefix)...)
		}
		f.dataPrefix = nil
	}
	return out
}
func safeEventName(s string) bool {
	if len(s) > 96 {
		return false
	}
	for _, c := range s {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '.' && c != '_' && c != '-' {
			return false
		}
	}
	return true
}
func (f *bodyFilter) End() ([]byte, string) {
	if f.binary {
		out := []byte{}
		if !f.binaryStarted {
			out = append(out, []byte("{\"encoding\":\"base64\",\"data\":\"")...)
		}
		out = base64.StdEncoding.AppendEncode(out, f.carry)
		out = append(out, []byte("\"}")...)
		return out, ""
	}
	if f.unsupported {
		reason := "unsupported_content_type"
		if f.knownMedia {
			reason = "media_metadata_only"
		}
		b, _ := json.Marshal(map[string]any{"omitted": reason, "content_type": f.contentType, "bytes": f.bytes, "sha256": hex.EncodeToString(f.digest.Sum(nil))})
		return b, reason
	}
	var tail []byte
	if f.framing.pending && len(f.framing.prefix) > 0 {
		f.invalid = true
	}
	if f.sse {
		if f.lineStarted && !f.dataReady {
			tail = f.sseData(nil, true)
		}
		if len(bytes.TrimSpace(f.linePrefix)) > 0 {
			f.unsupportedSSE = true
		}
	}
	if f.invalid || f.json.invalid || f.json.quoted || f.json.suppressDepth > 0 || (f.json.started && !f.json.validator.complete()) {
		return append(tail, []byte("\n[capture incomplete: invalid or truncated content]\n")...), "invalid_or_truncated_content"
	}
	if f.unsupportedSSE {
		return tail, "unsupported_sse_field"
	}
	if f.json.omitted || f.omitted {
		return tail, "media_metadata_only"
	}
	return tail, ""
}
