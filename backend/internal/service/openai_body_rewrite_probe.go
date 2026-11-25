package service

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
)

// No-op probes for the request pre-processing passes that decode a whole body
// into map[string]any: each answers "could the full pass change this body?".
// A false answer is a proof (the caller returns the body untouched and skips the
// decode); a true answer means "run the original pass", which stays the single
// source of truth for what actually changes. See json_shallow_scan.go for the
// conservative rules (duplicate keys, escapes, malformed text all answer true).

// jsonProbeMaxDepth bounds recursion into nested arrays; deeper bodies are
// left to the decoder.
const jsonProbeMaxDepth = 32

// responsesRewriteScope names which of the decode-based rewrites of
// normalizeDeepSeekResponsesRequestBody / RewriteInboundDeepSeekCallIDs apply.
type responsesRewriteScope struct {
	// dedupe: duplicate call-id cleanup on input and messages.
	dedupe bool
	// reasoning: DeepSeek reasoning repair on input, effective when the account
	// is a DeepSeek account or the body's model is a deepseek model.
	reasoning bool
	// deepSeekAccount: the account platform is already known to be DeepSeek.
	deepSeekAccount bool
}

// responsesBodyMayRewrite mirrors, on the raw text, the conditions under which
// apicompat.DedupeResponsesCallIDs, apicompat.DedupeChatToolCallIDs,
// apicompat.NormalizeResponsesReasoningForDeepSeek and
// apicompat.InjectResponsesReasoningPlaceholders report a change. The
// probe_test oracle compares it with the real passes.
func responsesBodyMayRewrite(body []byte, scope responsesRewriteScope) bool {
	var input, messages, model string
	var hasInput, hasMessages, hasModel, dup bool
	if !jsonForEachMember(jsonView(body), func(key, val string) bool {
		switch key {
		case "input":
			dup, hasInput, input = dup || hasInput, true, val
		case "messages":
			dup, hasMessages, messages = dup || hasMessages, true, val
		case "model":
			dup, hasModel, model = dup || hasModel, true, val
		}
		return !dup
	}) || dup {
		return true
	}

	deepSeek := scope.deepSeekAccount
	if scope.reasoning && !deepSeek && hasModel {
		name, plain := jsonPlainString(model)
		if !plain {
			return true
		}
		deepSeek = strings.HasPrefix(strings.ToLower(strings.TrimSpace(name)), "deepseek")
	}
	deepSeek = deepSeek && scope.reasoning

	if hasInput && input[0] == '[' && (scope.dedupe || deepSeek) {
		st := responsesProbe{dedupe: scope.dedupe, deepSeek: deepSeek}
		if st.items(input, true, 0) {
			return true
		}
		// Inject: tool calls with no reasoning item anywhere in the input.
		if deepSeek && st.hasCall && !st.hasReasoning {
			return true
		}
	}
	if scope.dedupe && hasMessages && messages[0] == '[' && chatMessagesMayRewrite(messages) {
		return true
	}
	return false
}

type responsesProbe struct {
	dedupe, deepSeek bool
	// seen maps a call id to the slots ("call" = 1, "output" = 2) already used.
	seen                  map[string]uint8
	hasCall, hasReasoning bool
}

// items walks an input array. top marks the request's own input array (the
// DeepSeek reasoning rules only look at it); nested arrays are only relevant to
// the call-id dedupe.
func (st *responsesProbe) items(arr string, top bool, depth int) (mayChange bool) {
	if depth > jsonProbeMaxDepth {
		return true
	}
	ok := jsonForEachElement(arr, func(elem string) bool {
		switch elem[0] {
		case '[':
			if st.dedupe && st.items(elem, false, depth+1) {
				mayChange = true
			}
		case '{':
			if st.item(elem, top, depth) {
				mayChange = true
			}
		}
		return !mayChange
	})
	return mayChange || !ok
}

