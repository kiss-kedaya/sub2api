package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// ---- jsonSpanPatcher 本身 ----

func TestJSONSpanPatcherAppliesPatchesInOnePass(t *testing.T) {
	cases := []struct {
		name  string
		doc   string
		patch func(p *jsonSpanPatcher, view string)
		want  string
	}{
		{
			name: "replace a value",
			doc:  `{"a":1,"b":"x"}`,
			patch: func(p *jsonSpanPatcher, view string) {
				p.replace(gjson.Get(view, "b"), `"y"`)
			},
			want: `{"a":1,"b":"y"}`,
		},
		{
			name: "append member to a non-empty object adds a comma",
			doc:  `{"a":1}`,
			patch: func(p *jsonSpanPatcher, view string) {
				p.appendMemberIn(0, len(view), `"b":2`)
			},
			want: `{"a":1,"b":2}`,
		},
		{
			name: "append member to an empty object adds no comma",
			doc:  `{"o":{ },"p":{}}`,
			patch: func(p *jsonSpanPatcher, view string) {
				p.appendMember(gjson.Get(view, "o"), `"b":2`)
				p.appendMember(gjson.Get(view, "p"), `"c":3`)
			},
			want: `{"o":{ "b":2},"p":{"c":3}}`,
		},
		{
			// 与 sjson 追加新键的位置一致：写在最后一个 '}' 之前，前面的缩进/换行原样保留。
			name: "append member keeps the pretty-printed layout",
			doc:  "{\n  \"o\": {\n    \"a\": 1\n  }\n}",
			patch: func(p *jsonSpanPatcher, view string) {
				p.appendMember(gjson.Get(view, "o"), `"b":2`)
			},
			want: "{\n  \"o\": {\n    \"a\": 1\n  ,\"b\":2}\n}",
		},
		{
			name: "insert after the opening bracket",
			doc:  `{"l":[1,2]}`,
			patch: func(p *jsonSpanPatcher, view string) {
				p.insertAfterOpen(gjson.Get(view, "l"), `0,`)
			},
			want: `{"l":[0,1,2]}`,
		},
		{
			name: "insert at an offset",
			doc:  `[1,2]`,
			patch: func(p *jsonSpanPatcher, view string) {
				p.insertAt(len(view)-1, `,3`)
			},
			want: `[1,2,3]`,
		},
		{
			name: "several patches, added out of order, are applied by offset",
			doc:  `{"a":1,"b":2,"c":{"d":3}}`,
			patch: func(p *jsonSpanPatcher, view string) {
				p.appendMember(gjson.Get(view, "c"), `"e":4`)
				p.replace(gjson.Get(view, "b"), `20`)
				p.replace(gjson.Get(view, "a"), `10`)
			},
			want: `{"a":10,"b":20,"c":{"d":3,"e":4}}`,
		},
		{
			name: "two inserts at the same offset keep their order",
			doc:  `[]`,
			patch: func(p *jsonSpanPatcher, view string) {
				p.insertAt(1, `1`)
				p.insertAt(1, `,2`)
			},
			want: `[1,2]`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := []byte(tc.doc)
			p := newJSONSpanPatcher(doc)
			tc.patch(p, openAIWSPayloadStringView(doc))
			out, changed := p.apply()
			require.True(t, changed)
			require.Equal(t, tc.want, string(out))
			require.Equal(t, tc.doc, string(doc), "the input must never be modified")
			require.Len(t, out, len(doc)+p.delta, "output is allocated at its exact size")
			require.Equal(t, len(out), cap(out))
		})
	}
}

func TestJSONSpanPatcherWithoutPatchesReturnsTheSameSlice(t *testing.T) {
	doc := []byte(`{"a":1}`)
	out, changed := newJSONSpanPatcher(doc).apply()
	require.False(t, changed)
	require.True(t, sameSpanSlice(out, doc))
}

