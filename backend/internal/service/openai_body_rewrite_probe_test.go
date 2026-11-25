package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// ---------------------------------------------------------------------------
// Frozen copies of the implementations the probes short-circuit. They are the
// oracle: for every body the optimized functions must return exactly what these
// return (same bytes, same error, same slice when nothing changed).
// ---------------------------------------------------------------------------

func legacyRewriteInboundDeepSeekCallIDs(groupPlatform, model string, body []byte) []byte {
	if len(body) == 0 {
		return body
	}
	if deepSeekCallIDScope(groupPlatform, model) {
		return body
	}
	if !deepSeekCallIDScope(groupPlatform, model) && !bodyHasToolCallIDs(body) {
		return body
	}
	var requestBody map[string]any
	if err := decodeOpenAIJSONUseNumber(body, &requestBody); err != nil {
		return body
	}
	changed := false
	if input, ok := requestBody["input"]; ok {
		if next, did := apicompat.DedupeResponsesCallIDs(input); did {
			requestBody["input"] = next
			changed = true
		}
	}
	if messages, ok := requestBody["messages"]; ok {
		if next, did := apicompat.DedupeChatToolCallIDs(messages); did {
			requestBody["messages"] = next
			changed = true
		}
	}
	if !changed {
		return body
	}
	rebuilt, err := marshalOpenAIUpstreamJSON(requestBody)
	if err != nil {
		return body
	}
	return rebuilt
}

func legacyNormalizeDeepSeekResponsesRequestBody(account *Account, body []byte) []byte {
	if account != nil && account.Platform == PlatformDeepseek {
		body = stripUnsupportedDeepSeekResponseFormat(body)
	}
	if account != nil && account.Platform == PlatformDeepseek && account.UsesNativeCNResponses() {
		return body
	}
	native := account != nil && account.UsesNativeCNResponses()
	dedupe := deepSeekDuplicateCallIDRewrite(account, body)
	repairReasoning := servedByDeepSeekBody(account, body)
	if !native && !dedupe && !repairReasoning {
		return body
	}
	normalized := body
	if native {
		var err error
		normalized, err = sjson.SetBytes(body, "store", false)
		if err != nil {
			return body
		}
		if stripped, err := sjson.DeleteBytes(normalized, "previous_response_id"); err == nil {
			normalized = stripped
		}
	}
	if dedupe {
		strippedSwitches := false
		for _, path := range []string{"reasoning.summary", "reasoning.generate_summary"} {
			if stripped, err := sjson.DeleteBytes(normalized, path); err == nil {
				normalized = stripped
				strippedSwitches = true
			}
		}
		if strippedSwitches {
			if reasoning := gjson.GetBytes(normalized, "reasoning"); reasoning.IsObject() && len(reasoning.Map()) == 0 {
				if next, err := sjson.DeleteBytes(normalized, "reasoning"); err == nil {
					normalized = next
				}
			}
		}
	}

	var requestBody map[string]any
	if err := decodeOpenAIJSONUseNumber(normalized, &requestBody); err != nil {
		return normalized
	}
	changed := false
	if input, exists := requestBody["input"]; exists {
		if native {
			if lifted, did := apicompat.LiftResponsesToolOutputMedia(input); did {
				input = lifted
				changed = true
			}
		}
		if dedupe {
			if deduped, did := apicompat.DedupeResponsesCallIDs(input); did {
				if !responsesCallPairsIntact(input) || responsesCallPairsIntact(deduped) {
					input = deduped
					changed = true
				}
			}
		}
		if changed {
			requestBody["input"] = input
		}
	}
	if input, exists := requestBody["input"]; exists && servedByDeepSeek(account, requestBody) {
		if fixed, did := apicompat.NormalizeResponsesReasoningForDeepSeek(input); did {
			input = fixed
			requestBody["input"] = input
			changed = true
		}
		if injected, did := apicompat.InjectResponsesReasoningPlaceholders(input); did {
			requestBody["input"] = injected
			changed = true
		}
	}
	if dedupe {
		if messages, ok := requestBody["messages"]; ok {
			if deduped, did := apicompat.DedupeChatToolCallIDs(messages); did {
				requestBody["messages"] = deduped
				changed = true
			}
		}
	}
	if !changed {
		return normalized
	}
	rebuilt, err := marshalOpenAIUpstreamJSON(requestBody)
	if err != nil {
		return normalized
	}
	return rebuilt
}

func legacyNormalizeStrictChatDeveloperRoles(account *Account, targetURL string, body []byte) ([]byte, error) {
	if !requiresSystemChatRole(account, targetURL) {
		return body, nil
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(body, &root) != nil || root == nil {
		return nil, fmt.Errorf("chat role normalization: invalid request object")
	}
	raw, exists := root["messages"]
	if !exists {
		return body, nil
	}
	var messages []json.RawMessage
	if json.Unmarshal(raw, &messages) != nil {
		return nil, fmt.Errorf("chat role normalization: invalid messages array")
	}
	changed := false
	for i, raw := range messages {
		var message map[string]json.RawMessage
		if json.Unmarshal(raw, &message) != nil || message == nil {
			return nil, fmt.Errorf("chat role normalization: invalid message object")
		}
		var role string
		if json.Unmarshal(message["role"], &role) != nil {
			return nil, fmt.Errorf("chat role normalization: invalid message role")
		}
		if role != "developer" {
			continue
		}
		message["role"] = json.RawMessage(`"system"`)
		updated, err := json.Marshal(message)
		if err != nil {
			return nil, fmt.Errorf("chat role normalization: cannot encode message")
		}
		messages[i] = updated
		changed = true
	}
	if !changed {
		return body, nil
	}
	updated, err := json.Marshal(messages)
	if err != nil {
		return nil, fmt.Errorf("chat role normalization: cannot encode messages")
	}
	root["messages"] = updated
	return json.Marshal(root)
}

func legacyFilterThinkingBlocksInternal(body []byte) []byte {
	if !bytes.Contains(body, []byte(`"type":"thinking"`)) &&
		!bytes.Contains(body, []byte(`"type": "thinking"`)) &&
		!bytes.Contains(body, []byte(`"type":"redacted_thinking"`)) &&
		!bytes.Contains(body, []byte(`"type": "redacted_thinking"`)) &&
		!bytes.Contains(body, []byte(`"thinking":`)) &&
		!bytes.Contains(body, []byte(`"thinking" :`)) {
		return body
	}
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return body
	}
	messages, ok := req["messages"].([]any)
	if !ok {
		return body
	}
	filtered := false
	for _, msg := range messages {
		msgMap, ok := msg.(map[string]any)
		if !ok {
			continue
		}
		role, _ := msgMap["role"].(string)
		content, ok := msgMap["content"].([]any)
		if !ok {
			continue
		}
		newContent := make([]any, 0, len(content))
		filteredThisMessage := false
		for _, block := range content {
			blockMap, ok := block.(map[string]any)
			if !ok {
				newContent = append(newContent, block)
				continue
			}
			blockType, _ := blockMap["type"].(string)
			if blockType == "thinking" || blockType == "redacted_thinking" {
				if role == "assistant" {
					signature, _ := blockMap["signature"].(string)
					if signature != "" && signature != antigravity.DummyThoughtSignature {
						newContent = append(newContent, block)
						continue
					}
				}
				filtered = true
				filteredThisMessage = true
				continue
			}
			if blockType == "" {
				if _, hasThinking := blockMap["thinking"]; hasThinking {
					filtered = true
					filteredThisMessage = true
					continue
				}
			}
			newContent = append(newContent, block)
		}
		if filteredThisMessage {
			msgMap["content"] = newContent
		}
	}
	if !filtered {
		return body
	}
	newBody, err := json.Marshal(req)
	if err != nil {
		return body
	}
	return newBody
}