func (st *responsesProbe) item(obj string, top bool, depth int) bool {
	var typ, callID, id, content string
	var seenType, seenCall, seenID, seenContent, seenEncrypted, bad bool
	ok := jsonForEachMember(obj, func(key, val string) bool {
		var plain bool
		switch key {
		case "type":
			bad = bad || seenType
			seenType = true
			typ, plain = jsonPlainString(val)
			bad = bad || !plain
		case "call_id":
			bad = bad || seenCall
			seenCall = true
			callID, plain = jsonPlainString(val)
			bad = bad || !plain
		case "id":
			bad = bad || seenID
			seenID = true
			id, plain = jsonPlainString(val)
			bad = bad || !plain
		case "content":
			bad = bad || seenContent
			seenContent = true
			content = val
		case "encrypted_content":
			seenEncrypted = true
		}
		return !bad
	})
	if !ok || bad {
		return true
	}

	if st.dedupe {
		// apicompat.responsesCallIDSlot
		kind := strings.TrimSpace(typ)
		if kind != "" && kind != "message" && kind != "reasoning" {
			call := strings.TrimSpace(callID)
			itemID := strings.TrimSpace(id)
			tracked := call
			if tracked == "" && strings.HasPrefix(itemID, "call_") {
				tracked = itemID
			}
			if tracked != "" {
				// separateCollidingCallItemID rewrites such an item even when
				// its call id is unique.
				if call == "" || itemID == call {
					return true
				}
				slot := uint8(1)
				if strings.Contains(kind, "output") {
					slot = 2
				}
				if st.seen == nil {
					st.seen = make(map[string]uint8, 16)
				}
				if st.seen[tracked]&slot != 0 {
					return true
				}
				st.seen[tracked] |= slot
			}
		}
	}
	if st.deepSeek && top {
		switch typ {
		case "function_call", "custom_tool_call", "local_shell_call":
			st.hasCall = true
		case "reasoning":
			st.hasReasoning = true
			// NormalizeResponsesReasoningForDeepSeek: drops encrypted_content
			// and fills in a missing id.
			if seenEncrypted || strings.TrimSpace(id) == "" {
				return true
			}
		}
	}
	if st.dedupe && seenContent && content[0] == '[' {
		return st.items(content, false, depth+1)
	}
	return false
}

// chatMessagesMayRewrite mirrors apicompat.DedupeChatToolCallIDs, which changes
// the history exactly when a tool call id repeats among assistant tool_calls or
// among tool messages.
func chatMessagesMayRewrite(arr string) bool {
	var calls, outputs map[string]struct{}
	mayChange := false
	ok := jsonForEachObject(arr, func(msg string) bool {
		var toolCalls, toolCallID string
		var seenCalls, seenID bool
		if !jsonForEachMember(msg, func(key, val string) bool {
			switch key {
			case "tool_calls":
				mayChange = mayChange || seenCalls
				seenCalls, toolCalls = true, val
			case "tool_call_id":
				mayChange = mayChange || seenID
				seenID, toolCallID = true, val
			}
			return !mayChange
		}) || mayChange {
			mayChange = true
			return false
		}
		if seenCalls && toolCalls[0] == '[' {
			if !jsonForEachElement(toolCalls, func(call string) bool {
				if call[0] != '{' {
					return true
				}
				var id string
				seen := false
				if !jsonForEachMember(call, func(key, val string) bool {
					if key != "id" {
						return true
					}
					var plain bool
					id, plain = jsonPlainString(val)
					mayChange = mayChange || seen || !plain
					seen = true
					return !mayChange
				}) || mayChange {
					mayChange = true
					return false
				}
				if id = strings.TrimSpace(id); id == "" {
					return true
				}
				if calls == nil {
					calls = make(map[string]struct{}, 8)
				}
				if _, dup := calls[id]; dup {
					mayChange = true
					return false
				}
				calls[id] = struct{}{}
				return true
			}) || mayChange {
				mayChange = true
				return false
			}
		}
		if seenID {
			id, plain := jsonPlainString(toolCallID)
			if !plain {
				mayChange = true
				return false
			}
			if id = strings.TrimSpace(id); id != "" {
				if outputs == nil {
					outputs = make(map[string]struct{}, 8)
				}
				if _, dup := outputs[id]; dup {
					mayChange = true
					return false
				}
				outputs[id] = struct{}{}
			}
		}
		return true
	})
	return mayChange || !ok
}

// jsonForEachObject is jsonForEachElement restricted to object elements:
// elements that are not objects are skipped, the way the dedupe passes skip
// entries that are not map[string]any.
func jsonForEachObject(arr string, fn func(obj string) bool) bool {
	return jsonForEachElement(arr, func(elem string) bool {
		if elem[0] != '{' {
			return true
		}
		return fn(elem)
	})
}

// strictChatRolesMayChange reports whether normalizeStrictChatDeveloperRoles has
// to run its decoder: when a message is a developer message, and also for every
// body shape the decoder rejects, so the caller still sees the same error. A
// false answer means the decoder would return the body as is.
func strictChatRolesMayChange(body []byte) bool {
	if !validJSON(body) {
		return true
	}
	var messages string
	var hasMessages, dup bool
	if !jsonForEachMember(jsonView(body), func(key, val string) bool {
		if key == "messages" {
			dup, hasMessages, messages = dup || hasMessages, true, val
		}
		return !dup
	}) || dup {
		return true
	}
	if !hasMessages || messages == "null" {
		return false
	}
	if messages[0] != '[' {
		return true
	}
	mayChange := false
	ok := jsonForEachElement(messages, func(msg string) bool {
		var role string
		seen := false
		if msg[0] != '{' || !jsonForEachMember(msg, func(key, val string) bool {
			if key == "role" {
				mayChange = mayChange || seen
				seen, role = true, val
			}
			return !mayChange
		}) || !seen || mayChange {
			mayChange = true
			return false
		}
		switch role[0] {
		case '"':
			// An escaped role could spell "developer".
			mayChange = strings.IndexByte(role, '\\') >= 0 || role == `"developer"`
		case 'n':
			// null leaves the role empty.
			mayChange = role != "null"
		default:
			mayChange = true
		}
		return !mayChange
	})
	return mayChange || !ok
}

