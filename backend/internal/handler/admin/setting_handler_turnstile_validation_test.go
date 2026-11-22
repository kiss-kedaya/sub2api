//go:build unit

package admin

import (
	"context"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Turnstile 保存语义：site key 与 secret key 都以 sentFields 区分「未提交」与
// 「显式提交空值」。未提交的字段回退已存值，不能因为前端不回显/省略字段就把一次
// 正常保存拒绝掉（线上曾因此报 "Turnstile Site Key is required when enabled"）。

type recordingTurnstileVerifier struct {
	secretKeys []string
	result     *service.TurnstileVerifyResponse
	err        error
}

func (v *recordingTurnstileVerifier) VerifyToken(ctx context.Context, secretKey, token, remoteIP string) (*service.TurnstileVerifyResponse, error) {
	v.secretKeys = append(v.secretKeys, secretKey)
	if v.err != nil {
		return nil, v.err
	}
	if v.result != nil {
		return v.result, nil
	}
	// invalid-input-response 说明 secret 本身有效，ValidateSecretKey 视为通过。
	return &service.TurnstileVerifyResponse{Success: false, ErrorCodes: []string{"invalid-input-response"}}, nil
}

func newTurnstileValidationTestHandler(t *testing.T, stored map[string]string) (*SettingHandler, *settingHandlerRepoStub, *recordingTurnstileVerifier) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	repo := &settingHandlerRepoStub{values: stored}
	settingService := service.NewSettingService(repo, &config.Config{Default: config.DefaultConfig{UserConcurrency: 5}})
	verifier := &recordingTurnstileVerifier{}
	handler := NewSettingHandler(settingService, nil, service.NewTurnstileService(settingService, verifier), nil, nil, nil, nil)
	return handler, repo, verifier
}

// 未提交 turnstile_site_key（库里已有值）：回退存量 site key，保存成功且仍启用。
func TestUpdateSettingsTurnstileOmittedSiteKeyKeepsStoredValue(t *testing.T) {
	h, repo, verifier := newTurnstileValidationTestHandler(t, map[string]string{
		service.SettingKeyTurnstileEnabled:   "true",
		service.SettingKeyTurnstileSiteKey:   "0x4AAAAAAFQKGGsR8fNgg06W",
		service.SettingKeyTurnstileSecretKey: "stored-secret",
	})

	rec := doUpdateSettings(t, h, map[string]any{
		"turnstile_enabled":    true,
		"turnstile_secret_key": "stored-secret",
	}, nil)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "true", repo.values[service.SettingKeyTurnstileEnabled])
	require.Equal(t, "0x4AAAAAAFQKGGsR8fNgg06W", repo.values[service.SettingKeyTurnstileSiteKey])
	require.Empty(t, verifier.secretKeys, "site/secret 都没有变化时不应打 Cloudflare 校验接口")
}

// 显式提交空 site key = 清空意图：开启状态下必须拒绝，避免把站点拦成无法登录。
func TestUpdateSettingsTurnstileExplicitEmptySiteKeyRejected(t *testing.T) {
	h, repo, _ := newTurnstileValidationTestHandler(t, map[string]string{
		service.SettingKeyTurnstileEnabled:   "true",
		service.SettingKeyTurnstileSiteKey:   "0x4AAAAAAFQKGGsR8fNgg06W",
		service.SettingKeyTurnstileSecretKey: "stored-secret",
	})

	rec := doUpdateSettings(t, h, map[string]any{
		"turnstile_enabled":    true,
		"turnstile_site_key":   "",
		"turnstile_secret_key": "stored-secret",
	}, nil)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "Turnstile Site Key is required when enabled")
	require.Equal(t, "0x4AAAAAAFQKGGsR8fNgg06W", repo.values[service.SettingKeyTurnstileSiteKey])
}