func legacyFilterEmptyPartsFromGeminiRequest(body []byte) ([]byte, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	contents, ok := payload["contents"].([]any)
	if !ok || len(contents) == 0 {
		return body, nil
	}
	filtered := make([]any, 0, len(contents))
	modified := false
	for _, c := range contents {
		contentMap, ok := c.(map[string]any)
		if !ok {
			filtered = append(filtered, c)
			continue
		}
		parts, hasParts := contentMap["parts"]
		if !hasParts {
			filtered = append(filtered, c)
			continue
		}
		partsSlice, ok := parts.([]any)
		if !ok {
			filtered = append(filtered, c)
			continue
		}
		if len(partsSlice) == 0 {
			modified = true
			continue
		}
		filtered = append(filtered, c)
	}
	if !modified {
		return body, nil
	}
	payload["contents"] = filtered
	return json.Marshal(payload)
}

func sameBytes(a, b []byte) bool {
	return len(a) == len(b) && (len(a) == 0 || &a[0] == &b[0])
}

// ---------------------------------------------------------------------------
// Random body generator. Short pools make collisions (duplicate call ids,
// id == call_id, blank ids, ...) common, and a few percent of the objects carry
// duplicate keys or keys spelled with escapes.
// ---------------------------------------------------------------------------

type probeGen struct {
	r *rand.Rand
	// hostile toggles duplicate keys, escaped spellings and malformed text.
	hostile bool
}

func (g *probeGen) chance(p float64) bool { return g.r.Float64() < p }

func (g *probeGen) pick(opts ...string) string { return opts[g.r.Intn(len(opts))] }

func (g *probeGen) sep() string { return jsonSep(g.r) }

func (g *probeGen) text() string {
	return g.pick(`"hello"`, `"line one\nline two"`, `"say \"hi\""`, `"tab\there"`, `"café é"`, `"émoji 😀"`, `"<b>&amp;</b>"`,
		`"{\"cmd\":\"ls\",\"n\":1}"`, `""`, `"   "`, `"a/b\/c"`, `"  sep"`, `"ctl \u001b[31mred\u001b[0m"`)
}

func (g *probeGen) callID() string {
	return g.pick(`"call_1"`, `"call_2"`, `"call_3"`, `"call_a"`, `"call_00_Xyz"`, `"fc_1"`, `""`, `" call_1"`, `"call_1 "`, `"CALL_1"`,
		`"call_1"`, `"call_2"`, `"call_3"`, `null`, `7`)
}

func (g *probeGen) member(key, val string) string {
	if g.hostile && g.chance(0.02) {
		key = strings.Replace(key, key[len(key)-1:], fmt.Sprintf(`\u%04x`, key[len(key)-1]), 1)
	}
	return `"` + key + `"` + jsonColon(g.r) + val
}

func (g *probeGen) object(members ...string) string {
	g.r.Shuffle(len(members), func(i, j int) { members[i], members[j] = members[j], members[i] })
	if g.hostile && len(members) > 0 && g.chance(0.04) {
		members = append(members, members[g.r.Intn(len(members))])
	}
	return "{" + strings.Join(members, g.sep()) + "}"
}

func (g *probeGen) parts(depth int) string {
	n := g.r.Intn(3)
	parts := make([]string, n)
	for i := range parts {
		switch g.r.Intn(8) {
		case 0:
			parts[i] = g.text()
		case 1:
			if depth > 0 {
				parts[i] = "[" + g.responsesItem(depth-1) + "]"
			} else {
				parts[i] = `[]`
			}
		case 2:
			// a content part that looks like a tracked call
			parts[i] = g.object(g.member("type", g.pick(`"function_call"`, `"function_call_output"`, `"input_text"`)),
				g.member("call_id", g.callID()), g.member("id", g.callID()))
		default:
			parts[i] = g.object(g.member("type", g.pick(`"input_text"`, `"output_text"`, `"input_image"`)), g.member("text", g.text()))
		}
	}
	return "[" + strings.Join(parts, g.sep()) + "]"
}

func (g *probeGen) responsesItem(depth int) string {
	typ := func(names ...string) string { return g.member("type", `"`+names[g.r.Intn(len(names))]+`"`) }
	switch g.r.Intn(16) {
	case 0:
		return g.object(typ("message"), g.member("role", g.pick(`"user"`, `"assistant"`, `"developer"`)), g.member("content", g.parts(depth)))
	case 1:
		return g.object(typ("message"), g.member("role", `"user"`), g.member("content", g.text()))
	case 2, 3:
		m := []string{typ("reasoning"), g.member("summary", `[]`)}
		if g.chance(0.7) {
			m = append(m, g.member("id", g.pick(`"rs_1"`, `"rs_2"`, `""`, `"  "`, `null`, `5`)))
		}
		if g.chance(0.3) {
			m = append(m, g.member("encrypted_content", g.pick(`"gAAAA"`, `null`, `""`)))
		}
		if g.chance(0.4) {
			m = append(m, g.member("content", g.parts(depth)))
		}
		return g.object(m...)
	case 4, 5, 6:
		m := []string{typ("function_call", "function_call", "custom_tool_call", "local_shell_call"), g.member("name", `"shell"`), g.member("arguments", g.text())}
		if g.chance(0.85) {
			m = append(m, g.member("call_id", g.callID()))
		}
		if g.chance(0.6) {
			m = append(m, g.member("id", g.pick(`"fc_1"`, `"fc_2"`, `"call_1"`, `"call_2"`, `"call_a"`, `""`, `null`)))
		}
		if g.chance(0.2) {
			m = append(m, g.member("content", g.parts(depth)))
		}
		return g.object(m...)
	case 7, 8, 9:
		m := []string{typ("function_call_output", "function_call_output", "custom_tool_call_output", "local_shell_call_output", "computer_call_output", "mcp_tool_call_output"),
			g.member("output", g.pick(`"ok"`, `""`, `"{}"`, `null`, `[{"type":"input_text","text":"x"}]`))}
		if g.chance(0.9) {
			m = append(m, g.member("call_id", g.callID()))
		}
		return g.object(m...)
	case 10:
		return g.object(typ("item_reference"), g.member("id", g.callID()))
	case 11:
		return g.pick(`"just text"`, `42`, `null`, `true`, `{}`, `[]`, `[[]]`)
	case 12:
		return g.object(g.member("call_id", g.callID()), g.member("id", g.callID()))
	case 13:
		return g.object(g.member("type", g.pick(`5`, `null`, `" function_call "`, `"Function_Call"`, `"function_call"`)),
			g.member("call_id", g.callID()), g.member("id", g.callID()))
	case 14:
		if depth > 0 {
			return "[" + g.responsesItem(depth-1) + g.sep() + g.responsesItem(depth-1) + "]"
		}
		return `[]`
	default:
		return g.object(typ("web_search_call", "tool_search_output", "additional_tools"), g.member("id", g.callID()), g.member("call_id", g.callID()))
	}
}

