//go:build unit

package repository

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBillingBalanceKey(t *testing.T) {
	tests := []struct {
		name     string
		userID   int64
		expected string
	}{
		{
			name:     "normal_user_id",
			userID:   123,
			expected: "billing:balance:123",
		},
		{
			name:     "zero_user_id",
			userID:   0,
			expected: "billing:balance:0",
		},
		{
			name:     "negative_user_id",
			userID:   -1,
			expected: "billing:balance:-1",
		},
		{
			name:     "max_int64",
			userID:   math.MaxInt64,
			expected: "billing:balance:9223372036854775807",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := billingBalanceKey(tc.userID)
			require.Equal(t, tc.expected, got)
		})
	}
}

func TestBillingSubKey(t *testing.T) {
	tests := []struct {
		name     string
		userID   int64
		groupID  int64
		expected string
	}{
		{
			name:     "normal_ids",
			userID:   123,
			groupID:  456,
			expected: "billing:sub:123:456",
		},
		{
			name:     "zero_ids",
			userID:   0,
			groupID:  0,
			expected: "billing:sub:0:0",
		},
		{
			name:     "negative_ids",
			userID:   -1,
			groupID:  -2,
			expected: "billing:sub:-1:-2",
		},
		{
			name:     "max_int64_ids",
			userID:   math.MaxInt64,
			groupID:  math.MaxInt64,
			expected: "billing:sub:9223372036854775807:9223372036854775807",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := billingSubKey(tc.userID, tc.groupID)
			require.Equal(t, tc.expected, got)
		})
	}
}

func TestJitteredTTL(t *testing.T) {
	const (
		minTTL = 4*time.Minute + 30*time.Second // 270s = 5min - 30s
		maxTTL = 5*time.Minute + 30*time.Second // 330s = 5min + 30s
	)

	for i := 0; i < 200; i++ {
		ttl := jitteredTTL()
		require.GreaterOrEqual(t, ttl, minTTL, "jitteredTTL() 返回值低于下限: %v", ttl)
		require.LessOrEqual(t, ttl, maxTTL, "jitteredTTL() 返回值超过上限: %v", ttl)
	}
}

func TestJitteredTTL_HasVariation(t *testing.T) {
	// 多次调用应该产生不同的值（验证抖动存在）
	seen := make(map[time.Duration]struct{}, 50)
	for i := 0; i < 50; i++ {
		seen[jitteredTTL()] = struct{}{}
	}
	// 50 次调用中应该至少有 2 个不同的值
	require.Greater(t, len(seen), 1, "jitteredTTL() 应产生不同的 TTL 值")
}

func TestParseSubscriptionCache_RejectsMalformedNumericFields(t *testing.T) {
	valid := map[string]string{
		subFieldStatus:       "active",
		subFieldExpiresAt:    "1760000000",
		subFieldDailyUsage:   "1.25",
		subFieldWeeklyUsage:  "2.5",
		subFieldMonthlyUsage: "3.75",
		subFieldVersion:      "42",
	}

	for _, field := range []string{subFieldDailyUsage, subFieldWeeklyUsage, subFieldMonthlyUsage} {
		t.Run(field, func(t *testing.T) {
			malformed := cloneStringMap(valid)
			malformed[field] = "not-a-number"
			_, err := (&billingCache{}).parseSubscriptionCache(malformed)
			require.Error(t, err)
		})
	}
}

func TestParseAPIKeyRateLimitCache_RejectsMissingOrNonFiniteFields(t *testing.T) {
	valid := map[string]string{
		rateLimitFieldUsage5h:  "1.25",
		rateLimitFieldUsage1d:  "2.5",
		rateLimitFieldUsage7d:  "3.75",
		rateLimitFieldWindow5h: "1760000000",
		rateLimitFieldWindow1d: "1760000000",
		rateLimitFieldWindow7d: "1760000000",
	}

	for _, field := range []string{rateLimitFieldUsage5h, rateLimitFieldUsage1d, rateLimitFieldUsage7d} {
		t.Run(field+"_nan", func(t *testing.T) {
			malformed := cloneStringMap(valid)
			malformed[field] = "NaN"
			_, err := parseAPIKeyRateLimitCache(malformed)
			require.Error(t, err)
		})
	}

	malformed := cloneStringMap(valid)
	delete(malformed, rateLimitFieldWindow7d)
	_, err := parseAPIKeyRateLimitCache(malformed)
	require.Error(t, err)
}

func cloneStringMap(input map[string]string) map[string]string {
	clone := make(map[string]string, len(input))
	for key, value := range input {
		clone[key] = value
	}
	return clone
}