// 显式提交空 secret key 同样按清空意图处理：开启状态下报 Secret Key 缺失。
func TestUpdateSettingsTurnstileExplicitEmptySecretKeyRejected(t *testing.T) {
	h, _, _ := newTurnstileValidationTestHandler(t, map[string]string{
		service.SettingKeyTurnstileEnabled:   "true",
		service.SettingKeyTurnstileSiteKey:   "0x4AAAAAAFQKGGsR8fNgg06W",
		service.SettingKeyTurnstileSecretKey: "stored-secret",
	})

	rec := doUpdateSettings(t, h, map[string]any{
		"turnstile_enabled":    true,
		"turnstile_site_key":   "0x4AAAAAAFQKGGsR8fNgg06W",
		"turnstile_secret_key": "",
	}, nil)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "Turnstile Secret Key is required when enabled")
}

// 请求未提交字段且库里也没有：仍然报 Site Key 缺失。
func TestUpdateSettingsTurnstileEnabledWithoutAnyKeysRejected(t *testing.T) {
	h, _, _ := newTurnstileValidationTestHandler(t, map[string]string{})

	rec := doUpdateSettings(t, h, map[string]any{"turnstile_enabled": true}, nil)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "Turnstile Site Key is required when enabled")
}

// 只带 secret 不带 site：使用存量 site key，保存成功；secret 变化需要校验一次。
func TestUpdateSettingsTurnstileSecretOnlyUsesStoredSiteKey(t *testing.T) {
	h, repo, verifier := newTurnstileValidationTestHandler(t, map[string]string{
		service.SettingKeyTurnstileEnabled:   "true",
		service.SettingKeyTurnstileSiteKey:   "0x4AAAAAAFQKGGsR8fNgg06W",
		service.SettingKeyTurnstileSecretKey: "stored-secret",
	})

	rec := doUpdateSettings(t, h, map[string]any{
		"turnstile_enabled":    true,
		"turnstile_secret_key": "rotated-secret",
	}, nil)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "0x4AAAAAAFQKGGsR8fNgg06W", repo.values[service.SettingKeyTurnstileSiteKey])
	require.Equal(t, "rotated-secret", repo.values[service.SettingKeyTurnstileSecretKey])
	require.Equal(t, []string{"rotated-secret"}, verifier.secretKeys)
}

// 关闭状态下显式清空 site key 允许：只有启用状态才要求必填。
func TestUpdateSettingsTurnstileClearSiteKeyAllowedWhenDisabled(t *testing.T) {
	h, repo, verifier := newTurnstileValidationTestHandler(t, map[string]string{
		service.SettingKeyTurnstileEnabled:   "false",
		service.SettingKeyTurnstileSiteKey:   "0x4AAAAAAFQKGGsR8fNgg06W",
		service.SettingKeyTurnstileSecretKey: "stored-secret",
	})

	rec := doUpdateSettings(t, h, map[string]any{
		"turnstile_enabled":  false,
		"turnstile_site_key": "",
	}, nil)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "", repo.values[service.SettingKeyTurnstileSiteKey])
	require.Empty(t, verifier.secretKeys)
}

// turnstile_enabled 被省略（库里仍为启用）时，显式清空 site key 同样必须拒绝，
// 否则会出现「已启用但没有 site key」的不可用状态。
func TestUpdateSettingsTurnstileClearSiteKeyRejectedWhenEnabledFlagOmitted(t *testing.T) {
	h, repo, _ := newTurnstileValidationTestHandler(t, map[string]string{
		service.SettingKeyTurnstileEnabled:   "true",
		service.SettingKeyTurnstileSiteKey:   "0x4AAAAAAFQKGGsR8fNgg06W",
		service.SettingKeyTurnstileSecretKey: "stored-secret",
	})

	rec := doUpdateSettings(t, h, map[string]any{"turnstile_site_key": ""}, nil)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "Turnstile Site Key is required when enabled")
	require.Equal(t, "0x4AAAAAAFQKGGsR8fNgg06W", repo.values[service.SettingKeyTurnstileSiteKey])
}