func (g *probeGen) chatMessage() string {
	switch g.r.Intn(6) {
	case 0:
		return g.object(g.member("role", g.pick(`"user"`, `"system"`, `"developer"`)), g.member("content", g.text()))
	case 1, 2:
		n := g.r.Intn(3)
		calls := make([]string, n)
		for i := range calls {
			if g.chance(0.1) {
				calls[i] = g.pick(`1`, `null`, `"x"`, `[]`)
				continue
			}
			m := []string{g.member("type", `"function"`), g.member("function", `{"name":"f","arguments":"{}"}`)}
			if g.chance(0.9) {
				m = append(m, g.member("id", g.callID()))
			}
			calls[i] = g.object(m...)
		}
		m := []string{g.member("role", `"assistant"`)}
		if n > 0 || g.chance(0.3) {
			m = append(m, g.member("tool_calls", "["+strings.Join(calls, g.sep())+"]"))
		}
		if g.chance(0.5) {
			m = append(m, g.member("content", g.pick(`null`, `""`, `"thinking out loud"`)))
		}
		return g.object(m...)
	case 3, 4:
		m := []string{g.member("role", `"tool"`), g.member("content", g.text())}
		if g.chance(0.9) {
			m = append(m, g.member("tool_call_id", g.callID()))
		}
		return g.object(m...)
	default:
		return g.pick(`"x"`, `null`, `3`, `[]`, `{}`)
	}
}

func (g *probeGen) responsesBody() string {
	var m []string
	if g.chance(0.9) {
		m = append(m, g.member("model", g.pick(`"gpt-5"`, `"deepseek-v4.1-flash"`, `"DeepSeek-R1"`, `" deepseek-chat "`, `"alias"`, `""`, `7`, `null`, `"deepseek-x"`, `"claude-opus-4"`)))
	}
	if g.chance(0.85) {
		switch {
		case g.chance(0.92):
			n := g.r.Intn(9)
			items := make([]string, n)
			for i := range items {
				items[i] = g.responsesItem(2)
			}
			m = append(m, g.member("input", "["+strings.Join(items, g.sep())+"]"))
		default:
			m = append(m, g.member("input", g.pick(`"hi"`, `{}`, `null`, `5`, `[]`)))
		}
	}
	if g.chance(0.3) {
		n := g.r.Intn(5)
		msgs := make([]string, n)
		for i := range msgs {
			msgs[i] = g.chatMessage()
		}
		m = append(m, g.member("messages", "["+strings.Join(msgs, g.sep())+"]"))
	}
	if g.chance(0.5) {
		m = append(m, g.member("reasoning", g.pick(`{}`, `{"effort":"high"}`, `{"summary":"auto"}`, `{"effort":"low","summary":"auto","generate_summary":"x"}`,
			`{"generate_summary":"concise"}`, `"high"`, `null`, `[]`, `{"summary":null}`)))
	}
	if g.chance(0.5) {
		m = append(m, g.member("tools", `[{"type":"function","name":"shell","parameters":{"type":"object","properties":{"cmd":{"type":"string"}}}}]`))
	}
	if g.chance(0.4) {
		m = append(m, g.member("seq", g.pick(`12345678901234567890123`, `1e999`, `-0`, `0.1000000000000000055511151231257827`, `9007199254740993`, `1E+2`)))
	}
	if g.chance(0.3) {
		m = append(m, g.member("store", g.pick(`true`, `false`)), g.member("previous_response_id", `"resp_1"`))
	}
	if g.chance(0.3) {
		m = append(m, g.member("response_format", g.pick(`{"type":"json_schema"}`, `{"type":"text"}`)))
	}
	return g.finish(g.object(m...))
}

// finish pretty-prints half of the valid documents and, for hostile runs,
// mangles a few.
func (g *probeGen) finish(doc string) string {
	if g.chance(0.4) && json.Valid([]byte(doc)) {
		var buf bytes.Buffer
		if json.Indent(&buf, []byte(doc), "", g.pick("  ", "\t", " ")) == nil {
			doc = buf.String()
		}
	}
	if g.hostile && g.chance(0.04) {
		doc = mutateJSON(g.r, doc)
	}
	if g.hostile && g.chance(0.02) {
		doc += g.pick(" ", "\n", ` {"x":1}`, " junk")
	}
	return doc
}

func (g *probeGen) thinkingBlock() string {
	m := []string{}
	switch g.r.Intn(9) {
	case 0, 1:
		m = append(m, g.member("type", `"text"`), g.member("text", g.text()))
	case 2, 3, 4:
		m = append(m, g.member("type", g.pick(`"thinking"`, `"redacted_thinking"`)), g.member("thinking", g.text()))
		switch g.r.Intn(7) {
		case 0, 1, 2:
			m = append(m, g.member("signature", `"EqQBCkYIBRgCIkD"`))
		case 3:
			m = append(m, g.member("signature", `""`))
		case 4:
			m = append(m, g.member("signature", `"`+antigravity.DummyThoughtSignature+`"`))
		case 5:
			m = append(m, g.member("signature", g.pick(`null`, `5`, `"skip_thought_signature_validator"`, `"EqQ\/Bk"`)))
		}
	case 5:
		m = append(m, g.member("type", `"tool_use"`), g.member("id", `"toolu_1"`), g.member("name", `"f"`), g.member("input", `{}`))
	case 6:
		m = append(m, g.member("type", `"tool_result"`), g.member("tool_use_id", `"toolu_1"`), g.member("content", g.text()))
	case 7:
		m = append(m, g.member("thinking", g.text()))
		if g.chance(0.4) {
			m = append(m, g.member("type", g.pick(`""`, `null`, `5`)))
		}
	default:
		return g.pick(`"text block"`, `null`, `1`, `[]`)
	}
	return g.object(m...)
}

func (g *probeGen) anthropicBody() string {
	var m []string
	m = append(m, g.member("model", `"claude-sonnet-4"`), g.member("max_tokens", `1024`))
	if g.chance(0.5) {
		m = append(m, g.member("thinking", `{"type":"enabled","budget_tokens":1024}`))
	}
	if g.chance(0.9) {
		n := g.r.Intn(6)
		msgs := make([]string, n)
		for i := range msgs {
			if g.chance(0.05) {
				msgs[i] = g.pick(`"x"`, `null`, `[]`)
				continue
			}
			mm := []string{g.member("role", g.pick(`"user"`, `"assistant"`, `"assistant"`, `"system"`, `5`, `"assistant"`))}
			switch {
			case g.chance(0.15):
				mm = append(mm, g.member("content", g.text()))
			case g.chance(0.05):
				// no content
			default:
				k := g.r.Intn(4)
				blocks := make([]string, k)
				for j := range blocks {
					blocks[j] = g.thinkingBlock()
				}
				mm = append(mm, g.member("content", "["+strings.Join(blocks, g.sep())+"]"))
			}
			msgs[i] = g.object(mm...)
		}
		m = append(m, g.member("messages", "["+strings.Join(msgs, g.sep())+"]"))
	}
	if g.chance(0.2) {
		m = append(m, g.member("seq", `12345678901234567890123`))
	}
	return g.finish(g.object(m...))
}

