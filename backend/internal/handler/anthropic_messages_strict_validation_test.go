package handler

import (
	"bytes"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const strictOpusModel = "claude-opus-5-5"

type strictOpusCase struct {
	name    string
	model   string
	body    string
	message string
	ok      bool
}

var strictOpusTableCases = []strictOpusCase{
	{
		name:  "缺 max_tokens",
		model: "claude-opus-5-5",
		body:  `{"model":"claude-opus-5-5","messages":[{"role":"user","content":"hi"}]}`,
		// 必须 400，且不得被补成 128000 / 4096 后放行。
		message: "max_tokens: Field required",
	},
	{
		name:    "max_tokens 小于 1",
		model:   "claude-opus-5-5",
		body:    `{"model":"claude-opus-5-5","max_tokens":0,"messages":[{"role":"user","content":"hi"}]}`,
		message: "max_tokens: Field required",
	},
	{
		name:    "temperature 越界",
		model:   "claude-opus-5-5",
		body:    `{"model":"claude-opus-5-5","max_tokens":16,"temperature":5,"messages":[{"role":"user","content":"hi"}]}`,
		message: "temperature: range: 0..1",
	},
	{
		name:    "text 块缺 text",
		model:   "claude-opus-5-5",
		body:    `{"model":"claude-opus-5-5","max_tokens":16,"messages":[{"role":"user","content":[{"type":"text"}]}]}`,
		message: "messages.0.content.0.text: text content blocks must contain non-whitespace text",
	},
	{
		name:    "messages 为空数组",
		model:   "claude-opus-5-5",
		body:    `{"model":"claude-opus-5-5","max_tokens":16,"messages":[]}`,
		message: "field messages is required",
	},
	{
		name:    "messages 缺失",
		model:   "claude-opus-5-5",
		body:    `{"model":"claude-opus-5-5","max_tokens":16}`,
		message: "field messages is required",
	},
	{
		name:    "开 thinking 又传非默认 temperature",
		model:   "claude-opus-5-5",
		body:    `{"model":"claude-opus-5-5","max_tokens":1200,"temperature":0.5,"thinking":{"type":"adaptive"},"messages":[{"role":"user","content":"hi"}]}`,
		message: "temperature may only be set to 1 when thinking is enabled",
	},
	{
		name:    "开 thinking 又传 top_p",
		model:   "claude-opus-5-5",
		body:    `{"model":"claude-opus-5-5","max_tokens":1200,"top_p":0.3,"thinking":{"type":"adaptive"},"messages":[{"role":"user","content":"hi"}]}`,
		message: "top_p may only be set to 1 when thinking is enabled",
	},
	{
		name:  "合格的 thinking 请求放行",
		model: "claude-opus-5-5",
		body:  `{"model":"claude-opus-5-5","max_tokens":1200,"thinking":{"type":"adaptive"},"output_config":{"effort":"high"},"messages":[{"role":"user","content":"A bat and a ball cost $1.10 total."}]}`,
		ok:    true,
	},
	{
		name:  "thinking.type=enabled 不在本函数拦（交由既有 adaptive 门处理）",
		model: "claude-opus-5-5",
		body:  `{"model":"claude-opus-5-5","max_tokens":1200,"thinking":{"type":"enabled","budget_tokens":2048},"messages":[{"role":"user","content":"hi"}]}`,
		ok:    true,
	},
	{
		name:  "非 opus-5-5 不拦",
		model: "claude-sonnet-4-5",
		body:  `{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":[{"type":"text"}]}]}`,
		ok:    true,
	},
	{
		name:  "带厂商前缀 / 点号写法同样命中",
		model: "anthropic/claude-opus-5.5",
		body:  `{"model":"anthropic/claude-opus-5.5","messages":[{"role":"user","content":"hi"}]}`,
		// IsOpus55 归一化后应识别为该模型，因此缺 max_tokens 要拦。
		message: "max_tokens: Field required",
	},
}

// strictOpusSpecCases 把每条规则的边界与校验顺序一条一条钉住（一个用例钉一个点），期望值取自旧实现的真实行为。
var strictOpusSpecCases = func() []strictOpusCase {
	const (
		model    = "claude-opus-5-5"
		msgs     = `"messages":[{"role":"user","content":"hi"}]`
		textErr  = ": text content blocks must contain non-whitespace text"
		needMsgs = "field messages is required"
		needMax  = "max_tokens: Field required"
		tempErr  = "temperature: range: 0..1"
		thinkT   = "temperature may only be set to 1 when thinking is enabled"
		thinkP   = "top_p may only be set to 1 when thinking is enabled"
		thinkK   = "top_k may only be set to 0 when thinking is enabled"
	)
	c := func(name, body, message string) strictOpusCase {
		return strictOpusCase{name: name, model: model, body: body, message: message, ok: message == ""}
	}
	return []strictOpusCase{
		// max_tokens
		c("max_tokens 恰为 1 放行", `{"max_tokens":1,`+msgs+`}`, ""),
		c("max_tokens 为 0.5 拒绝", `{"max_tokens":0.5,`+msgs+`}`, needMax),
		c("max_tokens 为字符串拒绝", `{"max_tokens":"16",`+msgs+`}`, needMax),
		c("max_tokens 为 null 拒绝", `{"max_tokens":null,`+msgs+`}`, needMax),
		// temperature
		c("temperature 恰为 0 放行", `{"max_tokens":1,"temperature":0,`+msgs+`}`, ""),
		c("temperature 恰为 1 放行", `{"max_tokens":1,"temperature":1,`+msgs+`}`, ""),
		c("temperature 为 -0 放行", `{"max_tokens":1,"temperature":-0,`+msgs+`}`, ""),
		c("temperature 略大于 1 拒绝", `{"max_tokens":1,"temperature":1.0000001,`+msgs+`}`, tempErr),
		c("temperature 略小于 0 拒绝", `{"max_tokens":1,"temperature":-0.0000001,`+msgs+`}`, tempErr),
		c("temperature 为字符串拒绝", `{"max_tokens":1,"temperature":"0.5",`+msgs+`}`, tempErr),
		c("temperature 为 null 拒绝", `{"max_tokens":1,"temperature":null,`+msgs+`}`, tempErr),
		// thinking 与采样参数
		c("开 thinking 又传 top_k", `{"max_tokens":1,"top_k":40,"thinking":{"type":"adaptive"},`+msgs+`}`, thinkK),
		c("开 thinking 时负的 top_k 也拒绝", `{"max_tokens":1,"top_k":-1,"thinking":{"type":"enabled"},`+msgs+`}`, thinkK),
		c("开 thinking 时大于 1 的 top_p 也拒绝", `{"max_tokens":1,"top_p":5,"thinking":{"type":"enabled"},`+msgs+`}`, thinkP),
		c("开 thinking 且采样参数取默认值放行", `{"max_tokens":1,"temperature":1,"top_p":1,"top_k":0,"thinking":{"type":"adaptive"},`+msgs+`}`, ""),
		c("thinking.type 前后空白被忽略", `{"max_tokens":1,"temperature":0.5,"thinking":{"type":" enabled "},`+msgs+`}`, thinkT),
		c("thinking.type 区分大小写", `{"max_tokens":1,"temperature":0.5,"thinking":{"type":"ENABLED"},`+msgs+`}`, ""),
		c("thinking 不是对象时不触发", `{"max_tokens":1,"temperature":0.5,"thinking":"enabled",`+msgs+`}`, ""),
		c("没开 thinking 时 top_k 随意", `{"max_tokens":1,"top_k":40,"top_p":0.3,`+msgs+`}`, ""),
		c("开 thinking 时字符串 \"1\" 的 top_p 等于 1", `{"max_tokens":1,"top_p":"1","thinking":{"type":"enabled"},`+msgs+`}`, ""),
		c("开 thinking 时字符串 \"0.5\" 的 top_p 拒绝", `{"max_tokens":1,"top_p":"0.5","thinking":{"type":"enabled"},`+msgs+`}`, thinkP),
		c("开 thinking 时 top_p 为 true 等于 1", `{"max_tokens":1,"top_p":true,"thinking":{"type":"enabled"},`+msgs+`}`, ""),
		// text 块
		c("空白 text 块报出准确下标",
			`{"max_tokens":1,"messages":[{"role":"user","content":"x"},{"role":"user","content":[{"type":"text","text":"ok"},{"type":"image","source":{"data":"x"}},{"type":"text","text":" \n "}]}]}`,
			"messages.1.content.2.text"+textErr),
		c("多个空白块只报第一个",
			`{"max_tokens":1,"messages":[{"content":[{"type":"text","text":"a"},{"type":"text","text":""}]},{"content":[{"type":"text","text":""}]}]}`,
			"messages.0.content.1.text"+textErr),
		c("转义的空白也算空白", `{"max_tokens":1,"messages":[{"content":[{"type":"text","text":" \n\t"}]}]}`, "messages.0.content.0.text"+textErr),
		c("NBSP 也算空白", "{\"max_tokens\":1,\"messages\":[{\"content\":[{\"type\":\"text\",\"text\":\"\u00a0 \u3000\"}]}]}", "messages.0.content.0.text"+textErr),
		c("零宽空格不算空白", "{\"max_tokens\":1,\"messages\":[{\"content\":[{\"type\":\"text\",\"text\":\"\u200b\"}]}]}", ""),
		c("text 为 null 当作空", `{"max_tokens":1,"messages":[{"content":[{"type":"text","text":null}]}]}`, "messages.0.content.0.text"+textErr),
		c("text 为数字不算空", `{"max_tokens":1,"messages":[{"content":[{"type":"text","text":0}]}]}`, ""),
		c("非 text 块的空 text 不报错", `{"max_tokens":1,"messages":[{"content":[{"type":"image","text":""},{"text":""}]}]}`, ""),
		c("tool_result 里嵌套的空 text 不报错", `{"max_tokens":1,"messages":[{"content":[{"type":"tool_result","content":[{"type":"text","text":""}]}]}]}`, ""),
		c("content 为字符串时不检查", `{"max_tokens":1,"messages":[{"content":""}]}`, ""),
		c("重复的 text 键以第一个为准", `{"max_tokens":1,"messages":[{"content":[{"type":"text","text":"x","text":""}]}]}`, ""),
		c("重复的 content 键以第一个为准", `{"max_tokens":1,"messages":[{"content":"x","content":[{"type":"text","text":""}]}]}`, ""),
		c("type 的转义写法同样是 text", `{"max_tokens":1,"messages":[{"content":[{"type":"te\u0078t","text":""}]}]}`, "messages.0.content.0.text"+textErr),
		// 校验顺序
		c("顺序：messages 必填先于 max_tokens", `{"temperature":5}`, needMsgs),
		c("顺序：max_tokens 先于 temperature", `{"temperature":5,`+msgs+`}`, needMax),
		c("顺序：temperature 先于空白 text 块", `{"max_tokens":1,"temperature":5,"messages":[{"content":[{"type":"text","text":""}]}]}`, tempErr),
		c("顺序：空白 text 块先于 thinking 采样规则",
			`{"max_tokens":1,"temperature":0.5,"thinking":{"type":"enabled"},"messages":[{"content":[{"type":"text","text":""}]}]}`,
			"messages.0.content.0.text"+textErr),
		// 请求体形状
		c("空 body", ``, needMsgs),
		c("messages 不是数组", `{"max_tokens":1,"messages":{"role":"user"}}`, needMsgs),
		c("BOM 前缀的合法请求放行（退回 gjson）", "\xef\xbb\xbf{\"max_tokens\":1,"+msgs+"}", ""),
		c("转义的键名同样识别（退回 gjson）", `{"max\u005ftokens":1,`+msgs+`}`, ""),
		// thinking 是数组：gjson 会下钻数组，畸形文本上收尾位置与括号计深不同（独立评审发现），一律退回 gjson。
		c("thinking 数组暗藏第二个 thinking（括号错位）", `{"messages":[{}],"max_tokens":1,"top_p":0,"thinking":[(],"thinking":{"type":"enabled"})}`, thinkP),
		c("thinking 数组暗藏第二个 thinking（} 被吞）", `{"messages":[{}],"max_tokens":1,"top_p":0,"thinking":[}}]"thinking":{"type":"enabled"}}`, thinkP),
		c("thinking 数组暗藏第二个 thinking（数字吞掉 [）", `{"messages":[{}],"max_tokens":1,"temperature":0,"thinking":[-[],"thinking":{"type":"enabled"}]}`, thinkT),
		c("thinking 是合法数组：取不到 type", `{"max_tokens":1,"top_p":0,"thinking":[{"type":"enabled"}],`+msgs+`}`, ""),
		c("thinking 数组元素里的 thinking：合法 JSON 取不到", `{"max_tokens":1,"temperature":0,"thinking":[{"thinking":{"type":"enabled"}}],`+msgs+`}`, ""),
	}
}()

func TestValidateStrictOpusMessagesRequest(t *testing.T) {
	for _, tt := range append(append([]strictOpusCase{}, strictOpusTableCases...), strictOpusSpecCases...) {
		t.Run(tt.name, func(t *testing.T) {
			message, ok := ValidateStrictOpusMessagesRequest(tt.model, []byte(tt.body))
			require.Equal(t, tt.ok, ok)
			require.Equal(t, tt.message, message)
		})
	}
}

// validateStrictOpusMessagesRequestLegacy 是单遍扫描出现之前的实现，逐字保留，作为差分测试的基准。
// 不要修改它；行为要变就同时改生产实现并更新期望。
func validateStrictOpusMessagesRequestLegacy(model string, body []byte) (string, bool) {
	if !claude.IsOpus55(model) {
		return "", true
	}
	if len(body) == 0 {
		return "field messages is required", false
	}

	messages := gjson.GetBytes(body, "messages")
	if !messages.IsArray() || len(messages.Array()) == 0 {
		return "field messages is required", false
	}

	maxTokens := gjson.GetBytes(body, "max_tokens")
	if !maxTokens.Exists() || maxTokens.Type != gjson.Number || maxTokens.Float() < 1 {
		return "max_tokens: Field required", false
	}

	if temperature := gjson.GetBytes(body, "temperature"); temperature.Exists() {
		if temperature.Type != gjson.Number || temperature.Float() < 0 || temperature.Float() > 1 {
			return "temperature: range: 0..1", false
		}
	}

	for i, message := range messages.Array() {
		content := message.Get("content")
		if !content.IsArray() {
			continue
		}
		for j, block := range content.Array() {
			if block.Get("type").String() != "text" {
				continue
			}
			// text 缺失、非字符串、或只有空白都属于同一类：Anthropic 拒收空文本块。
			if strings.TrimSpace(block.Get("text").String()) == "" {
				return fmt.Sprintf(
					"messages.%d.content.%d.text: text content blocks must contain non-whitespace text", i, j), false
			}
		}
	}

	// 开启 thinking 时不允许同时指定非默认采样参数。这里只做拒绝，不改写请求。
	if thinkingType := strings.TrimSpace(gjson.GetBytes(body, "thinking.type").String()); thinkingType == "enabled" || thinkingType == "adaptive" {
		if temperature := gjson.GetBytes(body, "temperature"); temperature.Exists() && temperature.Float() != 1 {
			return "temperature may only be set to 1 when thinking is enabled", false
		}
		if topP := gjson.GetBytes(body, "top_p"); topP.Exists() && topP.Float() != 1 {
			return "top_p may only be set to 1 when thinking is enabled", false
		}
		if topK := gjson.GetBytes(body, "top_k"); topK.Exists() && topK.Float() != 0 {
			return "top_k may only be set to 0 when thinking is enabled", false
		}
	}

	return "", true
}

// ---- 差分统计 ----

type strictOpusStats struct {
	total, scanned, failures int
	verdicts                 map[string]int // 单遍扫描直接给出结论的次数，按结论归类
	groups                   []strictOpusGroup
	markTotal, markScanned   int
}

// strictOpusGroup 是一组语料的规模：请求体个数，以及其中由单遍扫描直接作答的个数。
type strictOpusGroup struct {
	name           string
	total, scanned int
}

func newStrictOpusStats() *strictOpusStats {
	return &strictOpusStats{verdicts: map[string]int{}}
}

// cut 结束当前一组语料并记下它的规模。
func (st *strictOpusStats) cut(name string) {
	st.groups = append(st.groups, strictOpusGroup{name, st.total - st.markTotal, st.scanned - st.markScanned})
	st.markTotal, st.markScanned = st.total, st.scanned
}

func (st *strictOpusStats) summary() string {
	var sb strings.Builder
	for _, g := range st.groups {
		fmt.Fprintf(&sb, "\n  %s: %d 个（其中单遍扫描直接作答 %d 个）", g.name, g.total, g.scanned)
	}
	return sb.String()
}

func strictOpusVerdictKey(msg string, ok bool) string {
	switch {
	case ok:
		return "pass"
	case strings.HasPrefix(msg, "messages."):
		return "text"
	default:
		return msg
	}
}

func strictOpusClip(body []byte) string {
	if len(body) > 700 {
		return fmt.Sprintf("%q...", body[:700])
	}
	return fmt.Sprintf("%q", body)
}

// check 断言新旧实现对同一输入的 (文案, ok) 完全相同，并统计有多少输入由单遍扫描直接作答。
func (st *strictOpusStats) check(t *testing.T, model string, body []byte) {
	t.Helper()
	st.total++
	got, gotOK := ValidateStrictOpusMessagesRequest(model, body)
	want, wantOK := validateStrictOpusMessagesRequestLegacy(model, body)
	if got != want || gotOK != wantOK {
		st.failures++
		if st.failures <= 8 {
			t.Errorf("新旧实现结果不一致\n body(%d 字节)=%s\n 新: %q %v\n 旧: %q %v",
				len(body), strictOpusClip(body), got, gotOK, want, wantOK)
		}
		if st.failures == 8 {
			t.Fatalf("新旧实现已出现 8 处不一致，提前终止（已处理 %d 个请求体）", st.total)
		}
		return
	}
	if !claude.IsOpus55(model) || len(body) == 0 {
		return
	}
	if msg, ok, done := validateStrictOpusMessagesScan(body); done {
		st.scanned++
		st.verdicts[strictOpusVerdictKey(msg, ok)]++
	}
}

func (st *strictOpusStats) requireCovered(t *testing.T, atLeast int, keys ...string) {
	t.Helper()
	for _, k := range keys {
		require.GreaterOrEqual(t, st.verdicts[k], atLeast, "语料里单遍扫描直接给出「%s」结论的次数太少，覆盖不足", k)
	}
}

// ---- 手写边界 ----

func strictOpusEdgeBodies() []string {
	const msgs = `"messages":[{"role":"user","content":"hi"}]`
	valid := `{"model":"claude-opus-5-5","max_tokens":16,` + msgs + `}`
	out := []string{
		``, ` `, `null`, `[]`, `{}`, `"x"`, `1`, `true`, `{`, `}`, `[`, `{"messages"`, `{"messages":`,
		`{"messages":[]}`, `{"messages":[ ]}`, `{"messages":[,]}`, `{"messages":[1]}`, `{"messages":"x"}`,
		`{"messages":{}}`, `{"messages":null}`, `{"messages":[{}],"max_tokens":1}`, `{"messages":[null],"max_tokens":1}`,
		`{"messages":[[]],"max_tokens":1}`, `{"messages":["x"],"max_tokens":1}`, `{"messages":[{}]}`,
		valid,
		// 信封：BOM、前缀垃圾、尾部垃圾、截断。
		"\xef\xbb\xbf" + valid, "xx" + valid, ";" + valid, "\x00" + valid, " \n\t" + valid, valid + "garbage", valid + "}", valid + ",{}",
		valid + "\x00", "[" + valid + "]", `["` + valid + `"]`,
		// 非标准：单引号、无引号键、注释、尾逗号、缺逗号、缺冒号。
		`{messages:[],max_tokens:1}`, `{'messages':[{'role':'user','content':'hi'}],'max_tokens':1}`,
		`{"max_tokens":1,"messages":[{"role":"user","content":"hi"},]}`, `{"max_tokens":1,"messages":[{"role":"user","content":"hi"}],}`,
		`{"max_tokens":1 "messages":[{"role":"user","content":"hi"}]}`, `{"max_tokens" 1,"messages":[{"role":"user","content":"hi"}]}`,
		`{"max_tokens":1,,"messages":[{"role":"user","content":"hi"}]}`, `{"max_tokens":1,"messages":[{"role":"user" "content":"hi"}]}`,
		`{"max_tokens":1,"messages":[{"role":"user","content":[{"type":"text","text":"x"} {"type":"text","text":""}]}]}`,
		`{"max_tokens":1,"messages":[{"role":"user","content":[{"type":"text","text":""},,{"type":"text","text":"x"}]}]}`,
		`{"max_tokens":1,"messages":[{"role":"user","content":[{"type":"text","text":"x"}]}]} // c`,
		`/*c*/{"max_tokens":1,"messages":[{"role":"user","content":"hi"}]}`,
		// 括号：圆括号在 gjson 的跳过规则里也计深度。
		`{"max_tokens":1,"x":[(],"messages":[{"role":"user","content":"hi"}]}`, `{"max_tokens":1,"x":(,"messages":[{"role":"user","content":"hi"}]}`,
		`{"max_tokens":1,"x":[}, "messages":[{"role":"user","content":"hi"}]}`, `{"max_tokens":1,"x":{], "messages":[{"role":"user","content":"hi"}]}`,
		`{"max_tokens":1,"x":[)],"messages":[{"role":"user","content":"hi"}]}`, `{"max_tokens":1,"x":"(","messages":[{"role":"user","content":"hi"}]}`,
		`{"x":[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[1]]]]]]]]]]]]]]]]]]]]]]]]]]]]]],"max_tokens":1,` + msgs + `}`,
		`{"x":{"a":{"b":{"c":{"d":{"messages":[],"max_tokens":0}}}}},"max_tokens":1,` + msgs + `}`,
		// 数字：标准与非标准写法。
		`{"max_tokens":01,` + msgs + `}`, `{"max_tokens":+1,` + msgs + `}`, `{"max_tokens":1.,` + msgs + `}`, `{"max_tokens":.5,` + msgs + `}`,
		`{"max_tokens":NaN,` + msgs + `}`, `{"max_tokens":Infinity,` + msgs + `}`, `{"max_tokens":0x10,` + msgs + `}`, `{"max_tokens":1abc,` + msgs + `}`,
		`{"max_tokens":-,` + msgs + `}`, `{"max_tokens":1e,` + msgs + `}`, `{"max_tokens":1e+,` + msgs + `}`, `{"max_tokens":--1,` + msgs + `}`,
		`{"max_tokens":1 2,` + msgs + `}`, `{"max_tokens":tru,` + msgs + `}`, `{"max_tokens":True,` + msgs + `}`, `{"max_tokens":nul,` + msgs + `}`,
		`{"max_tokens":nan,` + msgs + `}`, `{"max_tokens":nullx,` + msgs + `}`, `{"max_tokens":truex,` + msgs + `}`, `{"max_tokens":null,` + msgs + `}`,
		`{"max_tokens":1,"temperature":NaN,` + msgs + `}`, `{"max_tokens":1,"temperature":nan,` + msgs + `}`, `{"max_tokens":1,"temperature":Infinity,` + msgs + `}`,
		`{"max_tokens":1,"temperature":-Infinity,` + msgs + `}`, `{"max_tokens":1,"temperature":+0.5,` + msgs + `}`,
		// 严格的边界值（任务点名）。
		`{"max_tokens":1,"temperature":0,` + msgs + `}`, `{"max_tokens":1,"temperature":1,` + msgs + `}`, `{"max_tokens":1,"temperature":1.0,` + msgs + `}`,
		`{"max_tokens":1,"temperature":1.0000001,` + msgs + `}`, `{"max_tokens":1,"temperature":-0,` + msgs + `}`, `{"max_tokens":1,"temperature":-0.0,` + msgs + `}`,
		`{"max_tokens":1,"temperature":"0.5",` + msgs + `}`, `{"max_tokens":1,"temperature":-0.0000001,` + msgs + `}`, `{"max_tokens":1,"temperature":0.9999999999999999,` + msgs + `}`,
		`{"max_tokens":1,"temperature":1.0000000000000001,` + msgs + `}`, `{"max_tokens":1,"temperature":1e-400,` + msgs + `}`, `{"max_tokens":1,"temperature":1e400,` + msgs + `}`,
		`{"max_tokens":0,` + msgs + `}`, `{"max_tokens":0.5,` + msgs + `}`, `{"max_tokens":0.9999999999999999,` + msgs + `}`, `{"max_tokens":1,` + msgs + `}`,
		`{"max_tokens":"1",` + msgs + `}`, `{"max_tokens":1.5,` + msgs + `}`, `{"max_tokens":-1,` + msgs + `}`, `{"max_tokens":1e400,` + msgs + `}`,
		`{"max_tokens":99999999999999999999999999,` + msgs + `}`, `{"max_tokens":true,` + msgs + `}`, `{"max_tokens":[1],` + msgs + `}`,
		`{"max_tokens":1,"thinking":{"type":"enabled"},"top_k":0,` + msgs + `}`, `{"max_tokens":1,"thinking":{"type":"enabled"},"top_k":1,` + msgs + `}`,
		`{"max_tokens":1,"thinking":{"type":"enabled"},"top_k":0.0,` + msgs + `}`, `{"max_tokens":1,"thinking":{"type":"enabled"},"top_k":-0,` + msgs + `}`,
		`{"max_tokens":1,"thinking":{"type":"Enabled"},"top_k":1,` + msgs + `}`, `{"max_tokens":1,"thinking":{"type":" enabled\t"},"top_k":1,` + msgs + `}`,
		`{"max_tokens":1,"thinking":{"type":"\u0065nabled"},"top_k":1,` + msgs + `}`, `{"max_tokens":1,"thinking":{"type":"adaptive "},"top_p":0.2,` + msgs + `}`,
		"{\"max_tokens\":1,\"thinking\":{\"type\":\"enabled\n\"},\"top_p\":0.2," + msgs + "}", "{\"max_tokens\":1,\"thinking\":{\"type\":\"\u00a0enabled\"},\"top_p\":0.2," + msgs + "}",
		`{"max_tokens":1,"thinking":{"type":"enabled"},"top_p":"1",` + msgs + `}`, `{"max_tokens":1,"thinking":{"type":"enabled"},"top_p":"\u0031",` + msgs + `}`,
		`{"max_tokens":1,"thinking":{"type":"enabled"},"top_p":true,` + msgs + `}`, `{"max_tokens":1,"thinking":{"type":"enabled"},"top_k":true,` + msgs + `}`,
		`{"max_tokens":1,"thinking":{"type":"enabled"},"top_p":null,` + msgs + `}`, `{"max_tokens":1,"thinking":{"type":"enabled"},"top_k":null,` + msgs + `}`,
		`{"max_tokens":1,"thinking":{"type":"enabled"},"top_k":[],` + msgs + `}`, `{"max_tokens":1,"thinking":{"type":"enabled"},"top_k":{},` + msgs + `}`,
		`{"max_tokens":1,"thinking":{"type":"enabled"},"top_p":"abc",` + msgs + `}`, `{"max_tokens":1,"thinking":{"type":"enabled"},"top_k":"0",` + msgs + `}`,
		// thinking 的各种形状。
		`{"max_tokens":1,"temperature":0.5,"thinking":[{"type":"enabled"}],` + msgs + `}`, `{"max_tokens":1,"temperature":0.5,"thinking":"enabled",` + msgs + `}`,
		`{"max_tokens":1,"temperature":0.5,"thinking":{"type":"enabled"},"thinking":{"type":"disabled"},` + msgs + `}`,
		`{"max_tokens":1,"temperature":0.5,"thinking":{},"thinking":{"type":"enabled"},` + msgs + `}`,
		`{"max_tokens":1,"temperature":0.5,"thinking":{"type":"enabled","type":"disabled"},` + msgs + `}`,
		`{"max_tokens":1,"temperature":0.5,"thinking":{"ty\u0070e":"enabled"},` + msgs + `}`, `{"max_tokens":1,"temperature":0.5,"thinking":{"type":"enabled"` + `,` + msgs + `}`,
		`{"max_tokens":1,"temperature":0.5,"thinking":{"nested":{"type":"enabled"}},` + msgs + `}`, `{"max_tokens":1,"temperature":0.5,"thinking":{"type":["enabled"]},` + msgs + `}`,
		`{"max_tokens":1,"temperature":0.5,"thinking.type":"enabled",` + msgs + `}`, `{"max_tokens":1,"temperature":0.5,"thinking":{"type":"enabled"}}`,
		// 顶层键：重复、转义、嵌套同名。
		`{"max_tokens":1,"max_tokens":0,` + msgs + `}`, `{"max_tokens":0,"max_tokens":1,` + msgs + `}`, `{"max\u005ftokens":1,` + msgs + `}`,
		`{"max_tokens":1,"mess\u0061ges":[{"role":"user","content":"hi"}]}`, `{"max_tokens":1,` + msgs + `,"messages":[]}`, `{"max_tokens":1,"messages":[],` + msgs + `}`,
		`{"max_tokens":1,"metadata":{"messages":[],"max_tokens":0},` + msgs + `}`, `{"metadata":{"max_tokens":1},` + msgs + `}`,
		`{"max_tokens":1,"temperature":0.5,"temperature":5,` + msgs + `}`, `{"max_tokens":1,"temperature":5,"temperature":0.5,` + msgs + `}`,
		// 元素形状。
		`{"max_tokens":1,"messages":[{"role":"user","content":[{"type":"text","text":""}]},{"role":"user","content":[{"type":"text","text":"x"}]}]}`,
		`{"max_tokens":1,"messages":[{"role":"user","content":[{"type":"text","text":"x"},{"type":"text","text":" "}]}]}`,
		`{"max_tokens":1,"messages":[{"role":"user","content":[{"type":"image"},{"type":"text","text":"x"},{"type":"text"}]},{"content":[{"type":"text","text":"\n"}]}]}`,
		`{"max_tokens":1,"messages":[{"role":"user","content":[{"text":"","type":"text"}]}]}`, `{"max_tokens":1,"messages":[{"role":"user","content":[{"type":"text","text":null}]}]}`,
		`{"max_tokens":1,"messages":[{"role":"user","content":[{"type":"text","text":"x","text":""}]}]}`, `{"max_tokens":1,"messages":[{"role":"user","content":[{"type":"text","text":"","text":"x"}]}]}`,
		`{"max_tokens":1,"messages":[{"role":"user","content":[{"type":"image","type":"text","text":""}]}]}`, `{"max_tokens":1,"messages":[{"role":"user","content":[{"type":"text","type":"image","text":""}]}]}`,
		`{"max_tokens":1,"messages":[{"role":"user","content":"x","content":[{"type":"text","text":""}]}]}`, `{"max_tokens":1,"messages":[{"role":"user","content":[{"type":"text","text":""}],"content":"x"}]}`,
		`{"max_tokens":1,"messages":[{"role":"user","content":[{"type":"tool_result","content":[{"type":"text","text":""}]}]}]}`,
		`{"max_tokens":1,"messages":[{"role":"user","content":[{"type":"text","t\u0065xt":""}]}]}`, `{"max_tokens":1,"messages":[{"role":"user","content":[{"t\u0079pe":"text","text":""}]}]}`,
		`{"max_tokens":1,"messages":[{"role":"user","cont\u0065nt":[{"type":"text","text":""}]}]}`, `{"max_tokens":1,"messages":[{"role":"user","content":[{"type":"te\u0078t","text":""}]}]}`,
		`{"max_tokens":1,"messages":[{"role":"user","content":["x",{"type":"text","text":""}]}]}`, `{"max_tokens":1,"messages":[{"role":"user","content":[1,{"type":"text","text":""}]}]}`,
		`{"max_tokens":1,"messages":["{\"content\":[{\"type\":\"text\",\"text\":\"\"}]}"]}`, `{"max_tokens":1,"messages":[[{"content":[{"type":"text","text":""}]}]]}`,
		`{"max_tokens":1,"messages":[{"role":"user","content":["{\"type\":\"text\",\"text\":\"\"}"]}]}`,
		`{"max_tokens":1,"messages":[{"role":"user","content":[{"type":"text","text":"\"}]}"},{"type":"text","text":""}]}]}`,
		// 文本里的转义、Unicode 空白、原始控制字符、非法 UTF-8。
		"{\"max_tokens\":1,\"messages\":[{\"role\":\"user\",\"content\":[{\"type\":\"text\",\"text\":\"\u00a0\"}]}]}",
		"{\"max_tokens\":1,\"messages\":[{\"role\":\"user\",\"content\":[{\"type\":\"text\",\"text\":\" \u2003\u3000\u0085\u2028 \"}]}]}",
		"{\"max_tokens\":1,\"messages\":[{\"role\":\"user\",\"content\":[{\"type\":\"text\",\"text\":\"\u200b\"}]}]}",
		"{\"max_tokens\":1,\"messages\":[{\"role\":\"user\",\"content\":[{\"type\":\"text\",\"text\":\"\ufeff\"}]}]}",
		"{\"max_tokens\":1,\"messages\":[{\"role\":\"user\",\"content\":[{\"type\":\"text\",\"text\":\"\t\"}]}]}",
		"{\"max_tokens\":1,\"messages\":[{\"role\":\"user\",\"content\":[{\"type\":\"text\",\"text\":\"\n\n\"}]}]}",
		"{\"max_tokens\":1,\"messages\":[{\"role\":\"user\",\"content\":[{\"type\":\"text\",\"text\":\"\t\\u0041\"}]}]}",
		"{\"max_tokens\":1,\"messages\":[{\"role\":\"user\",\"content\":[{\"type\":\"text\",\"text\":\"\\u0041\t\"}]}]}",
		"{\"max_tokens\":1,\"messages\":[{\"role\":\"user\",\"content\":[{\"type\":\"text\",\"text\":\" \\n\"}]}]}",
		"{\"max_tokens\":1,\"messages\":[{\"role\":\"user\",\"content\":[{\"type\":\"text\",\"text\":\"\xff\"}]}]}",
		"{\"max_tokens\":1,\"messages\":[{\"role\":\"user\",\"content\":[{\"type\":\"text\",\"text\":\" \xc2\"}]}]}",
		"{\"max_tokens\":1,\"messages\":[{\"role\":\"user\",\"content\":[{\"type\":\"text\",\"text\":\"\xc2\xa0\xc2\"}]}]}",
		"{\"max_tokens\":1,\"messages\":[{\"role\":\"user\",\"content\":[{\"type\":\"text\",\"text\":\"\x00\"}]}]}",
		`{"max_tokens":1,"messages":[{"role":"user","content":[{"type":"text","text":"\u0020"}]}]}`,
		`{"max_tokens":1,"messages":[{"role":"user","content":[{"type":"text","text":"\u00a0"}]}]}`,
		`{"max_tokens":1,"messages":[{"role":"user","content":[{"type":"text","text":"\u200b"}]}]}`,
		`{"max_tokens":1,"messages":[{"role":"user","content":[{"type":"text","text":"\n\t\r \f\u000b"}]}]}`,
		`{"max_tokens":1,"messages":[{"role":"user","content":[{"type":"text","text":"\b"}]}]}`,
		`{"max_tokens":1,"messages":[{"role":"user","content":[{"type":"text","text":"\u0000"}]}]}`,
		`{"max_tokens":1,"messages":[{"role":"user","content":[{"type":"text","text":"\x"}]}]}`,
		`{"max_tokens":1,"messages":[{"role":"user","content":[{"type":"text","text":"\u12"}]}]}`,
		`{"max_tokens":1,"messages":[{"role":"user","content":[{"type":"text","text":"\uZZZZ"}]}]}`,
		`{"max_tokens":1,"messages":[{"role":"user","content":[{"type":"text","text":"\ud800"}]}]}`,
		`{"max_tokens":1,"messages":[{"role":"user","content":[{"type":"text","text":" \ud83d\ude00"}]}]}`,
		`{"max_tokens":1,"messages":[{"role":"user","content":[{"type":"text","text":"\\"}]}]}`,
		`{"max_tokens":1,"messages":[{"role":"user","content":[{"type":"text","text":"a\\"},{"type":"text","text":""}]}]}`,
		`{"max_tokens":1,"messages":[{"role":"user","content":[{"type":"text","text":"\\\\"},{"type":"text","text":""}]}]}`,
		`{"max_tokens":1,"messages":[{"role":"user","content":[{"type":"text","text":"\\\""},{"type":"text","text":""}]}]}`,
		`{"max_tokens":1,"messages":[{"role":"user","content":[{"type":"text","text":"x\"},{\"type\":\"text\",\"text\":\"\"}"}]}]}`,
		`{"max_tokens":1,"messages":[{"role":"user","content":[{"type":"text","text":"x\"}`,
		`{"max_tokens":1,"messages":[{"role":"user","content":[{"type":"text","text":"x`,
		`{"max_tokens":1,"messages":[{"role":"user","content":[{"type":"text","text":"`,
	}
	// thinking 是数组：评审发现的畸形文本分歧（gjson 下钻数组，收尾位置与括号计深不同），一律退回 gjson。
	out = append(out,
		`{"messages":[{}],"max_tokens":1,"top_p":0,"thinking":[(],"thinking":{"type":"enabled"})}`,
		`{"messages":[{}],"max_tokens":1,"top_p":0,"thinking":[}}]"thinking":{"type":"enabled"}}`,
		`{"messages":[{}],"max_tokens":1,"temperature":0,"thinking":[-[],"thinking":{"type":"enabled"}]}`,
		`{"messages":[{}],"max_tokens":1,"top_k":1,"thinking":[{"thinking":{"type":"enabled"}}]}`,
		`{"messages":[{}],"max_tokens":1,"top_p":0,"thinking":[],"thinking":{"type":"enabled"}}`,
		`{"messages":[{}],"max_tokens":1,"top_p":0,"thinking":[1,2,3]}`,
		`{"thinking":[(],"thinking":{"type":"enabled"})}`,
		`{"messages":[{}],"max_tokens":1,"temperature":0,"temperature":[(],"temperature":0.5)}`,
	)
	// 深层嵌套的、被跳过的值：必须迭代跳过，不能栈溢出，边界与 gjson 一致。
	deep := strings.Repeat("[", 50000) + strings.Repeat("]", 50000)
	out = append(out,
		`{"x":`+deep+`,"max_tokens":1,`+msgs+`}`,
		`{"x":`+deep[:len(deep)-1]+`,"max_tokens":1,`+msgs+`}`, // 少一个收尾：吞掉后面所有字段
		`{"x":`+strings.Repeat(`{"a":`, 20000)+`1`+strings.Repeat(`}`, 20000)+`,"max_tokens":1,`+msgs+`}`,
	)
	// 截断：合法请求体的每个前缀。
	rich := `{"model":"claude-opus-5-5","max_tokens":16,"temperature":1,"thinking":{"type":"adaptive"},"messages":[{"role":"user","content":[{"type":"text","text":"a\"b"},{"type":"image","source":{"data":"x"}},{"type":"text","text":" "}]}]}`
	for i := 0; i <= len(rich); i++ {
		out = append(out, rich[:i])
	}
	// 键重复 / 乱序 / 空白：同一份内容换几种排版。
	out = append(out,
		"{\n  \"max_tokens\": 16,\n  \"messages\": [\n    {\n      \"role\": \"user\",\n      \"content\": [\n        {\n          \"type\": \"text\",\n          \"text\": \"hi\"\n        }\n      ]\n    }\n  ]\n}\n",
		"{\r\n\t\"messages\"\t:\t[\r\n\t\t{\"content\":[{\"text\":\"\",\"type\":\"text\"}]}\r\n\t]\r\n,\t\"max_tokens\"\t:\t1\r\n}",
		" { \"messages\" : [ { \"content\" : [ { \"type\" : \"text\" , \"text\" : \" \" } ] } ] , \"max_tokens\" : 1 } ",
	)
	return out
}

// strictOpusSweepSeeds 是做逐字节穷举变异的种子：覆盖各条校验规则的合法请求体。
func strictOpusSweepSeeds() []string {
	return []string{
		`{"model":"claude-opus-5-5","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`,
		`{"model":"claude-opus-5-5","messages":[{"role":"user","content":[{"type":"text","text":"a \"b\" \\ c\n"},{"type":"image","source":{"type":"base64","data":"QUJD"}},{"type":"text","text":" "}]}],"max_tokens":1}`,
		`{"model":"claude-opus-5-5","max_tokens":1200,"temperature":1,"top_p":1,"top_k":0,"thinking":{"type":"adaptive","budget_tokens":5},"messages":[{"role":"user","content":[{"type":"text","text":"x"}]}]}`,
		"{\n \"max_tokens\": 8,\n \"temperature\": 0.5,\n \"messages\": [\n  {\"role\": \"user\", \"content\": [\n   {\"type\": \"text\", \"text\": \"\\u0020\"}\n  ]}\n ]\n}",
		`{"max_tokens":3,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"t","name":"n","input":{"a":[1,2,{"b":"c"}]}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"t","content":[{"type":"text","text":""}]},{"type":"text","text":"ok","cache_control":{"type":"ephemeral"}}]}]}`,
		`{"model":"claude-opus-5-5","messages":[{"role":"user","content":"a","content":[{"type":"text","text":""}]},{"role":"user","content":[{"type":"text","type":"image","text":""},{"text":"y","type":"text","text":""}]}],"max_tokens":2,"stream":true,"system":[{"type":"text","text":"s"}],"tools":[{"name":"t","input_schema":{"type":"object"}}]}`,
		`{"max_tokens":5,"temperature":0,"thinking":{"type":"enabled"},"messages":[{"content":[{"type":"text","text":"\u00a0\n"}]}]}`,
		`{"max_tokens":1.5e0,"temperature":-0,"top_k":0,"thinking":{"budget_tokens":1,"type":"adaptive"},"top_p":"1","messages":[{"role":"user","content":[]}]}`,
		`{"messages":[{}],"max_tokens":1,"top_p":0,"thinking":[(],"thinking":{"type":"enabled"})}`,
		`{"messages":[{}],"max_tokens":1,"temperature":0,"thinking":[-[],"thinking":{"type":"enabled"}]}`,
	}
}

func TestValidateStrictOpusMessagesRequestMatchesGJSON(t *testing.T) {
	scale := 1
	if testing.Short() {
		scale = 10
	}
	st := newStrictOpusStats()

	// 1. 既有用例与逐条规则的补充用例（含非 Opus 5.5 与带厂商前缀的模型名）。
	for _, tt := range strictOpusTableCases {
		st.check(t, tt.model, []byte(tt.body))
	}
	for _, tt := range strictOpusSpecCases {
		st.check(t, tt.model, []byte(tt.body))
	}
	st.cut("既有用例 + 逐条规则用例")

	// 2. 手写边界。
	for _, body := range strictOpusEdgeBodies() {
		st.check(t, strictOpusModel, []byte(body))
	}
	st.cut("手写边界（含合法请求体的每个前缀）")

	// 3. 采样参数 × thinking 全组合（标准 JSON，固定合法的 messages 与 max_tokens）。
	if scale == 1 {
		for _, temp := range strictTemperatureStd {
			for _, think := range strictThinkingStd {
				for _, topP := range strictTopPStd {
					for _, topK := range strictTopKStd {
						body := `{"model":"claude-opus-5-5","max_tokens":16,"temperature":` + temp + `,"thinking":` + think +
							`,"top_p":` + topP + `,"top_k":` + topK + `,"messages":[{"role":"user","content":"hi"}]}`
						st.check(t, strictOpusModel, []byte(body))
					}
				}
			}
		}
	}
	st.cut("temperature x thinking x top_p x top_k 全组合")

	// 3b. thinking 是数组（含合法 JSON）：扫描器一律退回 gjson，这里验证退回后结果相同。
	if scale == 1 {
		for _, temp := range strictTemperatureStd {
			for _, think := range strictThinkingArrays {
				for _, topP := range strictTopPStd {
					for _, topK := range strictTopKStd {
						body := `{"model":"claude-opus-5-5","max_tokens":16,"temperature":` + temp + `,"thinking":` + think +
							`,"top_p":` + topP + `,"top_k":` + topK + `,"messages":[{"role":"user","content":"hi"}]}`
						st.check(t, strictOpusModel, []byte(body))
					}
				}
			}
		}
	}
	st.cut("thinking 为数组 x temperature x top_p x top_k 全组合（一律退回 gjson）")

	// 3c. thinking 数组陷阱全组合：数组里塞杂质括号并暗藏第二个 thinking（独立评审发现的那一类分歧）。
	if scale == 1 {
		heads := []string{
			`{"messages":[{}],"max_tokens":1,"top_p":0,"thinking":`,
			`{"messages":[{}],"max_tokens":1,"temperature":0,"thinking":`,
			`{"messages":[{}],"max_tokens":1,"top_k":1,"thinking":`,
			`{"messages":[{}],"max_tokens":1,"thinking":`,
		}
		for _, head := range heads {
			for _, inner := range strictTrapInner {
				for _, closer := range strictTrapCloser {
					for _, sep := range strictTrapSep {
						for _, hidden := range strictTrapHidden {
							for _, tail := range strictTrapTail {
								st.check(t, strictOpusModel, []byte(head+"["+inner+closer+sep+`"thinking":`+hidden+tail))
							}
						}
					}
				}
			}
		}
	}
	st.cut("thinking 数组陷阱全组合（4 个开头 x 17 x 7 x 4 x 6 x 8）")

	// 4. 对每个种子请求体做逐字节穷举变异：删除一个字节 / 替换为任一杂质字节 / 在任意位置插入杂质字节。
	junk := []byte("\"\\{}[](),: \n\t01-.etfnu\x00\xff\xc2\xa0")
	for _, seed := range strictOpusSweepSeeds() {
		for p := 0; p < len(seed); p++ {
			st.check(t, strictOpusModel, []byte(seed[:p]+seed[p+1:]))
			for _, c := range junk {
				st.check(t, strictOpusModel, []byte(seed[:p]+string([]byte{c})+seed[p+1:]))
				st.check(t, strictOpusModel, []byte(seed[:p]+string([]byte{c})+seed[p:]))
			}
		}
	}
	st.cut("种子请求体的逐字节穷举变异（删除 / 替换 / 插入）")

	// 5. 随机语料（固定种子）：标准 JSON 必须全部由单遍扫描作答；畸形/非标准写法按比例退回 gjson。
	std := newStrictGen(20261126, false)
	stdN := 60000 / scale
	for i := 0; i < stdN; i++ {
		st.check(t, strictOpusModel, []byte(std.body()))
	}
	st.cut("随机：标准 JSON")
	require.Equal(t, stdN, st.groups[len(st.groups)-1].scanned, "标准 JSON 请求体不应退回 gjson 实现")

	wild := newStrictGen(20261127, true)
	wildN := 100000 / scale
	for i := 0; i < wildN; i++ {
		st.check(t, strictOpusModel, []byte(wild.body()))
	}
	st.cut("随机：混入畸形/非标准/重复键/转义键名")
	wildScanned := st.groups[len(st.groups)-1].scanned

	mut := newStrictGen(20261128, true)
	mutStd := newStrictGen(20261129, false)
	mutN := 60000 / scale
	for i := 0; i < mutN; i++ {
		src := mutStd.body()
		if i%2 == 0 {
			src = mut.body()
		}
		st.check(t, strictOpusModel, []byte(mut.mutate(src)))
	}
	st.cut("随机：上述两类再做 1~3 次随机字节变异")

	t.Logf("差分语料共 %d 个请求体，新旧不一致 %d 个：%s\n扫描路径直接给出的结论分布 %v", st.total, st.failures, st.summary(), st.verdicts)
	require.Zero(t, st.failures)
	if scale == 1 {
		require.GreaterOrEqual(t, stdN+wildN+mutN, 200000, "随机语料不足 20 万")
	}
	// 防止扫描器整体退化成“全部退回 gjson”而差分测试照样通过：混合了 25% 畸形取值、8% 重复键、
	// 4% 非对象元素等写法的语料里，仍应有相当一部分由扫描器作答（标准 JSON 上面已断言 100%）。
	require.Greater(t, wildScanned*100, wildN*15, "畸形混合语料里由扫描器作答的比例过低")
	st.requireCovered(t, 20/scale, "pass", "text", "max_tokens: Field required", "temperature: range: 0..1", "field messages is required",
		"temperature may only be set to 1 when thinking is enabled", "top_p may only be set to 1 when thinking is enabled",
		"top_k may only be set to 0 when thinking is enabled")
}

// 扫描器的契约：哪些写法由它直接作答，哪些必须退回 gjson（done=false）。
func TestValidateStrictOpusMessagesScanContract(t *testing.T) {
	const tail = `"max_tokens":16,"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`
	handled := map[string]string{
		"标准请求":                    `{"model":"claude-opus-5-5",` + tail,
		"messages 在 max_tokens 前": `{"messages":[{"role":"user","content":"hi"}],"max_tokens":16}`,
		"美化排版":                    "{\n  \"max_tokens\": 16,\n  \"messages\": [\n    {\"content\": [ {\"type\": \"text\", \"text\": \"hi\"} ]}\n  ]\n}\n",
		"text 以转义开头（扫描器自己反转义）":     `{"max_tokens":16,"messages":[{"role":"user","content":[{"type":"text","text":"\n\nhello"}]}]}`,
		"text 以原始换行开头":             "{\"max_tokens\":16,\"messages\":[{\"role\":\"user\",\"content\":[{\"type\":\"text\",\"text\":\"\nhello\"}]}]}",
		"type 用转义写法（仅该块走 gjson）":   `{"max_tokens":16,"messages":[{"role":"user","content":[{"type":"te\u0078t","text":"hi"}]}]}`,
		"重复的 content/type/text 键":  `{"max_tokens":16,"messages":[{"content":"x","content":[]}],"x":{"messages":[],"max_tokens":0}}`,
		"thinking 开启且采样参数合规":       `{"thinking":{"type":"adaptive"},"temperature":1,"top_p":1,"top_k":0,` + tail,
		"尾部垃圾（gjson 不会读到）":         `{"model":"claude-opus-5-5",` + tail + `garbage`,
		"system/tools 里的括号引号":      `{"system":[{"type":"text","text":"}]\")("}],"tools":[{"a":"\\\\"}],` + tail,
		"空 content 数组与字符串 content": `{"max_tokens":16,"messages":[{"content":[]},{"content":"x"}]}`,
		"冒号与逗号两侧都有空白":              `{"max_tokens" : 16 , "messages" : [ { "content" : [ { "type" : "text" , "text" : "hi" } ] } ] }`,
		"CRLF 与制表符排版":              "{\r\n\t\"max_tokens\":\t16,\r\n\t\"messages\":\t[{\"content\":\t[{\"type\":\t\"text\",\t\"text\":\t\"hi\"}]}]\r\n}",
		"深层嵌套的被跳过值":                `{"x":` + strings.Repeat("[", 5000) + strings.Repeat("]", 5000) + `,` + tail,
		"数字写法：负号、小数、指数":            `{"max_tokens":1.6e1,"temperature":-0.0,"top_p":1E0,"top_k":0.0,"messages":[{"content":"x"}]}`,
		"thinking 是标量时仍由扫描器作答":     `{"thinking":"enabled","temperature":0.5,` + tail,
	}
	for name, body := range handled {
		t.Run("扫描器作答/"+name, func(t *testing.T) {
			_, _, done := validateStrictOpusMessagesScan([]byte(body))
			require.True(t, done)
		})
	}
	fallback := map[string]string{
		"UTF-8 BOM 前缀":       "\xef\xbb\xbf{" + tail,
		"前缀垃圾":               `xx{` + tail,
		"顶层是数组":              `[{` + tail + `]`,
		"顶层键含转义":             `{"max\u005ftokens":16,"messages":[{"content":"x"}]}`,
		"messages 重复":        `{"messages":[{"content":"x"}],` + tail,
		"max_tokens 重复":      `{"max_tokens":1,` + tail,
		"thinking 重复":        `{"thinking":{},"thinking":{"type":"enabled"},` + tail,
		"message 键含转义":       `{"max_tokens":16,"messages":[{"cont\u0065nt":[]}]}`,
		"block 键含转义":         `{"max_tokens":16,"messages":[{"content":[{"t\u0079pe":"text"}]}]}`,
		"messages 里有非对象元素":   `{"max_tokens":16,"messages":["x"]}`,
		"content 里有非对象元素":    `{"max_tokens":16,"messages":[{"content":["x"]}]}`,
		"数字前导零":              `{"max_tokens":016,"messages":[{"content":"x"}]}`,
		"NaN":                `{"max_tokens":16,"temperature":NaN,"messages":[{"content":"x"}]}`,
		"缺逗号":                `{"max_tokens":16 "messages":[{"content":"x"}]}`,
		"尾逗号":                `{"max_tokens":16,"messages":[{"content":"x"}],}`,
		"对象未闭合":              `{"max_tokens":16,"messages":[{"content":"x"}]`,
		"thinking.type 值含转义": `{"thinking":{"type":"en\u0061bled"},` + tail,
		"thinking 开启且 top_p 字符串含转义":         `{"thinking":{"type":"enabled"},"temperature":1,"top_p":"\u0031",` + tail,
		"thinking 对象写法不规范":                  `{"thinking":{"type":"enabled",},` + tail,
		"thinking 是数组（合法 JSON 也退回）":         `{"max_tokens":16,"thinking":[],"messages":[{"content":"x"}]}`,
		"thinking 数组暗藏第二个 thinking（括号错位）":   `{"messages":[{}],"max_tokens":1,"top_p":0,"thinking":[(],"thinking":{"type":"enabled"})}`,
		"thinking 数组暗藏第二个 thinking（} 被吞）":   `{"messages":[{}],"max_tokens":1,"top_p":0,"thinking":[}}]"thinking":{"type":"enabled"}}`,
		"thinking 数组暗藏第二个 thinking（数字吞掉 [）": `{"messages":[{}],"max_tokens":1,"temperature":0,"thinking":[-[],"thinking":{"type":"enabled"}]}`,
	}
	for name, body := range fallback {
		t.Run("退回 gjson/"+name, func(t *testing.T) {
			_, _, done := validateStrictOpusMessagesScan([]byte(body))
			require.False(t, done)
			// 退回之后仍然与旧实现一致。
			got, gotOK := ValidateStrictOpusMessagesRequest(strictOpusModel, []byte(body))
			want, wantOK := validateStrictOpusMessagesRequestLegacy(strictOpusModel, []byte(body))
			require.Equal(t, want, got)
			require.Equal(t, wantOK, gotOK)
		})
	}
}

// 正常请求走单遍扫描时不产生堆分配（旧实现每条消息都要分配 []Result 与反转义副本）。
func TestValidateStrictOpusMessagesRequestScanDoesNotAllocate(t *testing.T) {
	body := []byte(`{"model":"claude-opus-5-5","messages":[{"role":"user","content":[{"type":"text","text":"hello \"x\"\n"},` +
		`{"type":"image","source":{"data":"abc"}}]},{"role":"assistant","content":"ok"},{"role":"user","content":[{"type":"text","text":"hi"}]}],` +
		`"max_tokens":1024,"temperature":1,"thinking":{"type":"adaptive"},"top_p":1,"top_k":0}`)
	var message string
	var ok bool
	allocs := testing.AllocsPerRun(50, func() {
		message, ok = ValidateStrictOpusMessagesRequest(strictOpusModel, body)
	})
	require.True(t, ok, message)
	// 留一点余量：-race 下逃逸分析结论可能略有不同；回到“每条消息一次分配”会立刻超标。
	require.LessOrEqual(t, allocs, 1.0)
}

// opusTextBlank 对各类字符串内容的判定，同时与 gjson 的真实取值（反转义 + TrimSpace）逐条交叉校验。
func TestStrictOpusTextBlank(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want bool
	}{
		{"", true},
		{" ", true},
		{"     ", true},
		{"a", false},
		{"  a", false},
		{"\u00a0", true},
		{"\u00a0 \u2003\u3000", true},
		{" \u00a0x", false},
		{"\u200b", false},
		{"\ufeff", false},
		{"\xff", false},
		{"\xc2", false},
		{"\xc2\xa0\xc2", false},
		{"\xc2\xa0", true},
		{"\x7f", false},
		{"\x01", false},
		{"\t", true},            // 无反斜杠：值就是原文，制表符是空白
		{"\n \r", true},         // 同上
		{"\t\\u0041", true},     // 有反斜杠：unescape 在原始制表符处截断，得到空串
		{"\\u0041\t", false},    // 先输出 A
		{` \u0020\n\t\f`, true}, // 转义出来的空白
		{`\nx`, false},          // 每种转义空白后面接非空白字符：必须继续往后看，而不是当成截断
		{`\tx`, false},
		{`\rx`, false},
		{`\fx`, false},
		{`\u0020x`, false},
		{`\u00a0x`, false},
		{`\u00A0`, true}, // 十六进制大小写都认
		{`\u2028\u2029\u3000`, true},
		{`\u00a0\u2003`, true},
		{`\u200b`, false}, // 零宽空格不是空白
		{`\b`, false},     // 退格不是空白
		{`\"`, false},
		{`\\`, false},
		{`\/`, false},
		{`\x`, true}, // 未知转义：截断，输出为空
		{`  \x`, true},
		{`\u12`, true},    // \u 被截断
		{`\u002`, true},   // 同上（只有三位）
		{`\uZZZZ`, false}, // 非法十六进制得 0，输出 NUL
		{`\u0g20`, false}, // 非法十六进制整体得 0，而不是把合法的位拼起来
		{`\u+020`, false},
		{`\u 020`, false},
		{`\u0000`, false},
		{`\ud800`, false}, // 孤立代理项 -> U+FFFD
		{`\ud800\u0041`, false},
		{`\ud83d\ude00`, false},
		{`\u0020\ud800`, false},
		{` \u0020 `, true},
		{`\n\u4f60`, false}, // 中文被转义成 \uXXXX
		{`\u4f60\u597d`, false},
		{"é", false},
	} {
		doc := []byte(`{"text":"` + tt.in + `"}`)
		viaGJSON := strings.TrimSpace(gjson.GetBytes(doc, "text").String()) == ""
		require.Equal(t, tt.want, viaGJSON, "测试期望与 gjson 真实行为不符: %q", tt.in)
		require.Equal(t, tt.want, opusTextBlank([]byte(tt.in)), "%q", tt.in)
	}
}

