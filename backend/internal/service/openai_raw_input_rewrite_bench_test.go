package service

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

var benchmarkOpenAIRawRewriteSink []byte

func benchmarkOpenAIResponsesBody(itemCount, payloadBytes int, item func(int, string) string) []byte {
	payload := strings.Repeat("x", payloadBytes)
	items := make([]string, itemCount)
	for index := range items {
		items[index] = item(index, payload)
	}
	return []byte(`{"model":"gpt-5.6-sol","store":false,"input":[` + strings.Join(items, ",") + `]}`)
}

func benchmarkOpenAIRawRewrite(b *testing.B, body []byte, rewrite func([]byte) ([]byte, bool, error)) {
	b.Helper()
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for range b.N {
		result, changed, err := rewrite(body)
		if err != nil || !changed {
			b.Fatalf("rewrite failed: changed=%v err=%v", changed, err)
		}
		benchmarkOpenAIRawRewriteSink = result
	}
}

// The legacy implementations below are intentionally benchmark-only copies of
// the pre-optimization code. Keeping both paths in one test binary makes CPU
// and allocation profiles directly comparable on the same host and fixture.
func benchmarkLegacySanitizeOpenAIResponsesInputItemIDs(body []byte) ([]byte, bool, error) {
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return body, false, nil
	}

	type inputItem struct {
		body        []byte
		stripID     bool
		stripCallID bool
	}
	items := make([]inputItem, 0)
	input.ForEach(func(_, item gjson.Result) bool {
		parsed := inputItem{body: []byte(item.Raw)}
		if item.IsObject() {
			itemType := strings.TrimSpace(item.Get("type").String())
			id := item.Get("id")
			parsed.stripCallID = item.Get("call_id").Exists() && shouldStripOpenAIResponsesNonPairCallID(itemType)
			if id.Type == gjson.String {
				parsed.stripID = shouldStripOpenAIResponsesInputItemID(itemType, id.String())
			}
		}
		items = append(items, parsed)
		return true
	})
	hasSanitization := false
	for _, item := range items {
		if item.stripID || item.stripCallID {
			hasSanitization = true
			break
		}
	}
	if !hasSanitization {
		return body, false, nil
	}

	rebuiltItems := make([][]byte, 0, len(items))
	for index, item := range items {
		itemBody := item.body
		if item.stripID {
			var err error
			itemBody, err = sjson.DeleteBytes(itemBody, "id")
			if err != nil {
				return nil, false, fmt.Errorf("delete input.%d.id: %w", index, err)
			}
		}
		if item.stripCallID {
			var err error
			itemBody, err = sjson.DeleteBytes(itemBody, "call_id")
			if err != nil {
				return nil, false, fmt.Errorf("delete input.%d.call_id: %w", index, err)
			}
		}
		rebuiltItems = append(rebuiltItems, itemBody)
	}

	rebuiltInput := make([]byte, 0, len(input.Raw))
	rebuiltInput = append(rebuiltInput, '[')
	for index, item := range rebuiltItems {
		if index > 0 {
			rebuiltInput = append(rebuiltInput, ',')
		}
		rebuiltInput = append(rebuiltInput, item...)
	}
	rebuiltInput = append(rebuiltInput, ']')
	sanitized, err := sjson.SetRawBytes(body, "input", rebuiltInput)
	if err != nil {
		return nil, false, fmt.Errorf("replace sanitized input: %w", err)
	}
	return sanitized, true, nil
}

func benchmarkLegacyStripOpenAIResponsesInputNamespaces(body []byte) ([]byte, bool, error) {
	if !bytes.Contains(body, []byte(`"namespace"`)) {
		return body, false, nil
	}
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return body, false, nil
	}

	var rebuilt bytes.Buffer
	rebuilt.Grow(len(input.Raw))
	_ = rebuilt.WriteByte('[')
	changed := false
	first := true
	var stripErr error
	input.ForEach(func(_, item gjson.Result) bool {
		if !first {
			_ = rebuilt.WriteByte(',')
		}
		first = false
		itemBody := []byte(item.Raw)
		if item.IsObject() && item.Get("namespace").Exists() {
			itemBody, stripErr = sjson.DeleteBytes(itemBody, "namespace")
			if stripErr != nil {
				return false
			}
			changed = true
		}
		_, _ = rebuilt.Write(itemBody)
		return true
	})
	_ = rebuilt.WriteByte(']')
	if stripErr != nil {
		return body, false, fmt.Errorf("delete OpenAI input namespace: %w", stripErr)
	}
	if !changed {
		return body, false, nil
	}
	stripped, err := sjson.SetRawBytes(body, "input", rebuilt.Bytes())
	if err != nil {
		return body, false, fmt.Errorf("replace OpenAI input after namespace deletion: %w", err)
	}
	return stripped, true, nil
}

