package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 阶段 B 默认值：第三方身份 subject 明文双写必须默认关闭，
// 否则新版本部署后仍会继续制造可逆的 subject 明文行。
func TestLoadAuthSubjectPlaintextDualWriteDefaultsToFalse(t *testing.T) {
	resetViperWithJWTSecret(t)
	cfg, err := Load()
	require.NoError(t, err)
	require.False(t, cfg.Security.AuthSubjectPlaintextDualWrite,
		"security.auth_subject_plaintext_dual_write must default to false")
}

// 过渡期开关：显式打开时恢复旧的双写行为，供旧二进制混跑/回滚窗口使用。
func TestLoadAuthSubjectPlaintextDualWriteEnvOverride(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("SECURITY_AUTH_SUBJECT_PLAINTEXT_DUAL_WRITE", "true")

	cfg, err := Load()
	require.NoError(t, err)
	require.True(t, cfg.Security.AuthSubjectPlaintextDualWrite)
}
