//go:build unit

package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUserAuthIdentitySubjectHintOnlyExposesMaskedPrefix(t *testing.T) {
	const subject = "union-abcdef0123456789"

	// 已回填行：直接使用落库的 hash 前缀。
	require.Equal(t, "926fc83f***", userAuthIdentitySubjectHint(UserAuthIdentityRecord{
		ProviderType:          "wechat",
		ProviderSubjectPrefix: "926fc83f",
	}))
	// 过渡期未回填行：用明文现算 keyed hash 再取 hash 前缀，不展示 subject 首尾。
	hint := userAuthIdentitySubjectHint(UserAuthIdentityRecord{
		ProviderType:    "wechat",
		ProviderSubject: subject,
	})
	require.Equal(t, serviceMaskForTest(t, "wechat", subject), hint)
	require.NotContains(t, hint, subject)
	require.NotContains(t, hint, "union-")
	// 没有可展示信息时不输出 hint。
	require.Empty(t, userAuthIdentitySubjectHint(UserAuthIdentityRecord{}))
}

func TestUserAuthIdentityDisplayNameNeverReturnsRawSubject(t *testing.T) {
	const subject = "github-numeric-id-1234567890"

	record := UserAuthIdentityRecord{
		ProviderType:    "github",
		ProviderSubject: subject,
		Metadata:        map[string]any{},
	}
	require.Empty(t, userAuthIdentityDisplayName(record), "raw subject must not be used as display name")

	withMetadata := record
	withMetadata.Metadata = map[string]any{"display_name": "octocat"}
	require.Equal(t, "octocat", userAuthIdentityDisplayName(withMetadata))
}

func TestBuildProviderIdentitySummaryMasksSubject(t *testing.T) {
	const subject = "wechat-unionid-abcdef123456"
	service := &UserService{}
	user := &User{Email: "user@example.com"}

	summary := service.buildProviderIdentitySummary("wechat", user, []UserAuthIdentityRecord{{
		ProviderType:          "wechat",
		ProviderKey:           "wechat-main",
		ProviderSubjectPrefix: "deadbeef",
		Metadata:              map[string]any{"nickname": "wx-user"},
	}})
	require.True(t, summary.Bound)
	require.Equal(t, "wx-user", summary.DisplayName)
	require.Equal(t, "deadbeef***", summary.SubjectHint)
	require.NotContains(t, summary.SubjectHint, subject)
}

func TestBuildEmailIdentitySummaryDoesNotEchoProviderSubject(t *testing.T) {
	// 邮箱身份 subject 可能是用户邮箱；实现改成只用 metadata / users.email，
	// 避免明文 subject 在接口里被间接回显。
	service := &UserService{}
	user := &User{Email: "primary@example.com"}
	summary := service.buildEmailIdentitySummary(user, []UserAuthIdentityRecord{{
		ProviderType:    "email",
		ProviderKey:     "email",
		ProviderSubject: "legacy-subject@example.com",
		Metadata:        map[string]any{},
	}})
	require.True(t, summary.Bound)
	require.Equal(t, "primary@example.com", summary.DisplayName)
	require.True(t, strings.HasPrefix(summary.SubjectHint, "p***"))
	require.NotContains(t, summary.SubjectHint, "legacy-subject")
}

// serviceMaskForTest 返回过渡期明文行期望的展示掩码（hash 前缀 + ***）。
func serviceMaskForTest(t *testing.T, providerType, subject string) string {
	t.Helper()
	return MaskAuthSubjectHash(HashAuthIdentitySubject(providerType, subject))
}
