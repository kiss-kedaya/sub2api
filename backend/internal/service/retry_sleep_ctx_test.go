//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Gemini 同账号重试之间的退避最长 16 秒。以前是不可取消的 time.Sleep：客户端早就断开了，
// 请求 goroutine 还要被卡到退避结束。现在必须响应 ctx 取消。
func TestSleepGeminiBackoffHonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(30*time.Millisecond, cancel)

	start := time.Now()
	sleepGeminiBackoff(ctx, 5) // 16 秒封顶的那一档

	require.Less(t, time.Since(start), time.Second, "a canceled request must not keep sleeping through the backoff")
}

func TestSleepGeminiBackoffStillWaitsWhenNotCanceled(t *testing.T) {
	start := time.Now()
	sleepGeminiBackoff(context.Background(), 1)
	require.GreaterOrEqual(t, time.Since(start), time.Duration(float64(geminiRetryBaseDelay)*0.7), "the first retry still waits roughly the base delay (+/-20% jitter)")
}
