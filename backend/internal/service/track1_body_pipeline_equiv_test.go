package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"testing"
	"unsafe"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func referenceDeriveOpenAICyberTranscriptBlockKeys(apiKeyID int64, body []byte) openAICyberTranscriptBlockKeys {
	if len(body) == 0 {
		return openAICyberTranscriptBlockKeys{}
	}
	root := openAIRequestPayloadView(body)
	if !root.Exists() || !root.IsObject() {
		return openAICyberTranscriptBlockKeys{}
	}
	h := sha256.New()
	_, _ = h.Write([]byte("cyber-transcript:v3|api_key="))
	_, _ = h.Write([]byte(strconv.FormatInt(apiKeyID, 10)))
	if v := root.Get("instructions"); v.Exists() && (v.Type != gjson.String || strings.TrimSpace(v.String()) != "") {
		canonical := legacyNormalizeCompatSeedJSON(json.RawMessage(v.Raw))
		if v.Type == gjson.String {
			canonical = v.String()
		}
		_, _ = h.Write([]byte("|instructions="))
		_, _ = h.Write([]byte(canonical))
	}
	appendSequence := func(sequence gjson.Result) openAICyberTranscriptBlockKeys {
		if !sequence.Exists() || !sequence.IsArray() {
			return openAICyberTranscriptBlockKeys{}
		}
		result := openAICyberTranscriptBlockKeys{
			lookupKeys: make([]string, 0, maxOpenAICyberTranscriptLookupKeys),
		}
		nextLookupKey := 0
		lookupKeysRotated := false
		lastLookupKey := ""
		hasModelGeneratedItem := false
		sequence.ForEach(func(_, item gjson.Result) bool {
			canonical := item.Raw
			switch item.Type {
			case gjson.String:
				encoded, _ := json.Marshal(item.String())
				canonical = string(encoded)
			case gjson.JSON:
				canonical = legacyNormalizeCompatSeedJSON(json.RawMessage(item.Raw))
			}
			if strings.TrimSpace(canonical) == "" {
				return true
			}
			if openAICyberTranscriptItemStartsUserTurn(item) && hasModelGeneratedItem && lastLookupKey != "" {
				result.preLatestUserKey = lastLookupKey
			}
			_, _ = h.Write([]byte("|item="))
			_, _ = h.Write([]byte(canonical))
			lastLookupKey = hex.EncodeToString(h.Sum(nil))
			if len(result.lookupKeys) < maxOpenAICyberTranscriptLookupKeys {
				result.lookupKeys = append(result.lookupKeys, lastLookupKey)
			} else {
				result.lookupKeys[nextLookupKey] = lastLookupKey
				nextLookupKey = (nextLookupKey + 1) % maxOpenAICyberTranscriptLookupKeys
				lookupKeysRotated = true
				result.lookupKeysTruncated = true
			}
			if openAICyberTranscriptItemIsModelGenerated(item) {
				hasModelGeneratedItem = true
			}
			return true
		})
		if lookupKeysRotated {
			ordered := make([]string, 0, len(result.lookupKeys))
			ordered = append(ordered, result.lookupKeys[nextLookupKey:]...)
			ordered = append(ordered, result.lookupKeys[:nextLookupKey]...)
			result.lookupKeys = ordered
		}
		return result
	}
	if messages := root.Get("messages"); messages.Exists() {
		return appendSequence(messages)
	}
	input := root.Get("input")
	if input.IsArray() {
		return appendSequence(input)
	}
	if input.Type == gjson.String && strings.TrimSpace(input.String()) != "" {
		encoded, _ := json.Marshal(input.String())
		_, _ = h.Write([]byte("|item="))
		_, _ = h.Write(encoded)
		return openAICyberTranscriptBlockKeys{lookupKeys: []string{hex.EncodeToString(h.Sum(nil))}}
	}
	return openAICyberTranscriptBlockKeys{}
}