func benchmarkLegacyNormalizeOpenAIAPIKeyStoreFalseReasoningReplay(body []byte) ([]byte, bool, error) {
	if gjson.GetBytes(body, "store").Type != gjson.False {
		return body, false, nil
	}
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return body, false, nil
	}

	var reqBody map[string]any
	if err := decodeOpenAIJSONUseNumber(body, &reqBody); err != nil {
		return body, false, fmt.Errorf("normalize API-key store=false reasoning replay: %w", err)
	}
	items, ok := reqBody["input"].([]any)
	if !ok {
		return body, false, nil
	}
	filtered := make([]any, 0, len(items))
	changed := false
	for _, rawItem := range items {
		item, ok := rawItem.(map[string]any)
		if !ok {
			filtered = append(filtered, rawItem)
			continue
		}
		typ := strings.TrimSpace(firstNonEmptyString(item["type"]))
		id := strings.TrimSpace(firstNonEmptyString(item["id"]))
		switch typ {
		case "reasoning":
			encryptedContent, hasEncryptedContent := item["encrypted_content"].(string)
			if !hasEncryptedContent || strings.TrimSpace(encryptedContent) == "" {
				changed = true
				continue
			}
			if strings.HasPrefix(id, "rs_") {
				delete(item, "id")
				changed = true
			}
			if summary, ok := item["summary"]; !ok || summary == nil {
				item["summary"] = []any{}
				changed = true
			}
		case "item_reference":
			if strings.HasPrefix(id, "rs_") {
				changed = true
				continue
			}
		}
		if shouldStripOpenAIResponsesNonPairCallID(typ) {
			if _, hasCallID := item["call_id"]; hasCallID {
				delete(item, "call_id")
				changed = true
			}
		}
		filtered = append(filtered, item)
	}
	if !changed {
		return body, false, nil
	}
	reqBody["input"] = filtered
	normalized, err := marshalOpenAIUpstreamJSON(reqBody)
	if err != nil {
		return body, false, fmt.Errorf("serialize API-key store=false reasoning replay: %w", err)
	}
	return normalized, true, nil
}

func benchmarkLegacyNormalizeCompactionTriggerInputOrder(body []byte) ([]byte, bool, error) {
	var payload map[string]any
	if err := decodeOpenAIJSONUseNumber(body, &payload); err != nil {
		return body, false, err
	}
	input, ok := payload["input"].([]any)
	if !ok || len(input) == 0 {
		return body, false, nil
	}
	triggerCount := 0
	normalized := make([]any, 0, len(input))
	for _, raw := range input {
		item, itemOK := raw.(map[string]any)
		if itemOK && item["type"] == "compaction_trigger" {
			triggerCount++
			continue
		}
		normalized = append(normalized, raw)
	}
	if triggerCount == 0 {
		return body, false, nil
	}
	if triggerCount == 1 {
		if last, ok := input[len(input)-1].(map[string]any); ok && last["type"] == "compaction_trigger" {
			return body, false, nil
		}
	}
	normalized = append(normalized, map[string]any{"type": "compaction_trigger"})
	payload["input"] = normalized
	encoded, err := marshalOpenAIUpstreamJSON(payload)
	if err != nil {
		return body, false, err
	}
	return encoded, true, nil
}

func BenchmarkLegacySanitizeOpenAIResponsesInputItemIDsLargeHistory(b *testing.B) {
	body := benchmarkOpenAIResponsesBody(512, 2048, func(index int, payload string) string {
		return fmt.Sprintf(`{"type":"message","id":"item_%d","role":"user","content":[{"type":"input_text","text":"%s"}]}`, index, payload)
	})
	benchmarkOpenAIRawRewrite(b, body, benchmarkLegacySanitizeOpenAIResponsesInputItemIDs)
}