func (g *probeGen) chatBody() string {
	var m []string
	m = append(m, g.member("model", `"deepseek-chat"`))
	switch {
	case g.chance(0.9):
		n := g.r.Intn(7)
		msgs := make([]string, n)
		for i := range msgs {
			switch g.r.Intn(9) {
			case 0:
				msgs[i] = g.pick(`"x"`, `null`, `3`, `[]`)
			case 1:
				msgs[i] = g.object(g.member("content", g.text()))
			case 2:
				msgs[i] = g.object(g.member("role", g.pick(`5`, `true`, `[]`, `{}`, `"developer"`)), g.member("content", g.text()))
			default:
				msgs[i] = g.object(g.member("role", g.pick(`"user"`, `"system"`, `"assistant"`, `"tool"`, `"developer"`, `"developer"`, `null`, `"Developer"`, `"developer "`, `"é"`)),
					g.member("content", g.text()), g.member("name", `"n"`))
			}
		}
		m = append(m, g.member("messages", "["+strings.Join(msgs, g.sep())+"]"))
	case g.chance(0.5):
		m = append(m, g.member("messages", g.pick(`null`, `{}`, `"x"`, `5`, `[]`)))
	}
	if g.chance(0.3) {
		m = append(m, g.member("seq", g.pick(`12345678901234567890123`, `1e999`, `0.1000000000000000055511151231257827`)))
	}
	if g.chance(0.2) {
		m = append(m, g.member("stream", `true`))
	}
	doc := g.object(m...)
	if g.chance(0.03) {
		doc = g.pick(`null`, `[]`, `5`, `"x"`, ``, ` `)
	}
	return g.finish(doc)
}

func (g *probeGen) geminiBody() string {
	var m []string
	if g.chance(0.9) {
		n := g.r.Intn(6)
		contents := make([]string, n)
		for i := range contents {
			if g.chance(0.08) {
				contents[i] = g.pick(`"x"`, `null`, `3`, `[]`)
				continue
			}
			c := []string{g.member("role", g.pick(`"user"`, `"model"`))}
			if g.chance(0.9) {
				switch g.r.Intn(6) {
				case 0, 1:
					c = append(c, g.member("parts", g.pick(`[]`, `[ ]`, "[\n]")))
				case 2:
					c = append(c, g.member("parts", g.pick(`null`, `{}`, `"x"`, `0`)))
				default:
					c = append(c, g.member("parts", `[{"text":`+g.text()+`}]`))
				}
			}
			contents[i] = g.object(c...)
		}
		m = append(m, g.member("contents", "["+strings.Join(contents, g.sep())+"]"))
	} else {
		m = append(m, g.member("contents", g.pick(`null`, `{}`, `"x"`, `[]`)))
	}
	if g.chance(0.4) {
		m = append(m, g.member("generationConfig", `{"temperature":0.5,"seed":12345678901234567890123}`))
	}
	doc := g.object(m...)
	if g.chance(0.03) {
		doc = g.pick(`null`, `[]`, `5`, ``, ` `)
	}
	return g.finish(doc)
}

// ---------------------------------------------------------------------------
// Oracle tests
// ---------------------------------------------------------------------------

const probeOracleRuns = 40000

func TestRewriteInboundDeepSeekCallIDsMatchesLegacy(t *testing.T) {
	for _, hostile := range []bool{false, true} {
		g := &probeGen{r: rand.New(rand.NewSource(11)), hostile: hostile}
		changed, skipped := 0, 0
		for i := 0; i < probeOracleRuns; i++ {
			doc := g.responsesBody()
			body := []byte(doc)
			original := append([]byte(nil), body...)
			for _, scope := range []struct{ platform, model string }{{PlatformOpenAI, "alias"}, {"", ""}, {PlatformGrok, "x"}, {PlatformOpenAI, "deepseek-flash"}} {
				want := legacyRewriteInboundDeepSeekCallIDs(scope.platform, scope.model, body)
				got := RewriteInboundDeepSeekCallIDs(scope.platform, scope.model, body)
				require.Equalf(t, string(want), string(got), "scope %+v body %s", scope, doc)
				require.Equalf(t, sameBytes(want, body), sameBytes(got, body), "aliasing, scope %+v body %s", scope, doc)
				require.Truef(t, bytes.Equal(original, body), "input mutated: %s", doc)
				if scope.model == "alias" {
					if !bytes.Equal(want, body) {
						changed++
					} else if len(body) > 0 && !responsesBodyMayRewrite(body, responsesRewriteScope{dedupe: true}) {
						skipped++
					}
				}
			}
		}
		t.Logf("hostile=%v rewritten=%d decode-skipped=%d of %d", hostile, changed, skipped, probeOracleRuns)
		require.Greater(t, changed, probeOracleRuns/50, "generator must exercise the rewrite path")
		require.Greater(t, skipped, probeOracleRuns/10, "generator must exercise the skip path")
	}
}