// thinkingFilterMayChange reports whether filterThinkingBlocksInternal could
// drop a block: a thinking / redacted_thinking block that is not an assistant
// block with a real signature, or an untyped block with a "thinking" key.
func thinkingFilterMayChange(body []byte) bool {
	var messages string
	var hasMessages, dup bool
	if !jsonForEachMember(jsonView(body), func(key, val string) bool {
		if key == "messages" {
			dup, hasMessages, messages = dup || hasMessages, true, val
		}
		return !dup
	}) || dup {
		return true
	}
	if !hasMessages || messages[0] != '[' {
		return false
	}
	mayChange := false
	ok := jsonForEachObject(messages, func(msg string) bool {
		var role, content string
		var seenRole, seenContent, plainRole bool
		if !jsonForEachMember(msg, func(key, val string) bool {
			switch key {
			case "role":
				mayChange = mayChange || seenRole
				seenRole = true
				role, plainRole = jsonPlainString(val)
				mayChange = mayChange || !plainRole
			case "content":
				mayChange = mayChange || seenContent
				seenContent, content = true, val
			}
			return !mayChange
		}) || mayChange {
			mayChange = true
			return false
		}
		if !seenContent || content[0] != '[' {
			return true
		}
		assistant := role == "assistant"
		if !jsonForEachObject(content, func(block string) bool {
			var typ, signature string
			var seenType, seenSig, seenThinking, plainType bool
			if !jsonForEachMember(block, func(key, val string) bool {
				switch key {
				case "type":
					mayChange = mayChange || seenType
					seenType = true
					typ, plainType = jsonPlainString(val)
					mayChange = mayChange || !plainType
				case "signature":
					mayChange = mayChange || seenSig
					seenSig = true
					signature = val
				case "thinking":
					mayChange = mayChange || seenThinking
					seenThinking = true
				}
				return !mayChange
			}) || mayChange {
				mayChange = true
				return false
			}
			switch typ {
			case "thinking", "redacted_thinking":
				mayChange = !assistant || !jsonSignatureKept(signature)
			case "":
				mayChange = seenThinking
			}
			return !mayChange
		}) {
			mayChange = true
		}
		return !mayChange
	})
	return mayChange || !ok
}

// jsonSignatureKept reports whether a raw "signature" value is a non-empty
// string other than the dummy placeholder, i.e. a signature the filter keeps.
// Anything it cannot decide on the raw text (escapes) counts as not kept.
func jsonSignatureKept(raw string) bool {
	if len(raw) < 3 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return false // absent, non-string or empty
	}
	content := raw[1 : len(raw)-1]
	if strings.IndexByte(content, '\\') >= 0 {
		return false
	}
	return content != antigravity.DummyThoughtSignature
}

// geminiEmptyPartsMayChange reports whether filterEmptyPartsFromGeminiRequest has
// to run its decoder: a contents entry with an empty parts array, and every body
// the decoder rejects (so the caller still sees its error). A false answer means
// the decoder would hand the body back unchanged.
func geminiEmptyPartsMayChange(body []byte) bool {
	if !validJSON(body) {
		return true
	}
	var contents string
	var hasContents, dup bool
	if !jsonForEachMember(jsonView(body), func(key, val string) bool {
		if key == "contents" {
			dup, hasContents, contents = dup || hasContents, true, val
		}
		return !dup
	}) || dup {
		return true
	}
	if !hasContents || contents[0] != '[' {
		return false
	}
	mayChange := false
	ok := jsonForEachObject(contents, func(content string) bool {
		var parts string
		seen := false
		if !jsonForEachMember(content, func(key, val string) bool {
			if key == "parts" {
				mayChange = mayChange || seen
				seen, parts = true, val
			}
			return !mayChange
		}) || mayChange {
			mayChange = true
			return false
		}
		if seen && parts[0] == '[' {
			empty := true
			if !jsonForEachElement(parts, func(string) bool {
				empty = false
				return false
			}) {
				mayChange = true
			}
			mayChange = mayChange || empty
		}
		return !mayChange
	})
	return mayChange || !ok
}
