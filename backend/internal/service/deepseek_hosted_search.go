package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Only the outer request owns hosted execution. Recursive model turns retain
// the same account, authentication and forwarding implementation.
type deepSeekHostedSearchSkipContextKey struct{}

const (
	deepSeekHostedMaxCalls      = 3
	deepSeekHostedMaxRounds     = 3
	deepSeekHostedMaxFrameBytes = 16 << 20
)

type deepSeekHostedCall struct {
	itemID, callID, arguments, searchID, query string
	name, url                                  string
	index                                      int
	sources                                    []map[string]string
	status                                     string
}

type deepSeekHostedSearchState struct {
	c                      *gin.Context
	stream                 bool
	started                time.Time
	responseID             string
	model                  string
	sequence               int
	outputs                []json.RawMessage
	usage                  map[string]any
	searchCount, attempted int
	domains                []string
	header                 http.Header
	result                 *OpenAIForwardResult
	firstTokenMs           *int
	callIDs                map[string]struct{}
	clientDisconnected     bool
	// A disabled key leaves an explicitly supplied client web_fetch function
	// client-owned, including its declaration, call output and forced choice.
	skipFetchTool bool
}

type deepSeekHostedRoundWriter struct {
	gin.ResponseWriter
	state        *deepSeekHostedSearchState
	header       http.Header
	status, size int
	buffer       []byte
	err          error
	response     []byte
	terminal     string
	indices      map[int]int
	calls        []*deepSeekHostedCall
	owned        map[string]*deepSeekHostedCall
	ownedIndices map[int]*deepSeekHostedCall
	canonicalIDs map[int]string
	done         bool
}

