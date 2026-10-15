package service

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// deepSeekPassthrough moves DeepSeek <think>/<thinking> and DSML tool calls
// out of Responses output_text. Native /v1/responses accounts skip the chat bridge.
type deepSeekPassthrough struct {
	filter         apicompat.DeepSeekInlineFilter
	nextIndex      int
	reasoningOpen  bool
	reasoningID    string
	reasoningIndex int
	reasoningText  strings.Builder
	calls          []apicompat.DeepSeekCall
	callIndex      map[string]int
	callItemID     map[string]string
	droppedIndex   map[int]struct{}
}

func (st *deepSeekPassthrough) rewrite(c *gin.Context, eventType string, data []byte) (before, after []string, next []byte, drop bool) {
	st.note(data)
	if st.droppedCallEvent(data) || st.noteCallEvent(eventType, data) {
		return nil, nil, nil, true
	}
	separated := separateDeepSeekCallItemID(data)
	changedID := !bytes.Equal(separated, data)
	data = separated
	switch eventType {
	case "response.output_text.delta":
		delta := gjson.GetBytes(data, "delta").String()
		piece := st.filter.Push(delta)
		before, after = st.events(c, piece)
		if piece.Text == delta && len(before) == 0 && len(after) == 0 && !changedID {
			return nil, nil, nil, false
		}
		if piece.Text == "" {
			return before, after, nil, true
		}
		updated, err := sjson.SetBytes(data, "delta", piece.Text)
		if err != nil {
			return before, after, nil, false
		}
		return before, after, updated, false
	case "response.output_text.done":
		before = st.flush(c)
		updated, calls, changed := rewriteDeepSeekTextField(data, "text")
		_, toolLines := st.events(c, apicompat.DeepSeekPiece{Calls: calls})
		before = append(before, toolLines...)
		if !changed && !changedID {
			return before, after, nil, false
		}
		return before, after, updated, false
	case "response.content_part.done":
		before = st.flush(c)
		updated, calls, changed := rewriteDeepSeekTextField(data, "part.text")
		_, toolLines := st.events(c, apicompat.DeepSeekPiece{Calls: calls})
		before = append(before, toolLines...)
		if !changed && !changedID {
			return before, after, nil, false
		}
		return before, after, updated, false
	case "response.output_item.added":
		if !changedID {
			return nil, nil, nil, false
		}
		return nil, nil, data, false
	case "response.output_item.done":
		before = st.flush(c)
		updated, calls, changed := rewriteDeepSeekContentTexts(data, "item.content")
		_, toolLines := st.events(c, apicompat.DeepSeekPiece{Calls: calls})
		before = append(before, toolLines...)
		if !changed && !changedID {
			return before, after, nil, false
		}
		if !changed {
			updated = data
		}
		return before, after, updated, false
	case "response.completed", "response.done":
		before = st.flush(c)
		updated, calls, changed := rewriteDeepSeekContentTexts(data, "response.output")
		_, toolLines := st.events(c, apicompat.DeepSeekPiece{Calls: calls})
		before = append(before, toolLines...)
		updated = st.appendMissingCalls(c, updated, "response.output")
		updated = sanitizeResponsesCallIDs(updated, "response.output")
		if bytes.Equal(updated, data) && !changed && !changedID && len(before) == 0 && len(after) == 0 {
			return nil, nil, nil, false
		}
		return before, after, updated, false
	default:
		if !changedID {
			return nil, nil, nil, false
		}
		return nil, nil, data, false
	}
}

func rewriteDeepSeekResponseBody(c *gin.Context, body []byte) []byte {
	if len(body) == 0 {
		return body
	}
	if gjson.GetBytes(body, "output").IsArray() {
		body = sanitizeResponsesCallIDs(body, "output")
	}
	if !bytes.Contains(body, []byte("DSML")) && !bytes.Contains(body, []byte("<think")) {
		return body
	}
	if !gjson.GetBytes(body, "output").IsArray() {
		return body
	}
	var st deepSeekPassthrough
	updated, calls, _ := rewriteDeepSeekContentTexts(body, "output")
	_, _ = st.events(c, apicompat.DeepSeekPiece{Calls: calls})
	updated = st.appendMissingCalls(c, updated, "output")
	restored, err := restoreOpenAIResponsesClientToolPayload(c, updated)
	if err != nil {
		return updated
	}
	return restored
}

