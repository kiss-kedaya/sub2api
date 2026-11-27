package service

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/tidwall/gjson"
)

// contentSessionSeedPrefix prevents collisions between content-derived seeds
// and explicit session IDs (e.g. "sess-xxx" or "compat_cc_xxx").
const contentSessionSeedPrefix = "compat_cs_"

// contentStablePrefixSessionSeedPrefix distinguishes cache identities derived
// only from request fields that remain stable across independent prompts.
const contentStablePrefixSessionSeedPrefix = "compat_csp_"

// deriveOpenAIContentSessionSeed builds a stable session seed from an
// OpenAI-format request body. Only fields constant across conversation turns
// are included: model, tools/functions definitions, the leading system/developer
// prompt prefix in Chat messages, instructions (Responses API), and the first
// user message.
// Supports both Chat Completions (messages) and Responses API (input).
func deriveOpenAIContentSessionSeed(body []byte) string {
	if len(body) == 0 {
		return ""
	}

	const (
		modelField = iota
		toolsField
		functionsField
		instructionsField
		messagesField
		inputField
		contentSessionSeedFieldCount
		allContentSessionSeedFields = 1<<contentSessionSeedFieldCount - 1
	)
	var fields [contentSessionSeedFieldCount]gjson.Result
	var seen uint8
	// Match gjson.GetBytes by starting at the first root container, even when
	// malformed input has a non-JSON prefix.
	root := body
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '{':
			root = body[i:]
			goto scanRoot
		case '[':
			return ""
		}
	}
	return ""

scanRoot:
	nextKeyOffset := 1
	parseRawJSONView(root).ForEach(func(key, value gjson.Result) bool {
		if key.Index < nextKeyOffset || key.Index > len(root) {
			return false
		}
		// Result.ForEach can continue after the root '}' on malformed input.
		// The separator range excludes braces inside the preceding parsed value.
		if bytes.IndexByte(root[nextKeyOffset:key.Index], '}') >= 0 {
			return false
		}
		nextKeyOffset = value.Index + len(value.Raw)

		field := -1
		switch key.Str {
		case "model":
			field = modelField
		case "tools":
			field = toolsField
		case "functions":
			field = functionsField
		case "instructions":
			field = instructionsField
		case "messages":
			field = messagesField
		case "input":
			field = inputField
		}
		if field < 0 {
			return true
		}
		mask := uint8(1 << field)
		if seen&mask == 0 {
			fields[field] = value
			seen |= mask
		}
		return seen != allContentSessionSeedFields
	})

	var b strings.Builder

	if model := fields[modelField].String(); model != "" {
		_, _ = b.WriteString("model=")
		_, _ = b.WriteString(model)
	}

	if tools := fields[toolsField]; tools.Exists() && tools.IsArray() && tools.Raw != "[]" {
		_, _ = b.WriteString("|tools=")
		_, _ = b.WriteString(normalizeCompatSeedJSON(json.RawMessage(tools.Raw)))
	}

	if funcs := fields[functionsField]; funcs.Exists() && funcs.IsArray() && funcs.Raw != "[]" {
		_, _ = b.WriteString("|functions=")
		_, _ = b.WriteString(normalizeCompatSeedJSON(json.RawMessage(funcs.Raw)))
	}

	if instr := fields[instructionsField].String(); instr != "" {
		_, _ = b.WriteString("|instructions=")
		_, _ = b.WriteString(instr)
	}

	firstUserCaptured := false

	msgs := fields[messagesField]
	if msgs.Exists() && msgs.IsArray() {
		systemPrefixOpen := true
		msgs.ForEach(func(_, msg gjson.Result) bool {
			role := msg.Get("role").String()
			switch role {
			case "system", "developer":
				if systemPrefixOpen {
					_, _ = b.WriteString("|system=")
					if c := msg.Get("content"); c.Exists() {
						_, _ = b.WriteString(normalizeCompatSeedJSON(json.RawMessage(c.Raw)))
					}
				}
			case "user":
				systemPrefixOpen = false
				if !firstUserCaptured {
					_, _ = b.WriteString("|first_user=")
					if c := msg.Get("content"); c.Exists() {
						_, _ = b.WriteString(normalizeCompatSeedJSON(json.RawMessage(c.Raw)))
					}
					firstUserCaptured = true
				}
			default:
				systemPrefixOpen = false
			}
			return true
		})
	} else if inp := fields[inputField]; inp.Exists() {
		if inp.Type == gjson.String {
			_, _ = b.WriteString("|input=")
			_, _ = b.WriteString(inp.String())
		} else if inp.IsArray() {
			inp.ForEach(func(_, item gjson.Result) bool {
				role := item.Get("role").String()
				switch role {
				case "system", "developer":
					_, _ = b.WriteString("|system=")
					if c := item.Get("content"); c.Exists() {
						_, _ = b.WriteString(normalizeCompatSeedJSON(json.RawMessage(c.Raw)))
					}
				case "user":
					if !firstUserCaptured {
						_, _ = b.WriteString("|first_user=")
						if c := item.Get("content"); c.Exists() {
							_, _ = b.WriteString(normalizeCompatSeedJSON(json.RawMessage(c.Raw)))
						}
						firstUserCaptured = true
					}
				}
				if !firstUserCaptured && item.Get("type").String() == "input_text" {
					_, _ = b.WriteString("|first_user=")
					if text := item.Get("text").String(); text != "" {
						_, _ = b.WriteString(text)
					}
					firstUserCaptured = true
				}
				return true
			})
		}
	}

	if b.Len() == 0 {
		return ""
	}
	return contentSessionSeedPrefix + b.String()
}

