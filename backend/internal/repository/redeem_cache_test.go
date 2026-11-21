//go:build unit

package repository

import (
	"math"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestRedeemRateLimitKey(t *testing.T) {
	tests := []struct {
		name     string
		userID   int64
		expected string
	}{
		{
			name:     "normal_user_id",
			userID:   123,
			expected: "redeem:ratelimit:v2:123",
		},
		{
			name:     "zero_user_id",
			userID:   0,
			expected: "redeem:ratelimit:v2:0",
		},
		{
			name:     "negative_user_id",
			userID:   -1,
			expected: "redeem:ratelimit:v2:-1",
		},
		{
			name:     "max_int64",
			userID:   math.MaxInt64,
			expected: "redeem:ratelimit:v2:9223372036854775807",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := redeemRateLimitKey(tc.userID)
			require.Equal(t, tc.expected, got)
		})
	}
}

func TestRedeemLockKey(t *testing.T) {
	// 锁键必须是 sha256(明文) 而不是明文本身：卡密是可兑现凭据，明文进 Redis
	// key 会出现在 slowlog / MONITOR / 备份 / 运维截图里。
	// 同一张卡（含首尾空白）必须落到同一个锁键，不同卡必须不同。
	sameA := redeemLockKey("ABC123")
	sameB := redeemLockKey("  ABC123  ")
	require.Equal(t, sameA, sameB, "normalized plaintext must share one lock key")
	require.Equal(t, redeemLockKeyPrefix+service.HashRedeemCode("ABC123"), sameA)
	require.NotContains(t, sameA, "ABC123", "lock key must not contain the plaintext code")
	require.Len(t, sameA, len(redeemLockKeyPrefix)+service.RedeemCodeHashHexLen)

	other := redeemLockKey("ABC124")
	require.NotEqual(t, sameA, other)

	// 空卡密退化为固定字面量：查库阶段必然 404，不会放大可用性。
	require.Equal(t, redeemLockKeyPrefix+"empty", redeemLockKey(""))
	require.Equal(t, redeemLockKeyPrefix+"empty", redeemLockKey("   "))
}
