package handler

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

// normalizeCodexDelegationBootstrap converts the call-less tool result emitted
// by Codex delegation into a user message. It is intentionally conservative:
// any call/reference context or malformed input leaves the request untouched.
func normalizeCodexDelegationBootstrap(body []byte) ([]byte, bool) {
	// 已有任务通过 send_message_to_thread 唤醒时会携带 previous_response_id；
	// 完整历史回放还会带有已配对的调用项。delegation 仍是客户端注入的用户输入，
	// 不属于这些历史调用的结果，因此允许它与可明确配对的历史上下文共存。
	return normalizeCodexCallOutputBootstrap(body, isCodexDelegationCandidate, true)
}

func normalizeCodexAutomationBootstrap(body []byte) ([]byte, bool) {
	return normalizeCodexCallOutputBootstrap(body, isCodexAutomationCandidate, false)
}

// applyCodexBootstrapNormalizations rewrites call-less Codex bootstrap tool
// results. Cheap gjson scan first: typical /v1/responses bodies have no
// automation/delegation items, and a full encoding/json Decoder walk of those
// bodies was the hottest CPU path on production.
func applyCodexBootstrapNormalizations(body []byte) (normalized []byte, automationChanged, delegationChanged bool) {
	if !codexBootstrapInputLooksRelevant(body) {
		return body, false, false
	}
	normalized = body
	if next, changed := normalizeCodexAutomationBootstrap(normalized); changed {
		normalized = next
		automationChanged = true
	}
	if next, changed := normalizeCodexDelegationBootstrap(normalized); changed {
		normalized = next
		delegationChanged = true
	}
	return normalized, automationChanged, delegationChanged
}

func codexBootstrapInputLooksRelevant(body []byte) bool {
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return false
	}
	found := false
	input.ForEach(func(_, item gjson.Result) bool {
		if item.Get("type").String() != "function_call_output" {
			return true
		}
		namespace := item.Get("namespace").String()
		name := item.Get("name").String()
		if isCodexDelegationTool(namespace, name) || (namespace == "codex_app" && name == "automation_update") {
			found = true
			return false
		}
		return true
	})
	return found
}

func normalizeCodexCallOutputBootstrap(body []byte, isCandidate func(map[string]any) bool, allowHistoricalContext bool) ([]byte, bool) {
	if !hasUniqueJSONMembers(body) {
		return body, false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var request map[string]any
	if err := decoder.Decode(&request); err != nil {
		return body, false
	}
	if previousResponseID, exists := request["previous_response_id"]; exists {
		value, ok := previousResponseID.(string)
		if !ok || (!allowHistoricalContext && strings.TrimSpace(value) != "") {
			return body, false
		}
	}
	input, ok := request["input"].([]any)
	if !ok {
		return body, false
	}

	// Responses built-ins follow the *_call / *_call_output naming convention,
	// so classify by the wire type shape instead of maintaining an incomplete
	// allowlist. Delegation may coexist with historical anchors only when their
	// IDs make them unambiguous; automation retains the bootstrap-only boundary.
	for _, raw := range input {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		typ := stringField(item, "type")
		if isCandidate(item) {
			callIDValue, exists := item["call_id"]
			callID, isString := callIDValue.(string)
			if exists && (!isString || strings.TrimSpace(callID) != "") {
				return body, false
			}
			continue
		}
		if typ == "item_reference" {
			if allowHistoricalContext && strings.TrimSpace(stringField(item, "id")) != "" {
				continue
			}
			return body, false
		}
		if strings.HasSuffix(typ, "_call") || isResponsesCallOutputType(typ) {
			if allowHistoricalContext && strings.TrimSpace(stringField(item, "call_id")) != "" {
				continue
			}
			return body, false
		}
	}

	changed := false
	for i, raw := range input {
		item, ok := raw.(map[string]any)
		if !ok || !isCandidate(item) {
			continue
		}
		output, ok := item["output"].(string)
		if !ok {
			continue
		}
		input[i] = map[string]any{
			"type": "message",
			"role": "user",
			"content": []any{map[string]any{
				"type": "input_text",
				"text": output,
			}},
		}
		changed = true
	}
	if !changed {
		return body, false
	}
	normalized, err := json.Marshal(request)
	if err != nil {
		return body, false
	}
	return normalized, true
}

func hasUniqueJSONMembers(body []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(body))
	if !consumeUniqueJSONValue(decoder) {
		return false
	}
	_, err := decoder.Token()
	return err == io.EOF
}