// ---- 随机语料生成 ----

type strictGen struct {
	r    *rand.Rand
	wild bool // true 时混入畸形、非标准、重复键、转义键等写法
}

func newStrictGen(seed int64, wild bool) *strictGen {
	return &strictGen{r: rand.New(rand.NewSource(seed)), wild: wild}
}

func (g *strictGen) n(k int) int            { return g.r.Intn(k) }
func (g *strictGen) pct(p int) bool         { return g.r.Intn(100) < p }
func (g *strictGen) pick(v []string) string { return v[g.r.Intn(len(v))] }

func (g *strictGen) ws() string {
	if g.n(6) != 0 {
		return ""
	}
	return g.pick([]string{" ", "\n", "\t", "\r\n", "  \n  ", "\n\t"})
}

func (g *strictGen) member(k, v string) string { return `"` + k + `"` + g.ws() + ":" + g.ws() + v }

func (g *strictGen) object(members []string) string {
	if g.pct(30) {
		g.r.Shuffle(len(members), func(i, j int) { members[i], members[j] = members[j], members[i] })
	}
	return "{" + g.ws() + strings.Join(members, g.ws()+","+g.ws()) + g.ws() + "}"
}

func (g *strictGen) array(items []string) string {
	return "[" + g.ws() + strings.Join(items, g.ws()+","+g.ws()) + g.ws() + "]"
}