func (s *OpenAIGatewayService) forwardDeepSeekHostedSearch(ctx context.Context, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, bool, error) {
	if ctx.Value(deepSeekHostedSearchSkipContextKey{}) == true || account == nil ||
		account.Platform != PlatformDeepseek || isOpenAIResponsesCompactPath(c) {
		return nil, false, nil
	}
	group, enabled := deepSeekSearchGroupEnabled(c, account)
	if !enabled {
		return nil, false, nil
	}
	injectTools := deepSeekToolInjectionEnabled(c)
	prepared, changed, err := prepareDeepSeekHostedSearchRequestWithToolInjection(body, injectTools)
	if err != nil {
		return nil, true, writeDeepSeekHostedSearchValidationError(ctx, c, err)
	}
	if !changed {
		return nil, false, nil
	}
	hostedDeclared := false
	for _, tool := range gjson.GetBytes(prepared, "tools").Array() {
		if tool.Get("type").String() == "function" && deepSeekHostedOwnedToolName(tool.Get("name").String(), injectTools) {
			hostedDeclared = true
			break
		}
	}
	if !hostedDeclared {
		// Replaying old hosted history (including tool_choice=none) is only a
		// request normalization. Preserve the ordinary response path exactly.
		innerCtx := context.WithValue(ctx, deepSeekHostedSearchSkipContextKey{}, true)
		shadow := c.Copy()
		shadow.Request = c.Request.Clone(innerCtx)
		shadow.Writer = c.Writer
		result, forwardErr := s.Forward(innerCtx, shadow, account, prepared)
		return result, true, forwardErr
	}
	state := &deepSeekHostedSearchState{c: c, stream: gjson.GetBytes(body, "stream").Bool(), model: gjson.GetBytes(body, "model").String(), started: time.Now(), header: make(http.Header), callIDs: make(map[string]struct{}), skipFetchTool: !injectTools}
	fail := func(cause error) (*OpenAIForwardResult, bool, error) {
		if ctx.Err() != nil {
			state.clientDisconnected = true
		}
		if cause != nil && !state.clientDisconnected {
			status := c.GetInt(OpsUpstreamStatusCodeKey)
			if status <= 0 {
				status = http.StatusBadGateway
			}
			detail := truncateString(sanitizeUpstreamErrorMessage(cause.Error()), 512)
			setOpsUpstreamError(c, status, "DeepSeek hosted search failed", detail)
		}
		if state.stream && c.Writer.Written() && !IsResponseCommitted(c) &&
			strings.Contains(c.Writer.Header().Get("Content-Type"), "text/event-stream") &&
			!state.clientDisconnected && (state.result == nil || !state.result.ClientDisconnect) {
			_ = state.writeFailure()
		}
		if state.attempted > 0 || c.Writer.Written() {
			// Do not expose provider diagnostics or retain a retryable error type
			// after this logical response has become visible or executed a search.
			cause = errors.New("deepseek hosted search continuation failed")
		}
		return state.finishResult(), true, cause
	}
	for _, tool := range gjson.GetBytes(body, "tools").Array() {
		if isDeepSeekHostedSearchToolType(tool.Get("type").String()) {
			state.domains, err = deepSeekHostedSearchAllowedDomains(tool)
			if err != nil {
				return nil, true, err
			}
		}
	}
	request := prepared
	for round := 0; round <= deepSeekHostedMaxRounds; round++ {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		shadow := c.Copy()
		innerCtx := context.WithValue(ctx, deepSeekHostedSearchSkipContextKey{}, true)
		shadow.Request = c.Request.Clone(innerCtx)
		writer := &deepSeekHostedRoundWriter{ResponseWriter: c.Writer, state: state, header: make(http.Header), status: http.StatusOK, size: -1, indices: make(map[int]int), owned: make(map[string]*deepSeekHostedCall)}
		shadow.Writer = writer
		result, forwardErr := s.Forward(innerCtx, shadow, account, request)
		writer.finish()
		if writer.err != nil && !state.clientDisconnected && ctx.Err() == nil && result != nil && result.ClientDisconnect {
			// The ordinary streaming forwarder sees a Write error for both a
			// closed socket and this writer's protocol validation. Only the former
			// is a client disconnect; retain the provider failure attribution.
			partial := *result
			partial.ClientDisconnect = false
			result = &partial
		}
		state.addResult(result)
		if len(writer.response) > 0 {
			state.mergeUsage(gjson.GetBytes(writer.response, "usage"))
			if writer.err == nil {
				writer.captureOutput()
			}
		}
		// The recursive context owns per-attempt observations; carry only operation
		// metadata back to the outer handler, never replace its request or identity.
		for key, value := range shadow.Keys {
			if strings.HasPrefix(key, "ops_") {
				c.Set(key, value)
			}
		}
		if writer.err != nil {
			return fail(writer.err)
		}
		if ctx.Err() != nil {
			return fail(ctx.Err())
		}
		if result != nil && result.ClientDisconnect {
			state.clientDisconnected = true
			return fail(context.Canceled)
		}
		if forwardErr != nil {
			if state.stream && len(writer.response) > 0 &&
				(writer.terminal == "response.failed" || writer.terminal == "response.incomplete") {
				if writeErr := state.writeFinal(writer); writeErr != nil {
					return fail(writeErr)
				}
				return state.finishResult(), true, errors.New("deepseek upstream response did not complete")
			}
			if !c.Writer.Written() && len(writer.buffer) > 0 {
				if writeErr := writer.writeBufferedError(); writeErr != nil {
					return fail(writeErr)
				}
			}
			return fail(forwardErr)
		}
		if len(writer.response) == 0 {
			if writer.status >= http.StatusBadRequest && len(writer.buffer) > 0 {
				if writeErr := writer.writeBufferedError(); writeErr != nil {
					return fail(writeErr)
				}
				return state.finishResult(), true, nil
			}
			return fail(errors.New("deepseek upstream response has no terminal response"))
		}
		if writer.terminal == "response.failed" || writer.terminal == "response.incomplete" || len(writer.calls) == 0 {
			writeErr := state.writeFinal(writer)
			return state.finishResult(), true, writeErr
		}
		outputs := make(map[string]string, len(writer.calls))
		for _, call := range writer.calls {
			if call.callID == "" {
				return fail(errors.New("deepseek hosted search returned an unpaired tool call"))
			}
			if _, exists := state.callIDs[call.callID]; exists {
				return fail(errors.New("deepseek hosted search returned a duplicate tool call ID"))
			}
			state.callIDs[call.callID] = struct{}{}
		}
		for _, call := range writer.calls {
			if err := ctx.Err(); err != nil {
				return fail(err)
			}
			call.query, _ = deepSeekSearchCallQuery(call.arguments)
			call.status = "failed"
			if state.attempted >= deepSeekHostedMaxCalls || round >= deepSeekHostedMaxRounds {
				outputs[call.callID] = `{"error":"search call limit reached; continue with available results"}`
			} else {
				state.attempted++
				if isDeepSeekHostedFetchToolName(call.name) {
					outputs[call.callID] = s.runDeepSeekHostedFetch(ctx, c, account, group, state, call)
				} else {
					outputs[call.callID] = s.runDeepSeekHostedSearch(ctx, c, account, group, state, call)
				}
			}
			if ctx.Err() != nil {
				return fail(ctx.Err())
			}
			if state.clientDisconnected {
				return fail(context.Canceled)
			}
			if err := state.completeSearch(call); err != nil {
				return fail(err)
			}
		}
		// Client tools remain client-owned. Continuing before their outputs exist
		// would create an invalid function-call history, so return this mixed turn.
		if writer.hasClientCalls() {
			writeErr := state.writeFinal(writer)
			return state.finishResult(), true, writeErr
		}
		next, nextErr := deepSeekHostedSearchNextRequestWithFetch(request, writer.response, outputs, !state.skipFetchTool)
		if nextErr != nil {
			return fail(nextErr)
		}
		if state.attempted >= deepSeekHostedMaxCalls || round+1 >= deepSeekHostedMaxRounds {
			next, err = deepSeekHostedRemoveToolWithFetch(next, !state.skipFetchTool)
			if err != nil {
				return fail(err)
			}
		}
		request = next
	}
	return fail(errors.New("deepseek hosted search exceeded continuation limit"))
}

