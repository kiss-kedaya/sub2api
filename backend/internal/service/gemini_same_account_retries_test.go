//go:build unit

package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// 同账号重试次数来自 gateway.max_same_account_retries_gemini，配置值为「不含首次
// 请求」的重试数，所以总尝试次数要 +1。默认 4 → 每号最多 5 次请求。
func TestGeminiMaxAttemptsPerAccount(t *testing.T) {
	svc := &GeminiMessagesCompatService{}

	// 未注入配置时与上游默认值一致。
	require.Equal(t, 5, svc.geminiMaxAttemptsPerAccount())

	for _, tc := range []struct {
		clampsToNew int
		want        int
	}{
		{0, 1},
		{1, 2},
		{4, 5},
		{10, 11},
	} {
		svc.cfg = &config.Config{Gateway: config.GatewayConfig{MaxSameAccountRetriesGemini: tc.clampsToNew}}
		require.Equal(t, tc.want, svc.geminiMaxAttemptsPerAccount(), "retries=%d", tc.clampsToNew)
	}

	// 越过校验的负值/超大值也要钳住，不能把循环条件变成 0 或无限。
	svc.cfg = &config.Config{Gateway: config.GatewayConfig{MaxSameAccountRetriesGemini: -3}}
	require.Equal(t, 1, svc.geminiMaxAttemptsPerAccount())
	svc.cfg = &config.Config{Gateway: config.GatewayConfig{MaxSameAccountRetriesGemini: 99}}
	require.Equal(t, 11, svc.geminiMaxAttemptsPerAccount())
}
