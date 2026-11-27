package service

import (
	"bytes"
	"fmt"
	"math/rand"
	"runtime"
	"strings"
	"testing"
	"unsafe"
	"weak"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func legacyHasCompactionTriggerInInput(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	input := parseRawJSONView(body).Get("input")
	if !input.IsArray() {
		return false
	}
	found := false
	input.ForEach(func(_, item gjson.Result) bool {
		if item.Get("type").String() == "compaction_trigger" {
			found = true
			return false
		}
		return true
	})
	return found
}

func legacyHasOpenAIResponsesNamespaceToolDeclaration(body []byte) bool {
	hasNamespaceTool := func(tools gjson.Result) bool {
		if !tools.IsArray() {
			return false
		}
		found := false
		tools.ForEach(func(_, tool gjson.Result) bool {
			if strings.EqualFold(strings.TrimSpace(tool.Get("type").String()), "namespace") {
				found = true
				return false
			}
			return true
		})
		return found
	}
	root := parseRawJSONView(body)
	if hasNamespaceTool(root.Get("tools")) {
		return true
	}
	input := root.Get("input")
	if !input.IsArray() {
		return false
	}
	found := false
	input.ForEach(func(_, item gjson.Result) bool {
		if !strings.EqualFold(strings.TrimSpace(item.Get("type").String()), "additional_tools") {
			return true
		}
		if hasNamespaceTool(item.Get("tools")) {
			found = true
			return false
		}
		return true
	})
	return found
}

func legacyValidateFunctionCallOutputContextBytes(body []byte) FunctionCallOutputValidation {
	result := FunctionCallOutputValidation{}
	if len(body) == 0 {
		return result
	}
	input := parseRawJSONView(body).Get("input")
	if !input.IsArray() {
		return result
	}
	var callIDs map[string]struct{}
	var referenceIDs map[string]struct{}
	input.ForEach(func(_, item gjson.Result) bool {
		if !item.IsObject() {
			return true
		}
		itemType := item.Get("type").String()
		switch {
		case isCodexToolCallOutputItemType(itemType):
			result.HasFunctionCallOutput = true
			callID := strings.TrimSpace(item.Get("call_id").String())
			if callID == "" {
				result.HasFunctionCallOutputMissingCallID = true
				return true
			}
			if callIDs == nil {
				callIDs = make(map[string]struct{})
			}
			callIDs[callID] = struct{}{}
		case isCodexToolCallContextItemType(itemType):
			if strings.TrimSpace(item.Get("call_id").String()) != "" {
				result.HasToolCallContext = true
			}
		case itemType == "item_reference":
			idValue := strings.TrimSpace(item.Get("id").String())
			if idValue == "" {
				return true
			}
			if referenceIDs == nil {
				referenceIDs = make(map[string]struct{})
			}
			referenceIDs[idValue] = struct{}{}
		}
		return !result.HasFunctionCallOutput || !result.HasToolCallContext
	})
	if !result.HasFunctionCallOutput || result.HasToolCallContext || len(callIDs) == 0 || len(referenceIDs) == 0 {
		return result
	}
	allReferenced := true
	for callID := range callIDs {
		if _, ok := referenceIDs[callID]; !ok {
			allReferenced = false
			break
		}
	}
	result.HasItemReferenceForAllCallIDs = allReferenced
	return result
}

func legacyNormalizeOpenAIResponsesReasoningContentReplay(body []byte) ([]byte, bool, error) {
	input := parseRawJSONView(body).Get("input")
	if !input.IsArray() {
		return body, false, nil
	}
	needsNormalization := false
	input.ForEach(func(_, item gjson.Result) bool {
		if strings.TrimSpace(item.Get("type").String()) != "reasoning" {
			return true
		}
		content := item.Get("content")
		if content.IsArray() && len(content.Array()) > 0 {
			needsNormalization = true
			return false
		}
		return true
	})
	if !needsNormalization {
		return body, false, nil
	}
	var reqBody map[string]any
	if err := decodeOpenAIJSONUseNumber(body, &reqBody); err != nil {
		return body, false, fmt.Errorf("normalize OpenAI reasoning content replay: %w", err)
	}
	items, ok := reqBody["input"].([]any)
	if !ok {
		return body, false, nil
	}
	changed := false
	for _, rawItem := range items {
		item, ok := rawItem.(map[string]any)
		if !ok || strings.TrimSpace(firstNonEmptyString(item["type"])) != "reasoning" {
			continue
		}
		content, ok := item["content"].([]any)
		if !ok || len(content) == 0 {
			continue
		}
		delete(item, "content")
		changed = true
	}
	if !changed {
		return body, false, nil
	}
	normalized, err := marshalOpenAIUpstreamJSON(reqBody)
	if err != nil {
		return body, false, fmt.Errorf("serialize normalized OpenAI reasoning content replay: %w", err)
	}
	return normalized, true, nil
}

func TestHasCompactionTriggerInInputDifferential(t *testing.T) {
	withBodyTopMinBytes(t, 0)
	cases := []string{
		``,
		`{}`,
		`{"input":"compaction_trigger"}`,
		`{"input":{"type":"compaction_trigger"}}`,
		`{"input":[{"type":"message"},{"type":"compaction_trigger"}]}`,
		`{"input":[{"type":"compaction_\u0074rigger"}]}`,
		`{"in` + `\u00` + `70ut":[{"type":"compaction_trigger"}]}`,
		`{"input":[{"type":"message","type":"compaction_trigger"}]}`,
		`{"input":[{"type":"compaction_trigger","type":"message"}]}`,
		`{"input":[{"type":"message"}],"input":[{"type":"compaction_trigger"}]}`,
		`{"input":[{"type":"compaction_trigger"}]`,
		`{"input":[{"type":"compaction_trigger",}]}`,
	}
	for _, raw := range cases {
		body := []byte(raw)
		want := legacyHasCompactionTriggerInInput(body)
		require.Equalf(t, want, HasCompactionTriggerInInput(body), "nil ctx mismatch on %q", raw)
		c := newTopTestContext()
		require.Equalf(t, want, HasCompactionTriggerInInputCtx(c, body), "indexed ctx mismatch on %q", raw)
	}
}

func TestHasOpenAIResponsesNamespaceToolDeclarationDifferential(t *testing.T) {
	withBodyTopMinBytes(t, 0)
	cases := []string{
		``,
		`{}`,
		`{"tools":[{"type":"namespace","name":"mcp"}]}`,
		`{"tools":[{"type":" Namespace ","name":"mcp"}]}`,
		`{"tools":[{"ty\u0070e":"namespace"}]}`,
		`{"input":[{"type":"additional_tools","tools":[{"type":"namespace"}]}]}`,
		`{"input":[{"type":" Additional_Tools ","tools":[{"type":" Namespace "}]}]}`,
		`{"input":[{"type":"additional_\u0074ools","tools":[{"type":"n\u0061mespace"}]}]}`,
		`{"input":[{"type":"additional_tools","tools":[{"type":"function"}]}]}`,
		`{"input":[{"type":"message","tools":[{"type":"namespace"}]}]}`,
		`{"tools":"bad","input":"additional_tools"}`,
		`{"input":[{"type":"additional_tools","tools":[]}]}`,
	}
	for _, raw := range cases {
		body := []byte(raw)
		want := legacyHasOpenAIResponsesNamespaceToolDeclaration(body)
		require.Equalf(t, want, hasOpenAIResponsesNamespaceToolDeclaration(nil, body), "nil ctx mismatch on %q", raw)
		c := newTopTestContext()
		require.Equalf(t, want, hasOpenAIResponsesNamespaceToolDeclaration(c, body), "indexed ctx mismatch on %q", raw)
	}
}

func TestValidateFunctionCallOutputContextBytesDifferential(t *testing.T) {
	withBodyTopMinBytes(t, 0)
	cases := []string{
		``,
		`{}`,
		`{"input":[]}`,
		`{"input":[{"type":"function_call_output"}]}`,
		`{"input":[{"type":"function_call_output","call_id":null}]}`,
		`{"input":[{"type":"function_call_output","call_id":"c1","call_id":"c2"},{"type":"item_reference","id":"c1"}]}`,
		`{"input":[{"type":"function_call_output","call_id":"c1"},{"type":"function_call","call_id":"c1"},{"type":"function_call_output"}]}`,
		`{"input":[{"type":"function_call_output","call_id":"c1"},{"type":"function_call","call_id":""}]}`,
		`{"input":[{"type":"tool_search_output","call_id":"s1"},{"type":"item_reference","id":"s1"}]}`,
		`{"input":[{"type":"function_\u0063all_output","call_id":"c1"},{"type":"item_reference","id":"c1"}]}`,
		`{"input":[{"type":"custom_tool_call_output","call_id":"c1"},{"type":"mcp_tool_call_output","call_id":"c2"},{"type":"item_reference","id":"c1"}]}`,
		`{"input":[{"type":"message","content":"function_call_output"}]}`,
	}
	for _, raw := range cases {
		body := []byte(raw)
		want := legacyValidateFunctionCallOutputContextBytes(body)
		require.Equalf(t, want, ValidateFunctionCallOutputContextBytes(body), "nil ctx mismatch on %q", raw)
		c := newTopTestContext()
		require.Equalf(t, want, ValidateFunctionCallOutputContextBytesCtx(c, body), "indexed ctx mismatch on %q", raw)

		wantExact := gjson.GetBytes(body, `input.#(type=="function_call_output")`).Exists()
		gotExact, gotVal := ValidateFunctionCallOutputRequestCtx(c, body)
		require.Equalf(t, wantExact, gotExact, "exact flag mismatch on %q", raw)
		if wantExact {
			require.Equalf(t, want, gotVal, "request ctx validation mismatch on %q", raw)
		}
	}
}

func TestStripOpenAIResponsesInputNamespacesDifferential(t *testing.T) {
	withBodyTopMinBytes(t, 0)
	r := rand.New(rand.NewSource(20261127))
	types := []string{`"message"`, `"function_call"`, `" Function_Call "`, `"custom_tool_call"`, `"tool_call"`, `"mcp_tool_call"`, `"reasoning"`, `"function_call_output"`}
	for i := 0; i < 2000; i++ {
		loose := i%2 == 1
		ws := ""
		if loose {
			ws = " "
		}
		n := r.Intn(6) + 1
		items := make([]string, n)
		for j := range items {
			parts := []string{`"type":` + ws + types[r.Intn(len(types))]}
			if r.Intn(3) != 0 {
				parts = append(parts, `"namespace":`+ws+`"ns_val"`)
			}
			if r.Intn(5) == 0 {
				parts = append(parts, `"namespace":`+ws+`"ns_dup"`)
			}
			if r.Intn(8) == 0 {
				parts = append(parts, `"\u006eamespace":`+ws+`"ns_esc"`)
			}
			parts = append(parts, `"content":`+ws+`[{"type":"input_text","namespace":"keep"}]`)
			r.Shuffle(len(parts), func(a, b int) { parts[a], parts[b] = parts[b], parts[a] })
			items[j] = "{" + ws + strings.Join(parts, ","+ws) + ws + "}"
		}
		body := []byte(`{"input":[` + strings.Join(items, ","+ws) + `]}`)
		for _, keep := range []bool{false, true} {
			gotNil, errNil := stripOpenAIResponsesInputNamespaces(body, keep)
			require.NoError(t, errNil)
			c := newTopTestContext()
			gotCtx, errCtx := stripOpenAIResponsesInputNamespacesCtx(c, body, keep)
			require.NoError(t, errCtx)
			require.Equal(t, string(gotNil), string(gotCtx), "ctx vs nil mismatch keep=%v body=%s", keep, body)
			if !keep {
				want, wantChanged, wantErr := benchmarkLegacyStripOpenAIResponsesInputNamespaces(body)
				require.NoError(t, wantErr)
				if !wantChanged {
					require.Same(t, unsafe.SliceData(body), unsafe.SliceData(gotNil))
				} else {
					require.Equal(t, stripJSONInsignificantWhitespace(string(want)), stripJSONInsignificantWhitespace(string(gotNil)))
				}
			}
		}
	}
}

func TestNormalizeOpenAIResponsesReasoningContentReplayDifferential(t *testing.T) {
	withBodyTopMinBytes(t, 0)
	cases := []string{
		``,
		`{}`,
		`{"input":[]}`,
		`{"input":[{"type":"reasoning","content":[]}]}`,
		`{"input":[{"type":"reasoning","content":null}]}`,
		`{"input":[{"type":"reasoning","content":"text"}]}`,
		`{"input":[{"type":"reasoning","content":[{"type":"reasoning_text","text":"x"}]}]}`,
		`{"input":[{"type":"reasoning","content":[{"type":"reasoning_text","text":"x"}]},{"type":"message","content":[{"type":"input_text","text":"y"}]}]}`,
		`{"input":[{"type":"\u0072easoning","content":[{"type":"reasoning_text","text":"x"}]}]}`,
		`{"input":[{"type":"message","content":[{"type":"reasoning","text":"x"}]}]}`,
	}
	for _, raw := range cases {
		body := []byte(raw)
		wantBody, wantChanged, wantErr := legacyNormalizeOpenAIResponsesReasoningContentReplay(body)
		gotNil, gotChangedNil, gotErrNil := normalizeOpenAIResponsesReasoningContentReplay(body)
		c := newTopTestContext()
		gotCtx, gotChangedCtx, gotErrCtx := normalizeOpenAIResponsesReasoningContentReplayCtx(c, body)

		require.Equal(t, wantErr != nil, gotErrNil != nil)
		require.Equal(t, wantErr != nil, gotErrCtx != nil)
		require.Equal(t, wantChanged, gotChangedNil)
		require.Equal(t, wantChanged, gotChangedCtx)
		if !wantChanged {
			require.Same(t, unsafe.SliceData(body), unsafe.SliceData(gotNil))
			require.Same(t, unsafe.SliceData(body), unsafe.SliceData(gotCtx))
		} else {
			require.Equal(t, string(wantBody), string(gotNil))
			require.Equal(t, string(wantBody), string(gotCtx))
		}
	}
}

func TestResponsesInputIndexIsolationAndReleaseGC(t *testing.T) {
	c := newTopTestContext()

	// Populating responsesInputIndex + skip function tools on a 1 MiB body must
	// let the body be collected after ReleaseRequestBodyReferences(c).
	w := func() weak.Pointer[byte] {
		payload := strings.Repeat("x", 1<<20)
		body := []byte(`{"model":"gpt-5.5","tools":[{"type":"function","name":"calc"}],"input":[{"type":"message","id":"bad","namespace":"ns1","role":"user","content":[{"type":"input_text","text":"` + payload + `"}]},{"type":"function_call","call_id":"c1"},{"type":"function_call_output","call_id":"c1","output":"ok"}]}`)
		rememberOpenAINativeSearchSkipFunctionTools(c, body)
		require.False(t, HasCompactionTriggerInInputCtx(c, body))
		require.False(t, hasOpenAIResponsesNamespaceToolDeclaration(c, body))
		_, val := ValidateFunctionCallOutputRequestCtx(c, body)
		require.True(t, val.HasFunctionCallOutput)
		require.True(t, val.HasToolCallContext)

		stripped, err := stripOpenAIResponsesInputNamespacesCtx(c, body, false)
		require.NoError(t, err)
		require.NotSame(t, unsafe.SliceData(body), unsafe.SliceData(stripped))

		// Original body index must remain isolated from rewritten slice index.
		sanOrig, chOrig, err := sanitizeOpenAIResponsesInputItemIDsCtx(c, body)
		require.NoError(t, err)
		require.True(t, chOrig)
		require.False(t, gjson.GetBytes(sanOrig, "input.0.id").Exists())
		require.True(t, gjson.GetBytes(sanOrig, "input.0.namespace").Exists())

		sanStripped, chStripped, err := sanitizeOpenAIResponsesInputItemIDsCtx(c, stripped)
		require.NoError(t, err)
		require.True(t, chStripped)
		require.False(t, gjson.GetBytes(sanStripped, "input.0.id").Exists())
		require.False(t, gjson.GetBytes(sanStripped, "input.0.namespace").Exists())

		return weak.Make(&body[0])
	}()

	if w.Value() == nil {
		t.Fatal("expected gin context memo to retain body before ReleaseRequestBodyReferences")
	}
	ReleaseRequestBodyReferences(c)
	requireBodyCollected(t, w, "gin context still pins body after ReleaseRequestBodyReferences")
	runtime.KeepAlive(c)
}

func TestRepairDeepSeekMissingToolOutputsCtxDifferential(t *testing.T) {
	withBodyTopMinBytes(t, 0)
	cases := []string{
		``,
		`{"input":[{"type":"function_call","call_id":"c1"}]}`,
		`{"input":[{"type":"function_\u0063all","call_id":"c1"}]}`,
		`{"previous_response_id":"resp_1","input":[{"type":"function_call","call_id":"c1"}]}`,
		`{"input":[{"type":"function_call","call_id":"c1"},{"type":"function_call_output","call_id":"c1"}]}`,
		`{"input":[{"type":"function_call","call_id":"c1"}]`,
	}
	for _, raw := range cases {
		body := []byte(raw)
		want, wantChanged, wantErr := legacyRepairDeepSeekMissingToolOutputs(body)
		gotNil, gotChangedNil, gotErrNil := repairDeepSeekMissingToolOutputs(body)
		c := newTopTestContext()
		gotCtx, gotChangedCtx, gotErrCtx := repairDeepSeekMissingToolOutputsCtx(c, body)
		require.NoError(t, wantErr)
		require.NoError(t, gotErrNil)
		require.NoError(t, gotErrCtx)
		require.Equal(t, wantChanged, gotChangedNil)
		require.Equal(t, wantChanged, gotChangedCtx)
		if !wantChanged {
			require.Same(t, unsafe.SliceData(body), unsafe.SliceData(gotNil))
			require.Same(t, unsafe.SliceData(body), unsafe.SliceData(gotCtx))
		} else {
			require.Equal(t, stripJSONInsignificantWhitespace(string(want)), stripJSONInsignificantWhitespace(string(gotNil)))
			require.Equal(t, stripJSONInsignificantWhitespace(string(want)), stripJSONInsignificantWhitespace(string(gotCtx)))
		}
	}
}

func TestSanitizeOpenAIResponsesInputItemIDsCtxMatchesNil(t *testing.T) {
	withBodyTopMinBytes(t, 0)
	body := []byte(`{"input":[{"type":"message","id":"bad","call_id":"c1","content":"x"},{"type":"function_call","id":"fc_1","call_id":"c2"}]}`)
	c := newTopTestContext()
	gotNil, chNil, errNil := sanitizeOpenAIResponsesInputItemIDs(body)
	gotCtx, chCtx, errCtx := sanitizeOpenAIResponsesInputItemIDsCtx(c, body)
	require.NoError(t, errNil)
	require.NoError(t, errCtx)
	require.Equal(t, chNil, chCtx)
	require.Equal(t, string(gotNil), string(gotCtx))

	// Re-running on already-clean sanitized body must hit O(1) !idx.hasIDSanitization path.
	clean, chClean, errClean := sanitizeOpenAIResponsesInputItemIDsCtx(c, gotCtx)
	require.NoError(t, errClean)
	require.False(t, chClean)
	require.Same(t, unsafe.SliceData(gotCtx), unsafe.SliceData(clean))
}

func p1aBenchResponsesBody(sizeBytes int) []byte {
	return syntheticResponsesBody(sizeBytes, false)
}

func BenchmarkP1aHasCompactionTriggerInInput(b *testing.B) {
	body := p1aBenchResponsesBody(300 << 10)
	b.Run("legacy/300KB", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(body)))
		for i := 0; i < b.N; i++ {
			if legacyHasCompactionTriggerInInput(body) {
				b.Fatal("unexpected")
			}
		}
	})
	b.Run("optimized_nil/300KB", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(body)))
		for i := 0; i < b.N; i++ {
			if HasCompactionTriggerInInput(body) {
				b.Fatal("unexpected")
			}
		}
	})
}

