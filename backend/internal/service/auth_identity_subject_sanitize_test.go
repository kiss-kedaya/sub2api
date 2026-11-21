//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSanitizeAuthIdentityMetadataDropsSubjectDerivedKeys(t *testing.T) {
	in := map[string]any{
		"source":           "auth_service_dual_write",
		"display_name":     "octocat",
		"username":         "octocat",
		"subject":          "gh-123456",
		"provider_subject": "gh-123456",
		"unionid":          "union-1",
		"union_id":         "union-1",
		"openid":           "openid-1",
		"channel_subject":  "openid-1",
		"corp_user_id":     "staff-1",
		"email":            "user@example.com",
		"synthetic_email":  "gh-123456@example.invalid",
		"nested": map[string]any{
			"openid":       "nested-openid",
			"display_name": "nested-name",
		},
		"items": []any{
			map[string]any{"unionid": "x", "name": "keep"},
		},
	}

	out := SanitizeAuthIdentityMetadata(in)
	require.Equal(t, "octocat", out["display_name"])
	require.Equal(t, "octocat", out["username"])
	require.Equal(t, "auth_service_dual_write", out["source"])
	for _, key := range []string{
		"subject", "provider_subject", "unionid", "union_id", "openid",
		"channel_subject", "corp_user_id", "email", "synthetic_email",
	} {
		require.NotContains(t, out, key, "metadata key %s must be dropped", key)
	}
	nested, ok := out["nested"].(map[string]any)
	require.True(t, ok)
	require.NotContains(t, nested, "openid")
	require.Equal(t, "nested-name", nested["display_name"])
	items, ok := out["items"].([]any)
	require.True(t, ok)
	require.Len(t, items, 1)
	item, ok := items[0].(map[string]any)
	require.True(t, ok)
	require.NotContains(t, item, "unionid")
	require.Equal(t, "keep", item["name"])

	// 输入不能被原地修改（metadata 会被 merge 到别处复用）。
	require.Equal(t, "gh-123456", in["subject"])
	require.Equal(t, "user@example.com", in["email"])
}

func TestSanitizeOAuthIdentityClaimsKeepsFunctionalFieldsAndHashesChannelSubject(t *testing.T) {
	claims := map[string]any{
		"subject":                "union-1234567890",
		"provider_subject":       "union-1234567890",
		"unionid":                "union-1234567890",
		"openid":                 "openid-1234567890",
		"channel_subject":        "openid-1234567890",
		"channel":                "open",
		"channel_app_id":         "wx-open",
		"email":                  "user@example.com",
		"corp_user_id":           "staff-1",
		"primary_dept_id":        int64(3),
		"issuer":                 "https://issuer.example",
		"suggested_display_name": "WeChat Nick",
		"aff_code":               "aff-1",
	}

	out := SanitizeOAuthIdentityClaims("wechat", claims)
	for _, key := range []string{"subject", "provider_subject", "unionid", "openid", "channel_subject"} {
		require.NotContains(t, out, key, "claims key %s must be dropped", key)
	}
	// 功能字段必须保留，否则钉钉同步 / 完成登录会坏。
	require.Equal(t, "user@example.com", out["email"])
	require.Equal(t, "staff-1", out["corp_user_id"])
	require.Equal(t, int64(3), out["primary_dept_id"])
	require.Equal(t, "https://issuer.example", out["issuer"])
	require.Equal(t, "WeChat Nick", out["suggested_display_name"])
	require.Equal(t, "aff-1", out["aff_code"])
	require.Equal(t, "open", out["channel"])
	require.Equal(t, "wx-open", out["channel_app_id"])

	// channel_subject 换成 hash / 前缀 / identity 域 hash 三件套。
	require.Equal(t, HashAuthChannelSubject("wechat", "openid-1234567890"), out["channel_subject_hash"])
	require.Equal(t, AuthSubjectPrefixFromHash(HashAuthChannelSubject("wechat", "openid-1234567890")), out["channel_subject_prefix"])
	require.Equal(t, HashAuthIdentitySubject("wechat", "openid-1234567890"), out["channel_subject_identity_hash"])
	sealed, ok := out["channel_subject_sealed"].(string)
	require.True(t, ok)
	require.True(t, IsAuthSubjectSealed(sealed))
	require.Equal(t, "openid-1234567890", PendingAuthSubjectPlaintext(sealed))
}
