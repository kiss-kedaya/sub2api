package apicompat

import "strings"

// DedupeResponsesCallIDs drops repeated tool-call ids as one surviving pair.
//
// One call_id keeps a single call and a single output. Extra copies on either
// side go away together, so the call that stays still has its output. The
// survivor is the earliest top-level item. When that copy has empty arguments
// and a later copy has real ones, the real arguments are copied over before
// the extra copy is dropped. A single call with no output is left alone.
//
// Nested message content is included. Codex stores a second copy there and
// also sets item id equal to call_id. DeepSeek counts both, so a kept call
// whose id and call_id are the same string is split (id becomes fc_...).
func DedupeResponsesCallIDs(input any) (any, bool) {
	return dedupeResponsesCallIDs(input, true)
}

// DedupeResponsesCallIDsKeepItemIDs drops duplicate call and output items but
// does not rewrite item.id. Live SSE already published those ids on
// output_item.added and on argument deltas.
func DedupeResponsesCallIDsKeepItemIDs(input any) (any, bool) {
	return dedupeResponsesCallIDs(input, false)
}

func dedupeResponsesCallIDs(input any, rewriteIDs bool) (any, bool) {
	items, ok := input.([]any)
	if !ok || len(items) == 0 {
		return input, false
	}
	var hits []trackedCall
	var seq int
	collectTrackedCalls(items, true, &hits, &seq)
	if len(hits) == 0 {
		return input, false
	}
	keep := map[int]struct{}{}
	grafted := map[int]struct{}{}
	planCallDedupe(hits, keep, grafted)
	st := &callDedupeState{rewriteIDs: rewriteIDs, keep: keep, grafted: grafted}
	out, changed := filterTrackedCalls(items, st)
	if !changed {
		return input, false
	}
	return out, true
}

type trackedCall struct {
	seq    int
	callID string
	slot   string
	item   map[string]any
	top    bool
}

type callDedupeState struct {
	seq        int
	rewriteIDs bool
	keep       map[int]struct{}
	grafted    map[int]struct{}
}

func collectTrackedCalls(items []any, top bool, hits *[]trackedCall, seq *int) {
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			if child, ok := raw.([]any); ok {
				collectTrackedCalls(child, false, hits, seq)
			}
			continue
		}
		if callID, slot, tracked := responsesCallIDSlot(item); tracked {
			*seq++
			*hits = append(*hits, trackedCall{seq: *seq, callID: callID, slot: slot, item: item, top: top})
		}
		if content, ok := item["content"].([]any); ok {
			collectTrackedCalls(content, false, hits, seq)
		}
	}
}

func planCallDedupe(hits []trackedCall, keep map[int]struct{}, grafted map[int]struct{}) {
	groups := map[string][]trackedCall{}
	order := make([]string, 0)
	for _, hit := range hits {
		key := hit.slot + "\x00" + hit.callID
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], hit)
	}
	for _, key := range order {
		group := groups[key]
		winner := group[0]
		for _, hit := range group[1:] {
			if !winner.top && hit.top {
				winner = hit
			}
		}
		keep[winner.seq] = struct{}{}
		if len(group) == 1 {
			continue
		}
		if winner.slot == "output" {
			if graftOutputPayload(winner, group) {
				grafted[winner.seq] = struct{}{}
			}
			continue
		}
		if graftCallPayload(winner, group) {
			grafted[winner.seq] = struct{}{}
		}
	}
}

func graftCallPayload(winner trackedCall, group []trackedCall) bool {
	changed := false
	for _, hit := range group {
		if hit.seq == winner.seq {
			continue
		}
		if blankArguments(stringValue(winner.item["arguments"])) && !blankArguments(stringValue(hit.item["arguments"])) {
			winner.item["arguments"] = hit.item["arguments"]
			changed = true
		}
		if strings.TrimSpace(stringValue(winner.item["name"])) == "" && strings.TrimSpace(stringValue(hit.item["name"])) != "" {
			winner.item["name"] = hit.item["name"]
			changed = true
		}
	}
	return changed
}

func graftOutputPayload(winner trackedCall, group []trackedCall) bool {
	if !blankPayload(winner.item["output"]) {
		return false
	}
	for _, hit := range group {
		if hit.seq == winner.seq || blankPayload(hit.item["output"]) {
			continue
		}
		winner.item["output"] = hit.item["output"]
		return true
	}
	return false
}

func blankArguments(raw string) bool {
	switch strings.TrimSpace(raw) {
	case "", "{}", "null":
		return true
	default:
		return false
	}
}

func blankPayload(value any) bool {
	if value == nil {
		return true
	}
	text, ok := value.(string)
	if !ok {
		return false
	}
	return blankArguments(text)
}

func filterTrackedCalls(items []any, st *callDedupeState) ([]any, bool) {
	changed := false
	out := make([]any, 0, len(items))
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			if child, ok := raw.([]any); ok {
				next, did := filterTrackedCalls(child, st)
				if did {
					changed = true
					raw = next
				}
			}
			out = append(out, raw)
			continue
		}
		tracked := false
		seq := 0
		if _, _, ok := responsesCallIDSlot(item); ok {
			st.seq++
			seq = st.seq
			tracked = true
		}
		if tracked {
			if _, ok := st.keep[seq]; !ok {
				changed = true
				if content, ok := item["content"].([]any); ok {
					_, _ = filterTrackedCalls(content, st)
				}
				continue
			}
			if _, ok := st.grafted[seq]; ok {
				changed = true
			}
			if st.rewriteIDs && separateCollidingCallItemID(item) {
				changed = true
			}
		}
		if content, ok := item["content"].([]any); ok {
			next, did := filterTrackedCalls(content, st)
			if did {
				changed = true
				item["content"] = next
			}
		}
		out = append(out, item)
	}
	if !changed {
		return items, false
	}
	return out, true
}

