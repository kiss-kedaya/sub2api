package service

import (
	"bytes"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// RewriteRequestedModelResponse changes only the model metadata defined by the
// client's response protocol. It is an egress operation: callers must inspect
// upstream model/usage information before passing the payload here.
func RewriteRequestedModelResponse(payload []byte, protocol, model string) []byte {
	if model == "" || len(payload) == 0 {
		return payload
	}
	probe := requestedModelProbe{payload: payload}
	if !probe.mayChange(protocol, model) || !gjson.ValidBytes(payload) {
		return payload
	}
	root := gjson.ParseBytes(payload)
	// Error payloads, including failures carried in a successful SSE transport,
	// retain the upstream representation and diagnostic information verbatim.
	if value := root.Get("error"); value.Exists() && value.Type != gjson.Null {
		return payload
	}
	if value := root.Get("response.error"); value.Exists() && value.Type != gjson.Null {
		return payload
	}
	event := root.Get("type").Str
	if event == "error" || event == "response.failed" {
		return payload
	}
	var paths []string
	switch protocol {
	case "openai":
		paths = []string{"model", "response.model"}
	case "anthropic":
		paths = []string{"model"}
		if event == "message_start" {
			paths = append(paths, "message.model")
		}
	case "realtime":
		paths = []string{"model", "response.model"}
		if event == "session.created" || event == "session.updated" {
			paths = append(paths, "session.model")
		}
	case "gemini":
		paths = []string{"modelVersion"}
		if root.IsArray() {
			paths = nil
			for index, item := range root.Array() {
				if item.Get("error").Type != gjson.Null {
					continue
				}
				paths = append(paths, strconv.Itoa(index)+".modelVersion")
			}
		}
	default:
		return payload
	}
	updated := payload
	for _, path := range paths {
		value := probe.get(path)
		if value.Type != gjson.String || value.Str == model {
			continue
		}
		if next, err := sjson.SetBytes(updated, path, model); err == nil {
			updated = next
		}
	}
	return updated
}

var (
	requestedModelKeyNeedle     = []byte("model")
	requestedModelEscapedNeedle = []byte(`\u`)
)

// requestedModelCannotAppear reports that no key spelled model/modelVersion can
// occur in data: the text holds no "model" and no \u escape that could spell it.
func requestedModelCannotAppear(data []byte) bool {
	return !bytes.Contains(data, requestedModelKeyNeedle) && !bytes.Contains(data, requestedModelEscapedNeedle)
}

// requestedModelProbe reads model fields straight from the payload bytes and
// remembers what it read, so the cheap "does anything change" test and the
// rewrite itself never look up the same path twice.
type requestedModelProbe struct {
	payload []byte
	n       int
	paths   [3]string
	values  [3]gjson.Result
}

func (p *requestedModelProbe) get(path string) gjson.Result {
	for i := 0; i < p.n; i++ {
		if p.paths[i] == path {
			return p.values[i]
		}
	}
	value := gjson.GetBytes(p.payload, path)
	if p.n < len(p.paths) {
		p.paths[p.n], p.values[p.n] = path, value
		p.n++
	}
	return value
}

func (p *requestedModelProbe) differs(path, model string) bool {
	value := p.get(path)
	return value.Type == gjson.String && value.Str != model
}

// mayChange is a cheap necessary condition for RewriteRequestedModelResponse
// to return anything but its payload: some model field of the protocol is a
// string other than model. It reads the same paths (a superset of them, where
// they depend on the event type), so the error and validity checks, which only
// ever lead to returning the payload, need not run for the common unchanged
// frame.
func (p *requestedModelProbe) mayChange(protocol, model string) bool {
	if requestedModelCannotAppear(p.payload) {
		return false
	}
	switch protocol {
	case "openai":
		return p.differs("model", model) || p.differs("response.model", model)
	case "anthropic":
		return p.differs("model", model) || p.differs("message.model", model)
	case "realtime":
		return p.differs("model", model) || p.differs("response.model", model) || p.differs("session.model", model)
	case "gemini":
		// Array bodies address one path per element; leave them to the full check.
		return true
	}
	return false
}

// InstallRequestedModelResponse wraps only the final client-facing writer. The
// enabled callback is evaluated at write time so smart/composite routing can
// choose its effective group after the handler initially installs the wrapper.
// Existing mapped-model restoration is intentionally left in place. Callers
// defer the returned finalizer to preserve an unterminated final SSE frame.
func InstallRequestedModelResponse(c *gin.Context, protocol, requestedModel string, enabled func() bool) func() {
	if c == nil || c.Writer == nil || requestedModel == "" || enabled == nil {
		return func() {}
	}
	w := &requestedModelResponseWriter{
		ResponseWriter: c.Writer, protocol: protocol, model: requestedModel, enabled: enabled,
	}
	c.Writer = w
	return w.finish
}

type requestedModelResponseWriter struct {
	gin.ResponseWriter
	protocol string
	model    string
	enabled  func() bool
	// At most one unfinished SSE frame is retained. JSON responses are already
	// buffered by their protocol forwarder and are rewritten directly on Write;
	// fragmented JSON retains only its one response document until completion.
	pending         []byte
	pendingJSON     []byte
	jsonPassthrough bool
	oversizedSSE    bool
	oversizedTail   []byte
}

// Bound accumulation from malformed fragmented JSON or unseparated SSE. Complete
// frames already supplied by the forwarder need no additional pending buffer.
const requestedModelPendingLimit = 1 << 20

func (w *requestedModelResponseWriter) active() bool {
	status := w.Status()
	// Same value as Header().Get without canonicalizing the key on every write.
	encoding := ""
	if values := w.Header()["Content-Encoding"]; len(values) > 0 {
		encoding = strings.TrimSpace(values[0])
	}
	return status >= 200 && status < 300 && (encoding == "" || encoding == "identity") && w.enabled()
}

func (w *requestedModelResponseWriter) contentType() string {
	mediaType, _, _ := strings.Cut(w.Header().Get("Content-Type"), ";")
	return strings.ToLower(strings.TrimSpace(mediaType))
}

func (w *requestedModelResponseWriter) WriteHeaderNow() {
	if w.active() {
		switch w.contentType() {
		case "application/json", "text/event-stream":
			w.Header().Del("Content-Length")
		}
	}
	w.ResponseWriter.WriteHeaderNow()
}

func (w *requestedModelResponseWriter) Write(data []byte) (int, error) {
	return w.write(data, w.active())
}

// write is Write once the caller has evaluated active(); the two evaluations
// in WriteString's path would read the same status, header and group.
func (w *requestedModelResponseWriter) write(data []byte, active bool) (int, error) {
	if !active {
		w.finish()
		return w.ResponseWriter.Write(data)
	}
	switch w.contentType() {
	case "application/json":
		w.Header().Del("Content-Length")
		if w.jsonPassthrough {
			return w.ResponseWriter.Write(data)
		}
		if len(w.pendingJSON) > 0 || !gjson.ValidBytes(data) {
			w.pendingJSON = append(w.pendingJSON, data...)
			if !gjson.ValidBytes(w.pendingJSON) {
				if len(w.pendingJSON) > requestedModelPendingLimit {
					body := w.pendingJSON
					w.pendingJSON = nil
					w.jsonPassthrough = true
					if _, err := w.writeTransformed(body, body); err != nil {
						return 0, err
					}
				}
				return len(data), nil
			}
			body := w.pendingJSON
			w.pendingJSON = nil
			if _, err := w.writeTransformed(body, RewriteRequestedModelResponse(body, w.protocol, w.model)); err != nil {
				return 0, err
			}
			return len(data), nil
		}
		return w.writeTransformed(data, RewriteRequestedModelResponse(data, w.protocol, w.model))
	case "text/event-stream":
		w.Header().Del("Content-Length")
		return w.writeSSE(data)
	default:
		w.finish()
		return w.ResponseWriter.Write(data)
	}
}

func (w *requestedModelResponseWriter) writeTransformed(original, updated []byte) (int, error) {
	n, err := w.ResponseWriter.Write(updated)
	if err != nil {
		return 0, err
	}
	if n != len(updated) {
		return 0, io.ErrShortWrite
	}
	return len(original), nil
}

func (w *requestedModelResponseWriter) writeSSE(data []byte) (int, error) {
	originalLength := len(data)
	if len(w.pending) > 0 {
		w.pending = append(w.pending, data...)
		data = w.pending
		w.pending = nil
	}
	for len(data) > 0 {
		if w.oversizedSSE {
			combined := append(append([]byte(nil), w.oversizedTail...), data...)
			end := requestedModelOversizedSSEEnd(combined)
			if end < 0 {
				w.oversizedTail = append(w.oversizedTail[:0], combined[max(0, len(combined)-2):]...)
				if _, err := w.ResponseWriter.Write(data); err != nil {
					return 0, err
				}
				break
			}
			consumed := max(0, end-len(w.oversizedTail))
			if _, err := w.ResponseWriter.Write(data[:consumed]); err != nil {
				return 0, err
			}
			w.oversizedSSE, w.oversizedTail = false, nil
			data = data[consumed:]
			continue
		}
		end := requestedModelSSEFrameEnd(data)
		if end < 0 {
			if len(data) > requestedModelPendingLimit {
				w.oversizedSSE = true
				w.oversizedTail = append(w.oversizedTail[:0], data[max(0, len(data)-2):]...)
				if _, err := w.ResponseWriter.Write(data); err != nil {
					return 0, err
				}
			} else {
				w.pending = append(w.pending, data...)
			}
			break
		}
		frame := data[:end]
		if _, err := w.writeTransformed(frame, w.rewriteSSEFrame(frame)); err != nil {
			return 0, err
		}
		data = data[end:]
	}
	return originalLength, nil
}

func requestedModelOversizedSSEEnd(data []byte) int {
	end := -1
	for _, separator := range [][]byte{[]byte("\n\n"), []byte("\n\r\n")} {
		if index := bytes.Index(data, separator); index >= 0 && (end < 0 || index+len(separator) < end) {
			end = index + len(separator)
		}
	}
	return end
}

func requestedModelSSEFrameEnd(data []byte) int {
	start := 0
	for start < len(data) {
		end := bytes.IndexByte(data[start:], '\n')
		if end < 0 {
			return -1
		}
		end += start
		if end == start || end == start+1 && data[start] == '\r' {
			return end + 1
		}
		start = end + 1
	}
	return -1
}

func (w *requestedModelResponseWriter) rewriteSSEFrame(frame []byte) []byte {
	if requestedModelCannotAppear(frame) {
		// No data line can carry a model field, so nothing would change.
		return frame
	}
	lines := bytes.SplitAfter(frame, []byte("\n"))
	type dataLine struct{ index, start, end int }
	var dataLines []dataLine
	var payload []byte
	for index, line := range lines {
		content := bytes.TrimRight(line, "\r\n")
		if bytes.HasPrefix(content, []byte("event:")) {
			event := strings.TrimSpace(string(content[len("event:"):]))
			if event == "error" || event == "response.failed" {
				return frame
			}
		}
		if !bytes.HasPrefix(content, []byte("data:")) {
			continue
		}
		start := len("data:")
		if start < len(content) && content[start] == ' ' {
			start++
		}
		switch len(dataLines) {
		case 0:
			// A single data line, the common case, is read in place: the
			// rewrite never writes to its input, and the capacity is cut so
			// that an append could not reach the bytes that follow the line.
			payload = content[start:len(content):len(content)]
		case 1:
			// The payload still aliases the frame; copy before appending.
			payload = append(append(make([]byte, 0, len(payload)+1+len(content)-start), payload...), '\n')
			payload = append(payload, content[start:]...)
		default:
			payload = append(append(payload, '\n'), content[start:]...)
		}
		dataLines = append(dataLines, dataLine{index: index, start: start, end: len(content)})
	}
	updated := RewriteRequestedModelResponse(payload, w.protocol, w.model)
	if bytes.Equal(updated, payload) {
		return frame
	}
	segments := bytes.Split(updated, []byte("\n"))
	if len(segments) != len(dataLines) {
		return frame
	}
	for index, position := range dataLines {
		line := lines[position.index]
		next := make([]byte, 0, position.start+len(segments[index])+len(line)-position.end)
		next = append(next, line[:position.start]...)
		next = append(next, segments[index]...)
		lines[position.index] = append(next, line[position.end:]...)
	}
	return bytes.Join(lines, nil)
}

func (w *requestedModelResponseWriter) WriteString(value string) (int, error) {
	if len(w.pending) == 0 && len(w.pendingJSON) == 0 {
		if !w.active() {
			return w.ResponseWriter.WriteString(value)
		}
		return w.write([]byte(value), true)
	}
	return w.Write([]byte(value))
}

func (w *requestedModelResponseWriter) Flush() {
	// SSE consumers cannot dispatch an unfinished frame either. Retain only
	// that frame across Flush, while complete frames have already been written.
	w.WriteHeaderNow()
	w.ResponseWriter.Flush()
}

func (w *requestedModelResponseWriter) finish() {
	if len(w.pendingJSON) > 0 {
		body := w.pendingJSON
		w.pendingJSON = nil
		updated := body
		if w.active() {
			updated = RewriteRequestedModelResponse(body, w.protocol, w.model)
		}
		_, _ = w.writeTransformed(body, updated)
	}
	if len(w.pending) == 0 {
		return
	}
	frame := w.pending
	w.pending = nil
	updated := frame
	if w.active() {
		updated = w.rewriteSSEFrame(frame)
	}
	// Finalization has the same no-error-return contract as Gin's Flush.
	_, _ = w.writeTransformed(frame, updated)
}

// Unwrap preserves access to optional capabilities through ResponseController.
func (w *requestedModelResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Push preserves http.Pusher in addition to Gin's Pusher accessor.
func (w *requestedModelResponseWriter) Push(target string, options *http.PushOptions) error {
	if pusher := w.Pusher(); pusher != nil {
		return pusher.Push(target, options)
	}
	return http.ErrNotSupported
}