// 字符串内容（引号之间的原始文本）。
var strictTextStd = []string{
	"hello", "A bat and a ball cost $1.10 total.", "x", "0", "false", "null", "{}", "[]", "你好，世界", "emoji \U0001F600",
	"", " ", "   ", "                                        ",
	"\u00a0", "\u00a0\u00a0", " \u00a0 ", "\u2003", "\u3000", "\u0085", "\u2028", "\u1680", "\u202f", "\u205f", "\u2000",
	"\u200b", "\ufeff", " \u00a0x", "\u00a0\u200b", "  hello  ",
	`\n`, `\t`, `\r`, `\r\n`, `\n\n  \t`, `\u0020`, `\u0020\u0020`, `\u00a0`, `\u2003`, `\u000a`, `\u0009`, `\u000b`, `\f`, `\b`, `\u0008`,
	`\n x`, `  \nhello`, `\"`, `\"quoted\" text`, `\\`, `a\\`, `\\\\`, `\\\"`, `\/`, `\u0041`, `\ud83d\ude00`, `\n\u00a0\n`, ` \u0020 \t`,
	`\u0000`, `\u001f`, "你好", `\nx`, `\tx`, `\rx`, `\fx`, `\u4f60\u597d`, "\u00a0x", "x\u00a0", "\u3000 \u00a0",
}

