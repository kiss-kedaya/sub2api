//go:build unit

package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type legacyKeySettingsRepo struct {
	service.SettingRepository
	values      map[string]string
	writes      int
	lastDeleted string
}

func (r *legacyKeySettingsRepo) GetValue(_ context.Context, key string) (string, error) {
	if value, ok := r.values[key]; ok {
		return value, nil
	}
	return "", service.ErrSettingNotFound
}

func (r *legacyKeySettingsRepo) Set(_ context.Context, key, value string) error {
	r.writes++
	r.values[key] = value
	return nil
}

func (r *legacyKeySettingsRepo) Delete(_ context.Context, key string) error {
	r.writes++
	r.lastDeleted = key
	delete(r.values, key)
	return nil
}

func legacyKeyTestHandler(values map[string]string) (*SettingHandler, *legacyKeySettingsRepo) {
	repo := &legacyKeySettingsRepo{values: values}
	svc := service.NewSettingService(repo, &config.Config{})
	return NewSettingHandler(svc, nil, nil, nil, nil, nil, nil), repo
}

func withPrimaryAdminContext(c *gin.Context) {
	c.Request = c.Request.WithContext(service.WithAccountOwnerScope(c.Request.Context(), 1, 1))
}

// 旧版全权密钥已退役：regenerate 必须 403 且不生成/不写库，不返回任何 key 材料。
func TestRegenerateLegacyAdminAPIKeyIsRetired(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, repo := legacyKeyTestHandler(map[string]string{service.SettingKeyAdminAPIKey: "admin-0123456789abcdef0123456789abcdef"})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/settings/admin-api-key/regenerate", nil)
	withPrimaryAdminContext(c)

	h.RegenerateAdminAPIKey(c)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Contains(t, rec.Body.String(), "LEGACY_ADMIN_KEY_RETIRED")
	require.NotContains(t, rec.Body.String(), `"key"`)
	require.Zero(t, repo.writes)
	require.Equal(t, "admin-0123456789abcdef0123456789abcdef", repo.values[service.SettingKeyAdminAPIKey],
		"退役后的入口不得改写或清空已有旧 key")
}

// 读取状态接口保留，并明确告知旧 key 已退役、给出迁移提示。
func TestGetAdminAPIKeyStatusReportsRetirementAndHint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fullKey := "admin-0123456789abcdef0123456789abcdef"
	h, _ := legacyKeyTestHandler(map[string]string{service.SettingKeyAdminAPIKey: fullKey})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings/admin-api-key", nil)
	withPrimaryAdminContext(c)

	h.GetAdminAPIKey(c)

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, `"exists":true`)
	require.Contains(t, body, `"deprecated":true`)
	require.Contains(t, body, "migration_hint")
	require.NotContains(t, body, fullKey, "状态接口只能返回脱敏 key")
}

// 未配置旧 key 时状态接口保持 exists=false，不误导迁移。
func TestGetAdminAPIKeyStatusWithoutLegacyKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _ := legacyKeyTestHandler(map[string]string{})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings/admin-api-key", nil)
	withPrimaryAdminContext(c)

	h.GetAdminAPIKey(c)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"exists":false`)
}