func writeDeepSeekHostedSearchValidationError(ctx context.Context, c *gin.Context, cause error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.Request != nil {
		if err := c.Request.Context().Err(); err != nil {
			return err
		}
	}
	if IsResponseCommitted(c) {
		return cause
	}
	// Stop the pre-header heartbeat before deciding between HTTP JSON and an
	// in-band terminal event. A heartbeat may already have committed HTTP 200.
	streamCommitted := StopOpenAICompactSSEKeepaliveCommitted(c)
	MarkResponseCommitted(c)
	if streamCommitted || (c.Writer.Written() && strings.Contains(c.Writer.Header().Get("Content-Type"), "text/event-stream")) {
		writeOpenAICompactSSEFailureMessage(c, http.StatusBadRequest, "invalid_request_error", cause.Error())
		return cause
	}
	if !c.Writer.Written() {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"type": "invalid_request_error", "message": cause.Error(), "param": "tools",
		}})
	}
	return cause
}

func (s *OpenAIGatewayService) runDeepSeekHostedSearch(ctx context.Context, c *gin.Context, account *Account, group *Group, state *deepSeekHostedSearchState, call *deepSeekHostedCall) string {
	if ctx.Err() != nil {
		return `{"error":"request canceled"}`
	}
	query, count := deepSeekSearchCallQuery(call.arguments)
	if query == "" || !gjson.Valid(call.arguments) || utf8.RuneCountInString(query) > 128 {
		return `{"error":"search requires a nonempty query of at most 128 characters"}`
	}
	if len(state.domains) > 0 {
		parts := make([]string, len(state.domains))
		for i, domain := range state.domains {
			parts[i] = "site:" + domain
		}
		query += " (" + strings.Join(parts, " OR ") + ")"
		if utf8.RuneCountInString(query) > 128 {
			return `{"error":"search query and domain filters exceed the provider query limit"}`
		}
	}
	if state.stream {
		if err := state.event(map[string]any{"type": "response.web_search_call.searching", "item_id": call.searchID, "output_index": call.index}); err != nil {
			return `{"error":"client disconnected"}`
		}
	}
	payload, _ := json.Marshal(map[string]any{"query": query, "count": clampDeepSeekSearchCount(count)})
	searchCtx, cancel := context.WithTimeout(ctx, deepSeekSearchToolTimeout)
	defer cancel()
	recorder := httptest.NewRecorder()
	shadow := c.Copy()
	shadow.Request = c.Request.Clone(searchCtx)
	shadow.Writer = &deepSeekHostedRecorder{ResponseRecorder: recorder}
	result, err := s.ForwardDeepSeekTools(searchCtx, shadow, account, group, DeepSeekToolsPathSearch, "", payload)
	if err != nil || recorder.Code != http.StatusOK {
		return `{"error":"search provider request failed"}`
	}
	if result != nil && result.WebSearchCalls > 0 {
		state.searchCount += result.WebSearchCalls
	}
	if !gjson.ValidBytes(recorder.Body.Bytes()) || !gjson.GetBytes(recorder.Body.Bytes(), "results").IsArray() {
		return `{"error":"search provider returned an invalid response"}`
	}
	hits := make([]map[string]string, 0)
	for _, hit := range gjson.GetBytes(recorder.Body.Bytes(), "results").Array() {
		u, parseErr := url.Parse(hit.Get("url").String())
		if parseErr != nil || !deepSeekHostedSearchPublicURL(hit.Get("url").String()) {
			continue
		}
		if len(state.domains) > 0 {
			allowed := false
			for _, domain := range state.domains {
				if strings.EqualFold(u.Hostname(), domain) || strings.HasSuffix(strings.ToLower(u.Hostname()), "."+domain) {
					allowed = true
					break
				}
			}
			if !allowed {
				continue
			}
		}
		hits = append(hits, map[string]string{"title": hit.Get("title").String(), "url": u.String(), "snippet": hit.Get("snippet").String(), "summary": hit.Get("summary").String(), "date": hit.Get("date").String()})
		call.sources = append(call.sources, map[string]string{"type": "url", "url": u.String(), "title": hit.Get("title").String()})
	}
	call.status = "completed"
	encoded, _ := json.Marshal(map[string]any{"results": hits, "count": len(hits)})
	return string(encoded)
}