func TestJSONSpanPatcherRejectsPatchesThatDoNotMatchTheDocument(t *testing.T) {
	doc := []byte(`{"a":"xx","b":{}}`)
	view := openAIWSPayloadStringView(doc)

	t.Run("result from another document", func(t *testing.T) {
		other := gjson.Get(`{"a":"yy","b":{}}`, "a") // same offsets, different text
		p := newJSONSpanPatcher(doc)
		p.replace(other, `"z"`)
		out, changed := p.apply()
		require.False(t, changed)
		require.True(t, sameSpanSlice(out, doc))
	})
	t.Run("unknown index", func(t *testing.T) {
		p := newJSONSpanPatcher(doc)
		p.replace(gjson.Result{Type: gjson.String, Raw: `"xx"`}, `"z"`)
		_, changed := p.apply()
		require.False(t, changed)
	})
	t.Run("not an object", func(t *testing.T) {
		p := newJSONSpanPatcher(doc)
		p.appendMember(gjson.Get(view, "a"), `"k":1`)
		_, changed := p.apply()
		require.False(t, changed)
	})
	t.Run("overlapping patches", func(t *testing.T) {
		p := newJSONSpanPatcher(doc)
		p.replace(gjson.Get(view, "b"), `{"k":1}`)
		p.appendMember(gjson.Get(view, "b"), `"k":2`) // inserts inside the span replaced above
		_, changed := p.apply()
		require.False(t, changed)
	})
	t.Run("a bad patch invalidates the good ones", func(t *testing.T) {
		p := newJSONSpanPatcher(doc)
		p.replace(gjson.Get(view, "a"), `"z"`)
		p.insertAt(len(doc)+1, `x`)
		out, changed := p.apply()
		require.False(t, changed)
		require.True(t, sameSpanSlice(out, doc))
	})
	t.Run("result copied out of the document is still located by content", func(t *testing.T) {
		p := newJSONSpanPatcher(doc)
		p.replace(gjson.GetBytes(doc, "a"), `"z"`) // GetBytes copies Raw
		out, changed := p.apply()
		require.True(t, changed)
		require.Equal(t, `{"a":"z","b":{}}`, string(out))
	})
}

func TestDeepSeekPlaceholderFragmentsAreValidJSON(t *testing.T) {
	text, err := json.Marshal(deepSeekReasoningPlaceholderText)
	require.NoError(t, err)
	require.Equal(t, string(text), deepSeekReasoningPlaceholderJSON)
	text, err = json.Marshal(deepSeekThinkingPlaceholderText)
	require.NoError(t, err)
	require.Equal(t, string(text), deepSeekThinkingPlaceholderJSON)

	require.JSONEq(t, `{"reasoning_content":`+deepSeekReasoningPlaceholderJSON+`}`, `{`+deepSeekReasoningPlaceholderMember+`}`)
	require.JSONEq(t, `{"content":[{"type":"reasoning_text","text":`+deepSeekReasoningPlaceholderJSON+`}]}`, `{`+deepSeekNativeReasoningMember+`}`)
	require.JSONEq(t, `{"thinking":`+deepSeekThinkingPlaceholderJSON+`}`, `{`+deepSeekThinkingPlaceholderMember+`}`)
	require.JSONEq(t, `[{"type":"thinking","thinking":`+deepSeekThinkingPlaceholderJSON+`}]`, `[`+strings.TrimSuffix(deepSeekThinkingPlaceholderBlock, ",")+`]`)
}

// ---- 与旧 sjson 实现对照（oracle）----

type spanStrictness int

const (
	spanBytesIdentical            spanStrictness = iota // 新旧输出必须逐字节一致
	spanBytesIdenticalWhenCompact                       // 输入没有额外空白时逐字节一致，否则只要求语义一致
	spanSemanticallyEqual                               // 只要求解析后逐值相等
)

// runSpanOracle 对同一批随机输入分别跑旧实现和新实现，断言：
//   - 新实现不改入参（零拷贝视图的前提）；
//   - 输出与旧实现语义一致（合法输入解析后逐值相等，strict 时逐字节相等）；
//   - 旧实现没有改动时，新实现返回同一个切片；
//   - 输入不是合法 JSON 时，新实现要么原样返回要么与旧实现一致。
func runSpanOracle(t *testing.T, gen func(g *spanGen) (doc string, compact bool), mode spanStrictness, run func(t *testing.T, seed int64, input []byte) (want, got []byte)) {
	t.Helper()
	runs := 400
	if testing.Short() {
		runs = 80
	}
	check := func(seed int64, text string, compact bool) {
		input := []byte(text)
		orig := append([]byte(nil), input...)
		want, got := run(t, seed, input)
		require.Equal(t, string(orig), string(input), "seed=%d: input was modified", seed)
		strict := mode == spanBytesIdentical || (mode == spanBytesIdenticalWhenCompact && compact)
		if reason := spanEquivalent(input, want, got, strict); reason != "" {
			t.Fatalf("%s\n%s", reason, spanDescribe(seed, input, want, got))
		}
	}
	for seed := int64(1); seed <= int64(runs); seed++ {
		g := newSpanGen(seed)
		doc, compact := gen(g)
		check(seed, doc, compact)
		if seed%3 == 0 {
			check(seed, g.corruptJSON(doc), false)
		}
	}
}