var strictTextWild = []string{
	"\t", "\n", "\r\n", " \t ", "\x01", "\x00", "\x7f", "\t\\u0041", "\\u0041\t", " \n\\n", "a\nb", "\tx", "\nabc", " \x01 ", "\r\n\r\n",
	"\xff", "\xc2", "\xe2\x80", " \xc2", "\xc2\xa0\xc2", "\xc2\xa0\xc2\xa0 ",
	`\x`, `\u12`, `\uZZZZ`, `\ud800`, `\ud800x`, `\udc00`, `\u00zz`, `\q`, `\ud800\u0041`, `\u+123`, ` \u0020\q`,
	`\u0g20`, `\u+020`, `\u 020`, `\u002`, ` \u0g20x`, `  \u0g20`,
}

func (g *strictGen) textInner() string {
	switch w := g.n(100); {
	case w < 8:
		return strings.Repeat(g.pick(strictTextStd), 1+g.n(40))
	case w < 14:
		return strings.Repeat("lorem ipsum \\\"dolor\\\" sit amet\\n", 1+g.n(120))
	case w < 17:
		return strings.Repeat(" ", 1+g.n(300)) + g.pick([]string{"", "", "x", `\n`, "\u00a0", `\u0020`})
	case g.wild && w < 40:
		return g.pick(strictTextWild)
	default:
		return g.pick(strictTextStd)
	}
}

