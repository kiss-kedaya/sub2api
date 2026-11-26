package ipnotice

import (
	"math/rand"
	"strings"
	"testing"
)

// perf18Body builds an Anthropic-shaped body of about size bytes. Shapes:
// "code" has a \n escape every few dozen bytes plus snake_case identifiers, "prose" has
// almost no backslashes or underscores, "image" is mostly one long base64 string.
func perf18Body(size int, shape string) []byte {
	rng := rand.New(rand.NewSource(int64(size)))
	words := []string{"func", "return", "const", "the", "value", "error", "import", "context", "request", "package", "handler", "nil", "string", "gateway", "store", "model", "stream"}
	identifiers := []string{"ctx_id", "api_key", "user_id", "max_tokens", "request_body", "group_id"}
	var sb strings.Builder
	sb.WriteString(`{"model":"claude-sonnet-4-5","stream":true,"messages":[`)
	if shape == "image" {
		const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
		data := make([]byte, size)
		for i := range data {
			data[i] = alphabet[rng.Intn(len(alphabet))]
		}
		sb.WriteString(`{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + string(data) + `"}},{"type":"text","text":"what is this?"}]}]}`)
		return []byte(sb.String())
	}
	first := true
	for sb.Len() < size {
		if !first {
			sb.WriteByte(',')
		}
		first = false
		role := "user"
		if rng.Intn(2) == 0 {
			role = "assistant"
		}
		sb.WriteString(`{"role":"` + role + `","content":[{"type":"text","text":"`)
		n := 2000 + rng.Intn(4000)
		line := 0
		for i := 0; i < n; {
			w := words[rng.Intn(len(words))]
			if shape == "code" && rng.Intn(6) == 0 {
				w = identifiers[rng.Intn(len(identifiers))]
			}
			sb.WriteString(w)
			i += len(w) + 1
			line += len(w) + 1
			switch {
			case shape == "code" && line > 40+rng.Intn(30):
				sb.WriteString(`\n`)
				line = 0
			case shape == "code" && rng.Intn(17) == 0:
				sb.WriteString(` \"` + w + `\" `)
			case rng.Intn(53) == 0:
				sb.WriteString(" café — ")
			default:
				sb.WriteByte(' ')
			}
		}
		sb.WriteString(`"}]}`)
	}
	sb.WriteString(`]}`)
	return []byte(sb.String())
}

// BenchmarkPerf18NormalizeReplay is the cost every request body pays in ReadRequestBodyWithPrealloc:
// no notice marker in the body, so NormalizeReplay only has to prove that.
func BenchmarkPerf18NormalizeReplay(b *testing.B) {
	for _, shape := range []string{"code", "prose", "image"} {
		for _, size := range []struct {
			name string
			n    int
		}{{"10KB", 10 << 10}, {"200KB", 200 << 10}, {"2MB", 2 << 20}} {
			body := perf18Body(size.n, shape)
			b.Run(shape+"/"+size.name, func(b *testing.B) {
				b.SetBytes(int64(len(body)))
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					if out := NormalizeReplay(body, Anthropic); len(out) != len(body) {
						b.Fatal("changed")
					}
				}
			})
		}
	}
}