func TestNormalizeDeepSeekResponsesRequestBodyMatchesLegacy(t *testing.T) {
	accounts := map[string]*Account{
		"nil":             nil,
		"openai-apikey":   {Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
		"openai-oauth":    {Platform: PlatformOpenAI, Type: AccountTypeOAuth},
		"deepseek-chat":   {Platform: PlatformDeepseek, Type: AccountTypeAPIKey},
		"deepseek-native": {Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_protocol": APIProtocolResponses}},
		"kimi-native":     {Platform: PlatformKimi, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_protocol": APIProtocolResponses}},
		"deepseek-url":    {Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.deepseek.com/v1"}},
		"grok":            {Platform: PlatformGrok, Type: AccountTypeAPIKey},
	}
	for _, hostile := range []bool{false, true} {
		g := &probeGen{r: rand.New(rand.NewSource(12)), hostile: hostile}
		changed, skipped := 0, 0
		for i := 0; i < probeOracleRuns; i++ {
			doc := g.responsesBody()
			body := []byte(doc)
			original := append([]byte(nil), body...)
			for name, account := range accounts {
				want := legacyNormalizeDeepSeekResponsesRequestBody(account, body)
				got := normalizeDeepSeekResponsesRequestBody(account, body)
				require.Equalf(t, string(want), string(got), "account %s body %s", name, doc)
				require.Equalf(t, sameBytes(want, body), sameBytes(got, body), "aliasing, account %s body %s", name, doc)
				require.Truef(t, bytes.Equal(original, body), "input mutated: %s", doc)
				if name == "openai-oauth" {
					if !bytes.Equal(want, body) {
						changed++
					} else {
						skipped++
					}
				}
			}
		}
		t.Logf("hostile=%v rewritten=%d untouched=%d of %d", hostile, changed, skipped, probeOracleRuns)
		require.Greater(t, changed, probeOracleRuns/50)
		require.Greater(t, skipped, probeOracleRuns/10)
	}
}

func TestStrictChatDeveloperRolesMatchesLegacy(t *testing.T) {
	account := &Account{Platform: PlatformDeepseek, Type: AccountTypeAPIKey}
	for _, hostile := range []bool{false, true} {
		g := &probeGen{r: rand.New(rand.NewSource(13)), hostile: hostile}
		changed, failed, untouched := 0, 0, 0
		for i := 0; i < probeOracleRuns; i++ {
			doc := g.chatBody()
			body := []byte(doc)
			original := append([]byte(nil), body...)
			want, wantErr := legacyNormalizeStrictChatDeveloperRoles(account, "https://api.deepseek.com/v1/chat/completions", body)
			got, gotErr := normalizeStrictChatDeveloperRoles(account, "https://api.deepseek.com/v1/chat/completions", body)
			require.Equalf(t, fmt.Sprint(wantErr), fmt.Sprint(gotErr), "body %s", doc)
			require.Equalf(t, string(want), string(got), "body %s", doc)
			require.Equalf(t, sameBytes(want, body), sameBytes(got, body), "aliasing, body %s", doc)
			require.Truef(t, bytes.Equal(original, body), "input mutated: %s", doc)
			switch {
			case wantErr != nil:
				failed++
			case !bytes.Equal(want, body):
				changed++
			default:
				untouched++
			}
		}
		t.Logf("hostile=%v rewritten=%d rejected=%d untouched=%d of %d", hostile, changed, failed, untouched, probeOracleRuns)
		require.Greater(t, changed, probeOracleRuns/50)
		require.Greater(t, failed, probeOracleRuns/50)
		require.Greater(t, untouched, probeOracleRuns/10)
	}
	// Accounts that never needed the rewrite still skip it.
	other := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	body := []byte(`{"messages":[{"role":"developer","content":"x"}]}`)
	got, err := normalizeStrictChatDeveloperRoles(other, "https://relay.example.test/v1/chat/completions", body)
	require.NoError(t, err)
	require.True(t, sameBytes(body, got))
}

func TestFilterThinkingBlocksMatchesLegacy(t *testing.T) {
	for _, hostile := range []bool{false, true} {
		g := &probeGen{r: rand.New(rand.NewSource(14)), hostile: hostile}
		changed, untouched, probed := 0, 0, 0
		for i := 0; i < probeOracleRuns; i++ {
			doc := g.anthropicBody()
			body := []byte(doc)
			original := append([]byte(nil), body...)
			want := legacyFilterThinkingBlocksInternal(body)
			got := filterThinkingBlocksInternal(body, false)
			require.Equalf(t, string(want), string(got), "body %s", doc)
			require.Equalf(t, sameBytes(want, body), sameBytes(got, body), "aliasing, body %s", doc)
			require.Truef(t, bytes.Equal(original, body), "input mutated: %s", doc)
			if !bytes.Equal(want, body) {
				changed++
			} else {
				untouched++
				if bytes.Contains(body, []byte("thinking")) && !thinkingFilterMayChange(body) {
					probed++
				}
			}
		}
		t.Logf("hostile=%v rewritten=%d untouched=%d decode-skipped-by-probe=%d of %d", hostile, changed, untouched, probed, probeOracleRuns)
		require.Greater(t, changed, probeOracleRuns/50)
		require.Greater(t, probed, probeOracleRuns/50, "generator must reach the probe's no-op answer")
	}
}

func TestLiteParallelToolCallsMatchesLegacyRandom(t *testing.T) {
	for _, hostile := range []bool{false, true} {
		g := &probeGen{r: rand.New(rand.NewSource(15)), hostile: hostile}
		pinned := 0
		for i := 0; i < probeOracleRuns; i++ {
			var m []string
			for k := g.r.Intn(3); k > 0; k-- {
				m = append(m, g.member("parallel_tool_calls", g.pick(`false`, `true`, `null`, `"false"`, `0`, `{}`, `false`, `false`)))
			}
			if g.chance(0.15) {
				m = append(m, `"parallel_tool_calls":`+g.pick(`false`, `true`))
			}
			if g.chance(0.5) {
				m = append(m, g.member("input", "["+g.responsesItem(1)+"]"), g.member("seq", `12345678901234567890123`))
			}
			if g.chance(0.2) {
				m = append(m, g.member("nested", `{"parallel_tool_calls":false}`))
			}
			doc := g.finish(g.object(m...))
			body := []byte(doc)
			original := append([]byte(nil), body...)
			want, wantChanged, wantErr := originalLiteParallelToolCalls(body)
			got, gotChanged, gotErr := normalizeOpenAIResponsesLiteParallelToolCallsPayload(body)
			require.Equalf(t, fmt.Sprint(wantErr), fmt.Sprint(gotErr), "body %s", doc)
			require.Equalf(t, wantChanged, gotChanged, "body %s", doc)
			require.Equalf(t, string(want), string(got), "body %s", doc)
			require.Equalf(t, sameBytes(want, body), sameBytes(got, body), "aliasing, body %s", doc)
			require.Truef(t, bytes.Equal(original, body), "input mutated: %s", doc)
			if wantErr == nil && !wantChanged {
				pinned++
			}
		}
		t.Logf("hostile=%v no-op=%d of %d", hostile, pinned, probeOracleRuns)
		require.Greater(t, pinned, probeOracleRuns/10)
	}
}

func TestFilterEmptyPartsFromGeminiRequestMatchesLegacy(t *testing.T) {
	for _, hostile := range []bool{false, true} {
		g := &probeGen{r: rand.New(rand.NewSource(16)), hostile: hostile}
		changed, failed, untouched := 0, 0, 0
		for i := 0; i < probeOracleRuns; i++ {
			doc := g.geminiBody()
			body := []byte(doc)
			original := append([]byte(nil), body...)
			want, wantErr := legacyFilterEmptyPartsFromGeminiRequest(body)
			got, gotErr := filterEmptyPartsFromGeminiRequest(body)
			require.Equalf(t, fmt.Sprint(wantErr), fmt.Sprint(gotErr), "body %s", doc)
			require.Equalf(t, string(want), string(got), "body %s", doc)
			require.Equalf(t, sameBytes(want, body), sameBytes(got, body), "aliasing, body %s", doc)
			require.Truef(t, bytes.Equal(original, body), "input mutated: %s", doc)
			switch {
			case wantErr != nil:
				failed++
			case !bytes.Equal(want, body):
				changed++
			default:
				untouched++
			}
		}
		t.Logf("hostile=%v rewritten=%d rejected=%d untouched=%d of %d", hostile, changed, failed, untouched, probeOracleRuns)
		require.Greater(t, changed, probeOracleRuns/50)
		require.Greater(t, untouched, probeOracleRuns/10)
	}
}

// Realistic shapes must take the skip path; a regression that silently turned
// the probes into "always maybe" would pass the oracle tests above.
func TestProbesSkipRealisticBodies(t *testing.T) {
	body := syntheticResponsesBody(120<<10, false)
	require.False(t, responsesBodyMayRewrite(body, responsesRewriteScope{dedupe: true}))
	require.False(t, responsesBodyMayRewrite(body, responsesRewriteScope{dedupe: true, reasoning: true}))
	require.True(t, bytes.Contains(body, []byte(`"call_id"`)))

	require.True(t, responsesBodyMayRewrite(syntheticResponsesBody(120<<10, true), responsesRewriteScope{dedupe: true}),
		"a repeated call id must route to the decoder")

	chat := syntheticChatBody(120<<10, false)
	require.False(t, strictChatRolesMayChange(chat))
	require.True(t, strictChatRolesMayChange(syntheticChatBody(120<<10, true)))

	anthropic := syntheticAnthropicBody(120<<10, false)
	require.True(t, bytes.Contains(anthropic, []byte(`"type":"thinking"`)))
	require.False(t, thinkingFilterMayChange(anthropic))
	require.True(t, thinkingFilterMayChange(syntheticAnthropicBody(120<<10, true)))

	// The hosted-search style "id == call_id" shape is rewritten by the dedupe pass.
	collide := []byte(`{"input":[{"type":"function_call","id":"call_1","call_id":"call_1","name":"f","arguments":"{}"}]}`)
	require.True(t, responsesBodyMayRewrite(collide, responsesRewriteScope{dedupe: true}))
	// OpenAI's own shape (fc_ item id, call_ call id) is not.
	openai := []byte(`{"input":[{"type":"function_call","id":"fc_1","call_id":"call_1","name":"f","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":"ok"}]}`)
	require.False(t, responsesBodyMayRewrite(openai, responsesRewriteScope{dedupe: true}))
}

func TestProbesEmptyAndNilBodies(t *testing.T) {
	relay := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	deepseek := &Account{Platform: PlatformDeepseek, Type: AccountTypeAPIKey}
	for _, body := range [][]byte{nil, {}, []byte(" "), []byte("\n")} {
		require.Equal(t, legacyRewriteInboundDeepSeekCallIDs(PlatformOpenAI, "alias", body), RewriteInboundDeepSeekCallIDs(PlatformOpenAI, "alias", body))
		for _, account := range []*Account{nil, relay, deepseek} {
			require.Equal(t, legacyNormalizeDeepSeekResponsesRequestBody(account, body), normalizeDeepSeekResponsesRequestBody(account, body))
		}
		wantOut, wantErr := legacyNormalizeStrictChatDeveloperRoles(deepseek, "https://api.deepseek.com/v1/chat/completions", body)
		gotOut, gotErr := normalizeStrictChatDeveloperRoles(deepseek, "https://api.deepseek.com/v1/chat/completions", body)
		require.Equal(t, wantOut, gotOut)
		require.Equal(t, fmt.Sprint(wantErr), fmt.Sprint(gotErr))
		require.Equal(t, legacyFilterThinkingBlocksInternal(body), filterThinkingBlocksInternal(body, false))
		wantOut, wantErr = legacyFilterEmptyPartsFromGeminiRequest(body)
		gotOut, gotErr = filterEmptyPartsFromGeminiRequest(body)
		require.Equal(t, wantOut, gotOut)
		require.Equal(t, fmt.Sprint(wantErr), fmt.Sprint(gotErr))
		wantOut, wantChanged, wantLiteErr := originalLiteParallelToolCalls(body)
		gotOut, gotChanged, gotLiteErr := normalizeOpenAIResponsesLiteParallelToolCallsPayload(body)
		require.Equal(t, wantOut, gotOut)
		require.Equal(t, wantChanged, gotChanged)
		require.Equal(t, fmt.Sprint(wantLiteErr), fmt.Sprint(gotLiteErr))
	}
}

// atU spells unicode escapes as "@uXXXX" in test sources and turns them into
// real JSON escapes, so the sources carry no backslash sequences to mangle.
func atU(s string) string { return strings.ReplaceAll(s, "@u", `\u`) }

// Constructs the probes deliberately do not interpret must reach the decoder.
func TestProbesHandOverUnusualBodies(t *testing.T) {
	deep := strings.Repeat("[", jsonProbeMaxDepth+8) + strings.Repeat("]", jsonProbeMaxDepth+8)
	relay := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	for name, body := range map[string]string{
		"nesting beyond the probe depth": `{"input":[` + deep + `]}`,
		"escaped model name":             atU(`{"model":"deepse@u0065k-flash","input":[{"type":"function_call","call_id":"call_a","name":"f"}]}`),
		"escaped non-ascii id":           atU(`{"input":[{"type":"function_call","call_id":"call_@u00e9","name":"f"},{"type":"function_call","call_id":"call_@u00e9","name":"f"}]}`),
		"raw non-ascii id":               `{"input":[{"type":"function_call","call_id":"call_é","name":"f"},{"type":"function_call","call_id":"call_é","name":"f"}]}`,
		"escaped key":                    atU(`{"inp@u0075t":[{"type":"function_call","call_id":"call_a"},{"type":"function_call","call_id":"call_a"}]}`),
		"truncated array":                `{"input":[{"type":"function_call","call_id":"call_a"}`,
		"duplicate root input":           `{"input":[],"input":[{"type":"function_call","call_id":"call_a"},{"type":"function_call","call_id":"call_a"}]}`,
		"duplicate type in item":         `{"input":[{"type":"message","type":"function_call","call_id":"call_a"},{"type":"function_call","call_id":"call_a"}]}`,
	} {
		b := []byte(body)
		require.Equalf(t, string(legacyRewriteInboundDeepSeekCallIDs(PlatformOpenAI, "alias", b)), string(RewriteInboundDeepSeekCallIDs(PlatformOpenAI, "alias", b)), name)
		require.Equalf(t, string(legacyNormalizeDeepSeekResponsesRequestBody(relay, b)), string(normalizeDeepSeekResponsesRequestBody(relay, b)), name)
		require.Truef(t, responsesBodyMayRewrite(b, responsesRewriteScope{dedupe: true, reasoning: true}), name)
	}
	// With nothing else to rewrite, the escaped model name alone must force the decoder.
	hi := `"input":[{"type":"message","role":"user","content":"hi"}]}`
	require.True(t, responsesBodyMayRewrite([]byte(atU(`{"model":"deepse@u0065k-flash",`+hi)), responsesRewriteScope{reasoning: true}))
	require.False(t, responsesBodyMayRewrite([]byte(`{"model":"deepseek-flash",`+hi), responsesRewriteScope{reasoning: true}))
}

func TestNormalizeDeepSeekResponsesRequestBodySkipsMissingReasoningSwitches(t *testing.T) {
	// Exercise each branch of the guarded switch removal against the old code.
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	for _, reasoning := range []string{
		`{}`, `{"effort":"high"}`, `{"summary":"auto"}`, `{"generate_summary":"x"}`, `{"summary":"auto","generate_summary":"x"}`,
		`{"summary":"auto","effort":"low"}`, `"high"`, `null`, `[]`, `{ }`, `{"summary":null}`,
	} {
		for _, extra := range []string{``, `,"reasoning":{"summary":"later"}`} {
			doc := `{"model":"gpt-5","input":[{"type":"function_call","call_id":"call_1","name":"f","arguments":"{}"}],"reasoning":` + reasoning + extra + `}`
			body := []byte(doc)
			require.Equalf(t, string(legacyNormalizeDeepSeekResponsesRequestBody(account, body)), string(normalizeDeepSeekResponsesRequestBody(account, body)), doc)
		}
	}
}

// ---------------------------------------------------------------------------
// Benchmarks: legacy (frozen oracle) against the optimized functions, on bodies
// shaped like real agent traffic.
// ---------------------------------------------------------------------------

func byteSizeName(n int) string {
	if n >= 1<<20 {
		return fmt.Sprintf("%dMB", n>>20)
	}
	return fmt.Sprintf("%dKB", n>>10)
}

// syntheticResponsesBody builds a Codex-style Responses body of about target
// bytes: instructions, tools, and an input history of reasoning, function_call
// and function_call_output items. repeat plants one repeated call id, which the
// rewrite has to fix.
func syntheticResponsesBody(target int, repeat bool) []byte {
	r := rand.New(rand.NewSource(7))
	var b strings.Builder
	b.WriteString(`{"model":"gpt-5","instructions":"`)
	b.WriteString(strings.Repeat(`You are a coding agent.\nUse the \"shell\" tool.\n`, 160))
	b.WriteString(`","tools":[`)
	for i := 0; i < 12; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"type":"function","name":"tool_%d","description":"does thing %d","strict":false,"parameters":{"type":"object","properties":{"cmd":{"type":"string","description":"command to run"},"timeout":{"type":"number"}},"required":["cmd"]}}`, i, i)
	}
	b.WriteString(`],"tool_choice":"auto","parallel_tool_calls":true,"reasoning":{"effort":"high"},"store":false,"stream":true,"prompt_cache_key":"cache-key","input":[`)
	b.WriteString(`{"type":"message","role":"user","content":[{"type":"input_text","text":"fix the failing test"}]}`)
	for i := 0; b.Len() < target; i++ {
		fmt.Fprintf(&b, `,{"type":"reasoning","id":"rs_%d","summary":[],"encrypted_content":"%s"}`, i, strings.Repeat("gAAAAAB", 120))
		fmt.Fprintf(&b, `,{"type":"function_call","id":"fc_%d","call_id":"call_%d","name":"shell","arguments":"{\"cmd\":\"ls -la\",\"workdir\":\"/src\"}"}`, i, i)
		out := strings.Repeat(fmt.Sprintf("line %d of tool output \\\"quoted\\\" %d\\n", i, r.Intn(1000)), 40)
		callID := fmt.Sprintf("call_%d", i)
		if repeat && i == 1 {
			callID = "call_0"
		}
		fmt.Fprintf(&b, `,{"type":"function_call_output","call_id":"%s","output":"%s"}`, callID, out)
	}
	b.WriteString(`]}`)
	return []byte(b.String())
}

func syntheticChatBody(target int, developer bool) []byte {
	var b strings.Builder
	b.WriteString(`{"model":"deepseek-chat","stream":true,"messages":[{"role":"system","content":"` + strings.Repeat(`be brief\n`, 100) + `"}`)
	for i := 0; b.Len() < target; i++ {
		fmt.Fprintf(&b, `,{"role":"user","content":"question %d %s"}`, i, strings.Repeat("lorem ipsum \\\"dolor\\\" sit\\n", 40))
		fmt.Fprintf(&b, `,{"role":"assistant","content":null,"tool_calls":[{"id":"call_%d","type":"function","function":{"name":"f","arguments":"{}"}}]}`, i)
		fmt.Fprintf(&b, `,{"role":"tool","tool_call_id":"call_%d","content":"%s"}`, i, strings.Repeat("result line\\n", 60))
	}
	if developer {
		b.WriteString(`,{"role":"developer","content":"note"}`)
	}
	b.WriteString(`]}`)
	return []byte(b.String())
}

func syntheticAnthropicBody(target int, unsigned bool) []byte {
	var b strings.Builder
	b.WriteString(`{"model":"claude-sonnet-4","max_tokens":4096,"thinking":{"type":"enabled","budget_tokens":2048},"system":[{"type":"text","text":"` + strings.Repeat(`system prompt\n`, 100) + `"}],"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}`)
	for i := 0; b.Len() < target; i++ {
		sig := strings.Repeat("EqQBCkYIBRgC", 80)
		if unsigned && i == 1 {
			sig = ""
		}
		fmt.Fprintf(&b, `,{"role":"assistant","content":[{"type":"thinking","thinking":"%s","signature":"%s"},{"type":"tool_use","id":"toolu_%d","name":"bash","input":{"command":"ls"}}]}`,
			strings.Repeat("considering options\\n", 30), sig, i)
		fmt.Fprintf(&b, `,{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_%d","content":"%s"}]}`, i, strings.Repeat("output line\\n", 60))
	}
	b.WriteString(`]}`)
	return []byte(b.String())
}

func BenchmarkRewriteInboundDeepSeekCallIDs(b *testing.B) {
	for _, size := range []int{50 << 10, 300 << 10, 1 << 20} {
		for _, hit := range []bool{false, true} {
			body := syntheticResponsesBody(size, hit)
			name := byteSizeName(size) + "/noop"
			if hit {
				name = byteSizeName(size) + "/rewrite"
			}
			b.Run(name+"/legacy", func(b *testing.B) {
				b.SetBytes(int64(len(body)))
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					legacyRewriteInboundDeepSeekCallIDs(PlatformOpenAI, "gpt-5", body)
				}
			})
			b.Run(name+"/new", func(b *testing.B) {
				b.SetBytes(int64(len(body)))
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					RewriteInboundDeepSeekCallIDs(PlatformOpenAI, "gpt-5", body)
				}
			})
		}
	}
}