// The search provider adapter writes a regular Gin response. This isolated
// recorder implements the extra Gin methods without touching the client writer.
type deepSeekHostedRecorder struct{ *httptest.ResponseRecorder }

func (r *deepSeekHostedRecorder) WriteString(v string) (int, error) { return r.Write([]byte(v)) }
func (r *deepSeekHostedRecorder) Status() int                       { return r.Code }
func (r *deepSeekHostedRecorder) Size() int                         { return r.Body.Len() }
func (r *deepSeekHostedRecorder) Written() bool                     { return r.Body.Len() > 0 }
func (r *deepSeekHostedRecorder) WriteHeaderNow() {
	if r.Code == 0 {
		r.WriteHeader(http.StatusOK)
	}
}
func (r *deepSeekHostedRecorder) Pusher() http.Pusher      { return nil }
func (r *deepSeekHostedRecorder) CloseNotify() <-chan bool { return make(chan bool) }
func (r *deepSeekHostedRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, errors.New("hosted search recorder does not support hijacking")
}

func deepSeekHostedRemoveTool(body []byte) ([]byte, error) {
	return deepSeekHostedRemoveToolWithFetch(body, true)
}

func deepSeekHostedRemoveToolWithFetch(body []byte, ownFetch bool) ([]byte, error) {
	tools := make([]json.RawMessage, 0)
	for _, tool := range gjson.GetBytes(body, "tools").Array() {
		if !deepSeekHostedOwnedToolName(tool.Get("name").String(), ownFetch) {
			tools = append(tools, json.RawMessage(tool.Raw))
		}
	}
	out, err := sjson.SetBytes(body, "tools", tools)
	if err != nil {
		return nil, err
	}
	if len(tools) == 0 {
		out, err = sjson.DeleteBytes(out, "parallel_tool_calls")
		if err != nil {
			return nil, err
		}
	}
	choice := gjson.GetBytes(out, "tool_choice")
	if deepSeekHostedOwnedToolName(choice.Get("name").String(), ownFetch) || choice.String() == "required" {
		out, err = sjson.SetBytes(out, "tool_choice", "auto")
	}
	return out, err
}

func (w *deepSeekHostedRoundWriter) Header() http.Header  { return w.header }
func (w *deepSeekHostedRoundWriter) WriteHeader(code int) { w.status = code }
func (w *deepSeekHostedRoundWriter) WriteHeaderNow() {
	if w.size < 0 {
		w.size = 0
	}
}
func (w *deepSeekHostedRoundWriter) Status() int                       { return w.status }
func (w *deepSeekHostedRoundWriter) Size() int                         { return w.size }
func (w *deepSeekHostedRoundWriter) Written() bool                     { return w.size >= 0 }
func (w *deepSeekHostedRoundWriter) WriteString(v string) (int, error) { return w.Write([]byte(v)) }
func (w *deepSeekHostedRoundWriter) Flush() {
	if w.state.c.Writer.Written() {
		w.state.c.Writer.Flush()
	}
}
func (w *deepSeekHostedRoundWriter) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if w.size < 0 {
		w.size = 0
	}
	w.size += len(p)
	w.buffer = append(w.buffer, p...)
	if len(w.buffer) > deepSeekHostedMaxFrameBytes {
		w.err = errors.New("deepseek hosted response frame exceeds size limit")
		return 0, w.err
	}
	if strings.Contains(w.header.Get("Content-Type"), "text/event-stream") {
		for {
			index, separator := bytes.Index(w.buffer, []byte("\n\n")), 2
			if crlf := bytes.Index(w.buffer, []byte("\r\n\r\n")); crlf >= 0 && (index < 0 || crlf < index) {
				index, separator = crlf, 4
			}
			if index < 0 {
				break
			}
			frame := append([]byte(nil), w.buffer[:index]...)
			w.buffer = w.buffer[index+separator:]
			if err := w.frame(frame); err != nil {
				w.err = err
				return 0, err
			}
		}
	}
	return len(p), nil
}

