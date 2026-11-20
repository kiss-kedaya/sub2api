//go:build unit

package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// rectifierSettingsRepoStub 复用 settingHandlerRepoStub 的读语义，并补上 Set：
// 整流器配置走 Set（整份 JSON），而共享 stub 的 Set 是刻意 panic 的。
type rectifierSettingsRepoStub struct {
	*settingHandlerRepoStub
}

func (r *rectifierSettingsRepoStub) Set(_ context.Context, key, value string) error {
	if r.values == nil {
		r.values = map[string]string{}
	}
	r.values[key] = value
	return nil
}

func newRectifierSettingsTestHandler(t *testing.T, stored map[string]string) (*SettingHandler, *rectifierSettingsRepoStub) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	repo := &rectifierSettingsRepoStub{settingHandlerRepoStub: &settingHandlerRepoStub{values: stored}}
	svc := service.NewSettingService(repo, &config.Config{})
	return NewSettingHandler(svc, nil, nil, nil, nil, nil, nil), repo
}

func doGetRectifierSettings(t *testing.T, h *SettingHandler) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings/rectifier", nil)
	h.GetRectifierSettings(c)
	return rec
}

func doUpdateRectifierSettings(t *testing.T, h *SettingHandler, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	rawBody, err := json.Marshal(body)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings/rectifier", bytes.NewReader(rawBody))
	c.Request.Header.Set("Content-Type", "application/json")
	h.UpdateRectifierSettings(c)
	return rec
}

func rectifierResponseData(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var envelope struct {
		Code int            `json:"code"`
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope), "response body: %s", rec.Body.String())
	require.Equal(t, 0, envelope.Code, "response body: %s", rec.Body.String())
	return envelope.Data
}

// GET 默认值必须是 false：老库没有这个字段时，反序列化出来就是关闭。
func TestGetRectifierSettingsDefaultsAnthropicThinkingRewriteOff(t *testing.T) {
	h, _ := newRectifierSettingsTestHandler(t, map[string]string{})

	rec := doGetRectifierSettings(t, h)

	require.Equal(t, http.StatusOK, rec.Code)
	data := rectifierResponseData(t, rec)
	require.Equal(t, false, data["anthropic_thinking_rewrite_enabled"],
		"新字段默认必须关闭（原样透传）")
}

// PUT 保存后：响应回显 true、持久化 JSON 带新字段、重新 GET 仍是 true。
func TestUpdateRectifierSettingsPersistsAnthropicThinkingRewriteToggle(t *testing.T) {
	h, repo := newRectifierSettingsTestHandler(t, map[string]string{})

	rec := doUpdateRectifierSettings(t, h, map[string]any{
		"enabled":                            true,
		"thinking_signature_enabled":         true,
		"thinking_budget_enabled":            true,
		"apikey_signature_enabled":           false,
		"apikey_signature_patterns":          []string{},
		"anthropic_thinking_rewrite_enabled": true,
	})

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, true, rectifierResponseData(t, rec)["anthropic_thinking_rewrite_enabled"])

	var stored service.RectifierSettings
	require.NoError(t, json.Unmarshal([]byte(repo.values[service.SettingKeyRectifierSettings]), &stored))
	require.True(t, stored.AnthropicThinkingRewriteEnabled, "持久化 JSON 必须带上新字段")

	// 用同一份存储新建 handler，模拟重启后读回。
	h2, _ := newRectifierSettingsTestHandler(t, repo.values)
	require.Equal(t, true, rectifierResponseData(t, doGetRectifierSettings(t, h2))["anthropic_thinking_rewrite_enabled"])
}

// 同样要能关回去（回滚路径：把开关打开恢复旧行为，再关掉回到透传）。
func TestUpdateRectifierSettingsCanTurnAnthropicThinkingRewriteBackOff(t *testing.T) {
	h, repo := newRectifierSettingsTestHandler(t, map[string]string{
		service.SettingKeyRectifierSettings: `{"enabled":true,"thinking_signature_enabled":true,` +
			`"thinking_budget_enabled":true,"apikey_signature_enabled":false,` +
			`"apikey_signature_patterns":[],"anthropic_thinking_rewrite_enabled":true}`,
	})

	rec := doUpdateRectifierSettings(t, h, map[string]any{
		"enabled":                            true,
		"thinking_signature_enabled":         true,
		"thinking_budget_enabled":            true,
		"apikey_signature_enabled":           false,
		"apikey_signature_patterns":          []string{},
		"anthropic_thinking_rewrite_enabled": false,
	})

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, false, rectifierResponseData(t, rec)["anthropic_thinking_rewrite_enabled"])

	var stored service.RectifierSettings
	require.NoError(t, json.Unmarshal([]byte(repo.values[service.SettingKeyRectifierSettings]), &stored))
	require.False(t, stored.AnthropicThinkingRewriteEnabled)
}
