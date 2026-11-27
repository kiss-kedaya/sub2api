package service

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unsafe"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type modelSpanPatch struct {
	start, end int
}

// RewriteRequestedModelResponse changes only the model metadata defined by the
// client's response protocol. It is an egress operation: callers must inspect
// upstream model/usage information before passing the payload here.
func RewriteRequestedModelResponse(payload []byte, protocol, model string) []byte {
	if model == "" || len(payload) == 0 {
		return payload
	}
	spans, nSpans, handled := scanRequestedModelSpans(payload, protocol, model)
	if handled {
		if nSpans == 0 || (!validJSON(payload) && !gjson.ValidBytes(payload)) {
			return payload
		}
		return applyRequestedModelSpans(payload, spans[:nSpans], encodeRequestedModelJSON(model))
	}
	return rewriteRequestedModelFallback(payload, protocol, model)
}

func rewriteRequestedModelFallback(payload []byte, protocol, model string) []byte {
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

func isSafeJSONHTMLASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b < 0x20 || b >= 0x7f || b == '"' || b == '\\' || b == '<' || b == '>' || b == '&' {
			return false
		}
	}
	return true
}

func encodeRequestedModelJSON(model string) string {
	if isSafeJSONHTMLASCII(model) {
		return `"` + model + `"`
	}
	b, err := json.Marshal(model)
	if err != nil {
		return strconv.Quote(model)
	}
	return string(b)
}

func applyRequestedModelSpans(payload []byte, spans []modelSpanPatch, quotedModel string) []byte {
	delta := 0
	for _, sp := range spans {
		delta += len(quotedModel) - (sp.end - sp.start)
	}
	out := make([]byte, 0, len(payload)+delta)
	pos := 0
	for _, sp := range spans {
		out = append(out, payload[pos:sp.start]...)
		out = append(out, quotedModel...)
		pos = sp.end
	}
	return append(out, payload[pos:]...)
}

