package service

import (
	"bytes"
	"math/rand"
	"strings"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// legacyStripGrokAPIKeyOpenAICompatFields 是改成一遍扫描之前的实现，只作对照（oracle）。
func legacyStripGrokAPIKeyOpenAICompatFields(body []byte) ([]byte, error) {
	out := body
	for _, field := range []string{"store", "include", "previous_response_id", "service_tier"} {
		if !gjson.GetBytes(out, field).Exists() {
			continue
		}
		next, err := sjson.DeleteBytes(out, field)
		if err != nil {
			return nil, err
		}
		out = next
	}
	return out, nil
}

func assertGrokStripMatchesLegacy(t *testing.T, body []byte) {
	t.Helper()
	original := bytes.Clone(body)
	want, wantErr := legacyStripGrokAPIKeyOpenAICompatFields(body)
	got, gotErr := stripGrokAPIKeyOpenAICompatFields(body)
	require.Equal(t, original, body, "入参不得被改写: %q", original)
	require.Equal(t, wantErr != nil, gotErr != nil, "body: %q", original)
	if wantErr != nil {
		return
	}
	require.Equal(t, string(want), string(got), "body: %q", original)
	if bytes.Equal(original, got) {
		require.Same(t, unsafe.SliceData(body), unsafe.SliceData(got), "未改动必须原样返回切片: %q", original)
	}
}

func TestStripGrokAPIKeyOpenAICompatFieldsMatchesLegacy(t *testing.T) {
	fixed := []string{
		``,
		`{}`,
		`null`,
		`[1,2]`,
		`"store"`,
		`{"model":"grok-4"}`,
		`{"store":false}`,
		`{"store":false,"include":["reasoning.encrypted_content"]}`,
		`{"model":"m","store":false,"include":[],"previous_response_id":"resp_1","service_tier":"auto","input":[]}`,
		`{"service_tier":"auto","previous_response_id":"resp_1","include":[],"store":false}`,
		`{"model":"m","service_tier":"auto","input":"x","store":true}`,
		`{"store":null,"model":"m"}`,
		`{"model":"m","store":false,"store":true}`,
		`{"store":1,"store":2,"include":3,"include":4}`,
		`{"input":{"store":false,"include":[]},"store":false}`,
		`{"input":[{"store":false}],"model":"m"}`,
		`{"model":"m","store":false}`,
		`{"Store":false,"STORE":true}`,
		// 宽松排版：交给 sjson
		`{"model": "m", "store": false, "include": []}`,
		"{\n  \"model\": \"m\",\n  \"store\": false,\n  \"include\": [],\n  \"input\": []\n}",
		`{ "store":false,"model":"m"}`,
		`{"store":false ,"model":"m"}`,
		`{"model":"m","store":false }`,
		` {"model":"m","store":false}`,
		`{"model":"m","store":false} `,
		"\n{\"model\":\"m\",\"store\":false,\"include\":[]}\n",
		`{"model":"m","store":false} trailing`,
		// 畸形
		`{"model":"m","store":false`,
		`{"model":"m","store":false,`,
		`{"model":"m","store":}`,
		`{"store":false,"include":[1,2}`,
		`{"model":"m",,"store":false}`,
	}
	for _, fixture := range fixed {
		assertGrokStripMatchesLegacy(t, []byte(fixture))
	}
}

func randomGrokStripBody(r *rand.Rand, loose bool) string {
	ws := func() string {
		if loose && r.Intn(2) == 0 {
			return []string{" ", "\n", "\t"}[r.Intn(3)]
		}
		return ""
	}
	values := []string{`false`, `true`, `null`, `[]`, `["reasoning.encrypted_content"]`, `"resp_1"`, `"auto"`, `{"a":[1,2,{"store":1}]}`, `12`, `"x,y}"`}
	keys := []string{"store", "include", "previous_response_id", "service_tier", "model", "input", "tools", "prompt_cache_key", "stream", "Store", `store`}
	n := r.Intn(9)
	members := make([]string, 0, n)
	for i := 0; i < n; i++ {
		key := keys[r.Intn(len(keys))]
		members = append(members, `"`+key+`"`+ws()+`:`+ws()+values[r.Intn(len(values))])
	}
	sep := ","
	if loose && r.Intn(2) == 0 {
		sep = ws() + "," + ws()
	}
	body := `{` + ws() + strings.Join(members, sep) + ws() + `}`
	if loose && r.Intn(4) == 0 {
		body = " \n" + body + "\n"
	}
	if r.Intn(40) == 0 {
		body = body[:r.Intn(len(body)+1)]
	}
	return body
}

func TestStripGrokAPIKeyOpenAICompatFieldsMatchesLegacyRandomized(t *testing.T) {
	r := rand.New(rand.NewSource(7150))
	changed := 0
	for i := 0; i < 20000; i++ {
		body := []byte(randomGrokStripBody(r, i%2 == 1))
		assertGrokStripMatchesLegacy(t, body)
		if got, _ := stripGrokAPIKeyOpenAICompatFields(body); !bytes.Equal(got, body) {
			changed++
		}
	}
	require.Greater(t, changed, 4000, "随机语料应当大量覆盖会改写的输入")
}

func largeGrokStripBody(payloadBytes int) []byte {
	return []byte(`{"model":"grok-4","input":[{"role":"user","content":"` + strings.Repeat("x", payloadBytes) +
		`"}],"tools":[],"store":false,"stream":true,"include":["reasoning.encrypted_content"],"previous_response_id":"resp_1","service_tier":"auto","prompt_cache_key":"k"}`)
}

// 4 个字段都在、全部位于大 input 之后：旧实现做 4 次整体复制，新实现只分配结果那一份。
func TestStripGrokAPIKeyOpenAICompatFieldsAllocatesOnlyTheResult(t *testing.T) {
	body := largeGrokStripBody(2 << 20)
	var got []byte
	newBytes := allocatedBytesDuring(func() {
		var err error
		got, err = stripGrokAPIKeyOpenAICompatFields(body)
		require.NoError(t, err)
	})
	var want []byte
	legacyBytes := allocatedBytesDuring(func() {
		want, _ = legacyStripGrokAPIKeyOpenAICompatFields(body)
	})
	require.Equal(t, string(want), string(got))
	for _, field := range []string{"store", "include", "previous_response_id", "service_tier"} {
		require.False(t, gjson.GetBytes(got, field).Exists(), field)
	}
	require.Equal(t, "k", gjson.GetBytes(got, "prompt_cache_key").String())

	require.Equal(t, len(got), cap(got), "结果不应带 append 扩容留下的空闲容量")
	base := uintptr(unsafe.Pointer(unsafe.SliceData(body)))
	p := uintptr(unsafe.Pointer(unsafe.SliceData(got)))
	require.False(t, p >= base && p < base+uintptr(len(body)), "结果不应与入参共享内存")
	t.Logf("body=%d result=%d 一次剥离分配: legacy=%d current=%d", len(body), len(got), legacyBytes, newBytes)
	require.Less(t, newBytes, uint64(len(got))+64<<10, "只应分配结果那一份（new=%d legacy=%d body=%d）", newBytes, legacyBytes, len(body))
	require.Greater(t, legacyBytes, 3*uint64(len(body)), "旧实现应当有多份中间复制，说明度量有效")
}

func BenchmarkStripGrokAPIKeyOpenAICompatFields(b *testing.B) {
	body := largeGrokStripBody(256 << 10)
	for _, impl := range []struct {
		name string
		fn   func([]byte) ([]byte, error)
	}{
		{"legacy", legacyStripGrokAPIKeyOpenAICompatFields},
		{"current", stripGrokAPIKeyOpenAICompatFields},
	} {
		b.Run(impl.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			for i := 0; i < b.N; i++ {
				out, err := impl.fn(body)
				if err != nil || len(out) >= len(body) {
					b.Fatalf("strip failed: %v", err)
				}
				benchmarkOpenAIRawRewriteSink = out
			}
		})
	}
}