func BenchmarkOptimizedSanitizeOpenAIResponsesInputItemIDsLargeHistory(b *testing.B) {
	body := benchmarkOpenAIResponsesBody(512, 2048, func(index int, payload string) string {
		return fmt.Sprintf(`{"type":"message","id":"item_%d","role":"user","content":[{"type":"input_text","text":"%s"}]}`, index, payload)
	})
	benchmarkOpenAIRawRewrite(b, body, sanitizeOpenAIResponsesInputItemIDs)
}

func BenchmarkLegacyStripOpenAIResponsesInputNamespacesLargeHistory(b *testing.B) {
	body := benchmarkOpenAIResponsesBody(512, 2048, func(index int, payload string) string {
		return fmt.Sprintf(`{"type":"message","namespace":"ns_%d","role":"user","content":[{"type":"input_text","text":"%s"}]}`, index, payload)
	})
	benchmarkOpenAIRawRewrite(b, body, benchmarkLegacyStripOpenAIResponsesInputNamespaces)
}

func BenchmarkOptimizedStripOpenAIResponsesInputNamespacesLargeHistory(b *testing.B) {
	body := benchmarkOpenAIResponsesBody(512, 2048, func(index int, payload string) string {
		return fmt.Sprintf(`{"type":"message","namespace":"ns_%d","role":"user","content":[{"type":"input_text","text":"%s"}]}`, index, payload)
	})
	benchmarkOpenAIRawRewrite(b, body, func(body []byte) ([]byte, bool, error) {
		result, err := stripOpenAIResponsesInputNamespaces(body, false)
		return result, err == nil && len(result) != len(body), err
	})
}

func BenchmarkLegacyNormalizeOpenAIAPIKeyStoreFalseReasoningReplayLargeHistory(b *testing.B) {
	body := benchmarkOpenAIResponsesBody(512, 2048, func(index int, payload string) string {
		return fmt.Sprintf(`{"type":"reasoning","id":"rs_%d","encrypted_content":"%s"}`, index, payload)
	})
	benchmarkOpenAIRawRewrite(b, body, benchmarkLegacyNormalizeOpenAIAPIKeyStoreFalseReasoningReplay)
}

func BenchmarkOptimizedNormalizeOpenAIAPIKeyStoreFalseReasoningReplayLargeHistory(b *testing.B) {
	body := benchmarkOpenAIResponsesBody(512, 2048, func(index int, payload string) string {
		return fmt.Sprintf(`{"type":"reasoning","id":"rs_%d","encrypted_content":"%s"}`, index, payload)
	})
	benchmarkOpenAIRawRewrite(b, body, func(body []byte) ([]byte, bool, error) {
		return normalizeOpenAIAPIKeyStoreFalseReasoningReplay(body, false)
	})
}

func BenchmarkLegacyNormalizeCompactionTriggerInputOrderLargeHistory(b *testing.B) {
	body := benchmarkOpenAIResponsesBody(512, 2048, func(index int, payload string) string {
		if index == 128 {
			return `{"type":"compaction_trigger"}`
		}
		return fmt.Sprintf(`{"type":"message","role":"user","content":[{"type":"input_text","text":"%s"}]}`, payload)
	})
	benchmarkOpenAIRawRewrite(b, body, benchmarkLegacyNormalizeCompactionTriggerInputOrder)
}

func BenchmarkOptimizedNormalizeCompactionTriggerInputOrderLargeHistory(b *testing.B) {
	body := benchmarkOpenAIResponsesBody(512, 2048, func(index int, payload string) string {
		if index == 128 {
			return `{"type":"compaction_trigger"}`
		}
		return fmt.Sprintf(`{"type":"message","role":"user","content":[{"type":"input_text","text":"%s"}]}`, payload)
	})
	benchmarkOpenAIRawRewrite(b, body, func(body []byte) ([]byte, bool, error) {
		return NormalizeCompactionTriggerInputOrder(body)
	})
}
