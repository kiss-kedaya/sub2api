//go:build unit

package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 邮件投递台账按 SourceID 去重。旧实现用「用户 ID + UnixNano」：时钟精度不够的平台上
// （Windows 约 250 次里 11 次）同一用户两次重置会撞同一个键，第二封通知邮件被静默吞掉，
// 返回体却仍报 notification_queued=true。键里必须带随机成分，永不重复。
func TestAdmin2FAResetSourceIDNeverRepeats(t *testing.T) {
	const calls = 20000
	seen := make(map[string]struct{}, calls)
	for i := 0; i < calls; i++ {
		id := admin2FAResetSourceID(3249)
		require.True(t, strings.HasPrefix(id, "3249-"), id)
		_, duplicate := seen[id]
		require.False(t, duplicate, "source id %q repeated after %d calls", id, i)
		seen[id] = struct{}{}
	}
}

// 投递台账的键只保留 [a-z0-9_.-]：UUID（十六进制 + 连字符）必须原样通过，
// 否则不同的 SourceID 会被 safeNotificationEmailKeyPart 归并成同一个键。
func TestAdmin2FAResetSourceIDSurvivesTheDeliveryKeyNormalization(t *testing.T) {
	first, second := admin2FAResetSourceID(3249), admin2FAResetSourceID(3249)
	require.NotEqual(t, first, second)
	require.Equal(t, strings.ToLower(first), safeNotificationEmailKeyPart(first))
	require.NotEqual(t,
		notificationEmailDeliveryKey(NotificationEmailEventAdminSecurityTwoFactorReset, "admin_2fa_reset", first, "user@example.test", ""),
		notificationEmailDeliveryKey(NotificationEmailEventAdminSecurityTwoFactorReset, "admin_2fa_reset", second, "user@example.test", ""))
}
