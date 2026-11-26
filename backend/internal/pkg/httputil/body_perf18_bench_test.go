package httputil

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// BenchmarkPerf18ReadLenientJSONRequestBody is what every /v1/messages body goes through in the
// handler: read, replay normalization, lenient normalization, replay normalization again. The body has
// no notice marker and no control bytes, so every pass only has to prove that.
func BenchmarkPerf18ReadLenientJSONRequestBody(b *testing.B) {
	for _, size := range []struct {
		name string
		n    int
	}{{"10KB", 10 << 10}, {"200KB", 200 << 10}, {"2MB", 2 << 20}} {
		var sb strings.Builder
		sb.WriteString(`{"model":"claude-sonnet-4-5","stream":true,"messages":[{"role":"user","content":"`)
		for sb.Len() < size.n {
			sb.WriteString(`func handler_name(ctx context.Context) error {\n\treturn fmt.Errorf(\"value: %d\", 42) // café\n}\n`)
		}
		sb.WriteString(`"}]}`)
		body := []byte(sb.String())
		b.Run(size.name, func(b *testing.B) {
			b.SetBytes(int64(len(body)))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
				out, err := ReadLenientJSONRequestBodyWithPrealloc(req, 64<<20)
				if err != nil || len(out) != len(body) {
					b.Fatal("unexpected result", err)
				}
			}
		})
	}
}