func referenceGrokModerationTranscriptKeys(apiKey *APIKey, body []byte, write bool) []string {
	uid := balancePreauthorizationAPIKeyUserID(apiKey)
	if uid <= 0 || !hasOpenAIContentSessionUserAnchorGJSON([]byte(openAIRequestPayloadView(body).Raw)) {
		return nil
	}
	transcript := referenceDeriveOpenAICyberTranscriptBlockKeys(uid, body)
	keys := transcript.lookupKeys
	if write && len(keys) > 0 {
		keys = []string{keys[len(keys)-1]}
		if transcript.preLatestUserKey != "" {
			keys = append(keys, transcript.preLatestUserKey)
		}
	}
	for i, key := range keys {
		keys[i] = "grok-moderation-transcript:" + key
	}
	return keys
}

func TestTrack1GrokAndCyberTranscriptEquivalence(t *testing.T) {
	apiKey := &APIKey{ID: 55, UserID: 77}
	cases := []string{
		``,
		`{}`,
		`{"messages":null,"input":[{"role":"user","content":"should not fallback"}]}`,
		`{"messages":{"not":"array"},"input":[{"role":"user","content":"should not fallback"}]}`,
		`{"instructions":"   ","input":"hello"}`,
		`{"instructions":{"b":2,"a":1},"input":[{"role":"user","content":"hi"}]}`,
		`{"instructions":123,"messages":[{"role":"user","content":"a"},{"role":"assistant","content":"b"},{"role":"user","content":"c"}]}`,
		`{"messages":[{"role":"user","content":""},{"role":"assistant","content":"b"},{"role":"user","content":"real"}]}`,
		`{"messages":[{"role":"user","content":[{"type":"tool_result","text":""}]},{"role":"assistant","content":"b"}]}`,
		`{"messages":["raw-str",1.50,true,null,{"role":"user","content":"ok"}]}`,
		`{"input":[{"type":"input_text","text":"   "},{"role":"user","content":"later"}]}`,
		`{"input":[{"type":"input_text","text":"hello"},{"type":"output_text","text":"world"},{"type":"input_text","text":"follow"}]}`,
		`{"type":"response.create","response":{"instructions":"ws","input":[{"role":"user","content":"u1"},{"role":"assistant","content":"a1"},{"role":"user","content":[{"type":"function_call_output","output":"x"},{"type":"input_text","text":"u2"}]}]}}`,
		`{"\u006dessages":[{"role":"user","content":"escaped key"},{"role":"assistant","content":"ok"},{"role":"user","content":"next"}]}`,
		`{"messages":[{"\u0072ole":"user","content":"escaped item key"},{"role":"assistant","content":"ok"},{"role":"user","content":"next"}]}`,
		`{"messages":[{"role":"user","content":"truncated"}`,
	}

	// Add >256 message ring overflow case.
	var ringMsg strings.Builder
	_, _ = ringMsg.WriteString(`{"messages":[`)
	for i := 0; i < maxOpenAICyberTranscriptLookupKeys+15; i++ {
		if i > 0 {
			_ = ringMsg.WriteByte(',')
		}
		if i == 100 {
			fmt.Fprintf(&ringMsg, `{"role":"assistant","content":"model %d"}`, i)
		} else {
			fmt.Fprintf(&ringMsg, `{"role":"user","content":"turn %d"}`, i)
		}
	}
	_, _ = ringMsg.WriteString(`]}`)
	cases = append(cases, ringMsg.String())

	for i, doc := range cases {
		b := []byte(doc)
		wantT := referenceDeriveOpenAICyberTranscriptBlockKeys(77, b)
		gotT := deriveOpenAICyberTranscriptBlockKeys(77, b)
		require.Equalf(t, wantT, gotT, "case %d: %s", i, doc)

		for _, write := range []bool{false, true} {
			wantG := referenceGrokModerationTranscriptKeys(apiKey, b, write)
			gotG := grokModerationTranscriptKeys(apiKey, b, write)
			require.Equalf(t, wantG, gotG, "grok case %d write=%v: %s", i, write, doc)
		}

		require.Equalf(t, hasOpenAIContentSessionUserAnchorGJSON(b), hasOpenAIContentSessionUserAnchor(b), "anchor case %d", i)
	}
}

