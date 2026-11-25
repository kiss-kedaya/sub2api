package service

import (
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 旧（逐次 sjson）与新（单遍补丁）实现的对照 benchmark：
//
//	go test ./internal/service/ -run '^$' -bench BenchmarkSpanPatch -benchmem
//
// 子基准名里 old=逐次 sjson 的旧实现副本，new=当前生产实现。

var spanBenchSink []byte

var spanBenchShapes = []struct{ msgs, bytes int }{
	{100, 200 << 10}, {100, 800 << 10}, {300, 200 << 10}, {300, 800 << 10},
}

func spanBenchText(n int) string {
	const unit = `line of tool output with "quotes", a\path and unicode é世界 `
	var b strings.Builder
	for b.Len() < n {
		b.WriteString(strings.NewReplacer(`"`, `\"`, `\`, `\\`).Replace(unit))
		b.WriteString(`\n`)
	}
	return b.String()
}

// 每 4 条一组：user、assistant(tool_calls，无 reasoning_content)、tool、assistant(纯文本)。
func buildSpanChatBody(msgs, size int, orphans bool) []byte {
	text := spanBenchText(size / msgs)
	half := spanBenchText(size / msgs / 2)
	var b strings.Builder
	b.WriteString(`{"model":"deepseek-v4-pro","stream":true,"messages":[`)
	for i := 0; i < msgs; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		switch i % 4 {
		case 0:
			fmt.Fprintf(&b, `{"role":"user","content":"%s"}`, text)
		case 1:
			fmt.Fprintf(&b, `{"role":"assistant","content":"%s","tool_calls":[{"id":"call_%d","type":"function","function":{"name":"f","arguments":"{}"}}]}`, half, i)
		case 2:
			id := fmt.Sprintf("call_%d", i-1)
			if orphans && i%40 == 2 {
				id = "call_orphan"
			}
			fmt.Fprintf(&b, `{"role":"tool","tool_call_id":"%s","content":"%s"}`, id, text)
		default:
			fmt.Fprintf(&b, `{"role":"assistant","content":"%s"}`, half)
		}
	}
	b.WriteString(`]}`)
	return []byte(b.String())
}

// Anthropic 形状：assistant 带 tool_use 但没有 thinking 块。
func buildSpanAnthropicBody(msgs, size int) []byte {
	text := spanBenchText(size / msgs)
	half := spanBenchText(size / msgs / 2)
	var b strings.Builder
	b.WriteString(`{"model":"deepseek-v4-pro","stream":true,"messages":[`)
	for i := 0; i < msgs; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		switch i % 3 {
		case 0:
			fmt.Fprintf(&b, `{"role":"user","content":[{"type":"text","text":"%s"}]}`, text)
		case 1:
			fmt.Fprintf(&b, `{"role":"assistant","content":[{"type":"text","text":"%s"},{"type":"tool_use","id":"toolu_%d","name":"f","input":{}}]}`, half, i)
		default:
			fmt.Fprintf(&b, `{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_%d","content":"%s"}]}`, i-1, text)
		}
	}
	b.WriteString(`]}`)
	return []byte(b.String())
}

// Responses 形状：每 4 条一组：message、reasoning(content 为空)、function_call、function_call_output。
// missing=true 时每 40 条丢一个 function_call_output。
func buildSpanResponsesBody(items, size int, missing bool) []byte {
	text := spanBenchText(size / items)
	var b strings.Builder
	b.WriteString(`{"model":"deepseek-v4-pro","stream":true,"input":[`)
	for i := 0; i < items; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		switch i % 4 {
		case 0:
			fmt.Fprintf(&b, `{"type":"message","role":"user","content":[{"type":"input_text","text":"%s"}]}`, text)
		case 1:
			fmt.Fprintf(&b, `{"type":"reasoning","id":"rs_%d","summary":[],"encrypted_content":"opaque","content":[]}`, i)
		case 2:
			fmt.Fprintf(&b, `{"type":"function_call","call_id":"call_%d","name":"f","arguments":"{}"}`, i)
		default:
			outputID := i - 1
			if missing && i%40 == 3 {
				outputID = -1
			}
			fmt.Fprintf(&b, `{"type":"function_call_output","call_id":"call_%d","output":"%s"}`, outputID, text)
		}
	}
	b.WriteString(`]}`)
	return []byte(b.String())
}

func buildSpanStreamBody(size int, withUsage bool) []byte {
	usage := ""
	if withUsage {
		usage = `,"stream_options":{"include_usage":true}`
	}
	return []byte(`{"model":"gpt-5.4","stream":true` + usage + `,"messages":[{"role":"user","content":"` + spanBenchText(size) + `"}]}`)
}

func benchSpanPair(b *testing.B, name string, build func(msgs, size int) []byte, legacy, current func(body []byte) []byte) {
	for _, shape := range spanBenchShapes {
		body := build(shape.msgs, shape.bytes)
		for _, impl := range []struct {
			name string
			fn   func([]byte) []byte
		}{{"old", legacy}, {"new", current}} {
			b.Run(fmt.Sprintf("%s/%s/msgs=%d/size=%dKB", name, impl.name, shape.msgs, shape.bytes>>10), func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(body)))
				for i := 0; i < b.N; i++ {
					spanBenchSink = impl.fn(body)
				}
			})
		}
	}
}

func BenchmarkSpanPatch(b *testing.B) {
	deepseek := &Account{Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_protocol": APIProtocolAdaptive}}
	chat := func(msgs, size int) []byte { return buildSpanChatBody(msgs, size, false) }
	chatOrphans := func(msgs, size int) []byte { return buildSpanChatBody(msgs, size, true) }
	responses := func(items, size int) []byte { return buildSpanResponsesBody(items, size, false) }
	responsesMissing := func(items, size int) []byte { return buildSpanResponsesBody(items, size, true) }

	benchSpanPair(b, "ChatReasoningPlaceholders", chat,
		func(body []byte) []byte { return legacyEnsureDeepSeekChatReasoningPlaceholders(deepseek, body) },
		func(body []byte) []byte { return ensureDeepSeekChatReasoningPlaceholders(deepseek, body) })
	benchSpanPair(b, "ThinkingReplayPlaceholders", buildSpanAnthropicBody,
		func(body []byte) []byte {
			out, _ := legacyEnsureDeepSeekThinkingReplayPlaceholders(body, "deepseek-v4-pro")
			return out
		},
		func(body []byte) []byte {
			out, _ := ensureDeepSeekThinkingReplayPlaceholders(body, "deepseek-v4-pro")
			return out
		})
	benchSpanPair(b, "NativeReasoningPlaceholders", responses,
		func(body []byte) []byte { return legacyEnsureDeepSeekNativeReasoningPlaceholders(deepseek, body) },
		func(body []byte) []byte { return ensureDeepSeekNativeReasoningPlaceholders(deepseek, body) })
	benchSpanPair(b, "DropOrphanToolMessages/none", chat,
		legacyDropOrphanDeepSeekToolMessages, dropOrphanDeepSeekToolMessages)
	benchSpanPair(b, "DropOrphanToolMessages/some", chatOrphans,
		legacyDropOrphanDeepSeekToolMessages, dropOrphanDeepSeekToolMessages)
	benchSpanPair(b, "NormalizeChatMessages", chat,
		legacyNormalizeDeepSeekChatMessages, normalizeDeepSeekChatMessages)
	benchSpanPair(b, "RepairMissingToolOutputs/complete", responses,
		func(body []byte) []byte { out, _, _ := legacyRepairDeepSeekMissingToolOutputs(body); return out },
		func(body []byte) []byte { out, _, _ := repairDeepSeekMissingToolOutputs(body); return out })
	benchSpanPair(b, "RepairMissingToolOutputs/missing", responsesMissing,
		func(body []byte) []byte { out, _, _ := legacyRepairDeepSeekMissingToolOutputs(body); return out },
		func(body []byte) []byte { out, _, _ := repairDeepSeekMissingToolOutputs(body); return out })
	for _, withUsage := range []bool{false, true} {
		body := buildSpanStreamBody(800<<10, withUsage)
		name := "StreamUsage/absent"
		if withUsage {
			name = "StreamUsage/already_true"
		}
		for _, impl := range []struct {
			name string
			fn   func([]byte) ([]byte, error)
		}{{"old", legacyEnsureOpenAIChatStreamUsage}, {"new", ensureOpenAIChatStreamUsage}} {
			b.Run(name+"/"+impl.name+"/size=800KB", func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(body)))
				for i := 0; i < b.N; i++ {
					spanBenchSink, _ = impl.fn(body)
				}
			})
		}
	}
}

