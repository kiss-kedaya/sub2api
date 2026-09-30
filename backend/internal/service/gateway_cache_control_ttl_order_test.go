package service

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// 上游硬约束（2026-09-30 实测，6h 194 次，全部来自 apikey 中转账号 30761/30762）：
//
//	a ttl='1h' cache_control block must not come after a ttl='5m' cache_control
//	block. Note that blocks are processed in the following order: tools, system, messages.
//
// 省略 ttl 的 ephemeral 块走默认 5m，所以「只写 type 不写 ttl」也参与排序。
// 这些用例同时充当不变量校验：断言出站 body 里不存在「5m 之后还有 1h」。

// assertNoTTLOrderViolation 按上游的 tools → system → messages 顺序扫描出站 body，
// 一旦出现 「ephemeral 块（无 ttl 或 5m）之后又出现 ttl=1h」就失败。
func assertNoTTLOrderViolation(t *testing.T, body []byte) {
	t.Helper()

	seenShortTTL := ""
	check := func(cc gjson.Result) {
		if !cc.IsObject() || cc.Get("type").String() != "ephemeral" {
			return
		}
		ttl := cc.Get("ttl").String()
		switch ttl {
		case cacheTTLTarget1h:
			require.NotEqual(t, cacheTTLTarget5m, seenShortTTL,
				"出站 body 里 ttl=1h 的块跟在 5m/无 ttl 的块之后，上游会 400")
		case cacheTTLTarget5m, "":
			seenShortTTL = cacheTTLTarget5m
		}
	}

	walk := func(array gjson.Result, block func(gjson.Result)) {
		if !array.IsArray() {
			return
		}
		array.ForEach(func(_, item gjson.Result) bool {
			block(item)
			return true
		})
	}

	walk(gjson.GetBytes(body, "tools"), func(item gjson.Result) {
		check(item.Get("cache_control"))
	})
	walk(gjson.GetBytes(body, "system"), func(item gjson.Result) {
		check(item.Get("cache_control"))
	})
	walk(gjson.GetBytes(body, "messages"), func(msg gjson.Result) {
		walk(msg.Get("content"), func(block gjson.Result) {
			check(block.Get("cache_control"))
		})
	})
	check(gjson.GetBytes(body, "cache_control"))
}

// 真实形态：中转客户端把断点打在 tools 上却不写 ttl（默认 5m），
// 而 messages 的断点写了 1h —— 这正是生产上 400 的那一批。
func TestNormalizeCacheControlTTLOrder_PromotesToolsBeforeMessage1h(t *testing.T) {
	body := []byte(`{"tools":[{"name":"a","input_schema":{}},{"name":"b","input_schema":{},"cache_control":{"type":"ephemeral"}}],` +
		`"system":[{"type":"text","text":"sys","cache_control":{"type":"ephemeral","ttl":"5m"}}],` +
		`"messages":[{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral","ttl":"1h"}}]}]}`)

	out := normalizeCacheControlTTLOrder(body)

	// 最后一个 1h 之前的所有 ephemeral 块提升为 1h。
	require.Equal(t, "1h", gjson.GetBytes(out, "tools.1.cache_control.ttl").String())
	require.Equal(t, "1h", gjson.GetBytes(out, "system.0.cache_control.ttl").String())
	// 1h 块本身不动。
	require.Equal(t, "1h", gjson.GetBytes(out, "messages.0.content.0.cache_control.ttl").String())
	require.Equal(t, "ephemeral", gjson.GetBytes(out, "tools.1.cache_control.type").String())
	// 没有 cache_control 的 tool 不该凭空长出来。
	require.False(t, gjson.GetBytes(out, "tools.0.cache_control").Exists())
	require.False(t, gjson.GetBytes(out, "systems.0.cache_control.ttl").Exists())
	assertNoTTLOrderViolation(t, out)

	// 幂等：再跑一次不再变化。
	require.Equal(t, string(out), string(normalizeCacheControlTTLOrder(out)))
}

// 只提升到「第一个 1h」是不够的：后面还可能再出现 1h。
func TestNormalizeCacheControlTTLOrder_UsesLast1hNotFirst(t *testing.T) {
	body := []byte(`{"tools":[{"name":"a","input_schema":{},"cache_control":{"type":"ephemeral","ttl":"5m"}}],` +
		`"system":[{"type":"text","text":"s1","cache_control":{"type":"ephemeral","ttl":"1h"}},` +
		`{"type":"text","text":"s2","cache_control":{"type":"ephemeral","ttl":"5m"}}],` +
		`"messages":[{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral"}},` +
		`{"type":"text","text":"again","cache_control":{"type":"ephemeral","ttl":"1h"}}]}]}`)

	out := normalizeCacheControlTTLOrder(body)

	require.Equal(t, "1h", gjson.GetBytes(out, "tools.0.cache_control.ttl").String())
	require.Equal(t, "1h", gjson.GetBytes(out, "system.1.cache_control.ttl").String())
	require.Equal(t, "1h", gjson.GetBytes(out, "messages.0.content.0.cache_control.ttl").String())
	require.Equal(t, "1h", gjson.GetBytes(out, "messages.0.content.1.cache_control.ttl").String())
	assertNoTTLOrderViolation(t, out)
}