func BenchmarkP1aValidateFunctionCallOutputContextBytes(b *testing.B) {
	body := p1aBenchResponsesBody(300 << 10)
	b.Run("legacy/300KB", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(body)))
		for i := 0; i < b.N; i++ {
			_ = legacyValidateFunctionCallOutputContextBytes(body)
		}
	})
	b.Run("optimized_nil/300KB", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(body)))
		for i := 0; i < b.N; i++ {
			_ = ValidateFunctionCallOutputContextBytes(body)
		}
	})
	c := newTopTestContext()
	_ = RequestBodyTop(c, body).responsesInput()
	b.Run("optimized_ctx/300KB", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(body)))
		for i := 0; i < b.N; i++ {
			_ = ValidateFunctionCallOutputContextBytesCtx(c, body)
		}
	})
}

func BenchmarkP1aHasOpenAIResponsesNamespaceToolDeclaration(b *testing.B) {
	body := p1aBenchResponsesBody(300 << 10)
	b.Run("legacy/300KB", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(body)))
		for i := 0; i < b.N; i++ {
			if legacyHasOpenAIResponsesNamespaceToolDeclaration(body) {
				b.Fatal("unexpected")
			}
		}
	})
	c := newTopTestContext()
	b.Run("optimized_ctx/300KB", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(body)))
		for i := 0; i < b.N; i++ {
			if hasOpenAIResponsesNamespaceToolDeclaration(c, body) {
				b.Fatal("unexpected")
			}
		}
	})
}