func scanRequestedModelSpans(payload []byte, protocol, model string) (spans [3]modelSpanPatch, n int, handled bool) {
	switch protocol {
	case "openai", "anthropic", "realtime", "gemini":
	default:
		return spans, 0, true
	}
	if requestedModelCannotAppear(payload) {
		return spans, 0, true
	}
	s := jsonView(payload)
	i := jsonSkipSpace(s, 0)
	if i >= len(s) || s[i] != '{' {
		return spans, 0, false
	}
	i++

	var (
		seenError, seenResponse, seenMessage, seenSession, seenType, seenModel, seenModelVersion bool
		eventType                                                                                string
		hasModelSpan, hasNestedRespSpan, hasNestedMsgSpan, hasNestedSessSpan                     bool
		modelSpan, nestedRespSpan, nestedMsgSpan, nestedSessSpan                                 modelSpanPatch
		msgModelFallback, sessModelFallback                                                      bool
	)

	for {
		i = jsonSkipSpace(s, i)
		if i >= len(s) {
			return spans, 0, false
		}
		switch s[i] {
		case '}':
			if jsonSkipSpace(s, i+1) != len(s) {
				return spans, 0, false
			}
			goto done
		case ',':
			i++
			continue
		case '"':
		default:
			return spans, 0, false
		}
		end, esc, ok := jsonSkipString(s, i)
		if !ok || esc {
			return spans, 0, false
		}
		key := s[i+1 : end-1]
		i = jsonSkipSpace(s, end)
		if i >= len(s) || s[i] != ':' {
			return spans, 0, false
		}
		valStart := jsonSkipSpace(s, i+1)
		valEnd, ok := jsonSkipValue(s, valStart)
		if !ok {
			return spans, 0, false
		}
		val := s[valStart:valEnd]
		switch key {
		case "error":
			if seenError {
				return spans, 0, false
			}
			seenError = true
			if val != "null" {
				return spans, 0, true
			}
		case "type":
			if seenType {
				return spans, 0, false
			}
			seenType = true
			if len(val) > 0 && val[0] == '"' {
				ev, plainOK := jsonPlainString(val)
				if !plainOK {
					return spans, 0, false
				}
				if ev == "error" || ev == "response.failed" {
					return spans, 0, true
				}
				eventType = ev
			}
		case "model":
			if protocol == "openai" || protocol == "anthropic" || protocol == "realtime" {
				if seenModel {
					return spans, 0, false
				}
				seenModel = true
				if len(val) > 0 && val[0] == '"' {
					cur, plainOK := jsonPlainString(val)
					if !plainOK {
						return spans, 0, false
					}
					if cur != model {
						hasModelSpan = true
						modelSpan = modelSpanPatch{start: valStart, end: valEnd}
					}
				}
			}
		case "modelVersion":
			if protocol == "gemini" {
				if seenModelVersion {
					return spans, 0, false
				}
				seenModelVersion = true
				if len(val) > 0 && val[0] == '"' {
					cur, plainOK := jsonPlainString(val)
					if !plainOK {
						return spans, 0, false
					}
					if cur != model {
						hasModelSpan = true
						modelSpan = modelSpanPatch{start: valStart, end: valEnd}
					}
				}
			}
		case "response":
			if seenResponse {
				return spans, 0, false
			}
			seenResponse = true
			if len(val) > 0 && val[0] == '{' {
				respErr, respModel, hasRespModel, nestedOK := scanNestedModelObject(
					s, valStart, valEnd, model,
					protocol == "openai" || protocol == "realtime",
					true,
				)
				if !nestedOK {
					return spans, 0, false
				}
				if respErr {
					return spans, 0, true
				}
				if hasRespModel {
					hasNestedRespSpan = true
					nestedRespSpan = respModel
				}
			}
		case "message":
			if protocol == "anthropic" {
				if seenMessage {
					return spans, 0, false
				}
				seenMessage = true
				if len(val) > 0 && val[0] == '{' {
					_, msgModel, hasMsgModel, nestedOK := scanNestedModelObject(s, valStart, valEnd, model, true, false)
					if !nestedOK {
						msgModelFallback = true
					} else if hasMsgModel {
						hasNestedMsgSpan = true
						nestedMsgSpan = msgModel
					}
				}
			}
		case "session":
			if protocol == "realtime" {
				if seenSession {
					return spans, 0, false
				}
				seenSession = true
				if len(val) > 0 && val[0] == '{' {
					_, sessModel, hasSessModel, nestedOK := scanNestedModelObject(s, valStart, valEnd, model, true, false)
					if !nestedOK {
						sessModelFallback = true
					} else if hasSessModel {
						hasNestedSessSpan = true
						nestedSessSpan = sessModel
					}
				}
			}
		}
		i = valEnd
	}

done:
	if protocol == "anthropic" && eventType == "message_start" && msgModelFallback {
		return spans, 0, false
	}
	if protocol == "realtime" && (eventType == "session.created" || eventType == "session.updated") && sessModelFallback {
		return spans, 0, false
	}
	if hasModelSpan {
		spans[n] = modelSpan
		n++
	}
	if hasNestedRespSpan {
		spans[n] = nestedRespSpan
		n++
	}
	if protocol == "anthropic" && eventType == "message_start" && hasNestedMsgSpan {
		spans[n] = nestedMsgSpan
		n++
	}
	if protocol == "realtime" && (eventType == "session.created" || eventType == "session.updated") && hasNestedSessSpan {
		spans[n] = nestedSessSpan
		n++
	}
	for k := 1; k < n; k++ {
		cur := spans[k]
		j := k - 1
		for j >= 0 && spans[j].start > cur.start {
			spans[j+1] = spans[j]
			j--
		}
		spans[j+1] = cur
	}
	return spans, n, true
}