func BenchmarkNormalizeDeepSeekResponsesRequestBody(b *testing.B) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	for _, size := range []int{50 << 10, 300 << 10, 1 << 20} {
		for _, hit := range []bool{false, true} {
			body := syntheticResponsesBody(size, hit)
			name := byteSizeName(size) + "/noop"
			if hit {
				name = byteSizeName(size) + "/rewrite"
			}
			b.Run(name+"/legacy", func(b *testing.B) {
				b.SetBytes(int64(len(body)))
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					legacyNormalizeDeepSeekResponsesRequestBody(account, body)
				}
			})
			b.Run(name+"/new", func(b *testing.B) {
				b.SetBytes(int64(len(body)))
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					normalizeDeepSeekResponsesRequestBody(account, body)
				}
			})
		}
	}
}

func BenchmarkNormalizeStrictChatDeveloperRoles(b *testing.B) {
	account := &Account{Platform: PlatformDeepseek, Type: AccountTypeAPIKey}
	const target = "https://api.deepseek.com/v1/chat/completions"
	for _, size := range []int{50 << 10, 300 << 10, 1 << 20} {
		for _, hit := range []bool{false, true} {
			body := syntheticChatBody(size, hit)
			name := byteSizeName(size) + "/noop"
			if hit {
				name = byteSizeName(size) + "/rewrite"
			}
			b.Run(name+"/legacy", func(b *testing.B) {
				b.SetBytes(int64(len(body)))
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					if _, err := legacyNormalizeStrictChatDeveloperRoles(account, target, body); err != nil {
						b.Fatal(err)
					}
				}
			})
			b.Run(name+"/new", func(b *testing.B) {
				b.SetBytes(int64(len(body)))
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					if _, err := normalizeStrictChatDeveloperRoles(account, target, body); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func BenchmarkFilterThinkingBlocksInternal(b *testing.B) {
	for _, size := range []int{50 << 10, 300 << 10, 1 << 20} {
		for _, hit := range []bool{false, true} {
			body := syntheticAnthropicBody(size, hit)
			name := byteSizeName(size) + "/noop"
			if hit {
				name = byteSizeName(size) + "/rewrite"
			}
			b.Run(name+"/legacy", func(b *testing.B) {
				b.SetBytes(int64(len(body)))
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					legacyFilterThinkingBlocksInternal(body)
				}
			})
			b.Run(name+"/new", func(b *testing.B) {
				b.SetBytes(int64(len(body)))
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					filterThinkingBlocksInternal(body, false)
				}
			})
		}
	}
}

func BenchmarkLiteParallelToolCallsPinned(b *testing.B) {
	for _, size := range []int{50 << 10, 300 << 10, 1 << 20} {
		body := syntheticResponsesBody(size, false)
		body = []byte(strings.Replace(string(body), `"parallel_tool_calls":true`, `"parallel_tool_calls":false`, 1))
		b.Run(byteSizeName(size)+"/legacy", func(b *testing.B) {
			b.SetBytes(int64(len(body)))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				root := parseRawJSONView(body)
				var parallel gjson.Result
				root.ForEach(func(key, value gjson.Result) bool {
					if key.Str == "parallel_tool_calls" {
						parallel = value
					}
					return true
				})
				if !(parallel.Type == gjson.False && json.Valid(body)) {
					b.Fatal("expected pinned body")
				}
			}
		})
		b.Run(byteSizeName(size)+"/new", func(b *testing.B) {
			b.SetBytes(int64(len(body)))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, changed, err := normalizeOpenAIResponsesLiteParallelToolCallsPayload(body); err != nil || changed {
					b.Fatal("expected no-op")
				}
			}
		})
	}
}

