package service

import (
	"bytes"
	"fmt"
	"math/rand"
	"runtime"
	"strings"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// legacySanitizeOpenAIResponsesInputItemIDs 是改成“一遍扫描收集 edit”之前的实现，只作对照（oracle）。
func legacySanitizeOpenAIResponsesInputItemIDs(body []byte) ([]byte, bool, error) {
	input := parseRawJSONView(body).Get("input")
	if !input.IsArray() {
		return body, false, nil
	}

	type inputItem struct {
		raw         string
		stripID     bool
		stripCallID bool
	}

	items := make([]inputItem, 0)
	input.ForEach(func(_, item gjson.Result) bool {
		parsed := inputItem{raw: item.Raw}
		if item.IsObject() {
			itemType := item.Get("type")
			id := item.Get("id")
			trimmedItemType := strings.TrimSpace(itemType.String())
			parsed.stripCallID = item.Get("call_id").Exists() && shouldStripOpenAIResponsesNonPairCallID(trimmedItemType)
			if id.Type == gjson.String {
				parsed.stripID = shouldStripOpenAIResponsesInputItemID(trimmedItemType, id.String())
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

	rebuiltItems := make([]string, 0, len(items))
	for index, item := range items {
		if !item.stripID && !item.stripCallID {
			rebuiltItems = append(rebuiltItems, item.raw)
			continue
		}
		itemBody := []byte(item.raw)
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
		rebuiltItems = append(rebuiltItems, string(itemBody))
	}
	return replaceOpenAIRawInput(body, input, rebuiltItems), true, nil
}

func TestSanitizeOpenAIResponsesInputItemIDsMatchesLegacy(t *testing.T) {
	fixed := []string{
		``,
		`{}`,
		`{"input":[]}`,
		`{"input":"text"}`,
		`{"input":{"type":"message","id":"bad"}}`,
		`[{"input":[{"type":"message","id":"bad"}]}]`,
		// 单项/首项/末项/中间项
		`{"input":[{"type":"message","id":"bad"}]}`,
		`{"input":[{"id":"bad","type":"message","content":"x"}]}`,
		`{"input":[{"type":"message","content":"x","id":"bad"}]}`,
		`{"input":[{"type":"message","id":"bad","content":"x"}]}`,
		`{"input":[{"id":"bad"}]}`,
		`{"input":[{"type":"message","id":""}]}`,
		// id 与 call_id 相邻 / 不相邻 / 顺序颠倒
		`{"input":[{"type":"message","id":"bad","call_id":"c","content":"x"}]}`,
		`{"input":[{"type":"message","call_id":"c","id":"bad","content":"x"}]}`,
		`{"input":[{"id":"bad","call_id":"c","type":"message"}]}`,
		`{"input":[{"call_id":"c","id":"bad","type":"message"}]}`,
		`{"input":[{"type":"message","id":"bad","content":"x","call_id":"c"}]}`,
		`{"input":[{"call_id":"c","type":"message","id":"bad"}]}`,
		`{"input":[{"type":"message","call_id":"c","content":"x"}]}`,
		`{"input":[{"type":"message","call_id":null}]}`,
		`{"input":[{"type":"reasoning","id":"bad","call_id":"c"}]}`,
		`{"input":[{"id":"bad","type":"image_generation_call","call_id":"c"}]}`,
		// 重复键、转义键、嵌套同名键、非字符串 id
		`{"input":[{"type":"message","id":"bad","id":"msg_ok"}]}`,
		`{"input":[{"type":"message","id":"msg_ok","id":"bad"}]}`,
		`{"input":[{"type":"message","id":7}]}`,
		`{"input":[{"type":"message","id":"bad"}]}`,
		`{"input":[{"type":"message","content":{"id":"nested","call_id":"nested"},"id":"bad"}]}`,
		`{"input":[{"type":"message","id":"bad"}]}`,
		`{"input":[{"type":" message ","id":"bad"}]}`,
		// 多项混合、非对象项
		`{"input":[null,1,"s",[1],{"type":"message","id":"bad"},{"type":"message","id":"msg_ok"},{"type":"reasoning","id":"bad"}]}`,
		`{"input":[{"type":"message","id":"bad"},{"type":"message","id":"bad"},{"type":"message","id":"bad"}]}`,
		// 宽松排版（交给 sjson）
		`{"input":[ {"type":"message", "id":"bad", "content":"x"} ]}`,
		`{"input":[{ "type" : "message" , "id" : "bad" }]}`,
		`{"input":[{"id": "bad", "type": "message"}]}`,
		"{\n  \"input\": [\n    {\"type\":\"message\",\"id\":\"bad\"},\n    {\n      \"type\": \"message\",\n      \"id\": \"bad\",\n      \"call_id\": \"c\"\n    }\n  ]\n}",
		`{"input":[{"type":"message","id":"bad" ,"call_id":"c" }]}`,
		`{"input":[{"type":"message" ,"id":"bad"}]}`,
		// 根部排版 / 前后缀
		" \n\t" + `{"model":"m","input":[{"type":"message","id":"bad"}],"store":false}` + "\r\n",
		`{"shadow":{"input":[{"type":"message","id":"bad"}]},"input":[{"type":"message","id":"bad"}],"sequence":9007199254740993}`,
		`{"input":[{"type":"message","id":"bad"}]} trailing`,
		`{"input":[{"type":"message","id":"bad"}],"input":[{"type":"message","id":"bad2"}]}`,
		// 已经合法 / 未约束的类型
		`{"input":[{"type":"message","id":"msg_1"},{"type":"item_reference","id":"remote"},{"type":"future","id":""}]}`,
		// 畸形
		`{"input":[{"type":"message","id":"bad"}`,
		`{"input":[{"type":"message","id":"bad"},{"type":"message","id":}]}`,
		`{"input":[{"type":"message","id":"bad",}]}`,
		`{"input":[{"type":"message","id":"bad"}]`,
	}
	for _, fixture := range fixed {
		assertSanitizeMatchesLegacy(t, []byte(fixture))
	}
}

// stripJSONInsignificantWhitespace 去掉字符串字面量之外的空白，用来比较“语义相同”的两份 JSON。
func stripJSONInsignificantWhitespace(s string) string {
	var sb strings.Builder
	inString, escaped := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inString:
			_ = sb.WriteByte(c)
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
		case c == '"':
			inString = true
			_ = sb.WriteByte(c)
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
		default:
			_ = sb.WriteByte(c)
		}
	}
	return sb.String()
}

// inputArrayItemsAreCommaJoined 报告 body 里顶层 input 数组是否正好是 "[" + 各项用 "," 连接 + "]"。
// 旧实现改写时会把数组重建成这种排版（吞掉项与项之间的空白）；新实现保留未改动字节原样。
func inputArrayItemsAreCommaJoined(body []byte) bool {
	input := gjson.ParseBytes(body).Get("input")
	if !input.IsArray() {
		return true
	}
	var raws []string
	input.ForEach(func(_, item gjson.Result) bool {
		raws = append(raws, item.Raw)
		return true
	})
	return input.Raw == "["+strings.Join(raws, ",")+"]"
}

func assertSanitizeMatchesLegacy(t *testing.T, body []byte) {
	t.Helper()
	original := bytes.Clone(body)
	wantBody, wantChanged, wantErr := legacySanitizeOpenAIResponsesInputItemIDs(body)
	gotBody, gotChanged, gotErr := sanitizeOpenAIResponsesInputItemIDs(body)
	require.Equal(t, original, body, "入参不得被改写: %q", original)
	require.Equal(t, wantErr != nil, gotErr != nil, "body: %q", original)
	require.Equal(t, wantChanged, gotChanged, "body: %q", original)
	if wantErr != nil {
		return
	}
	if !gotChanged {
		require.Same(t, unsafe.SliceData(body), unsafe.SliceData(gotBody), "未改动必须原样返回切片")
		require.Equal(t, string(wantBody), string(gotBody))
		return
	}
	if !gjson.ValidBytes(body) {
		// 截断/畸形输入：旧实现会顺手补上缺失的 ']'，只要求二者都判定为“有改动”。
		return
	}
	require.True(t, gjson.ValidBytes(gotBody), "body: %q", original)
	require.Equal(t, stripJSONInsignificantWhitespace(string(wantBody)), stripJSONInsignificantWhitespace(string(gotBody)), "body: %q", original)
	if inputArrayItemsAreCommaJoined(body) {
		require.Equal(t, string(wantBody), string(gotBody), "排版紧凑时必须逐字节一致, body: %q", original)
	}
}

func randomSanitizeItem(r *rand.Rand, loose bool) string {
	ws := func() string {
		if loose && r.Intn(2) == 0 {
			return []string{" ", "\n", "\t", "  "}[r.Intn(4)]
		}
		return ""
	}
	types := []string{"message", "reasoning", "function_call", "function_call_output", "custom_tool_call", "custom_tool_call_output", "image_generation_call", "item_reference", "web_search_call", "tool_search_call", "tool_search_output", "future_type", "", "local_shell_call"}
	ids := []string{`"msg_ok"`, `"bad"`, `""`, `"rs_ok"`, `"fc_ok"`, `"ctc_ok"`, `"call_x"`, `"ws_1"`, `7`, `null`, `true`}
	callIDs := []string{`"c1"`, `"call_9"`, `null`, `5`, `""`}
	members := make([]string, 0, 8)
	add := func(key, value string) {
		members = append(members, `"`+key+`"`+ws()+`:`+ws()+value)
	}
	if r.Intn(8) != 0 {
		add("type", `"`+types[r.Intn(len(types))]+`"`)
	}
	if r.Intn(8) != 0 {
		add("id", ids[r.Intn(len(ids))])
	}
	if r.Intn(3) == 0 {
		add("call_id", callIDs[r.Intn(len(callIDs))])
	}
	if r.Intn(6) == 0 {
		add("id", ids[r.Intn(len(ids))]) // 重复键
	}
	if r.Intn(6) == 0 {
		members = append(members, `"\u00`+`69d":`+ids[r.Intn(len(ids))]) // 转义键: "id" == "id"
	}
	add("content", []string{`"hello"`, `[{"type":"input_text","text":"x","id":"nested"}]`, `{"id":"n","call_id":"n"}`, `null`, `12345678901234567890`, `"你好\n"`}[r.Intn(6)])
	if r.Intn(3) == 0 {
		add("status", `"completed"`)
	}
	r.Shuffle(len(members), func(i, j int) { members[i], members[j] = members[j], members[i] })
	sep := ","
	if loose && r.Intn(2) == 0 {
		sep = " , "
	}
	return `{` + ws() + strings.Join(members, sep) + ws() + `}`
}

func randomSanitizeBody(r *rand.Rand, loose bool) string {
	n := r.Intn(8)
	items := make([]string, n)
	for i := range items {
		switch r.Intn(12) {
		case 0:
			items[i] = []string{`null`, `1`, `"text"`, `[1,2]`, `true`}[r.Intn(5)]
		default:
			items[i] = randomSanitizeItem(r, loose)
		}
	}
	sep := ","
	if loose && r.Intn(3) == 0 {
		sep = ", "
	}
	members := []string{`"model":"m"`, `"input":[` + strings.Join(items, sep) + `]`, `"store":false`}
	if r.Intn(6) == 0 {
		members = append(members, `"input":[`+randomSanitizeItem(r, loose)+`]`)
	}
	if r.Intn(6) == 0 {
		members = append(members, `"shadow":{"input":[`+randomSanitizeItem(r, loose)+`]}`)
	}
	r.Shuffle(len(members), func(i, j int) { members[i], members[j] = members[j], members[i] })
	body := `{` + strings.Join(members, ",") + `}`
	if loose && r.Intn(4) == 0 {
		body = " \n" + body + "\n"
	}
	return body
}

func TestSanitizeOpenAIResponsesInputItemIDsMatchesLegacyRandomized(t *testing.T) {
	r := rand.New(rand.NewSource(20261125))
	changedSeen := 0
	for i := 0; i < 6000; i++ {
		body := randomSanitizeBody(r, i%2 == 1)
		assertSanitizeMatchesLegacy(t, []byte(body))
		if _, changed, _ := sanitizeOpenAIResponsesInputItemIDs([]byte(body)); changed {
			changedSeen++
		}
	}
	require.Greater(t, changedSeen, 1500, "随机语料应当大量覆盖会改写的输入")
}

func TestSanitizeOpenAIResponsesInputItemIDsTruncatedBodiesMatchLegacy(t *testing.T) {
	base := `{"model":"m","input":[{"type":"message","id":"bad","call_id":"c","content":"x"},{"type":"reasoning","id":"bad"}],"store":false}`
	for cut := 0; cut <= len(base); cut++ {
		assertSanitizeMatchesLegacy(t, []byte(base[:cut]))
	}
}

func largeSanitizeBody(items, payloadBytes int) []byte {
	payload := strings.Repeat("x", payloadBytes)
	parts := make([]string, items)
	for i := range parts {
		parts[i] = fmt.Sprintf(`{"type":"message","id":"item_%d","role":"user","content":[{"type":"input_text","text":"%s"}]}`, i, payload)
	}
	return []byte(`{"model":"m","stream":true,"input":[` + strings.Join(parts, ",") + `],"store":false}`)
}

func allocatedBytesDuring(fn func()) uint64 {
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	fn()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// MemStats 和 testing.AllocsPerRun 都是进程级计数：同包其他测试遗留的后台
// goroutine 在测量窗口里分配的内存也会被算进来（CI 上出现过 66 次 > 40 的误报），
// 噪声只会让读数偏大，所以对“上界断言”取多次测量的最小值。
const allocMeasureAttempts = 5

func minAllocatedBytes(first uint64, fn func()) uint64 {
	best := first
	for i := 1; i < allocMeasureAttempts; i++ {
		if b := allocatedBytesDuring(fn); b < best {
			best = b
		}
	}
	return best
}

func minAllocsPerRun(runs int, fn func()) float64 {
	best := testing.AllocsPerRun(runs, fn)
	for i := 1; i < allocMeasureAttempts; i++ {
		if a := testing.AllocsPerRun(runs, fn); a < best {
			best = a
		}
	}
	return best
}

// 内存断言：改写 N 个大 item 只允许分配“结果本身”那一份，且结果没有多余容量，
// 也不与入参共享内存（入参还要留给 failover 重放和计费估算）。
func TestSanitizeOpenAIResponsesInputItemIDsAllocatesOnlyTheResult(t *testing.T) {
	body := largeSanitizeBody(256, 8<<10) // 约 2MB，256 个 item 全部带无效 id
	var got []byte
	newBytes := allocatedBytesDuring(func() {
		var changed bool
		var err error
		got, changed, err = sanitizeOpenAIResponsesInputItemIDs(body)
		require.NoError(t, err)
		require.True(t, changed)
	})
	newBytes = minAllocatedBytes(newBytes, func() { _, _, _ = sanitizeOpenAIResponsesInputItemIDs(body) })
	var want []byte
	legacyBytes := allocatedBytesDuring(func() {
		want, _, _ = legacySanitizeOpenAIResponsesInputItemIDs(body)
	})
	require.Equal(t, string(want), string(got))

	require.Equal(t, len(got), cap(got), "结果不应带 append 扩容留下的空闲容量")
	base := uintptr(unsafe.Pointer(unsafe.SliceData(body)))
	p := uintptr(unsafe.Pointer(unsafe.SliceData(got)))
	require.False(t, p >= base && p < base+uintptr(len(body)), "结果不应与入参共享内存")
	t.Logf("body=%d result=%d 改写一次分配: legacy=%d current=%d", len(body), len(got), legacyBytes, newBytes)

	require.Less(t, newBytes, uint64(len(got))+256<<10, "只应分配结果那一份（new=%d legacy=%d body=%d）", newBytes, legacyBytes, len(body))
	require.Greater(t, legacyBytes, 2*uint64(len(body)), "旧实现应当有多份中间复制，说明度量有效")

	allocs := minAllocsPerRun(20, func() { _, _, _ = sanitizeOpenAIResponsesInputItemIDs(body) })
	legacyAllocs := testing.AllocsPerRun(20, func() { _, _, _ = legacySanitizeOpenAIResponsesInputItemIDs(body) })
	require.Less(t, allocs, 40.0, "分配次数不应随被改 item 数线性增长（旧实现=%v）", legacyAllocs)
	require.Greater(t, legacyAllocs, 3*256.0)
}

func TestSanitizeOpenAIResponsesInputItemIDsLeadingWhitespaceKeepsOffsets(t *testing.T) {
	body := []byte("   \n" + `{"input":[{"type":"message","id":"bad"}],"tail":1}`)
	got, changed, err := sanitizeOpenAIResponsesInputItemIDs(body)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "   \n"+`{"input":[{"type":"message"}],"tail":1}`, string(got))
	require.True(t, bytes.HasPrefix(got, []byte("   \n{")))
}

func BenchmarkSanitizeOpenAIResponsesInputItemIDs(b *testing.B) {
	for _, tc := range []struct {
		name  string
		items int
		size  int
	}{
		{"256items_8KB", 256, 8 << 10},
		{"2000items_512B", 2000, 512},
	} {
		body := largeSanitizeBody(tc.items, tc.size)
		for _, impl := range []struct {
			name string
			fn   func([]byte) ([]byte, bool, error)
		}{
			{"legacy", legacySanitizeOpenAIResponsesInputItemIDs},
			{"current", sanitizeOpenAIResponsesInputItemIDs},
		} {
			b.Run(impl.name+"/"+tc.name, func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(body)))
				for i := 0; i < b.N; i++ {
					out, changed, err := impl.fn(body)
					if err != nil || !changed {
						b.Fatalf("changed=%v err=%v", changed, err)
					}
					benchmarkOpenAIRawRewriteSink = out
				}
			})
		}
	}
}