func scanNestedModelObject(s string, objStart, objEnd int, model string, checkModel, checkError bool) (hasError bool, modelSpan modelSpanPatch, hasModel bool, ok bool) {
	if objEnd-objStart < 2 || s[objStart] != '{' || s[objEnd-1] != '}' {
		return false, modelSpan, false, false
	}
	i := objStart + 1
	var seenError, seenModel bool
	for {
		i = jsonSkipSpace(s, i)
		if i >= objEnd {
			return false, modelSpan, false, false
		}
		switch s[i] {
		case '}':
			return false, modelSpan, hasModel, i == objEnd-1
		case ',':
			i++
			continue
		case '"':
		default:
			return false, modelSpan, false, false
		}
		end, esc, strOK := jsonSkipString(s, i)
		if !strOK || esc || end >= objEnd {
			return false, modelSpan, false, false
		}
		key := s[i+1 : end-1]
		i = jsonSkipSpace(s, end)
		if i >= objEnd || s[i] != ':' {
			return false, modelSpan, false, false
		}
		valStart := jsonSkipSpace(s, i+1)
		valEnd, valOK := jsonSkipValue(s, valStart)
		if !valOK || valEnd > objEnd {
			return false, modelSpan, false, false
		}
		val := s[valStart:valEnd]
		switch key {
		case "error":
			if checkError {
				if seenError {
					return false, modelSpan, false, false
				}
				seenError = true
				if val != "null" {
					return true, modelSpan, false, true
				}
			}
		case "model":
			if checkModel {
				if seenModel {
					return false, modelSpan, false, false
				}
				seenModel = true
				if len(val) > 0 && val[0] == '"' {
					cur, plainOK := jsonPlainString(val)
					if !plainOK {
						return false, modelSpan, false, false
					}
					if cur != model {
						hasModel = true
						modelSpan = modelSpanPatch{start: valStart, end: valEnd}
					}
				}
			}
		}
		i = valEnd
	}
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
	scratch         []byte
	quotedModel     string
	jsonPassthrough bool
	oversizedSSE    bool
	oversizedTail   []byte

	headersCommitted     bool
	contentLengthDeleted bool
	activeKnown          bool
	cachedActive         bool
	cachedContentType    uint8
	sseInCommentLine     bool
	sseSeenNonComment    bool
}

const (
	requestedModelCTUnknown uint8 = iota
	requestedModelCTOther
	requestedModelCTJSON
	requestedModelCTSSE
)

// Bound accumulation from malformed fragmented JSON or unseparated SSE. Complete
// frames already supplied by the forwarder need no additional pending buffer.
const (
	requestedModelPendingLimit     = 1 << 20
	requestedModelPendingRetainCap = 64 << 10
)

func (w *requestedModelResponseWriter) deleteContentLengthOnce() {
	if !w.contentLengthDeleted {
		w.contentLengthDeleted = true
		w.Header().Del("Content-Length")
	}
}

func (w *requestedModelResponseWriter) commitHeaderMeta() {
	if w.headersCommitted {
		return
	}
	w.headersCommitted = true
	status := w.Status()
	// Same value as Header().Get without canonicalizing the key on every write.
	encoding := ""
	if values := w.Header()["Content-Encoding"]; len(values) > 0 {
		encoding = strings.TrimSpace(values[0])
	}
	if status < 200 || status >= 300 || (encoding != "" && encoding != "identity") {
		w.cachedContentType = requestedModelCTOther
		w.activeKnown = true
		w.cachedActive = false
		return
	}
	switch w.contentType() {
	case "application/json":
		w.cachedContentType = requestedModelCTJSON
	case "text/event-stream":
		w.cachedContentType = requestedModelCTSSE
	default:
		w.cachedContentType = requestedModelCTOther
		w.activeKnown = true
		w.cachedActive = false
	}
}

func (w *requestedModelResponseWriter) evalActive(doNotLatch bool) bool {
	if w.activeKnown {
		return w.cachedActive
	}
	if !w.headersCommitted {
		w.commitHeaderMeta()
		if w.activeKnown {
			return w.cachedActive
		}
	}
	en := w.enabled()
	if w.headersCommitted && (!doNotLatch || w.cachedContentType == requestedModelCTJSON) {
		w.cachedActive = en
		w.activeKnown = true
	}
	return en
}

