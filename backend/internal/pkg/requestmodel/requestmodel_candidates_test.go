package requestmodel

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// buildModelBody 造一个「顶层 model + 大段填充 + session.model」的真实形状请求体。
func buildModelBody(targetSize int, model string) []byte {
	var sb strings.Builder
	// strings.Builder 的写入永不失败，但 errcheck 要求显式忽略返回值。
	_, _ = sb.WriteString(`{"model":"` + model + `","session":{"model":"` + model + `"},"input":[`)
	filler := `{"role":"user","content":"` + strings.Repeat("x", 512) + `"},`
	for sb.Len() < targetSize {
		_, _ = sb.WriteString(filler)
	}
	_, _ = sb.WriteString(`{"role":"user","content":"done"}]}`)
	return []byte(sb.String())
}

// gjson v1.18.0 的 ParseBytes 等价于 Parse(string(json))，会**把整份 body 复制成
// string**。修复前 FromBodyCandidates 分别调两个候选函数、各解析一次，于是每个
// /v1 POST 白白复制两份（生产 pprof 实测 gjson.ParseBytes 分配 4.97 GB / 10.6%）。
//
// 这条测试用「调用期间的总分配字节数」锁住修复：旧实现面对 8 MB body 至少要
// 分配 8 MB 以上，新实现只分配候选字符串本身。
func TestFromBodyCandidatesDoesNotCopyWholeBody(t *testing.T) {
	const bodySize = 8 << 20 // 8 MB

	body := buildModelBody(bodySize, "claude-sonnet-5")
	require.Greater(t, len(body), bodySize, "precondition: the fixture must exceed the target size")

	got := FromBodyCandidates("", "application/json", body)
	require.Equal(t, []string{"claude-sonnet-5"}, got)

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_ = FromBodyCandidates("", "application/json", body)
	runtime.ReadMemStats(&after)

	allocated := after.TotalAlloc - before.TotalAlloc
	// 允许解析器自身的少量临时分配，但绝不能是「复制整份 body」的量级。
	require.Less(t, allocated, uint64(256*1024),
		"解析 %d 字节的 body 分配了 %d 字节 —— 说明整份 body 又被复制了", len(body), allocated)
}

// 零拷贝解析的代价是：gjson 返回的字符串可能与入参 body 共享底层数组。
// 候选值会被上游做准入校验、可能跨请求存活，所以必须拷贝出来。
// 这条测试锁住 strings.Clone。
func TestFromBodyCandidatesDoesNotAliasCallerBuffer(t *testing.T) {
	body := []byte(`{"model":"model-aaaa","session":{"model":"model-bbbb"}}`)
	got := FromBodyCandidates("", "application/json", body)
	require.Equal(t, []string{"model-aaaa"}, got)

	// 模拟调用方复用/改写同一块缓冲区。
	copy(body, []byte(`{"model":"ZZZZZZZZZZ","session":{"model":"ZZZZZZZZZZ"}}`))

	require.Equal(t, []string{"model-aaaa"}, got,
		"candidates must not change when the caller reuses the body buffer")
}

// 两个候选函数合并成一趟遍历后，各自的独立语义必须保持不变。
func TestJsonCandidateHelpersKeepTheirOwnSemantics(t *testing.T) {
	body := []byte(`{"Model":"caps","model":"lower","session":{"MODEL":"inner"},"other":1}`)

	require.Equal(t, []string{"caps", "lower"}, jsonModelCandidates(body))
	require.Equal(t, []string{"inner"}, jsonSessionModelCandidates(body))

	models, sessions := jsonModelAndSessionCandidates(body)
	require.Equal(t, []string{"caps", "lower"}, models)
	require.Equal(t, []string{"inner"}, sessions)
}

func TestJsonModelAndSessionCandidatesEdgeCases(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		models  []string
		session []string
	}{
		{"empty", ``, nil, nil},
		{"not an object", `[1,2,3]`, nil, nil},
		{"model is not a string", `{"model":123}`, nil, nil},
		{"blank model is skipped", `{"model":"   "}`, nil, nil},
		{"session is not an object", `{"session":"x"}`, nil, nil},
		{"session model not a string", `{"session":{"model":9}}`, nil, nil},
		{"duplicates preserved", `{"model":"a","model":"b"}`, []string{"a", "b"}, nil},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			models, session := jsonModelAndSessionCandidates([]byte(tc.body))
			require.Equal(t, tc.models, models)
			require.Equal(t, tc.session, session)
		})
	}
}

// 保留一条 benchmark，方便以后在真实形状的 body 上复核分配量。
func BenchmarkFromBodyCandidates(b *testing.B) {
	for _, size := range []int{1 << 10, 64 << 10, 1 << 20} {
		body := buildModelBody(size, "claude-sonnet-5")
		b.Run(fmt.Sprintf("body=%dKB", len(body)>>10), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			for i := 0; i < b.N; i++ {
				if got := FromBodyCandidates("", "application/json", body); len(got) == 0 {
					b.Fatal("no candidates")
				}
			}
		})
	}
}
