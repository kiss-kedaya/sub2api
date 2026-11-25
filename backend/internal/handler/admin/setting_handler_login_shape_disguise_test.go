package admin

import (
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/stretchr/testify/require"
)

// 登录首步凭据无效挑战稳定伪装开关（审计 A-P1-02）：默认关闭，省略=保持现值，显式 true/false 才改，
// 不需要 step-up（它不放宽任何校验，只改变失败分支的响应形状）。

func TestUpdateSettingsLoginShapeDisguiseDefaultsOff(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{})

	rec := doUpdateSettings(t, h, map[string]any{"registration_enabled": true}, nil)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "false", repo.values[service.SettingKeyLoginShapeDisguiseEnabled])
	require.Contains(t, rec.Body.String(), `"login_shape_disguise_enabled":false`)
}

func TestUpdateSettingsLoginShapeDisguiseExplicitToggle(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{})

	on := doUpdateSettings(t, h, map[string]any{"login_shape_disguise_enabled": true}, nil)
	require.Equal(t, http.StatusOK, on.Code, on.Body.String())
	require.Equal(t, "true", repo.values[service.SettingKeyLoginShapeDisguiseEnabled])
	require.Contains(t, on.Body.String(), `"login_shape_disguise_enabled":true`)

	off := doUpdateSettings(t, h, map[string]any{"login_shape_disguise_enabled": false}, nil)
	require.Equal(t, http.StatusOK, off.Code, off.Body.String())
	require.Equal(t, "false", repo.values[service.SettingKeyLoginShapeDisguiseEnabled])
	require.Contains(t, off.Body.String(), `"login_shape_disguise_enabled":false`)
}

// 省略字段=保持现值：旧前端 / 脚本用不含该字段的全量 payload 保存其它设置，不得把已打开的开关静默关掉。
func TestUpdateSettingsLoginShapeDisguiseOmittedKeepsStoredValue(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{
		service.SettingKeyLoginShapeDisguiseEnabled: "true",
	})

	rec := doUpdateSettings(t, h, map[string]any{"registration_enabled": true}, nil)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "true", repo.values[service.SettingKeyLoginShapeDisguiseEnabled])
	require.Contains(t, rec.Body.String(), `"login_shape_disguise_enabled":true`)
}