func TestTrack1NormalizeCompatSeedEdgeCasesAndRandom(t *testing.T) {
	edgeCases := []string{
		``,
		`null`,
		`true`,
		`false`,
		`0`,
		`-0`,
		`-0.0`,
		`1e-6`,
		`9.999999999999999e-7`,
		`1e-7`,
		`1e20`,
		`1e21`,
		`1e999`,
		`-1e999`,
		`999999999999999`,
		`1000000000000000`,
		`9007199254740993`,
		`"<>&\u2028\u2029\/"`,
		"\"raw\u2028and\u2029and<>&\"",
		`"\ud800"`,
		`"\ud83d\ude00"`,
		`"\ud800\ud800"`,
		"\"badutf8:\xff\xfe\"",
		`{"a":1,"\u0061":2,"b":3}`,
		`{"\u0062":1,"a":2,"b":3}`,
		"{\"\xff\":1,\"\\ud800\":2,\"a\":3}",
		`{"z":1,"m":[1.50,-0,1E-7,1E+21],"a":{"k":"v"}}`,
		strings.Repeat("[", 10000) + strings.Repeat("]", 10000),
		strings.Repeat("[", 10001) + strings.Repeat("]", 10001),
		`{"broken":`,
	}
	for i, doc := range edgeCases {
		raw := json.RawMessage(doc)
		want := legacyNormalizeCompatSeedJSON(raw)
		got := normalizeCompatSeedJSONUncached(raw)
		require.Equalf(t, want, got, "edge case %d: %q", i, doc)
	}

	r := rand.New(rand.NewSource(991))
	for i := 0; i < 2000; i++ {
		size := []int{16, 80, 128, 300, 1200}[r.Intn(5)]
		doc := bigRandomJSON(r, size)
		if r.Intn(5) == 0 {
			doc = mutateJSON(r, doc)
		}
		raw := json.RawMessage(doc)
		require.Equalf(t, legacyNormalizeCompatSeedJSON(raw), normalizeCompatSeedJSONUncached(raw), "rand %d", i)
	}
}

