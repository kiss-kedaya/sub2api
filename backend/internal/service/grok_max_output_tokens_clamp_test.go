//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// 上游对超过模型上限的 max_output_tokens 直接 400 拒掉整个请求：
//
//	Field 'max_output_tokens' must be at most 128000
//
// 线上实测该错误 12 小时 48 次（账号 30767 / platform grok / /v1/responses）。
// 客户端带超限值不是我们的错，但请求被拒是我们要承担的，所以在补丁链上夹住。
func TestPatchGrokResponsesBodyClampsOversizedMaxOutputTokens(t *testing.T) {
	t.Parallel()

	t.Run("oversized value is clamped to the cap", func(t *testing.T) {
		t.Parallel()

		body := []byte(`{"model":"grok-4.7","input":"hi","max_output_tokens":524288}`)
		out, err := patchGrokResponsesBody(body, "grok-4.7")
		require.NoError(t, err)
		require.Equal(t, int64(grokResponsesMaxOutputTokensCap), gjson.GetBytes(out, "max_output_tokens").Int())
	})

	t.Run("value within the cap is untouched", func(t *testing.T) {
		t.Parallel()

		body := []byte(`{"model":"grok-4.7","input":"hi","max_output_tokens":8000}`)
		out, err := patchGrokResponsesBody(body, "grok-4.7")
		require.NoError(t, err)
		require.Equal(t, int64(8000), gjson.GetBytes(out, "max_output_tokens").Int())
	})

	t.Run("value exactly at the cap is untouched", func(t *testing.T) {
		t.Parallel()

		body := []byte(`{"model":"grok-4.7","input":"hi","max_output_tokens":128000}`)
		out, err := patchGrokResponsesBody(body, "grok-4.7")
		require.NoError(t, err)
		require.Equal(t, int64(128000), gjson.GetBytes(out, "max_output_tokens").Int())
	})

	// 关键：不设置时**不能凭空加上**这个字段，否则等于替客户端改了输出预算。
	t.Run("absent field stays absent", func(t *testing.T) {
		t.Parallel()

		body := []byte(`{"model":"grok-4.7","input":"hi"}`)
		out, err := patchGrokResponsesBody(body, "grok-4.7")
		require.NoError(t, err)
		require.False(t, gjson.GetBytes(out, "max_output_tokens").Exists())
	})

	// 非数字不该被我们改写（保持原样交给上游判定）。
	t.Run("non-numeric value is left alone", func(t *testing.T) {
		t.Parallel()

		body := []byte(`{"model":"grok-4.7","input":"hi","max_output_tokens":"big"}`)
		out, err := patchGrokResponsesBody(body, "grok-4.7")
		require.NoError(t, err)
		require.Equal(t, "big", gjson.GetBytes(out, "max_output_tokens").String())
	})
}