var strictTypeVariants = []string{
	`"Text"`, `"text "`, `" text"`, `"TEXT"`, `null`, `1`, `true`, `false`, `{}`, `[]`, `{"a":"text"}`, `["text"]`, `""`, `"tex"`, `"texts"`,
	`"te\u0078t"`, `"\u0074ext"`, `"text\u0000"`, `"tex\u0074"`,
}

var strictTextValueVariants = []string{
	`null`, `0`, `1.5`, `true`, `false`, `{}`, `{"a":""}`, `[]`, `[""]`, `""`, `" "`, `"\n"`, `-0`, `1e2`,
}

func (g *strictGen) block() string {
	text := func() string { return g.member("text", `"`+g.textInner()+`"`) }
	if g.wild && g.pct(5) {
		return g.pick([]string{`"abc"`, `1`, `null`, `[]`, `true`, `"{\"type\":\"text\",\"text\":\"\"}"`,
			`[{"type":"text","text":""}]`, `"{\"type\":\"text\"}"`})
	}
	var m []string
	switch w := g.n(100); {
	case w < 56:
		m = []string{g.member("type", `"text"`), text()}
	case w < 64:
		switch g.n(5) {
		case 0:
			m = []string{g.member("type", `"image"`), g.member("source", `{"type":"base64","media_type":"image/png","data":"iVBORw0KGgo="}`)}
		case 1:
			m = []string{g.member("type", `"tool_use"`), g.member("id", `"toolu_1"`), g.member("name", `"bash"`),
				g.member("input", `{"command":"ls \"a b\"","text":""}`)}
		case 2:
			m = []string{g.member("type", `"tool_result"`), g.member("tool_use_id", `"toolu_1"`),
				g.member("content", g.pick([]string{`"ok"`, `[{"type":"text","text":""}]`, `[]`}))}
		case 3:
			m = []string{g.member("type", `"thinking"`), g.member("thinking", `""`), g.member("signature", `"sig"`)}
		default:
			m = []string{g.member("type", `"redacted_thinking"`), g.member("data", `"x"`)}
		}
		if g.pct(25) {
			m = append(m, text()) // 非 text 块上的空 text 字段不应报错
		}
	case w < 70:
		m = []string{g.member("type", g.pick(strictTypeVariants)), text()}
	case w < 74:
		m = []string{text()}
	case w < 80:
		m = []string{g.member("type", `"text"`), g.member("text", g.pick(strictTextValueVariants))}
	case w < 85:
		// 重复键：首个胜出。
		m = []string{g.member("type", `"text"`), text(), g.member("text", g.pick([]string{`""`, `"x"`, `null`})),
			g.member("type", g.pick([]string{`"image"`, `"text"`}))}
	case w < 88 && g.wild:
		// 转义的键名：gjson 会先反转义再比较。
		k1 := g.pick([]string{`t\u0079pe`, `\u0074ype`, `type\u0000`, `type`})
		k2 := g.pick([]string{`te\u0078t`, `\u0074ext`, `text`})
		m = []string{g.member(k1, `"text"`), g.member(k2, `"`+g.textInner()+`"`)}
	default:
		m = []string{g.member("type", `"text"`), text(), g.member("cache_control", `{"type":"ephemeral"}`)}
	}
	return g.object(m)
}