func TestTrack1GroupBEquivalenceAndZeroAlloc(t *testing.T) {
	// 1. deepSeekDuplicateCallIDRewrite ordering equivalence.
	accounts := []*Account{
		nil,
		{Platform: PlatformDeepseek, Type: AccountTypeAPIKey},
		{Platform: PlatformOpenAI, Type: AccountTypeOAuth},
		{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.deepseek.com/v1"}},
		{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"model_mapping": map[string]any{"alias": "deepseek-chat"}}},
	}
	bodies := [][]byte{
		[]byte(`{"model":"gpt-5","input":"hi"}`),
		[]byte(`{"model":"deepseek-chat","input":"hi"}`),
		[]byte(`{"model":"alias","input":"hi"}`),
		[]byte(`{"model":"gpt-5","input":[{"type":"function_call","call_id":"c1"}]}`),
		[]byte(`{"model":"gpt-5","messages":[{"role":"assistant","tool_calls":[]}]}`),
	}
	for _, acc := range accounts {
		for _, body := range bodies {
			want := false
			model := gjson.GetBytes(body, "model").String()
			if acc == nil {
				want = deepSeekCallIDScope("", model)
			} else {
				want = deepSeekCallIDScope(acc.Platform, model) ||
					deepSeekCallIDScope("", acc.GetMappedModel(model)) ||
					strings.Contains(strings.ToLower(acc.GetOpenAIBaseURL()+" "+acc.GetCNProtocolBaseURL(APIProtocolResponses)+" "+acc.GetCNProtocolBaseURL(APIProtocolChatCompletions)), "deepseek") ||
					bodyHasToolCallIDs(body)
			}
			require.Equal(t, want, deepSeekDuplicateCallIDRewrite(acc, body))
		}
	}

	// 2. normalizeOpenAIAPIKeyStoreFalseReasoningReplay >16 keys & 0-alloc no-op.
	var wideRoot strings.Builder
	_, _ = wideRoot.WriteString(`{"store":false`)
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&wideRoot, `,"k%02d":%d`, i, i)
	}
	_, _ = wideRoot.WriteString(`,"input":[{`)
	for i := 0; i < 20; i++ {
		if i > 0 {
			_ = wideRoot.WriteByte(',')
		}
		fmt.Fprintf(&wideRoot, `"f%02d":%d`, i, i)
	}
	_, _ = wideRoot.WriteString(`,"type":"message","role":"user","content":"hi"}]}`)
	wideBytes := []byte(wideRoot.String())
	gotWide, changedWide, errWide := normalizeOpenAIAPIKeyStoreFalseReasoningReplay(nil, wideBytes, false)
	require.NoError(t, errWide)
	require.False(t, changedWide)
	require.Equal(t, string(wideBytes), string(gotWide))

	noopBody := []byte(`{"store":false,"input":[{"type":"message","role":"user","content":"hello"}]}`)
	allocs := testing.AllocsPerRun(20, func() {
		out, ch, err := normalizeOpenAIAPIKeyStoreFalseReasoningReplay(nil, noopBody, true)
		if err != nil || ch || len(out) == 0 {
			t.Fatal("unexpected rewrite")
		}
	})
	require.Equal(t, float64(0), allocs, "no-op store=false replay should do 0 allocs when knownStoreFalse=true")

	// 3. openAIRequestBodyMayContainEmptyBase64InputImage deep nesting & BOM & escapes.
	deep := strings.Repeat(`{"content":[`, 40) + `{"type":"input_image","image_url":"data:image/png;base64, "}` + strings.Repeat(`]}`, 40)
	deepBody := []byte(`{"input":[` + deep + `]}`)
	require.True(t, openAIRequestBodyMayContainEmptyBase64InputImage(nil, deepBody))

	// 4. normalizeAndDropOrphanDeepSeekChatMessages == normalizeDeepSeekChatMessages(dropOrphanDeepSeekToolMessages(body))
	// and canonical tight messages do 0 allocs!
	dsCases := []string{
		`{}`,
		`{"messages":[]}`,
		`{"messages":[{"role":"system","content":"s"},{"role":"user","content":"u"},{"role":"assistant","reasoning_content":"r","tool_calls":[{"id":"c1","type":"function","function":{"name":"f","arguments":"{}"}}]},{"role":"tool","content":"ok","tool_call_id":"c1"}]}`,
		`{"messages":[{"role":"tool","content":"orphan","tool_call_id":"missing"},{"role":"tool","content":"bare"},{"role":"assistant","tool_calls":[{"id":"c1","type":"function","function":{"name":"f","arguments":"{}"}}]},{"role":"user","content":"between"},{"role":"tool","content":"ans","tool_call_id":"c1"}]}`,
		`{"messages":[{"role":"tool","content":"early","tool_call_id":"c1"},{"role":"assistant","tool_calls":[{"id":"c1","type":"function","function":{"name":"f","arguments":"{}"}}]}]}`,
		`{"messages":[{"role":"assistant","content":"text","tool_calls":[{"id":"c1","type":"function","function":{"name":"f","arguments":"{}"}}]},{"role":"tool","content":"dup1","tool_call_id":"c1"},{"role":"tool","content":"dup2","tool_call_id":"c1"}]}`,
		`{"messages":[{"role":"assistant","content":"  ","tool_calls":[{"id":"unans","type":"function","function":{"name":"f","arguments":"{}"}}]}]}`,
		`{"messages":[{"role":"user","content":"<tag>&\u2028"}]}`,
		`{"messages":[{"role":"user","content":"spaces" , "extra": 1}]}`,
	}
	for i, doc := range dsCases {
		b := []byte(doc)
		want := legacyNormalizeDeepSeekChatMessages(legacyDropOrphanDeepSeekToolMessages(b))
		got := normalizeAndDropOrphanDeepSeekChatMessages(b)
		require.Equalf(t, string(want), string(got), "deepseek fused case %d: %s", i, doc)
		require.Truef(t, bytes.Equal(want, got), "deepseek fused byte mismatch case %d", i)
	}

	canonMsg := []byte(`{"model":"deepseek-chat","messages":[{"role":"system","content":"sys"},{"role":"user","content":"hi"},{"role":"assistant","reasoning_content":"think","tool_calls":[{"id":"c1","type":"function","function":{"name":"f","arguments":"{}"}}]},{"role":"tool","content":"done","tool_call_id":"c1"}]}`)
	dsAllocs := testing.AllocsPerRun(20, func() {
		out := normalizeAndDropOrphanDeepSeekChatMessages(canonMsg)
		if len(out) != len(canonMsg) {
			t.Fatal("unexpected rewrite")
		}
	})
	require.Equal(t, float64(0), dsAllocs, "canonical tight DeepSeek chat messages should do 0 allocs")
}

