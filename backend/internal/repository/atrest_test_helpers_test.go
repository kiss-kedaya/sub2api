package repository

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// mustCredentialsCASParam 供 sqlmock 断言使用：返回与生产代码完全一致的
// {"plain":..., "sealed":...} 包装参数。
func mustCredentialsCASParam(t *testing.T, credentials map[string]any) string {
	t.Helper()
	value, err := credentialsCASParam(credentials)
	require.NoError(t, err)
	return value
}