// deriveOpenAIAnchoredContentSessionSeed returns the legacy content-derived
// seed only when it contains a meaningful user/input anchor. This preserves
// the existing session derivation while preventing model-only requests from
// becoming a tenant-wide cache routing identity.
func deriveOpenAIAnchoredContentSessionSeed(body []byte) string {
	if !hasOpenAIContentSessionUserAnchor(body) {
		return ""
	}
	return deriveOpenAIContentSessionSeed(body)
}

func hasOpenAIContentSessionUserAnchor(body []byte) bool {
	if len(body) == 0 {
		return false
	}

	var messagesRaw string
	var hasMessages bool
	var inputRaw string
	var hasInput bool
	ok := jsonForEachMember(jsonView(body), func(key, val string) bool {
		switch key {
		case "messages":
			if !hasMessages {
				messagesRaw = val
				hasMessages = true
			}
		case "input":
			if !hasInput {
				inputRaw = val
				hasInput = true
			}
		}
		return true
	})
	if !ok {
		return hasOpenAIContentSessionUserAnchorGJSON(body)
	}

	var memberBack [32]canonMember
	var keyBack [256]byte
	var strBack [512]byte
	scratch := canonScratch{
		members: memberBack[:0],
		keyBuf:  keyBack[:0],
		strBuf:  strBack[:0],
	}
	var canonBack [512]byte
	canonBuf := canonBack[:0]

	if hasMessages && len(messagesRaw) > 0 && messagesRaw[0] == '[' {
		anchored := false
		walkOK := jsonForEachElement(messagesRaw, func(msgRaw string) bool {
			if len(msgRaw) == 0 || msgRaw[0] != '{' {
				return true
			}
			var roleRaw, contentRaw string
			var hasContent bool
			mOK := jsonForEachMember(msgRaw, func(k, v string) bool {
				switch k {
				case "role":
					if roleRaw == "" {
						roleRaw = v
					}
				case "content":
					if !hasContent {
						contentRaw = v
						hasContent = true
					}
				}
				return true
			})
			if !mOK {
				msg := gjson.Parse(msgRaw)
				if strings.TrimSpace(msg.Get("role").String()) != "user" {
					return true
				}
				anchored = hasMeaningfulOpenAIContent(msg.Get("content"))
				return false
			}
			if strings.TrimSpace(jsonAnyRawStringValue(roleRaw)) != "user" {
				return true
			}
			anchored = hasMeaningfulOpenAIContentRaw(contentRaw, hasContent, &canonBuf, &scratch)
			return false
		})
		if walkOK {
			return anchored
		}
		return hasOpenAIContentSessionUserAnchorGJSON(body)
	}

	if !hasInput || len(inputRaw) == 0 {
		return false
	}
	if inputRaw[0] == '"' {
		return strings.TrimSpace(jsonRawStringValue(inputRaw)) != ""
	}
	if inputRaw[0] != '[' {
		return false
	}

	anchored := false
	walkOK := jsonForEachElement(inputRaw, func(itemRaw string) bool {
		if len(itemRaw) == 0 || itemRaw[0] != '{' {
			return true
		}
		var roleRaw, typeRaw, contentRaw, textRaw string
		var hasContent bool
		mOK := jsonForEachMember(itemRaw, func(k, v string) bool {
			switch k {
			case "role":
				if roleRaw == "" {
					roleRaw = v
				}
			case "type":
				if typeRaw == "" {
					typeRaw = v
				}
			case "content":
				if !hasContent {
					contentRaw = v
					hasContent = true
				}
			case "text":
				if textRaw == "" {
					textRaw = v
				}
			}
			return true
		})
		if !mOK {
			item := gjson.Parse(itemRaw)
			if strings.TrimSpace(item.Get("role").String()) == "user" {
				anchored = hasMeaningfulOpenAIContent(item.Get("content"))
				return false
			}
			if strings.TrimSpace(item.Get("type").String()) == "input_text" {
				anchored = strings.TrimSpace(item.Get("text").String()) != ""
				return false
			}
			return true
		}
		if strings.TrimSpace(jsonAnyRawStringValue(roleRaw)) == "user" {
			anchored = hasMeaningfulOpenAIContentRaw(contentRaw, hasContent, &canonBuf, &scratch)
			return false
		}
		if strings.TrimSpace(jsonAnyRawStringValue(typeRaw)) == "input_text" {
			anchored = strings.TrimSpace(jsonAnyRawStringValue(textRaw)) != ""
			return false
		}
		return true
	})
	if walkOK {
		return anchored
	}
	return hasOpenAIContentSessionUserAnchorGJSON(body)
}