func (w *requestedModelResponseWriter) advanceSSECommentBytes(data []byte) {
	pos := 0
	for pos < len(data) {
		if w.sseInCommentLine {
			nl := bytes.IndexByte(data[pos:], '\n')
			if nl < 0 {
				return
			}
			pos += nl + 1
			w.sseInCommentLine = false
			continue
		}
		switch data[pos] {
		case ':':
			w.sseInCommentLine = true
			pos++
		case '\n', '\r':
			pos++
		default:
			w.sseSeenNonComment = true
			return
		}
	}
}

func (w *requestedModelResponseWriter) activeForBytes(data []byte) bool {
	if w.activeKnown {
		return w.cachedActive
	}
	if !w.headersCommitted {
		w.commitHeaderMeta()
		if w.activeKnown {
			return w.cachedActive
		}
	}
	if w.cachedContentType == requestedModelCTSSE && !w.sseSeenNonComment {
		w.advanceSSECommentBytes(data)
		return w.evalActive(!w.sseSeenNonComment)
	}
	return w.evalActive(false)
}

func (w *requestedModelResponseWriter) activeForString(value string) bool {
	if w.activeKnown {
		return w.cachedActive
	}
	var data []byte
	if len(value) > 0 {
		data = unsafe.Slice(unsafe.StringData(value), len(value))
	}
	return w.activeForBytes(data)
}

func (w *requestedModelResponseWriter) finishOversizedCommentPassthroughBytes(data []byte) {
	if !w.oversizedSSE || w.activeKnown {
		return
	}
	combined := append(append([]byte(nil), w.oversizedTail...), data...)
	if requestedModelOversizedSSEEnd(combined) >= 0 {
		w.oversizedSSE, w.oversizedTail = false, nil
		return
	}
	w.oversizedTail = append(w.oversizedTail[:0], combined[max(0, len(combined)-2):]...)
}

func (w *requestedModelResponseWriter) finishOversizedCommentPassthroughString(value string) {
	if !w.oversizedSSE || w.activeKnown {
		return
	}
	if requestedModelOversizedSSEEndJoinedString(w.oversizedTail, value) >= 0 {
		w.oversizedSSE, w.oversizedTail = false, nil
		return
	}
	w.appendOversizedTailString(value)
}

func (w *requestedModelResponseWriter) contentType() string {
	mediaType, _, _ := strings.Cut(w.Header().Get("Content-Type"), ";")
	return strings.ToLower(strings.TrimSpace(mediaType))
}

func (w *requestedModelResponseWriter) resetPending() {
	if cap(w.pending) <= requestedModelPendingRetainCap {
		w.pending = w.pending[:0]
	} else {
		w.pending = nil
	}
}

func (w *requestedModelResponseWriter) resetScratch() {
	if cap(w.scratch) <= requestedModelPendingRetainCap {
		w.scratch = w.scratch[:0]
	} else {
		w.scratch = nil
	}
}

func (w *requestedModelResponseWriter) cachedQuotedModel() string {
	if w.quotedModel == "" && w.model != "" {
		w.quotedModel = encodeRequestedModelJSON(w.model)
	}
	return w.quotedModel
}

func (w *requestedModelResponseWriter) WriteHeaderNow() {
	if w.evalActive(true) && (w.cachedContentType == requestedModelCTJSON || w.cachedContentType == requestedModelCTSSE) {
		w.deleteContentLengthOnce()
	}
	w.ResponseWriter.WriteHeaderNow()
}

func (w *requestedModelResponseWriter) Write(data []byte) (int, error) {
	return w.write(data, w.activeForBytes(data))
}

func (w *requestedModelResponseWriter) write(data []byte, active bool) (int, error) {
	if !active {
		if len(w.pending) > 0 || len(w.pendingJSON) > 0 {
			w.finish()
		}
		if w.oversizedSSE {
			w.finishOversizedCommentPassthroughBytes(data)
		}
		return w.ResponseWriter.Write(data)
	}
	w.deleteContentLengthOnce()
	switch w.cachedContentType {
	case requestedModelCTJSON:
		return w.writeJSON(data)
	case requestedModelCTSSE:
		return w.writeSSE(data)
	default:
		w.finish()
		return w.ResponseWriter.Write(data)
	}
}