// 1h 排在前面、5m 排在后面本来就是合法顺序，一个字都不该改。
func TestNormalizeCacheControlTTLOrder_LeavesValidDescendingOrderAlone(t *testing.T) {
	body := []byte(`{"tools":[{"name":"a","input_schema":{},"cache_control":{"type":"ephemeral","ttl":"1h"}}],` +
		`"system":[{"type":"text","text":"sys","cache_control":{"type":"ephemeral","ttl":"1h"}}],` +
		`"messages":[{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral","ttl":"5m"}}]}]}`)

	out := normalizeCacheControlTTLOrder(body)

	require.Equal(t, string(body), string(out), "合法顺序必须字节级原样返回")
	assertNoTTLOrderViolation(t, out)
}

// 完全没有 1h 的请求走快路径（一次字节扫描），也必须字节级原样返回。
func TestNormalizeCacheControlTTLOrder_NoLongTTLIsUntouched(t *testing.T) {
	body := []byte(`{"tools":[{"name":"a","input_schema":{},"cache_control":{"type":"ephemeral"}}],` +
		`"system":[{"type":"text","text":"sys","cache_control":{"type":"ephemeral","ttl":"5m"}}],` +
		`"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)

	require.Equal(t, string(body), string(normalizeCacheControlTTLOrder(body)))
	require.Equal(t, `{}`, string(normalizeCacheControlTTLOrder([]byte(`{}`))))
	require.Nil(t, normalizeCacheControlTTLOrder(nil))
}

// 非 ephemeral 的 cache_control 不是缓存断点，既不参与判定也不该被改写。
func TestNormalizeCacheControlTTLOrder_IgnoresNonEphemeralBlocks(t *testing.T) {
	body := []byte(`{"tools":[{"name":"a","input_schema":{},"cache_control":{"type":"persistent","ttl":"5m"}}],` +
		`"messages":[{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral","ttl":"1h"}}]}]}`)

	out := normalizeCacheControlTTLOrder(body)

	require.Equal(t, `"persistent"`, gjson.GetBytes(out, "tools.0.cache_control.type").Raw)
	require.Equal(t, "5m", gjson.GetBytes(out, "tools.0.cache_control.ttl").String())
	assertNoTTLOrderViolation(t, out)
}

// 顶层 cache_control 位置不明，按最后一位处理：提升完的顺序无论上游怎么解读都合法。
func TestNormalizeCacheControlTTLOrder_TopLevelCountsAsLast(t *testing.T) {
	body := []byte(`{"cache_control":{"type":"ephemeral","ttl":"1h"},` +
		`"system":[{"type":"text","text":"sys","cache_control":{"type":"ephemeral","ttl":"5m"}}],` +
		`"messages":[{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral","ttl":"5m"}}]}]}`)

	out := normalizeCacheControlTTLOrder(body)

	require.Equal(t, "1h", gjson.GetBytes(out, "system.0.cache_control.ttl").String())
	require.Equal(t, "1h", gjson.GetBytes(out, "messages.0.content.0.cache_control.ttl").String())
	assertNoTTLOrderViolation(t, out)
}

// content 是字符串（非数组）时不能崩，也不能误判。
func TestNormalizeCacheControlTTLOrder_HandlesStringContent(t *testing.T) {
	body := []byte(`{"system":"plain","messages":[{"role":"user","content":"hi"}],` +
		`"tools":[{"name":"a","input_schema":{},"cache_control":{"type":"ephemeral"}}]}`)

	require.Equal(t, string(body), string(normalizeCacheControlTTLOrder(body)))
}

// 回归锁：与 +build unit 无关的通用断言——出站 body 不允许出现违规顺序。
func TestNormalizeCacheControlTTLOrder_KeepsOtherFieldsIntact(t *testing.T) {
	body := []byte(`{"model":"claude-sonnet-5-5","max_tokens":32000,"stream":true,` +
		`"system":[{"type":"text","text":"sys","cache_control":{"type":"ephemeral","ttl":"5m"}}],` +
		`"messages":[{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral","ttl":"1h"}}]}]}`)

	out := normalizeCacheControlTTLOrder(body)

	require.True(t, bytes.Contains(out, []byte(`"model":"claude-sonnet-5-5"`)))
	require.True(t, bytes.Contains(out, []byte(`"max_tokens":32000`)))
	require.True(t, bytes.Contains(out, []byte(`"stream":true`)))
	require.Equal(t, "sys", gjson.GetBytes(out, "system.0.text").String())
	require.Equal(t, "hi", gjson.GetBytes(out, "messages.0.content.0.text").String())
	assertNoTTLOrderViolation(t, out)
}