func (g *strictGen) content() string {
	switch w := g.n(100); {
	case w < 28:
		return `"` + g.textInner() + `"`
	case w < 84:
		var n int
		switch k := g.n(100); {
		case k < 8:
			n = 0
		case k < 48:
			n = 1
		case k < 88:
			n = 2 + g.n(3)
		default:
			n = 5 + g.n(8)
		}
		items := make([]string, n)
		for i := range items {
			items[i] = g.block()
		}
		return g.array(items)
	case w < 88:
		return "null"
	case w < 91:
		return "42"
	case w < 94:
		return `{"type":"text","text":""}`
	case w < 96:
		return "true"
	default:
		return g.pick([]string{`[]`, ` [ ] `, `"[{\"type\":\"text\",\"text\":\"\"}]"`})
	}
}

func (g *strictGen) message() string {
	if g.wild && g.pct(4) {
		return g.pick([]string{`"hi"`, `1`, `null`, `[]`, `true`, `[{"type":"text","text":""}]`,
			`"{\"content\":[{\"type\":\"text\",\"text\":\"\"}]}"`})
	}
	m := []string{g.member("role", g.pick([]string{`"user"`, `"assistant"`}))}
	switch w := g.n(100); {
	case w < 4: // 缺 content
	case w < 8: // 重复 content：首个胜出
		m = append(m, g.member("content", g.content()), g.member("content", g.content()))
	default:
		m = append(m, g.member("content", g.content()))
	}
	if g.pct(10) {
		m = append(m, g.member("cache_control", `{"type":"ephemeral"}`))
	}
	if g.wild && g.pct(3) {
		m = append(m, g.member(`cont\u0065nt`, g.content()))
	}
	return g.object(m)
}

