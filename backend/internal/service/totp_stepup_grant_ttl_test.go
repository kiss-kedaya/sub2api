//go:build unit

package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// step-up 的免授权窗口必须与链上充值的 crypto grant 一致：5 分钟。
// 之前是 15 分钟，老板看到的「验证过一次，15 分钟内删用户都不输码」就是它。
func TestStepUpGrantTTLMatchesCryptoGrant(t *testing.T) {
	require.Equal(t, 5*time.Minute, StepUpGrantTTL)
	require.Equal(t, cryptoGrantTTL, StepUpGrantTTL,
		"step-up 窗口必须与 crypto grant 的 5 分钟保持一致")
	require.Less(t, StepUpGrantTTL, 15*time.Minute,
		"不允许回退到 15 分钟 sudo 窗口")
}
