package apicompat

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestApplyAutomaticCacheControl(t *testing.T) {
	t.Run("system string gets breakpoint", func(t *testing.T) {
		in := `{"model":"m","cache_control":{"type":"ephemeral"},"system":"hello","messages":[{"role":"user","content":"hi"}]}`
		out := ApplyAutomaticCacheControl([]byte(in))
		// Automatic caching anchors on the last cacheable block, which is the
		// final message; the prefix (including system) is covered by it.
		require.Equal(t, "ephemeral", gjson.GetBytes(out, "messages.0.content.0.cache_control.type").String())
		require.Equal(t, "hello", gjson.GetBytes(out, "system").String())
		require.True(t, gjson.GetBytes(out, "cache_control").Exists(), "top-level intent is preserved for native upstreams")
	})

	t.Run("ttl is propagated", func(t *testing.T) {
		in := `{"cache_control":{"type":"ephemeral","ttl":"1h"},"system":[{"type":"text","text":"a"},{"type":"text","text":"b"}]}`
		out := ApplyAutomaticCacheControl([]byte(in))
		require.Equal(t, "1h", gjson.GetBytes(out, "system.1.cache_control.ttl").String())
		require.False(t, gjson.GetBytes(out, "system.0.cache_control").Exists())
	})

	t.Run("explicit block marker wins", func(t *testing.T) {
		in := `{"cache_control":{"type":"ephemeral"},"system":[{"type":"text","text":"a","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":"hi"}]}`
		out := ApplyAutomaticCacheControl([]byte(in))
		require.Equal(t, in, string(out))
	})

	t.Run("no top-level field is untouched", func(t *testing.T) {
		in := `{"system":"hello","messages":[{"role":"user","content":"hi"}]}`
		out := ApplyAutomaticCacheControl([]byte(in))
		require.Equal(t, in, string(out))
	})

	t.Run("non-ephemeral top-level is ignored", func(t *testing.T) {
		in := `{"cache_control":{"type":"persistent"},"system":"hello"}`
		out := ApplyAutomaticCacheControl([]byte(in))
		require.Equal(t, in, string(out))
	})

	t.Run("message block wins over system", func(t *testing.T) {
		in := `{"cache_control":{"type":"ephemeral"},"system":"sys","messages":[{"role":"user","content":[{"type":"text","text":"q"}]}]}`
		out := ApplyAutomaticCacheControl([]byte(in))
		require.Equal(t, "ephemeral", gjson.GetBytes(out, "messages.0.content.0.cache_control.type").String())
		require.False(t, gjson.GetBytes(out, "system.0.cache_control").Exists())
	})

	t.Run("thinking block is skipped", func(t *testing.T) {
		in := `{"cache_control":{"type":"ephemeral"},"messages":[{"role":"assistant","content":[{"type":"text","text":"a"},{"type":"thinking","thinking":"x"}]}]}`
		out := ApplyAutomaticCacheControl([]byte(in))
		require.Equal(t, "ephemeral", gjson.GetBytes(out, "messages.0.content.0.cache_control.type").String())
		require.False(t, gjson.GetBytes(out, "messages.0.content.1.cache_control").Exists())
	})

	t.Run("message string content is upgraded", func(t *testing.T) {
		in := `{"cache_control":{"type":"ephemeral"},"messages":[{"role":"user","content":"plain"}]}`
		out := ApplyAutomaticCacheControl([]byte(in))
		require.Equal(t, "text", gjson.GetBytes(out, "messages.0.content.0.type").String())
		require.Equal(t, "plain", gjson.GetBytes(out, "messages.0.content.0.text").String())
		require.Equal(t, "ephemeral", gjson.GetBytes(out, "messages.0.content.0.cache_control.type").String())
	})

	t.Run("tools fallback when no system or messages", func(t *testing.T) {
		in := `{"cache_control":{"type":"ephemeral"},"tools":[{"name":"a"},{"name":"b"}]}`
		out := ApplyAutomaticCacheControl([]byte(in))
		require.Equal(t, "ephemeral", gjson.GetBytes(out, "tools.1.cache_control.type").String())
	})

	t.Run("idempotent", func(t *testing.T) {
		in := `{"cache_control":{"type":"ephemeral"},"system":"hello","messages":[{"role":"user","content":"hi"}]}`
		once := ApplyAutomaticCacheControl([]byte(in))
		twice := ApplyAutomaticCacheControl(once)
		require.Equal(t, string(once), string(twice))
	})
}