// 分配量棘轮：新实现每次调用的总分配字节不得超过请求体的若干倍，锁住「一遍扫描、一次分配」，
// 防止以后又退回逐条 sjson.Set 的 O(消息数 × 请求体)。旧实现在同一输入上分配几十到上百倍请求体。
func spanAllocBytes(run func()) uint64 {
	run() // 预热（sync.Pool、gjson 内部缓存）
	// 取多次测量的最小值：TotalAlloc 是整个进程的计数，整包测试里别的 goroutine 偶尔的分配不该让棘轮误报。
	least := ^uint64(0)
	for i := 0; i < 5; i++ {
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		run()
		runtime.ReadMemStats(&after)
		least = min(least, after.TotalAlloc-before.TotalAlloc)
	}
	return least
}

func TestSpanPatchAllocationCeilings(t *testing.T) {
	deepseek := &Account{Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_protocol": APIProtocolAdaptive}}
	const msgs, size = 300, 800 << 10
	chat := buildSpanChatBody(msgs, size, false)
	chatOrphans := buildSpanChatBody(msgs, size, true)
	anthropic := buildSpanAnthropicBody(msgs, size)
	responses := buildSpanResponsesBody(msgs, size, false)
	responsesMissing := buildSpanResponsesBody(msgs, size, true)
	stream := buildSpanStreamBody(size, false)
	streamDone := buildSpanStreamBody(size, true)

	cases := []struct {
		name string
		body []byte
		// maxCopies：新实现允许的最大分配量（请求体的倍数）；minRatio：旧/新至少要差这么多倍（0 表示不比较）。
		maxCopies float64
		minRatio  float64
		legacy    func([]byte)
		current   func([]byte)
	}{
		{"chat reasoning placeholders", chat, 1.5, 10,
			func(b []byte) { legacyEnsureDeepSeekChatReasoningPlaceholders(deepseek, b) },
			func(b []byte) { ensureDeepSeekChatReasoningPlaceholders(deepseek, b) }},
		{"thinking replay placeholders", anthropic, 1.5, 10,
			func(b []byte) { legacyEnsureDeepSeekThinkingReplayPlaceholders(b, "deepseek-v4-pro") },
			func(b []byte) { ensureDeepSeekThinkingReplayPlaceholders(b, "deepseek-v4-pro") }},
		{"native reasoning placeholders", responses, 1.5, 10,
			func(b []byte) { legacyEnsureDeepSeekNativeReasoningPlaceholders(deepseek, b) },
			func(b []byte) { ensureDeepSeekNativeReasoningPlaceholders(deepseek, b) }},
		{"drop orphan tool messages", chatOrphans, 1.5, 2,
			func(b []byte) { legacyDropOrphanDeepSeekToolMessages(b) },
			func(b []byte) { dropOrphanDeepSeekToolMessages(b) }},
		{"repair, nothing missing", responses, 0.1, 10,
			func(b []byte) { legacyRepairDeepSeekMissingToolOutputs(b) },
			func(b []byte) { repairDeepSeekMissingToolOutputs(b) }},
		{"repair, outputs missing", responsesMissing, 1.5, 0,
			func(b []byte) { legacyRepairDeepSeekMissingToolOutputs(b) },
			func(b []byte) { repairDeepSeekMissingToolOutputs(b) }},
		{"stream usage, absent", stream, 1.5, 0,
			func(b []byte) { legacyEnsureOpenAIChatStreamUsage(b) },
			func(b []byte) { ensureOpenAIChatStreamUsage(b) }},
		{"stream usage, already true", streamDone, 0.01, 0,
			func(b []byte) { legacyEnsureOpenAIChatStreamUsage(b) },
			func(b []byte) { ensureOpenAIChatStreamUsage(b) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.True(t, json.Valid(tc.body), "fixture must be valid JSON")
			current := spanAllocBytes(func() { tc.current(tc.body) })
			legacy := spanAllocBytes(func() { tc.legacy(tc.body) })
			copies := float64(current) / float64(len(tc.body))
			t.Logf("body=%dKB new=%d B (%.2fx body) old=%d B (%.1fx body)", len(tc.body)>>10, current, copies, legacy, float64(legacy)/float64(len(tc.body)))
			require.LessOrEqual(t, copies, tc.maxCopies, "new implementation allocates too much")
			if tc.minRatio > 0 {
				require.GreaterOrEqual(t, float64(legacy)/float64(current+1), tc.minRatio)
			}
		})
	}
}
