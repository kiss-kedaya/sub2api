package service

import (
	"fmt"
	"strings"
	"testing"
)

func BenchmarkGrokResponsesLargeInput(b *testing.B) {
	for _, size := range []int{1 << 20, 4 << 20} {
		for _, typed := range []bool{false, true} {
			prefix := `{"role":"user","content":"`
			if typed {
				prefix = `{"type":"message","role":"user","content":"`
			}
			body := []byte(`{"model":"grok-4","input":[` + prefix + strings.Repeat("x", size) + `"}]}`)
			for _, patch := range []bool{false, true} {
				b.Run(fmt.Sprintf("%dMiB/typed=%t/patch=%t", size>>20, typed, patch), func(b *testing.B) {
					b.ReportAllocs()
					b.SetBytes(int64(len(body)))
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						var err error
						if patch {
							_, err = patchGrokResponsesBody(body, "grok-4.5")
						} else {
							_, err = sanitizeGrokResponsesModelInput(body)
						}
						if err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		}
	}
}
