package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSettingHandlerRechargeCenterToggle(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &settingHandlerRepoStub{values: map[string]string{service.SettingEnabledPaymentTypes: "epusdt"}}
	svc := service.NewSettingService(repo, &config.Config{})
	handler := NewSettingHandler(svc, nil, nil, nil, service.NewPaymentConfigService(nil, repo, nil), nil, nil)
	for _, enabled := range []bool{true, false} {
		body, err := json.Marshal(map[string]bool{"payment_recharge_center_enabled": enabled})
		require.NoError(t, err)
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", bytes.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		handler.UpdateSettings(c)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var response struct {
			Data map[string]any `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
		require.Equal(t, enabled, response.Data["payment_recharge_center_enabled"])
		require.Equal(t, "epusdt", repo.values[service.SettingEnabledPaymentTypes])

		getRec := httptest.NewRecorder()
		getCtx, _ := gin.CreateTestContext(getRec)
		getCtx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings", nil)
		handler.GetSettings(getCtx)
		require.Equal(t, http.StatusOK, getRec.Code, getRec.Body.String())
		require.NoError(t, json.Unmarshal(getRec.Body.Bytes(), &response))
		require.Equal(t, enabled, response.Data["payment_recharge_center_enabled"])
	}
}
