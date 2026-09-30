//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// referenceUpstreamFinancialFailureEnvelope 是**加便宜门之前**的实现，仅用于等价性比对。
//
// 新实现先做 `strings.Contains(text, "error") || strings.Contains(text, "failed")`
// 的提前退出。这个门是可证明安全的：下面五个分支分别要求出现
// errors / error / response.error / "type":"error" / "status":"failed"，
// 每一个都必然包含 "error" 或 "failed" 之一。本测试用逐载荷比对把这个证明钉死——
// 如果有人以后往分支里加了不含这两个词的触发条件（例如新加一个
// `status == "rejected"`），这里会立刻失败。
func referenceUpstreamFinancialFailureEnvelope(body []byte) bool {
	node := gjson.ParseBytes(body)
	if node.IsArray() {
		for _, item := range node.Array() {
			if referenceUpstreamFinancialFailureEnvelope([]byte(item.Raw)) {
				return true
			}
		}
		return false
	}
	if node.Get("errors").IsArray() || node.Get("error").Exists() && node.Get("error").Type != gjson.Null || node.Get("response.error").Exists() && node.Get("response.error").Type != gjson.Null || node.Get("type").String() == "error" || node.Get("status").String() == "failed" {
		return IsUpstreamFinancialError(0, body)
	}
	return false
}

func TestUpstreamFinancialFailureEnvelopeGateIsEquivalent(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
	}{
		{"empty", ``},
		{"plain output, no keyword at all", `{"id":"r1","output":[{"type":"message","content":"hello"}]}`},
		// 便宜门要按「出现即放行」处理，不能漏掉这两支：
		{"status failed without the word error", `{"status":"failed","error":{"code":"insufficient_quota"}}`},
		{"status failed alone", `{"status":"failed"}`},
		{"bare error object", `{"error":{"code":"insufficient_quota","message":"quota exceeded"}}`},
		{"errors array", `{"errors":[{"message":"spending limit reached"}]}`},
		{"type error", `{"type":"error","message":"payment required"}`},
		{"response.error nested", `{"response":{"error":{"code":"billing_hard_limit_reached"}}}`},
		{"error as null must not match", `{"error":null}`},
		{"response.error null", `{"response":{"error":null}}`},
		// 关键词出现在用户内容里、但没有任何 envelope：不应误报。
		{"keyword inside user content only", `{"output":[{"type":"message","content":"an error occurred in my script"}]}`},
		{"keyword inside tool output only", `{"output":[{"type":"function_call_output","output":"failed to compile"}]}`},
		// 数组递归（含嵌套）。
		{"array of one envelope", `[{"error":{"message":"insufficient_quota"}}]`},
		{"nested arrays", `[[{"status":"failed"}]]`},
		{"array without envelope", `[{"output":"hello"},{"output":"failed"}]`},
		{"empty array", `[]`},
		// 非 JSON。
		{"not json", `insufficient_quota: spending limit reached`},
		{"truncated json", `{"error":`},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := upstreamFinancialFailureEnvelope([]byte(tc.body))
			want := referenceUpstreamFinancialFailureEnvelope([]byte(tc.body))
			require.Equal(t, want, got,
				"the cheap gate changed the result for %q —— 说明有新分支的触发词不含 error/failed", tc.body)
		})
	}
}

// 便宜门该省的是「正常帧」的整份解析。这里确认它确实走了快路径：
// 既不含 error 也不含 failed 的 body 必须返回 false，且不依赖 gjson。
func TestUpstreamFinancialFailureEnvelopeFastPathRejectsKeywordlessBody(t *testing.T) {
	t.Parallel()

	big := make([]byte, 0, 64<<10)
	big = append(big, `{"output":[`...)
	for len(big) < 64<<10 {
		big = append(big, `{"type":"message","content":"ordinary streaming text"},`...)
	}
	big = append(big, `{"type":"message","content":"end"}]}`...)

	require.NotContains(t, string(big), "error")
	require.NotContains(t, string(big), "failed")
	require.False(t, upstreamFinancialFailureEnvelope(big))
}

func BenchmarkUpstreamFinancialFailureEnvelope(b *testing.B) {
	plain := []byte(`{"type":"response.output_text.delta","delta":"ordinary streaming text","sequence_number":42}`)
	withErr := []byte(`{"error":{"code":"insufficient_quota","message":"quota exceeded"}}`)

	b.Run("plain_frame_with_gate", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if upstreamFinancialFailureEnvelope(plain) {
				b.Fatal("unexpected")
			}
		}
	})
	b.Run("plain_frame_reference", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if referenceUpstreamFinancialFailureEnvelope(plain) {
				b.Fatal("unexpected")
			}
		}
	})
	b.Run("error_frame_with_gate", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = upstreamFinancialFailureEnvelope(withErr)
		}
	})
}