func BenchmarkFilterEmptyPartsFromGeminiRequest(b *testing.B) {
	build := func(target int, empty bool) []byte {
		var sb strings.Builder
		sb.WriteString(`{"generationConfig":{"temperature":0.5},"contents":[`)
		for i := 0; sb.Len() < target; i++ {
			if i > 0 {
				sb.WriteString(",")
			}
			fmt.Fprintf(&sb, `{"role":"user","parts":[{"text":"%s"}]},{"role":"model","parts":[{"functionCall":{"name":"f","args":{"q":"%d"}}}]}`, strings.Repeat("lorem ipsum \\\"dolor\\\" sit\\n", 40), i)
		}
		if empty {
			sb.WriteString(`,{"role":"model","parts":[]}`)
		}
		sb.WriteString(`]}`)
		return []byte(sb.String())
	}
	for _, size := range []int{50 << 10, 300 << 10, 1 << 20} {
		for _, hit := range []bool{false, true} {
			body := build(size, hit)
			name := byteSizeName(size) + "/noop"
			if hit {
				name = byteSizeName(size) + "/rewrite"
			}
			b.Run(name+"/legacy", func(b *testing.B) {
				b.SetBytes(int64(len(body)))
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					if _, err := legacyFilterEmptyPartsFromGeminiRequest(body); err != nil {
						b.Fatal(err)
					}
				}
			})
			b.Run(name+"/new", func(b *testing.B) {
				b.SetBytes(int64(len(body)))
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					if _, err := filterEmptyPartsFromGeminiRequest(body); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// Fuzz targets: `go test -fuzz=FuzzRewriteInboundCallIDs ./internal/service/` hunts for a
// body where a probe says "no change" but the original pass would rewrite it
// (the oracle comparison fails). Without -fuzz they replay the seeds below.
var probeFuzzResponsesSeeds = []string{
	`{"model":"gpt-5","input":[{"type":"function_call","id":"fc_1","call_id":"call_1","name":"f"},{"type":"function_call_output","call_id":"call_1","output":"ok"}]}`,
	`{"input":[{"type":"function_call","id":"call_1","call_id":"call_1"},{"type":"function_call","id":"call_1","call_id":"call_1"}]}`,
	`{"input":[{"type":"function_call","call_id":"call_1","content":[{"type":"function_call","call_id":"call_1"}]}]}`,
	`{"model":"deepseek-chat","reasoning":{"summary":"auto"},"input":[{"type":"reasoning","id":"","encrypted_content":"x"},{"type":"function_call","call_id":"call_1"}]}`,
	`{"messages":[{"role":"assistant","tool_calls":[{"id":"a"},{"id":"a"}]},{"role":"tool","tool_call_id":"a"}]}`,
	`{"input":[[{"type":"x","call_id":"c"}],[{"type":"x","call_id":"c"}]]}`,
	`{"input":[{"type":"function_call","type":"message","call_id":"call_1"},{"type":"function_call","call_id":"call_1"}]}`,
	`{"inp\u0075t":[{"type":"function_call","call_id":"call_1"},{"type":"function_call","call_id":"call_1"}]}`,
}

func FuzzRewriteInboundCallIDs(f *testing.F) {
	for _, s := range probeFuzzResponsesSeeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		want := legacyRewriteInboundDeepSeekCallIDs(PlatformOpenAI, "alias", body)
		got := RewriteInboundDeepSeekCallIDs(PlatformOpenAI, "alias", body)
		if !bytes.Equal(want, got) || sameBytes(want, body) != sameBytes(got, body) {
			t.Fatalf("body %q: legacy %q, got %q", body, want, got)
		}
	})
}

func FuzzNormalizeDeepSeekResponsesBody(f *testing.F) {
	for _, s := range probeFuzzResponsesSeeds {
		f.Add([]byte(s), false)
		f.Add([]byte(s), true)
	}
	f.Fuzz(func(t *testing.T, body []byte, deepseek bool) {
		account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}
		if deepseek {
			account = &Account{Platform: PlatformDeepseek, Type: AccountTypeAPIKey}
		}
		want := legacyNormalizeDeepSeekResponsesRequestBody(account, body)
		got := normalizeDeepSeekResponsesRequestBody(account, body)
		if !bytes.Equal(want, got) || sameBytes(want, body) != sameBytes(got, body) {
			t.Fatalf("body %q: legacy %q, got %q", body, want, got)
		}
	})
}

func FuzzStrictChatDeveloperRolesBody(f *testing.F) {
	for _, s := range []string{
		`{"messages":[{"role":"user","content":"x"},{"role":"developer","content":"y"}]}`,
		`{"messages":[{"role":"user"}]}`, `{"messages":null}`, `{"messages":[null]}`, `{"messages":[{"content":"x"}]}`,
		`{"messages":[{"role":null}]}`, `{"messages":[{"role":"develop\u0065r"}]}`, `{"messages":[{"role":5}]}`, `[]`, `null`,
		`{"messages":[{"role":"user","role":"developer"}]}`, `{"messages":[]}`, `{}`,
	} {
		f.Add([]byte(s))
	}
	account := &Account{Platform: PlatformDeepseek, Type: AccountTypeAPIKey}
	f.Fuzz(func(t *testing.T, body []byte) {
		want, wantErr := legacyNormalizeStrictChatDeveloperRoles(account, "https://api.deepseek.com/v1/chat/completions", body)
		got, gotErr := normalizeStrictChatDeveloperRoles(account, "https://api.deepseek.com/v1/chat/completions", body)
		if fmt.Sprint(wantErr) != fmt.Sprint(gotErr) || !bytes.Equal(want, got) || sameBytes(want, body) != sameBytes(got, body) {
			t.Fatalf("body %q: legacy %q/%v, got %q/%v", body, want, wantErr, got, gotErr)
		}
	})
}

func FuzzFilterThinkingBlocksBody(f *testing.F) {
	for _, s := range []string{
		`{"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"t","signature":"abc"},{"type":"text","text":"x"}]}]}`,
		`{"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"t","signature":""}]}]}`,
		`{"messages":[{"role":"user","content":[{"type":"thinking","signature":"abc"}]}]}`,
		`{"messages":[{"role":"assistant","content":[{"thinking":"t"}]}]}`,
		`{"messages":[{"role":"assistant","content":[{"type":"thinking","signature":"skip_thought_signature_validator"}]}]}`,
		`{"messages":[{"role":"assistant","content":[{"type":"thinking","type":"text","signature":"a"}]}]}`,
		`{"messages":[{"role":"assistant","role":"user","content":[{"type":"thinking","signature":"a"}]}]}`,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		want := legacyFilterThinkingBlocksInternal(body)
		got := filterThinkingBlocksInternal(body, false)
		if !bytes.Equal(want, got) || sameBytes(want, body) != sameBytes(got, body) {
			t.Fatalf("body %q: legacy %q, got %q", body, want, got)
		}
	})
}

func FuzzFilterEmptyPartsFromGeminiBody(f *testing.F) {
	for _, s := range []string{
		`{"contents":[{"role":"user","parts":[{"text":"x"}]}]}`, `{"contents":[{"role":"user","parts":[]}]}`, `{"contents":[{"parts":null}]}`,
		`{"contents":[1,null,{"parts":[ ]}]}`, `{"contents":{}}`, `[]`, `{"contents":[{"parts":[],"parts":[1]}]}`, `{"contents":[],"contents":[{"parts":[]}]}`,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		want, wantErr := legacyFilterEmptyPartsFromGeminiRequest(body)
		got, gotErr := filterEmptyPartsFromGeminiRequest(body)
		if fmt.Sprint(wantErr) != fmt.Sprint(gotErr) || !bytes.Equal(want, got) || sameBytes(want, body) != sameBytes(got, body) {
			t.Fatalf("body %q: legacy %q/%v, got %q/%v", body, want, wantErr, got, gotErr)
		}
	})
}
