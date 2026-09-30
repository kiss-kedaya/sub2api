//go:build unit

package service

import (
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func buildLargeGrokBody(targetSize int) []byte {
	var sb strings.Builder
	sb.WriteString(`{"model":"grok-4","input":[`)
	for sb.Len() < targetSize {
		sb.WriteString(`{"role":"user","content":"` + strings.Repeat("x", 400) + `"},`)
	}
	sb.WriteString(`{"role":"user","content":"done"}]}`)
	return []byte(sb.String())
}

// 回归：这条链上有三组「删除上游不支持的字段」，每组都先 gjson 探测存在性再删。
//
// 有人（包括我）会直觉认为探测是多余的——「sjson.DeleteBytes 对不存在的路径本来
// 就是无操作」。**语义上确实是，但它仍然整份解析并重新序列化 body**。而这些字段
// 在生产里绝大多数请求都不存在，于是把探测去掉等于给每个请求白加最多 9 次
// 解析+序列化+分配。实测（256 KB body、2 个字段）：
//
//	探测后删   95,796 ns   39,652 B/op
//	无条件删  268,906 ns  580,455 B/op   ← 慢 2.8 倍、多分配 14.6 倍
//
// 本测试是一条**棘轮**：锁住当前实测上限，只能变小不能变大。
//
// 需要说明的是，当前值本身就偏高，问题不在上面那三组探测，而在链尾的
// sanitizeGrokResponsesModelInput：它把整个 `input` 数组泛型解码成 []any
// （每个对象都是 map）再重新序列化，是 profile 里 5.17 GB / 11% 分配的主因。
// 1 MB body 实测分配约 27.5 份 body。理想值应在 2~3 份（一次必要的 model 重写
// 加少量固定开销）。把它降到那个量级需要改写该函数，属独立的专门一轮；
// 在此之前，这条棘轮保证现状不会继续恶化。
func TestPatchGrokResponsesBodyAllocationCeiling(t *testing.T) {
	const bodySize = 1 << 20
	// 实测约 27.5 倍；留一点余量以吸收构建/运行环境差异。
	const maxBodyCopies = 32

	body := buildLargeGrokBody(bodySize)
	require.Greater(t, len(body), bodySize, "precondition: fixture must exceed the target size")

	// 确认前提：这些字段一个都不在，所以那些删除分支都不该触发。
	for _, field := range []string{
		"prompt_cache_retention", "safety_identifier",
		"presence_penalty", "frequency_penalty", "stop", "logprobs", "top_logprobs",
	} {
		require.NotContains(t, string(body), field, "fixture must not contain %s", field)
	}

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	out, err := patchGrokResponsesBody(body, "grok-4.5") // 4.5 会走最多的删除列表
	require.NoError(t, err)
	runtime.ReadMemStats(&after)

	require.Contains(t, string(out), `"model":"grok-4.5"`, "the model rewrite must still happen")

	allocated := after.TotalAlloc - before.TotalAlloc
	copies := float64(allocated) / float64(len(body))
	require.Less(t, copies, float64(maxBodyCopies),
		"patchGrokResponsesBody 分配了 %.1f 份 body（%d 字节 / %d 字节）；"+
			"上限是 %d 份。请检查是否又出现了「字段不存在时逐字段整份重写」", copies, allocated, len(body), maxBodyCopies)
}
