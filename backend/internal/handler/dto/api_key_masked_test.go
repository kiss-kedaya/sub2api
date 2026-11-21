package dto

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestMaskAPIKeyFormat(t *testing.T) {
	cases := []struct {
		key    string
		prefix string
		masked string
	}{
		// 生成的 key 形如 sk- + 64 hex。
		{key: "sk-abcdefgh1234", prefix: "sk-abcde", masked: "sk-abcde****1234"},
		// 最短的自定义 key（16 位）：仍然只暴露前 8 后 4。
		{key: "abcdefgh12345678", prefix: "abcdefgh", masked: "abcdefgh****5678"},
		// 异常短/空数据不得回显任何可识别片段。
		{key: "sk-short", prefix: "", masked: "****"},
		{key: "", prefix: "", masked: ""},
	}
	for _, tc := range cases {
		require.Equal(t, tc.prefix, APIKeyPrefix(tc.key), "prefix of %q", tc.key)
		require.Equal(t, tc.masked, MaskAPIKey(tc.key), "masked of %q", tc.key)
	}
}

// 列表/详情/admin 列表/导出等所有普通出口都走 APIKeyFromService，
// 它不得序列化出任何 key 明文字段。
func TestAPIKeyFromServiceNeverSerializesPlaintext(t *testing.T) {
	plaintext := "sk-abcdefgh1234"
	out := APIKeyFromService(&service.APIKey{ID: 7, UserID: 8, Key: plaintext, Name: "prod"})
	require.NotNil(t, out)

	raw, err := json.Marshal(out)
	require.NoError(t, err)
	require.NotContains(t, string(raw), plaintext)
	require.NotContains(t, string(raw), `"key"`)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.NotContains(t, decoded, "key")
	require.Equal(t, "sk-abcde", decoded["key_prefix"])
	require.Equal(t, "sk-abcde****1234", decoded["key_masked"])
}

// 迁移 327 回填+清理明文之后，库里只剩 key_hash / key_prefix：
// 列表仍要显示前缀（前 8 + ****），且哈希本身也不得出现在响应里。
func TestAPIKeyFromServiceUsesStoredPrefixWhenPlaintextGone(t *testing.T) {
	storedHash := strings.Repeat("a", 64)
	out := APIKeyFromService(&service.APIKey{
		ID:        9,
		UserID:    1,
		KeyHash:   storedHash,
		KeyPrefix: "sk-abcde",
		Name:      "hash-only",
	})
	require.NotNil(t, out)
	require.Equal(t, "sk-abcde", out.KeyPrefix)
	require.Equal(t, "sk-abcde****", out.KeyMasked)

	raw := mustJSON(t, out)
	require.NotContains(t, raw, `"key"`)
	require.NotContains(t, raw, storedHash, "key_hash must never be serialized")
}

// 只有创建/轮换的当次回执允许带明文，且同时带脱敏字段供列表复用。
func TestAPIKeySecretFromServiceIsTheOnlyPlaintextExit(t *testing.T) {
	plaintext := "sk-abcdefgh1234"
	out := APIKeySecretFromService(&service.APIKey{ID: 7, UserID: 8, Key: plaintext, Name: "prod"})
	require.NotNil(t, out)

	raw, err := json.Marshal(out)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.Equal(t, plaintext, decoded["key"])
	require.Equal(t, "sk-abcde****1234", decoded["key_masked"])

	// 并回列表前必须显式丢弃明文。
	require.NotContains(t, mustJSON(t, out.ToAPIKey()), `"key"`)
}

func TestAPIKeySecretFromServiceNil(t *testing.T) {
	require.Nil(t, APIKeySecretFromService(nil))
	require.Nil(t, (*APIKeySecret)(nil).ToAPIKey())
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	return string(raw)
}

// 嵌套在用户 DTO 里的 keys 同样只能是脱敏视图（admin 用户详情/用户自身接口）。
func TestUserDTOAPIKeysAreMasked(t *testing.T) {
	plaintext := "sk-abcdefgh1234"
	user := UserFromService(&service.User{
		ID:      1,
		APIKeys: []service.APIKey{{ID: 2, UserID: 1, Key: plaintext}},
	})
	raw := mustJSON(t, user)
	require.NotContains(t, raw, plaintext)
	require.True(t, strings.Contains(raw, `"key_prefix"`))
	require.False(t, strings.Contains(raw, `"key":`))
}
