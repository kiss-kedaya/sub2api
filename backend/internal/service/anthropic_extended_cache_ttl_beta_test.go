package service

import (
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestNeedsExtendedCacheTTLBeta(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"无 cache_control", `{"model":"claude-opus-5-5","messages":[]}`, false},
		{"仅 5m 断点", `{"system":[{"type":"text","text":"x","cache_control":{"type":"ephemeral","ttl":"5m"}}]}`, false},
		{"ephemeral 无 ttl", `{"system":[{"type":"text","text":"x","cache_control":{"type":"ephemeral"}}]}`, false},
		{"顶层 1h", `{"cache_control":{"type":"ephemeral","ttl":"1h"}}`, true},
		{"system 1h", `{"system":[{"type":"text","text":"x","cache_control":{"type":"ephemeral","ttl":"1h"}}]}`, true},
		{"tools 1h", `{"tools":[{"name":"t","cache_control":{"type":"ephemeral","ttl":"1h"}}]}`, true},
		{"messages 内容块 1h", `{"messages":[{"role":"user","content":[{"type":"text","text":"x","cache_control":{"type":"ephemeral","ttl":"1h"}}]}]}`, true},
		{"字符串里出现 1h 但无断点", `{"messages":[{"role":"user","content":"提到 1h 这个词"}]}`, false},
		{"非 ephemeral 的 1h", `{"system":[{"type":"text","text":"x","cache_control":{"type":"persistent","ttl":"1h"}}]}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, requestNeedsExtendedCacheTTLBeta([]byte(tc.body)))
		})
	}
}

func TestEnsureExtendedCacheTTLBeta(t *testing.T) {
	body1h := []byte(`{"system":[{"type":"text","text":"x","cache_control":{"type":"ephemeral","ttl":"1h"}}]}`)
	body5m := []byte(`{"system":[{"type":"text","text":"x","cache_control":{"type":"ephemeral","ttl":"5m"}}]}`)

	t.Run("需要时追加", func(t *testing.T) {
		got := ensureExtendedCacheTTLBeta("claude-code-20250219", body1h, nil)
		assert.Equal(t, "claude-code-20250219,"+claude.BetaExtendedCacheTTL, got)
	})

	t.Run("头为空时直接返回 token", func(t *testing.T) {
		assert.Equal(t, claude.BetaExtendedCacheTTL, ensureExtendedCacheTTLBeta("", body1h, nil))
	})

	t.Run("已存在不重复", func(t *testing.T) {
		in := "claude-code-20250219," + claude.BetaExtendedCacheTTL
		assert.Equal(t, in, ensureExtendedCacheTTLBeta(in, body1h, nil))
	})

	t.Run("drop 集合命中则不追加", func(t *testing.T) {
		drop := map[string]struct{}{claude.BetaExtendedCacheTTL: {}}
		assert.Equal(t, "claude-code-20250219", ensureExtendedCacheTTLBeta("claude-code-20250219", body1h, drop))
	})

	t.Run("不需要时一字不动", func(t *testing.T) {
		assert.Equal(t, "claude-code-20250219", ensureExtendedCacheTTLBeta("claude-code-20250219", body5m, nil))
	})
}

func TestApplyExtendedCacheTTLBetaHeader(t *testing.T) {
	body1h := []byte(`{"system":[{"type":"text","text":"x","cache_control":{"type":"ephemeral","ttl":"1h"}}]}`)

	t.Run("客户端未带头时写入", func(t *testing.T) {
		h := http.Header{}
		applyExtendedCacheTTLBetaHeader(h, body1h)
		assert.Equal(t, claude.BetaExtendedCacheTTL, getHeaderRaw(h, "anthropic-beta"))
	})

	t.Run("已有其它 beta 时追加而不是替换", func(t *testing.T) {
		h := http.Header{}
		h.Set("anthropic-beta", "claude-code-20250219,oauth-2025-04-20")
		applyExtendedCacheTTLBetaHeader(h, body1h)
		got := getHeaderRaw(h, "anthropic-beta")
		require.NotEmpty(t, got)
		assert.Contains(t, got, "claude-code-20250219")
		assert.Contains(t, got, "oauth-2025-04-20")
		assert.Contains(t, got, claude.BetaExtendedCacheTTL)
	})

	t.Run("非规范大小写的多行合并后追加", func(t *testing.T) {
		// 真实客户端走 textproto 会被规范化，只有直接构造 map 才可能出现多写法；
		// anthropicBetaHeaderValues 正是要覆盖这种情形。
		h := http.Header{
			"anthropic-beta": {"claude-code-20250219"},
			"Anthropic-Beta": {"oauth-2025-04-20"},
		}
		applyExtendedCacheTTLBetaHeader(h, body1h)
		got := getHeaderRaw(h, "anthropic-beta")
		require.NotEmpty(t, got)
		assert.Contains(t, got, "claude-code-20250219")
		assert.Contains(t, got, "oauth-2025-04-20")
		assert.Contains(t, got, claude.BetaExtendedCacheTTL)
		assert.Len(t, h, 1, "多行应收成一个 key")
	})

	t.Run("无 1h 断点时不动头", func(t *testing.T) {
		h := http.Header{}
		h.Set("anthropic-beta", "claude-code-20250219")
		applyExtendedCacheTTLBetaHeader(h, []byte(`{"system":[{"cache_control":{"type":"ephemeral","ttl":"5m"}}]}`))
		assert.Equal(t, "claude-code-20250219", getHeaderRaw(h, "anthropic-beta"))
	})
}