func TestTrack1StoreFalseReasoningReplayOmittedSummaryParity(t *testing.T) {
	// Legacy gjson-based reference (pre-Track-1) matching item.Get("summary").Type == gjson.Null
	// where gjson.Null == 0 covers both omitted "summary" and explicit "summary": null.
	legacyNormalize := func(body []byte, knownStoreFalse bool) ([]byte, bool, error) {
		if !knownStoreFalse && gjson.GetBytes(body, "store").Type != gjson.False {
			return body, false, nil
		}
		input := gjson.GetBytes(body, "input")
		if !input.IsArray() {
			return body, false, nil
		}
		return normalizeOpenAIAPIKeyStoreFalseReasoningReplayDecoded(body, knownStoreFalse)
	}

	cases := []struct {
		name            string
		body            string
		knownStoreFalse bool
		wantChanged     bool
		checkSummaryIdx int
	}{
		{
			name:            "omitted_summary_no_id_store_false",
			body:            `{"store":false,"input":[{"type":"reasoning","encrypted_content":"enc"}]}`,
			knownStoreFalse: false,
			wantChanged:     true,
			checkSummaryIdx: 0,
		},
		{
			name:            "omitted_summary_no_id_known_store_false",
			body:            `{"input":[{"type":"reasoning","encrypted_content":"enc"}]}`,
			knownStoreFalse: true,
			wantChanged:     true,
			checkSummaryIdx: 0,
		},
		{
			name:            "omitted_summary_with_rs_id_store_false",
			body:            `{"store":false,"input":[{"type":"reasoning","id":"rs_1","encrypted_content":"enc"}]}`,
			knownStoreFalse: false,
			wantChanged:     true,
			checkSummaryIdx: 0,
		},
		{
			name:            "omitted_summary_with_rs_id_known_store_false",
			body:            `{"input":[{"type":"reasoning","id":"rs_1","encrypted_content":"enc"}]}`,
			knownStoreFalse: true,
			wantChanged:     true,
			checkSummaryIdx: 0,
		},
		{
			name:            "omitted_summary_with_non_rs_id",
			body:            `{"store":false,"input":[{"type":"reasoning","id":"keep_1","encrypted_content":"enc"}]}`,
			knownStoreFalse: false,
			wantChanged:     true,
			checkSummaryIdx: 0,
		},
		{
			name:            "omitted_summary_after_unchanged_item",
			body:            `{"store":false,"input":[{"type":"message","role":"user","content":"hi"},{"type":"reasoning","encrypted_content":"enc"}]}`,
			knownStoreFalse: false,
			wantChanged:     true,
			checkSummaryIdx: 1,
		},
		{
			name:            "null_summary_no_id",
			body:            `{"store":false,"input":[{"type":"reasoning","encrypted_content":"enc","summary":null}]}`,
			knownStoreFalse: false,
			wantChanged:     true,
			checkSummaryIdx: 0,
		},
		{
			name:            "empty_summary_noop",
			body:            `{"store":false,"input":[{"type":"reasoning","id":"keep_1","encrypted_content":"enc","summary":[]}]}`,
			knownStoreFalse: false,
			wantChanged:     false,
			checkSummaryIdx: 0,
		},
	}

	ctxModes := []struct {
		name    string
		makeCtx func(body []byte) *gin.Context
	}{
		{
			name:    "nil_ctx",
			makeCtx: func([]byte) *gin.Context { return nil },
		},
		{
			name:    "cold_ctx",
			makeCtx: func([]byte) *gin.Context { return newTrack1BenchGinContext() },
		},
		{
			name: "warm_RequestBodyTop_ctx",
			makeCtx: func(body []byte) *gin.Context {
				c := newTrack1BenchGinContext()
				_ = RequestBodyTop(c, body)
				return c
			},
		},
	}

	for _, tc := range cases {
		for _, cm := range ctxModes {
			t.Run(tc.name+"/"+cm.name, func(t *testing.T) {
				body := []byte(tc.body)
				origSnapshot := append([]byte(nil), body...)
				c := cm.makeCtx(body)

				wantDec, wantChanged, wantErr := normalizeOpenAIAPIKeyStoreFalseReasoningReplayDecoded(body, tc.knownStoreFalse)
				require.NoError(t, wantErr)
				wantLegacy, wantLegacyChanged, wantLegacyErr := legacyNormalize(body, tc.knownStoreFalse)
				require.NoError(t, wantLegacyErr)

				got, gotChanged, gotErr := normalizeOpenAIAPIKeyStoreFalseReasoningReplay(c, body, tc.knownStoreFalse)
				require.NoError(t, gotErr)
				require.Equal(t, tc.wantChanged, gotChanged)
				require.Equal(t, wantChanged, gotChanged)
				require.Equal(t, wantLegacyChanged, gotChanged)
				require.JSONEq(t, string(wantDec), string(got))
				require.JSONEq(t, string(wantLegacy), string(got))
				require.True(t, gjson.GetBytes(got, fmt.Sprintf("input.%d.summary", tc.checkSummaryIdx)).IsArray())
				require.Equal(t, origSnapshot, body, "input body must never be mutated in place")
			})
		}
	}
}

func TestTrack1CompatSeedMemoInvalidJSONDoesNotPinBodySlice(t *testing.T) {
	invalidItem := `{"broken":` + strings.Repeat("x", compatSeedMemoMinItem+64)
	callerBody := []byte(`{"prefix":1,"item":` + invalidItem + `,"suffix":2}`)
	subStart := strings.Index(string(callerBody), invalidItem)
	require.Greater(t, subStart, 0)
	subEnd := subStart + len(invalidItem)
	subView := jsonView(callerBody[subStart:subEnd])

	var scratch canonScratch
	got := appendCompatSeedJSON(nil, subView, &scratch)
	require.Equal(t, invalidItem, string(got))

	cached, ok := compatSeedMemo.get(callerBody[subStart:subEnd])
	require.True(t, ok)
	require.Equal(t, invalidItem, cached)
	require.NotEqual(t,
		uintptr(unsafe.Pointer(unsafe.SliceData(callerBody[subStart:subEnd]))),
		uintptr(unsafe.Pointer(unsafe.StringData(cached))),
		"invalid-JSON memo fallback must store a detached copy instead of pinning caller body slice")
}