func (g *strictGen) messages() string {
	var n int
	switch w := g.n(1000); {
	case w < 30:
		n = 0
	case w < 330:
		n = 1
	case w < 780:
		n = 2 + g.n(4)
	case w < 995:
		n = 6 + g.n(25)
	default:
		n = 60 + g.n(300)
	}
	items := make([]string, n)
	for i := range items {
		items[i] = g.message()
	}
	return g.array(items)
}

var strictNoiseScalars = []string{
	`1`, `-2.5e3`, `true`, `false`, `null`, `{}`, `[]`,
	`"}"`, `"]"`, `"{"`, `"["`, `"("`, `")"`, `"\""`, `"\\"`, `"a\\\"b"`, `"\\\\"`, `"x\ny"`, `"\u0022"`, `"messages"`,
	`"max_tokens"`, `"text"`, `""`, `"}]}\"{[("`,
}

var strictNoiseKeys = []string{"messages", "max_tokens", "type", "text", "content", "thinking", "temperature", "top_p", "top_k", "a", "b c", `x\"y`}

var strictNoiseOdd = []string{`[(]`, `[)]`, `{(}`, `[1,(2]`, `[,]`, `{,}`, `[1 2]`, `{"a" 1}`, `[}`, `{]`, `[[}`, `[{)]`, `(`, `)`, `[ ( ) ]`}

// noise 生成 system / tools / metadata 之类不被解释、只被跳过的值，里面塞满括号、引号和与被解释字段同名的键。
func (g *strictGen) noise(depth int) string {
	if g.wild && g.pct(6) {
		return g.pick(strictNoiseOdd)
	}
	switch w := g.n(10); {
	case w < 3 || depth >= 3:
		return g.pick(strictNoiseScalars)
	case w < 6:
		m := make([]string, g.n(5))
		for i := range m {
			m[i] = g.member(g.pick(strictNoiseKeys), g.noise(depth+1))
		}
		return "{" + strings.Join(m, ",") + "}"
	default:
		m := make([]string, g.n(5))
		for i := range m {
			m[i] = g.noise(depth + 1)
		}
		return "[" + strings.Join(m, ",") + "]"
	}
}

var (
	strictMaxTokensStd = []string{`1`, `16`, `1024`, `128000`, `0`, `-1`, `0.5`, `1.0`, `1e0`, `1E2`, `1e-1`, `0.99999`, `"16"`, `null`, `true`,
		`false`, `[]`, `{}`, `[1]`, `99999999999999999999999`, `1e999`, `-0`, `2.5e3`}
	strictMaxTokensWild = []string{`01`, `+1`, `1.`, `.5`, `NaN`, `Infinity`, `0x10`, `1abc`, `-`, `1e`, `1 2`, `--1`, `tru`, `nul`, `True`, `nan`, `nullx`}

	strictTemperatureStd = []string{`0`, `0.0`, `1`, `1.0`, `0.5`, `-0`, `-0.0`, `1.0000001`, `0.9999999`, `-0.0000001`, `2`, `5`, `"0.5"`, `null`,
		`true`, `false`, `[0.5]`, `{}`, `1e0`, `1e-400`, `1E+0`, `0e0`, `0.1`, `1e999`, `-1`, `0.7`}
	strictTemperatureWild = []string{`NaN`, `Infinity`, `-Infinity`, `+0.5`, `.5`, `1.`, `00.5`, `nan`}

	strictThinkingStd = []string{`{"type":"enabled"}`, `{"type":"adaptive"}`, `{"type":"disabled"}`, `{"type":" enabled "}`, `{"type":"ENABLED"}`,
		`{"type":"Adaptive"}`, `{"type":"enabled","budget_tokens":2048}`, `{"budget_tokens":1,"type":"adaptive"}`,
		`{"type":"enabled","type":"disabled"}`, `{"type":"disabled","type":"enabled"}`, `{"type":null}`, `{"type":1}`, `{"type":["enabled"]}`,
		`{"type":{"type":"enabled"}}`, `{"nested":{"type":"enabled"}}`, `"enabled"`, `null`, `true`, `{}`,
		`{"type"  :  "enabled"  ,  "budget_tokens"  :  5}`, `{"type":"adaptive","display":"summarized"}`, `{"type":""}`, `{"type":"enable"}`}
	strictThinkingWild = []string{`{"type":"en\u0061bled"}`, `{"ty\u0070e":"enabled"}`, "{\"type\":\"enabled\t\"}", `{"type":"enabled"`, `{"type":"enabled",}`,
		`{"type":"adaptive"}}`, `{"thinking":{"type":"enabled"}}`, `{"type":"\u0065nabled"}`, `{"type":enabled}`, `{type:"enabled"}`, `{"type":"enabled" "x":1}`}

	// thinking 是数组：合法 JSON 里取不到 type，但扫描器一律退回 gjson（见 thinkingEnabled），这里验证退回后结果相同。
	strictThinkingArrays = []string{`["enabled"]`, `[{"type":"enabled"}]`, `[]`, `[ ]`, `[1,2]`, `[{"type":"enabled"},{"type":"disabled"}]`,
		`[[{"type":"enabled"}]]`, `[null]`, `[{"thinking":{"type":"enabled"}}]`}

	// thinking 数组陷阱的部件（见 thinkingTrap）：数组里塞杂质括号，再暗藏第二个 thinking。
	strictTrapInner  = []string{`(`, `[`, `{`, `-`, `-[`, ``, `}`, `)`, `1`, `"x"`, `[(`, `{(`, `((`, `}}`, `,`, ` `, `null`}
	strictTrapCloser = []string{`]`, `)`, `}`, `]]`, `])`, `]}`, ``}
	strictTrapSep    = []string{`,`, ``, ` `, `, `}
	strictTrapHidden = []string{`{"type":"enabled"}`, `{"type":"adaptive"}`, `{"type":"disabled"}`, `{}`, `[]`, `{"type":"enabled","budget_tokens":1}`}
	strictTrapTail   = []string{``, `)`, `]`, `}`, `]}`, `)}`, `}}`, `]]`}

	strictTopPStd = []string{`1`, `1.0`, `0.5`, `0`, `0.3`, `"1"`, `"0.5"`, `"abc"`, `null`, `true`, `false`, `[]`, `{}`, `5`, `-1`, `1e0`, `""`, `" 1"`,
		`"1 "`, `"1e0"`, `1.0000000001`, `0.99999999`}
	strictTopPWild = []string{`"\u0031"`, `NaN`, `"1\u0020"`, `01`}

	strictTopKStd  = []string{`0`, `1`, `0.0`, `-0`, `"0"`, `null`, `true`, `false`, `40`, `[]`, `{}`, `"abc"`, `0.5`, `-1`, `1e-400`, `"0.0"`, `" 0"`}
	strictTopKWild = []string{`"\u0030"`, `NaN`, `00`}
)

func (g *strictGen) val(std, wild []string) string {
	if g.wild && g.pct(25) {
		return g.pick(wild)
	}
	return g.pick(std)
}

// thinkingTrap 生成“thinking 是数组、数组范围内暗藏第二个 thinking”的畸形取值：
//
//	[ 杂质 收尾 分隔 "thinking":{…} 杂质
//
// 合法 JSON 里不会出现。gjson 对 "thinking.type" 会下钻进数组并在它自己的位置收尾，与按括号计深的 squash 不同，
// 暗藏的 thinking 对旧实现可见、对只做 squash 的扫描器不可见（反过来也会）。调用方在取值后接逗号和下一个字段，或直接收尾。
func (g *strictGen) thinkingTrap() string {
	return "[" + g.pick(strictTrapInner) + g.pick(strictTrapCloser) + g.pick(strictTrapSep) + `"thinking":` + g.pick(strictTrapHidden) + g.pick(strictTrapTail)
}

// scalar 生成 max_tokens / temperature / top_p / top_k 的取值；畸形模式下偶尔换成“数组里暗藏同名第二个键”的陷阱。
// 这些字段 gjson 按简单路径查找，数组/对象只做 squash 跳过，应当与扫描器一致。
func (g *strictGen) scalar(key string, std, wild []string) string {
	if g.wild && g.pct(4) {
		return "[" + g.pick(strictTrapInner) + g.pick(strictTrapCloser) + g.pick(strictTrapSep) + `"` + key + `":` +
			g.pick([]string{"0", "0.5", "1", "5", `"1"`, "true"}) + g.pick(strictTrapTail)
	}
	return g.val(std, wild)
}

// thinking 生成 thinking 的取值：标准模式只出对象和标量；畸形模式还会出数组与陷阱。
func (g *strictGen) thinking() string {
	switch {
	case g.wild && g.pct(8):
		return g.thinkingTrap()
	case g.wild && g.pct(6):
		return g.pick(strictThinkingArrays)
	}
	return g.val(strictThinkingStd, strictThinkingWild)
}