func hasOpenAIContentSessionUserAnchorGJSON(body []byte) bool {
	root := parseRawJSONView(body)
	if messages := root.Get("messages"); messages.Exists() && messages.IsArray() {
		anchored := false
		messages.ForEach(func(_, message gjson.Result) bool {
			if strings.TrimSpace(message.Get("role").String()) != "user" {
				return true
			}
			anchored = hasMeaningfulOpenAIContent(message.Get("content"))
			return false
		})
		return anchored
	}

	input := root.Get("input")
	if !input.Exists() {
		return false
	}
	if input.Type == gjson.String {
		return strings.TrimSpace(input.String()) != ""
	}
	if !input.IsArray() {
		return false
	}

	anchored := false
	input.ForEach(func(_, item gjson.Result) bool {
		if strings.TrimSpace(item.Get("role").String()) == "user" {
			anchored = hasMeaningfulOpenAIContent(item.Get("content"))
			return false
		}
		if strings.TrimSpace(item.Get("type").String()) == "input_text" {
			anchored = strings.TrimSpace(item.Get("text").String()) != ""
			return false
		}
		return true
	})
	return anchored
}

func hasMeaningfulOpenAIContentRaw(contentRaw string, hasContent bool, canonBuf *[]byte, scratch *canonScratch) bool {
	if !hasContent || contentRaw == "" || contentRaw == "null" {
		return false
	}
	if contentRaw[0] == '"' {
		return strings.TrimSpace(jsonRawStringValue(contentRaw)) != ""
	}
	if contentRaw[0] != '[' {
		*canonBuf = appendCompatSeedJSON((*canonBuf)[:0], contentRaw, scratch)
		return isNonEmptyCompatSeedBytes(*canonBuf) && len(bytes.TrimSpace(*canonBuf)) > 0
	}

	meaningful := false
	walkOK := jsonForEachElement(contentRaw, func(itemRaw string) bool {
		if len(itemRaw) > 0 && itemRaw[0] == '"' {
			meaningful = strings.TrimSpace(jsonRawStringValue(itemRaw)) != ""
		} else if len(itemRaw) > 0 && itemRaw[0] == '{' {
			var textRaw string
			var hasText bool
			mOK := jsonForEachMember(itemRaw, func(k, v string) bool {
				if k == "text" && !hasText {
					textRaw = v
					hasText = true
					return false
				}
				return true
			})
			if !mOK {
				item := gjson.Parse(itemRaw)
				if text := item.Get("text"); text.Exists() {
					meaningful = strings.TrimSpace(text.String()) != ""
				} else {
					_, meaningful = normalizeNonEmptyCompatSeedJSON(item)
				}
			} else if hasText {
				meaningful = strings.TrimSpace(jsonAnyRawStringValue(textRaw)) != ""
			} else {
				*canonBuf = appendCompatSeedJSON((*canonBuf)[:0], itemRaw, scratch)
				meaningful = isNonEmptyCompatSeedBytes(*canonBuf)
			}
		} else {
			*canonBuf = appendCompatSeedJSON((*canonBuf)[:0], itemRaw, scratch)
			meaningful = isNonEmptyCompatSeedBytes(*canonBuf)
		}
		return !meaningful
	})
	if walkOK {
		return meaningful
	}
	return hasMeaningfulOpenAIContent(gjson.Parse(contentRaw))
}