func (w *requestedModelResponseWriter) writeJSON(data []byte) (int, error) {
	if w.jsonPassthrough {
		return w.ResponseWriter.Write(data)
	}
	if len(w.pendingJSON) > 0 || (!validJSON(data) && !gjson.ValidBytes(data)) {
		w.pendingJSON = append(w.pendingJSON, data...)
		if !validJSON(w.pendingJSON) && !gjson.ValidBytes(w.pendingJSON) {
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
	usingPending := false
	if len(w.pending) > 0 {
		w.pending = append(w.pending, data...)
		data = w.pending
		usingPending = true
	}
	for len(data) > 0 {
		if w.oversizedSSE {
			combined := append(append([]byte(nil), w.oversizedTail...), data...)
			end := requestedModelOversizedSSEEnd(combined)
			if end < 0 {
				w.oversizedTail = append(w.oversizedTail[:0], combined[max(0, len(combined)-2):]...)
				if _, err := w.ResponseWriter.Write(data); err != nil {
					if usingPending {
						w.resetPending()
					}
					return 0, err
				}
				break
			}
			consumed := max(0, end-len(w.oversizedTail))
			if _, err := w.ResponseWriter.Write(data[:consumed]); err != nil {
				if usingPending {
					w.resetPending()
				}
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
				_, err := w.ResponseWriter.Write(data)
				if usingPending {
					w.resetPending()
				}
				if err != nil {
					return 0, err
				}
			} else if usingPending {
				copy(w.pending[:len(data)], data)
				w.pending = w.pending[:len(data)]
				return originalLength, nil
			} else {
				w.pending = append(w.pending[:0], data...)
			}
			break
		}
		frame := data[:end]
		if _, err := w.writeTransformed(frame, w.rewriteSSEFrame(frame)); err != nil {
			if usingPending {
				w.resetPending()
			}
			return 0, err
		}
		data = data[end:]
	}
	if usingPending {
		w.resetPending()
	}
	return originalLength, nil
}

func (w *requestedModelResponseWriter) writeSSEString(value string) (int, error) {
	originalLength := len(value)
	if len(w.pending) > 0 {
		w.pending = append(w.pending, value...)
		return w.writeSSEPending(originalLength)
	}
	for len(value) > 0 {
		if w.oversizedSSE {
			consumed := requestedModelOversizedSSEEndJoinedString(w.oversizedTail, value)
			if consumed < 0 {
				w.appendOversizedTailString(value)
				n, err := w.ResponseWriter.WriteString(value)
				if err != nil {
					return 0, err
				}
				if n != len(value) {
					return 0, io.ErrShortWrite
				}
				break
			}
			n, err := w.ResponseWriter.WriteString(value[:consumed])
			if err != nil {
				return 0, err
			}
			if n != consumed {
				return 0, io.ErrShortWrite
			}
			w.oversizedSSE, w.oversizedTail = false, nil
			value = value[consumed:]
			continue
		}
		end := requestedModelSSEFrameEndString(value)
		if end < 0 {
			if len(value) > requestedModelPendingLimit {
				w.oversizedSSE = true
				w.oversizedTail = append(w.oversizedTail[:0], value[max(0, len(value)-2):]...)
				n, err := w.ResponseWriter.WriteString(value)
				if err != nil {
					return 0, err
				}
				if n != len(value) {
					return 0, io.ErrShortWrite
				}
			} else {
				w.pending = append(w.pending[:0], value...)
			}
			break
		}
		frameStr := value[:end]
		frameBytes := unsafe.Slice(unsafe.StringData(frameStr), len(frameStr))
		updated := w.rewriteSSEFrame(frameBytes)
		if len(updated) == len(frameBytes) && unsafe.SliceData(updated) == unsafe.SliceData(frameBytes) {
			n, err := w.ResponseWriter.WriteString(frameStr)
			if err != nil {
				return 0, err
			}
			if n != len(frameStr) {
				return 0, io.ErrShortWrite
			}
		} else if _, err := w.writeTransformed(frameBytes, updated); err != nil {
			return 0, err
		}
		value = value[end:]
	}
	return originalLength, nil
}

func (w *requestedModelResponseWriter) writeSSEPending(originalLength int) (int, error) {
	data := w.pending
	for len(data) > 0 {
		end := requestedModelSSEFrameEnd(data)
		if end < 0 {
			if len(data) > requestedModelPendingLimit {
				w.oversizedSSE = true
				w.oversizedTail = append(w.oversizedTail[:0], data[max(0, len(data)-2):]...)
				_, err := w.ResponseWriter.Write(data)
				w.resetPending()
				if err != nil {
					return 0, err
				}
				return originalLength, nil
			}
			copy(w.pending[:len(data)], data)
			w.pending = w.pending[:len(data)]
			return originalLength, nil
		}
		frame := data[:end]
		if _, err := w.writeTransformed(frame, w.rewriteSSEFrame(frame)); err != nil {
			w.resetPending()
			return 0, err
		}
		data = data[end:]
	}
	w.resetPending()
	return originalLength, nil
}

func (w *requestedModelResponseWriter) appendOversizedTailString(value string) {
	if len(value) >= 2 {
		w.oversizedTail = append(w.oversizedTail[:0], value[len(value)-2:]...)
		return
	}
	if len(value) == 1 {
		if len(w.oversizedTail) >= 2 {
			w.oversizedTail[0] = w.oversizedTail[len(w.oversizedTail)-1]
			w.oversizedTail[1] = value[0]
			w.oversizedTail = w.oversizedTail[:2]
		} else {
			w.oversizedTail = append(w.oversizedTail, value[0])
		}
	}
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

func requestedModelOversizedSSEEndJoinedString(tail []byte, value string) int {
	if len(tail) > 0 {
		var bridge [4]byte
		n := copy(bridge[:], tail)
		n += copy(bridge[n:], value[:min(len(value), 2)])
		if bEnd := requestedModelOversizedSSEEnd(bridge[:n]); bEnd >= 0 {
			return max(0, bEnd-len(tail))
		}
	}
	end := -1
	if idx := strings.Index(value, "\n\n"); idx >= 0 {
		end = idx + 2
	}
	if idx := strings.Index(value, "\n\r\n"); idx >= 0 && (end < 0 || idx+3 < end) {
		end = idx + 3
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

func requestedModelSSEFrameEndString(data string) int {
	start := 0
	for start < len(data) {
		end := strings.IndexByte(data[start:], '\n')
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

type sseDataLineSpan struct {
	absStart int
	absEnd   int
}

func (w *requestedModelResponseWriter) rewriteSSEFrame(frame []byte) []byte {
	if requestedModelCannotAppear(frame) {
		// No data line can carry a model field, so nothing would change.
		return frame
	}
	var (
		dataLinesBuf [4]sseDataLineSpan
		dataLines    = dataLinesBuf[:0]
		firstStart   int
		firstEnd     int
		dataCount    int
		payload      []byte
	)
	for pos := 0; pos < len(frame); {
		nl := bytes.IndexByte(frame[pos:], '\n')
		lineEnd := len(frame)
		if nl >= 0 {
			lineEnd = pos + nl + 1
		}
		contentEnd := lineEnd
		for contentEnd > pos && (frame[contentEnd-1] == '\n' || frame[contentEnd-1] == '\r') {
			contentEnd--
		}
		content := frame[pos:contentEnd]
		if bytes.HasPrefix(content, []byte("event:")) {
			event := strings.TrimSpace(jsonView(content[len("event:"):]))
			if event == "error" || event == "response.failed" {
				return frame
			}
		}
		if bytes.HasPrefix(content, []byte("data:")) {
			start := len("data:")
			if start < len(content) && content[start] == ' ' {
				start++
			}
			absStart := pos + start
			absEnd := contentEnd
			switch dataCount {
			case 0:
				firstStart = absStart
				firstEnd = absEnd
				payload = frame[absStart:absEnd:absEnd]
			case 1:
				dataLines = append(dataLines, sseDataLineSpan{absStart: firstStart, absEnd: firstEnd})
				dataLines = append(dataLines, sseDataLineSpan{absStart: absStart, absEnd: absEnd})
				payload = append(append(make([]byte, 0, len(payload)+1+absEnd-absStart), payload...), '\n')
				payload = append(payload, frame[absStart:absEnd]...)
			default:
				dataLines = append(dataLines, sseDataLineSpan{absStart: absStart, absEnd: absEnd})
				payload = append(append(payload, '\n'), frame[absStart:absEnd]...)
			}
			dataCount++
		}
		pos = lineEnd
	}
	if dataCount == 0 {
		return frame
	}
	if dataCount == 1 {
		spans, nSpans, handled := scanRequestedModelSpans(payload, w.protocol, w.model)
		if handled {
			if nSpans == 0 || (!validJSON(payload) && !gjson.ValidBytes(payload)) {
				return frame
			}
			quotedModel := w.cachedQuotedModel()
			delta := 0
			for _, sp := range spans[:nSpans] {
				delta += len(quotedModel) - (sp.end - sp.start)
			}
			out := make([]byte, 0, len(frame)+delta)
			out = append(out, frame[:firstStart]...)
			pos := 0
			for _, sp := range spans[:nSpans] {
				out = append(out, payload[pos:sp.start]...)
				out = append(out, quotedModel...)
				pos = sp.end
			}
			out = append(out, payload[pos:]...)
			return append(out, frame[firstEnd:]...)
		}
		updated := rewriteRequestedModelFallback(payload, w.protocol, w.model)
		if bytes.Equal(updated, payload) || bytes.IndexByte(updated, '\n') >= 0 {
			return frame
		}
		out := make([]byte, 0, firstStart+len(updated)+len(frame)-firstEnd)
		out = append(out, frame[:firstStart]...)
		out = append(out, updated...)
		return append(out, frame[firstEnd:]...)
	}
	updated := RewriteRequestedModelResponse(payload, w.protocol, w.model)
	if bytes.Equal(updated, payload) {
		return frame
	}
	if bytes.Count(updated, []byte{'\n'})+1 != len(dataLines) {
		return frame
	}
	outLen := len(frame) + len(updated) - len(payload)
	out := make([]byte, 0, outLen)
	framePos := 0
	updPos := 0
	for index, dl := range dataLines {
		out = append(out, frame[framePos:dl.absStart]...)
		var seg []byte
		if index == len(dataLines)-1 {
			seg = updated[updPos:]
		} else {
			nl := bytes.IndexByte(updated[updPos:], '\n')
			seg = updated[updPos : updPos+nl]
			updPos += nl + 1
		}
		out = append(out, seg...)
		framePos = dl.absEnd
	}
	return append(out, frame[framePos:]...)
}

func (w *requestedModelResponseWriter) WriteString(value string) (int, error) {
	if !w.activeForString(value) {
		if len(w.pending) > 0 || len(w.pendingJSON) > 0 {
			w.finish()
		}
		if w.oversizedSSE {
			w.finishOversizedCommentPassthroughString(value)
		}
		return w.ResponseWriter.WriteString(value)
	}
	w.deleteContentLengthOnce()
	switch w.cachedContentType {
	case requestedModelCTSSE:
		return w.writeSSEString(value)
	case requestedModelCTJSON:
		if w.jsonPassthrough {
			return w.ResponseWriter.WriteString(value)
		}
		w.scratch = append(w.scratch[:0], value...)
		n, err := w.writeJSON(w.scratch)
		w.resetScratch()
		return n, err
	default:
		w.finish()
		return w.ResponseWriter.WriteString(value)
	}
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
		if w.evalActive(false) {
			updated = RewriteRequestedModelResponse(body, w.protocol, w.model)
		}
		_, _ = w.writeTransformed(body, updated)
	}
	if len(w.pending) == 0 {
		return
	}
	frame := w.pending
	updated := frame
	if w.sseSeenNonComment && w.evalActive(false) {
		updated = w.rewriteSSEFrame(frame)
	}
	// Finalization has the same no-error-return contract as Gin's Flush.
	_, _ = w.writeTransformed(frame, updated)
	w.resetPending()
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