func (st *deepSeekPassthrough) flush(c *gin.Context) []string {
	before, after := st.events(c, st.filter.Flush())
	return append(append(before, after...), st.closeReasoning(c)...)
}

func (st *deepSeekPassthrough) events(c *gin.Context, piece apicompat.DeepSeekPiece) (before, after []string) {
	if piece.Reasoning != "" {
		before = append(before, st.openReasoning(c, piece.Reasoning)...)
	}
	if piece.Text != "" || len(piece.Calls) > 0 {
		before = append(before, st.closeReasoning(c)...)
	}
	after = st.toolEvents(c, st.remember(piece.Calls))
	return before, after
}

func (st *deepSeekPassthrough) note(data []byte) {
	if oi := gjson.GetBytes(data, "output_index"); oi.Exists() {
		if n := int(oi.Int()) + 1; n > st.nextIndex {
			st.nextIndex = n
		}
	}
	if n := int(gjson.GetBytes(data, "response.output.#").Int()); n > st.nextIndex {
		st.nextIndex = n
	}
	if n := int(gjson.GetBytes(data, "output.#").Int()); n > st.nextIndex {
		st.nextIndex = n
	}
}

func (st *deepSeekPassthrough) alloc() int {
	n := st.nextIndex
	st.nextIndex++
	return n
}

func (st *deepSeekPassthrough) openReasoning(c *gin.Context, text string) []string {
	var lines []string
	if !st.reasoningOpen {
		st.reasoningOpen = true
		st.reasoningID = "rs_" + deepSeekRand()
		st.reasoningIndex = st.alloc()
		lines = append(lines, deepSeekSSE(c, map[string]any{
			"type":         "response.output_item.added",
			"output_index": st.reasoningIndex,
			"item": map[string]any{
				"type":    "reasoning",
				"id":      st.reasoningID,
				"status":  "in_progress",
				"summary": []any{},
			},
		})...)
		lines = append(lines, deepSeekSSE(c, map[string]any{
			"type":          "response.reasoning_summary_part.added",
			"output_index":  st.reasoningIndex,
			"summary_index": 0,
			"item_id":       st.reasoningID,
			"part":          map[string]any{"type": "summary_text", "text": ""},
		})...)
	}
	_, _ = st.reasoningText.WriteString(text)
	return append(lines, deepSeekSSE(c, map[string]any{
		"type":          "response.reasoning_summary_text.delta",
		"output_index":  st.reasoningIndex,
		"summary_index": 0,
		"item_id":       st.reasoningID,
		"delta":         text,
	})...)
}

func (st *deepSeekPassthrough) closeReasoning(c *gin.Context) []string {
	if !st.reasoningOpen {
		return nil
	}
	st.reasoningOpen = false
	text := st.reasoningText.String()
	return append(deepSeekSSE(c, map[string]any{
		"type":          "response.reasoning_summary_text.done",
		"output_index":  st.reasoningIndex,
		"summary_index": 0,
		"item_id":       st.reasoningID,
		"text":          text,
	}), deepSeekSSE(c, map[string]any{
		"type":         "response.output_item.done",
		"output_index": st.reasoningIndex,
		"item": map[string]any{
			"type":   "reasoning",
			"id":     st.reasoningID,
			"status": "completed",
			"summary": []any{map[string]any{
				"type": "summary_text",
				"text": text,
			}},
		},
	})...)
}

func (st *deepSeekPassthrough) remember(calls []apicompat.DeepSeekCall) []apicompat.DeepSeekCall {
	var fresh []apicompat.DeepSeekCall
	for _, call := range calls {
		call.Name = strings.TrimSpace(call.Name)
		if call.Name == "" || st.seen(call) {
			continue
		}
		if strings.TrimSpace(call.Arguments) == "" {
			call.Arguments = "{}"
		}
		if call.CallID == "" {
			call.CallID = "call_" + deepSeekRand()
		}
		st.calls = append(st.calls, call)
		fresh = append(fresh, call)
	}
	return fresh
}