func BenchmarkP1aNormalizeOpenAIResponsesReasoningContentReplay(b *testing.B) {
	body := p1aBenchResponsesBody(300 << 10)
	b.Run("legacy/300KB", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(body)))
		for i := 0; i < b.N; i++ {
			_, changed, err := legacyNormalizeOpenAIResponsesReasoningContentReplay(body)
			if err != nil || changed {
				b.Fatalf("unexpected changed=%v err=%v", changed, err)
			}
		}
	})
	b.Run("optimized_nil/300KB", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(body)))
		for i := 0; i < b.N; i++ {
			_, changed, err := normalizeOpenAIResponsesReasoningContentReplay(body)
			if err != nil || changed {
				b.Fatalf("unexpected changed=%v err=%v", changed, err)
			}
		}
	})
}

func BenchmarkP1aResponsesInputPipelineChain(b *testing.B) {
	body := p1aBenchResponsesBody(300 << 10)
	// Ensure no-op body doesn't mutate between runs, exercising full request chain.
	b.Run("legacy_chain/300KB", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(body)))
		for i := 0; i < b.N; i++ {
			_ = legacyHasCompactionTriggerInInput(body)
			_ = legacyValidateFunctionCallOutputContextBytes(body)
			_ = legacyHasOpenAIResponsesNamespaceToolDeclaration(body)
			_, _, _ = legacyRepairDeepSeekMissingToolOutputs(body)
			_, _, _ = legacyNormalizeOpenAIResponsesReasoningContentReplay(body)
			_, _, _ = legacySanitizeOpenAIResponsesInputItemIDs(body)
			if bytes.Contains(body, []byte("never")) {
				b.Fatal("impossible")
			}
		}
	})
	b.Run("optimized_ctx_chain/300KB", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(body)))
		for i := 0; i < b.N; i++ {
			c := newTopTestContext()
			_ = HasCompactionTriggerInInputCtx(c, body)
			_, _ = ValidateFunctionCallOutputRequestCtx(c, body)
			_ = hasOpenAIResponsesNamespaceToolDeclaration(c, body)
			_, _, _ = repairDeepSeekMissingToolOutputsCtx(c, body)
			_, _, _ = normalizeOpenAIResponsesReasoningContentReplayCtx(c, body)
			_, _, _ = sanitizeOpenAIResponsesInputItemIDsCtx(c, body)
			ReleaseRequestBodyReferences(c)
		}
	})
}