func (w *deepSeekHostedRoundWriter) finish() {
	if w.err != nil {
		return
	}
	if strings.Contains(w.header.Get("Content-Type"), "text/event-stream") {
		if len(bytes.TrimSpace(w.buffer)) > 0 {
			w.err = w.frame(w.buffer)
			w.buffer = nil
		}
		if w.err == nil && (len(w.response) == 0 || !deepSeekHostedTerminal(w.terminal)) {
			w.err = errors.New("deepseek upstream stream ended without a terminal response")
		}
	} else if w.status >= http.StatusBadRequest {
		return
	} else if gjson.ValidBytes(w.buffer) && gjson.GetBytes(w.buffer, "output").IsArray() {
		w.response = append([]byte(nil), w.buffer...)
		w.terminal = "response." + gjson.GetBytes(w.buffer, "status").String()
		if !deepSeekHostedTerminal(w.terminal) {
			w.err = errors.New("deepseek upstream JSON response has no valid terminal status")
			return
		}
		w.registerSnapshotCalls()
	} else {
		w.err = errors.New("deepseek upstream did not return a Responses response")
	}
}

func deepSeekHostedTerminal(kind string) bool {
	return kind == "response.completed" || kind == "response.done" || kind == "response.failed" || kind == "response.incomplete"
}

func (w *deepSeekHostedRoundWriter) index(original int) int {
	if index, ok := w.indices[original]; ok {
		return index
	}
	index := len(w.state.outputs)
	w.state.outputs = append(w.state.outputs, nil)
	w.indices[original] = index
	return index
}

func (w *deepSeekHostedRoundWriter) ownedCall(item gjson.Result, original int) *deepSeekHostedCall {
	id := item.Get("id").String()
	callID := item.Get("call_id").String()
	index := w.index(original)
	call := w.owned[id]
	if call == nil {
		call = w.ownedIndices[original]
	}
	if call != nil {
		if call.index != index || call.callID != "" && callID != "" && call.callID != callID ||
			call.name != item.Get("name").String() {
			w.err = errors.New("deepseek hosted search changed a tool call identity within one output slot")
			return call
		}
		// Native DeepSeek changes item.id in the terminal snapshot. The output
		// slot and call_id remain the same logical operation; retain live ids as
		// aliases while keeping the original upstream snapshot for continuation.
		if id != "" {
			w.owned[id] = call
		}
		if args := item.Get("arguments"); args.Exists() {
			call.arguments = args.String()
		}
		if callID != "" {
			call.callID = callID
		}
		return call
	}
	call = &deepSeekHostedCall{itemID: id, callID: callID, name: item.Get("name").String(), arguments: item.Get("arguments").String(), index: index, searchID: "ws_" + strings.ReplaceAll(uuid.NewString(), "-", ""), status: "in_progress"}
	if w.ownedIndices == nil {
		w.ownedIndices = make(map[int]*deepSeekHostedCall)
	}
	w.ownedIndices[original] = call
	w.owned[id] = call
	w.calls = append(w.calls, call)
	if w.state.stream {
		w.err = w.state.event(map[string]any{"type": "response.output_item.added", "output_index": call.index, "item": deepSeekHostedSearchItem(call)})
		if w.err == nil {
			w.err = w.state.event(map[string]any{"type": "response.web_search_call.in_progress", "output_index": call.index, "item_id": call.searchID})
		}
	}
	return call
}

func (w *deepSeekHostedRoundWriter) validateOwnedSlot(item gjson.Result, original int) error {
	call := w.ownedIndices[original]
	if call == nil {
		return nil
	}
	if item.Get("type").String() != "function_call" || item.Get("name").String() != call.name ||
		(call.callID != "" && item.Get("call_id").String() != call.callID) {
		return errors.New("deepseek hosted search changed a tool call identity within one output slot")
	}
	return nil
}

