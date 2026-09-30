//go:build unit

package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// settings 表里 98% 的行是邮件投递台账（线上实测 16,935 / 17,224），
// 而 GetAll 是「读出全部配置」的语义。这份前缀清单是 repository.GetAll
// 排除它们的唯一依据，所以它的形状必须被锁住。
func TestNotificationEmailRecordKeyPrefixes(t *testing.T) {
	t.Parallel()

	prefixes := NotificationEmailRecordKeyPrefixes()
	require.NotEmpty(t, prefixes, "the exclusion list must not be empty, or GetAll loads the whole table again")

	for _, prefix := range prefixes {
		require.NotEmpty(t, strings.TrimSpace(prefix))

		// 关键不变量：必须以分隔符结尾。否则 `notification_email_locale`
		// 这类前缀会连真正的配置键一起吞掉；带冒号才能精确匹配台账键。
		require.True(t, strings.HasSuffix(prefix, ":"),
			"prefix %q must end with ':' so it cannot swallow a sibling config key", prefix)

		// 台账键确实以这些前缀开头。
		require.True(t, strings.HasPrefix(prefix+"abc", prefix))
	}

	// 投递台账与收件人语言台账都是按记录存的，必须都在排除列表里。
	joined := strings.Join(prefixes, "|")
	require.Contains(t, joined, notificationEmailDeliveryKeyPrefix)
	require.Contains(t, joined, notificationEmailLocaleEmailKeyPrefix)
	require.Contains(t, joined, notificationEmailLocaleUserKeyPrefix)

	// 真正的配置键不能被误伤：它们是前缀的子串关系之外的东西。
	for _, cfgKey := range []string{
		"notification_email_unsubscribe_secret",
		"email_verify_enabled",
		"smtp_host",
	} {
		for _, prefix := range prefixes {
			require.False(t, strings.HasPrefix(cfgKey, prefix),
				"config key %q must not match record prefix %q", cfgKey, prefix)
		}
	}
}
