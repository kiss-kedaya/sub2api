package httputil

import (
	"bytes"
	"fmt"
	"testing"
)

func BenchmarkNormalizeLenientJSONRequestBody(b *testing.B) {
	for _, size := range []int{256, 1 << 20, 32 << 20} {
		b.Run(fmt.Sprintf("compact_%d", size), func(b *testing.B) {
			body := append([]byte(`{"input":"`), bytes.Repeat([]byte("x"), size)...)
			body = append(body, []byte(`","cache_control":{"type":"ephemeral"}}`)...)
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			b.ResetTimer()
			for b.Loop() {
				got, err := NormalizeLenientJSONRequestBody(body, int64(len(body)))
				if err != nil || len(got) != len(body) || &got[0] != &body[0] {
					b.Fatal("normal JSON must reuse the original request")
				}
			}
		})
	}
}