func consumeUniqueJSONValue(decoder *json.Decoder) bool {
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return true
	}
	switch delim {
	case '{':
		members := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return false
			}
			key, ok := keyToken.(string)
			if !ok {
				return false
			}
			if _, duplicate := members[key]; duplicate {
				return false
			}
			members[key] = struct{}{}
			if !consumeUniqueJSONValue(decoder) {
				return false
			}
		}
		end, err := decoder.Token()
		return err == nil && end == json.Delim('}')
	case '[':
		for decoder.More() {
			if !consumeUniqueJSONValue(decoder) {
				return false
			}
		}
		end, err := decoder.Token()
		return err == nil && end == json.Delim(']')
	default:
		return false
	}
}

func isResponsesCallOutputType(typ string) bool {
	return strings.HasSuffix(typ, "_call_output") || typ == "tool_search_output"
}

func isCodexDelegationCandidate(item map[string]any) bool {
	if stringField(item, "type") != "function_call_output" ||
		!isCodexDelegationTool(stringField(item, "namespace"), stringField(item, "name")) {
		return false
	}
	output, ok := item["output"].(string)
	return ok && validCodexDelegationEnvelope(output)
}

func isCodexAutomationCandidate(item map[string]any) bool {
	if stringField(item, "type") != "function_call_output" ||
		stringField(item, "namespace") != "codex_app" ||
		stringField(item, "name") != "automation_update" {
		return false
	}
	output, ok := item["output"].(string)
	return ok && (validCodexAutomationBootstrap(output) || validCodexAutomationHeartbeat(output))
}

func stringField(item map[string]any, key string) string {
	value, _ := item[key].(string)
	return value
}

func isCodexDelegationTool(namespace, name string) bool {
	return (namespace == "codex_app" || namespace == "codex_tui") &&
		(name == "create_thread" || name == "send_message_to_thread")
}

func validCodexAutomationBootstrap(value string) bool {
	normalized := strings.ReplaceAll(value, "\r\n", "\n")
	if strings.ContainsRune(normalized, '\r') {
		return false
	}
	lines := strings.Split(normalized, "\n")
	if len(lines) < 6 {
		return false
	}
	if _, ok := codexAutomationHeaderValue(lines[0], "Automation: "); !ok {
		return false
	}
	automationID, ok := codexAutomationHeaderValue(lines[1], "Automation ID: ")
	if !ok || !validCodexAutomationID(automationID) {
		return false
	}
	if lines[2] != "Automation memory: $CODEX_HOME/automations/"+automationID+"/memory.md" {
		return false
	}
	lastRun, ok := codexAutomationHeaderValue(lines[3], "Last run: ")
	if !ok || !validCodexAutomationLastRun(lastRun) || lines[4] != "" {
		return false
	}
	return strings.TrimSpace(strings.Join(lines[5:], "\n")) != ""
}

func codexAutomationHeaderValue(line, prefix string) (string, bool) {
	if !strings.HasPrefix(line, prefix) {
		return "", false
	}
	value := strings.TrimPrefix(line, prefix)
	return value, value != "" && strings.TrimSpace(value) == value
}

func validCodexAutomationID(value string) bool {
	if len(value) == 0 || len(value) > 128 || value == "." || value == ".." {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' {
			continue
		}
		return false
	}
	return true
}

