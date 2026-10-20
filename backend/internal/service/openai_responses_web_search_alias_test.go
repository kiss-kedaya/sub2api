package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestNormalizeOpenAIResponsesLegacyWebSearchToolType(t *testing.T) {
	t.Run("tools 里的 preview 改成 web_search", func(t *testing.T) {
		body := []byte(`{"model":"gpt-6.1-sol","tools":[{"type":"web_search_preview"}]}`)
		out, changed, err := normalizeOpenAIResponsesLegacyWebSearchToolType(body)
		require.NoError(t, err)
		require.True(t, changed)
		require.Equal(t, "web_search", gjson.GetBytes(out, "tools.0.type").String())
		require.Equal(t, "gpt-6.1-sol", gjson.GetBytes(out, "model").String())
	})

	t.Run("带日期后缀的变体同样命中", func(t *testing.T) {
		body := []byte(`{"tools":[{"type":"web_search_preview_2025_03_11"}]}`)
		out, changed, err := normalizeOpenAIResponsesLegacyWebSearchToolType(body)
		require.NoError(t, err)
		require.True(t, changed)
		require.Equal(t, "web_search", gjson.GetBytes(out, "tools.0.type").String())
	})

	t.Run("混合工具只改 preview 那一条", func(t *testing.T) {
		body := []byte(`{"tools":[{"type":"function","name":"calc"},{"type":"web_search_preview"},{"type":"web_search"}]}`)
		out, changed, err := normalizeOpenAIResponsesLegacyWebSearchToolType(body)
		require.NoError(t, err)
		require.True(t, changed)
		require.Equal(t, "function", gjson.GetBytes(out, "tools.0.type").String())
		require.Equal(t, "calc", gjson.GetBytes(out, "tools.0.name").String())
		require.Equal(t, "web_search", gjson.GetBytes(out, "tools.1.type").String())
		require.Equal(t, "web_search", gjson.GetBytes(out, "tools.2.type").String())
	})

	t.Run("tool_choice 对象里的 preview 也改", func(t *testing.T) {
		body := []byte(`{"tools":[{"type":"web_search_preview"}],"tool_choice":{"type":"web_search_preview"}}`)
		out, changed, err := normalizeOpenAIResponsesLegacyWebSearchToolType(body)
		require.NoError(t, err)
		require.True(t, changed)
		require.Equal(t, "web_search", gjson.GetBytes(out, "tool_choice.type").String())
	})

	t.Run("已经是新名时字节级 no-op", func(t *testing.T) {
		body := []byte(`{"tools":[{"type":"web_search"}]}`)
		out, changed, err := normalizeOpenAIResponsesLegacyWebSearchToolType(body)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, string(body), string(out))
	})

	t.Run("没有 preview 字面量时快速返回", func(t *testing.T) {
		body := []byte(`{"model":"gpt-6.1-sol","input":"hi"}`)
		out, changed, err := normalizeOpenAIResponsesLegacyWebSearchToolType(body)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, string(body), string(out))
	})

	t.Run("空 body 安全", func(t *testing.T) {
		out, changed, err := normalizeOpenAIResponsesLegacyWebSearchToolType(nil)
		require.NoError(t, err)
		require.False(t, changed)
		require.Nil(t, out)
	})
}