func (st *deepSeekPassthrough) seen(call apicompat.DeepSeekCall) bool {
	for _, have := range st.calls {
		if call.CallID != "" && have.CallID == call.CallID {
			return true
		}
		if have.Name == call.Name && have.Arguments == call.Arguments {
			return true
		}
	}
	return false
}

func (st *deepSeekPassthrough) toolEvents(c *gin.Context, calls []apicompat.DeepSeekCall) []string {
	var lines []string
	for _, call := range calls {
		idx := st.alloc()
		itemID := "fc_" + deepSeekRand()
		lines = append(lines, deepSeekSSE(c, map[string]any{
			"type":         "response.output_item.added",
			"output_index": idx,
			"item": map[string]any{
				"type":      "function_call",
				"id":        itemID,
				"call_id":   call.CallID,
				"name":      call.Name,
				"arguments": "",
				"status":    "in_progress",
			},
		})...)
		lines = append(lines, deepSeekSSE(c, map[string]any{
			"type":         "response.function_call_arguments.delta",
			"output_index": idx,
			"item_id":      itemID,
			"call_id":      call.CallID,
			"name":         call.Name,
			"delta":        call.Arguments,
		})...)
		lines = append(lines, deepSeekSSE(c, map[string]any{
			"type":         "response.function_call_arguments.done",
			"output_index": idx,
			"item_id":      itemID,
			"call_id":      call.CallID,
			"name":         call.Name,
			"arguments":    call.Arguments,
		})...)
		lines = append(lines, deepSeekSSE(c, map[string]any{
			"type":         "response.output_item.done",
			"output_index": idx,
			"item": map[string]any{
				"type":      "function_call",
				"id":        itemID,
				"call_id":   call.CallID,
				"name":      call.Name,
				"arguments": call.Arguments,
				"status":    "completed",
			},
		})...)
	}
	return lines
}

func (st *deepSeekPassthrough) appendMissingCalls(c *gin.Context, data []byte, path string) []byte {
	if !gjson.GetBytes(data, path).IsArray() {
		return data
	}
	for _, call := range st.calls {
		if deepSeekOutputHasCall(data, path, call) {
			continue
		}
		raw := deepSeekJSON(map[string]any{
			"type":      "function_call",
			"id":        "fc_" + deepSeekRand(),
			"call_id":   call.CallID,
			"name":      call.Name,
			"arguments": call.Arguments,
			"status":    "completed",
		})
		if restored, err := restoreOpenAIResponsesClientToolPayload(c, raw); err == nil {
			raw = restored
		}
		next, err := sjson.SetRawBytes(data, path+".-1", raw)
		if err != nil {
			return data
		}
		data = next
	}
	return data
}

func deepSeekOutputHasCall(data []byte, path string, call apicompat.DeepSeekCall) bool {
	found := false
	gjson.GetBytes(data, path).ForEach(func(_, item gjson.Result) bool {
		if call.CallID != "" && item.Get("call_id").String() == call.CallID {
			found = true
			return false
		}
		if item.Get("name").String() == call.Name && item.Get("arguments").String() == call.Arguments {
			found = true
			return false
		}
		return true
	})
	return found
}

func rewriteDeepSeekTextField(data []byte, path string) ([]byte, []apicompat.DeepSeekCall, bool) {
	val := gjson.GetBytes(data, path)
	if !val.Exists() || val.Type != gjson.String {
		return data, nil, false
	}
	visible, _, calls := apicompat.ExtractDeepSeekInline(val.String())
	if visible == val.String() {
		return data, calls, false
	}
	next, err := sjson.SetBytes(data, path, visible)
	if err != nil {
		return data, calls, false
	}
	return next, calls, true
}