func validCodexAutomationLastRun(value string) bool {
	if value == "never" {
		return true
	}
	separator := strings.LastIndex(value, " (")
	if separator <= 0 || !strings.HasSuffix(value, ")") {
		return false
	}
	runAt, err := time.Parse(time.RFC3339Nano, value[:separator])
	if err != nil {
		return false
	}
	epochMillis, err := strconv.ParseInt(value[separator+2:len(value)-1], 10, 64)
	return err == nil && runAt.UnixMilli() == epochMillis
}

func validCodexAutomationHeartbeat(value string) bool {
	decoder := xml.NewDecoder(strings.NewReader(value))
	var rootSeen, automationIDSeen bool
	var childName string
	var childText bytes.Buffer
	fields := make(map[string]string)
	depth := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			id := fields["automation_id"]
			timestamp, hasTime := fields["current_time_iso"]
			instructions, hasInstructions := fields["instructions"]
			timestamp = strings.TrimSpace(timestamp)
			instructions = strings.TrimSpace(instructions)
			if hasTime != hasInstructions {
				return false
			}
			if hasTime {
				if _, err := time.Parse(time.RFC3339Nano, timestamp); err != nil {
					if _, err := time.Parse(time.RFC3339, timestamp); err != nil || instructions == "" {
						return false
					}
				} else if instructions == "" {
					return false
				}
			}
			return rootSeen && automationIDSeen && depth == 0 &&
				strings.TrimSpace(id) == id && validCodexAutomationID(id)
		}
		if err != nil {
			return false
		}
		switch current := token.(type) {
		case xml.StartElement:
			depth++
			if current.Name.Space != "" || len(current.Attr) != 0 || depth > 2 {
				return false
			}
			if depth == 1 {
				if rootSeen || current.Name.Local != "heartbeat" {
					return false
				}
				rootSeen = true
			} else {
				childName = current.Name.Local
				switch childName {
				case "automation_id", "current_time_iso", "instructions":
				default:
					return false
				}
				if _, duplicate := fields[childName]; duplicate {
					return false
				}
				childText.Reset()
			}
		case xml.EndElement:
			if current.Name.Space != "" {
				return false
			}
			if depth == 2 {
				fields[childName] = childText.String()
				automationIDSeen = automationIDSeen || childName == "automation_id"
			}
			depth--
			if depth < 0 {
				return false
			}
		case xml.CharData:
			if depth == 2 {
				_, _ = childText.Write(current)
			} else if len(bytes.TrimSpace(current)) != 0 {
				return false
			}
		case xml.Comment, xml.ProcInst, xml.Directive:
			return false
		}
	}
}

func validCodexDelegationEnvelope(value string) bool {
	decoder := xml.NewDecoder(strings.NewReader(value))
	var rootSeen, sourceSeen, inputSeen bool
	var childName string
	var childText bytes.Buffer
	depth := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return rootSeen && depth == 0 && sourceSeen && inputSeen
		}
		if err != nil {
			return false
		}
		switch current := token.(type) {
		case xml.StartElement:
			depth++
			if current.Name.Space != "" || len(current.Attr) != 0 || (depth == 1 && current.Name.Local != "codex_delegation") || depth > 2 {
				return false
			}
			if depth == 1 {
				if rootSeen {
					return false
				}
				rootSeen = true
				continue
			}
			if current.Name.Local != "source_thread_id" && current.Name.Local != "input" {
				return false
			}
			childName = current.Name.Local
			childText.Reset()
		case xml.EndElement:
			if current.Name.Space != "" {
				return false
			}
			if depth == 2 {
				if current.Name.Local != childName || strings.TrimSpace(childText.String()) == "" {
					return false
				}
				if childName == "source_thread_id" {
					if sourceSeen {
						return false
					}
					sourceSeen = true
				} else {
					if inputSeen {
						return false
					}
					inputSeen = true
				}
				childName = ""
			}
			depth--
			if depth < 0 {
				return false
			}
		case xml.CharData:
			if depth == 2 {
				_, _ = childText.Write(current)
			} else if len(bytes.TrimSpace(current)) != 0 {
				return false
			}
		case xml.Comment, xml.ProcInst, xml.Directive:
			return false
		}
	}
}