func isNonEmptyCompatSeedBytes(b []byte) bool {
	switch string(b) {
	case "", `""`, "[]", "{}", "null":
		return false
	default:
		return true
	}
}

func hasMeaningfulOpenAIContent(content gjson.Result) bool {
	if !content.Exists() || content.Type == gjson.Null {
		return false
	}
	if content.Type == gjson.String {
		return strings.TrimSpace(content.String()) != ""
	}
	if !content.IsArray() {
		normalized, ok := normalizeNonEmptyCompatSeedJSON(content)
		return ok && strings.TrimSpace(normalized) != ""
	}

	meaningful := false
	content.ForEach(func(_, item gjson.Result) bool {
		if item.Type == gjson.String {
			meaningful = strings.TrimSpace(item.String()) != ""
		} else if text := item.Get("text"); text.Exists() {
			meaningful = strings.TrimSpace(text.String()) != ""
		} else {
			_, meaningful = normalizeNonEmptyCompatSeedJSON(item)
		}
		return !meaningful
	})
	return meaningful
}

// deriveOpenAIStablePrefixSessionSeed builds a seed from the reusable prefix
// of an OpenAI-format request. User and assistant content are deliberately
// excluded so independent prompts with the same system/tool prefix can share
// an upstream prompt-cache routing identity.
//
// An empty result means the request has no meaningful stable prefix. Callers
// must then use a narrower fallback instead of grouping all requests by tenant
// and model alone.
func deriveOpenAIStablePrefixSessionSeed(body []byte) string {
	if len(body) == 0 {
		return ""
	}

	var b strings.Builder
	hasStablePrefix := false
	appendJSON := func(label string, value gjson.Result) {
		normalized, ok := normalizeNonEmptyCompatSeedJSON(value)
		if !ok {
			return
		}
		_, _ = b.WriteString("|")
		_, _ = b.WriteString(label)
		_, _ = b.WriteString("=")
		_, _ = b.WriteString(normalized)
		hasStablePrefix = true
	}

	if tools := gjson.GetBytes(body, "tools"); tools.Exists() && tools.IsArray() {
		appendJSON("tools", tools)
	}
	if funcs := gjson.GetBytes(body, "functions"); funcs.Exists() && funcs.IsArray() {
		appendJSON("functions", funcs)
	}
	if instructions := gjson.GetBytes(body, "instructions"); strings.TrimSpace(instructions.String()) != "" {
		appendJSON("instructions", instructions)
	}

	appendSystemMessages := func(items gjson.Result) {
		items.ForEach(func(_, item gjson.Result) bool {
			role := strings.TrimSpace(item.Get("role").String())
			switch role {
			case "system", "developer":
				appendJSON(role, item.Get("content"))
			}
			return true
		})
	}

	if messages := gjson.GetBytes(body, "messages"); messages.Exists() && messages.IsArray() {
		appendSystemMessages(messages)
	} else if input := gjson.GetBytes(body, "input"); input.Exists() && input.IsArray() {
		appendSystemMessages(input)
	}

	if !hasStablePrefix {
		return ""
	}
	return contentStablePrefixSessionSeedPrefix + b.String()
}

func normalizeNonEmptyCompatSeedJSON(value gjson.Result) (string, bool) {
	if !value.Exists() || value.Type == gjson.Null {
		return "", false
	}
	normalized := normalizeCompatSeedJSON(json.RawMessage(value.Raw))
	switch normalized {
	case "", `""`, "[]", "{}", "null":
		return "", false
	default:
		return normalized, true
	}
}