func spanOracleAccounts() []*Account {
	return []*Account{
		{Platform: PlatformDeepseek, Type: AccountTypeAPIKey},
		{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": DefaultDeepseekBaseURL}},
		{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "http://upstream.example"}},
		nil,
	}
}

func TestEnsureDeepSeekChatReasoningPlaceholdersMatchesSJSONOracle(t *testing.T) {
	accounts := spanOracleAccounts()
	runSpanOracle(t, func(g *spanGen) (string, bool) { return g.chatDoc(55) }, spanBytesIdentical,
		func(t *testing.T, seed int64, input []byte) ([]byte, []byte) {
			account := accounts[seed%int64(len(accounts))]
			return legacyEnsureDeepSeekChatReasoningPlaceholders(account, input), ensureDeepSeekChatReasoningPlaceholders(account, input)
		})
}

func TestEnsureDeepSeekNativeReasoningPlaceholdersMatchesSJSONOracle(t *testing.T) {
	accounts := []*Account{
		{Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_protocol": APIProtocolAdaptive}},
		{Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_protocol": APIProtocolResponses}},
		{Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
		{Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_protocol": "chat"}},
		nil,
	}
	runSpanOracle(t, func(g *spanGen) (string, bool) { return g.nativeDoc() }, spanBytesIdentical,
		func(t *testing.T, seed int64, input []byte) ([]byte, []byte) {
			account := accounts[seed%int64(len(accounts))]
			return legacyEnsureDeepSeekNativeReasoningPlaceholders(account, input), ensureDeepSeekNativeReasoningPlaceholders(account, input)
		})
}

func TestEnsureDeepSeekThinkingReplayPlaceholdersMatchesSJSONOracle(t *testing.T) {
	models := []string{"deepseek-v4.1-flash", " DeepSeek-V4-Pro ", "claude-opus-5-5", ""}
	// 原实现在「空 thinking 块 + tool_use」时会把整个 content 数组重新 join（去掉元素间空白），
	// 紧凑输入逐字节一致，带缩进的输入只要求语义一致。
	runSpanOracle(t, func(g *spanGen) (string, bool) { return g.anthropicDoc() }, spanBytesIdenticalWhenCompact,
		func(t *testing.T, seed int64, input []byte) ([]byte, []byte) {
			model := models[seed%int64(len(models))]
			want, wantChanged := legacyEnsureDeepSeekThinkingReplayPlaceholders(input, model)
			got, gotChanged := ensureDeepSeekThinkingReplayPlaceholders(input, model)
			if json.Valid(input) {
				require.Equal(t, wantChanged, gotChanged, "seed=%d: changed flag", seed)
			}
			require.Equal(t, gotChanged, !sameSpanSlice(got, input), "seed=%d: changed means a new slice, unchanged the same one", seed)
			return want, got
		})
}

func TestDropOrphanDeepSeekToolMessagesMatchesSJSONOracle(t *testing.T) {
	runSpanOracle(t, func(g *spanGen) (string, bool) { return g.pairingDoc() }, spanBytesIdentical,
		func(t *testing.T, seed int64, input []byte) ([]byte, []byte) {
			return legacyDropOrphanDeepSeekToolMessages(input), dropOrphanDeepSeekToolMessages(input)
		})
}

func TestNormalizeDeepSeekChatMessagesMatchesSJSONOracle(t *testing.T) {
	runSpanOracle(t, func(g *spanGen) (string, bool) { return g.pairingDoc() }, spanBytesIdentical,
		func(t *testing.T, seed int64, input []byte) ([]byte, []byte) {
			return legacyNormalizeDeepSeekChatMessages(input), normalizeDeepSeekChatMessages(input)
		})
}

func TestRepairDeepSeekMissingToolOutputsMatchesSJSONOracle(t *testing.T) {
	runSpanOracle(t, func(g *spanGen) (string, bool) { return g.repairDoc() }, spanSemanticallyEqual,
		func(t *testing.T, seed int64, input []byte) ([]byte, []byte) {
			want, wantChanged, wantErr := legacyRepairDeepSeekMissingToolOutputs(input)
			got, gotChanged, gotErr := repairDeepSeekMissingToolOutputs(input)
			require.NoError(t, wantErr)
			require.NoError(t, gotErr)
			// 旧实现对「非法 JSON」一律原样返回；新实现只在需要修复时才校验，结论相同。
			require.Equal(t, wantChanged, gotChanged, "seed=%d: changed flag", seed)
			return want, got
		})
}

func TestEnsureOpenAIChatStreamUsageMatchesSJSONOracle(t *testing.T) {
	runs := 600
	if testing.Short() {
		runs = 120
	}
	for seed := int64(1); seed <= int64(runs); seed++ {
		g := newSpanGen(seed)
		text, _ := g.streamOptionsDoc()
		input := []byte(text)
		orig := append([]byte(nil), input...)
		want, wantErr := legacyEnsureOpenAIChatStreamUsage(input)
		got, gotErr := ensureOpenAIChatStreamUsage(input)
		require.Equal(t, string(orig), string(input), "seed=%d: input was modified", seed)
		if wantErr != nil || gotErr != nil {
			require.Equal(t, wantErr, gotErr, "seed=%d input=%q", seed, text)
			require.True(t, sameSpanSlice(got, input), "seed=%d: an error returns the original body", seed)
			continue
		}
		// 与 sjson 写法一致：连首尾空白的处理都逐字节相同（含非法输入）。
		if reason := spanEquivalent(input, want, got, true); reason != "" {
			t.Fatalf("%s\n%s", reason, spanDescribe(seed, input, want, got))
		}
		if json.Valid(input) {
			require.True(t, gjson.GetBytes(got, "stream_options.include_usage").Bool(), "seed=%d: include_usage must end up true\n%s", seed, text)
		}
	}
}

// ---- 逐条写死的期望（文档化行为）----

func TestEnsureDeepSeekChatReasoningPlaceholdersExactOutput(t *testing.T) {
	account := &Account{Platform: PlatformDeepseek}
	body := []byte(`{"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"a"},{"role":"assistant","reasoning_content":"","content":"b"},{"role":"assistant","reasoning_content":null},{"role":"assistant","reasoning_content":"keep"},{"role":"assistant"}],"stream":true}`)
	want := `{"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"a","reasoning_content":" "},{"role":"assistant","reasoning_content":" ","content":"b"},{"role":"assistant","reasoning_content":" "},{"role":"assistant","reasoning_content":"keep"},{"role":"assistant","reasoning_content":" "}],"stream":true}`
	require.Equal(t, want, string(ensureDeepSeekChatReasoningPlaceholders(account, body)))
	require.Equal(t, want, string(legacyEnsureDeepSeekChatReasoningPlaceholders(account, body)))
}

func TestEnsureDeepSeekNativeReasoningPlaceholdersExactOutput(t *testing.T) {
	account := &Account{Platform: PlatformDeepseek, Credentials: map[string]any{"api_protocol": APIProtocolAdaptive}}
	body := []byte(`{"input":[{"type":"reasoning","id":"a"},{"type":"reasoning","id":"b","content":null},{"type":"reasoning","id":"c","content":[]},{"type":"reasoning","id":"d","content":[{"type":"reasoning_text","text":"real"}]},{"type":"reasoning","id":"e","content":"vendor"},{"type":"message"}]}`)
	ph := `[{"type":"reasoning_text","text":" "}]`
	want := `{"input":[{"type":"reasoning","id":"a","content":` + ph + `},{"type":"reasoning","id":"b","content":` + ph + `},{"type":"reasoning","id":"c","content":` + ph + `},{"type":"reasoning","id":"d","content":[{"type":"reasoning_text","text":"real"}]},{"type":"reasoning","id":"e","content":"vendor"},{"type":"message"}]}`
	require.Equal(t, want, string(ensureDeepSeekNativeReasoningPlaceholders(account, body)))
	require.Equal(t, want, string(legacyEnsureDeepSeekNativeReasoningPlaceholders(account, body)))
}

func TestEnsureDeepSeekThinkingReplayPlaceholdersPrettyPrintedHistory(t *testing.T) {
	body := []byte("{\n \"messages\": [\n  {\"role\": \"user\", \"content\": \"q\"},\n  {\n   \"role\": \"assistant\",\n   \"content\": [\n    {\"type\": \"thinking\", \"thinking\": \"\"},\n    {\"type\": \"tool_use\", \"id\": \"t1\"}\n   ]\n  },\n  {\"role\": \"assistant\", \"content\": [ {\"type\": \"tool_use\", \"id\": \"t2\"} ]}\n ]\n}")
	out, changed := ensureDeepSeekThinkingReplayPlaceholders(body, "deepseek-v4-pro")
	require.True(t, changed)
	// 未改动的字节（缩进、换行）原样保留。
	require.Equal(t, "{\n \"messages\": [\n  {\"role\": \"user\", \"content\": \"q\"},\n  {\n   \"role\": \"assistant\",\n   \"content\": [\n    {\"type\": \"thinking\", \"thinking\": \" \"},\n    {\"type\": \"tool_use\", \"id\": \"t1\"}\n   ]\n  },\n  {\"role\": \"assistant\", \"content\": [{\"type\":\"thinking\",\"thinking\":\" \"}, {\"type\": \"tool_use\", \"id\": \"t2\"} ]}\n ]\n}", string(out))
	want, _ := legacyEnsureDeepSeekThinkingReplayPlaceholders(body, "deepseek-v4-pro")
	require.Equal(t, mustDecodeSpanJSON(t, want), mustDecodeSpanJSON(t, out))
}

func TestRepairDeepSeekMissingToolOutputsExactOutput(t *testing.T) {
	body := []byte(`{"input":[{"type":"message","role":"user","content":"run"},{"type":"function_call","call_id":"c1"},{"type":"custom_tool_call","call_id":"c2"},{"type":"function_call_output","call_id":"c1","output":"ok"},{"type":"message","role":"user","content":"go"},{"type":"local_shell_call","call_id":"c3"}]}`)
	out, changed, err := repairDeepSeekMissingToolOutputs(body)
	require.NoError(t, err)
	require.True(t, changed)
	// c2 的输出紧跟在它之后的第一个「非调用/非输出」条目之前；末尾悬空的 c3 补在数组最后。
	want := `{"input":[{"type":"message","role":"user","content":"run"},{"type":"function_call","call_id":"c1"},{"type":"custom_tool_call","call_id":"c2"},{"type":"function_call_output","call_id":"c1","output":"ok"},` +
		`{"call_id":"c2","output":` + mustJSONString(deepSeekMissingToolOutput) + `,"type":"custom_tool_call_output"},` +
		`{"type":"message","role":"user","content":"go"},{"type":"local_shell_call","call_id":"c3"},` +
		`{"call_id":"c3","output":` + mustJSONString(deepSeekMissingToolOutput) + `,"type":"local_shell_call_output"}]}`
	require.Equal(t, want, string(out))

	// 已完整配对 / 续接请求 / 非数组 input：原样返回同一个切片。
	for _, same := range []string{
		`{"input":[{"type":"function_call","call_id":"c1"},{"type":"function_call_output","call_id":"c1"}]}`,
		`{"previous_response_id":"resp","input":[{"type":"function_call","call_id":"c1"}]}`,
		`{"input":"text"}`,
		`{"input":[{"type":"function_call","call_id":"c1"}]`, // 非法 JSON：不修复
		``,
	} {
		in := []byte(same)
		next, changed, err := repairDeepSeekMissingToolOutputs(in)
		require.NoError(t, err)
		require.False(t, changed, same)
		require.True(t, sameSpanSlice(next, in), same)
	}
}

func TestEnsureOpenAIChatStreamUsageShapes(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"absent", `{"model":"m"}`, `{"model":"m","stream_options":{"include_usage":true}}`},
		{"absent in empty object", `{}`, `{"stream_options":{"include_usage":true}}`},
		{"empty stream_options", `{"stream_options":{}}`, `{"stream_options":{"include_usage":true}}`},
		{"other members", `{"stream_options":{"a":1}}`, `{"stream_options":{"a":1,"include_usage":true}}`},
		{"false becomes true", `{"stream_options":{"include_usage":false,"a":1}}`, `{"stream_options":{"include_usage":true,"a":1}}`},
		{"null becomes true", `{"stream_options":{"include_usage":null}}`, `{"stream_options":{"include_usage":true}}`},
		{"null stream_options", `{"stream_options":null}`, `{"stream_options":{"include_usage":true}}`},
		{"already true", `{"stream_options":{"include_usage":true},"m":1}`, `{"stream_options":{"include_usage":true},"m":1}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := []byte(tc.in)
			out, err := ensureOpenAIChatStreamUsage(in)
			require.NoError(t, err)
			require.Equal(t, tc.want, string(out))
			want, _ := legacyEnsureOpenAIChatStreamUsage(in)
			require.Equal(t, string(want), string(out))
		})
	}

	in := []byte(`{"stream_options":{"include_usage":true}}`)
	out, err := ensureOpenAIChatStreamUsage(in)
	require.NoError(t, err)
	require.True(t, sameSpanSlice(out, in), "nothing to change: no copy")

	// 数组形状沿用 sjson 的报错，并原样返回入参。
	in = []byte(`{"stream_options":[]}`)
	out, err = ensureOpenAIChatStreamUsage(in)
	require.Error(t, err)
	require.True(t, sameSpanSlice(out, in))
}

func mustDecodeSpanJSON(t *testing.T, b []byte) any {
	t.Helper()
	v, err := decodeSpanJSON(b)
	require.NoError(t, err, string(b))
	return v
}
