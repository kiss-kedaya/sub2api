package requestmodel

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConflictingWSBillingField(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		field  string
		expect bool
	}{
		{
			name:   "duplicate service_tier",
			body:   `{"type":"response.create","service_tier":"default","service_tier":"priority"}`,
			field:  "service_tier",
			expect: true,
		},
		{
			name:   "duplicate service_tier same value still rejected",
			body:   `{"type":"response.create","service_tier":"flex","service_tier":"flex"}`,
			field:  "service_tier",
			expect: true,
		},
		{
			name:   "case variant service_tier",
			body:   `{"Service_Tier":"default","service_tier":"priority"}`,
			field:  "service_tier",
			expect: true,
		},
		{
			name:   "escaped duplicate service_tier",
			body:   `{"\u0073ervice_tier":"default","service_tier":"priority"}`,
			field:  "service_tier",
			expect: true,
		},
		{
			name:   "duplicate reasoning object",
			body:   `{"reasoning":{"effort":"low"},"reasoning":{"effort":"high"}}`,
			field:  "reasoning",
			expect: true,
		},
		{
			name:   "duplicate nested effort",
			body:   `{"reasoning":{"effort":"low","effort":"high"}}`,
			field:  "reasoning.effort",
			expect: true,
		},
		{
			name:   "duplicate output_config nested effort",
			body:   `{"output_config":{"effort":"low","Effort":"high"}}`,
			field:  "output_config.effort",
			expect: true,
		},
		{
			name:   "duplicate speed",
			body:   `{"speed":"fast","speed":"standard"}`,
			field:  "speed",
			expect: true,
		},
		{
			name:   "normal codex frame",
			body:   `{"type":"response.create","model":"gpt-6.1-sol","service_tier":"priority","reasoning":{"effort":"high"},"input":[]}`,
			expect: false,
		},
		{
			name:   "duplicate model handled by model candidates",
			body:   `{"model":"a","model":"b"}`,
			expect: false,
		},
		{
			name:   "duplicates inside freeform tool arguments are ignored",
			body:   `{"model":"gpt-6.1-sol","input":[{"type":"function_call","arguments":"{\"effort\":\"a\",\"effort\":\"b\"}"}]}`,
			expect: false,
		},
		{
			name:   "duplicate unrelated field ignored",
			body:   `{"input":[],"input":[]}`,
			expect: false,
		},
		{
			name:   "non object body ignored",
			body:   `[]`,
			expect: false,
		},
		{
			name:   "empty body ignored",
			body:   ``,
			expect: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			field, conflict := ConflictingWSBillingField([]byte(tt.body))
			require.Equal(t, tt.expect, conflict)
			if tt.expect {
				require.Equal(t, tt.field, field)
			}
		})
	}
}

// BenchmarkConflictingWSBillingFieldTypical 覆盖 Codex 后续 turn 帧（几 KB）与
// 首帧（含完整 input 的大帧）。WS 每 turn 都会调用一次，热路径成本必须可测。
func BenchmarkConflictingWSBillingFieldTypical(b *testing.B) {
	for _, size := range []int{2048, 200 << 10, 2 << 20} {
		b.Run(fmt.Sprintf("frame_%dKB", size>>10), func(b *testing.B) {
			body := buildConflictingWSBenchFrame(size)
			b.SetBytes(int64(len(body)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, conflict := ConflictingWSBillingField(body); conflict {
					b.Fatal("unexpected conflict")
				}
			}
		})
	}
}

func buildConflictingWSBenchFrame(size int) []byte {
	var sb strings.Builder
	sb.Grow(size + 256)
	_, _ = sb.WriteString(`{"type":"response.create","model":"gpt-6.1-sol","service_tier":"priority","reasoning":{"effort":"high"},"input":[`)
	part := strings.Repeat("abcdefgh", 128) // 1KB 文本块
	written := 0
	first := true
	for written < size {
		if !first {
			_ = sb.WriteByte(',')
		}
		first = false
		_, _ = sb.WriteString(`{"type":"message","role":"user","content":[{"type":"input_text","text":"`)
		_, _ = sb.WriteString(part)
		_, _ = sb.WriteString(`"}]}`)
		written += len(part) + 64
	}
	_, _ = sb.WriteString(`]}`)
	return []byte(sb.String())
}