// separateCollidingCallItemID makes id and call_id different strings.
// DeepSeek rejects one item when both fields carry the same call_ id.
// Callers must not use this on a live SSE event. The id was already sent.
func separateCollidingCallItemID(item map[string]any) bool {
	callID := strings.TrimSpace(stringValue(item["call_id"]))
	itemID := strings.TrimSpace(stringValue(item["id"]))
	if callID == "" && strings.HasPrefix(itemID, "call_") {
		item["call_id"] = itemID
		item["id"] = "fc_" + strings.TrimPrefix(itemID, "call_")
		return true
	}
	if callID != "" && itemID == callID {
		item["id"] = "fc_" + strings.TrimPrefix(callID, "call_")
		return true
	}
	return false
}

// DedupeChatToolCallIDs drops repeated chat tool ids as complete pairs.
//
// The surviving assistant tool_calls entry and the surviving tool message of
// an id are kept. Later copies on either side are dropped together. An empty
// first copy does not beat a later copy that actually carries arguments.
// One assistant message is enough: two tool_calls with the same id inside it
// are still a duplicate.
type chatCallSlot struct {
	msgIndex  int
	callIndex int
}

func DedupeChatToolCallIDs(messages any) (any, bool) {
	items, ok := messages.([]any)
	if !ok || len(items) == 0 {
		return messages, false
	}

	calls := make(map[string][]chatCallSlot, 4)
	outputs := make(map[string][]int, 4)
	for mi, raw := range items {
		msg, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if callList, ok := msg["tool_calls"].([]any); ok {
			for ci, rawCall := range callList {
				call, ok := rawCall.(map[string]any)
				if !ok {
					continue
				}
				id := strings.TrimSpace(stringValue(call["id"]))
				if id == "" {
					continue
				}
				calls[id] = append(calls[id], chatCallSlot{msgIndex: mi, callIndex: ci})
			}
		}
		if id := strings.TrimSpace(stringValue(msg["tool_call_id"])); id != "" {
			outputs[id] = append(outputs[id], mi)
		}
	}

	dropCalls := make(map[chatCallSlot]struct{})
	dropMsgs := make(map[int]struct{})
	for id, list := range calls {
		outList := outputs[id]
		if len(list) <= 1 && len(outList) <= 1 {
			continue
		}
		keepCall := pickChatCall(items, list)
		for _, slot := range list {
			if slot != keepCall {
				dropCalls[slot] = struct{}{}
			}
		}
		if len(outList) > 1 {
			keepOut := pickChatOutput(items, outList)
			for _, mi := range outList {
				if mi != keepOut {
					dropMsgs[mi] = struct{}{}
				}
			}
		}
	}
	for id, list := range outputs {
		if _, hasCall := calls[id]; hasCall || len(list) <= 1 {
			continue
		}
		keepOut := pickChatOutput(items, list)
		for _, mi := range list {
			if mi != keepOut {
				dropMsgs[mi] = struct{}{}
			}
		}
	}
	if len(dropCalls) == 0 && len(dropMsgs) == 0 {
		return messages, false
	}

	out := make([]any, 0, len(items)-len(dropMsgs))
	for mi, raw := range items {
		if _, drop := dropMsgs[mi]; drop {
			continue
		}
		msg, ok := raw.(map[string]any)
		if !ok {
			out = append(out, raw)
			continue
		}
		if callList, ok := msg["tool_calls"].([]any); ok {
			kept := make([]any, 0, len(callList))
			for ci, rawCall := range callList {
				if _, drop := dropCalls[chatCallSlot{msgIndex: mi, callIndex: ci}]; drop {
					continue
				}
				kept = append(kept, rawCall)
			}
			if len(kept) != len(callList) {
				if len(kept) == 0 {
					delete(msg, "tool_calls")
					if !chatMessageHasVisibleContent(msg) {
						continue
					}
				} else {
					msg["tool_calls"] = kept
				}
			}
		}
		out = append(out, msg)
	}
	return out, true
}

func pickChatCall(items []any, list []chatCallSlot) chatCallSlot {
	best := list[0]
	bestBlank := chatCallBlank(chatCallAt(items, best.msgIndex, best.callIndex))
	for _, slot := range list[1:] {
		if bestBlank && !chatCallBlank(chatCallAt(items, slot.msgIndex, slot.callIndex)) {
			best = slot
			bestBlank = false
		}
	}
	return best
}

func pickChatOutput(items []any, list []int) int {
	best := list[0]
	if !chatOutputBlank(items, best) {
		return best
	}
	for _, mi := range list[1:] {
		if !chatOutputBlank(items, mi) {
			return mi
		}
	}
	return best
}

func chatCallAt(items []any, msgIndex, callIndex int) map[string]any {
	msg, _ := items[msgIndex].(map[string]any)
	callList, _ := msg["tool_calls"].([]any)
	call, _ := callList[callIndex].(map[string]any)
	return call
}

func chatCallBlank(call map[string]any) bool {
	if call == nil {
		return true
	}
	if fn, ok := call["function"].(map[string]any); ok && !blankArguments(stringValue(fn["arguments"])) {
		return false
	}
	return blankArguments(stringValue(call["arguments"]))
}

func chatOutputBlank(items []any, msgIndex int) bool {
	msg, _ := items[msgIndex].(map[string]any)
	if msg == nil {
		return true
	}
	return blankPayload(msg["content"])
}