func (w *deepSeekHostedRoundWriter) frame(frame []byte) error {
	var data []string
	for _, line := range strings.Split(string(frame), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if len(data) == 0 {
		if w.state.stream && w.state.c.Writer.Written() {
			_, err := w.state.c.Writer.Write(append(frame, '\n', '\n'))
			return err
		}
		return nil
	}
	raw := []byte(strings.Join(data, "\n"))
	if bytes.Equal(raw, []byte("[DONE]")) {
		w.done = true
		return nil
	}
	if !gjson.ValidBytes(raw) {
		return errors.New("deepseek hosted search received invalid SSE JSON")
	}
	kind := gjson.GetBytes(raw, "type").String()
	if kind == "" || (!strings.HasPrefix(kind, "response.") && kind != "error") {
		return errors.New("deepseek upstream returned an unsupported SSE event")
	}
	w.state.header = w.header.Clone()
	if kind == "response.created" || kind == "response.in_progress" {
		id := gjson.GetBytes(raw, "response.id").String()
		if w.state.responseID != "" {
			return nil
		}
		w.state.responseID = id
		return w.state.rawEvent(raw)
	}
	if deepSeekHostedTerminal(kind) {
		response := gjson.GetBytes(raw, "response")
		if !response.IsObject() {
			return errors.New("deepseek upstream terminal event has no response body")
		}
		w.response = []byte(response.Raw)
		w.terminal = kind
		w.registerSnapshotCalls()
		return w.err
	}
	item := gjson.GetBytes(raw, "item")
	if item.IsObject() || kind == "response.output_item.added" || kind == "response.output_item.done" {
		if err := w.validateOwnedSlot(item, int(gjson.GetBytes(raw, "output_index").Int())); err != nil {
			return err
		}
	}
	if item.Get("type").String() == "function_call" && deepSeekHostedOwnedToolName(item.Get("name").String(), !w.state.skipFetchTool) {
		w.ownedCall(item, int(gjson.GetBytes(raw, "output_index").Int()))
		return w.err
	}
	itemID := gjson.GetBytes(raw, "item_id").String()
	call := w.owned[itemID]
	if call == nil && (kind == "response.function_call_arguments.delta" || kind == "response.function_call_arguments.done") {
		if original := gjson.GetBytes(raw, "output_index"); original.Exists() {
			call = w.ownedIndices[int(original.Int())]
		}
		if call != nil && itemID != "" {
			w.owned[itemID] = call
		}
	}
	if call != nil {
		if kind == "response.function_call_arguments.delta" {
			call.arguments += gjson.GetBytes(raw, "delta").String()
		}
		if kind == "response.function_call_arguments.done" {
			call.arguments = gjson.GetBytes(raw, "arguments").String()
		}
		return nil
	}
	if oi := gjson.GetBytes(raw, "output_index"); oi.Exists() {
		original := int(oi.Int())
		index := w.index(original)
		if w.canonicalIDs == nil {
			w.canonicalIDs = make(map[int]string)
		}
		if id := item.Get("id").String(); id != "" && w.canonicalIDs[original] == "" {
			w.canonicalIDs[original] = id
		}
		if canonical := w.canonicalIDs[original]; canonical != "" {
			var err error
			if item.Get("id").Exists() {
				raw, err = sjson.SetBytes(raw, "item.id", canonical)
				if err != nil {
					return err
				}
			}
			if gjson.GetBytes(raw, "item_id").Exists() {
				raw, err = sjson.SetBytes(raw, "item_id", canonical)
				if err != nil {
					return err
				}
			}
			item = gjson.GetBytes(raw, "item")
		}
		if kind == "response.output_item.done" && item.IsObject() {
			w.state.outputs[index] = json.RawMessage(item.Raw)
		}
		var err error
		raw, err = sjson.SetBytes(raw, "output_index", index)
		if err != nil {
			return err
		}
	}
	if kind == "response.output_text.delta" && w.state.firstTokenMs == nil && gjson.GetBytes(raw, "delta").String() != "" {
		ms := int(time.Since(w.state.started).Milliseconds())
		w.state.firstTokenMs = &ms
	}
	if !w.state.stream {
		return nil
	}
	return w.state.rawEvent(raw)
}

func (w *deepSeekHostedRoundWriter) registerSnapshotCalls() {
	items := gjson.GetBytes(w.response, "output").Array()
	for original := range w.ownedIndices {
		if original >= len(items) {
			w.err = errors.New("deepseek hosted search lost an owned tool call in the terminal response")
			return
		}
		if err := w.validateOwnedSlot(items[original], original); err != nil {
			w.err = err
			return
		}
	}
	for i, item := range items {
		w.index(i)
		if item.Get("type").String() == "function_call" && deepSeekHostedOwnedToolName(item.Get("name").String(), !w.state.skipFetchTool) {
			w.ownedCall(item, i)
		}
	}
}

func (w *deepSeekHostedRoundWriter) captureOutput() {
	for i, item := range gjson.GetBytes(w.response, "output").Array() {
		if item.Get("type").String() == "function_call" && deepSeekHostedOwnedToolName(item.Get("name").String(), !w.state.skipFetchTool) {
			continue
		}
		index := w.index(i)
		raw := []byte(item.Raw)
		if canonical := w.canonicalIDs[i]; canonical != "" {
			if updated, err := sjson.SetBytes(raw, "id", canonical); err == nil {
				raw = updated
			}
		}
		w.state.outputs[index] = json.RawMessage(raw)
	}
	if w.state.responseID == "" {
		w.state.responseID = gjson.GetBytes(w.response, "id").String()
	}
}

func (w *deepSeekHostedRoundWriter) hasClientCalls() bool {
	for _, item := range gjson.GetBytes(w.response, "output").Array() {
		t := item.Get("type").String()
		if t == "function_call" && !deepSeekHostedOwnedToolName(item.Get("name").String(), !w.state.skipFetchTool) || t == "custom_tool_call" || t == "local_shell_call" || t == "tool_search_call" {
			return true
		}
	}
	return false
}

func (w *deepSeekHostedRoundWriter) writeBufferedError() error {
	if w.state.c.Writer.Written() {
		return nil
	}
	for key, values := range w.header {
		if !strings.EqualFold(key, "Content-Length") {
			w.state.c.Writer.Header()[key] = values
		}
	}
	MarkResponseCommitted(w.state.c)
	w.state.c.Status(w.status)
	_, err := w.state.c.Writer.Write(w.buffer)
	if err != nil {
		w.state.clientDisconnected = true
	}
	return err
}

func deepSeekHostedSearchItem(call *deepSeekHostedCall) map[string]any {
	action := map[string]any{"type": "search", "query": call.query}
	if isDeepSeekHostedFetchToolName(call.name) {
		action = map[string]any{"type": "open_page"}
		if call.url != "" {
			action["url"] = call.url
		}
	}
	if len(call.sources) > 0 {
		action["sources"] = call.sources
	}
	return map[string]any{"type": "web_search_call", "id": call.searchID, "status": call.status, "action": action}
}

func (s *deepSeekHostedSearchState) completeSearch(call *deepSeekHostedCall) error {
	item := deepSeekHostedSearchItem(call)
	raw, err := json.Marshal(item)
	if err != nil {
		return err
	}
	s.outputs[call.index] = raw
	if !s.stream {
		return nil
	}
	if call.status == "completed" {
		if err := s.event(map[string]any{"type": "response.web_search_call.completed", "item_id": call.searchID, "output_index": call.index}); err != nil {
			return err
		}
	}
	return s.event(map[string]any{"type": "response.output_item.done", "output_index": call.index, "item": item})
}

func (s *deepSeekHostedSearchState) rawEvent(raw []byte) error {
	if !s.stream {
		return nil
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	return s.event(value)
}

func (s *deepSeekHostedSearchState) event(value map[string]any) error {
	if !s.stream {
		return nil
	}
	value["sequence_number"] = s.sequence
	s.sequence++
	if response, ok := value["response"].(map[string]any); ok && s.responseID != "" {
		response["id"] = s.responseID
	}
	if _, ok := value["response_id"]; ok && s.responseID != "" {
		value["response_id"] = s.responseID
	}
	if !s.c.Writer.Written() {
		for key, values := range s.header {
			if !strings.EqualFold(key, "Content-Length") && !strings.EqualFold(key, "Content-Encoding") {
				s.c.Writer.Header()[key] = values
			}
		}
		s.c.Header("Content-Type", "text/event-stream")
		s.c.Header("Cache-Control", "no-cache")
		s.c.Status(http.StatusOK)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(s.c.Writer, "event: %s\ndata: %s\n\n", value["type"], raw)
	if err == nil {
		s.c.Writer.Flush()
	} else {
		s.clientDisconnected = true
	}
	return err
}

func (s *deepSeekHostedSearchState) addResult(result *OpenAIForwardResult) {
	if result == nil {
		return
	}
	if s.result == nil {
		copy := *result
		if result.NonBillableUpstreamError {
			copy.Usage = OpenAIUsage{}
		}
		s.result = &copy
		return
	}
	if !result.NonBillableUpstreamError {
		s.result.Usage.InputTokens += result.Usage.InputTokens
		s.result.Usage.OutputTokens += result.Usage.OutputTokens
		s.result.Usage.CacheReadInputTokens += result.Usage.CacheReadInputTokens
		s.result.Usage.CacheCreationInputTokens += result.Usage.CacheCreationInputTokens
		s.result.Usage.ImageInputTokens += result.Usage.ImageInputTokens
		s.result.Usage.ImageOutputTokens += result.Usage.ImageOutputTokens
	}
	s.result.ClientDisconnect = s.result.ClientDisconnect || result.ClientDisconnect
	s.result.NonBillableUpstreamError = s.result.NonBillableUpstreamError && result.NonBillableUpstreamError
	if result.RequestID != "" {
		s.result.RequestID = result.RequestID
	}
}

func (s *deepSeekHostedSearchState) finishResult() *OpenAIForwardResult {
	if s.result == nil && (s.searchCount > 0 || s.clientDisconnected) {
		s.result = &OpenAIForwardResult{Model: s.model, Stream: s.stream}
	}
	if s.result == nil {
		return nil
	}
	s.result.SearchCount = s.searchCount
	s.result.FixedToolBilling = true
	s.result.WebSearchCalls = 0
	s.result.ClientDisconnect = s.result.ClientDisconnect || s.clientDisconnected
	if s.searchCount > 0 {
		s.result.NonBillableUpstreamError = false
	}
	s.result.Duration = time.Since(s.started)
	s.result.ResponseID = s.responseID
	if s.firstTokenMs != nil {
		s.result.FirstTokenMs = s.firstTokenMs
	}
	return s.result
}

func (s *deepSeekHostedSearchState) mergeUsage(usage gjson.Result) {
	if !usage.IsObject() {
		return
	}
	var current map[string]any
	if json.Unmarshal([]byte(usage.Raw), &current) != nil {
		return
	}
	if s.usage == nil {
		s.usage = make(map[string]any)
	}
	deepSeekHostedAddUsage(s.usage, current)
}

func deepSeekHostedAddUsage(dst, src map[string]any) {
	for key, value := range src {
		if number, ok := value.(float64); ok {
			previous, _ := dst[key].(float64)
			dst[key] = previous + number
			continue
		}
		if object, ok := value.(map[string]any); ok {
			target, _ := dst[key].(map[string]any)
			if target == nil {
				target = make(map[string]any)
				dst[key] = target
			}
			deepSeekHostedAddUsage(target, object)
		} else if _, exists := dst[key]; !exists {
			dst[key] = value
		}
	}
}

func (s *deepSeekHostedSearchState) writeFinal(w *deepSeekHostedRoundWriter) error {
	response := w.response
	var outputs []json.RawMessage
	for _, item := range s.outputs {
		if len(item) > 0 {
			outputs = append(outputs, item)
		}
	}
	var err error
	response, err = sjson.SetBytes(response, "output", outputs)
	if err != nil {
		return err
	}
	if s.responseID != "" {
		response, err = sjson.SetBytes(response, "id", s.responseID)
		if err != nil {
			return err
		}
	}
	if s.usage != nil {
		response, err = sjson.SetBytes(response, "usage", s.usage)
		if err != nil {
			return err
		}
	}
	if !s.stream {
		for key, values := range w.header {
			if !strings.EqualFold(key, "Content-Length") && !strings.EqualFold(key, "Content-Encoding") {
				s.c.Writer.Header()[key] = values
			}
		}
		s.c.Header("Content-Type", "application/json")
		MarkResponseCommitted(s.c)
		s.c.Status(w.status)
		_, writeErr := s.c.Writer.Write(response)
		if writeErr != nil {
			s.clientDisconnected = true
		}
		return writeErr
	}
	kind := w.terminal
	if kind == "" || kind == "response.done" {
		kind = "response.completed"
	}
	var object map[string]any
	if err := json.Unmarshal(response, &object); err != nil {
		return err
	}
	MarkResponseCommitted(s.c)
	if err := s.event(map[string]any{"type": kind, "response": object}); err != nil {
		return err
	}
	if w.done {
		_, err = s.c.Writer.Write([]byte("data: [DONE]\n\n"))
		if err != nil {
			s.clientDisconnected = true
		}
		s.c.Writer.Flush()
	}
	return err
}

func (s *deepSeekHostedSearchState) writeFailure() error {
	output := make([]json.RawMessage, 0, len(s.outputs))
	for _, item := range s.outputs {
		if len(item) > 0 {
			output = append(output, item)
		}
	}
	MarkResponseCommitted(s.c)
	return s.event(map[string]any{"type": "response.failed", "response": map[string]any{
		"id": s.responseID, "object": "response", "created_at": s.started.Unix(), "model": s.model,
		"status": "failed", "output": output, "usage": s.usage,
		"error": map[string]string{"code": "server_error", "message": "Hosted web search could not complete this response."},
	}})
}