func (g *strictGen) body() string {
	var f []string
	add := func(k, v string) { f = append(f, g.member(k, v)) }
	add("model", `"claude-opus-5-5"`)
	if !g.wild || !g.pct(3) {
		add("messages", g.messages())
	}
	if g.pct(88) {
		add("max_tokens", g.scalar("max_tokens", strictMaxTokensStd, strictMaxTokensWild))
	}
	if g.pct(40) {
		add("temperature", g.scalar("temperature", strictTemperatureStd, strictTemperatureWild))
	}
	if g.pct(35) {
		add("thinking", g.thinking())
	}
	if g.pct(18) {
		add("top_p", g.scalar("top_p", strictTopPStd, strictTopPWild))
	}
	if g.pct(18) {
		add("top_k", g.scalar("top_k", strictTopKStd, strictTopKWild))
	}
	if g.pct(30) {
		add("system", g.noise(0))
	}
	if g.pct(25) {
		add("tools", g.noise(0))
	}
	if g.pct(20) {
		add("metadata", g.noise(0))
	}
	if g.pct(30) {
		add("stream", g.pick([]string{`true`, `false`}))
	}
	if g.pct(10) {
		add("stop_sequences", g.noise(0))
	}
	if g.pct(5) { // 非追踪字段重复出现是合法 JSON
		add("stream", `true`)
	}
	if g.wild && g.pct(8) { // 追踪字段重复出现
		switch g.n(6) {
		case 0:
			add("messages", g.messages())
		case 1:
			add("max_tokens", g.val(strictMaxTokensStd, strictMaxTokensWild))
		case 2:
			add("temperature", g.val(strictTemperatureStd, strictTemperatureWild))
		case 3:
			add("thinking", g.val(strictThinkingStd, strictThinkingWild))
		case 4:
			add("top_p", g.val(strictTopPStd, strictTopPWild))
		default:
			add("top_k", g.val(strictTopKStd, strictTopKWild))
		}
	}
	if g.wild && g.pct(3) { // 转义的追踪字段名
		switch g.n(3) {
		case 0:
			add(`max\u005ftokens`, g.val(strictMaxTokensStd, strictMaxTokensWild))
		case 1:
			add(`mess\u0061ges`, g.messages())
		default:
			add(`th\u0069nking`, g.val(strictThinkingStd, strictThinkingWild))
		}
	}
	if g.pct(60) {
		g.r.Shuffle(len(f), func(i, j int) { f[i], f[j] = f[j], f[i] })
	}
	s := "{" + g.ws() + strings.Join(f, g.ws()+","+g.ws()) + g.ws() + "}"
	if g.wild {
		switch g.n(100) {
		case 0, 1:
			s = "\xef\xbb\xbf" + s
		case 2:
			s = g.pick([]string{"xx", ";", "\x00", ")", "(", "}", "]"}) + s
		case 3, 4:
			s += g.pick([]string{"garbage", "{}", "}", " ", "\n", ",", "\x00"})
		case 5:
			if len(s) > 2 {
				s = s[:len(s)-1-g.n(min(len(s)-1, 30))]
			}
		}
	}
	return s
}

var strictMutationJunk = []byte("\"\\{}[](),: \n\t0123456789-.+eEtfnul\x00\xff\xc2\xa0")

// mutate 对请求体做 1~3 次随机改动：删除、插入、替换、截断、复制片段、交换相邻字节；一半的位置优先落在结构字符上。
func (g *strictGen) mutate(s string) string {
	b := []byte(s)
	pos := func() int {
		if g.pct(50) {
			for try := 0; try < 24; try++ {
				i := g.n(len(b))
				switch b[i] {
				case '{', '}', '[', ']', ',', ':', '"', '\\':
					return i
				}
			}
		}
		return g.n(len(b))
	}
	for k := 1 + g.n(3); k > 0 && len(b) > 1; k-- {
		i := pos()
		c := strictMutationJunk[g.n(len(strictMutationJunk))]
		switch g.n(7) {
		case 0:
			b = append(b[:i], b[i+1:]...)
		case 1:
			b = append(b[:i], append([]byte{c}, b[i:]...)...)
		case 2:
			b[i] = c
		case 3:
			b = b[:i]
		case 4:
			j := min(len(b), i+1+g.n(40))
			b = append(b[:j], append(append([]byte{}, b[i:j]...), b[j:]...)...)
		case 5:
			if i+1 < len(b) {
				b[i], b[i+1] = b[i+1], b[i]
			}
		default:
			j := min(len(b), i+1+g.n(20))
			b = append(b[:i], b[j:]...)
		}
	}
	return string(b)
}

// ---- Fuzz（日常只跑种子；`go test -fuzz FuzzValidateStrictOpusMessagesRequest` 可长时间探索）----

func FuzzValidateStrictOpusMessagesRequest(f *testing.F) {
	for _, seed := range strictOpusSweepSeeds() {
		f.Add([]byte(seed))
	}
	for _, body := range strictOpusEdgeBodies()[:80] {
		f.Add([]byte(body))
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		got, gotOK := ValidateStrictOpusMessagesRequest(strictOpusModel, body)
		want, wantOK := validateStrictOpusMessagesRequestLegacy(strictOpusModel, body)
		if got != want || gotOK != wantOK {
			t.Fatalf("新旧实现结果不一致\n body=%s\n 新: %q %v\n 旧: %q %v", strictOpusClip(body), got, gotOK, want, wantOK)
		}
	})
}

// ---- 基准 ----

// 接近基准形状的长请求体：在任意 (i, j) 放一个空白 text 块，新旧报错下标一致（含三位、四位下标），
// 放两个时只报靠前的那个，并且这类请求体必须由扫描器直接作答（不能因为体积大就退回 gjson）。
func TestValidateStrictOpusMessagesRequestLongBodies(t *testing.T) {
	r := rand.New(rand.NewSource(20261130))
	shapes := []struct{ messages, blocks, textBytes int }{{500, 3, 900}, {3000, 2, 200}, {40, 12, 3000}}
	blanks := []string{"", " ", `\n`, `\u0020\u00a0`, "\t", "\u3000"}
	for _, sh := range shapes {
		for k := 0; k < 8; k++ {
			at := [][2]int{{r.Intn(sh.messages), r.Intn(sh.blocks)}, {r.Intn(sh.messages), r.Intn(sh.blocks)}}
			blank := blanks[r.Intn(len(blanks))]
			body := strictOpusBenchBodyWith(sh.messages, sh.blocks, sh.textBytes, "", func(i, j int) (string, bool) {
				for _, p := range at {
					if p[0] == i && p[1] == j {
						return blank, true
					}
				}
				return "", false
			})
			got, gotOK := ValidateStrictOpusMessagesRequest(strictOpusModel, body)
			want, wantOK := validateStrictOpusMessagesRequestLegacy(strictOpusModel, body)
			require.Equal(t, want, got)
			require.Equal(t, wantOK, gotOK)
			require.False(t, gotOK, "放了空白 text 块就应报错")
			_, _, done := validateStrictOpusMessagesScan(body)
			require.True(t, done, "长请求体必须由扫描器直接作答")
		}
	}
	for _, sh := range shapes {
		body := strictOpusBenchBody(sh.messages, sh.blocks, sh.textBytes, "")
		got, gotOK := ValidateStrictOpusMessagesRequest(strictOpusModel, body)
		want, wantOK := validateStrictOpusMessagesRequestLegacy(strictOpusModel, body)
		require.Equal(t, want, got)
		require.Equal(t, wantOK, gotOK)
		require.True(t, gotOK)
		_, _, done := validateStrictOpusMessagesScan(body)
		require.True(t, done, "长请求体必须由扫描器直接作答")
	}
}

// strictOpusBenchBody 生成接近真实客户端的请求体：system、20 个工具定义在前，
// messages 居中，max_tokens / thinking 等排在 messages 之后（旧实现最慢的排法：每次取字段都要先跳过整个 messages）。
// lead 是每个 text 开头的内容；传 `\n你好` 构造“以转义开头”的形状（Python 默认 ensure_ascii 序列化的中文就是这样）。
func strictOpusBenchBody(messages, blocks, textBytes int, lead string) []byte {
	return strictOpusBenchBodyWith(messages, blocks, textBytes, lead, nil)
}

// strictOpusBenchBodyWith 同上；textAt 返回 (内容, true) 时覆盖第 i 条消息第 j 个 block 的 text（传入的是 JSON 字符串里的原始文本）。
func strictOpusBenchBodyWith(messages, blocks, textBytes int, lead string, textAt func(i, j int) (string, bool)) []byte {
	unit := `The quick brown fox \"jumps\" over the lazy dog, then reads {braces}, [brackets] and (parens)\n`
	text := lead + strings.Repeat(unit, textBytes/len(unit)+1)
	b := append([]byte(nil), `{"model":"claude-opus-5-5","system":[{"type":"text","text":"You are Claude Code, Anthropic's official CLI for Claude.","cache_control":{"type":"ephemeral"}}],"tools":[`...)
	for i := 0; i < 20; i++ {
		if i > 0 {
			b = append(b, ',')
		}
		b = fmt.Appendf(b, `{"name":"tool_%d","description":"Runs a command and returns \"stdout\"; see {docs} for [details] (%d).","input_schema":{"type":"object","properties":{"command":{"type":"string","description":"The command to run"},"timeout":{"type":"number"},"mode":{"type":"string","enum":["a","b","c"]}},"required":["command"]}}`, i, i)
	}
	b = append(b, `],"messages":[`...)
	for i := 0; i < messages; i++ {
		if i > 0 {
			b = append(b, ',')
		}
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		b = fmt.Appendf(b, `{"role":"%s","content":[`, role)
		for j := 0; j < blocks; j++ {
			if j > 0 {
				b = append(b, ',')
			}
			blockText := text
			if textAt != nil {
				if override, ok := textAt(i, j); ok {
					blockText = override
				}
			}
			b = append(b, `{"type":"text","text":"`...)
			b = append(b, blockText...)
			b = append(b, '"')
			if j == blocks-1 {
				b = append(b, `,"cache_control":{"type":"ephemeral"}`...)
			}
			b = append(b, '}')
		}
		b = append(b, `]}`...)
	}
	b = append(b, `],"metadata":{"user_id":"user_abc_account__session_123"},"max_tokens":8192,"temperature":1,"thinking":{"type":"adaptive"},"stream":true}`...)
	return b
}

func BenchmarkValidateStrictOpusMessagesRequest(b *testing.B) {
	long := strictOpusBenchBody(500, 3, 900, "")
	shapes := []struct {
		name string
		body []byte
	}{
		{"ordinary_20msg", strictOpusBenchBody(20, 2, 300, "")},
		{"long_500msg_x3blocks", long},
		{"extreme_3000msg_x2blocks", strictOpusBenchBody(3000, 2, 200, "")},
		{"long_500msg_x3blocks_escape_lead", strictOpusBenchBody(500, 3, 900, `\n你好`)},
		// 扫描器要放弃、退回 gjson 的请求体：量化最坏情形下多付的成本。
		{"fallback_bom_prefix", append([]byte("\xef\xbb\xbf"), long...)},                                    // 第一个字节就放弃
		{"fallback_late_bail", bytes.Replace(long, []byte(`"stream":true}`), []byte(`"stream":true,}`), 1)}, // 扫完整个请求体才放弃
	}
	impls := []struct {
		name string
		fn   func(string, []byte) (string, bool)
	}{
		{"legacy", validateStrictOpusMessagesRequestLegacy},
		{"scan", ValidateStrictOpusMessagesRequest},
	}
	for _, shape := range shapes {
		for _, impl := range impls {
			b.Run(shape.name+"/"+impl.name, func(b *testing.B) {
				if msg, ok := impl.fn(strictOpusModel, shape.body); !ok {
					b.Fatalf("基准请求体必须通过校验: %s", msg)
				}
				b.SetBytes(int64(len(shape.body)))
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					impl.fn(strictOpusModel, shape.body)
				}
			})
		}
	}
}