func rewriteDeepSeekContentTexts(data []byte, path string) ([]byte, []apicompat.DeepSeekCall, bool) {
	node := gjson.GetBytes(data, path)
	if !node.Exists() {
		return data, nil, false
	}
	changed := false
	var calls []apicompat.DeepSeekCall
	if node.IsArray() && strings.HasSuffix(path, "content") {
		node.ForEach(func(i, part gjson.Result) bool {
			if part.Get("type").String() != "output_text" {
				return true
			}
			next, more, ok := rewriteDeepSeekTextField(data, fmt.Sprintf("%s.%d.text", path, i.Int()))
			calls = append(calls, more...)
			if ok {
				data = next
				changed = true
			}
			return true
		})
		return data, calls, changed
	}
	if !node.IsArray() {
		return data, nil, false
	}
	node.ForEach(func(i, item gjson.Result) bool {
		if item.Get("type").String() != "message" {
			return true
		}
		next, more, ok := rewriteDeepSeekContentTexts(data, fmt.Sprintf("%s.%d.content", path, i.Int()))
		calls = append(calls, more...)
		if ok {
			data = next
			changed = true
		}
		return true
	})
	return data, calls, changed
}

func deepSeekSSE(c *gin.Context, payload map[string]any) []string {
	raw := deepSeekJSON(payload)
	if restored, err := restoreOpenAIResponsesClientToolPayload(c, raw); err == nil {
		raw = restored
	}
	return []string{"event: " + gjson.GetBytes(raw, "type").String(), "data: " + string(raw), ""}
}

func deepSeekJSON(payload any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(payload); err != nil {
		return []byte("{}")
	}
	return bytes.TrimRight(buf.Bytes(), "\n")
}

func deepSeekRand() string {
	buf := make([]byte, 8)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

func (st *deepSeekPassthrough) noteCallEvent(eventType string, data []byte) bool {
	if eventType != "response.output_item.added" && eventType != "response.output_item.done" {
		return false
	}
	callID := strings.TrimSpace(gjson.GetBytes(data, "item.call_id").String())
	if callID == "" {
		return false
	}
	idxResult := gjson.GetBytes(data, "output_index")
	idx := -1
	if idxResult.Exists() {
		idx = int(idxResult.Int())
	}
	itemID := strings.TrimSpace(gjson.GetBytes(data, "item.id").String())
	if st.callIndex == nil {
		st.callIndex = map[string]int{}
		st.callItemID = map[string]string{}
	}
	prevIdx, seen := st.callIndex[callID]
	if !seen {
		st.callIndex[callID] = idx
		st.callItemID[callID] = itemID
		return false
	}
	if idx >= 0 && prevIdx == idx {
		return false
	}
	if itemID != "" && st.callItemID[callID] == itemID {
		return false
	}
	if st.droppedIndex == nil {
		st.droppedIndex = map[int]struct{}{}
	}
	if idx >= 0 {
		st.droppedIndex[idx] = struct{}{}
	}
	return true
}

func (st *deepSeekPassthrough) droppedCallEvent(data []byte) bool {
	if len(st.droppedIndex) == 0 {
		return false
	}
	idx := gjson.GetBytes(data, "output_index")
	if !idx.Exists() {
		return false
	}
	_, ok := st.droppedIndex[int(idx.Int())]
	return ok
}

func separateDeepSeekCallItemID(data []byte) []byte {
	itemID := gjson.GetBytes(data, "item.id").String()
	callID := gjson.GetBytes(data, "item.call_id").String()
	if callID != "" && itemID == callID {
		if next, err := sjson.SetBytes(data, "item.id", "fc_"+strings.TrimPrefix(callID, "call_")); err == nil {
			data = next
		}
	}
	itemID = gjson.GetBytes(data, "item_id").String()
	callID = gjson.GetBytes(data, "call_id").String()
	if callID != "" && itemID == callID {
		if next, err := sjson.SetBytes(data, "item_id", "fc_"+strings.TrimPrefix(callID, "call_")); err == nil {
			data = next
		}
	}
	return data
}

func sanitizeResponsesCallIDs(data []byte, path string) []byte {
	node := gjson.GetBytes(data, path)
	if !node.IsArray() {
		return data
	}
	var input any
	if err := json.Unmarshal([]byte(node.Raw), &input); err != nil {
		return data
	}
	next, changed := apicompat.DedupeResponsesCallIDs(input)
	if !changed {
		return data
	}
	raw, err := json.Marshal(next)
	if err != nil {
		return data
	}
	updated, err := sjson.SetRawBytes(data, path, raw)
	if err != nil {
		return data
	}
	return updated
}
