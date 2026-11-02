package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newSiteThemeTestHandler(t *testing.T, stored map[string]string) (*SettingHandler, *settingHandlerRepoStub) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	repo := &settingHandlerRepoStub{values: stored}
	svc := service.NewSettingService(repo, &config.Config{})
	return NewSettingHandler(svc, nil, nil, nil, nil, nil, nil), repo
}

func doSiteThemeRequest(t *testing.T, h *SettingHandler, method, body string, userID int64) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(method, "/api/v1/admin/settings/theme", bytes.NewReader([]byte(body)))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: userID})
	c.Set(string(middleware.ContextKeyUserRole), "admin")
	if method == http.MethodGet {
		h.GetSiteTheme(c)
	} else {
		h.UpdateSiteTheme(c)
	}
	return rec
}

func decodeSiteTheme(t *testing.T, rec *httptest.ResponseRecorder) service.SiteThemeSettings {
	t.Helper()
	var resp struct {
		Code int                       `json:"code"`
		Data service.SiteThemeSettings `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	return resp.Data
}

func TestUpdateSiteThemeRequiresPrimaryAdmin(t *testing.T) {
	h, repo := newSiteThemeTestHandler(t, map[string]string{service.SettingKeyThemePrimary: "green"})
	rec := doSiteThemeRequest(t, h, http.MethodPut, `{"theme_primary":"red"}`, 2)
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Nil(t, repo.lastUpdates)
	require.Equal(t, "green", repo.values[service.SettingKeyThemePrimary])
}

func TestUpdateSiteThemePartialWriteKeepsOtherFields(t *testing.T) {
	h, repo := newSiteThemeTestHandler(t, map[string]string{
		service.SettingKeyThemeNeutral: "zinc",
		service.SettingKeyThemeFont:    "inter",
	})
	rec := doSiteThemeRequest(t, h, http.MethodPut, `{"theme_primary":"sakura","theme_radius":0,"theme_font":null}`, middleware.PrimaryAdminUserID)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, map[string]string{service.SettingKeyThemePrimary: "sakura", service.SettingKeyThemeRadius: "0"}, repo.lastUpdates)
	require.Equal(t, service.SiteThemeSettings{Primary: "sakura", Neutral: "zinc", Radius: 0, Font: "inter", DefaultMode: "system"}, decodeSiteTheme(t, rec))
}

func TestUpdateSiteThemeRejectsInvalidValues(t *testing.T) {
	for _, body := range []string{
		`{"theme_primary":"red;}*{display:none"}`,
		`{"theme_primary":"#ff0000"}`,
		`{"theme_neutral":"red"}`,
		`{"theme_radius":0.3}`,
		`{"theme_radius":"0.25"}`,
		`{"theme_radius":-1}`,
		`{"theme_font":"url(https://evil.example/f.woff2)"}`,
		`{"theme_default_mode":"auto"}`,
		`{"theme_primary":"blue","theme_default_mode":"night"}`,
		`{"theme_primary":7}`,
		`not json`,
	} {
		h, repo := newSiteThemeTestHandler(t, map[string]string{service.SettingKeyThemePrimary: "green"})
		rec := doSiteThemeRequest(t, h, http.MethodPut, body, middleware.PrimaryAdminUserID)
		require.Equal(t, http.StatusBadRequest, rec.Code, body)
		require.Nil(t, repo.lastUpdates, body)
		require.Equal(t, "green", repo.values[service.SettingKeyThemePrimary], body)
	}
}

func TestGetSiteThemeDefaultsAndStoredValues(t *testing.T) {
	h, _ := newSiteThemeTestHandler(t, map[string]string{})
	rec := doSiteThemeRequest(t, h, http.MethodGet, "", middleware.PrimaryAdminUserID)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, service.DefaultSiteThemeSettings(), decodeSiteTheme(t, rec))

	h, _ = newSiteThemeTestHandler(t, map[string]string{
		service.SettingKeyThemePrimary:     "violet",
		service.SettingKeyThemeRadius:      "0.125",
		service.SettingKeyThemeDefaultMode: "light",
		service.SettingKeyThemeFont:        "noto-sans-sc",
		service.SettingKeyThemeNeutral:     "unknown",
	})
	rec = doSiteThemeRequest(t, h, http.MethodGet, "", middleware.PrimaryAdminUserID)
	require.Equal(t, service.SiteThemeSettings{Primary: "violet", Neutral: "neutral", Radius: 0.125, Font: "noto-sans-sc", DefaultMode: "light"}, decodeSiteTheme(t, rec))
}
